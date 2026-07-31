package e2e_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/model/wire"

	"github.com/c360studio/semmachina/internal/mockmodel"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/playersocket"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// A whole turn, with no dice, from a socket frame to a committed world change and
// a delivered story.
//
// This is the first test in the repository where a turn goes end to end through a
// model. Everything it asserts is a specific fact rather than "the phase reached
// complete", because a completed turn is compatible with almost any defect
// upstream of the last stage: an adjudicator that was never asked, a batch that
// committed the wrong band, a narration nobody stored, an archive that missed it.
func TestE2E_ANoRollTurnResolvesEndToEnd(t *testing.T) {
	w := newWorld(t, "e2enoroll", "no-roll")
	player := w.dial(t)

	const action = "I take the sentry's supper off the winch housing while nobody is looking."
	response := player.submit(t, "no-roll-1", action)
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the engine refused the submission: %+v", response.Refusal)
	}
	turnEntityID := w.turnEntity(t, response.TurnID)

	if phase := awaitTerminal(t, turnEntityID); phase != vocabulary.PhaseComplete {
		t.Fatalf("the turn ended in %q, want %q (failure reason %q)",
			phase, vocabulary.PhaseComplete,
			stringObject(t, entityState(t, turnEntityID), vocabulary.TurnFailureReason))
	}
	state := entityState(t, turnEntityID)

	// The adjudicator's judgment reached the rule-matching surface as the closed
	// scalars the script declared — not as whatever the model said, and not as
	// prose. These four are the whole of what a rule may branch on.
	for _, want := range []struct {
		predicate vocabulary.Predicate
		value     string
	}{
		{vocabulary.TurnVerdictPlausibility, string(vocabulary.PlausibilityCertain)},
		{vocabulary.TurnVerdictRisk, string(vocabulary.RiskNone)},
		{vocabulary.TurnVerdictConsequence, string(vocabulary.ConsequenceNone)},
		{vocabulary.TurnVerdictRequiresRoll, "false"},
	} {
		if got := stringObject(t, state, want.predicate); got != want.value {
			t.Errorf("%s = %q, want %q", want.predicate, got, want.value)
		}
	}

	// The dice never ran, and that is a fact rather than an absence of evidence:
	// the verdict declined them, so the applier's own coherence check would have
	// refused the turn had a band appeared.
	requireAbsent(t, turnEntityID, vocabulary.TurnRollBand,
		"the verdict declined the dice, so a recorded band means something rolled that nothing authorised")

	// Every artifact the turn produced is on the turn, by reference.
	for _, predicate := range []vocabulary.Predicate{
		vocabulary.TurnActionRef, vocabulary.TurnVerdictRef,
		vocabulary.TurnEffectsBatch, vocabulary.TurnEffectsRef, vocabulary.TurnNarrationRef,
	} {
		if stringObject(t, state, predicate) == "" {
			t.Errorf("the completed turn carries no %s", predicate)
		}
	}

	// The world changed, and it changed the way a multi-valued write has to: the
	// new object joined the set instead of replacing it. The merge lane writes a
	// predicate's WHOLE value set, so an applier that published only the new
	// object would have left Rook carrying the rations alone, with a success
	// response and no error anywhere (F14's third face).
	rook := entityState(t, w.entity("character", starterCharacter))
	carried := objectsFor(rook, vocabulary.WorldRelationCarries)
	slices.Sort(carried)
	want := []string{
		w.entity("item", starterCrowbar), w.entity("item", starterLantern), w.entity("item", starterRations),
	}
	slices.Sort(want)
	if !slices.Equal(carried, want) {
		t.Errorf("Rook carries %v, want %v; the committed effect added the rations, and the two items he "+
			"already had must survive it", carried, want)
	}

	// Exactly one billed call per persona, and no refusals. The scripts hold one
	// step each, so a second call would have run off the end and been refused —
	// which is the same assertion twice, from both directions.
	requireCallBudget(t, w, 1, 1)

	// Spend is attributable and non-zero. A token-free run that reported no tokens
	// would make the flat-cost-per-turn claim untestable (M7).
	totals := w.mock.Totals()
	if wantTokens := 1180 + 96 + 940 + 74; totals.TotalTokens() != wantTokens {
		t.Errorf("the turn's scripted spend totals %d tokens, want %d", totals.TotalTokens(), wantTokens)
	}

	requireProviderShape(t, w)
	requirePromptsCarriedTheAction(t, w, action, vocabulary.BandAuto)

	// The player was told, on the socket they were still holding.
	delivery := player.await(t, playersocket.FrameTurnDelivery, turnBudget).Delivery
	if err := delivery.Validate(); err != nil {
		t.Fatalf("the delivered document does not satisfy its own contract: %v", err)
	}
	if delivery.Result.TurnID != response.TurnID {
		t.Errorf("the delivery names turn %q, want %q", delivery.Result.TurnID, response.TurnID)
	}
	if delivery.Result.Phase != vocabulary.PhaseComplete {
		t.Errorf("the delivered result reports phase %q", delivery.Result.Phase)
	}
	if delivery.Narration == nil || !strings.Contains(delivery.Narration.Prose, "bread is still warm") {
		t.Errorf("the delivered prose is not the narrator's scripted words: %+v", delivery.Narration)
	}
	if delivery.Result.Resolution == nil || delivery.Result.Resolution.Roll != nil {
		t.Errorf("the delivered resolution carries a roll for a turn that never rolled: %+v",
			delivery.Result.Resolution)
	}

	// The archive holds the turn once, and it holds the roll-gate agreement the
	// ledger exists to make queryable.
	manifest := awaitManifest(t, response.TurnID)
	if manifest.TurnID != response.TurnID {
		t.Errorf("the manifest names turn %q", manifest.TurnID)
	}
	if manifest.Phase != vocabulary.PhaseComplete {
		t.Errorf("the manifest records phase %q", manifest.Phase)
	}
	if manifest.RollGate.Reported {
		t.Error("the manifest records a reported roll gate of true for a turn that declined the dice")
	}
	if count := manifestsFor(t, response.TurnID); count != 1 {
		t.Errorf("the archive holds %d manifests for turn %s, want exactly 1", count, response.TurnID)
	}

	requireNothingQueuedFor(t, turnEntityID)
}

