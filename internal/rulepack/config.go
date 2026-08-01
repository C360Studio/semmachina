package rulepack

import (
	"encoding/json"
	"fmt"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/processor/rule"
)

// EntityStatesBucket is the only bucket the rule engine's typed entity watcher
// accepts, and the only one this pack needs: the turn's phase is a graph fact.
const EntityStatesBucket = "ENTITY_STATES"

// ProcessorConfig returns the rule-processor configuration that runs the
// turn-sequencing pack.
//
// Three things it settles that a hand-written flow config would get wrong:
//
//   - The pack rides as INLINE rules rather than as file paths. The rules are
//     embedded in the binary for the same reason the starter world is: a
//     deployment cannot boot with half a turn loop because somebody forgot to
//     copy a directory, and a missing rules file would present as a game that
//     accepts actions and never adjudicates them.
//   - Every stage subject is declared as a JETSTREAM output port. The rule
//     engine decides publish-vs-publish-to-stream by exact subject match against
//     this list, and a subject missing from it is published to core NATS —
//     silently, successfully, and into nothing, because the stage runners are
//     bound to durable consumers on a stream.
//   - The entity watch pattern is the turn pattern, so the watcher does not
//     decode every world entity on every write to find the turns.
func ProcessorConfig() (json.RawMessage, error) {
	definitions, err := Definitions()
	if err != nil {
		return nil, err
	}
	caseDefinitions, err := CaseLifecycleDefinitions()
	if err != nil {
		return nil, err
	}
	definitions = append(definitions, caseDefinitions...)
	outputs := make([]component.PortDefinition, 0, len(stagePhases)+2)
	for _, phase := range stagePhases {
		subject, subjectErr := SubjectForPhase(phase)
		if subjectErr != nil {
			return nil, subjectErr
		}
		outputs = append(outputs, stagePort(string(phase), subject))
	}
	outputs = append(outputs, stagePort("resolved", SubjectResolved))
	outputs = append(outputs, stagePort("knowledge", SubjectKnowledge))
	outputs = append(outputs, stagePort("accusation", SubjectAccusation))

	config := rule.Config{
		PackID:      PackID,
		InlineRules: definitions,
		EntityWatchBuckets: map[string][]string{
			EntityStatesBucket: {EntityPattern, CaseEntityPattern},
		},
		Ports: &component.PortConfig{Outputs: outputs},
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("turn-sequencing rule config: %w", err)
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode the turn-sequencing rule config: %w", err)
	}
	return raw, nil
}

func stagePort(name, subject string) component.PortDefinition {
	return component.PortDefinition{
		Name:        "semmachina.turn." + name,
		Type:        "jetstream",
		Subject:     subject,
		StreamName:  StageStream,
		Description: "Stage trigger for the turn loop's " + name + " hop",
	}
}
