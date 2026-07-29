package persona_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func newGuard(t *testing.T, store *fakeGraph) *persona.Guard {
	t.Helper()
	guard, err := persona.NewGuard(store)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	return guard
}

func refFor(t *testing.T, predicate vocabulary.Predicate) content.Ref {
	t.Helper()
	key, err := content.KeyFor(predicate, content.SubjectTurn, testTurnID)
	if err != nil {
		t.Fatalf("KeyFor: %v", err)
	}
	return content.Ref{Instance: testInstance, Key: key}
}

// The phase cannot tell an interrupted attempt from a finished one: it is
// written on stage ENTRY, so `adjudicating` is equally true of a persona that
// crashed mid-call and one that stored its verdict and died before the phase
// moved on. Re-running on that signal is not incorrect — the derived key and the
// replacing lane make it converge — it is PAID, and a persona re-run is a second
// billed model call for a judgment the engine already has.
func TestGuard_SkipsAStageWhoseArtifactAlreadyLanded(t *testing.T) {
	store := newFakeGraph(&journal{})
	store.seedTurn(
		fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseAdjudicating)),
		fact(vocabulary.TurnVerdictRef, refFor(t, vocabulary.TurnVerdictRef).String()),
	)

	resumption, err := newGuard(t, store).Check(
		t.Context(), persona.Adjudicator(), testTurnID, testTurnEntityID)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resumption.Decision != persona.DecisionSkip {
		t.Fatalf("the check says %q for a stage whose verdict is already on the turn; the phase says the "+
			"stage was ENTERED and only the artifact can say it finished", resumption.Decision)
	}
	if resumption.Ref != refFor(t, vocabulary.TurnVerdictRef) {
		t.Fatalf("the skip carries reference %+v, want the one the turn records", resumption.Ref)
	}
	if resumption.Role != persona.RoleAdjudicator {
		t.Fatalf("the answer names role %q", resumption.Role)
	}
}

func TestGuard_RunsAStageThatHasProducedNothing(t *testing.T) {
	store := newFakeGraph(&journal{})
	store.seedTurn(fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseAdjudicating)))

	resumption, err := newGuard(t, store).Check(
		t.Context(), persona.Adjudicator(), testTurnID, testTurnEntityID)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resumption.Decision != persona.DecisionRun {
		t.Fatalf("the check says %q for a stage with no verdict on the turn", resumption.Decision)
	}
	if !resumption.Ref.IsZero() {
		t.Fatalf("a run decision carries reference %+v", resumption.Ref)
	}
}

// The check is PER STAGE, and this is the case that would make it useless if it
// were not: a turn mid-narration carries a verdict, and reading "an artifact is
// present" without asking which one would skip the narrator on the strength of
// the adjudicator's work.
func TestGuard_ReadsEachPersonasOwnArtifactAndNotAnyArtifact(t *testing.T) {
	store := newFakeGraph(&journal{})
	store.seedTurn(
		fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseNarrating)),
		fact(vocabulary.TurnVerdictRef, refFor(t, vocabulary.TurnVerdictRef).String()),
		fact(vocabulary.TurnRollRef, refFor(t, vocabulary.TurnRollRef).String()),
		fact(vocabulary.TurnEffectsRef, refFor(t, vocabulary.TurnEffectsRef).String()),
	)
	guard := newGuard(t, store)

	narrator, err := guard.Check(t.Context(), persona.Narrator(), testTurnID, testTurnEntityID)
	if err != nil {
		t.Fatalf("Check(narrator): %v", err)
	}
	if narrator.Decision != persona.DecisionRun {
		t.Fatalf("the narrator was skipped on a turn carrying every artifact but its own")
	}

	adjudicator, err := guard.Check(t.Context(), persona.Adjudicator(), testTurnID, testTurnEntityID)
	if err != nil {
		t.Fatalf("Check(adjudicator): %v", err)
	}
	if adjudicator.Decision != persona.DecisionSkip {
		t.Fatalf("the adjudicator was re-run on a turn that already records its verdict")
	}
}

// Every anomaly is an error rather than a shrug, because this answer decides
// whether a model call is made.
func TestGuard_RefusesEveryShapeItCannotReadHonestly(t *testing.T) {
	cases := map[string]struct {
		triples []message.Triple
		want    string
	}{
		"two references for one single-valued predicate": {
			triples: []message.Triple{
				fact(vocabulary.TurnVerdictRef, refFor(t, vocabulary.TurnVerdictRef).String()),
				fact(vocabulary.TurnVerdictRef, "obj://OTHER/turn/turn-act-1/verdict"),
			},
			want: "appending lane",
		},
		"a sentence where a pointer belongs": {
			triples: []message.Triple{
				fact(vocabulary.TurnVerdictRef, "the adjudicator decided it was plausible"),
			},
			want: "not a resolvable storage reference",
		},
		"a number where a pointer belongs": {
			triples: []message.Triple{fact(vocabulary.TurnVerdictRef, 7)},
			want:    "want a storage reference",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeGraph(&journal{})
			// Two identical predicates cannot be seeded through the replacing
			// merge lane, so they are placed directly — which is exactly what an
			// appending write would have left behind.
			store.seedTurn(testCase.triples...)

			_, err := newGuard(t, store).Check(t.Context(), persona.Adjudicator(), testTurnID, testTurnEntityID)
			if err == nil {
				t.Fatal("the check produced an answer it could not honestly give")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("the refusal is %q, want it to mention %q", err, testCase.want)
			}
		})
	}
}

// A referential stub is queryable and factless, so "no artifact recorded" read
// off one is a false negative — and this false negative buys a second billed
// model call every single attempt.
func TestGuard_RefusesAReferentialStub(t *testing.T) {
	store := newFakeGraph(&journal{})
	store.entities[testTurnEntityID] = &graph.EntityState{
		ID:          testTurnEntityID,
		MessageType: graph.StubMessageType,
	}

	_, err := newGuard(t, store).Check(t.Context(), persona.Adjudicator(), testTurnID, testTurnEntityID)
	if err == nil {
		t.Fatal("a referential stub was read as a stage that has produced nothing")
	}
	if !strings.Contains(err.Error(), "stub") {
		t.Fatalf("the refusal is %q", err)
	}
}

// turn_id and the turn entity ID are one fact wearing two shapes, and this is a
// seam where they arrive as two arguments: a check made against turn A's entity
// for turn B would skip a stage that never ran.
func TestGuard_RefusesAMismatchedTurnAndEntity(t *testing.T) {
	store := newFakeGraph(&journal{})
	store.seedTurn(fact(vocabulary.TurnPhaseCurrent, string(vocabulary.PhaseAdjudicating)))

	_, err := newGuard(t, store).Check(
		t.Context(), persona.Adjudicator(), "turn-act-9", testTurnEntityID)
	if err == nil {
		t.Fatal("a check for one turn was answered from another turn's entity")
	}
}

func TestGuard_SurfacesAReadFailureRatherThanGuessing(t *testing.T) {
	store := newFakeGraph(&journal{})
	store.getErr = errors.New("graph-ingest unreachable")

	_, err := newGuard(t, store).Check(t.Context(), persona.Adjudicator(), testTurnID, testTurnEntityID)
	if err == nil {
		t.Fatal("an unreadable turn was reported as a stage that has produced nothing, which would spawn a " +
			"persona for a turn nobody can read")
	}
}

func TestNewGuard_RequiresAReadSurface(t *testing.T) {
	if _, err := persona.NewGuard(nil); err == nil {
		t.Fatal("a resume check was built with nothing to read")
	}
}