// One turn per outcome band, chosen by the SEED and not by the script.
//
// The verdict is byte-identical across the three: it declares intents for all
// three bands, and modifier sums are bounded precisely so it cannot pre-determine
// the result (D3, F19). What differs is the (campaign_seed, turn_id) pair the run
// supplies, and what each case proves is that the applier committed THAT band's
// intents — asserted by checking the other two bands' effects did NOT land, which
// is the half that distinguishes "a band was committed" from "the right one was".
func TestE2E_EachOutcomeBandIsSelectedBySeedAndCommitted(t *testing.T) {
	for _, fixture := range bandFixtures {
		t.Run(string(fixture.Band), func(t *testing.T) {
			w := newWorld(t, fixture.WorldNS, fixture.Scenario, withCampaignSeed(pinnedSeed(t)))
			player := w.dial(t)

			response := player.submit(t, fixture.Key, "I lever the winch with the crowbar and get under the gate.")
			if response.Status != payload.StatusAccepted {
				t.Fatalf("the engine refused the submission: %+v", response.Refusal)
			}
			// The pinned turn id is an INPUT to the roll, so a turn that got a
			// different one would roll a different band and every assertion below
			// would be about a different experiment.
			if response.TurnID != fixture.TurnID {
				t.Fatalf("the engine derived turn %q and the pinned pair names %q; the seed search that chose "+
					"this band was done for a turn this run does not have",
					response.TurnID, fixture.TurnID)
			}
			turnEntityID := w.turnEntity(t, response.TurnID)

			if phase := awaitTerminal(t, turnEntityID); phase != vocabulary.PhaseComplete {
				t.Fatalf("the turn ended in %q (failure reason %q)", phase,
					stringObject(t, entityState(t, turnEntityID), vocabulary.TurnFailureReason))
			}
			state := entityState(t, turnEntityID)

			// The seeded dice produced exactly the pinned roll.
			if got := stringObject(t, state, vocabulary.TurnRollBand); got != string(fixture.Band) {
				t.Fatalf("the turn landed on band %q, want %q; the campaign seed reached the dice as something "+
					"other than the pinned one", got, fixture.Band)
			}
			if got := stringObject(t, state, vocabulary.TurnRollTotal); got != fmt.Sprint(fixture.Total) {
				t.Errorf("the turn recorded total %s, want %d", got, fixture.Total)
			}

			// The committed world change is THIS band's and neither of the others'.
			// The three bands touch three different predicates on purpose.
			rook := entityState(t, w.entity("character", starterCharacter))
			status := stringObject(t, rook, vocabulary.CharacterStatusCurrent)
			stamina := stringObject(t, rook, vocabulary.CharacterAttributeStamina)
			hostile := objectsFor(rook, vocabulary.WorldRelationHostileTo)

			committed := map[vocabulary.OutcomeBand]bool{
				vocabulary.BandMiss:    status == string(vocabulary.StatusRestrained),
				vocabulary.BandPartial: stamina == "4",
				vocabulary.BandFull:    len(hostile) == 0,
			}
			for band, landed := range committed {
				switch {
				case band == fixture.Band && !landed:
					t.Errorf("the turn landed on %q and that band's effect did not commit "+
						"(status=%q stamina=%s hostile-to=%v)", band, status, stamina, hostile)
				case band != fixture.Band && landed:
					t.Errorf("the turn landed on %q and the %q band's effect committed as well "+
						"(status=%q stamina=%s hostile-to=%v); the applier commits ONE band's intents",
						fixture.Band, band, status, stamina, hostile)
				}
			}

			requireCallBudget(t, w, 1, 1)

			// The narrator was told which band it is voicing — engine knowledge,
			// never asked of the model — and the delivered prose is that band's.
			requirePromptsCarriedTheAction(t, w, "lever the winch", fixture.Band)

			delivery := player.await(t, playersocket.FrameTurnDelivery, turnBudget).Delivery
			if delivery.Narration == nil || delivery.Narration.Band != fixture.Band {
				t.Fatalf("the delivered narration voices %+v, want band %q", delivery.Narration, fixture.Band)
			}
			if delivery.Result.Resolution == nil || delivery.Result.Resolution.Roll == nil {
				t.Fatal("the delivered resolution carries no roll for a turn that rolled")
			}
			if got := delivery.Result.Resolution.Roll.Total; got != fixture.Total {
				t.Errorf("the delivered resolution card reports total %d, want %d", got, fixture.Total)
			}
			if got := delivery.Result.Resolution.Roll.Dice; !slices.Equal(got, fixture.Dice) {
				t.Errorf("the delivered resolution card reports dice %v, want %v", got, fixture.Dice)
			}

			// Replay honesty, closed at the archive. The manifest carries a
			// REFERENCE to the roll and never the roll itself, so the check that
			// means anything is the replay reader re-executing it from the campaign
			// seed and the verdict's own modifiers and getting byte-identical dice.
			// That is the claim the whole seeded-dice design rests on, and this is
			// the only place in the repository where it is made against a roll a
			// live engine produced rather than one a test constructed.
			manifest := awaitManifest(t, response.TurnID)
			if manifest.RollRef == "" {
				t.Fatal("the manifest records no roll reference for a turn that rolled")
			}
			replay, err := w.replayReader(t).Replay(t.Context(), manifest)
			if err != nil {
				t.Fatalf("replay the archived turn: %v", err)
			}
			if replay.Reproduced == nil {
				t.Fatal("the replay reproduced no roll for a turn that rolled")
			}
			if replay.Reproduced.Band != fixture.Band || replay.Reproduced.Total != fixture.Total {
				t.Errorf("the replayed roll is band %q total %d, want %q %d",
					replay.Reproduced.Band, replay.Reproduced.Total, fixture.Band, fixture.Total)
			}
			if !slices.Equal(replay.Reproduced.Dice, fixture.Dice) {
				t.Errorf("the replayed dice are %v, want %v", replay.Reproduced.Dice, fixture.Dice)
			}
			if replay.Narration == nil || replay.Narration.Band != fixture.Band {
				t.Errorf("the replay read no narration for this band: %+v", replay.Narration)
			}

			requireNothingQueuedFor(t, turnEntityID)
		})
	}
}

