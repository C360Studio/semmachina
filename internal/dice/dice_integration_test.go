//go:build integration

package dice_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/projectioncontract"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// "Exactly one roll-result triple per turn" is a claim about the GRAPH, and the
// graph is where it can be false. The mutation API offers two lanes that both
// accept the same triples: one reconciles a complete predicate group and one appends.
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
	parts := strings.Split(id, ".")
	turnID := parts[len(parts)-1]
	prefix := strings.Join(parts[:4], ".")
	birth := &payload.TurnState{
		TurnID: turnID, Phase: vocabulary.PhaseAccepted,
		PlayerID: prefix + ".player.test", SceneID: prefix + ".scene.test",
		ActionRef: "obj://TEST/turn/" + turnID + "/action",
	}
	birthTriples, err := birth.Triples(id, dice.Source, resolveTime)
	if err != nil {
		t.Fatalf("turn birth triples: %v", err)
	}
	if _, err := store.CreateEntity(t.Context(), projectioncontract.TurnBirthContract, &graph.EntityState{
		ID:          id,
		MessageType: birth.Schema(),
		Version:     1,
		UpdatedAt:   resolveTime,
		Triples:     birthTriples,
	}); err != nil {
		t.Fatalf("create turn entity: %v", err)
	}
	if _, err := store.Reconcile(t.Context(), projectioncontract.TurnPhaseState, id, []message.Triple{{
		Subject:    id,
		Predicate:  vocabulary.TurnPhaseCurrent.String(),
		Object:     string(vocabulary.PhaseResolving),
		Source:     dice.Source,
		Timestamp:  resolveTime,
		Confidence: 1.0,
	}},
	); err != nil {
		t.Fatalf("advance turn to resolving: %v", err)
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
	// predicates replace through their complete reconcile group.
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
	// The phase predicate written at turn creation must survive the reconcile —
	// the roll replaces its OWN predicates, not the entity's other facts.
	if got := testinfra.FirstObject(state, vocabulary.TurnPhaseCurrent.String()); got != string(vocabulary.PhaseResolving) {
		t.Fatalf("the roll write clobbered the turn phase: %v", got)
	}
}

// The other half of the lane argument, pinned against the real broker: the
// APPEND lane really does leave a turn holding two bands, with a success
// response and no error anywhere. Choosing between the two lanes is therefore a
// correctness decision, not a performance one — and if a future semstreams
// version changes this, the reason the reconcile group exists changes with it and this
// test says so.
func TestIntegration_TheAppendLaneLeavesTwoBandsWhichIsWhyReconcileIsUsed(t *testing.T) {
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
		// The append lane deduplicates an identical six-field tuple.
		// Give these occurrences distinct correlation contexts while keeping
		// source and the single-valued predicate/object identical: that is the anomaly the
		// reconcile lane prevents, not byte-identical redelivery.
		for idx := range triples {
			triples[idx].Context = fmt.Sprintf("append-control-%d", attempt)
		}
		appendRollTriples(t, harness, turnID, fmt.Sprintf("append-control-%d", attempt), triples)
	}

	state := harness.AwaitEntity(t, turnID)
	bands := testinfra.ObjectsFor(state, vocabulary.TurnRollBand.String())
	if len(bands) < 2 {
		t.Fatalf("two append-lane writes left %d band(s) (%v); if the append lane now replaces, "+
			"the reconcile group is no longer load-bearing and its documentation is stale", len(bands), bands)
	}
}

// The append lane turns a byte-identical add into an explicit successful no-op. The
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
	add := func() projection.MutationReceipt {
		t.Helper()
		return appendRollTriples(t, harness, turnID, "one-roll-occurrence", []message.Triple{triple})
	}

	first := add()
	if first.Commit != projection.CommitVerified || first.KVRevision == 0 {
		t.Fatalf("first append receipt = %+v, want a verified revision", first)
	}
	second := add()
	if second.Commit != projection.CommitVerified || second.KVRevision != first.KVRevision {
		t.Fatalf("identical append advanced revision from %d to %d; want an unchanged verified occurrence",
			first.KVRevision, second.KVRevision)
	}
}

// appendRollTriples deliberately exercises the wrong lane through beta.160's
// contract-validating client. It is a negative control for the production
// TurnRoll reconciliation group, not an alternate application write path.
func appendRollTriples(
	t *testing.T,
	harness *testinfra.Harness,
	entityID, requestID string,
	triples []message.Triple,
) projection.MutationReceipt {
	t.Helper()
	client, err := projection.NewMutationClient(projection.MutationClientConfig{
		NATS: harness.Client,
		Contracts: []projection.Contract{{
			Name:          "test-turn-roll-append",
			MessageType:   payload.Domain + "." + payload.CategoryTurnState + "." + payload.SchemaVersion,
			EntityPattern: "*.semmachina.*.*.turn.*",
			Groups: []projection.PredicateGroup{{
				Name: "roll", Mode: projection.ModeAppend,
				Predicates: projectioncontract.Predicates(projectioncontract.TurnRoll),
			}},
		}},
		Timeout: graphio.DefaultTimeout,
	})
	if err != nil {
		t.Fatalf("build append control client: %v", err)
	}
	receipt, err := client.Append(t.Context(), projection.AppendMutation{
		Contract: "test-turn-roll-append", Group: "roll", EntityID: entityID, Triples: triples,
		Metadata: projection.MutationMetadata{RequestID: requestID, Source: dice.Source, Timestamp: resolveTime},
	})
	if err != nil {
		t.Fatalf("append roll control %s: %v", requestID, err)
	}
	return receipt
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
	if _, err := store.Reconcile(t.Context(), projectioncontract.TurnRoll, turnEntityID, triples); err != nil {
		t.Fatalf("Reconcile %s/%s: %v", projectioncontract.TurnRoll.Contract, projectioncontract.TurnRoll.Group, err)
	}
}
