package e2e_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

type bellweatherTurn struct {
	key, text string
	kind      payload.CaseDecisionKind
	wantCase  vocabulary.CasePhase
}

func TestE2E_BellweatherMysteryCompanionAcceptance(t *testing.T) {
	w := newBellweatherWorld(t, "e2ebellweather")
	player := w.dial(t)
	caseID := w.entity("case", "bellweather-case")
	culpritID := w.entity("character", "judith-bell")
	motiveID := w.entity("evidence", "evidence-motive")

	turns := []bellweatherTurn{
		{"bellweather-observe-body", "I observe Harold Wren's body in the maze.", payload.CaseDecisionObserve,
			vocabulary.CasePhaseDiscovery},
		{"bellweather-investigate-green", "I investigate the fete green and its maze entrance.",
			payload.CaseDecisionInvestigate, vocabulary.CasePhaseInvestigation},
		{"bellweather-question-beatrice", "I question Dr Beatrice Crome about the missing sedative.",
			payload.CaseDecisionQuestion, vocabulary.CasePhaseInvestigation},
		{"bellweather-share-sedative", "I share Beatrice's sedative testimony with Kit Finch.",
			payload.CaseDecisionShare, vocabulary.CasePhaseInvestigation},
		{"bellweather-hint-nudge", "Kit, give me one bounded hint.",
			payload.CaseDecisionRequestHint, vocabulary.CasePhaseInvestigation},
		{"bellweather-hint-connect", "Kit, connect only the evidence you already know.",
			payload.CaseDecisionRequestHint, vocabulary.CasePhaseInvestigation},
		{"bellweather-hint-next", "Kit, name a bounded next step if the evidence supports it.",
			payload.CaseDecisionRequestHint, vocabulary.CasePhaseInvestigation},
		{"bellweather-wrong-accusation", "I accuse Eleanor: the bell wire, motivated by the ledger.",
			payload.CaseDecisionAccuse, vocabulary.CasePhaseAccusation},
		{"bellweather-correct-accusation", "I accuse Judith Bell: the bell wire, motivated by Harold's dismissal threat.",
			payload.CaseDecisionAccuse, vocabulary.CasePhaseDenouement},
	}

	deliveries := make([]*payload.TurnDelivery, 0, len(turns))
	turnIDs := make([]string, 0, len(turns))
	for index, action := range turns {
		response := player.submit(t, action.key, action.text)
		if response.Status != payload.StatusAccepted {
			t.Fatalf("turn %d submission refused: %+v", index+1, response.Refusal)
		}
		turnIDs = append(turnIDs, response.TurnID)
		turnEntityID := w.turnEntity(t, response.TurnID)
		if phase := awaitTerminal(t, turnEntityID); phase != vocabulary.PhaseComplete {
			t.Fatalf("turn %d ended in %s (%s)", index+1, phase,
				stringObject(t, entityState(t, turnEntityID), vocabulary.TurnFailureReason))
		}
		frame := player.await(t, playersocket.FrameTurnDelivery, turnBudget)
		if frame.Delivery == nil || frame.Delivery.Result.TurnID != response.TurnID {
			t.Fatalf("turn %d delivery = %+v", index+1, frame.Delivery)
		}
		deliveries = append(deliveries, frame.Delivery)
		retrieved := player.retrieve(t, playersocket.RetrieveByTurn, response.TurnID)
		if retrieved.Status != playersocket.RetrieveFound || retrieved.Delivery == nil ||
			retrieved.Delivery.Result.TurnID != response.TurnID ||
			!reflect.DeepEqual(retrieved.Delivery, frame.Delivery) {
			t.Fatalf("turn %d retrieval = %+v", index+1, retrieved)
		}

		state := entityState(t, turnEntityID)
		if got := payload.CaseDecisionKind(stringObject(t, state, vocabulary.TurnCaseDecisionKind)); got != action.kind {
			t.Fatalf("turn %d decision kind = %q, want %q", index+1, got, action.kind)
		}
		for _, predicate := range []vocabulary.Predicate{
			vocabulary.TurnKnowledgeRef, vocabulary.TurnAccusationRef, vocabulary.TurnCaseProgressRef,
		} {
			if stringObject(t, state, predicate) == "" {
				t.Fatalf("turn %d completed without universal barrier %s", index+1, predicate)
			}
		}
		got := awaitCasePhase(t, caseID, action.wantCase)
		t.Logf("turn %d complete: %s, case phase %s", index+1, action.kind, got)

		if index == 0 {
			assertBellweatherCanariesAbsent(t, w, player, "evidence-wire", "Freshly cut wire end", culpritID,
				"Judith Bell", motiveID,
				"case.solution.culprit", "case.solution.method", "case.solution.motive",
				"Threatened churchwarden dismissal")
			assertBellweatherRoleContains(t, w, "casekeeper",
				culpritID, "Judith Bell", motiveID, "Threatened churchwarden dismissal",
				"case.solution.culprit", "case.solution.method", "case.solution.motive")
			before := w.mock.Totals().Calls
			republishStageTrigger(t, vocabulary.PhaseInterpreting, turnEntityID)
			if after := w.mock.Totals().Calls; after != before {
				t.Fatalf("redelivered finished interpretation bought %d extra model call(s)", after-before)
			}
		}
		if index == 1 {
			assertBellweatherRoleContains(t, w, "narrator", "evidence-wire", "Freshly cut wire end")
		}
		if index < len(turns)-1 {
			assertBellweatherCanariesAbsent(t, w, player, culpritID, "Judith Bell", motiveID,
				"case.solution.culprit", "case.solution.method", "case.solution.motive",
				"Threatened churchwarden dismissal")
		}
	}

	assertBellweatherKnowledge(t, w, turnIDs)
	assertBellweatherHints(t, deliveries)
	assertBellweatherAccusations(t, w, turnIDs[7], turnIDs[8])
	assertBellweatherCallBudget(t, w)
	finalNarrator := w.mock.CallsFor("narrator")[8].Body
	for _, disclosed := range []string{
		w.entity("character", "judith-bell"), w.entity("item", "bell-wire"),
		w.entity("evidence", "evidence-motive"),
	} {
		if !bytes.Contains(finalNarrator, []byte(disclosed)) {
			t.Fatalf("authorized denouement narrator context omitted %q: %s", disclosed, finalNarrator)
		}
	}

	firstTurnEntityID := w.turnEntity(t, turnIDs[0])
	requireNothingQueuedFor(t, firstTurnEntityID)
	beforeArtifacts := bellweatherArtifactSnapshot(t, firstTurnEntityID)
	if got := bellweatherDeliveryCount(t, player, turnIDs[0]); got != 1 {
		t.Fatalf("first turn has %d terminal deliveries before duplicate, want exactly one", got)
	}
	beforeDuplicate := w.mock.Totals().Calls
	duplicate := player.submit(t, turns[0].key, turns[0].text)
	if duplicate.Status != payload.StatusAccepted || duplicate.TurnID != turnIDs[0] ||
		duplicate.ActionID == "" || duplicate.ActionID != deliveries[0].Result.ActionID {
		t.Fatalf("duplicate action did not converge on first turn: %+v", duplicate)
	}
	player.noFrameWithin(t, 500*time.Millisecond)
	if after := w.mock.Totals().Calls; after != beforeDuplicate {
		t.Fatalf("duplicate action bought %d extra model call(s)", after-beforeDuplicate)
	}
	afterRetrieval := player.retrieve(t, playersocket.RetrieveByTurn, turnIDs[0])
	if afterRetrieval.Status != playersocket.RetrieveFound || afterRetrieval.Delivery == nil ||
		!reflect.DeepEqual(afterRetrieval.Delivery, deliveries[0]) {
		t.Fatalf("duplicate retrieval diverged from the one logical delivery: %#v", afterRetrieval)
	}
	if got := bellweatherDeliveryCount(t, player, turnIDs[0]); got != 1 {
		t.Fatalf("first turn has %d terminal deliveries after duplicate, want exactly one", got)
	}
	afterArtifacts := bellweatherArtifactSnapshot(t, firstTurnEntityID)
	if !reflect.DeepEqual(afterArtifacts, beforeArtifacts) {
		t.Fatalf("duplicate changed logical artifact set: before=%v after=%v", beforeArtifacts, afterArtifacts)
	}
	requireNothingQueuedFor(t, firstTurnEntityID)

	finalRaw := bytes.Join(player.rawFrames(), nil)
	if !bytes.Contains(finalRaw, []byte("Judith Bell")) ||
		!bytes.Contains(finalRaw, []byte("churchwarden dismissal")) {
		t.Fatalf("denouement delivery/retrieval did not disclose the authorized solution: %s", finalRaw)
	}
}

