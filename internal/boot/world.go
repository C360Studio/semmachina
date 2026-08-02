package boot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/c360studio/semstreams/graph"
	sspersona "github.com/c360studio/semstreams/persona"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

// membershipPredicate is the edge "who is here" is answered by.
//
// Membership is asserted by the MEMBER — a character carries
// world.location.current pointing at a location, and the location says nothing
// about who is in it — so it is a reverse lookup, served by graph-index rather
// than by the graph. That is precisely why the boot-readiness gate is keyed on
// it: a mid-build index returns a partial keyset, which reads as a SMALLER PLACE
// rather than as an error, and a persona handed part of a room narrates a room
// that is not there.
const membershipPredicate = vocabulary.WorldLocationCurrent

// loadWorld reads and resolves the world package into this instance's plan.
//
// Pure: no broker, no clock, no randomness. Two boots of the same package into
// the same namespace produce the same plan, which is what makes re-import a
// convergent no-op rather than a second world.
func (e *Engine) loadWorld() error {
	pkg, err := world.LoadPackage(e.cfg.World, world.LoadOptions{})
	if err != nil {
		return fmt.Errorf("load the world package: %w", err)
	}
	plan, err := pkg.Resolve(e.cfg.instanceConfig())
	if err != nil {
		return fmt.Errorf("resolve the world package into this instance: %w", err)
	}
	personas, mechanics, err := bindSelectedExperience(e.cfg.World, plan)
	if err != nil {
		return err
	}

	sceneID, err := sceneEntityID(plan, e.cfg.SceneLocalID)
	if err != nil {
		return err
	}
	playerID, err := playerEntityID(plan, e.cfg.Player.LocalID)
	if err != nil {
		return err
	}

	e.pkg = pkg
	e.plan = plan
	e.personas = personas
	e.mechanics = mechanics
	e.sceneID = sceneID
	e.playerID = playerID
	return nil
}

// sceneEntityID resolves the scene actions are taken in.
//
// A package declaring exactly one scene answers this itself. A package declaring
// several REQUIRES the operator to say which, because the alternative — take the
// first — would pick the scene a campaign is played in by iteration order, and
// the wrong answer is invisible: every action would be stamped with a scene the
// player is not in, and the assembler would hand the adjudicator a room nobody
// is standing in.
func sceneEntityID(plan *world.Plan, localID string) (string, error) {
	var scenes []world.PlannedEntity
	for _, entity := range plan.Entities {
		if entity.Kind == vocabulary.EntityKindScene {
			scenes = append(scenes, entity)
		}
	}
	if len(scenes) == 0 {
		return "", errors.New(
			"the world package declares no scene; a turn is taken in one, and an instance without one has " +
				"nowhere to play")
	}
	if localID == "" {
		if len(scenes) > 1 {
			return "", fmt.Errorf(
				"the world package declares %d scenes (%v) and the instance names none; this slice plays one "+
					"scene, and choosing for the operator would pick it by iteration order",
				len(scenes), localIDs(scenes))
		}
		return scenes[0].ID, nil
	}
	for _, scene := range scenes {
		if scene.Template.LocalID == localID {
			return scene.ID, nil
		}
	}
	return "", fmt.Errorf(
		"the instance names scene %q, which this world package does not declare (it declares %v)",
		localID, localIDs(scenes))
}

// playerEntityID finds the instance-configured player in the resolved plan.
func playerEntityID(plan *world.Plan, localID string) (string, error) {
	for _, entity := range plan.Entities {
		if entity.Kind == vocabulary.EntityKindPlayer && entity.Template.LocalID == localID {
			return entity.ID, nil
		}
	}
	return "", fmt.Errorf(
		"the resolved plan carries no player entity for instance player %q; the plan is what materializes them, "+
			"so a credential naming this player would authenticate to an entity nothing imports", localID)
}

func localIDs(entities []world.PlannedEntity) []string {
	out := make([]string, 0, len(entities))
	for _, entity := range entities {
		out = append(out, entity.Template.LocalID)
	}
	sort.Strings(out)
	return out
}

