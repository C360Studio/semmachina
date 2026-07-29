package payload

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// MaxRationaleBytes bounds the adjudicator's free-text reasoning.
//
// The rationale is the largest LLM-authored string on the verdict and carries
// the same argument as MaxModifierNoteBytes: rule-opaque bounds what it can
// influence, not how big it can be, and the only thing that would eventually
// stop a runaway one is NATS's 1 MB max payload — long after the tokens were
// spent. It is also a spend signal in its own right: a persona writing an essay
// per turn is a prompt-and-completion cost curve, and cost is a policy rather
// than a forecast.
//
// 2 KiB is roughly 350 words: a paragraph of reasoning for the resolution card
// and the chronicler, not a transcript. A persona whose reasoning genuinely
// needs more room is producing prose, and prose has a home already — ObjectStore
// behind a ref-triple.
const MaxRationaleBytes = 2 * 1024

// VerdictScalars is the rule-matched projection of a verdict: the four small,
// closed-vocabulary values that become triples on the turn entity. Nothing
// else in a Verdict is ever rule-matched.
//
// This is the F6 split made structural rather than conventional. The upstream
// decide executor publishes a small metadata triple set onto the loop entity
// and leaves bulky fields in result content fetched by reference; a verdict's
// banded effect-intent set is bulky and is consumed only by the applier, so
// it follows the same discipline. Keeping the scalars in their own type means
// Triples() cannot accidentally grow to include a band: it only ever sees
// these four fields plus the reference.
type VerdictScalars struct {
	// Plausibility is whether the fiction admits the action.
	Plausibility vocabulary.Plausibility `json:"plausibility"`
	// Risk is what is at stake.
	Risk vocabulary.Risk `json:"risk"`
	// Consequence is the shape a miss or partial takes.
	Consequence vocabulary.ConsequenceClass `json:"consequence"`
	// RequiresRoll is the roll gate, AUTHORITATIVE as reported: fiction
	// decides, the mapping advises. Snake_case is legal here because this is
	// a payload field; its triple form is turn.verdict.requires-roll, since
	// underscores are illegal in predicates.
	RequiresRoll bool `json:"requires_roll"`
}

// Validate checks every scalar against its closed set.
//
// It deliberately does NOT check the reported roll gate against
// vocabulary.RequiresRoll. Narrative positioning dictates mechanics — the
// project's founding claim — so the persona that read the fiction holds the
// gate, and a verdict whose reported requires_roll disagrees with the mapping
// is valid and proceeds. The disagreement is recorded instead of rejected;
// see RollGate.
func (s VerdictScalars) Validate() error {
	if _, err := vocabulary.ParsePlausibility(string(s.Plausibility)); err != nil {
		return err
	}
	if _, err := vocabulary.ParseRisk(string(s.Risk)); err != nil {
		return err
	}
	if _, err := vocabulary.ParseConsequence(string(s.Consequence)); err != nil {
		return err
	}
	return nil
}

// RollGateExpectation records one verdict's reported roll gate beside the
// mapping's advice for the same classes, and the VERSION of the mapping that
// advice came from. It is a comparison, never a verdict on the verdict:
// Reported is what the engine acts on.
//
// This exists as a typed, serializable value rather than a log line because
// the divergence rate is the measurement that tells us whether the
// adjudicator's fiction judgment tracks the deterministic mapping — a quality
// and cost signal (a persona that calls for the dice far more often than the
// mapping expects is buying rolls with tokens). The ledger writer and any
// metrics consumer record it as data; nothing branches on Advised.
type RollGateExpectation struct {
	// Plausibility and Risk are the classes the comparison was made from,
	// carried so a recorded expectation is self-contained.
	Plausibility vocabulary.Plausibility `json:"plausibility"`
	Risk         vocabulary.Risk         `json:"risk"`
	// Reported is the adjudicator's authoritative gate — what ran.
	Reported bool `json:"reported"`
	// Advised is vocabulary.RequiresRoll's expectation for the same classes.
	Advised bool `json:"advised"`
	// Mapping is the VERSION of the advisory mapping Advised was computed
	// under.
	//
	// Without it this whole struct is redundant: the classes and the reported
	// gate are already on the stored verdict, so any reader could recompute
	// Advised on demand. The version is what makes the recomputation WRONG to
	// rely on. The mapping is advisory and expected to be tuned with play
	// (D12), so the day it changes, a derived Advised silently flips for every
	// turn already in the archive — a campaign's history quietly re-deciding
	// whether its adjudicator ever disagreed with the engine. Stamped, the
	// record says what was true at the time, and a reader can tell a v1
	// disagreement from a v2 one instead of averaging them.
	Mapping vocabulary.RollGateMapping `json:"mapping"`
}

