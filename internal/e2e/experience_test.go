package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	upstreamgraph "github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/model/wire"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semmachina/internal/boot"
	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/stage"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	experienceScenario        = "experience-selection"
	plainNarratorMarker       = "[starter-narrator:plain]"
	alternateNarratorMarker   = "[starter-narrator:alternate]"
	experiencePlayerAction    = "I hold my ground while Hollis's spear catches me across the ribs."
	experienceNarration       = "Rook catches himself against the cold gatehouse wall. He straightens slowly, one hand pressed to the wound."
	starterNarratorPersonaKey = "starter/narrator"
)

type experienceRun struct {
	world              *world
	turnEntityID       string
	verdictIntent      payload.EffectIntent
	batchIntent        payload.EffectIntent
	narratorResponse   []byte
	normalizedTriggers [][]byte
}

// The same embedded world is a template, not a singleton. This acceptance runs
// each experience serially because PERSONAS is intentionally global; sharing a
// stable narrator ID makes the second boot replace the selected voice.
func TestE2E_StarterExperienceSelectionIsIsolatedSealedAndRestartSafe(t *testing.T) {
	replaceBrokerWithFresh(t)

	plain := runStarterExperience(t, "e2eexpplain", "default", plainNarratorMarker,
		alternateNarratorMarker, vocabulary.WorldRelationHostileTo, vocabulary.WorldRelationCarries)
	plain.world.crash()

	alternate := runStarterExperience(t, "e2eexpalternate", "alternate", alternateNarratorMarker,
		plainNarratorMarker, vocabulary.WorldRelationCarries, vocabulary.WorldRelationHostileTo)
	alternate.world.crash()

	if plain.turnEntityID == alternate.turnEntityID || strings.Contains(plain.turnEntityID, alternate.world.ns) ||
		strings.Contains(alternate.turnEntityID, plain.world.ns) {
		t.Fatalf("experience entity identities overlap: plain=%q alternate=%q", plain.turnEntityID, alternate.turnEntityID)
	}
	if plain.verdictIntent.Type != vocabulary.EffectSetStatus || alternate.verdictIntent.Type != vocabulary.EffectSetStatus ||
		plain.verdictIntent.Status != vocabulary.StatusWounded || alternate.verdictIntent.Status != vocabulary.StatusWounded {
		t.Fatalf("verdict intents differ from the common wounded input: plain=%+v alternate=%+v",
			plain.verdictIntent, alternate.verdictIntent)
	}
	if plain.batchIntent.Type != vocabulary.EffectSetStatus || alternate.batchIntent.Type != vocabulary.EffectSetStatus ||
		plain.batchIntent.Status != vocabulary.StatusWounded || alternate.batchIntent.Status != vocabulary.StatusWounded {
		t.Fatalf("applied effect artifacts differ from the common wounded input: plain=%+v alternate=%+v",
			plain.batchIntent, alternate.batchIntent)
	}
	if !bytes.Equal(plain.narratorResponse, alternate.narratorResponse) {
		t.Fatalf("the mock's scripted narration response differs by selection:\nplain=%s\nalternate=%s",
			plain.narratorResponse, alternate.narratorResponse)
	}
	if len(plain.normalizedTriggers) != len(alternate.normalizedTriggers) {
		t.Fatalf("stage trigger counts differ: plain=%d alternate=%d",
			len(plain.normalizedTriggers), len(alternate.normalizedTriggers))
	}
	for i := range plain.normalizedTriggers {
		if !bytes.Equal(plain.normalizedTriggers[i], alternate.normalizedTriggers[i]) {
			t.Fatalf("normalized stage trigger %d differs:\nplain=%s\nalternate=%s",
				i, plain.normalizedTriggers[i], alternate.normalizedTriggers[i])
		}
	}

	// Restart the alternate campaign with the same pair. The authored world and
	// play-created wound must survive, and a skipped reimport must not advance any
	// authoritative entity revision.
	w := alternate.world
	tracked := []string{w.campaignID, w.entity("character", starterCharacter), w.entity("location", "north-road")}
	beforeRestart := entityRevisions(t, tracked...)
	w.boot(t)
	for id, before := range beforeRestart {
		if after := entityRevision(t, id); after != before {
			t.Errorf("same-selection restart changed %s revision from %d to %d; world import reran", id, before, after)
		}
	}
	requireExperienceState(t, w, "alternate", vocabulary.WorldRelationCarries, vocabulary.WorldRelationHostileTo)
	campaignState := entityState(t, w.campaignID)
	if stringObject(t, campaignState, vocabulary.CampaignImportCompleted) == "" {
		t.Error("same-selection restart lost the import-completion marker")
	}
	if got := stringObject(t, entityState(t, w.entity("location", "north-road")), vocabulary.WorldEntityName); got == "" {
		t.Error("same-selection restart lost the authored north-road name")
	}
	w.crash()

	// A different pair is refused by World before Persona or Rules can mutate
	// their shared stores. Revisions make the ordering claim observable.
	beforeMismatchEntities := entityRevisions(t, tracked...)
	personaBefore := kvRevision(t, "PERSONAS", starterNarratorPersonaKey)
	ruleBefore := kvRevision(t, "COMPONENT_STATUS", "rule-processor")
	mismatch := newWorldUnstarted(t, w.ns, experienceScenario, withExperience("default", "default"))
	engine, err := boot.New(mismatch.cfg)
	if err != nil {
		t.Fatalf("construct mismatched boot: %v", err)
	}
	err = engine.Start(t.Context())
	engine.Stop()
	if !errors.Is(err, campaign.ErrExperienceMismatch) {
		t.Fatalf("mismatched restart error = %v, want %v", err, campaign.ErrExperienceMismatch)
	}
	for _, want := range []string{"alternate", "default"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("mismatch error %q does not name pack %q", err, want)
		}
	}
	if after := kvRevision(t, "PERSONAS", starterNarratorPersonaKey); after != personaBefore {
		t.Errorf("mismatched boot changed narrator revision from %d to %d before World refused it", personaBefore, after)
	}
	if after := kvRevision(t, "COMPONENT_STATUS", "rule-processor"); after != ruleBefore {
		t.Errorf("mismatched boot changed rule status revision from %d to %d before World refused it", ruleBefore, after)
	}
	for id, before := range beforeMismatchEntities {
		if after := entityRevision(t, id); after != before {
			t.Errorf("mismatched boot changed entity %s revision from %d to %d", id, before, after)
		}
	}
}

