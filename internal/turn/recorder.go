package turn

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Source is the triple Source stamped on every phase fact the recorder writes.
const Source = "turn-recorder"

// EntityMessageType is the provenance envelope stamped on a turn entity.
//
// It is stamped explicitly because upstream treats a zero or invalid envelope as
// NOT-YET-REALLY-BORN. graph.EntityState.IsStub keys on the stub envelope
// specifically, so a typeless entity does not read as a stub — but every
// re-stamping lane refuses to treat one as a real birth
// (restampStubOnCreate: "!IsValid() rejects a zero/partial type"), which is how
// a typeless entity ends up in the dispatchable-non-real class upstream named in
// gh#429. The turn entity is precisely the thing every stage guard asks about,
// so it is born with its own type or not at all.
//
// No message of this type is ever published — the turn is created through the
// atomic mutation lane and advanced through the merge lane — so it is
// deliberately absent from the payload registry.
var EntityMessageType = message.Type{
	Domain:   payload.Domain,
	Category: payload.CategoryTurnState,
	Version:  payload.SchemaVersion,
}

// Store is the graph surface the recorder needs.
//
// The write methods are the atomic-create lane and the merge lane, and nothing
// else. There is no seam here for triple.add or .add_batch: the phase is
// single-valued, and a phase committed through an appending lane leaves a turn
// holding two phases with a success response and no error anywhere.
type Store interface {
	// CreateEntity performs an atomic create-or-fail, reporting a taken key as
	// graphio.ErrEntityExists.
	CreateEntity(ctx context.Context, entity *graph.EntityState) (graphio.CreateResult, error)
	// GetEntity reads one entity, reporting an absent one as
	// graphio.ErrEntityNotFound rather than as an empty state.
	GetEntity(ctx context.Context, id string) (*graph.EntityState, error)
	// MergeTriples writes one entity's triples with replace-by-predicate
	// semantics.
	MergeTriples(
		ctx context.Context,
		entityID string,
		triples []message.Triple,
		opts ...graphio.MergeOption,
	) (*graph.EntityState, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ Store = (*graphio.Store)(nil)

// ActionStore is the durable home of the player's own words.
//
// It is a REQUIRED dependency rather than an option, and that is the whole
// content of this seam. Intake acknowledges an action once the turn entity
// exists; without a store, that guarantee covers the paperwork — the phase, the
// player, the scene — and not the sentence the player actually wrote, which
// exceeds the triple-object budget and is fiction besides (M1), so it can only
// reach the graph as a pointer. A recorder that could be built without one
// would be a recorder that silently loses the move.
type ActionStore interface {
	// PutAction stores the canonical action and returns the reference the turn
	// entity carries. It is called BEFORE the create, so a redelivery re-puts
	// identically rather than storing a second copy.
	PutAction(ctx context.Context, turnEntityID string, action *payload.PlayerAction) (content.Ref, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ ActionStore = (*content.Store)(nil)

// Recorder owns the turn entity: its creation, its phase, and its explicit
// failure.
type Recorder struct {
	store    Store
	actions  ActionStore
	identity Identity
	now      func() time.Time
}

// Option configures a Recorder.
type Option func(*Recorder)

// WithClock overrides the recorder's clock. Tests use it so a recorded phase's
// timestamp is an assertable value rather than whatever time it happened to be,
// and so an arbitrarily long gap between turns can be exercised without waiting
// for one.
func WithClock(now func() time.Time) Option {
	return func(r *Recorder) { r.now = now }
}

// NewRecorder builds a recorder for one world instance.
func NewRecorder(store Store, actions ActionStore, identity Identity, opts ...Option) (*Recorder, error) {
	if store == nil {
		return nil, errors.New("turn recorder requires a graph store")
	}
	if actions == nil {
		return nil, errors.New(
			"turn recorder requires an action store; without one an accepted turn carries no pointer to the " +
				"player's words, and 'acknowledged only after the turn durably exists' is true of the " +
				"paperwork and false of the move")
	}
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	recorder := &Recorder{store: store, actions: actions, identity: identity, now: time.Now}
	for _, opt := range opts {
		opt(recorder)
	}
	if recorder.now == nil {
		return nil, errors.New("turn recorder requires a clock")
	}
	return recorder, nil
}

// Acceptance is one accept attempt's result.
type Acceptance struct {
	// TurnID is the turn identity derived from the action.
	TurnID string
	// TurnEntityID is the turn's six-part entity ID; TurnID is its instance
	// segment.
	TurnEntityID string
	// Created reports that THIS call brought the turn into existence. False
	// means the action had already been accepted — a duplicate delivery or a
	// crash between the create and the acknowledgment — and nothing was written.
	Created bool
	// Phase is the turn's phase as of this call. On a duplicate it is whatever
	// the turn has since advanced to, which is the point: an accepted action
	// that is already narrating must not be reset to accepted.
	Phase vocabulary.TurnPhase
}

// Accept creates the turn entity for an action, in phase accepted.
//
// The create is ATOMIC, and that is the entire duplicate-delivery guard. An
// exists-check followed by a create has a window in which two deliveries both
// find the turn absent and both create it; create-or-fail has exactly one
// winner, and every loser learns the turn was already accepted. There is no read
// on the winning path at all, so there is no read-then-write race to lose.
//
// The caller acknowledges the action only after this returns without error. That
// ordering is the guarantee: a crash before it redelivers the action, a crash
// after it redelivers the action into an existing turn, and neither loses the
// player's move.
//
// The ACTION IS STORED FIRST, and the reference it produces rides inside the
// create rather than following it. Both halves of that are load-bearing:
//
//   - Object before reference, always. A reference to a missing object is a
//     correctness bug — a turn the rule pack re-triggers and the adjudicator
//     cannot re-prompt — while an object no turn references is garbage a
//     redelivery overwrites and a sweeper can collect. The two failures are not
//     symmetric, so the order is not a preference.
//   - Reference inside the create. A create followed by a second write to add
//     the reference would reopen precisely the crash window the atomic create
//     closes, and the turn left in the gap would look complete to every guard.
//
// The store is written unconditionally, including on the duplicate path,
// because the key is derived from the turn: a redelivery re-puts the identical
// bytes at the identical key. That is deliberately cheaper than reading first —
// a crash may have happened before the first put landed, so the second delivery
// has to write rather than assume.
//
// A create that reports Degraded is a COMMITTED write whose read-back failed. It
// is a success, and must not be retried — a retry would come back as
// entity_already_exists and a caller reading that as "somebody else got here
// first" would draw exactly the wrong conclusion about a turn it just created.
func (r *Recorder) Accept(ctx context.Context, action *payload.PlayerAction) (Acceptance, error) {
	if action == nil {
		return Acceptance{}, errors.New("accept requires a player action")
	}
	if err := action.Validate(); err != nil {
		return Acceptance{}, &RejectedActionError{ActionID: action.ActionID, Err: err}
	}
	turnID, turnEntityID, err := r.identity.EntityIDForAction(action.ActionID)
	if err != nil {
		return Acceptance{}, &RejectedActionError{ActionID: action.ActionID, Err: err}
	}

	// A store failure is TRANSIENT, not a rejected action: the action is
	// perfectly well formed and the store is unreachable. Rejecting it here
	// would terminate the delivery and throw away a move the player can never
	// resubmit, to spare a redelivery.
	actionRef, err := r.actions.PutAction(ctx, turnEntityID, action)
	if err != nil {
		return Acceptance{}, fmt.Errorf("store action for turn %s: %w", turnEntityID, err)
	}

	entity, err := r.acceptedEntity(turnID, turnEntityID, actionRef, action)
	if err != nil {
		return Acceptance{}, &RejectedActionError{ActionID: action.ActionID, Err: err}
	}

	result, err := r.store.CreateEntity(ctx, entity)
	acceptance := Acceptance{TurnID: turnID, TurnEntityID: turnEntityID}
	switch {
	case err == nil:
		// Degraded means the write committed and only the read-back failed, so
		// there is nothing to confirm and nothing to retry. Otherwise the
		// read-back is checked: a create that succeeded while the phase triple
		// was dropped would leave a turn every later guard refuses to read, and
		// a wedged turn that reports itself as accepted is the worst of both.
		if !result.Degraded {
			if err := confirmAccepted(result.Entity, turnEntityID); err != nil {
				return Acceptance{}, err
			}
		}
		acceptance.Created = true
		acceptance.Phase = vocabulary.PhaseAccepted

	case errors.Is(err, graphio.ErrEntityExists):
		// The action was accepted before. Read the phase rather than assuming
		// `accepted`: a redelivery can arrive at any point in a turn's life, and
		// reporting a narrating turn as freshly accepted would invite a caller
		// to start it over.
		phase, phaseErr := r.Current(ctx, turnEntityID)
		if phaseErr != nil {
			return Acceptance{}, phaseErr
		}
		acceptance.Phase = phase

	default:
		return Acceptance{}, fmt.Errorf("create turn %s: %w", turnEntityID, err)
	}

	if err := r.pointPlayerAtTurn(ctx, action.PlayerID, acceptance); err != nil {
		return Acceptance{}, err
	}
	return acceptance, nil
}

// pointPlayerAtTurn records on the PLAYER entity which turn they now hold.
//
// # Why the turn recorder owns a write to another entity
//
// Because it is the only thing that knows the turn became a fact. The pointer's
// entire value is that it never names a turn that does not exist, and the moment
// that becomes true is here — after the atomic create, inside the same call that
// performed it. A gateway that stamped the pointer before publishing would have
// to decide what an absent turn means, and both answers are bad: "admit" reopens
// the second turn it exists to close, and "refuse" locks the player out forever
// the first time a publish fails after the write.
//
// # After the create, never before or inside it
//
// Inside is not available — the create is atomic on the TURN entity and
// graph-ingest's merge lane is per-entity, so a foreign subject in it is split
// onto the appending lane and the failure is swallowed (F14). So it is a second
// write, and the ORDER is the turn first. A crash in the gap leaves the pointer
// naming the player's PREVIOUS turn, which is terminal, so the gate admits their
// next action: fail-open, one extra turn, self-healing on the next accept. The
// reverse order would leave a pointer at a turn that was never created, which is
// the lockout this design exists to make impossible.
//
// # Written on a duplicate too, and the two conditions it takes to be safe
//
// Written on a duplicate because that is what heals the crash gap above: the
// action was never acknowledged, so it is redelivered, the create loses to the
// existing turn, and this call finally lands the pointer.
//
// Which makes this call LATE by construction, and it takes two questions to know
// whether it is too late. Both are necessary and neither is sufficient:
//
//   - Has THIS turn ended? A turn that has ended is not one anybody holds, so a
//     redelivery of a finished action must not repoint the player at it.
//   - Does the player already hold a DIFFERENT turn that has not ended? This is
//     the converse the first question does not answer, and it is false exactly
//     when two turns are unfinished at once — which the failure path above
//     creates without a crash and without a hostile client. The pointer write
//     for T1 fails and the action naks for thirty seconds; the gate reads a
//     terminal pointer, admits A2, and T2 becomes the live turn; A1 then
//     redelivers into a still-running T1. Repointing at T1 there abandons T2, and
//     when T1 resolves the gate reopens while T2 is still running — so the
//     documented "one extra turn" becomes a cascade, one per redelivery.
//
// The second question is asked on BOTH branches, not just the duplicate one. A
// create that fails transiently and succeeds on redelivery reaches the identical
// state through the identical failure, and a fresh turn has no recorded phase to
// look stale with: the player's own pointer is the only thing that can tell this
// call it is late.
//
// It costs one read of the player and, when they hold a turn, one of that turn.
// That is per accepted turn on the intake path — not on the player's submission
// path, where the gateway's own two reads live — and a turn costs a model call.
//
// The check is CONVERGENT, like every other guard here: another accept can land
// between the read and the write. That is the same missing compare-and-swap the
// package doc names, and it degrades the same way, toward one extra turn rather
// than toward a lockout.
//
// # A failure here is transient, not a rejected action
//
// It returns an ordinary error, so intake naks and redelivers rather than
// terminating: the turn exists and the player's move is safe, the redelivery
// retries exactly this write, and the failure is loud on the consumer's own
// counters. The alternative — swallowing it — would leave the admission gate
// silently degraded for that player with nothing anywhere to say so.
func (r *Recorder) pointPlayerAtTurn(ctx context.Context, playerID string, acceptance Acceptance) error {
	if acceptance.Phase.IsTerminal() {
		return nil
	}
	late, err := r.holdsAnotherLiveTurn(ctx, playerID, acceptance.TurnEntityID)
	if err != nil {
		return err
	}
	if late {
		return nil
	}
	pointer := &payload.PlayerTurn{
		PlayerID:     playerID,
		TurnID:       acceptance.TurnID,
		TurnEntityID: acceptance.TurnEntityID,
	}
	triples, err := pointer.Triples(playerID, Source, r.now().UTC())
	if err != nil {
		return err
	}
	if _, err := r.store.MergeTriples(ctx, playerID, triples); err != nil {
		return fmt.Errorf(
			"point player %s at turn %s: %w; the turn exists, so this is retried on redelivery rather than "+
				"failing the action", playerID, acceptance.TurnEntityID, err)
	}
	return nil
}

// acceptedEntity builds the turn's birth record.
func (r *Recorder) acceptedEntity(
	turnID, turnEntityID string,
	actionRef content.Ref,
	action *payload.PlayerAction,
) (*graph.EntityState, error) {
	at := r.now().UTC()
	state := &payload.TurnState{
		TurnID:    turnID,
		Phase:     vocabulary.PhaseAccepted,
		PlayerID:  action.PlayerID,
		SceneID:   action.SceneID,
		ActionRef: actionRef.String(),
	}
	triples, err := state.Triples(turnEntityID, Source, at)
	if err != nil {
		return nil, err
	}
	return &graph.EntityState{
		ID:          turnEntityID,
		MessageType: EntityMessageType,
		Version:     1,
		UpdatedAt:   at,
		Triples:     triples,
	}, nil
}

// confirmAccepted checks a created turn read back in the phase it was born in.
func confirmAccepted(stored *graph.EntityState, turnEntityID string) error {
	phase, err := PhaseOf(stored, turnEntityID)
	if err != nil {
		return fmt.Errorf("turn %s was created but its phase did not store: %w", turnEntityID, err)
	}
	if phase != vocabulary.PhaseAccepted {
		return &RecordError{TurnEntityID: turnEntityID, Err: fmt.Errorf(
			"was created in phase %q, not %q", phase, vocabulary.PhaseAccepted)}
	}
	return nil
}

// Current reads the turn's recorded phase.
//
// Every anomaly is an error rather than a shrug, because this answer decides
// whether a stage runs. A turn holding two phases is the signature of a write
// that took an appending lane, and a reader taking the first value it found
// would be reading a coin flip; a turn holding none is a half-created record; a
// turn that is only a referential stub is queryable and factless, so "no phase
// recorded" read off one would be a false negative.
func (r *Recorder) Current(ctx context.Context, turnEntityID string) (vocabulary.TurnPhase, error) {
	_, phase, err := r.currentState(ctx, turnEntityID)
	return phase, err
}

// currentState reads the turn and its phase together.
//
// It exists because a TERMINAL transition needs one more fact off the same
// record — which player the turn belongs to — and re-reading the entity to get
// it would be a second read whose answer could differ from the one the guard
// just made its decision on.
func (r *Recorder) currentState(
	ctx context.Context,
	turnEntityID string,
) (*graph.EntityState, vocabulary.TurnPhase, error) {
	state, err := r.store.GetEntity(ctx, turnEntityID)
	if err != nil {
		return nil, "", fmt.Errorf("read turn entity %s: %w", turnEntityID, err)
	}
	phase, err := PhaseOf(state, turnEntityID)
	if err != nil {
		return nil, "", err
	}
	return state, phase, nil
}

// PhaseOf reads a turn's recorded phase off an entity somebody else fetched.
//
// It is exported so the second reader of this fact — the player-session
// gateway's admission gate, which asks whether a player's current turn is still
// running — asks the question exactly the way the recorder does. A second
// implementation would be a second opinion about what an anomalous turn record
// means, and the anomalies are the whole content of this function: a turn
// holding two phases, none, or nothing but a referential stub each has a
// deliberate answer, and "take the first triple you find" is the wrong one three
// times over.
func PhaseOf(state *graph.EntityState, turnEntityID string) (vocabulary.TurnPhase, error) {
	if state == nil {
		return "", &RecordError{TurnEntityID: turnEntityID, Err: errors.New("read back as nil")}
	}
	if state.IsStub() {
		return "", &RecordError{TurnEntityID: turnEntityID, Err: errors.New(
			"is a referential stub: it holds no facts, so its phase is unknown")}
	}

	var objects []any
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.TurnPhaseCurrent.String() {
			objects = append(objects, triple.Object)
		}
	}
	switch len(objects) {
	case 0:
		return "", &RecordError{TurnEntityID: turnEntityID, Err: fmt.Errorf(
			"carries no %s triple; a turn without a phase cannot be resumed or diagnosed",
			vocabulary.TurnPhaseCurrent)}
	case 1:
	default:
		return "", &RecordError{TurnEntityID: turnEntityID, Err: fmt.Errorf(
			"holds %d values for the single-valued %s; a phase written on an appending lane leaves the "+
				"guard reading a coin flip", len(objects), vocabulary.TurnPhaseCurrent)}
	}

	value, ok := objects[0].(string)
	if !ok {
		return "", &RecordError{TurnEntityID: turnEntityID, Err: fmt.Errorf(
			"records a %T phase, want a string", objects[0])}
	}
	phase, err := vocabulary.ParseTurnPhase(value)
	if err != nil {
		return "", &RecordError{TurnEntityID: turnEntityID, Err: err}
	}
	return phase, nil
}

// holdsAnotherLiveTurn reports whether the player's pointer already names a turn
// that is not this one and has not ended.
//
// Every way of failing to find out resolves toward WRITING the pointer rather
// than toward leaving it where it is, and that polarity is deliberate:
//
//   - The player entity is absent. Nothing is held. (The turn's own birth record
//     names the player, so graph-ingest has usually minted a referential stub by
//     now; a stub holds no facts and answers the same way.)
//   - A held turn is absent. A turn nobody can find is not one that is running.
//   - A held turn's record is unreadable — two phases, none, a stub. Moving the
//     pointer HEALS that: leaving it would strand the player behind a turn the
//     gateway cannot read, which is the lockout this whole design refuses.
//
// Only a transport failure is returned as an error, because that one is
// answerable later: intake naks, redelivers, and asks again.
func (r *Recorder) holdsAnotherLiveTurn(ctx context.Context, playerID, exceptTurnEntityID string) (bool, error) {
	state, err := r.store.GetEntity(ctx, playerID)
	switch {
	case errors.Is(err, graphio.ErrEntityNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("read player %s to place their turn pointer: %w", playerID, err)
	}

	held, _ := HeldTurns(state)
	for _, turnEntityID := range held {
		if turnEntityID == exceptTurnEntityID {
			continue
		}
		live, err := r.turnIsLive(ctx, turnEntityID)
		if err != nil {
			return false, err
		}
		if live {
			return true, nil
		}
	}
	return false, nil
}

// turnIsLive reports whether a turn exists and has not ended.
func (r *Recorder) turnIsLive(ctx context.Context, turnEntityID string) (bool, error) {
	state, err := r.store.GetEntity(ctx, turnEntityID)
	switch {
	case errors.Is(err, graphio.ErrEntityNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("read turn %s to place a player's turn pointer: %w", turnEntityID, err)
	}

	phase, err := PhaseOf(state, turnEntityID)
	var record *RecordError
	if errors.As(err, &record) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !phase.IsTerminal(), nil
}

// HeldTurns lists the turn entity ids a player's pointer names, and counts the
// ones it could not read.
//
// A slice rather than a single value, because "how many values does this
// predicate hold" is what distinguishes a merge-lane write from an append-lane
// one, and a reader that took the first would answer a coin flip.
//
// Exported because it has two callers asking the same question from opposite
// sides — the recorder deciding whether it is too late to move the pointer, and
// the player-session gateway deciding whether to admit an action. A second
// implementation would be a second opinion about what an anomalous pointer
// means, and the anomalies are the whole content of this function.
//
// The unreadable count is returned rather than dropped so a caller can say so:
// a pointer nothing can read is a pointer nothing checks, and "ignored" plus
// "silent" is how a player ends up holding two live turns with nothing anywhere
// explaining why.
func HeldTurns(state *graph.EntityState) (held []string, unreadable int) {
	if state == nil {
		return nil, 0
	}
	for _, triple := range state.Triples {
		if triple.Predicate != vocabulary.PlayerTurnCurrent.String() {
			continue
		}
		turnEntityID, ok := triple.Object.(string)
		if !ok || turnEntityID == "" {
			unreadable++
			continue
		}
		held = append(held, turnEntityID)
	}
	return held, unreadable
}

// ResolvedTurns lists the turn entity ids a player's RESOLVED pointer names, and
// counts the ones it could not read.
//
// The shape mirrors HeldTurns, and for the same reason: a slice rather than one
// value, because "how many values does this predicate hold" is what distinguishes
// a merge-lane write from an append-lane one, and a reader that took the first
// would answer a coin flip about what happened to this player last.
//
// Exported because it also has two callers on opposite sides — this package
// deciding whether a repair is needed, and the retrieval surface deciding which
// turn to answer with. A second implementation would be a second opinion about
// what an anomalous pointer means.
func ResolvedTurns(state *graph.EntityState) (resolved []string, unreadable int) {
	if state == nil {
		return nil, 0
	}
	for _, triple := range state.Triples {
		if triple.Predicate != vocabulary.PlayerTurnResolved.String() {
			continue
		}
		turnEntityID, ok := triple.Object.(string)
		if !ok || turnEntityID == "" {
			unreadable++
			continue
		}
		resolved = append(resolved, turnEntityID)
	}
	return resolved, unreadable
}

// Outcome is what a transition attempt did. It never reaches the graph — the
// graph records phases, not attempts — so it is an in-process answer, not
// vocabulary.
type Outcome string

// The three answers a guarded transition can give.
const (
	// OutcomeAdvanced means this call moved the turn into the target phase and
	// the caller owns the stage.
	OutcomeAdvanced Outcome = "advanced"
	// OutcomeResumed means the turn was ALREADY in the target phase: an
	// interrupted stage re-entering itself after a crash, or a second trigger
	// arriving while the first is in flight. Nothing was written, because the
	// fact is already stated. The stage may run — it was never recorded as
	// finished — and every stage under the guard is idempotent by construction.
	OutcomeResumed Outcome = "resumed"
	// OutcomeDeclined means the turn has moved PAST this stage. The trigger is
	// stale, and a completed stage must not re-run. Nothing was written.
	OutcomeDeclined Outcome = "declined"
)

// Transition is one guarded phase write's result.
type Transition struct {
	// Previous is the phase the turn was recorded in when the guard read it.
	Previous vocabulary.TurnPhase
	// Phase is the turn's phase after this call — equal to Previous whenever
	// the outcome is not Advanced.
	Phase vocabulary.TurnPhase
	// Outcome is what this call did.
	Outcome Outcome
}

// Advance moves the turn into a phase, guarded by the phase it is recorded in.
//
// The guard is the FSM table in internal/vocabulary, and it answers three
// different questions with three different outcomes:
//
//   - The recorded phase is a legal predecessor → Advanced. The phase is written
//     on the merge lane, replacing its own prior value.
//   - The recorded phase IS the target → Resumed. Nothing is written; the fact
//     is already stated, and rewriting it would only churn the timestamp.
//   - The turn has moved past this stage (or ended) → Declined. Nothing is
//     written. This is what at-least-once delivery produces, and doing nothing is
//     the correct response.
//
// Anything else is an IllegalTransitionError, because a hop that skips a stage
// is a wiring bug rather than a duplicate, and a turn that advanced without
// running a stage is worse than a turn that stalled.
//
// The two phases Advance refuses OUTRIGHT are refused as that same type, and
// carry their own faults. They are a wiring bug of exactly the same class — a
// caller reaching for the wrong lane — and a plain error for them would put two
// members of the class outside every `errors.As` a caller writes to catch it.
// Both are decided from the target alone, before the turn's phase is read: where
// the turn is has no bearing on whether Advance may write those phases at all.
//
// The read and the write are two round trips, which is a window: two writers
// that read the same phase both pass the guard. Closing it needs a
// compare-and-swap on the read revision, and the entity query surface returns no
// revision to swap on. Nothing here works around that — see the package doc for
// why this slice does not need it and where the upgrade goes.
func (r *Recorder) Advance(
	ctx context.Context,
	turnID, turnEntityID string,
	to vocabulary.TurnPhase,
) (Transition, error) {
	switch to {
	case vocabulary.PhaseAccepted:
		return Transition{}, &IllegalTransitionError{
			TurnEntityID: turnEntityID, To: to, Fault: FaultCreatedNotAdvanced}
	case vocabulary.PhaseFailed:
		return Transition{}, &IllegalTransitionError{
			TurnEntityID: turnEntityID, To: to, Fault: FaultFailureNeedsReason}
	}
	return r.transition(ctx, turnID, turnEntityID, to, "", content.Ref{})
}

// Fail ends the turn explicitly, recording a closed reason.
//
// reason MUST be a member of the closed vocabulary. That check is the whole
// point of this method existing separately from Advance: the reason lands on the
// turn entity, which is rule-matching surface, and the shared triple projection
// gates an object's SHAPE but not its CLOSURE — so a sentence composed at the
// call site would pass every gate on the way to the graph.
//
// detail is an optional reference to stored detail (the refused batch, the
// exhausted loop). It is a content.Ref rather than a string so that "detail
// reaches the graph as a reference or not at all" is enforced by the type
// rather than by this comment: a caller holding an explanation has nothing to
// pass, and has to store it first.
//
// A turn that has already ended DECLINES rather than being overwritten, so the
// first recorded reason is the one that actually happened. A second failure
// arriving later is a stale trigger, and a completed turn is not failed after
// the fact.
func (r *Recorder) Fail(
	ctx context.Context,
	turnID, turnEntityID string,
	reason vocabulary.FailureReason,
	detail content.Ref,
) (Transition, error) {
	if _, err := vocabulary.ParseFailureReason(string(reason)); err != nil {
		return Transition{}, fmt.Errorf(
			"refusing to record a turn failure: %w; the reason lands on rule-matching surface, so it is a "+
				"code and never a sentence", err)
	}
	if !detail.IsZero() {
		if err := detail.Validate(); err != nil {
			return Transition{}, err
		}
	}
	return r.transition(ctx, turnID, turnEntityID, vocabulary.PhaseFailed, reason, detail)
}

// transition is the one guarded write both Advance and Fail run through.
func (r *Recorder) transition(
	ctx context.Context,
	turnID, turnEntityID string,
	to vocabulary.TurnPhase,
	reason vocabulary.FailureReason,
	detail content.Ref,
) (Transition, error) {
	// The entity and the turn id are one turn wearing two shapes, and this is
	// the pairing seam: a stage handed turn A's entity with turn B's id would
	// advance a turn nobody is running and strand the one that is. The
	// projection checks it again on the way to the graph; checking it here means
	// a mismatch is refused before a read is even issued.
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return Transition{}, err
	}

	stored, previous, err := r.currentState(ctx, turnEntityID)
	if err != nil {
		return Transition{}, err
	}

	outcome, fault := classify(previous, to)
	if fault != "" {
		return Transition{}, &IllegalTransitionError{
			TurnEntityID: turnEntityID, From: previous, To: to, Fault: fault}
	}
	if outcome != OutcomeAdvanced {
		// A DECLINED terminal transition is the redelivery of a transition that
		// already happened, and it is the only thing that can still land a
		// resolved-turn pointer whose first write was lost. See
		// repairResolvedPointer for why the repair is guarded and the write on
		// the advancing path below is not.
		if to.IsTerminal() && previous.IsTerminal() {
			if err := r.repairResolvedPointer(ctx, stored, turnID, turnEntityID); err != nil {
				return Transition{}, err
			}
		}
		return Transition{Previous: previous, Phase: previous, Outcome: outcome}, nil
	}

	state := &payload.TurnState{
		TurnID: turnID, Phase: to, Reason: reason, DetailRef: detail.String(),
	}
	triples, err := state.Triples(turnEntityID, Source, r.now().UTC())
	if err != nil {
		return Transition{}, err
	}
	if _, err := r.store.MergeTriples(ctx, turnEntityID, triples); err != nil {
		return Transition{}, fmt.Errorf("record phase %q on turn %s: %w", to, turnEntityID, err)
	}

	if to.IsTerminal() {
		if err := r.pointPlayerAtResolvedTurn(ctx, stored, turnID, turnEntityID); err != nil {
			return Transition{}, err
		}
	}
	return Transition{Previous: previous, Phase: to, Outcome: OutcomeAdvanced}, nil
}

// pointPlayerAtResolvedTurn records on the PLAYER entity which turn most
// recently ended for them.
//
// # Why the recorder owns this write too
//
// For the reason it owns pointPlayerAtTurn: it is the only thing that knows the
// fact became true. Every terminal phase in this engine is written by the call
// above — `complete` from the completion stage, and `failed` from the applier,
// the persona failure path and the stranded-turn pass all reaching Fail — so
// this is the one place in the process that can say "this turn ended, now" as
// opposed to "this turn is ended", which is a different and much weaker claim.
//
// The alternative was a consumer of the resolved-turn notification, and it is
// worse in a way that only shows up under load: a durable consumer's
// redeliveries arrive in whatever order the broker produces them, and a pointer
// at THE MOST RECENT written out of order is a pointer that walks backwards.
// Here the writes happen in the same order the turns end, because ending them is
// what triggers the writes.
//
// # After the phase, never before
//
// The phase is written first, so a failure in the gap leaves the pointer naming
// the player's PREVIOUS terminal turn — an older answer, never a missing one.
// The reverse order would leave the pointer naming a turn that had not ended,
// and a retrieval reading it would find a live turn where a result should be and
// have nothing else to fall back on.
//
// # A failure here is transient, not a resolved turn
//
// It returns an ordinary error, so the stage naks and redelivers rather than
// acknowledging: the phase is written and the turn really has ended, the
// redelivery declines the transition, and the DECLINED path repairs the pointer.
// Swallowing it would leave the player's "what happened last?" permanently
// answered with the turn before this one, silently.
//
// # This write is UNGUARDED, and can walk backwards
//
// It is unguarded on purpose — the turn just became terminal, so no read is
// needed to know the fact is new. But two turns can be live at once (the
// documented one-extra-turn window), and if the newer resolves first, the older
// one resolving afterwards overwrites the pointer with an older turn.
//
// That is harmless only because of how the pointer is READ. Results.Latest does
// not trust which pointer named a candidate; it reads both, reads each named
// turn's own recorded phase timestamp, and answers with the turn that actually
// ended most recently. Guarding this write instead would need a read-modify-write
// with no compare-and-swap to protect it (F15) — a race on the very fact
// retrieval depends on, bought to remove one a reader already handles.
func (r *Recorder) pointPlayerAtResolvedTurn(
	ctx context.Context,
	state *graph.EntityState,
	turnID, turnEntityID string,
) error {
	playerID, err := playerOf(state, turnEntityID)
	if err != nil {
		return err
	}
	pointer := &payload.PlayerResolvedTurn{
		PlayerID:     playerID,
		TurnID:       turnID,
		TurnEntityID: turnEntityID,
	}
	triples, err := pointer.Triples(playerID, Source, r.now().UTC())
	if err != nil {
		return err
	}
	if _, err := r.store.MergeTriples(ctx, playerID, triples); err != nil {
		return fmt.Errorf(
			"point player %s at their resolved turn %s: %w; the phase is written, so this is retried on "+
				"redelivery rather than failing the turn", playerID, turnEntityID, err)
	}
	return nil
}

// repairResolvedPointer lands a resolved-turn pointer whose first write was lost,
// and refuses to land one that would run backwards.
//
// The guard is player.turn.current, and it is the cheapest sufficient one. That
// pointer names the most recently ACCEPTED turn, so while it names THIS turn the
// player has started nothing since — which makes this turn, having ended, the
// most recent one to end. When it names a different turn the player has moved on,
// and repairing here would overwrite a newer turn's answer with an older one.
//
// The alternative guard was comparing the two turns' terminal timestamps, which
// is exact and needs a read-modify-write with no compare-and-swap to protect it
// (F15). It would trade a bounded, self-correcting staleness for a race on the
// one fact retrieval depends on, so it is refused.
//
// # It is a BOUNDED STALENESS, not a retry that eventually lands
//
// Stating that precisely, because the word "repair" invites the wrong reading.
// When the guard declines, this returns nil and the caller ACKNOWLEDGES — the
// repair is not deferred to a later delivery, it is gone. So the case it gives
// up is: two turns live at once, the older resolves first, its pointer write
// fails, its redelivery finds the current pointer on the newer turn and declines
// the repair. The resolved pointer then names a turn OLDER than the one that just
// ended, until the newer turn resolves and writes it — which it will, on its own
// terminal transition, with no further redelivery needed.
//
// So the guarantee is one bounded interval of staleness followed by correctness,
// never "it lands eventually". And through that whole interval Results.Latest
// still answers correctly anyway, because player.turn.current names the newer
// live turn and the older terminal one is the only candidate that composes.
func (r *Recorder) repairResolvedPointer(
	ctx context.Context,
	state *graph.EntityState,
	turnID, turnEntityID string,
) error {
	playerID, err := playerOf(state, turnEntityID)
	if err != nil {
		return err
	}
	player, err := r.store.GetEntity(ctx, playerID)
	switch {
	case errors.Is(err, graphio.ErrEntityNotFound):
		// Nothing to repair a pointer on. A player entity that is gone is not a
		// player waiting for an answer, and refusing here would nak forever over
		// a turn that has already ended.
		return nil
	case err != nil:
		return fmt.Errorf("read player %s to repair their resolved-turn pointer: %w", playerID, err)
	}

	resolved, _ := ResolvedTurns(player)
	if slices.Contains(resolved, turnEntityID) {
		// Already landed. The common case on a redelivery, and worth one read to
		// avoid: an unconditional write would churn the pointer's timestamp on
		// every duplicate trigger.
		return nil
	}
	held, _ := HeldTurns(player)
	if !slices.Contains(held, turnEntityID) {
		return nil
	}

	pointer := &payload.PlayerResolvedTurn{
		PlayerID:     playerID,
		TurnID:       turnID,
		TurnEntityID: turnEntityID,
	}
	triples, err := pointer.Triples(playerID, Source, r.now().UTC())
	if err != nil {
		return err
	}
	if _, err := r.store.MergeTriples(ctx, playerID, triples); err != nil {
		return fmt.Errorf(
			"repair player %s's pointer at their resolved turn %s: %w", playerID, turnEntityID, err)
	}
	return nil
}

// playerOf reads the player a turn belongs to off its birth record.
//
// A turn without one is reported rather than skipped, and it is the right
// polarity even though the phase is already written: a turn that names no player
// cannot be delivered to anybody and cannot be archived either — the ledger's
// Compose refuses it for the same fact — so it is already in the loud class. The
// alternative is a turn that quietly resolves for nobody.
func playerOf(state *graph.EntityState, turnEntityID string) (string, error) {
	var values []any
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.TurnActionPlayer.String() {
			values = append(values, triple.Object)
		}
	}
	switch len(values) {
	case 0:
		return "", &RecordError{TurnEntityID: turnEntityID, Err: fmt.Errorf(
			"carries no %s; it is written by the turn's atomic create, so a turn without one cannot be "+
				"delivered to anybody", vocabulary.TurnActionPlayer)}
	case 1:
	default:
		return "", &RecordError{TurnEntityID: turnEntityID, Err: fmt.Errorf(
			"holds %d values for the single-valued %s; a turn belonging to two players would resolve for "+
				"whichever one a reader happened to take", len(values), vocabulary.TurnActionPlayer)}
	}
	playerID, ok := values[0].(string)
	if !ok || playerID == "" {
		return "", &RecordError{TurnEntityID: turnEntityID, Err: fmt.Errorf(
			"records a %T for %s, want a player entity id", values[0], vocabulary.TurnActionPlayer)}
	}
	return playerID, nil
}

// classify decides what a proposed transition means. A non-empty fault means the
// transition is refused and the Outcome is meaningless.
//
// Order matters. Knownness is checked FIRST because every answer below it is
// read off the FSM table, and a phase the table does not contain gets the same
// answers as one it does: an unknown target has rank zero, so a terminal turn
// asked to move to a misspelled phase would be told "declined" — a benign no-op
// — for what is a typo the caller needs to hear about.
//
// The terminal check comes before the equality check, and those two were
// originally the other way round. A TERMINAL turn declines everything, including
// a repeat of its own ending: a second failure arriving at an already-failed
// turn is a stale trigger, not a stage resuming, and calling it a resume would
// tell the caller to run something. Re-entry only means "resume" while the turn
// is still running.
//
// After those, the legal-predecessor set decides, and only then does rank
// separate "you are late" (decline, ordinary) from "you skipped a stage" (a
// wiring bug).
func classify(previous, to vocabulary.TurnPhase) (Outcome, TransitionFault) {
	previousRank, previousKnown := vocabulary.PhaseRank(previous)
	toRank, toKnown := vocabulary.PhaseRank(to)
	if !previousKnown || !toKnown {
		return "", FaultUnknownPhase
	}

	if previous.IsTerminal() {
		return OutcomeDeclined, ""
	}
	if previous == to {
		return OutcomeResumed, ""
	}
	if vocabulary.PhaseFollows(previous, to) {
		return OutcomeAdvanced, ""
	}
	if previousRank > toRank {
		return OutcomeDeclined, ""
	}
	return "", FaultSkippedStage
}
