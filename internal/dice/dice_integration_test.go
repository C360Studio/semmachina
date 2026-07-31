package dice_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	graphingest "github.com/c360studio/semstreams/processor/graph-ingest"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// "Exactly one roll-result triple per turn" is a claim about the GRAPH, and the
// graph is where it can be false. The mutation API offers two lanes that both
// accept the same triples: one merges by (subject, predicate) and one appends.
// Picking the wrong one leaves a turn holding two bands, both stored, with no
// error raised anywhere — so the property is proven against real graph-ingest
// rather than against a fake that has one lane.

func TestMain(m *testing.M) { os.Exit(testinfra.RunTests(m)) }

const integrationTurnEntity = "c360.semmachina.diceworld1.starter.turn.turn-act-1"

func realStore(t *testing.T) (*graphio.Store, *testinfra.Harness) {
	t.Helper()
	harness := testinfra.Require(t)
	store, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, harness
}

// createTurnEntity puts a turn in the graph in its resolving phase, the way the
// turn-loop's phase management will.
func createTurnEntity(t *testing.T, store *graphio.Store, id string) {
	t.Helper()
	if _, err := store.CreateEntity(t.Context(), &graph.EntityState{
		ID:          id,
		MessageType: turnEntity().MessageType,
		Version:     1,
		UpdatedAt:   resolveTime,
		Triples: []message.Triple{{
			Subject:    id,
			Predicate:  vocabulary.TurnPhaseCurrent.String(),
			Object:     string(vocabulary.PhaseResolving),
			Source:     dice.Source,
			Timestamp:  resolveTime,
			Confidence: 1.0,
		}},
	}); err != nil {
		t.Fatalf("create turn entity: %v", err)
	}
}

func TestIntegration_ARollTriggerDeliveredTwiceLeavesExactlyOneRoll(t *testing.T) {
	store, harness := realStore(t)
	createTurnEntity(t, store, integrationTurnEntity)

	seed := seedOf(0x05)
	verdict := rollingVerdict()
	resolver, err := dice.NewResolver(newRoller(t), store, instantiationFor(seed))
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// First delivery: the turn has no roll, so the dice run.
	first, err := resolver.Resolve(t.Context(), verdict, integrationTurnEntity)
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if !first.Rolled {
		t.Fatal("the first trigger did not roll")
	}
	writeRoll(t, store, first)

	// Second delivery of the same trigger: the guard must see the recorded
	// roll and no-op.
	second, err := resolver.Resolve(t.Context(), verdict, integrationTurnEntity)
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if second.Rolled {
		t.Fatal("a duplicate trigger rolled the turn a second time")
	}
	if second.Roll.Total != first.Roll.Total || second.Roll.Band != first.Roll.Band {
		t.Fatalf("the duplicate trigger produced %d/%s, the first produced %d/%s",
			second.Roll.Total, second.Roll.Band, first.Roll.Total, first.Roll.Band)
	}

	// And a caller that wrote anyway must still converge: single-valued
	// predicates replace on the merge lane.
	writeRoll(t, store, second)

	state := harness.AwaitEntity(t, integrationTurnEntity)
	for _, predicate := range []vocabulary.Predicate{
		vocabulary.TurnRollBand, vocabulary.TurnRollTotal, vocabulary.TurnRollRef,
	} {
		objects := testinfra.ObjectsFor(state, predicate.String())
		if len(objects) != 1 {
			t.Fatalf("the turn holds %d values for %s after two writes: %v", len(objects), predicate, objects)
		}
	}
	if got := testinfra.FirstObject(state, vocabulary.TurnRollBand.String()); got != string(first.Roll.Band) {
		t.Fatalf("the graph records band %v, the roll produced %q", got, first.Roll.Band)
	}
}