func runStarterExperience(
	t *testing.T,
	ns, pack, selectedMarker, otherMarker string,
	removed, preserved vocabulary.Predicate,
) experienceRun {
	t.Helper()
	w := newWorld(t, ns, experienceScenario, withExperience(pack, pack))
	player := w.dial(t)
	response := player.submit(t, "same-experience-action", experiencePlayerAction)
	if response.Status != payload.StatusAccepted {
		t.Fatalf("%s experience refused the identical action: %+v", pack, response.Refusal)
	}
	turnEntityID := w.turnEntity(t, response.TurnID)
	if phase := awaitTerminal(t, turnEntityID); phase != vocabulary.PhaseComplete {
		t.Fatalf("%s experience ended in %q", pack, phase)
	}
	awaitExperienceReaction(t, w, removed, preserved)
	requireExperienceState(t, w, pack, removed, preserved)

	turnState := entityState(t, turnEntityID)
	store := w.contentStore(t)
	verdictRef, err := content.ParseRef(stringObject(t, turnState, vocabulary.TurnVerdictRef))
	if err != nil {
		t.Fatalf("parse %s verdict ref: %v", pack, err)
	}
	verdict, err := store.GetVerdict(t.Context(), verdictRef)
	if err != nil {
		t.Fatalf("read %s verdict: %v", pack, err)
	}
	batchRef, err := content.ParseRef(stringObject(t, turnState, vocabulary.TurnEffectsRef))
	if err != nil {
		t.Fatalf("parse %s effect ref: %v", pack, err)
	}
	batch, err := store.GetEffectBatch(t.Context(), batchRef)
	if err != nil {
		t.Fatalf("read %s effect batch: %v", pack, err)
	}
	if len(verdict.Bands[vocabulary.BandAuto]) != 1 || len(batch.Intents) != 1 {
		t.Fatalf("%s artifacts do not each hold one auto intent: verdict=%+v batch=%+v", pack, verdict.Bands, batch.Intents)
	}

	campaignState := entityState(t, w.campaignID)
	if got := stringObject(t, campaignState, vocabulary.CampaignExperiencePersonaPack); got != pack {
		t.Errorf("%s campaign persona provenance = %q", pack, got)
	}
	if got := stringObject(t, campaignState, vocabulary.CampaignExperienceMechanicsPack); got != pack {
		t.Errorf("%s campaign mechanics provenance = %q", pack, got)
	}

	narratorCalls := w.mock.CallsFor("narrator")
	if len(narratorCalls) != 1 {
		t.Fatalf("%s narrator calls = %d, want 1", pack, len(narratorCalls))
	}
	var request wire.ChatCompletionRequest
	if err := json.Unmarshal(narratorCalls[0].Body, &request); err != nil {
		t.Fatalf("decode %s narrator request: %v", pack, err)
	}
	prompt := promptText(t, request)
	if !strings.Contains(prompt, selectedMarker) || strings.Contains(prompt, otherMarker) {
		t.Errorf("%s narrator prompt does not uniquely contain %q", pack, selectedMarker)
	}
	responses := w.wire.Responses()
	if len(responses) != 2 {
		t.Fatalf("%s wire responses = %d, want adjudicator+narrator", pack, len(responses))
	}
	if !bytes.Contains(responses[1].Body, []byte(experienceNarration)) {
		t.Fatalf("%s narrator response does not carry the common scripted prose: %s", pack, responses[1].Body)
	}

	return experienceRun{
		world: w, turnEntityID: turnEntityID,
		verdictIntent: verdict.Bands[vocabulary.BandAuto][0], batchIntent: batch.Intents[0],
		narratorResponse:   slices.Clone(responses[1].Body),
		normalizedTriggers: stageTriggerBytes(t, turnEntityID),
	}
}

