package egress

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/ledger"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Sentinels for the outcomes a caller genuinely branches on.
var (
	// ErrNoResult reports a turn that has not ended, or a player who has not
	// finished one.
	//
	// Distinct from ErrTurnNotFound because the two mean opposite things to
	// whoever asked. "Your turn is still running" is an answer; "there is no such
	// turn" is a different one, and a surface that spelled them the same way
	// would leave a player unable to tell waiting from a typo — which is exactly
	// the silence a terminal-result surface exists to replace.
	ErrNoResult = errors.New("no terminal result for this turn")
	// ErrTurnNotFound reports a turn id nothing in this world ever created.
	ErrTurnNotFound = errors.New("no such turn")
	// ErrResultNotAccessible reports a named turn that does not belong to the
	// authenticated player asking for it. Public adapters intentionally collapse
	// it with ErrTurnNotFound so ids cannot be used as a cross-player state oracle.
	ErrResultNotAccessible = errors.New("result is not accessible to this player")
	// ErrNarrationMissing reports a result whose narration reference resolves to
	// no object.
	//
	// It is its own sentinel because it is a CORRECTNESS bug rather than a state:
	// prose is written before the reference is committed, so a reference to a
	// missing object means the ordering was violated or the object was removed.
	// The turn cannot be delivered — a completed turn's result is unreadable
	// without its prose — and saying so is better than shipping a document with a
	// hole in it.
	ErrNarrationMissing = errors.New("the prose this result references is not in the store")
)

// MaxLatestCandidates bounds how many turns a most-recent-terminal lookup will
// read.
//
// A healthy player entity offers exactly two candidates — the turn they most
// recently had ACCEPTED and the one that most recently RESOLVED — so anything
// past a handful is the signature of a write that took an appending lane. The
// bound exists so that anomaly degrades into a slightly stale answer rather than
// turning every retrieval into a scan of one player's whole history: a retrieval
// surface whose cost depends on how corrupted an entity got is a retrieval
// surface that fails hardest exactly when it is needed.
const MaxLatestCandidates = 8

// TurnReader is the graph read surface retrieval needs.
//
// READS ONLY, and that is a boundary rather than an accident: answering what
// happened must not be able to change what happened. Both durable pointers this
// package follows are written by the turn recorder, at the moments it learns the
// facts became true.
type TurnReader interface {
	GetEntity(ctx context.Context, id string) (*graph.EntityState, error)
}

// ArtifactReader resolves the references a result carries.
//
// These are only the artifacts needed to compose the public result: the roll
// behind the resolution card, the prose, and the exact prose-free companion
// stage/decision chain behind an optional companion summary. The verdict body,
// effect batch and stored action are never delivered.
type ArtifactReader interface {
	GetRoll(ctx context.Context, ref content.Ref) (*payload.RollResult, error)
	GetNarration(ctx context.Context, ref content.Ref) (*content.Narration, error)
	GetCompanionStageRecord(ctx context.Context, ref content.Ref) (*payload.CompanionStageRecord, error)
	GetCompanionDecision(ctx context.Context, ref content.Ref) (*payload.CompanionDecision, error)
}

// The claims above, enforced by the compiler rather than by doc comments.
var (
	_ TurnReader     = (*graphio.Store)(nil)
	_ ArtifactReader = (*content.Store)(nil)
)

// Results composes a player-facing delivery for one turn, from durable state.
type Results struct {
	turns      TurnReader
	artifacts  ArtifactReader
	identity   turn.Identity
	campaignID string
}