// instantiate decides whether this boot imports the world, skips it, or refuses
// to serve it — and never imports over a campaign somebody is playing.
//
// # The gate is one atomic create, and the marker is what it does not answer
//
// campaign.Gate.Claim answers "was this campaign created before?" atomically, so
// two boots cannot both decide the world is fresh. It does NOT answer "did the
// import that followed it finish": a process that claims and then dies part-way
// through publishing entities leaves a claimed campaign and a partial world, and
// a boot told only "the campaign exists" would skip the import and serve play
// from half a world. So the claim decides who imports, and the marker decides
// whether an import ever completed:
//
//   - no campaign — this boot claims it, imports, reads the world back, and
//     marks it;
//   - campaign WITH a marker — instantiated and imported; skip;
//   - campaign WITHOUT a marker — somebody else is mid-import, or was. Wait a
//     bounded time and then fail loudly. Importing would run a template over a
//     world another process is writing; marking would certify a world this boot
//     never read.
//
// # Per-entity create-not-put is the wrong granularity, and it is a trap
//
// The obvious generalisation of the seeding idiom — create each template entity
// and treat key-exists as a no-op — is wrong here for a reason that only shows up
// against a real broker: graph-ingest materialises a referential STUB at a
// referenced id as soon as the REFERENCING entity lands. So an entity that is
// pointed at gets its key occupied first, its own create returns key-exists, the
// no-op policy swallows it, and it stays a permanent factless stub. The stream
// path works precisely because it merges.
func (e *Engine) instantiate(ctx context.Context) error {
	claim, err := e.gate.Claim(ctx, campaign.Experience{
		PersonaPack: e.plan.Experience.PersonaPack, MechanicsPack: e.plan.Experience.MechanicsPack,
	})
	if err != nil {
		return fmt.Errorf("claim the campaign: %w", err)
	}
	e.instantiation = claim

	switch {
	case claim.Fresh:
		e.log().Info("this boot claimed a fresh campaign; importing the world",
			"campaign", claim.CampaignID, "entities", len(e.plan.Entities))
		if err := e.importWorld(ctx); err != nil {
			return err
		}
	default:
		at, marked, completionErr := e.gate.ImportCompletion(ctx)
		if completionErr != nil {
			return completionErr
		}
		if !marked {
			e.log().Warn("the campaign exists but no import has been marked complete; waiting for the claimant",
				"campaign", claim.CampaignID, "wait", e.cfg.MarkerWait)
			at, err = e.gate.AwaitImportCompletion(ctx, e.cfg.MarkerWait, e.cfg.ReadyPoll)
			if err != nil {
				return err
			}
		}
		e.log().Info("the world was instantiated by an earlier boot; skipping the import",
			"campaign", claim.CampaignID, "imported_at", at)
	}

	// The reverse-edge readback runs on BOTH paths, and that is not belt and
	// braces. The marker is a fact about the IMPORT; graph-index is a PROJECTION
	// that every process rebuilds, so an already-imported world says nothing
	// about whether this boot's index has caught up with it yet.
	return e.awaitMembershipIndexed(ctx)
}

// importWorld publishes the plan and does not return until the world is
// queryable, indexed, and marked.
//
// The order is the content of this method. The importer acknowledges a PUBLISH,
// which is a durability claim and not a materialisation claim — graph-ingest
// applies the messages asynchronously on its own consumer — so "the import
// finished" means every planned entity is queryable and NON-STUB, not merely
// durably queued. The marker is written last, after both readbacks, because the
// whole point of the marker is that a later boot may trust it without repeating
// them.
func (e *Engine) importWorld(ctx context.Context) error {
	result, err := e.importer.Import(ctx, e.plan)
	if err != nil {
		return fmt.Errorf("import the world: %w", err)
	}
	e.log().Info("world entities published", "count", len(result.Entities), "at", result.RecordedAt)

	if err := e.awaitEntitiesBorn(ctx); err != nil {
		return err
	}
	if err := e.awaitMembershipIndexed(ctx); err != nil {
		return err
	}

	at, err := e.gate.MarkImported(ctx, e.instantiation)
	if err != nil {
		return err
	}
	e.log().Info("world import marked complete", "campaign", e.instantiation.CampaignID, "at", at)
	return nil
}

