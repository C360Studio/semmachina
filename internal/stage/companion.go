package stage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/companion"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/epistemic"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const companionStageSource = "stage-companion"

// CompanionGraph is the deterministic stage's authoritative graph surface.
type CompanionGraph interface {
	GetEntity(context.Context, string) (*graph.EntityState, error)
	GetEntities(context.Context, []string) (graphio.BatchResult, error)
	EntitiesByPredicateValue(context.Context, string, string, int) ([]string, error)
	MergeTriples(context.Context, string, []message.Triple, ...graphio.MergeOption) (*graph.EntityState, error)
}

// CompanionArtifacts is the exact resident decision/stage-record store.
type CompanionArtifacts interface {
	InstanceName() string
	PutCompanionDecision(context.Context, string, *payload.CompanionDecision) (content.Ref, error)
	GetCompanionDecision(context.Context, content.Ref) (*payload.CompanionDecision, error)
	PutCompanionStageRecord(context.Context, string, *payload.CompanionStageRecord) (content.Ref, error)
	GetCompanionStageRecord(context.Context, content.Ref) (*payload.CompanionStageRecord, error)
}

type companionPrompter interface {
	Companion(context.Context, *epistemic.Projection) (persona.TaskRequest, error)
}

// CompanionStage performs deterministic trigger and explicit-hint work. Only a
// selected automatic warning publishes a model task.
type CompanionStage struct {
	recorder  PhaseRecorder
	graph     CompanionGraph
	artifacts CompanionArtifacts
	authority *companion.Authority
	projector ContextProjector
	prompter  companionPrompter
	tasks     TaskPublisher
	now       func() time.Time
}

// NewCompanionStage builds the applying-to-narrating structural barrier.
func NewCompanionStage(
	recorder PhaseRecorder,
	graphStore CompanionGraph,
	artifacts CompanionArtifacts,
	authority *companion.Authority,
	projector ContextProjector,
	prompter companionPrompter,
	tasks TaskPublisher,
) (*CompanionStage, error) {
	if recorder == nil || graphStore == nil || artifacts == nil || authority == nil || projector == nil || prompter == nil || tasks == nil {
		return nil, errors.New("companion stage requires recorder, graph, artifacts, authority, projector, prompter, and task publisher")
	}
	return &CompanionStage{recorder: recorder, graph: graphStore, artifacts: artifacts,
		authority: authority, projector: projector, prompter: prompter, tasks: tasks, now: time.Now}, nil
}

// Phase implements Stage.
func (*CompanionStage) Phase() vocabulary.TurnPhase { return vocabulary.PhaseCompanion }

// Run converges exact no-op, deterministic hint, and warning-spawn paths.
func (s *CompanionStage) Run(ctx context.Context, trigger Trigger) error {
	run, err := enter(ctx, s.recorder, trigger, s.Phase())
	if err != nil || !run {
		return err
	}
	state, err := s.graph.GetEntity(ctx, trigger.TurnEntityID)
	if err != nil {
		return fmt.Errorf("read companion turn: %w", err)
	}
	if stageRef, present, err := soleCompanionRef(state, vocabulary.TurnCompanionStageRef); err != nil {
		return err
	} else if present {
		record, readErr := s.artifacts.GetCompanionStageRecord(ctx, stageRef)
		if readErr != nil {
			return readErr
		}
		if record.TurnID != trigger.TurnID {
			return errors.New("resident companion stage record belongs to another turn")
		}
		return nil
	}
	playerID, _, err := optionalSoleTurnString(state, vocabulary.TurnActionPlayer)
	if err != nil || playerID == "" {
		return fmt.Errorf("read companion player identity: %w", err)
	}
	if resident, found, err := s.residentStage(ctx, trigger.TurnID); err != nil {
		return err
	} else if found {
		if resident.PlayerID != playerID {
			return errors.New("resident companion stage record names another player")
		}
		var decisionRef content.Ref
		if resident.DecisionRef != "" {
			decisionRef, err = content.ParseRef(resident.DecisionRef)
			if err != nil {
				return err
			}
		}
		return s.commitRecord(ctx, trigger, resident, decisionRef)
	}
	bond, err := s.authority.ActiveBondForPlayer(ctx, playerID)
	if err != nil {
		return err
	}
	if bond == nil {
		return s.commitRecord(ctx, trigger, &payload.CompanionStageRecord{
			TurnID: trigger.TurnID, PlayerID: playerID, Status: payload.CompanionStageNoActiveBond,
		}, content.Ref{})
	}
	return s.authority.WithBondTransaction(ctx, bond.ID, playerID, bond.CharacterID,
		func(transaction *companion.LadderTransaction) error {
			fresh := transaction.Bond()
			return s.runBondTransaction(ctx, trigger, state, &fresh, transaction)
		})
}

