package companion_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic"

	"github.com/c360studio/semmachina/fixtures"
	"github.com/c360studio/semmachina/internal/companion"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/scene"
	"github.com/c360studio/semmachina/internal/testinfra"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

func TestMain(m *testing.M) { os.Exit(testinfra.RunTests(m)) }

func TestIntegration_ArbitraryNonMysteryCompanionUsesImporterAudienceAndRejectsUntriggeredModelExit(t *testing.T) {
	live := prepareLiveCompanion(t)
	projection := awaitCompanionProjection(t, live)
	if projection.ContextRef != live.ids.scene || projection.CompanionID != live.ids.companion || projection.HasSolution {
		t.Fatalf("projection leaked mystery assumptions or lost identity: %+v", projection)
	}
	task := buildCompanionTask(t, live.artifacts, projection)
	executor, err := companion.NewExecutor(live.artifacts, live.graph, live.authority, live.projector)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(t.Context(), agentic.ToolCall{
		ID: "call-companion", Name: persona.CompanionDecisionToolName, Metadata: task.Metadata,
		Arguments: map[string]any{"kind": "silent", "hint_level": "", "evidence_refs": []any{}, "target_ref": ""},
	})
	if err == nil || result.StopLoop || result.ErrorKind != agentic.ToolErrorInternal {
		t.Fatalf("untriggered companion execution result=%+v err=%v", result, err)
	}
	assertNoCompanionDecisionReference(t, live)
}

type companionPlanIDs struct{ player, companion, bond, scene string }

type liveCompanion struct {
	ids       companionPlanIDs
	graph     *graphio.Store
	artifacts *content.Store
	authority *companion.Authority
	projector *epistemic.Projector
	accepted  turn.Acceptance
}

func prepareLiveCompanion(t *testing.T) *liveCompanion {
	t.Helper()
	harness := testinfra.Require(t)
	harness.RequireIndex(t)
	namespace := fmt.Sprintf("companion%d", time.Now().UnixNano())
	pkg, plan := importCompanionPlan(t, harness, namespace)
	ids := companionIDs(plan)
	graphStore, err := graphio.NewStore(harness.Client)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := liveArtifacts(t, harness, namespace)
	accepted := acceptCompanionAction(t, graphStore, artifacts, namespace, pkg.Manifest.ID, ids)
	authority, err := companion.NewAuthority(graphStore)
	if err != nil {
		t.Fatal(err)
	}
	awaitBondAuthority(t, authority, ids)
	projector := liveCompanionProjector(t, graphStore, authority)
	return &liveCompanion{
		ids: ids, graph: graphStore, artifacts: artifacts,
		authority: authority, projector: projector, accepted: accepted,
	}
}

