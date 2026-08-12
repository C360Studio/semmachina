package rulepack_test

import (
	"encoding/json"
	"testing"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/processor/rule"

	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestCaseProgressUniversalBarrierIsRoutedAfterDecisionAndKnowledge(t *testing.T) {
	declare(t)
	raw, err := rulepack.ProcessorConfig()
	if err != nil {
		t.Fatal(err)
	}
	var cfg rule.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	portFound, triggerFound, barrierFound := false, false, false
	for _, port := range cfg.Ports.Outputs {
		stream, ok := port.Config.(component.JetStreamPort)
		portFound = portFound || ok && stream.StreamName == rulepack.StageStream &&
			len(stream.Subjects) == 1 && stream.Subjects[0] == rulepack.SubjectCaseProgress
	}
	for _, definition := range cfg.InlineRules {
		switch definition.ID {
		case "turn-knowledge-committed-records-case-progress":
			triggerFound = true
			fields := map[string]bool{}
			for _, condition := range definition.Conditions {
				fields[condition.Field] = true
			}
			if !fields[vocabulary.TurnCaseDecisionRef.String()] || !fields[vocabulary.TurnKnowledgeRef.String()] {
				t.Fatalf("case progress trigger lacks committed artifact gates: %#v", definition.Conditions)
			}
		case "turn-committed-effects-enter-companion":
			for _, condition := range definition.Conditions {
				barrierFound = barrierFound || condition.Field == vocabulary.TurnCaseProgressRef.String()
			}
		}
	}
	if !portFound || !triggerFound || !barrierFound {
		t.Fatalf("case progress wiring missing: port=%v trigger=%v barrier=%v", portFound, triggerFound, barrierFound)
	}
}

func TestSubjectCaseProgressIsAuxiliaryRatherThanAStagePhase(t *testing.T) {
	if phase, ok := rulepack.PhaseForSubject(rulepack.SubjectCaseProgress); ok {
		t.Fatalf("case progress subject maps to stage phase %q", phase)
	}
}