func (s *CompanionStage) runBondTransaction(
	ctx context.Context, trigger Trigger, state *graph.EntityState, bond *companion.Bond,
	transaction *companion.LadderTransaction,
) error {
	selected, err := companion.SelectTrigger(state, bond)
	if err != nil {
		return err
	}
	if err := s.persistTrigger(ctx, trigger.TurnEntityID, selected); err != nil {
		return err
	}
	if selected.Kind == vocabulary.CompanionTriggerNone {
		return s.commitRecord(ctx, trigger, stageRecord(trigger.TurnID, bond, selected,
			payload.CompanionStageNoTrigger, ""), content.Ref{})
	}
	contextRef, _, err := optionalSoleTurnString(state, vocabulary.TurnActionScene)
	if err != nil || contextRef == "" {
		return fmt.Errorf("read companion context: %w", err)
	}
	audience := epistemic.CompanionAudience(trigger.TurnID, trigger.TurnEntityID,
		contextRef, bond.CharacterID, bond.ID)
	projection, err := s.projector.Project(ctx, audience)
	if err != nil {
		return err
	}
	if selected.Kind == vocabulary.CompanionTriggerWarning {
		if resident, ref, found, err := s.residentDecision(ctx, trigger.TurnID); err != nil {
			return err
		} else if found {
			if resident.TurnID != trigger.TurnID || resident.PlayerID != bond.PlayerID ||
				resident.CompanionID != bond.CharacterID || resident.ContextRef != projection.ContextRef ||
				resident.Kind == payload.CompanionDecisionHint {
				return errors.New("resident warning decision diverges from the authorized turn and bond")
			}
			record := stageRecord(trigger.TurnID, bond, selected, payload.CompanionStageDecision, ref.String())
			return s.commitRecord(ctx, trigger, record, ref)
		}
		request, err := s.prompter.Companion(ctx, projection)
		if err != nil {
			return err
		}
		task, err := persona.Companion().Task(request)
		if err != nil {
			return err
		}
		return s.publish(ctx, task)
	}
	return s.commitHint(ctx, trigger, bond, projection, selected, transaction)
}