// The total has to survive the graph as a NUMBER: the rule engine's threshold
// operators compare numerically, and a total stored as "8" would write cleanly
// and make every threshold rule over it quietly never fire.
func TestIntegration_TheRecordedTotalStaysNumericThroughTheGraph(t *testing.T) {
	const turnID = "c360.semmachina.diceworld2.starter.turn.turn-act-1"
	store, harness := realStore(t)
	createTurnEntity(t, store, turnID)

	resolver, err := dice.NewResolver(newRoller(t), store, instantiationFor(seedOf(0x13)))
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	resolution, err := resolver.Resolve(t.Context(), rollingVerdict(), turnID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	writeRollTo(t, store, turnID, resolution)

	state := harness.AwaitEntity(t, turnID)
	stored := testinfra.FirstObject(state, vocabulary.TurnRollTotal.String())
	number, ok := stored.(float64)
	if !ok {
		t.Fatalf("the graph stored the total as %T (%v); a rule's numeric comparison would never match", stored, stored)
	}
	if int(number) != resolution.Roll.Total {
		t.Fatalf("the graph stored total %v, the roll produced %d", number, resolution.Roll.Total)
	}
}

// The bulky half of a roll must not be in the graph at all: only the two
// rule-matched scalars and the reference land.
func TestIntegration_OnlyTheRuleMatchedScalarsAndTheReferenceReachTheGraph(t *testing.T) {
	const turnID = "c360.semmachina.diceworld3.starter.turn.turn-act-1"
	store, harness := realStore(t)
	createTurnEntity(t, store, turnID)

	resolver, err := dice.NewResolver(newRoller(t), store, instantiationFor(seedOf(0x17)))
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	resolution, err := resolver.Resolve(t.Context(), rollingVerdict(), turnID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	writeRollTo(t, store, turnID, resolution)

	state := harness.AwaitEntity(t, turnID)
	for _, triple := range state.Triples {
		if !strings.HasPrefix(triple.Predicate, "turn.roll.") {
			continue
		}
		switch triple.Predicate {
		case vocabulary.TurnRollBand.String(), vocabulary.TurnRollTotal.String(), vocabulary.TurnRollRef.String():
		default:
			t.Fatalf("the roll wrote %q into the graph; everything replay needs rides behind the reference",
				triple.Predicate)
		}
	}
	// The phase predicate written at turn creation must survive the merge —
	// the roll replaces its OWN predicates, not the entity's other facts.
	if got := testinfra.FirstObject(state, vocabulary.TurnPhaseCurrent.String()); got != string(vocabulary.PhaseResolving) {
		t.Fatalf("the roll write clobbered the turn phase: %v", got)
	}
}

// The other half of the lane argument, pinned against the real broker: the
// APPEND lane really does leave a turn holding two bands, with a success
// response and no error anywhere. Choosing between the two lanes is therefore a
// correctness decision, not a performance one — and if a future semstreams
// version changes this, the reason MergeTriples exists changes with it and this
// test says so.
func TestIntegration_TheAppendLaneLeavesTwoBandsWhichIsWhyTheMergeLaneIsUsed(t *testing.T) {
	const turnID = "c360.semmachina.diceworld4.starter.turn.turn-act-1"
	store, harness := realStore(t)
	createTurnEntity(t, store, turnID)

	resolver, err := dice.NewResolver(newRoller(t), store, instantiationFor(seedOf(0x19)))
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	resolution, err := resolver.Resolve(t.Context(), rollingVerdict(), turnID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	triples, err := resolution.Roll.Triples(turnID, testRollRef, dice.Source, resolveTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}

	for attempt := range 2 {
		// beta.159 deduplicates an identical six-field tuple on the add lane.
		// Give these occurrences distinct correlation contexts while keeping
		// source and the single-valued predicate/object identical: that is the anomaly the
		// merge lane prevents, not byte-identical redelivery.
		for idx := range triples {
			triples[idx].Context = fmt.Sprintf("append-control-%d", attempt)
		}
		request, err := json.Marshal(graph.AddTriplesBatchRequest{Triples: triples})
		if err != nil {
			t.Fatalf("encode add_batch: %v", err)
		}
		reply, err := harness.Client.RequestClassified(
			t.Context(), graphingest.SubjectTripleAddBatch, request, 5*time.Second)
		if err != nil {
			t.Fatalf("add_batch attempt %d: %v", attempt+1, err)
		}
		var response graph.AddTriplesBatchResponse
		if err := json.Unmarshal(reply, &response); err != nil {
			t.Fatalf("decode add_batch response: %v", err)
		}
		if response.WrittenCount != len(triples) || response.Deduplicated != 0 ||
			len(response.FailedSubjects) != 0 {
			t.Fatalf("distinct-context add response = %+v, want written=%d deduplicated=0 and no failures",
				response, len(triples))
		}
	}

	state := harness.AwaitEntity(t, turnID)
	bands := testinfra.ObjectsFor(state, vocabulary.TurnRollBand.String())
	if len(bands) < 2 {
		t.Fatalf("two append-lane writes left %d band(s) (%v); if the append lane now replaces, "+
			"MergeTriples is no longer load-bearing and its doc comment is stale", len(bands), bands)
	}
}

// beta.159 turns a byte-identical add into an explicit successful no-op. The
// response signal is load-bearing: without it a caller cannot distinguish an
// already-present occurrence from an empty or silently ignored request.
func TestIntegration_TheAddLaneReportsAnIdenticalOccurrenceAsDeduplicated(t *testing.T) {
	const turnID = "c360.semmachina.diceworld5.starter.turn.turn-act-1"
	store, harness := realStore(t)
	createTurnEntity(t, store, turnID)

	triple := message.Triple{
		Subject: turnID, Predicate: vocabulary.TurnRollBand.String(), Object: string(vocabulary.BandPartial),
		Source: dice.Source, Timestamp: resolveTime, Confidence: 1, Context: "one-roll-occurrence",
	}
	add := func() graph.AddTriplesBatchResponse {
		t.Helper()
		request, err := json.Marshal(graph.AddTriplesBatchRequest{Triples: []message.Triple{triple}})
		if err != nil {
			t.Fatalf("encode add_batch: %v", err)
		}
		reply, err := harness.Client.RequestClassified(
			t.Context(), graphingest.SubjectTripleAddBatch, request, 5*time.Second)
		if err != nil {
			t.Fatalf("add_batch: %v", err)
		}
		var response graph.AddTriplesBatchResponse
		if err := json.Unmarshal(reply, &response); err != nil {
			t.Fatalf("decode add_batch response: %v", err)
		}
		return response
	}

	first := add()
	if first.WrittenCount != 1 || first.Deduplicated != 0 || len(first.FailedSubjects) != 0 {
		t.Fatalf("first add response = %+v, want one committed occurrence", first)
	}
	second := add()
	if second.WrittenCount != 0 || second.Deduplicated != 1 || len(second.FailedSubjects) != 0 {
		t.Fatalf("identical add response = %+v, want written=0 deduplicated=1 and no failures", second)
	}
}

func instantiationFor(seed campaign.Seed) campaign.Instantiation {
	return campaign.Instantiation{CampaignID: testCampaignID, Seed: seed}
}

func writeRoll(t *testing.T, store *graphio.Store, resolution dice.Resolution) {
	t.Helper()
	writeRollTo(t, store, integrationTurnEntity, resolution)
}

// writeRollTo projects the roll and commits it on the REPLACE lane — the write
// the turn loop will do, exercised here rather than described.
func writeRollTo(t *testing.T, store *graphio.Store, turnEntityID string, resolution dice.Resolution) {
	t.Helper()
	triples, err := resolution.Roll.Triples(turnEntityID, testRollRef, dice.Source, resolveTime)
	if err != nil {
		t.Fatalf("Triples: %v", err)
	}
	if _, err := store.MergeTriples(t.Context(), turnEntityID, triples); err != nil {
		t.Fatalf("MergeTriples: %v", err)
	}
}
