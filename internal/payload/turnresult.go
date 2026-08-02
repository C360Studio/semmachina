package payload

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// TurnRoll is the dice half of a resolution card: what was thrown, what adjusted
// it, and what it came to.
//
// It carries the MECHANIC and not the RNG version, and the split is the audience.
// The mechanic is what makes the numbers legible — a total of 9 means nothing
// without knowing it came from 2d6 and where that mechanic's band thresholds
// sit — while the RNG version exists to re-execute a roll byte-identically, and
// a player replays nothing. The ledger's RollResult keeps both.
type TurnRoll struct {
	// Mechanic is the versioned resolution mechanic the dice were thrown under.
	Mechanic vocabulary.Mechanic `json:"mechanic"`
	// Dice are the raw die faces, in roll order.
	Dice []int `json:"dice"`
	// Modifiers are the verdict's typed adjustments, carried so the card can
	// explain the total rather than assert it. Each carries a closed-vocabulary
	// source and an optional bounded note.
	Modifiers []Modifier `json:"modifiers,omitempty"`
	// ModifierTotal is the summed modifiers.
	ModifierTotal int `json:"modifier_total"`
	// Total is dice plus ModifierTotal.
	Total int `json:"total"`
}

// Validate recomputes the arithmetic under the roll's own mechanic rather than
// trusting it, sharing validateRollArithmetic with the ledger's RollResult so
// the card and the archive cannot disagree about one roll.
func (r *TurnRoll) Validate() error {
	spec, err := vocabulary.MechanicSpecFor(r.Mechanic)
	if err != nil {
		return err
	}
	return validateRollArithmetic(spec, r.Dice, r.Modifiers, r.ModifierTotal, r.Total)
}

// TurnResolution is why the turn came out the way it did, in the shape the
// resolution card names: plausibility, risk, consequence, modifiers, roll, band.
//
// Shaping it that way now is deliberate and free. The card is a later milestone
// (project.md Sequencing stage 3) and nothing here renders one, but the fields a
// client speaks are the single most expensive thing in the system to change, and
// a protocol that could not express the card would force a version bump on every
// client the day the card ships.
//
// Every value in it is answerable from what the ledger already archives: the
// verdict scalars land on the turn entity in one write (and a manifest records
// the roll gate), and the roll is on the stored RollResult behind the manifest's
// roll_ref.
type TurnResolution struct {
	// Verdict is the adjudicator's four rule-matched scalars, reused from the
	// verdict's own projection rather than restated, so the classes a player is
	// shown are exactly the classes the rule pack routed the turn on.
	//
	// Its RequiresRoll is a plain bool whose zero value is a legitimate answer,
	// and that is safe only because presence rides on the POINTER to this
	// struct: a turn that was never adjudicated carries no TurnResolution at
	// all, and the three scalars land together or not at all (the ledger refuses
	// a partial set), so there is no state in which "false" has to stand in for
	// "unknown".
	Verdict VerdictScalars `json:"verdict"`

	// Band is the outcome band the engine ACTED on — the band whose effect
	// intents the applier committed.
	//
	// It is derivable (auto when the gate declined the dice, otherwise the roll's
	// total under its mechanic) and is carried anyway, for the reason
	// RollGateExpectation.Mapping is carried: a derived value re-decides history
	// when the table behind it moves, and a card that showed a band different
	// from the one whose effects are on the world would be a lie with no
	// reporter. Validate holds the two together instead.
	//
	// EMPTY is a real state and means the turn ended before an outcome was
	// selected — a turn stranded after adjudication and before the dice. It is
	// legal only on a failed turn.
	Band vocabulary.OutcomeBand `json:"band,omitempty"`

	// Roll is present exactly when dice were thrown. A no-roll verdict resolves
	// in the auto band and has none; nil is that fact rather than an empty roll,
	// which would render as a card claiming a total of zero.
	Roll *TurnRoll `json:"roll,omitempty"`
}

// CompanionResolution is the player-facing structural summary of a committed
// companion decision. Companion dialogue remains in the referenced narration;
// this summary carries only the bounded fields a client can reason about.
type CompanionResolution struct {
	CompanionID string                `json:"companion_id"`
	Kind        CompanionDecisionKind `json:"kind"`
	HintLevel   vocabulary.HintLevel  `json:"hint_level,omitempty"`
}

// Validate enforces the same closed decision vocabulary as the committed
// CompanionDecision and makes hint_level present exactly for a hint.
func (r *CompanionResolution) Validate() error {
	if r == nil {
		return errors.New("companion resolution is required")
	}
	if err := requireEntityID("companion_id", r.CompanionID); err != nil {
		return err
	}
	if !slices.Contains(companionDecisionKinds, r.Kind) {
		return fmt.Errorf("kind %q is not a registered companion decision kind", r.Kind)
	}
	if r.Kind == CompanionDecisionHint {
		if _, err := vocabulary.ParseHintLevel(string(r.HintLevel)); err != nil {
			return fmt.Errorf("hint_level is required and must be closed when kind is hint: %w", err)
		}
		return nil
	}
	if r.HintLevel != "" {
		return fmt.Errorf("hint_level is forbidden when kind is %q", r.Kind)
	}
	return nil
}