func importCompanionPlan(t *testing.T, harness *testinfra.Harness, namespace string) (*world.Package, *world.Plan) {
	t.Helper()
	fsys, err := fixtures.StarterWorld()
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := world.LoadPackage(fsys, world.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	instance := world.InstanceConfig{
		Org: "c360", WorldNS: namespace,
		Player:    world.PlayerBinding{LocalID: "guest", Name: "Guest", Character: "local:rook"},
		Companion: &world.CompanionBinding{Character: "local:wren", Policy: vocabulary.CompanionPolicyReactive},
	}
	plan, err := pkg.Resolve(instance)
	if err != nil {
		t.Fatalf("Resolve non-mystery companion: %v", err)
	}
	importer, err := world.NewImporter(harness.Client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Import(t.Context(), plan); err != nil {
		t.Fatalf("Import: %v", err)
	}
	for _, entity := range plan.Entities {
		harness.AwaitEntity(t, entity.ID)
	}
	return pkg, plan
}

func companionIDs(plan *world.Plan) companionPlanIDs {
	var ids companionPlanIDs
	for _, entity := range plan.Entities {
		switch entity.Kind {
		case vocabulary.EntityKindPlayer:
			ids.player = entity.ID
		case vocabulary.EntityKindCompanionBond:
			ids.bond = entity.ID
		case vocabulary.EntityKindCharacter:
			if entity.Template.LocalID == "wren" {
				ids.companion = entity.ID
			}
		case vocabulary.EntityKindScene:
			ids.scene = entity.ID
		}
	}
	return ids
}

func liveArtifacts(t *testing.T, harness *testinfra.Harness, namespace string) *content.Store {
	t.Helper()
	backend, err := content.NewObjectStore(t.Context(), harness.Client,
		content.WithBucket("COMPANION_"+namespace[len(namespace)-8:]))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	artifacts, err := content.NewStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	return artifacts
}

func acceptCompanionAction(
	t *testing.T, graphStore *graphio.Store, artifacts *content.Store,
	namespace, template string, ids companionPlanIDs,
) turn.Acceptance {
	t.Helper()
	recorder, err := turn.NewRecorder(graphStore, artifacts,
		turn.Identity{Org: "c360", WorldNS: namespace, Template: template})
	if err != nil {
		t.Fatal(err)
	}
	action := &payload.PlayerAction{
		ActionID: "companion-act", PlayerID: ids.player,
		CampaignID: "c360.semmachina." + namespace + ".starter.campaign.main", SceneID: ids.scene,
		Text: "Wren, what do you make of the gate?", ArrivedAt: time.Now().UTC(),
		Channel: payload.ChannelBinding{Adapter: vocabulary.AdapterWebSocket, ReplyTo: "test"},
	}
	accepted, err := recorder.Accept(t.Context(), action)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	return accepted
}

func awaitBondAuthority(t *testing.T, authority *companion.Authority, ids companionPlanIDs) {
	t.Helper()
	// ENTITY_STATES is authoritative immediately, while reverse predicate indexes
	// follow asynchronously. Wait until the production authority can prove bond
	// uniqueness before exercising the terminal boundary.
	var err error
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_, err = authority.ValidateBond(t.Context(), ids.bond, ids.player, ids.companion)
		if err == nil {
			break
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err != nil {
		t.Fatalf("companion bond index never became authoritative: %v", err)
	}
}

func liveCompanionProjector(
	t *testing.T, graphStore *graphio.Store, authority *companion.Authority,
) *epistemic.Projector {
	t.Helper()
	assembler, err := scene.NewAssembler(graphStore)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := epistemic.NewScope("", nil)
	if err != nil {
		t.Fatal(err)
	}
	projector, err := epistemic.NewProjector(
		assembler, graphStore, scope, epistemic.WithCompanionBondValidator(authority))
	if err != nil {
		t.Fatal(err)
	}
	return projector
}

func awaitCompanionProjection(t *testing.T, live *liveCompanion) *epistemic.Projection {
	t.Helper()
	audience := epistemic.CompanionAudience(
		live.accepted.TurnID, live.accepted.TurnEntityID,
		live.ids.scene, live.ids.companion, live.ids.bond)
	var projection *epistemic.Projection
	var err error
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		projection, err = live.projector.Project(t.Context(), audience)
		if err == nil {
			break
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err != nil {
		t.Fatalf("generic non-mystery companion projection: %v", err)
	}
	return projection
}

func buildCompanionTask(
	t *testing.T, artifacts *content.Store, projection *epistemic.Projection,
) agentic.TaskMessage {
	t.Helper()
	builder, err := persona.NewBuilder(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	request, err := builder.Companion(t.Context(), projection)
	if err != nil {
		t.Fatalf("Companion prompt: %v", err)
	}
	task, err := persona.Companion().Task(request)
	if err != nil {
		t.Fatalf("Companion task: %v", err)
	}
	return task
}

func assertNoCompanionDecisionReference(t *testing.T, live *liveCompanion) {
	t.Helper()
	turnState, err := live.graph.GetEntity(t.Context(), live.accepted.TurnEntityID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(testinfra.ObjectsFor(turnState, vocabulary.TurnCompanionDecisionRef.String())); got != 0 {
		t.Fatalf("untriggered turn received %d companion decision references: %+v", got, turnState.Triples)
	}
}