// A well-formed verdict the APPLIER refuses: a character moved into an item.
//
// Every value in it is in the closed vocabulary and every field is the right
// shape, so the terminal tool accepts it — a wrong-KIND target can only be caught
// by something that reads the graph, which is F16's own point. What this proves is
// the whole rejection path: the batch commits nothing, the turn ends in `failed`
// with a CLOSED code and a stored explanation, the narrator is never billed for a
// turn that did not happen, and the player is still told.
func TestE2E_TheApplierRefusesAWrongKindTargetAndEndsTheTurn(t *testing.T) {
	w := newWorld(t, "e2ereject", "invalid-effect")
	player := w.dial(t)

	// The facts the refused batch must leave exactly as it found them.
	before := entityState(t, w.entity("character", starterCharacter))
	locationBefore := stringObject(t, before, vocabulary.WorldLocationCurrent)
	if locationBefore != w.entity("scene", starterScene) {
		t.Fatalf("Rook starts at %q, and this test's premise is that he starts in the scene", locationBefore)
	}

	response := player.submit(t, "reject-1", "I climb inside the crowbar.")
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the engine refused the submission: %+v", response.Refusal)
	}
	turnEntityID := w.turnEntity(t, response.TurnID)

	if phase := awaitTerminal(t, turnEntityID); phase != vocabulary.PhaseFailed {
		t.Fatalf("the turn ended in %q, want %q; the applier accepted a character moved into an item",
			phase, vocabulary.PhaseFailed)
	}
	state := entityState(t, turnEntityID)

	if got := stringObject(t, state, vocabulary.TurnFailureReason); got !=
		string(vocabulary.FailureEffectEntityKind) {
		t.Errorf("the turn failed with reason %q, want %q; the code is what a rule and an operator branch on, "+
			"and a different one would name a different defect", got, vocabulary.FailureEffectEntityKind)
	}
	if stringObject(t, state, vocabulary.TurnFailureRef) == "" {
		t.Error("the failed turn carries no failure detail reference; the closed code says WHAT and the " +
			"reference is the only place the explanation can live")
	}

	// Nothing committed. Validation completes before the first write, so a
	// rejected batch applies nothing at all.
	requireAbsent(t, turnEntityID, vocabulary.TurnEffectsBatch,
		"the batch was refused, so a batch marker would say the world changed when it did not")
	after := entityState(t, w.entity("character", starterCharacter))
	if got := stringObject(t, after, vocabulary.WorldLocationCurrent); got != locationBefore {
		t.Errorf("Rook is now at %q, was %q; the refused intent was committed anyway", got, locationBefore)
	}

	// The narrator was never called. A failed turn is terminal and the narration
	// hop is gated on the effect-batch marker the applier never wrote — the
	// scenario scripts a narration step precisely so an engine that narrated
	// anyway would be answered rather than refused, and would show up here as a
	// call instead of as a confusing 400.
	requireCallBudget(t, w, 1, 0)
	requireAbsent(t, turnEntityID, vocabulary.TurnNarrationRef,
		"a turn that failed before the narrator ran has no prose, and a reference here would point at fiction "+
			"about a world change that never happened")

	// The player is still told. The turn a player most needs an answer about is
	// the one that failed, and silence is indistinguishable from a turn still
	// running.
	delivery := player.await(t, playersocket.FrameTurnDelivery, turnBudget).Delivery
	if err := delivery.Validate(); err != nil {
		t.Fatalf("the delivered failure does not satisfy its own contract: %v", err)
	}
	if delivery.Result.Phase != vocabulary.PhaseFailed {
		t.Errorf("the delivered result reports phase %q, want %q", delivery.Result.Phase, vocabulary.PhaseFailed)
	}
	if delivery.Result.FailureReason != vocabulary.FailureEffectEntityKind {
		t.Errorf("the delivered result reports reason %q", delivery.Result.FailureReason)
	}
	if delivery.Narration != nil {
		t.Errorf("the delivery carries prose for a turn that never reached the narrator: %+v", delivery.Narration)
	}

	// The archive records failed turns too (design D6).
	manifest := awaitManifest(t, response.TurnID)
	if manifest.Phase != vocabulary.PhaseFailed ||
		manifest.FailureReason != string(vocabulary.FailureEffectEntityKind) {
		t.Errorf("the archived manifest reports %q/%q", manifest.Phase, manifest.FailureReason)
	}

	requireNothingQueuedFor(t, turnEntityID)
}