// Validate checks the closed vocabularies and — the part that matters — the
// coherence between the gate, the band, and the dice.
//
// outcomeRequired is the caller's phase knowledge: a completed turn selected a
// band by definition, because the applier committed that band's intents, while a
// failed one may have ended before anything was selected. It is a parameter
// rather than a field for the same reason EffectBands.Validate takes the gate:
// the constraint belongs to the enclosing record, and a copy of the phase in
// here would be a second place for it to disagree.
func (r *TurnResolution) Validate(outcomeRequired bool) error {
	if err := r.Verdict.Validate(); err != nil {
		return err
	}

	if r.Band == "" {
		if outcomeRequired {
			return errors.New(
				"a completed turn records no outcome band; the applier commits the intents of exactly one band, " +
					"so a turn that completed selected one")
		}
		if r.Roll != nil {
			return errors.New(
				"a resolution carries a roll but no band; the band is what the roll SELECTED, so dice with no " +
					"band is a resolution that threw them and discarded the answer")
		}
		return nil
	}

	band, err := vocabulary.ParseOutcomeBand(string(r.Band))
	if err != nil {
		return err
	}

	if r.Roll == nil {
		if band != vocabulary.BandAuto {
			return fmt.Errorf(
				"band %q is selected by dice, but the resolution carries no roll; only %q resolves without one",
				band, vocabulary.BandAuto)
		}
		if r.Verdict.RequiresRoll {
			return fmt.Errorf(
				"the verdict reported that this action requires a roll, but the resolution is banded %q; "+
					"auto is the band of a verdict that declined the dice", vocabulary.BandAuto)
		}
		return nil
	}

	if !band.IsRollBand() {
		return fmt.Errorf("band %q is not selectable by a roll, but the resolution carries one", band)
	}
	if !r.Verdict.RequiresRoll {
		return errors.New(
			"the resolution carries a roll, but the verdict reported that this action requires none; the " +
				"reported gate is what the engine acts on, so dice it did not call for came from somewhere else")
	}
	if err := r.Roll.Validate(); err != nil {
		return err
	}
	spec, err := vocabulary.MechanicSpecFor(r.Roll.Mechanic)
	if err != nil {
		return err
	}
	return requireBandForTotal(spec, r.Roll.Total, band)
}

// TurnResult is the canonical terminal result of one turn: the public
// counterpart of PlayerAction, and the ONE type a client receives for both a
// turn that completed and a turn that failed.
//
// One type for both endings is the point. A client must not have to guess which
// shape arrived, so Phase is the discriminator and it is always one of the two
// terminal phases; from it every other field's presence follows:
//
//   - complete — narration_ref and resolution are present, failure_reason is not.
//   - failed   — failure_reason is present as a CLOSED code; narration and
//     resolution are present or absent according to how far the turn got.
//
// TERMINAL rather than completed, deliberately. A failed turn is the one a
// player most needs an answer about, and a result surface that only spoke for
// completed turns would leave them with silence indistinguishable from a turn
// still running.
//
// A failed turn is allowed narration and a resolution because failed turns
// really do carry them. The stranded-turn pass ends a turn parked in `narrating`
// after its re-trigger budget is gone even when the narration reference has
// already landed (see internal/resume), and a turn can fail during effect commit
// long after the dice — so forbidding either would make real turns
// unrepresentable and would throw away prose the player has already earned.
//
// It carries NO channel binding, deliberately. Delivery resolves the player's
// current connection from PlayerID at DELIVERY time; a binding captured at
// submission is stale after a reconnect, and a result that carried one would
// invite exactly the delivery-to-a-dead-connection this design exists to avoid.
type TurnResult struct {
	// Protocol is the version of the public player protocol this result is
	// written against.
	Protocol PlayerProtocolVersion `json:"protocol"`

	// TurnID is the turn, and ActionID the action that caused it. Both are
	// retrieval keys, and both are carried rather than one being left to the
	// client to derive: the derivation is the engine's (TurnIDForAction), and a
	// client re-implementing it would be a second statement of the pairing.
	// Validate holds them to it.
	TurnID   string `json:"turn_id"`
	ActionID string `json:"action_id"`

	// PlayerID is the graph entity this result belongs to — never a connection
	// identifier. It is what targeted egress resolves a connection FROM, which
	// is why it survives reconnects and a connection id would not.
	PlayerID string `json:"player_id"`

	// Phase is the terminal phase, and the discriminator for everything below.
	Phase vocabulary.TurnPhase `json:"phase"`

	// Resolution is why the turn came out the way it did. Required on a
	// completed turn; present on a failed one only if the turn was adjudicated
	// before it died.
	Resolution *TurnResolution `json:"resolution,omitempty"`

	// CompanionResolution summarizes a committed active-companion decision.
	// It is absent for no active bond and for an active bond with no structured
	// trigger; even a committed silent decision remains present.
	CompanionResolution *CompanionResolution `json:"companion_resolution,omitempty"`

	// NarrationRef references the narration prose in the content store.
	//
	// A REFERENCE and not the prose. Prose is bulky and content-addressed, and
	// putting it inline would make every result message unbounded and would put
	// a second copy of the campaign's fiction on the wire beside the one the
	// store already holds. Adapters differ in what they do with it — a WebSocket
	// client cannot resolve an obj:// reference and its adapter must, while an
	// email adapter renders the prose into a body — and that per-adapter
	// resolution is exactly what "player I/O is adapter-shaped" means.
	NarrationRef string `json:"narration_ref,omitempty"`

	// FailureReason is the closed code a failed turn ended with. A CODE and
	// never a sentence, for the reason vocabulary.FailureReason exists: the
	// explanatory detail is prose behind turn.failure.ref, and a protocol that
	// carried it inline would put engine- and persona-authored text in front of
	// a player as if it were the fiction.
	FailureReason vocabulary.FailureReason `json:"failure_reason,omitempty"`

	// ResolvedAt is when the turn's terminal phase was written — not when this
	// result was composed and not when it was delivered.
	//
	// The distinction is load-bearing for email-cadence play: a result retrieved
	// days later must still say when the turn actually resolved, and a delivery
	// stamp would quietly make retrieval time part of the record.
	ResolvedAt time.Time `json:"resolved_at"`
}

