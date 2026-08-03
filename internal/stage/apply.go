package stage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/c360studio/semstreams/graph"
	semerrs "github.com/c360studio/semstreams/pkg/errs"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/effect"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// BatchStore is the durable home of the batch the applier was asked to commit.
type BatchStore interface {
	PutEffectBatch(ctx context.Context, turnEntityID string, batch *payload.EffectBatch) (content.Ref, error)
}

// DetailStore is the durable home of a failure's explanation.
type DetailStore interface {
	PutFailureDetail(ctx context.Context, turnEntityID string, detail *content.FailureDetail) (content.Ref, error)
}

// The claims above, enforced by the compiler rather than by doc comments.
var (
	_ BatchStore  = (*content.Store)(nil)
	_ DetailStore = (*content.Store)(nil)
)

// Effector is the stage that commits the selected band's effects.
type Effector struct {
	recorder PhaseRecorder
	failer   TurnFailer
	turns    TurnReader
	verdicts VerdictReader
	batches  BatchStore
	details  DetailStore
	applier  *effect.Applier
	logger   *slog.Logger
}

// EffectorOption configures an Effector stage.
type EffectorOption func(*Effector)

// WithEffectorLogger sets the stage's logger.
func WithEffectorLogger(logger *slog.Logger) EffectorOption {
	return func(e *Effector) {
		if logger != nil {
			e.logger = logger
		}
	}
}

// NewEffector builds the effect-application stage.
func NewEffector(
	recorder PhaseRecorder,
	failer TurnFailer,
	turns TurnReader,
	verdicts VerdictReader,
	batches BatchStore,
	details DetailStore,
	applier *effect.Applier,
	opts ...EffectorOption,
) (*Effector, error) {
	switch {
	case recorder == nil:
		return nil, errors.New("the effect stage requires a phase recorder")
	case failer == nil:
		return nil, errors.New(
			"the effect stage requires a turn failer; rejection is a normal path here, and a rejected batch " +
				"that did not end the turn would leave a player waiting on a stage that already refused")
	case turns == nil:
		return nil, errors.New("the effect stage requires a turn reader")
	case verdicts == nil:
		return nil, errors.New("the effect stage requires a verdict reader")
	case batches == nil:
		return nil, errors.New("the effect stage requires a batch store")
	case details == nil:
		return nil, errors.New("the effect stage requires a failure-detail store")
	case applier == nil:
		return nil, errors.New("the effect stage requires an effect applier")
	}
	stage := &Effector{
		recorder: recorder, failer: failer, turns: turns, verdicts: verdicts,
		batches: batches, details: details, applier: applier, logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(stage)
	}
	return stage, nil
}

// Phase implements Stage.
func (e *Effector) Phase() vocabulary.TurnPhase { return vocabulary.PhaseApplying }

// Run enters application, selects the committed band, and commits it.
//
// The band is ENGINE knowledge assembled here from two facts the turn already
// carries — the verdict's reported roll gate and, when the dice were consulted,
// the band they selected — never from anything a persona is asked to repeat.
//
// A rejection is a NORMAL outcome, not an exception: the applier classifies its
// refusals into closed codes, the explanation is stored, and the turn ends
// explicitly. A transient incomplete commit is different: it is returned to the
// durable runner while the turn remains in applying, and redelivery re-applies
// the same derived batch. An invalid or fatal classified commit cannot improve
// on redelivery, so it ends once under the closed incomplete-commit reason. What
// is also NOT normal is an uncoded failure — a corrupt turn record, a transport
// fault — and that is returned rather than recorded, because writing a game
// outcome onto a record whose readability the applier just disputed would state
// a verdict on evidence it does not have.
func (e *Effector) Run(ctx context.Context, trigger Trigger) error {
	run, err := enter(ctx, e.recorder, trigger, e.Phase())
	if err != nil {
		return err
	}
	if !run {
		return nil
	}

	state, err := readTurn(ctx, e.turns, trigger.TurnEntityID)
	if err != nil {
		return err
	}
	verdict, err := verdictOf(ctx, e.verdicts, state)
	if err != nil {
		return err
	}
	band, err := committedBand(state, verdict)
	if err != nil {
		return err
	}
	intents, err := verdict.Bands.For(band)
	if err != nil {
		return err
	}

	batch := payload.NewEffectBatch(trigger.TurnID, band, intents)
	ref, err := e.batches.PutEffectBatch(ctx, trigger.TurnEntityID, batch)
	if err != nil {
		return fmt.Errorf("store the effect batch for turn %s: %w", trigger.TurnEntityID, err)
	}

	outcome, applyErr := e.applier.Apply(ctx, batch, trigger.TurnEntityID, ref.String())
	if applyErr != nil {
		var incomplete *effect.CommitError
		if errors.As(applyErr, &incomplete) && semerrs.Classify(incomplete.Err) == semerrs.ErrorTransient {
			return fmt.Errorf(
				"effect batch commit for turn %s is incomplete; retry the applying stage: %w",
				trigger.TurnEntityID, applyErr)
		}
		return e.refuse(ctx, trigger, applyErr)
	}
	if !outcome.Applied {
		e.logger.Info("turn already recorded its effect batch; the world was left alone",
			"turn", trigger.TurnEntityID, "batch", outcome.BatchID)
	}
	return nil
}