func (s *CompanionStage) commitHint(
	ctx context.Context, trigger Trigger, bond *companion.Bond,
	projection *epistemic.Projection, selected companion.Trigger,
	transaction *companion.LadderTransaction,
) error {
	if resident, ref, found, err := s.residentDecision(ctx, trigger.TurnID); err != nil {
		return err
	} else if found {
		if resident.TurnID != trigger.TurnID || resident.PlayerID != bond.PlayerID ||
			resident.CompanionID != bond.CharacterID || resident.ContextRef != projection.ContextRef {
			return errors.New("resident companion decision diverges from the authorized turn and bond")
		}
		if resident.Kind == payload.CompanionDecisionHint {
			if err := advanceResidentHint(ctx, transaction, resident.HintLevel); err != nil {
				return err
			}
		}
		record := stageRecord(trigger.TurnID, bond, selected, payload.CompanionStageDecision, ref.String())
		return s.commitRecord(ctx, trigger, record, ref)
	}
	ids, err := s.graph.EntitiesByPredicateValue(ctx, vocabulary.KnowledgeActorHolder.String(), bond.CharacterID, 256)
	if err != nil {
		return err
	}
	batch, err := s.graph.GetEntities(ctx, ids)
	if err != nil {
		return err
	}
	if len(batch.Missing) != 0 {
		return fmt.Errorf("companion knowledge hydration is short by %d records", len(batch.Missing))
	}
	level := bond.HintLevel
	refs, err := companion.SelectHintEvidence(projection, bond.CharacterID, batch.Entities, level)
	if err != nil {
		return err
	}
	need, _ := companion.HintEvidenceCount(level)
	if len(refs) < need {
		decision := companionDecision(trigger.TurnID, projection.ContextRef, bond,
			payload.CompanionDecisionSilent, "", nil)
		return s.commitDecision(ctx, trigger, bond, selected, decision, false, transaction)
	}
	decision := companionDecision(trigger.TurnID, projection.ContextRef, bond,
		payload.CompanionDecisionHint, level, refs)
	return s.commitDecision(ctx, trigger, bond, selected, decision, true, transaction)
}

func advanceResidentHint(
	ctx context.Context, transaction *companion.LadderTransaction, emitted vocabulary.HintLevel,
) error {
	bond := transaction.Bond()
	_, next, err := companion.NextHintLevel(emitted)
	if err != nil {
		return err
	}
	if bond.HintLevel == next {
		return nil
	}
	_, err = transaction.AdvanceHint(ctx, emitted)
	return err
}

func (s *CompanionStage) residentStage(ctx context.Context, turnID string) (*payload.CompanionStageRecord, bool, error) {
	ref, err := s.directRef(vocabulary.TurnCompanionStageRef, turnID)
	if err != nil {
		return nil, false, err
	}
	record, err := s.artifacts.GetCompanionStageRecord(ctx, ref)
	if errors.Is(err, content.ErrArtifactNotFound) {
		return nil, false, nil
	}
	return record, err == nil, err
}

func (s *CompanionStage) residentDecision(ctx context.Context, turnID string) (*payload.CompanionDecision, content.Ref, bool, error) {
	ref, err := s.directRef(vocabulary.TurnCompanionDecisionRef, turnID)
	if err != nil {
		return nil, content.Ref{}, false, err
	}
	decision, err := s.artifacts.GetCompanionDecision(ctx, ref)
	if errors.Is(err, content.ErrArtifactNotFound) {
		return nil, ref, false, nil
	}
	return decision, ref, err == nil, err
}

func (s *CompanionStage) directRef(predicate vocabulary.Predicate, turnID string) (content.Ref, error) {
	key, err := content.KeyFor(predicate, content.SubjectTurn, turnID)
	if err != nil {
		return content.Ref{}, err
	}
	ref := content.Ref{Instance: s.artifacts.InstanceName(), Key: key}
	return ref, ref.Validate()
}

func companionDecision(
	turnID, contextRef string, bond *companion.Bond, kind payload.CompanionDecisionKind,
	level vocabulary.HintLevel, refs []string,
) *payload.CompanionDecision {
	decision := &payload.CompanionDecision{TurnID: turnID, ContextRef: contextRef,
		PlayerID: bond.PlayerID, CompanionID: bond.CharacterID, Kind: kind,
		HintLevel: level, EvidenceRefs: refs}
	decision.DecisionID = payload.CompanionDecisionID(turnID, contextRef, bond.PlayerID, bond.CharacterID)
	return decision
}

