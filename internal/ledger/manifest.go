package ledger

import (
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Compose builds one turn's manifest from the turn entity and the campaign it
// belongs to.
//
// It is a PURE function of its arguments — no clock, no store, no identifiers
// invented here — and that is what makes the writer idempotent without a
// transaction. Composing the same terminal turn twice yields a byte-identical
// manifest, so "this turn is already archived" and "the archive disagrees with
// the turn" are distinguishable answers rather than a comparison modulo
// whatever happened to differ.
//
// The real-time stamp comes from the TURN's terminal phase triple, not from
// now(). An archive's timestamp should say when the turn resolved, not when the
// archiver got round to it; and reading it from the record is also what makes
// recomposition exact.
//
// Everything else is read off the turn entity, which is deliberate: the turn is
// the authoritative record of what happened, and the manifest is an index into
// it. Nothing here follows a reference to a stored artifact — the refs are
// carried, not resolved — so archiving costs one graph read and cannot fail
// because an ObjectStore was briefly unreachable.
func Compose(turnID string, state *graph.EntityState, campaignID string) (*payload.TurnManifest, error) {
	if state == nil {
		return nil, fmt.Errorf("cannot archive turn %s: the graph returned nothing", turnID)
	}
	// A referential stub is queryable and carries none of the entity's own
	// facts, so every predicate below would read as absent and the manifest
	// would archive an empty turn as if that were the record.
	if state.IsStub() {
		return nil, fmt.Errorf(
			"turn entity %s is a referential stub: it holds no facts, so there is nothing to archive", state.ID)
	}
	// turn_id and the entity id are one turn wearing two shapes. The writer
	// reads the entity BY the id the resolved event named, so a mismatch here
	// means the graph answered with a different turn than the one asked about.
	if err := payload.RequireTurnEntityID(turnID, state.ID); err != nil {
		return nil, err
	}

	actionID, err := payload.ActionIDForTurn(turnID)
	if err != nil {
		return nil, err
	}

	phase, recordedAt, err := terminalPhase(state)
	if err != nil {
		return nil, err
	}
	manifest := &payload.TurnManifest{
		TurnID:     turnID,
		ActionID:   actionID,
		CampaignID: campaignID,
		Phase:      phase,
		RecordedAt: recordedAt,
		// The world clock is roadmap stage 8. The field is present and zero so
		// a reader written today needs no schema change when it arrives.
		WorldTime: 0,
	}

	if manifest.PlayerID, err = requiredString(state, vocabulary.TurnActionPlayer); err != nil {
		return nil, err
	}
	if manifest.SceneID, err = requiredString(state, vocabulary.TurnActionScene); err != nil {
		return nil, err
	}
	if manifest.ActionRef, err = requiredString(state, vocabulary.TurnActionRef); err != nil {
		return nil, err
	}

	// The optional half. Each of these is absent for a legitimate reason — a
	// turn that failed in adjudication has no roll, a no-roll verdict has no
	// roll either, a failed turn is never narrated — so absence is carried
	// through as an empty reference rather than refused.
	for _, optional := range []struct {
		predicate vocabulary.Predicate
		into      *string
	}{
		{vocabulary.TurnVerdictRef, &manifest.VerdictRef},
		{vocabulary.TurnRollRef, &manifest.RollRef},
		{vocabulary.TurnEffectsRef, &manifest.EffectBatchRef},
		{vocabulary.TurnNarrationRef, &manifest.NarrationRef},
		{vocabulary.TurnFailureReason, &manifest.FailureReason},
	} {
		value, err := optionalString(state, optional.predicate)
		if err != nil {
			return nil, err
		}
		*optional.into = value
	}

	if manifest.RollGate, err = rollGate(state); err != nil {
		return nil, err
	}

	// The manifest's own contract is the last gate, and it is the one that
	// catches an incoherent turn: a complete turn with no narration reference,
	// a failed one with no reason, a recorded gate whose advice its own mapping
	// version does not give.
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("turn %s does not compose a coherent ledger record: %w", state.ID, err)
	}
	return manifest, nil
}

// terminalPhase reads the turn's phase and the moment it was written.
//
// Non-terminal is an error rather than a manifest, because the ledger's unit is
// a RESOLVED turn: archiving a turn mid-flight would record an outcome that has
// not happened yet, and the archive is append-only, so nothing would ever
// correct it.
func terminalPhase(state *graph.EntityState) (vocabulary.TurnPhase, time.Time, error) {
	triple, err := soleTriple(state, vocabulary.TurnPhaseCurrent)
	if err != nil {
		return "", time.Time{}, err
	}
	if triple == nil {
		return "", time.Time{}, fmt.Errorf(
			"turn entity %s carries no %s; a turn without a phase cannot be archived or diagnosed",
			state.ID, vocabulary.TurnPhaseCurrent)
	}
	value, ok := triple.Object.(string)
	if !ok {
		return "", time.Time{}, fmt.Errorf(
			"turn entity %s records a %T phase, want a string", state.ID, triple.Object)
	}
	phase, err := vocabulary.ParseTurnPhase(value)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("turn entity %s: %w", state.ID, err)
	}
	if !phase.IsTerminal() {
		return "", time.Time{}, fmt.Errorf(
			"turn entity %s is in phase %q, which is not terminal; the ledger archives resolved turns and an "+
				"append-only record of an unfinished one could never be corrected", state.ID, phase)
	}
	if triple.Timestamp.IsZero() {
		return "", time.Time{}, fmt.Errorf(
			"turn entity %s records phase %q with no timestamp, so when it resolved is unknown", state.ID, phase)
	}
	return phase, triple.Timestamp.UTC(), nil
}