// refuse ends the turn on a deterministic applier rejection or a nonretryable
// classified commit failure.
//
// The reason on the graph is a closed code and the explanation is a reference,
// which is the same discipline every other failure here follows — and the detail
// is stored FIRST, so a turn never points at an explanation nobody wrote. A
// detail that cannot be stored does not buy back the stall: the turn ends
// ref-lessly and the store's refusal is reported alongside.
func (e *Effector) refuse(ctx context.Context, trigger Trigger, applyErr error) error {
	reason, coded := effect.FailureReasonFor(applyErr)
	if !coded {
		return fmt.Errorf(
			"turn %s could not be applied and the failure is not a turn outcome: %w", trigger.TurnEntityID, applyErr)
	}

	detail := &content.FailureDetail{
		TurnID:  trigger.TurnID,
		Reason:  reason,
		Class:   content.FailureClassDeterministic,
		Message: applyErr.Error(),
	}
	var rejection *effect.RejectionError
	if errors.As(applyErr, &rejection) {
		detail.Target = rejection.Target
	}
	var commit *effect.CommitError
	if errors.As(applyErr, &commit) {
		detail.Target = commit.Target
		detail.Committed = commit.Committed
	}

	ref, storeErr := e.details.PutFailureDetail(ctx, trigger.TurnEntityID, detail)
	if storeErr != nil {
		e.logger.Error("the effect failure detail could not be stored; failing the turn without it",
			"turn", trigger.TurnEntityID, "reason", reason, "class", content.FailureClassDeterministic)
		ref = content.Ref{}
	}
	if _, failErr := e.failer.Fail(ctx, trigger.TurnID, trigger.TurnEntityID, reason, ref); failErr != nil {
		return errors.Join(
			fmt.Errorf("fail turn %s after %s: %w", trigger.TurnEntityID, reason, failErr), applyErr)
	}
	e.logger.Info("effect batch refused; the turn ended explicitly",
		"turn", trigger.TurnEntityID, "reason", reason, "class", content.FailureClassDeterministic)
	return nil
}

// committedBand is the outcome band whose intents the applier commits.
//
// The two coherence checks are the interesting part. A verdict that called for
// the dice and carries no recorded band means the applier was triggered before
// the roll landed; a verdict that DECLINED the dice and carries one means a roll
// happened that nothing authorised. Either way the wrong intents would be
// committed, and the world would afterwards disagree with the record of why —
// which is the exact drift this engine exists to detect, manufactured by the
// engine itself.
func committedBand(state *graph.EntityState, verdict *payload.Verdict) (vocabulary.OutcomeBand, error) {
	object, present, err := soleObject(state, vocabulary.TurnRollBand)
	if err != nil {
		return "", err
	}

	switch {
	case verdict.Scalars.RequiresRoll && !present:
		return "", fmt.Errorf(
			"turn %s carries a verdict that requires a roll and records no %s; the applier commits the band "+
				"the dice selected, and this turn has not been resolved yet", state.ID, vocabulary.TurnRollBand)
	case !verdict.Scalars.RequiresRoll && present:
		return "", fmt.Errorf(
			"turn %s carries a verdict that declined the dice and records %s anyway; the engine never rolls a "+
				"turn the adjudicator did not send to the dice", state.ID, vocabulary.TurnRollBand)
	case !verdict.Scalars.RequiresRoll:
		return vocabulary.BandAuto, nil
	}

	value, ok := object.(string)
	if !ok {
		return "", fmt.Errorf("turn %s records a %T roll band, want a string", state.ID, object)
	}
	band, err := vocabulary.ParseOutcomeBand(value)
	if err != nil {
		return "", err
	}
	if !band.IsRollBand() {
		return "", fmt.Errorf(
			"turn %s records roll band %q, which the dice cannot select; %q belongs to a verdict that never "+
				"reached them", state.ID, band, vocabulary.BandAuto)
	}
	return band, nil
}
