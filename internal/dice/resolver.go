package dice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// ErrNoRollRequired reports a verdict whose REPORTED gate says the dice stay in
// the cup.
//
// It is a refusal, not a failure: a no-roll verdict is a normal turn that goes
// straight to its single auto band. The refusal exists so a mis-wired rule
// cannot make the dice run for a verdict that never asked for them.
var ErrNoRollRequired = errors.New("verdict reports no roll")

// TurnReader is the graph read surface the one-roll-per-turn guard needs.
// Satisfied by *graphio.Store.
type TurnReader interface {
	GetEntity(ctx context.Context, id string) (*graph.EntityState, error)
}

// Resolver runs the dice for one campaign.
//
// It holds the campaign seed for the life of the process, which is why it is
// built from a campaign.Instantiation: the seed reaches the dice from the gate
// that minted or loaded it, and never from a second generation.
type Resolver struct {
	roller     *Roller
	reader     TurnReader
	campaignID string
	seed       campaign.Seed
}

// NewResolver builds a resolver over a claimed campaign.
func NewResolver(roller *Roller, reader TurnReader, instantiation campaign.Instantiation) (*Resolver, error) {
	if roller == nil {
		return nil, errors.New("resolver requires a roller")
	}
	if reader == nil {
		return nil, errors.New("resolver requires a turn reader")
	}
	if instantiation.Seed.IsZero() {
		return nil, errors.New("resolver requires a campaign seed; the zero seed rolls the same dice forever")
	}
	if instantiation.CampaignID == "" {
		return nil, errors.New("resolver requires a campaign id")
	}
	return &Resolver{
		roller:     roller,
		reader:     reader,
		campaignID: instantiation.CampaignID,
		seed:       instantiation.Seed,
	}, nil
}

// Resolution is one resolve attempt's outcome.
type Resolution struct {
	// Roll is the turn's resolution record, freshly rolled or re-derived.
	Roll *payload.RollResult
	// Rolled reports that this call produced the turn's FIRST roll. False
	// means the turn already carried one and Roll is its re-derivation:
	// writing it again is convergent but unnecessary.
	Rolled bool
}

// Resolve returns the turn's roll, producing at most one per turn.
//
// The gate is the verdict's REPORTED requires_roll and nothing else. The
// (plausibility, risk) mapping is deliberately not consulted: the persona that
// read the fiction decides whether the dice come out, and a component that
// second-guessed it would take that authority back into a lookup table (D12).
//
// The at-most-one guarantee is held twice over, which is what makes it survive
// a duplicate rule trigger AND a crash-restart:
//
//   - A turn that already carries a roll is not re-rolled. Its recorded band
//     and total are cross-checked against a fresh derivation, so a duplicate
//     trigger both no-ops and proves the recorded roll is still the roll the
//     inputs produce.
//   - Even a caller that writes anyway converges, because the projection's
//     predicates are single-valued and the merge lane replaces them.
//
// Re-derivation is free — the roll is a pure function of the seed and the turn
// id — so the guard costs a graph read and buys an integrity check.
func (r *Resolver) Resolve(
	ctx context.Context,
	verdict *payload.Verdict,
	turnEntityID string,
) (Resolution, error) {
	if verdict == nil {
		return Resolution{}, errors.New("resolve requires a verdict")
	}
	if err := verdict.Validate(); err != nil {
		return Resolution{}, err
	}
	if !verdict.Scalars.RequiresRoll {
		return Resolution{}, fmt.Errorf("turn %s: %w", verdict.TurnID, ErrNoRollRequired)
	}

	state, err := r.reader.GetEntity(ctx, turnEntityID)
	if err != nil {
		return Resolution{}, fmt.Errorf("read turn entity %s: %w", turnEntityID, err)
	}
	// A referential stub is queryable and carries none of the entity's own
	// facts, so "no roll recorded" read off one would be a false negative — the
	// component would roll a second time for a turn that already had one.
	if state.IsStub() {
		return Resolution{}, fmt.Errorf(
			"turn entity %s is a referential stub: it holds no facts, so its roll state is unknown", turnEntityID)
	}

	roll, err := r.roller.Roll(r.seed, r.campaignID, verdict.TurnID, verdict.Modifiers)
	if err != nil {
		return Resolution{}, err
	}

	recorded, err := recordedRoll(state)
	if err != nil {
		return Resolution{}, fmt.Errorf("turn entity %s: %w", turnEntityID, err)
	}
	if recorded == nil {
		return Resolution{Roll: roll, Rolled: true}, nil
	}
	if err := recorded.agreesWith(roll); err != nil {
		return Resolution{}, fmt.Errorf("turn entity %s: %w", turnEntityID, err)
	}
	return Resolution{Roll: roll, Rolled: false}, nil
}