// Agrees reports whether the persona's gate matched the mapping's advice.
func (e RollGateExpectation) Agrees() bool { return e.Reported == e.Advised }

// Validate holds a recorded expectation to its own contract.
//
// The advice is re-derived under the record's OWN mapping version, never under
// the engine's current one. That is the whole reason the version is on the
// record: checking it against today's table would re-decide a historical turn's
// disagreement every time the mapping is tuned — the archive quietly rewriting
// itself, which is the failure this field exists to prevent. Asking the named
// version instead means an old manifest stays verifiable against the table it
// actually ran under, and a version this engine no longer holds is refused
// outright rather than silently re-judged.
//
// What it deliberately does NOT check is Reported against Advised. A verdict
// whose reported gate disagrees with the mapping is VALID and proceeds (D12);
// the disagreement is the measurement, not a defect.
func (e RollGateExpectation) Validate() error {
	advised, err := vocabulary.AdviseRollGate(e.Mapping, e.Plausibility, e.Risk)
	if err != nil {
		return err
	}
	if advised != e.Advised {
		return fmt.Errorf(
			"roll gate records advised=%v under mapping %s, but that mapping advises %v for (%s, %s); "+
				"a recorded expectation is a comparison that happened, not one recomputed later",
			e.Advised, e.Mapping, advised, e.Plausibility, e.Risk)
	}
	return nil
}

// RollGate returns the reported gate beside the mapping's advice.
//
// The error is non-nil only when the scalars are outside the closed
// vocabulary, which Validate already rejects; a caller holding a validated
// verdict can treat it as total.
func (s VerdictScalars) RollGate() (RollGateExpectation, error) {
	advised, err := vocabulary.RequiresRoll(s.Plausibility, s.Risk)
	if err != nil {
		return RollGateExpectation{}, err
	}
	return RollGateExpectation{
		Plausibility: s.Plausibility,
		Risk:         s.Risk,
		Reported:     s.RequiresRoll,
		Advised:      advised,
		Mapping:      vocabulary.RollGateMappingVersion(),
	}, nil
}

// EffectBands groups proposed effect intents by outcome band. A roll-
// requiring verdict declares exactly miss, partial, and full (any of which
// may be empty); a no-roll verdict declares exactly auto.
//
// A map rather than four named fields, because "exactly these bands and no
// others" is the actual requirement, and a map makes an unexpected or missing
// band a validation failure instead of a silently-ignored field.
type EffectBands map[vocabulary.OutcomeBand][]EffectIntent

// Validate checks that the declared band set is exactly the one the verdict's
// REPORTED requires_roll calls for, and that every intent is in vocabulary.
// Using the reported gate (not the mapping's advice) is what keeps a verdict
// internally coherent without the engine ever requiring a band the persona
// did not author.
//
// A band key outside the vocabulary needs no separate check: the declared set
// must equal the required set exactly, so an unregistered key can only appear
// alongside a missing required one or an inflated count, both rejected below.
//
// Every loop walks `required` rather than the map, so a verdict with two bad
// bands reports the same one every time; ranging a map would make the
// rejection reason depend on Go's randomized iteration order.
func (b EffectBands) Validate(requiresRoll bool) error {
	required := vocabulary.BandsForVerdict(requiresRoll)
	if len(b) != len(required) {
		return fmt.Errorf("verdict declares %d bands, expected exactly %v", len(b), required)
	}
	for _, band := range required {
		if _, ok := b[band]; !ok {
			return fmt.Errorf("verdict is missing the %q band (expected exactly %v)", band, required)
		}
	}
	for _, band := range required {
		intents := b[band]
		for idx := range intents {
			if err := intents[idx].Validate(); err != nil {
				return fmt.Errorf("band %q intent %d: %w", band, idx, err)
			}
		}
	}
	return nil
}

