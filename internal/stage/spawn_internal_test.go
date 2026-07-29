package stage

import (
	"testing"

	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The resume guard and the stranded-turn pass ask the same question from two
// places, and they must not answer it from two different facts.
//
// The guard reads persona.Spec.Artifact to decide whether to pay for a persona
// again; the boot pass reads vocabulary.StageWitness to decide whether a parked
// turn's stage finished. If those disagree for one role, the boot pass would
// re-trigger a stage the guard then skips — a turn that can never leave its
// phase and burns an attempt on every boot until it is abandoned.
//
// This is the only place both statements are in scope, because spawnPhases is
// what pairs a role with a phase.
func TestStageWitness_AgreesWithEveryPersonaSpec(t *testing.T) {
	if len(spawnPhases) == 0 {
		t.Fatal("no persona is paired with a phase; the agreement check would be vacuous")
	}
	for role, phase := range spawnPhases {
		spec, err := persona.SpecFor(role)
		if err != nil {
			t.Fatalf("SpecFor(%s): %v", role, err)
		}
		witness, ok := vocabulary.StageWitness(phase)
		if !ok {
			t.Fatalf("phase %s runs the %s persona but the vocabulary names no completion witness for it",
				phase, role)
		}
		if witness != spec.Artifact {
			t.Errorf("the %s stage enters %s: the resume guard looks for %s and the boot pass looks for %s. "+
				"A turn parked there would be re-triggered by one and skipped by the other, forever",
				role, phase, spec.Artifact, witness)
		}
	}
}