func awaitCasePhase(t *testing.T, caseID string, want vocabulary.CasePhase) vocabulary.CasePhase {
	t.Helper()
	deadline := time.Now().Add(turnBudget)
	last := vocabulary.CasePhase("")
	for time.Now().Before(deadline) {
		state, err := graphStore(t).GetEntity(t.Context(), caseID)
		if err == nil && !state.IsStub() {
			last = vocabulary.CasePhase(stringObject(t, state, vocabulary.CaseLifecyclePhase))
			if last == want {
				return last
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("case %s never reached phase %q (last seen %q)", caseID, want, last)
	return ""
}

func bellweatherArtifactSnapshot(t *testing.T, turnEntityID string) map[vocabulary.Predicate]string {
	t.Helper()
	state := entityState(t, turnEntityID)
	predicates := []vocabulary.Predicate{
		vocabulary.TurnActionRef, vocabulary.TurnCaseDecisionRef, vocabulary.TurnVerdictRef,
		vocabulary.TurnRollRef, vocabulary.TurnEffectsRef, vocabulary.TurnKnowledgeRef,
		vocabulary.TurnAccusationRef, vocabulary.TurnCaseProgressRef,
		vocabulary.TurnCompanionStageRef, vocabulary.TurnCompanionDecisionRef, vocabulary.TurnNarrationRef,
	}
	snapshot := make(map[vocabulary.Predicate]string, len(predicates))
	for _, predicate := range predicates {
		if value := stringObject(t, state, predicate); value != "" {
			snapshot[predicate] = value
		}
	}
	return snapshot
}

func bellweatherDeliveryCount(t *testing.T, player *client, turnID string) int {
	t.Helper()
	count := 0
	for _, raw := range player.rawFrames() {
		var frame playersocket.Frame
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatalf("decode captured player frame: %v", err)
		}
		if frame.Type == playersocket.FrameTurnDelivery && frame.Delivery != nil &&
			frame.Delivery.Result.TurnID == turnID {
			count++
		}
	}
	return count
}

func assertBellweatherKnowledge(t *testing.T, w *world, turnIDs []string) {
	t.Helper()
	store := w.contentStore(t)
	read := func(turnID string) *content.KnowledgeReceipt {
		state := entityState(t, w.turnEntity(t, turnID))
		ref, err := content.ParseRef(stringObject(t, state, vocabulary.TurnKnowledgeRef))
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := store.GetKnowledgeReceipt(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	first := read(turnIDs[0])
	if first.Status != content.KnowledgeCommitted || len(first.Entries) != 0 {
		t.Fatalf("body observation knowledge = %#v", first)
	}
	second := read(turnIDs[1])
	wire, rowan, kit := w.entity("evidence", "evidence-wire"), w.entity("character", "rowan-vale"),
		w.entity("character", "kit-finch")
	seen := map[string]bool{}
	for _, entry := range second.Entries {
		if entry.EvidenceID == wire {
			seen[entry.RecipientID] = true
		}
	}
	if !seen[rowan] || !seen[kit] {
		t.Fatalf("investigation did not commit player+witness wire knowledge: %#v", second.Entries)
	}
	third := read(turnIDs[2])
	if len(third.Entries) != 1 || third.Entries[0].RecipientID != rowan ||
		third.Entries[0].EvidenceID != w.entity("evidence", "evidence-sedative") ||
		third.Entries[0].TestimonyRef == "" {
		t.Fatalf("question testimony receipt = %#v", third.Entries)
	}
	testimonyRef, err := content.ParseRef(third.Entries[0].TestimonyRef)
	if err != nil {
		t.Fatal(err)
	}
	testimony, err := store.GetTestimony(t.Context(), testimonyRef)
	if err != nil || testimony.SourceActorID != w.entity("character", "beatrice-crome") ||
		testimony.BeliefID != w.entity("belief", "belief-beatrice") {
		t.Fatalf("Beatrice testimony = %#v, %v", testimony, err)
	}
	fourth := read(turnIDs[3])
	if len(fourth.Entries) != 1 || fourth.Entries[0].RecipientID != kit ||
		fourth.Entries[0].EvidenceID != w.entity("evidence", "evidence-sedative") {
		t.Fatalf("share receipt = %#v", fourth.Entries)
	}
}

func assertBellweatherHints(t *testing.T, deliveries []*payload.TurnDelivery) {
	t.Helper()
	for offset, want := range []struct {
		kind  payload.CompanionDecisionKind
		level vocabulary.HintLevel
	}{
		{payload.CompanionDecisionHint, vocabulary.HintLevelNudge},
		{payload.CompanionDecisionHint, vocabulary.HintLevelConnect},
		{payload.CompanionDecisionHint, vocabulary.HintLevelNextStep},
	} {
		got := deliveries[4+offset].Result.CompanionResolution
		if got == nil || got.Kind != want.kind || got.HintLevel != want.level {
			t.Fatalf("hint turn %d resolution = %#v, want %s/%s", offset+1, got, want.kind, want.level)
		}
	}
}

func assertBellweatherAccusations(t *testing.T, w *world, wrongTurnID, correctTurnID string) {
	t.Helper()
	store := w.contentStore(t)
	for turnID, want := range map[string]payload.AccusationOutcome{
		wrongTurnID: payload.AccusationIncorrect, correctTurnID: payload.AccusationCorrect,
	} {
		state := entityState(t, w.turnEntity(t, turnID))
		ref, err := content.ParseRef(stringObject(t, state, vocabulary.TurnAccusationRef))
		if err != nil {
			t.Fatal(err)
		}
		record, err := store.GetAccusationRecord(t.Context(), ref)
		if err != nil || record.Result == nil || record.Result.Outcome != want {
			t.Fatalf("accusation %s = %#v, %v; want %s", turnID, record, err, want)
		}
	}
}

func assertBellweatherCallBudget(t *testing.T, w *world) {
	t.Helper()
	if totals := w.mock.Totals(); totals.Calls != 27 || totals.Refusals != 0 ||
		totals.PromptTokens != 2430 || totals.CompletionTokens != 486 {
		t.Fatalf("mock totals = %+v, want 27 calls, no refusals, 2430/486 tokens", totals)
	}
	for _, role := range []string{"casekeeper", "adjudicator", "narrator"} {
		if calls := w.mock.CallsFor(role); len(calls) != 9 {
			t.Fatalf("role %s received %d calls, want 9", role, len(calls))
		}
	}
	if calls := w.mock.CallsFor("companion"); len(calls) != 0 {
		t.Fatalf("deterministic companion path made %d model call(s)", len(calls))
	}
}

func assertBellweatherRoleContains(t *testing.T, w *world, role string, canaries ...string) {
	t.Helper()
	var bodies [][]byte
	for _, call := range w.mock.CallsFor(role) {
		bodies = append(bodies, call.Body)
	}
	joined := bytes.Join(bodies, nil)
	for _, canary := range canaries {
		if !bytes.Contains(joined, []byte(canary)) {
			t.Fatalf("role %s positive-control context omitted %q: %s", role, canary, joined)
		}
	}
}

func assertBellweatherCanariesAbsent(t *testing.T, w *world, player *client, canaries ...string) {
	t.Helper()
	var surfaces [][]byte
	// The casekeeper is the private mystery judge and deliberately receives the
	// complete authored case. The confidentiality boundary under test is the
	// public adjudicator/narrator projection/request and player egress before denouement.
	for _, call := range w.mock.CallsFor("adjudicator") {
		surfaces = append(surfaces, call.Body)
	}
	for _, call := range w.mock.CallsFor("narrator") {
		surfaces = append(surfaces, call.Body)
	}
	for _, response := range w.wire.Responses() {
		if bytes.Contains(response.Body, []byte("-narrator-")) ||
			bytes.Contains(response.Body, []byte("-adjudicator-")) {
			surfaces = append(surfaces, response.Body)
		}
	}
	surfaces = append(surfaces, player.rawFrames()...)
	joined := bytes.Join(surfaces, nil)
	for _, canary := range canaries {
		if bytes.Contains(joined, []byte(canary)) {
			t.Fatalf("secret canary %q crossed a pre-denouement surface: %s", canary, joined)
		}
	}
}