// The same action, delivered twice: one turn, one adjudication, one narration,
// one world change, one manifest.
//
// The duplicate is a genuine REDELIVERY — the identical bytes the gateway
// published, put back on PLAYER_ACTIONS — rather than a second submission,
// because that is the shape at-least-once actually produces and the shape D2's
// idempotency guards were written for. A second submission is refused at the
// admission gate and would prove a different, easier thing.
func TestE2E_ARedeliveredActionMakesOneTurnAndBuysNoSecondCall(t *testing.T) {
	w := newWorldUnstarted(t, "e2edup", "duplicate-delivery")

	// The stream has to exist before anything can be read off it, and the engine
	// is what creates it. Booting first and then capturing the published action is
	// the ordering; the capture itself is a read.
	w.boot(t)
	player := w.dial(t)

	response := player.submit(t, "dup-1", "I pocket the supper and keep walking.")
	if response.Status != payload.StatusAccepted {
		t.Fatalf("the engine refused the submission: %+v", response.Refusal)
	}
	turnEntityID := w.turnEntity(t, response.TurnID)

	// The bytes the gateway put on the wire, read back and published again. This
	// is what a JetStream redelivery hands the intake consumer.
	raw := lastActionBytes(t)
	if !strings.Contains(string(raw), response.ActionID) {
		t.Fatalf("the captured action does not name action %s; the redelivery would be about another turn",
			response.ActionID)
	}
	if err := requireBroker(t).Client.PublishToStream(t.Context(), turn.ActionSubject, raw); err != nil {
		t.Fatalf("redeliver the action: %v", err)
	}

	if phase := awaitTerminal(t, turnEntityID); phase != vocabulary.PhaseComplete {
		t.Fatalf("the turn ended in %q (failure reason %q)", phase,
			stringObject(t, entityState(t, turnEntityID), vocabulary.TurnFailureReason))
	}

	// The assertion the scripts make for us: one step each, so a second
	// adjudication would have run off the end and been REFUSED rather than
	// answered. Both halves are checked — the count, and that nothing was refused
	// — because a refusal is what a duplicate would look like from the model's side.
	requireCallBudget(t, w, 1, 1)

	// One turn, not two. The action id is derived from the authenticated player
	// and the client's key, so a redelivery addresses the same turn entity; a
	// second turn would carry a different id and the player's pointer would name it.
	playerState := entityState(t, w.playerID)
	current := objectsFor(playerState, vocabulary.PlayerTurnCurrent)
	if len(current) != 1 || current[0] != turnEntityID {
		t.Errorf("the player points at %v, want exactly [%s]; a redelivery that made a second turn would "+
			"show up here", current, turnEntityID)
	}

	// One world change. The effect adds the rations to what Rook already carries,
	// so a batch applied twice under set semantics is invisible in the count —
	// which is why the assertion is the whole set rather than its size.
	rook := entityState(t, w.entity("character", starterCharacter))
	carried := objectsFor(rook, vocabulary.WorldRelationCarries)
	slices.Sort(carried)
	want := []string{w.entity("item", starterCrowbar), w.entity("item", starterLantern), w.entity("item", starterRations)}
	slices.Sort(want)
	if !slices.Equal(carried, want) {
		t.Errorf("Rook carries %v, want %v", carried, want)
	}

	if count := manifestsFor(t, response.TurnID); count != 1 {
		t.Errorf("the archive holds %d manifests for turn %s, want exactly 1", count, response.TurnID)
	}
	requireNothingQueuedFor(t, turnEntityID)
}