func (s *CompanionStage) commitDecision(
	ctx context.Context, trigger Trigger, bond *companion.Bond, selected companion.Trigger,
	decision *payload.CompanionDecision, advance bool, transaction *companion.LadderTransaction,
) error {
	decisionRef, err := s.artifacts.PutCompanionDecision(ctx, trigger.TurnEntityID, decision)
	if err != nil {
		return err
	}
	if advance {
		if _, err := transaction.AdvanceHint(ctx, decision.HintLevel); err != nil {
			return err
		}
	}
	record := stageRecord(trigger.TurnID, bond, selected, payload.CompanionStageDecision, decisionRef.String())
	return s.commitRecord(ctx, trigger, record, decisionRef)
}

func stageRecord(
	turnID string, bond *companion.Bond, selected companion.Trigger,
	status payload.CompanionStageStatus, decisionRef string,
) *payload.CompanionStageRecord {
	return &payload.CompanionStageRecord{TurnID: turnID, PlayerID: bond.PlayerID,
		CompanionID: bond.CharacterID, BondID: bond.ID, Status: status,
		TriggerKind: selected.Kind, TriggerSource: selected.Source, DecisionRef: decisionRef}
}

func (s *CompanionStage) commitRecord(
	ctx context.Context, trigger Trigger, record *payload.CompanionStageRecord, decisionRef content.Ref,
) error {
	stageRef, err := s.artifacts.PutCompanionStageRecord(ctx, trigger.TurnEntityID, record)
	if err != nil {
		return err
	}
	triples, err := record.Triples(trigger.TurnEntityID, stageRef.String(), companionStageSource, s.now())
	if err != nil {
		return err
	}
	if !decisionRef.IsZero() {
		triples = append(triples, message.Triple{Subject: trigger.TurnEntityID,
			Predicate: vocabulary.TurnCompanionDecisionRef.String(), Object: decisionRef.String(),
			Source: companionStageSource, Timestamp: s.now().UTC(), Confidence: 1, Context: trigger.TurnEntityID})
	}
	_, err = s.graph.MergeTriples(ctx, trigger.TurnEntityID, triples)
	return err
}

func (s *CompanionStage) persistTrigger(ctx context.Context, turnEntityID string, trigger companion.Trigger) error {
	at := s.now().UTC()
	_, err := s.graph.MergeTriples(ctx, turnEntityID, []message.Triple{
		{Subject: turnEntityID, Predicate: vocabulary.TurnCompanionTriggerKind.String(), Object: string(trigger.Kind), Source: companionStageSource, Timestamp: at, Confidence: 1, Context: turnEntityID},
		{Subject: turnEntityID, Predicate: vocabulary.TurnCompanionTriggerSource.String(), Object: string(trigger.Source), Source: companionStageSource, Timestamp: at, Confidence: 1, Context: turnEntityID},
	})
	return err
}

func (s *CompanionStage) publish(ctx context.Context, task agentic.TaskMessage) error {
	envelope := message.NewBaseMessage(task.Schema(), &task, TaskSource)
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return s.tasks.PublishToStreamWithMsgID(ctx, TaskSubjectFor(persona.RoleCompanion), data, task.TaskID)
}

func soleCompanionRef(state *graph.EntityState, predicate vocabulary.Predicate) (content.Ref, bool, error) {
	value, present, err := optionalSoleTurnString(state, predicate)
	if err != nil || !present {
		return content.Ref{}, present, err
	}
	ref, err := content.ParseRef(value)
	return ref, true, err
}

func optionalSoleTurnString(state *graph.EntityState, predicate vocabulary.Predicate) (string, bool, error) {
	var value string
	count := 0
	for _, triple := range state.Triples {
		if triple.Predicate == predicate.String() {
			text, ok := triple.Object.(string)
			if !ok || text == "" {
				return "", false, fmt.Errorf("turn predicate %s is not a string", predicate)
			}
			value, count = text, count+1
		}
	}
	if count > 1 {
		return "", false, fmt.Errorf("turn carries %d values for %s", count, predicate)
	}
	return value, count == 1, nil
}