// awaitEntitiesBorn polls until every planned entity is queryable AND is no
// longer a bare referential stub.
//
// Both halves are load-bearing and the second is the one that gets forgotten.
// graph-ingest creates a referenced entity as a STUB — queryable, carrying only
// core.identity markers and none of its own facts — the moment the entity that
// REFERENCES it lands. So an existence poll alone succeeds against a half-world,
// and anything that treats "the id resolves" as "the entity is loaded" reads
// half-entities. IsStub keys on the envelope, which the fact lane re-stamps at
// true birth, so it is the signal that actually flips.
func (e *Engine) awaitEntitiesBorn(ctx context.Context) error {
	return awaitEntitiesBorn(ctx, e.graph, e.plan.IDs(), e.window())
}

// window is the poll budget every boot readback shares.
func (e *Engine) window() readinessWindow {
	return readinessWindow{timeout: e.cfg.ReadyTimeout, poll: e.cfg.ReadyPoll, sleep: e.sleep}
}

// readinessGraph is the read surface the two boot readbacks need.
//
// An interface rather than the concrete store because the states these gates
// exist to refuse — a referential stub, an index that answers SHORT, an index
// that says it is not ready — are states a real broker produces on its own
// schedule and never on demand. A gate whose refusals were only ever exercised
// by luck is a gate nobody has checked.
type readinessGraph interface {
	GetEntities(ctx context.Context, ids []string) (graphio.BatchResult, error)
	IncomingRelationships(ctx context.Context, entityID string) ([]graph.IncomingEntry, error)
}

// The claim above, enforced by the compiler rather than by a doc comment.
var _ readinessGraph = (*graphio.Store)(nil)

// readinessWindow is how long a readback polls and how often.
type readinessWindow struct {
	timeout time.Duration
	poll    time.Duration
	sleep   func(ctx context.Context, d time.Duration) error
}

func awaitEntitiesBorn(ctx context.Context, g readinessGraph, ids []string, w readinessWindow) error {
	deadline := time.Now().Add(w.timeout)
	var pending []string

	for {
		batch, err := g.GetEntities(ctx, ids)
		if err != nil {
			return fmt.Errorf("read the imported world back: %w", err)
		}
		pending = pending[:0]
		for _, missing := range batch.Missing {
			pending = append(pending, fmt.Sprintf("%s (%s)", missing.ID, missing.Reason))
		}
		for idx := range batch.Entities {
			if batch.Entities[idx].IsStub() {
				pending = append(pending, batch.Entities[idx].ID+" (referential stub)")
			}
		}
		if len(pending) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			slices.Sort(pending)
			return fmt.Errorf(
				"after %s, %d of %d imported entities are still unborn: %s. The importer acknowledges a PUBLISH "+
					"and graph-ingest applies it asynchronously, so this is the gap between \"durably queued\" "+
					"and \"queryable\" — a boot that marked the import complete here would certify a world the "+
					"context assembler reads as quietly smaller",
				w.timeout, len(pending), len(ids), strings.Join(pending, ", "))
		}
		if err := w.sleep(ctx, w.poll); err != nil {
			return err
		}
	}
}

// awaitMembershipIndexed polls the INCOMING query until every membership edge
// the plan declares actually appears.
//
// # A positive readback, never the absence of ErrIndexNotReady
//
// That sentinel is inert in exactly this window. graph-index latches ready when
// its target count is zero and enumeration completes, so on a fresh
// instance-per-world boot it reports READY before the import has written
// anything — and a gate keyed on "no ErrIndexNotReady" would pass instantly,
// against an index that has nothing in it. The failure that follows is silent:
// the incoming query returns a short list, the assembler reads it as a smaller
// scene, and a persona narrates a room that is not there.
//
// So the gate is the fact itself: the edges the plan says exist must be
// readable. ErrIndexNotReady is treated as "not yet" and never as an answer.
//
// A plan declaring no membership at all is REFUSED rather than passing an empty
// gate. A world where nobody is anywhere is not playable, and a readiness check
// that can be satisfied by having nothing to check is worse than no check —
// it is the one that reports green on the day the import produced nothing.
func (e *Engine) awaitMembershipIndexed(ctx context.Context) error {
	return awaitMembershipIndexed(ctx, e.graph, membershipEdges(e.plan), e.window())
}