// Schema implements message.Payload.
func (r *TurnResult) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryTurnResult, Version: SchemaVersion}
}

// Validate implements message.Payload.
func (r *TurnResult) Validate() error {
	if _, err := ParsePlayerProtocolVersion(string(r.Protocol)); err != nil {
		return err
	}
	// Turn and action are 1:1 by a derivation the engine owns. A result naming
	// turn A and action B addresses two turns and is retrievable by a key that
	// answers with the other one's outcome.
	//
	// This pair is the WHOLE of both identity checks, deliberately.
	// ActionIDForTurn validates turn_id as an entity-ID segment and validates the
	// action id it derives, and the equality below pins ActionID to that
	// derivation — so separate requireIDSegment and requireActionID calls in
	// front of it would be unreachable. No input exists that either would refuse
	// and this pair would not, and a check nothing can reach reads as coverage
	// and is not. (Both were written, and both survived mutation; that is how
	// they were found.)
	derived, err := ActionIDForTurn(r.TurnID)
	if err != nil {
		return err
	}
	if derived != r.ActionID {
		return fmt.Errorf(
			"turn %q derives action id %q, but this result names action %q; turn and action are 1:1 and the "+
				"derivation is the only thing that pairs them", r.TurnID, derived, r.ActionID)
	}
	if err := requireEntityID("player_id", r.PlayerID); err != nil {
		return err
	}

	phase, err := vocabulary.ParseTurnPhase(string(r.Phase))
	if err != nil {
		return err
	}
	if !phase.IsTerminal() {
		return fmt.Errorf(
			"phase %q is not terminal; a result is what a turn ENDED as, and answering with a phase a turn is "+
				"still passing through would tell a player their turn is over when it is running", phase)
	}

	switch phase {
	case vocabulary.PhaseComplete:
		if r.FailureReason != "" {
			return errors.New("a complete turn must not carry failure_reason")
		}
		if r.NarrationRef == "" {
			return errors.New(
				"a complete turn requires narration_ref; the turn closes on the narration landing, so a " +
					"completed turn with no prose to show is a result the player cannot read")
		}
		if r.Resolution == nil {
			return errors.New(
				"a complete turn requires resolution; it was adjudicated and banded by definition, and the " +
					"resolution is what makes the outcome legible rather than arbitrary")
		}
	case vocabulary.PhaseFailed:
		// The closed code, never a sentence, and never absent: bounded execution
		// promises a turn never ends in silence, and an unexplained failed turn
		// is that promise unmet at the one surface the player reads.
		if _, err := vocabulary.ParseFailureReason(string(r.FailureReason)); err != nil {
			return err
		}
	}

	if r.NarrationRef != "" {
		if err := RequireStorageRef("narration_ref", r.NarrationRef); err != nil {
			return err
		}
	}
	if r.Resolution != nil {
		if err := r.Resolution.Validate(phase == vocabulary.PhaseComplete); err != nil {
			return fmt.Errorf("resolution: %w", err)
		}
	}
	if r.CompanionResolution != nil {
		if err := r.CompanionResolution.Validate(); err != nil {
			return fmt.Errorf("companion_resolution: %w", err)
		}
	}
	if r.ResolvedAt.IsZero() {
		return errors.New("resolved_at is required")
	}
	return nil
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion;
// the BaseMessage envelope is added by the publisher.
func (r *TurnResult) MarshalJSON() ([]byte, error) {
	type Alias TurnResult
	return json.Marshal((*Alias)(r))
}

// UnmarshalJSON implements json.Unmarshaler.
//
// Deliberately permissive about fields it does not know, which is the opposite
// of SubmitAction's unmarshaler and is the protocol's stated asymmetry (see
// PlayerProtocolVersion): a later version may ADD result fields, and a client or
// adapter built against v1 must keep working when it meets one.
func (r *TurnResult) UnmarshalJSON(data []byte) error {
	type Alias TurnResult
	return json.Unmarshal(data, (*Alias)(r))
}