// assertions -----------------------------------------------------------------

// requireCallBudget asserts how many times each persona was billed, and that the
// stub refused nothing.
//
// The refusal count is the half that would otherwise hide: a stub refusal is a
// 400 the framework client reports as a provider error, which a persona loop can
// absorb into an iteration and a turn can absorb into a retry. A run with the
// right call count and a refusal in it is a run where something called the model
// one more time than the scenario allows.
func requireCallBudget(t *testing.T, w *world, adjudications, narrations int) {
	t.Helper()
	if got := len(w.mock.CallsFor("adjudicator")); got != adjudications {
		t.Errorf("the adjudicator was called %d time(s), want %d", got, adjudications)
	}
	if got := len(w.mock.CallsFor("narrator")); got != narrations {
		t.Errorf("the narrator was called %d time(s), want %d", got, narrations)
	}
	for _, call := range w.mock.Calls() {
		if call.Refusal != "" {
			t.Errorf("the scripted model refused a call [%s] (role %q); the loop asked for more than the "+
				"scenario allows", call.Refusal, call.Role)
		}
	}
}

// requireProviderShape asserts the bytes the stub put on the socket are shaped
// like a provider's, decoded from the RAW response rather than through the
// framework's client.
//
// This is F18 applied to the end-to-end run. The client recomputes total_tokens,
// infers a tool call from the presence of tool_calls regardless of finish_reason,
// and substitutes an empty argument map for arguments it cannot parse — so a
// response with a zeroed total, a `stop` finish reason on a tool call, or
// truncated argument bytes is INDISTINGUISHABLE from a correct one when asserted
// through it. Three mock mutations survived on exactly that basis.
func requireProviderShape(t *testing.T, w *world) {
	t.Helper()
	responses := w.wire.Responses()
	if len(responses) == 0 {
		t.Fatal("the stub answered nothing; no model call was made at all")
	}

	checked := 0
	for index, response := range responses {
		if response.Status != 200 {
			t.Errorf("response %d is HTTP %d: %s", index, response.Status, response.Body)
			continue
		}
		var completion wireCompletion
		if err := json.Unmarshal(response.Body, &completion); err != nil {
			t.Fatalf("response %d is not a chat completion: %v (%s)", index, err, response.Body)
		}
		if completion.Object != "chat.completion" {
			t.Errorf("response %d carries object %q", index, completion.Object)
		}
		if len(completion.Choices) != 1 {
			t.Fatalf("response %d carries %d choices, want 1", index, len(completion.Choices))
		}
		choice := completion.Choices[0]
		if len(choice.Message.ToolCalls) == 0 {
			t.Fatalf("response %d carries no tool call, so the persona had no exit to take", index)
		}
		// The field the client does NOT need and a real provider always sets.
		if choice.FinishReason != "tool_calls" {
			t.Errorf("response %d finishes with %q while carrying tool calls; the framework client infers a "+
				"tool call from the calls alone, so this is invisible to every assertion made through it",
				index, choice.FinishReason)
		}
		// The total the client recomputes for itself.
		if got, want := completion.Usage.TotalTokens,
			completion.Usage.PromptTokens+completion.Usage.CompletionTokens; got != want {
			t.Errorf("response %d reports total_tokens %d, want %d (%d prompt + %d completion); the client "+
				"recomputes this, so a wrong one reaches no assertion that reads it through the client",
				index, got, want, completion.Usage.PromptTokens, completion.Usage.CompletionTokens)
		}
		if completion.Usage.TotalTokens == 0 {
			t.Errorf("response %d prices the call at nothing", index)
		}
		// The arguments the client replaces with an empty map when it cannot parse
		// them — which is how a truncated exit reads as an exit with no fields.
		for _, call := range choice.Message.ToolCalls {
			if call.Type != "function" {
				t.Errorf("response %d tool call %q has type %q", index, call.Function.Name, call.Type)
			}
			if !json.Valid([]byte(call.Function.Arguments)) {
				t.Errorf("response %d tool call %q carries arguments that are not valid JSON: %q",
					index, call.Function.Name, call.Function.Arguments)
			}
		}
		// Frozen, because a model endpoint is the easiest place in an otherwise
		// seeded system to leak a clock.
		if completion.Created != mockmodel.FrozenCreated {
			t.Errorf("response %d is stamped %d, not the stub's frozen constant %d",
				index, completion.Created, mockmodel.FrozenCreated)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no successful response was checked; the fidelity assertions ran over nothing")
	}
}

// requirePromptsCarriedTheAction asserts what the ENGINE put on the wire.
//
// Two things, and both are seams with no other caller. The player's declared
// action reaches both personas only if the prompt builder followed
// `turn.action.ref` itself — the context assembler deliberately leaves that
// reference alone, so an adjudicator handed a scene and no action is a live
// failure mode. And the narrator's task carries exactly one tool, which is what
// makes "the narrator has no mutation-capable tool" structural rather than a
// review note.
func requirePromptsCarriedTheAction(t *testing.T, w *world, actionFragment string, band vocabulary.OutcomeBand) {
	t.Helper()
	seen := map[string]bool{}
	for _, call := range w.mock.Calls() {
		if call.Refusal != "" {
			continue
		}
		seen[call.Role] = true

		// Decoded from the RAW body: what the engine actually sent, not the stub's
		// own reading of it.
		var request wire.ChatCompletionRequest
		if err := json.Unmarshal(call.Body, &request); err != nil {
			t.Fatalf("the %s request is not a chat-completions request: %v", call.Role, err)
		}
		if request.Stream {
			t.Errorf("the %s request asks for a stream", call.Role)
		}
		if len(request.Tools) != 1 {
			t.Errorf("the %s request declares %d tools, want exactly 1; the per-task allowlist is what closes "+
				"the exit vocabulary", call.Role, len(request.Tools))
		}
		prompt := promptText(t, request)
		if !strings.Contains(prompt, actionFragment) {
			t.Errorf("the %s prompt does not carry the player's declared action (%q); the assembler leaves "+
				"turn.action.ref unresolved on purpose, so a persona that never saw the action is what a "+
				"prompt builder that forgot to follow it produces", call.Role, actionFragment)
		}
	}

	if seen["narrator"] {
		narration := w.mock.CallsFor("narrator")
		if len(narration) == 0 {
			t.Fatal("unreachable: the narrator was seen and has no calls")
		}
		var request wire.ChatCompletionRequest
		if err := json.Unmarshal(narration[0].Body, &request); err != nil {
			t.Fatalf("the narrator request is not a chat-completions request: %v", err)
		}
		if len(request.Tools) == 1 && request.Tools[0].Function.Name != persona.NarrationToolName {
			t.Errorf("the narrator is offered %q, not %q", request.Tools[0].Function.Name, persona.NarrationToolName)
		}
		if !strings.Contains(promptText(t, request), string(band)) {
			t.Errorf("the narrator's prompt does not name the committed band %q; the band is engine knowledge "+
				"and the narrator is TOLD it rather than asked for it", band)
		}
	}
	adjudication := w.mock.CallsFor("adjudicator")
	if len(adjudication) > 0 {
		var request wire.ChatCompletionRequest
		if err := json.Unmarshal(adjudication[0].Body, &request); err != nil {
			t.Fatalf("the adjudicator request is not a chat-completions request: %v", err)
		}
		if len(request.Tools) == 1 && request.Tools[0].Function.Name != persona.VerdictToolName {
			t.Errorf("the adjudicator is offered %q, not %q",
				request.Tools[0].Function.Name, persona.VerdictToolName)
		}
	}
}

// promptText flattens a request's messages into one searchable string.
func promptText(t *testing.T, request wire.ChatCompletionRequest) string {
	t.Helper()
	var out strings.Builder
	for i := range request.Messages {
		text, ok := request.Messages[i].ContentString()
		if !ok {
			continue
		}
		out.WriteString(text)
		out.WriteString("\n")
	}
	return out.String()
}

// lastActionBytes reads back the most recent message on the player-action stream.
//
// The BYTES, not a re-encoding: a redelivery hands the consumer exactly what was
// stored, and a test that rebuilt the envelope would be redelivering a message
// with a fresh id and would prove idempotency against an input production never
// produces.
func lastActionBytes(t *testing.T) []byte {
	t.Helper()
	stream, err := jetStream(t).Stream(t.Context(), turn.ActionStream)
	if err != nil {
		t.Fatalf("read the %s stream: %v", turn.ActionStream, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		raw, err := stream.GetLastMsgForSubject(t.Context(), turn.ActionSubject)
		if err == nil {
			return raw.Data
		}
		if time.Now().After(deadline) {
			t.Fatalf("no player action was published on %s within 30s: %v", turn.ActionSubject, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
