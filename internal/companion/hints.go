package companion

import (
	"errors"
	"fmt"
	"slices"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// NextHintLevel returns the level emitted now and the level persisted for the
// next successful request. The terminal level saturates.
func NextHintLevel(current vocabulary.HintLevel) (vocabulary.HintLevel, vocabulary.HintLevel, error) {
	if _, err := vocabulary.ParseHintLevel(string(current)); err != nil {
		return "", "", err
	}
	switch current {
	case vocabulary.HintLevelNudge:
		return current, vocabulary.HintLevelConnect, nil
	case vocabulary.HintLevelConnect:
		return current, vocabulary.HintLevelNextStep, nil
	case vocabulary.HintLevelNextStep:
		return current, vocabulary.HintLevelNextStep, nil
	default:
		panic("closed hint level reached an unhandled branch")
	}
}

// HintEvidenceCount is the fixed disclosure bound for one ladder level.
func HintEvidenceCount(level vocabulary.HintLevel) (int, error) {
	switch level {
	case vocabulary.HintLevelNudge:
		return 1, nil
	case vocabulary.HintLevelConnect:
		return 2, nil
	case vocabulary.HintLevelNextStep:
		return 3, nil
	default:
		_, err := vocabulary.ParseHintLevel(string(level))
		return 0, err
	}
}

// SelectHintEvidence extracts canonical evidence targets from exact Knowledge
// records held by the companion, intersects them with the already-authorized
// companion projection, and returns the level-bounded prefix. A short result is
// deliberate: the caller commits a deterministic no-hint stage result and does
// not advance the ladder.
func SelectHintEvidence(
	projection *epistemic.Projection,
	companionID string,
	records []graph.EntityState,
	level vocabulary.HintLevel,
) ([]string, error) {
	count, err := HintEvidenceCount(level)
	if err != nil {
		return nil, err
	}
	if projection == nil || projection.Purpose != epistemic.PurposeCompanion {
		return nil, errors.New("hint selection requires a companion projection")
	}
	if projection.HasSolution {
		return nil, errors.New("hint selection refuses a solution-bearing projection")
	}
	if projection.CompanionID != "" && projection.CompanionID != companionID {
		return nil, errors.New("hint selection companion does not match projection identity")
	}
	parsed, err := types.ParseEntityID(companionID)
	if err != nil || parsed.Type != string(vocabulary.EntityKindCharacter) {
		return nil, fmt.Errorf("hint companion %q is not a canonical character", companionID)
	}
	authorized := make(map[string]bool)
	for _, entity := range projection.Entities() {
		authorized[entity.ID] = true
	}
	candidates := make([]string, 0, len(records))
	for index := range records {
		record := &records[index]
		recordID, err := types.ParseEntityID(record.ID)
		if err != nil || recordID.Type != string(vocabulary.EntityKindKnowledge) {
			return nil, fmt.Errorf("knowledge record id %q is not canonical", record.ID)
		}
		worldType := (&payload.WorldEntity{}).Schema()
		runtimeType := message.Type{Domain: payload.Domain, Category: "knowledge_grant_entity", Version: payload.SchemaVersion}
		if record.MessageType != worldType && record.MessageType != runtimeType {
			return nil, fmt.Errorf("knowledge record %s has envelope %s", record.ID, record.MessageType)
		}
		if record.Version != 1 {
			return nil, fmt.Errorf("knowledge record %s has version %d, want 1", record.ID, record.Version)
		}
		kind, err := soleRecordString(record, vocabulary.WorldEntityKind)
		if err != nil {
			return nil, fmt.Errorf("knowledge record %s: %w", record.ID, err)
		}
		if kind != string(vocabulary.EntityKindKnowledge) {
			return nil, fmt.Errorf("knowledge record %s declares kind %q", record.ID, kind)
		}
		holder, err := soleRecordString(record, vocabulary.KnowledgeActorHolder)
		if err != nil {
			return nil, fmt.Errorf("knowledge record %s: %w", record.ID, err)
		}
		if holder != companionID {
			continue
		}
		evidence, err := soleRecordString(record, vocabulary.KnowledgeEvidenceRef)
		if err != nil {
			return nil, fmt.Errorf("knowledge record %s: %w", record.ID, err)
		}
		id, err := types.ParseEntityID(evidence)
		if err != nil || id.Type != string(vocabulary.EntityKindEvidence) {
			return nil, fmt.Errorf("knowledge record %s evidence %q is not canonical evidence", record.ID, evidence)
		}
		if authorized[evidence] {
			candidates = append(candidates, evidence)
		}
	}
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	if len(candidates) < count {
		return candidates, nil
	}
	return slices.Clone(candidates[:count]), nil
}

func soleRecordString(state *graph.EntityState, predicate vocabulary.Predicate) (string, error) {
	var values []string
	for _, triple := range state.Triples {
		if triple.Predicate != predicate.String() {
			continue
		}
		value, ok := triple.Object.(string)
		if !ok || value == "" {
			return "", fmt.Errorf("%s is not a non-empty string", predicate)
		}
		values = append(values, value)
	}
	if len(values) != 1 {
		return "", fmt.Errorf("carries %d values for %s, want exactly one", len(values), predicate)
	}
	return values[0], nil
}