// NewResults builds the retrieval surface for one world instance.
//
// The identity is how a turn id becomes a turn ENTITY id, which is what makes
// retrieval by turn id possible at all without a second index; the campaign id is
// what ledger.Compose stamps, and it is configuration for the reason the ledger
// writer's is — instance-per-world means one process serves one campaign, and the
// turn entity carries no campaign reference to read.
func NewResults(
	turns TurnReader,
	artifacts ArtifactReader,
	identity turn.Identity,
	campaignID string,
) (*Results, error) {
	switch {
	case turns == nil:
		return nil, errors.New("the result surface requires a graph read surface")
	case artifacts == nil:
		return nil, errors.New(
			"the result surface requires an artifact reader; a result carries references and a player cannot " +
				"resolve one, so a surface without a store would deliver pointers at prose nobody can read")
	}
	if err := identity.Validate(); err != nil {
		return nil, fmt.Errorf("the result surface requires a turn identity: %w", err)
	}
	if err := types.ValidateEntityID(campaignID); err != nil {
		return nil, fmt.Errorf("the result surface requires a canonical campaign entity id: %w", err)
	}
	return &Results{turns: turns, artifacts: artifacts, identity: identity, campaignID: campaignID}, nil
}

// ByTurn answers with one turn's delivery.
func (r *Results) ByTurn(ctx context.Context, turnID string) (*payload.TurnDelivery, error) {
	turnEntityID, err := r.identity.EntityID(turnID)
	if err != nil {
		return nil, err
	}
	state, err := r.read(ctx, turnEntityID)
	if err != nil {
		return nil, err
	}
	return r.deliverable(ctx, turnID, turnEntityID, state)
}

// ByTurnForPlayer answers only when the named turn belongs to playerID.
// Ownership is checked from the turn's birth scalar before any artifact is
// dereferenced, so a foreign lookup cannot read another player's dice or prose.
func (r *Results) ByTurnForPlayer(
	ctx context.Context,
	playerID, turnID string,
) (*payload.TurnDelivery, error) {
	if err := types.ValidateEntityID(playerID); err != nil {
		return nil, fmt.Errorf("a scoped result lookup needs a player entity id: %w", err)
	}
	turnEntityID, err := r.identity.EntityID(turnID)
	if err != nil {
		return nil, err
	}
	state, err := r.read(ctx, turnEntityID)
	if err != nil {
		return nil, err
	}
	owner, err := resultOwner(state, turnEntityID)
	if err != nil {
		return nil, err
	}
	if owner != playerID {
		return nil, fmt.Errorf("%w: turn %s", ErrResultNotAccessible, turnID)
	}
	return r.deliverable(ctx, turnID, turnEntityID, state)
}

// ByAction answers with the delivery for the turn one action produced.
//
// It DERIVES the turn rather than looking it up, because turn and action are 1:1
// by a derivation this engine owns. A second index keyed on action id would be a
// second statement of that pairing and a second thing to keep true.
func (r *Results) ByAction(ctx context.Context, actionID string) (*payload.TurnDelivery, error) {
	if err := payload.RequireActionID(actionID); err != nil {
		return nil, err
	}
	return r.ByTurn(ctx, payload.TurnIDForAction(actionID))
}

// ByActionForPlayer is the action-id form of ByTurnForPlayer.
func (r *Results) ByActionForPlayer(
	ctx context.Context,
	playerID, actionID string,
) (*payload.TurnDelivery, error) {
	if err := payload.RequireActionID(actionID); err != nil {
		return nil, err
	}
	return r.ByTurnForPlayer(ctx, playerID, payload.TurnIDForAction(actionID))
}