// rollGate reconstructs the roll-gate agreement from the verdict scalars the
// turn carries.
//
// It reads the GRAPH's copy rather than following the verdict reference, and
// that is deliberate on two counts: these are the exact values the rule pack
// routed the turn on — so the archive records what the engine acted on, not a
// second opinion — and composing a manifest stays one graph read.
//
// All three scalars or none. A turn holding some of them is a half-written
// verdict, and picking a default for the missing one would archive a gate
// nobody reported.
func rollGate(state *graph.EntityState) (*payload.RollGateExpectation, error) {
	plausibility, err := optionalString(state, vocabulary.TurnVerdictPlausibility)
	if err != nil {
		return nil, err
	}
	risk, err := optionalString(state, vocabulary.TurnVerdictRisk)
	if err != nil {
		return nil, err
	}
	reported, present, err := optionalBool(state, vocabulary.TurnVerdictRequiresRoll)
	if err != nil {
		return nil, err
	}

	found := 0
	for _, has := range []bool{plausibility != "", risk != "", present} {
		if has {
			found++
		}
	}
	switch found {
	case 0:
		// A turn that failed before adjudication. No verdict, no gate; the
		// manifest says so by carrying none.
		return nil, nil
	case 3:
	default:
		return nil, fmt.Errorf(
			"turn entity %s carries %d of the 3 verdict scalars; they land in one write, so a partial set is a "+
				"record no gate can be read off", state.ID, found)
	}

	scalars := payload.VerdictScalars{
		Plausibility: vocabulary.Plausibility(plausibility),
		Risk:         vocabulary.Risk(risk),
		RequiresRoll: reported,
	}
	gate, err := scalars.RollGate()
	if err != nil {
		return nil, fmt.Errorf("turn entity %s: %w", state.ID, err)
	}
	return &gate, nil
}

// soleTriple returns the one triple an entity records for a predicate, or nil.
//
// Two is an error and never a choice. Every predicate the ledger reads is
// single-valued, so a second value is the signature of a write that took an
// appending lane (F14) — and a reader that took the first would archive a coin
// flip as the campaign's permanent record of what happened.
func soleTriple(state *graph.EntityState, predicate vocabulary.Predicate) (*message.Triple, error) {
	var found *message.Triple
	count := 0
	for idx := range state.Triples {
		if state.Triples[idx].Predicate != predicate.String() {
			continue
		}
		count++
		found = &state.Triples[idx]
	}
	if count > 1 {
		return nil, fmt.Errorf(
			"turn entity %s holds %d values for the single-valued %s; a fact written on an appending lane "+
				"would be archived at random", state.ID, count, predicate)
	}
	return found, nil
}

// optionalString reads a single-valued string predicate, empty when absent.
func optionalString(state *graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	triple, err := soleTriple(state, predicate)
	if err != nil || triple == nil {
		return "", err
	}
	value, ok := triple.Object.(string)
	if !ok {
		return "", fmt.Errorf(
			"turn entity %s records a %T for %s, want a string", state.ID, triple.Object, predicate)
	}
	return value, nil
}

// requiredString reads a single-valued string predicate every resolved turn
// must carry: the two identity references written into the turn's atomic birth
// record, and the pointer at the player's own words.
func requiredString(state *graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	value, err := optionalString(state, predicate)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf(
			"turn entity %s carries no %s; it is written by the turn's atomic create, so a resolved turn "+
				"without one is a record the archive cannot stand behind", state.ID, predicate)
	}
	return value, nil
}

// optionalBool reads a single-valued boolean predicate.
//
// present is reported separately because false and absent are different facts
// here: `requires_roll = false` is a verdict that declined the dice, and an
// absent predicate is a turn that was never adjudicated at all.
func optionalBool(state *graph.EntityState, predicate vocabulary.Predicate) (value, present bool, err error) {
	triple, err := soleTriple(state, predicate)
	if err != nil || triple == nil {
		return false, false, err
	}
	flag, ok := triple.Object.(bool)
	if !ok {
		return false, false, fmt.Errorf(
			"turn entity %s records a %T for %s, want a bool", state.ID, triple.Object, predicate)
	}
	return flag, true, nil
}