// recordedRollState is what a turn entity says about its roll.
type recordedRollState struct {
	band  string
	total int
}

// recordedRoll reads the roll a turn entity already carries, or nil.
//
// Every anomaly is an error rather than a shrug. A turn carrying two bands is
// the signature of a write that went down the APPEND lane instead of the
// replace lane; a turn carrying a band but no reference is a half-written
// record. Both are silent under a reader that takes the first value it finds.
func recordedRoll(state *graph.EntityState) (*recordedRollState, error) {
	bands := objectsFor(state, vocabulary.TurnRollBand)
	totals := objectsFor(state, vocabulary.TurnRollTotal)
	refs := objectsFor(state, vocabulary.TurnRollRef)

	present := 0
	for _, objects := range [][]any{bands, totals, refs} {
		if len(objects) > 1 {
			return nil, fmt.Errorf(
				"holds %d values for a single-valued roll predicate; single-valued facts must be written "+
					"on the replace lane, never appended", len(objects))
		}
		if len(objects) == 1 {
			present++
		}
	}
	switch present {
	case 0:
		return nil, nil
	case 3:
	default:
		return nil, fmt.Errorf(
			"carries %d of the 3 roll predicates; a roll record lands in one write or not at all", present)
	}

	band, ok := bands[0].(string)
	if !ok {
		return nil, fmt.Errorf("records a %T band, want a string", bands[0])
	}
	total, ok := asInt(totals[0])
	if !ok {
		return nil, fmt.Errorf("records a %T total, want a number", totals[0])
	}
	return &recordedRollState{band: band, total: total}, nil
}

// agreesWith cross-checks a recorded roll against a fresh derivation.
//
// A disagreement means the turn's recorded outcome is not the outcome its own
// inputs produce — the campaign seed changed, the mechanic's thresholds moved,
// or the verdict's modifiers are not the ones the roll was made with. Any of
// those makes the ledger's claim of replayability false, so it stops the turn
// instead of quietly preferring one answer.
func (s *recordedRollState) agreesWith(roll *payload.RollResult) error {
	if s.band != string(roll.Band) || s.total != roll.Total {
		return fmt.Errorf(
			"records band %q total %d, but its own inputs now produce band %q total %d; "+
				"the recorded roll is no longer reproducible",
			s.band, s.total, roll.Band, roll.Total)
	}
	return nil
}

// objectsFor returns every object a turn entity records for a predicate.
func objectsFor(state *graph.EntityState, predicate vocabulary.Predicate) []any {
	var out []any
	for _, triple := range state.Triples {
		if triple.Predicate == predicate.String() {
			out = append(out, triple.Object)
		}
	}
	return out
}

// asInt narrows a stored numeric object back to an int.
//
// The float64 case is not defensive padding: a triple object read back through
// the graph's JSON round trip arrives as float64, so an int comparison against
// the raw object would fail for every roll ever recorded. Non-integral values
// are refused rather than truncated — a total of 9.5 is a corrupted record, not
// a 9.
func asInt(object any) (int, bool) {
	switch value := object.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		if value != float64(int(value)) {
			return 0, false
		}
		return int(value), true
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	default:
		return 0, false
	}
}
