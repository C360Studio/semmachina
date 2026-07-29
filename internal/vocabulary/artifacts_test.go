package vocabulary_test

import (
	"slices"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The phases a stage owns, and therefore the phases a parked turn can be
// stranded in with an artifact to look for.
var stageOwnedPhases = []vocabulary.TurnPhase{
	vocabulary.PhaseAdjudicating,
	vocabulary.PhaseResolving,
	vocabulary.PhaseApplying,
	vocabulary.PhaseNarrating,
}

func TestStageArtifacts_CoverEveryPhaseAStageOwns(t *testing.T) {
	for _, phase := range stageOwnedPhases {
		artifacts, known := vocabulary.StageArtifacts(phase)
		if !known {
			t.Fatalf("phase %s is not in the artifact table at all", phase)
		}
		if len(artifacts) == 0 {
			t.Fatalf("phase %s owns a stage but declares no artifact; a rule gated on it could then only be "+
				"gated on the phase, which fires as the stage STARTS", phase)
		}
	}
}

// `accepted` and the two terminal phases are KNOWN with no artifacts, and the
// distinction from an unknown phase is the whole reason the bool exists: a
// caller must be able to tell "no stage owes this turn anything" from "that is
// not a phase".
func TestStageArtifacts_DistinguishNoStageFromNoPhase(t *testing.T) {
	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseAccepted, vocabulary.PhaseComplete, vocabulary.PhaseFailed,
	} {
		artifacts, known := vocabulary.StageArtifacts(phase)
		if !known {
			t.Errorf("phase %s reports as unknown; it is a phase, it just owns no stage", phase)
		}
		if len(artifacts) != 0 {
			t.Errorf("phase %s declares artifacts %v; nothing is pending in it", phase, artifacts)
		}
	}
	if artifacts, known := vocabulary.StageArtifacts(vocabulary.TurnPhase("adjudicated")); known {
		t.Errorf("a misspelled phase reported as known with artifacts %v; a typo would read as a turn owing "+
			"nothing rather than as a mistake", artifacts)
	}
}

// The artifact sets must not overlap. A predicate that witnessed two phases
// would make "did the stage that owns THIS phase finish" unanswerable, and would
// let a mid-chain rule pass the artifact gate by naming the previous hop's
// output instead of its own.
func TestStageArtifacts_AreDisjointAcrossPhases(t *testing.T) {
	owner := map[vocabulary.Predicate]vocabulary.TurnPhase{}
	for _, phase := range stageOwnedPhases {
		artifacts, _ := vocabulary.StageArtifacts(phase)
		for _, artifact := range artifacts {
			if previous, seen := owner[artifact]; seen {
				t.Errorf("%s witnesses both %s and %s", artifact, previous, phase)
			}
			owner[artifact] = phase
		}
	}
}

// The point of the whole table (F21): an artifact is written when a stage is
// DONE. A predicate the turn carries from birth — the player, the scene, the
// action reference — is present the entire time, so a rule conjoining one with a
// phase looks gated and races exactly as a phase-only rule does.
func TestStageArtifacts_ExcludeEveryBirthRecordPredicate(t *testing.T) {
	birth := vocabulary.TurnAcceptedPredicates()
	birth = append(birth, vocabulary.TurnActionRef)

	for _, phase := range stageOwnedPhases {
		artifacts, _ := vocabulary.StageArtifacts(phase)
		for _, artifact := range artifacts {
			if slices.Contains(birth, artifact) {
				t.Errorf("%s is listed as an artifact of %s, but it is written by the turn's atomic create and "+
					"is present from the moment the turn exists", artifact, phase)
			}
		}
	}
}

func TestStageArtifacts_AreAllDeclaredPredicates(t *testing.T) {
	declared := vocabulary.AllPredicates()
	for _, phase := range vocabulary.TurnPhases() {
		artifacts, _ := vocabulary.StageArtifacts(phase)
		for _, artifact := range artifacts {
			if !slices.Contains(declared, artifact) {
				t.Errorf("%s is an artifact of %s but is not a predicate this engine writes", artifact, phase)
			}
		}
	}
}

// The returned slice is the caller's. A shared backing array would let one
// reader's filtering corrupt the table for every other.
func TestStageArtifacts_ReturnACopy(t *testing.T) {
	first, _ := vocabulary.StageArtifacts(vocabulary.PhaseAdjudicating)
	if len(first) == 0 {
		t.Fatal("adjudicating declares no artifacts; the mutation check would be vacuous")
	}
	first[0] = vocabulary.Predicate("turn.tampered.value")

	second, _ := vocabulary.StageArtifacts(vocabulary.PhaseAdjudicating)
	if second[0] == first[0] {
		t.Fatal("mutating a returned artifact list changed the table")
	}
}

// The witness must be one of the phase's own artifacts, or "the stage finished"
// and "the stage's artifacts are present" would be answered from two different
// facts.
func TestStageWitness_IsOneOfItsPhasesArtifacts(t *testing.T) {
	for _, phase := range stageOwnedPhases {
		witness, ok := vocabulary.StageWitness(phase)
		if !ok {
			t.Fatalf("phase %s owns a stage but has no completion witness", phase)
		}
		artifacts, _ := vocabulary.StageArtifacts(phase)
		if !slices.Contains(artifacts, witness) {
			t.Errorf("the witness for %s is %s, which is not among its artifacts %v", phase, witness, artifacts)
		}
	}
}

func TestStageWitness_RefusesAPhaseNoStageOwns(t *testing.T) {
	for _, phase := range []vocabulary.TurnPhase{
		vocabulary.PhaseAccepted, vocabulary.PhaseComplete, vocabulary.PhaseFailed,
		vocabulary.TurnPhase("nonsense"),
	} {
		if witness, ok := vocabulary.StageWitness(phase); ok {
			t.Errorf("phase %s reported witness %s; no stage owes it an artifact", phase, witness)
		}
	}
}