func awaitMembershipIndexed(
	ctx context.Context,
	g readinessGraph,
	expected map[string][]string,
	w readinessWindow,
) error {
	if len(expected) == 0 {
		return fmt.Errorf(
			"the world package declares no %s edge, so this boot has no membership to gate on; \"who is in this "+
				"scene\" is a reverse lookup and a readiness check with nothing to read would pass against an "+
				"empty index", membershipPredicate)
	}

	targets := make([]string, 0, len(expected))
	for target := range expected {
		targets = append(targets, target)
	}
	sort.Strings(targets)

	deadline := time.Now().Add(w.timeout)
	var pending []string
	for {
		pending = pending[:0]
		for _, target := range targets {
			missing, err := missingIncoming(ctx, g, target, expected[target])
			if err != nil {
				return err
			}
			for _, source := range missing {
				pending = append(pending, source+" -> "+target)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"after %s the reverse-edge index still does not carry %d membership edge(s): %s. The index is a "+
					"projection over ENTITY_STATES and is rebuilt per process, so this is the window in which "+
					"\"who is here\" answers with a SHORTER list rather than an error",
				w.timeout, len(pending), strings.Join(pending, ", "))
		}
		if err := w.sleep(ctx, w.poll); err != nil {
			return err
		}
	}
}

// missingIncoming returns the sources whose membership edge the index does not
// yet carry for one target.
//
// An index that reports NOT READY is reported as "everything is still missing"
// rather than as an error, which is the honest reading: upstream refuses to
// serve a partial keyset, so a not-ready answer is the index saying it does not
// know yet.
func missingIncoming(
	ctx context.Context,
	g readinessGraph,
	target string,
	sources []string,
) ([]string, error) {
	entries, err := g.IncomingRelationships(ctx, target)
	if err != nil {
		if errors.Is(err, graphio.ErrIndexNotReady) {
			return sources, nil
		}
		return nil, fmt.Errorf("read the incoming edges of %s: %w", target, err)
	}
	present := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Predicate == membershipPredicate.String() {
			present[entry.FromEntityID] = true
		}
	}
	var missing []string
	for _, source := range sources {
		if !present[source] {
			missing = append(missing, source)
		}
	}
	return missing, nil
}

// MembershipEdges returns, per target entity, the members the resolved plan says
// are there.
//
// Exported because it is what the boot-readiness gate is asserting, and a gate
// whose subject cannot be inspected is a gate nobody can tell is empty. It reads
// the PLAN, not the graph: this is the set the index is expected to catch up
// with.
func (e *Engine) MembershipEdges() map[string][]string { return membershipEdges(e.plan) }

// membershipEdges returns, per target entity, the sources the plan says point at
// it through the membership predicate.
func membershipEdges(plan *world.Plan) map[string][]string {
	edges := make(map[string][]string)
	for _, entity := range plan.Entities {
		for _, fact := range entity.Facts {
			if !fact.Reference || fact.Predicate != membershipPredicate {
				continue
			}
			target, _ := fact.Object.(string)
			if target == "" {
				continue
			}
			edges[target] = append(edges[target], entity.ID)
		}
	}
	for target := range edges {
		sort.Strings(edges[target])
	}
	return edges
}

// seedPersonas writes the world package's persona records into the PERSONAS
// bucket the agentic loop reads its role fragments from.
//
// This is where a world's VOICE reaches the model. The package's persona records
// carry tone, register and judging stance — properties of that world — and
// nothing else: no model slot, no iteration cap, no tool schema, because a
// downloaded package must not be able to pick its own spend or widen its own
// exit vocabulary. Those live on the persona Spec, in code.
//
// Upsert rather than Create: a world package is authored data that an operator
// may edit and re-import, and Create would make the second boot after an edit
// silently keep the first boot's text. Seeding is idempotent by that choice.
func (e *Engine) seedPersonas(ctx context.Context) error {
	if len(e.personas) == 0 {
		return errors.New(
			"the world package ships no persona records; without them both loops run on the framework's default " +
				"fragments and the world has no voice at all")
	}
	if err := validateCachedPersonas(e.plan, e.personas); err != nil {
		return err
	}
	manager, err := sspersona.NewManager(e.client)
	if err != nil {
		return fmt.Errorf("open the persona bucket: %w", err)
	}
	for index := range e.personas {
		record := &e.personas[index]
		if err := manager.Upsert(ctx, record); err != nil {
			name := e.plan.Experience.PersonaFiles[index]
			return fmt.Errorf("seed persona record %s: %w", name, err)
		}
		name := e.plan.Experience.PersonaFiles[index]
		e.log().Info("seeded a world persona fragment", "file", name, "id", record.ID, "roles", record.Roles)
	}
	return nil
}