// Latest answers with a player's most recent TERMINAL result.
//
// # Why two pointers and not one
//
// player.turn.current names the most recently ACCEPTED turn, so it answers this
// question only while that turn happens to be terminal — the moment the player
// acts again it names a live turn. player.turn.resolved names the most recent
// turn to END. Neither alone is sufficient and neither is TRUSTED here: both are
// read as ADDRESSES, each named turn's own phase decides whether it is an answer
// at all, and the recorded phase TIMESTAMP decides between two that are.
//
// That composition is what makes the recorder's write ordering safe, in two
// separate ways, and the second is the stronger argument for reading both.
//
// The phase is written before the resolved pointer, so a failure in the gap
// leaves the resolved pointer naming the previous turn — and in exactly that gap
// the current pointer still names the turn that just ended, so this answers
// correctly anyway.
//
// And the resolved pointer's write is UNGUARDED, so it can walk BACKWARDS: two
// turns live at once, the newer resolves first, the older then overwrites the
// pointer with itself. Nothing on the writing side prevents that, and nothing
// needs to — ordering candidates by their own recorded ResolvedAt rather than by
// which pointer named them makes the answer independent of the order the pointers
// were written in. Trusting the resolved pointer to BE the answer is the version
// of this that would be wrong, and it is the obvious version.
//
// # Why not scan the ledger
//
// Because the archive is ordered by when turns were archived, not indexed by
// player, so "this player's most recent terminal turn" is a walk of the whole
// campaign's history filtered by player — O(campaign history) on a request path,
// growing forever, for a question two graph reads answer. The pointer is the
// index, exactly as player.turn.current is the admission gate's.
//
// # What it skips, and what it refuses
//
// A candidate that is absent, that is not a turn of this world, or that has not
// ended is SKIPPED: each is a normal state of one pointer while the other still
// answers, and refusing on any of them would let a stale triple deny a player
// every result they have.
//
// A candidate whose RECORD cannot compose a result — two phases, a doubled
// single-valued fact, prose that resolves to another turn — is returned as an
// error rather than skipped, and that is the one place this surface refuses to
// degrade.
//
// The reason is not that the alternative answer would be stale, though it would
// be. It is that a corrupt turn record is the signature of a write that took an
// APPENDING LANE, and that fault is systemic rather than per-turn: whatever wrote
// that predicate through the wrong lane is still doing it, to every turn it
// touches. Falling through to the other candidate would produce a smooth,
// plausible answer that hides a live data-integrity fault — on the surface a
// player trusts most, and with nothing anywhere to report it. Loud here, for the
// same reason it is loud in the admission gate and in the archive.
func (r *Results) Latest(ctx context.Context, playerID string) (*payload.TurnDelivery, error) {
	if err := types.ValidateEntityID(playerID); err != nil {
		return nil, fmt.Errorf("a result lookup needs a player entity id: %w", err)
	}
	player, err := r.turns.GetEntity(ctx, playerID)
	switch {
	case errors.Is(err, graphio.ErrEntityNotFound):
		return nil, fmt.Errorf("%w: player %s is not an entity of this world", ErrNoResult, playerID)
	case err != nil:
		return nil, fmt.Errorf("read player %s: %w", playerID, err)
	}

	var best *payload.TurnDelivery
	for _, turnEntityID := range candidateTurns(player) {
		turnID, err := turnIDOf(turnEntityID)
		if err != nil {
			// A pointer at something that is not a turn of this world. Skipped
			// rather than fatal: the other pointer may still answer, and refusing
			// outright would let one bad triple deny a player every result they
			// have.
			continue
		}
		state, err := r.read(ctx, turnEntityID)
		if errors.Is(err, ErrTurnNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		owner, err := resultOwner(state, turnEntityID)
		if err != nil {
			return nil, err
		}
		if owner != playerID {
			// A stale or corrupt pointer at another player's turn is not an
			// address this player may follow. The other candidate can still
			// answer, and no private artifact has been read.
			continue
		}
		delivery, err := r.deliverable(ctx, turnID, turnEntityID, state)
		if errors.Is(err, ErrNoResult) {
			// The turn is still running. Normal for the current pointer, and the
			// whole reason the resolved pointer exists.
			continue
		}
		if err != nil {
			return nil, err
		}
		if best == nil || delivery.Result.ResolvedAt.After(best.Result.ResolvedAt) {
			best = delivery
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w: player %s has finished no turn", ErrNoResult, playerID)
	}
	return best, nil
}

// resultOwner reads the authorization scalar written atomically when a turn is
// born. It deliberately performs no artifact reads.
func resultOwner(state *graph.EntityState, turnEntityID string) (string, error) {
	playerID, err := soleString(state, vocabulary.TurnActionPlayer)
	if err != nil {
		return "", err
	}
	if err := types.ValidateEntityID(playerID); err != nil {
		return "", fmt.Errorf("turn %s records an invalid player at %s: %w",
			turnEntityID, vocabulary.TurnActionPlayer, err)
	}
	return playerID, nil
}

// candidateTurns is the bounded set of turns a most-recent lookup will read.
//
// Both pointers, deduplicated, capped. Reading BOTH values of a pointer that
// holds two — rather than taking the first — is the same discipline every reader
// of a single-valued predicate in this engine follows: an append-lane write leaves
// two values with no error anywhere, and a reader that took one would answer a
// coin flip about what happened to this player last.
func candidateTurns(player *graph.EntityState) []string {
	held, _ := turn.HeldTurns(player)
	resolved, _ := turn.ResolvedTurns(player)

	seen := make(map[string]bool, len(held)+len(resolved))
	candidates := make([]string, 0, len(held)+len(resolved))
	for _, turnEntityID := range slices.Concat(resolved, held) {
		if seen[turnEntityID] || len(candidates) >= MaxLatestCandidates {
			continue
		}
		seen[turnEntityID] = true
		candidates = append(candidates, turnEntityID)
	}
	return candidates
}

// read fetches a turn entity, distinguishing absence from failure.
func (r *Results) read(ctx context.Context, turnEntityID string) (*graph.EntityState, error) {
	state, err := r.turns.GetEntity(ctx, turnEntityID)
	switch {
	case errors.Is(err, graphio.ErrEntityNotFound):
		return nil, fmt.Errorf("%w: %s", ErrTurnNotFound, turnEntityID)
	case err != nil:
		return nil, fmt.Errorf("read turn %s: %w", turnEntityID, err)
	case state.IsStub():
		// Queryable and factless. Something referenced this turn without it ever
		// being created, so there is no phase to read and no result to compose.
		return nil, fmt.Errorf("%w: %s is a referential stub", ErrTurnNotFound, turnEntityID)
	}
	return state, nil
}

// deliverable composes the result and resolves the prose it references.
func (r *Results) deliverable(
	ctx context.Context,
	turnID, turnEntityID string,
	state *graph.EntityState,
) (*payload.TurnDelivery, error) {
	result, err := r.compose(ctx, turnID, turnEntityID, state)
	if err != nil {
		return nil, err
	}
	delivery := &payload.TurnDelivery{Protocol: payload.PlayerProtocolV1, Result: result}
	if result.NarrationRef != "" {
		narration, err := r.narration(ctx, result.NarrationRef, turnID)
		if err != nil {
			return nil, err
		}
		delivery.Narration = narration
	}
	if err := delivery.Validate(); err != nil {
		return nil, fmt.Errorf("turn %s does not compose a coherent delivery: %w", turnEntityID, err)
	}
	return delivery, nil
}

// compose builds the canonical result from the turn entity.
//
// # The phase is read FIRST, and separately
//
// ledger.Compose refuses a non-terminal turn, but it refuses it as a generic
// error — and "your turn is still running" is the single most common answer this
// surface gives. Reading the phase here makes that a typed sentinel rather than a
// string a caller would have to sniff for.
//
// # Everything else is the archive's own composer
//
// ledger.Compose is pure and is what the campaign archive uses, so the result a
// player is shown and the manifest the archive holds are two readings of ONE
// record. A second composer here would be a second account of the same turn, and
// they would diverge on exactly the turns that are hardest to reason about — the
// failed ones.
func (r *Results) compose(
	ctx context.Context,
	turnID, turnEntityID string,
	state *graph.EntityState,
) (*payload.TurnResult, error) {
	phase, err := turn.PhaseOf(state, turnEntityID)
	if err != nil {
		return nil, err
	}
	if !phase.IsTerminal() {
		return nil, fmt.Errorf("%w: turn %s is %s", ErrNoResult, turnID, phase)
	}

	manifest, err := ledger.Compose(turnID, state, r.campaignID)
	if err != nil {
		return nil, err
	}
	result := &payload.TurnResult{
		Protocol:      payload.PlayerProtocolV1,
		TurnID:        manifest.TurnID,
		ActionID:      manifest.ActionID,
		PlayerID:      manifest.PlayerID,
		Phase:         manifest.Phase,
		NarrationRef:  manifest.NarrationRef,
		FailureReason: vocabulary.FailureReason(manifest.FailureReason),
		ResolvedAt:    manifest.RecordedAt,
	}
	if result.Resolution, err = r.resolution(ctx, state, manifest); err != nil {
		return nil, err
	}
	if result.CompanionResolution, err = r.companionResolution(ctx, state, manifest); err != nil {
		return nil, err
	}
	if err := result.Validate(); err != nil {
		return nil, fmt.Errorf("turn %s does not compose a coherent result: %w", turnEntityID, err)
	}
	return result, nil
}

func (r *Results) companionResolution(
	ctx context.Context,
	state *graph.EntityState,
	manifest *payload.TurnManifest,
) (*payload.CompanionResolution, error) {
	stageReference, err := soleString(state, vocabulary.TurnCompanionStageRef)
	if err != nil {
		return nil, err
	}
	if stageReference == "" {
		if manifest.Phase == vocabulary.PhaseComplete {
			return nil, fmt.Errorf("complete turn %s carries no %s",
				manifest.TurnID, vocabulary.TurnCompanionStageRef)
		}
		return nil, nil
	}
	stageRef, err := content.ParseRef(stageReference)
	if err != nil {
		return nil, fmt.Errorf("turn %s records an unresolvable companion stage reference: %w", manifest.TurnID, err)
	}
	record, err := r.artifacts.GetCompanionStageRecord(ctx, stageRef)
	if err != nil {
		return nil, fmt.Errorf("resolve the companion stage for turn %s: %w", manifest.TurnID, err)
	}
	if err := record.Validate(); err != nil {
		return nil, fmt.Errorf("turn %s companion stage at %s is invalid: %w", manifest.TurnID, stageRef, err)
	}
	if record.TurnID != manifest.TurnID || record.PlayerID != manifest.PlayerID {
		return nil, fmt.Errorf(
			"companion stage at %s belongs to turn %s/player %s, not turn %s/player %s",
			stageRef, record.TurnID, record.PlayerID, manifest.TurnID, manifest.PlayerID)
	}

	decisionReference, err := soleString(state, vocabulary.TurnCompanionDecisionRef)
	if err != nil {
		return nil, err
	}
	switch record.Status {
	case payload.CompanionStageNoActiveBond, payload.CompanionStageNoTrigger:
		if decisionReference != "" {
			return nil, fmt.Errorf(
				"turn %s companion stage status %q forbids decision reference %s",
				manifest.TurnID, record.Status, decisionReference)
		}
		return nil, nil
	case payload.CompanionStageDecision, payload.CompanionStageExhausted:
		if decisionReference == "" {
			return nil, fmt.Errorf("turn %s companion stage status %q requires a decision reference",
				manifest.TurnID, record.Status)
		}
	default:
		return nil, fmt.Errorf("turn %s companion stage has unsupported status %q", manifest.TurnID, record.Status)
	}
	if decisionReference != record.DecisionRef {
		return nil, fmt.Errorf(
			"turn %s companion decision reference %s disagrees with stage record reference %s",
			manifest.TurnID, decisionReference, record.DecisionRef)
	}
	decisionRef, err := content.ParseRef(decisionReference)
	if err != nil {
		return nil, fmt.Errorf("turn %s records an unresolvable companion decision reference: %w", manifest.TurnID, err)
	}
	decision, err := r.artifacts.GetCompanionDecision(ctx, decisionRef)
	if err != nil {
		return nil, fmt.Errorf("resolve the companion decision for turn %s: %w", manifest.TurnID, err)
	}
	if err := decision.Validate(); err != nil {
		return nil, fmt.Errorf("turn %s companion decision at %s is invalid: %w", manifest.TurnID, decisionRef, err)
	}
	if decision.TurnID != manifest.TurnID || decision.PlayerID != manifest.PlayerID ||
		decision.CompanionID != record.CompanionID {
		return nil, fmt.Errorf(
			"companion decision at %s has turn/player/companion %s/%s/%s, want %s/%s/%s",
			decisionRef, decision.TurnID, decision.PlayerID, decision.CompanionID,
			manifest.TurnID, manifest.PlayerID, record.CompanionID)
	}
	summary := &payload.CompanionResolution{
		CompanionID: decision.CompanionID,
		Kind:        decision.Kind,
		HintLevel:   decision.HintLevel,
	}
	if err := summary.Validate(); err != nil {
		return nil, fmt.Errorf("turn %s companion summary is invalid: %w", manifest.TurnID, err)
	}
	return summary, nil
}

// resolution builds the resolution card's data, or nil for a turn that was never
// adjudicated.
//
// The verdict half comes from the GRAPH's own scalars rather than from the stored
// verdict, which is deliberate and is the same choice ledger.rollGate makes: these
// are the exact values the rule pack routed the turn on, so the card shows what
// the engine ACTED on rather than a second opinion read from an artifact. It also
// keeps the verdict body — banded intents, rationale, the adjudicator's prose —
// out of a document headed for a player.
func (r *Results) resolution(
	ctx context.Context,
	state *graph.EntityState,
	manifest *payload.TurnManifest,
) (*payload.TurnResolution, error) {
	if manifest.RollGate == nil {
		// No verdict, so no resolution. A turn that failed before adjudication.
		return nil, nil
	}
	consequence, err := soleString(state, vocabulary.TurnVerdictConsequence)
	if err != nil {
		return nil, err
	}
	resolution := &payload.TurnResolution{
		Verdict: payload.VerdictScalars{
			Plausibility: manifest.RollGate.Plausibility,
			Risk:         manifest.RollGate.Risk,
			Consequence:  vocabulary.ConsequenceClass(consequence),
			RequiresRoll: manifest.RollGate.Reported,
		},
	}
	if resolution.Roll, err = r.roll(ctx, manifest); err != nil {
		return nil, err
	}
	if resolution.Band, err = actedBand(state, manifest, resolution.Verdict.RequiresRoll); err != nil {
		return nil, err
	}
	return resolution, nil
}

// roll reads the turn's dice back, or nil when the turn never reached them.
//
// The stored artifact rather than the graph's two roll scalars, because the card
// explains a total instead of asserting one: the mechanic, the raw dice and the
// typed modifiers are what make "9" legible, and none of them are rule-matching
// surface, so none of them are on the turn.
func (r *Results) roll(ctx context.Context, manifest *payload.TurnManifest) (*payload.TurnRoll, error) {
	if manifest.RollRef == "" {
		return nil, nil
	}
	ref, err := content.ParseRef(manifest.RollRef)
	if err != nil {
		return nil, fmt.Errorf("turn %s records an unresolvable roll reference: %w", manifest.TurnID, err)
	}
	stored, err := r.artifacts.GetRoll(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("resolve the roll for turn %s: %w", manifest.TurnID, err)
	}
	if stored.TurnID != manifest.TurnID {
		return nil, fmt.Errorf(
			"the roll at %s belongs to turn %s, not %s; a reference pointing at another turn's dice would "+
				"explain this turn's outcome with somebody else's numbers", ref, stored.TurnID, manifest.TurnID)
	}
	return &payload.TurnRoll{
		Mechanic:      stored.Mechanic,
		Dice:          stored.Dice,
		Modifiers:     stored.Modifiers,
		ModifierTotal: stored.ModifierTotal,
		Total:         stored.Total,
	}, nil
}

// actedBand is the outcome band the engine acted on, or empty when it never
// selected one.
//
// Three cases, and the empty one is a real answer rather than a gap. A turn that
// rolled records the band the dice selected. A no-roll turn's band is `auto`, and
// it is reported only once the applier has actually committed a batch — before
// that, no band has been acted on, and claiming one would tell a player the engine
// did something it had not. A turn that ended in between records neither.
//
// The derivation follows the applier's own (stage.committedBand) rather than
// inventing a second one; what differs is that this reads a turn that may have
// ended EARLY, so absence is answered instead of refused.
func actedBand(
	state *graph.EntityState,
	manifest *payload.TurnManifest,
	requiresRoll bool,
) (vocabulary.OutcomeBand, error) {
	recorded, err := soleString(state, vocabulary.TurnRollBand)
	if err != nil {
		return "", err
	}
	if recorded != "" {
		band, err := vocabulary.ParseOutcomeBand(recorded)
		if err != nil {
			return "", fmt.Errorf("turn %s: %w", manifest.TurnID, err)
		}
		if !band.IsRollBand() {
			return "", fmt.Errorf(
				"turn %s records roll band %q, which the dice cannot select", manifest.TurnID, band)
		}
		return band, nil
	}
	if !requiresRoll && manifest.EffectBatchRef != "" {
		return vocabulary.BandAuto, nil
	}
	return "", nil
}

// narration resolves the prose behind a result's reference.
//
// A MISSING object is its own sentinel and not a degraded delivery. The write
// ordering says prose lands before the reference is committed, so a reference to
// nothing means that ordering was violated or the object was removed — and a
// completed turn's result is unreadable without its prose, so shipping the
// document with a hole in it would hand the player a turn they cannot read while
// reporting success.
func (r *Results) narration(
	ctx context.Context,
	reference, turnID string,
) (*payload.DeliveredNarration, error) {
	ref, err := content.ParseRef(reference)
	if err != nil {
		return nil, fmt.Errorf("turn %s records an unresolvable narration reference: %w", turnID, err)
	}
	stored, err := r.artifacts.GetNarration(ctx, ref)
	if errors.Is(err, content.ErrArtifactNotFound) {
		return nil, fmt.Errorf("%w: turn %s references %s: %w", ErrNarrationMissing, turnID, ref, err)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve the narration for turn %s: %w", turnID, err)
	}
	return &payload.DeliveredNarration{
		TurnID: stored.TurnID,
		Band:   stored.Band,
		Prose:  stored.Prose,
	}, nil
}

// soleString reads a single-valued string predicate, empty when absent.
//
// Two values is an error and never a choice, for the reason ledger.soleTriple
// says: every predicate read here is single-valued, so a second value is the
// signature of an append-lane write and a reader that took the first would show
// the player a coin flip.
func soleString(state *graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	var found string
	count := 0
	for _, triple := range state.Triples {
		if triple.Predicate != predicate.String() {
			continue
		}
		count++
		value, ok := triple.Object.(string)
		if !ok {
			return "", fmt.Errorf("turn %s records a %T for %s, want a string", state.ID, triple.Object, predicate)
		}
		found = value
	}
	if count > 1 {
		return "", fmt.Errorf(
			"turn %s holds %d values for the single-valued %s; a fact written on an appending lane would be "+
				"reported to the player at random", state.ID, count, predicate)
	}
	return found, nil
}

// turnIDOf reads the turn identity out of a turn entity id, refusing anything
// that is not a turn of this world.
func turnIDOf(turnEntityID string) (string, error) {
	if err := types.ValidateEntityID(turnEntityID); err != nil {
		return "", fmt.Errorf("%q is not a canonical six-part entity id: %w", turnEntityID, err)
	}
	parts := strings.Split(turnEntityID, ".")
	if segment := parts[len(parts)-2]; segment != turn.TypeSegment {
		return "", fmt.Errorf(
			"%q has type segment %q rather than %q; only turns have a result", turnEntityID, segment, turn.TypeSegment)
	}
	turnID := parts[len(parts)-1]
	if err := payload.RequireTurnEntityID(turnID, turnEntityID); err != nil {
		return "", err
	}
	return turnID, nil
}