func awaitExperienceReaction(t *testing.T, w *world, removed, preserved vocabulary.Predicate) {
	t.Helper()
	deadline := time.Now().Add(turnBudget)
	for {
		rook := entityState(t, w.entity("character", starterCharacter))
		if stringObject(t, rook, vocabulary.CharacterStatusCurrent) == string(vocabulary.StatusWounded) &&
			len(objectsFor(rook, removed)) == 0 && len(objectsFor(rook, preserved)) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("selected mechanics reaction did not settle: status=%q removed=%v preserved=%v",
				stringObject(t, rook, vocabulary.CharacterStatusCurrent), objectsFor(rook, removed), objectsFor(rook, preserved))
		}
		select {
		case <-t.Context().Done():
			t.Fatal("test context ended while waiting for selected mechanics reaction")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func requireExperienceState(t *testing.T, w *world, pack string, removed, preserved vocabulary.Predicate) {
	t.Helper()
	rook := entityState(t, w.entity("character", starterCharacter))
	if got := stringObject(t, rook, vocabulary.CharacterStatusCurrent); got != string(vocabulary.StatusWounded) {
		t.Errorf("%s Rook status = %q, want wounded", pack, got)
	}
	if got := objectsFor(rook, removed); len(got) != 0 {
		t.Errorf("%s selected relation %s remains %v", pack, removed, got)
	}
	if got := objectsFor(rook, preserved); len(got) == 0 {
		t.Errorf("%s alternate relation %s was also removed", pack, preserved)
	}
}

func stageTriggerBytes(t *testing.T, turnEntityID string) [][]byte {
	t.Helper()
	wantPhases := []vocabulary.TurnPhase{
		vocabulary.PhaseInterpreting, vocabulary.PhaseAdjudicating, vocabulary.PhaseApplying,
		vocabulary.PhaseCompanion, vocabulary.PhaseNarrating, vocabulary.PhaseComplete,
	}
	stream, err := jetStream(t).Stream(t.Context(), rulepack.StageStream)
	if err != nil {
		t.Fatalf("read %s: %v", rulepack.StageStream, err)
	}
	reader, err := stream.CreateConsumer(t.Context(), jetstream.ConsumerConfig{
		FilterSubject: rulepack.StageSubjectFilter, DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy: jetstream.AckNonePolicy, InactiveThreshold: time.Minute,
	})
	if err != nil {
		t.Fatalf("create stage trigger reader: %v", err)
	}
	batch, err := reader.Fetch(4096, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch stage triggers: %v", err)
	}
	var gotPhases []vocabulary.TurnPhase
	var out [][]byte
	for msg := range batch.Messages() {
		trigger, parseErr := stage.ParseTrigger(msg.Data())
		if parseErr != nil || trigger.TurnEntityID != turnEntityID {
			continue
		}
		phase, isPhase := rulepack.PhaseForSubject(trigger.Subject)
		if !isPhase {
			continue // resolved is a terminal notification, not a stage trigger.
		}
		gotPhases = append(gotPhases, phase)
		out = append(out, normalizeStageTrigger(t, msg.Data()))
	}
	if !slices.Equal(gotPhases, wantPhases) {
		t.Fatalf("turn %s phase triggers = %v, want exactly %v; accepted belongs to intake and resolving is absent on no-roll",
			turnEntityID, gotPhases, wantPhases)
	}
	return out
}

func normalizeStageTrigger(t *testing.T, data []byte) []byte {
	t.Helper()
	var trigger map[string]any
	if err := json.Unmarshal(data, &trigger); err != nil {
		t.Fatalf("decode stage trigger for normalization: %v", err)
	}
	if trigger["entity_id"] == "" || trigger["timestamp"] == "" {
		t.Fatalf("stage trigger is missing runtime identity or timestamp: %s", data)
	}
	// These are the only two necessarily different fields between disjoint,
	// serial runs. Canonical JSON after replacing them makes every structural
	// byte (including null properties, source, subject, and field set) comparable.
	trigger["entity_id"] = "<turn-entity-id>"
	trigger["timestamp"] = "<timestamp>"
	normalized, err := json.Marshal(trigger)
	if err != nil {
		t.Fatalf("encode normalized stage trigger: %v", err)
	}
	return normalized
}

func entityRevisions(t *testing.T, ids ...string) map[string]uint64 {
	t.Helper()
	out := make(map[string]uint64, len(ids))
	for _, id := range ids {
		out[id] = entityRevision(t, id)
	}
	return out
}

func entityRevision(t *testing.T, id string) uint64 {
	t.Helper()
	return kvRevision(t, upstreamgraph.BucketEntityStates, id)
}

func kvRevision(t *testing.T, bucketName, key string) uint64 {
	t.Helper()
	bucket, err := requireBroker(t).Client.GetKeyValueBucket(t.Context(), bucketName)
	if err != nil {
		t.Fatalf("open %s: %v", bucketName, err)
	}
	entry, err := bucket.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("read %s[%s]: %v", bucketName, key, err)
	}
	return entry.Revision()
}