// For returns the intents for one band. An unknown or undeclared band is an
// error rather than an empty result, because "this band declared nothing" and
// "this verdict has no such band" must not resolve to the same silent no-op
// in the applier.
func (b EffectBands) For(band vocabulary.OutcomeBand) ([]EffectIntent, error) {
	intents, ok := b[band]
	if !ok {
		return nil, fmt.Errorf("verdict declares no %q band", band)
	}
	return intents, nil
}

// Verdict is the fiction adjudicator's single structured exit for one turn.
// It is the only place in the loop where judgment about fiction becomes
// structure; after it lands, everything downstream is deterministic.
type Verdict struct {
	// TurnID is the turn this verdict resolves, 1:1 with ActionID.
	TurnID string `json:"turn_id"`
	// ActionID is the originating action.
	ActionID string `json:"action_id"`
	// SceneID is the scene the action occurred in.
	SceneID string `json:"scene_id"`

	// Scalars is the rule-matched projection — the only part that becomes
	// triples.
	Scalars VerdictScalars `json:"scalars"`

	// Modifiers are the typed roll adjustments the dice component sums.
	Modifiers []Modifier `json:"modifiers,omitempty"`

	// Bands is the bulky part: proposed intents per outcome band. Never
	// rule-matched, never a triple; carried by reference.
	Bands EffectBands `json:"bands"`

	// Rationale is the adjudicator's free-text reasoning. Rule-opaque — it
	// exists for the resolution card and the chronicler, and no rule or
	// component may branch on it — and bounded by MaxRationaleBytes.
	Rationale string `json:"rationale,omitempty"`
}

// Schema implements message.Payload.
func (v *Verdict) Schema() message.Type {
	return message.Type{Domain: Domain, Category: CategoryVerdict, Version: SchemaVersion}
}

// Validate implements message.Payload.
func (v *Verdict) Validate() error {
	if err := requireIDSegment("turn_id", v.TurnID); err != nil {
		return err
	}
	if err := requireActionID("action_id", v.ActionID); err != nil {
		return err
	}
	if err := requireEntityID("scene_id", v.SceneID); err != nil {
		return err
	}
	if err := v.Scalars.Validate(); err != nil {
		return err
	}
	if err := validateModifiers(v.Modifiers); err != nil {
		return err
	}
	if len(v.Rationale) > MaxRationaleBytes {
		return fmt.Errorf("rationale is %d bytes, which exceeds the %d-byte rationale budget",
			len(v.Rationale), MaxRationaleBytes)
	}
	return v.Bands.Validate(v.Scalars.RequiresRoll)
}

// MarshalJSON implements json.Marshaler. The alias avoids infinite recursion;
// the BaseMessage envelope is added by the publisher.
func (v *Verdict) MarshalJSON() ([]byte, error) {
	type Alias Verdict
	return json.Marshal((*Alias)(v))
}

// UnmarshalJSON implements json.Unmarshaler.
func (v *Verdict) UnmarshalJSON(data []byte) error {
	type Alias Verdict
	return json.Unmarshal(data, (*Alias)(v))
}

// Triples returns the verdict's rule-matched projection as triples on the
// turn entity, plus one reference to the stored verdict payload.
//
// This is the whole F6 discipline: four scalars land where rules can match
// them, and the banded intent set — which only the applier reads — stays behind
// verdictRef. Adding a band, a modifier note, or the rationale would put
// LLM-authored bulk into the graph's rule-matching surface, and the shared
// projection refuses it: the predicate list is vocabulary's, and a non-scalar
// object has no case in the shape gate.
func (v *Verdict) Triples(turnEntityID, verdictRef, source string, at time.Time) ([]message.Triple, error) {
	return tripleProjection{
		payload: v,
		subject: turnEntityID,
		turnID:  v.TurnID,
		source:  source,
		at:      at,

		registered: vocabulary.VerdictScalarPredicates(),
		objects: map[vocabulary.Predicate]any{
			vocabulary.TurnVerdictPlausibility: string(v.Scalars.Plausibility),
			vocabulary.TurnVerdictRisk:         string(v.Scalars.Risk),
			vocabulary.TurnVerdictConsequence:  string(v.Scalars.Consequence),
			vocabulary.TurnVerdictRequiresRoll: v.Scalars.RequiresRoll,
		},

		refPredicate: vocabulary.TurnVerdictRef,
		refName:      "verdict ref",
		ref:          verdictRef,
	}.build()
}
