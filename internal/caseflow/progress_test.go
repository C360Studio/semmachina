package caseflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/caseflow"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	progressTurnID     = "turn-act-1"
	progressTurnEntity = "acme.semmachina.bellweather.campaign.turn.turn-act-1"
	progressCaseID     = "acme.semmachina.bellweather.campaign.case.bellweather-case"
	progressActorID    = "acme.semmachina.bellweather.campaign.character.rowan"
	progressVictimID   = "acme.semmachina.bellweather.campaign.character.harold"
)

type progressGraph struct {
	turn, caseState *graph.EntityState
	writes          [][]message.Triple
	readErr         error
	mergeErr        error
}

func (g *progressGraph) GetEntity(_ context.Context, id string) (*graph.EntityState, error) {
	if g.readErr != nil {
		return nil, g.readErr
	}
	if id == progressTurnEntity {
		return g.turn, nil
	}
	return g.caseState, nil
}

func (g *progressGraph) MergeTriples(_ context.Context, _ string, triples []message.Triple,
	_ ...graphio.MergeOption) (*graph.EntityState, error) {
	if g.mergeErr != nil {
		return nil, g.mergeErr
	}
	g.writes = append(g.writes, triples)
	g.turn.Triples = append(g.turn.Triples, triples...)
	return g.turn, nil
}

type progressArtifacts struct {
	decision                          *payload.CaseDecisionRecord
	knowledge                         *content.KnowledgeReceipt
	progress                          *content.CaseProgressRecord
	puts                              int
	decisionErr, knowledgeErr, putErr error
}

func (a *progressArtifacts) GetCaseDecisionRecord(context.Context, content.Ref) (*payload.CaseDecisionRecord, error) {
	return a.decision, a.decisionErr
}
func (a *progressArtifacts) GetKnowledgeReceipt(context.Context, content.Ref) (*content.KnowledgeReceipt, error) {
	return a.knowledge, a.knowledgeErr
}
func (a *progressArtifacts) PutCaseProgressRecord(_ context.Context, _ string,
	record *content.CaseProgressRecord) (content.Ref, error) {
	a.puts++
	a.progress = record
	return content.Ref{Instance: "TEST", Key: "turn/turn-act-1/case-progress"}, a.putErr
}
func (a *progressArtifacts) GetCaseProgressRecord(context.Context, content.Ref) (*content.CaseProgressRecord, error) {
	return a.progress, nil
}
func (a *progressArtifacts) InstanceName() string { return "TEST" }

type progressRecorder struct {
	requests []caseflow.TransitionRequest
	err      error
}

func (r *progressRecorder) Record(_ context.Context, request caseflow.TransitionRequest) (caseflow.ReceiptOutcome, error) {
	r.requests = append(r.requests, request)
	return caseflow.ReceiptOutcome{Recorded: true}, r.err
}

func TestProgressorClassifiesPermanentAndTransientFailuresBeforeReferenceWrite(t *testing.T) {
	for name, arrange := range map[string]func(*progressGraph, *progressArtifacts, *progressRecorder){
		"illegal transition": func(_ *progressGraph, _ *progressArtifacts, r *progressRecorder) {
			r.err = &caseflow.IllegalTransitionError{CaseEntityID: progressCaseID,
				EventID: "event", Kind: vocabulary.CaseEventInvestigationStarted,
				Current: vocabulary.CasePhaseColdOpen, Required: vocabulary.CasePhaseDiscovery}
		},
		"malformed committed ref": func(g *progressGraph, _ *progressArtifacts, _ *progressRecorder) {
			g.turn.Triples[0].Object = "not-a-ref"
		},
		"missing committed artifact": func(_ *progressGraph, a *progressArtifacts, _ *progressRecorder) {
			a.decisionErr = content.ErrArtifactNotFound
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, g, artifacts, recorder := progressFixture(t, payload.CaseDecisionInvestigate)
			arrange(g, artifacts, recorder)
			_, err := p.Process(t.Context(), progressTurnID, progressTurnEntity)
			var permanent *caseflow.PermanentProgressError
			if !errors.As(err, &permanent) {
				t.Fatalf("error = %v, want PermanentProgressError", err)
			}
			if artifacts.puts != 0 || len(g.writes) != 0 {
				t.Fatalf("permanent failure wrote progress: puts=%d writes=%d", artifacts.puts, len(g.writes))
			}
		})
	}

	p, g, _, recorder := progressFixture(t, payload.CaseDecisionInvestigate)
	recorder.err = context.DeadlineExceeded
	_, err := p.Process(t.Context(), progressTurnID, progressTurnEntity)
	var permanent *caseflow.PermanentProgressError
	if err == nil || errors.As(err, &permanent) || len(g.writes) != 0 {
		t.Fatalf("transient recorder failure = %v, permanent=%v writes=%d", err,
			errors.As(err, &permanent), len(g.writes))
	}

	p, g, _, _ = progressFixture(t, payload.CaseDecisionInvestigate)
	g.readErr = graphio.ErrEntityNotFound
	_, err = p.Process(t.Context(), progressTurnID, progressTurnEntity)
	var missing *caseflow.MissingTriggeredTurnError
	if !errors.As(err, &missing) {
		t.Fatalf("missing turn error = %v", err)
	}
}

func progressFixture(t *testing.T, kind payload.CaseDecisionKind, targets ...string) (
	*caseflow.Progressor, *progressGraph, *progressArtifacts, *progressRecorder,
) {
	t.Helper()
	decision := &payload.CaseDecision{
		TurnID: progressTurnID, ActionID: "act-1", CaseID: progressCaseID, ActorID: progressActorID,
		Kind: kind, TargetRefs: targets,
	}
	decision.DecisionID = payload.CaseDecisionID(decision.TurnID, decision.ActionID, decision.CaseID, decision.ActorID)
	decisionRef := content.Ref{Instance: "TEST", Key: "turn/turn-act-1/case-decision"}
	knowledgeRef := content.Ref{Instance: "TEST", Key: "turn/turn-act-1/knowledge"}
	g := &progressGraph{
		turn: &graph.EntityState{ID: progressTurnEntity, Triples: []message.Triple{
			{Subject: progressTurnEntity, Predicate: vocabulary.TurnCaseDecisionRef.String(), Object: decisionRef.String()},
			{Subject: progressTurnEntity, Predicate: vocabulary.TurnKnowledgeRef.String(), Object: knowledgeRef.String()},
		}},
		caseState: &graph.EntityState{ID: progressCaseID, Triples: []message.Triple{
			{Subject: progressCaseID, Predicate: vocabulary.CaseMemberVictim.String(), Object: progressVictimID},
		}},
	}
	a := &progressArtifacts{
		decision: &payload.CaseDecisionRecord{TurnID: progressTurnID, ActionID: "act-1",
			Status: payload.CaseDecisionStatusDecision, Decision: decision},
		knowledge: &content.KnowledgeReceipt{TurnID: progressTurnID, DecisionID: decision.DecisionID,
			Status: content.KnowledgeCommitted},
	}
	r := &progressRecorder{}
	p, err := caseflow.NewProgressor(g, a, r)
	if err != nil {
		t.Fatal(err)
	}
	return p, g, a, r
}

func TestProgressor_ObserveVictimRecordsBodyEventThenLandsReferenceLast(t *testing.T) {
	p, g, artifacts, recorder := progressFixture(t, payload.CaseDecisionObserve, progressVictimID)
	ref, err := p.Process(t.Context(), progressTurnID, progressTurnEntity)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.requests) != 1 || recorder.requests[0].Kind != vocabulary.CaseEventBodyObserved {
		t.Fatalf("lifecycle requests = %#v", recorder.requests)
	}
	if recorder.requests[0].EventID != content.CaseProgressEventID(progressTurnID,
		artifacts.decision.Decision.DecisionID, vocabulary.CaseEventBodyObserved) {
		t.Fatalf("event id = %q", recorder.requests[0].EventID)
	}
	if len(g.writes) != 1 || len(g.writes[0]) != 1 ||
		g.writes[0][0].Predicate != vocabulary.TurnCaseProgressRef.String() || g.writes[0][0].Object != ref.String() {
		t.Fatalf("reference-last writes = %#v", g.writes)
	}
	if _, err := p.Process(t.Context(), progressTurnID, progressTurnEntity); err != nil {
		t.Fatal(err)
	}
	if len(recorder.requests) != 1 || artifacts.puts != 1 || len(g.writes) != 1 {
		t.Fatalf("redelivery repeated work: lifecycle=%d puts=%d writes=%d",
			len(recorder.requests), artifacts.puts, len(g.writes))
	}
}

func TestProgressor_ResidentRecordMustMatchCanonicalDecisionAndKnowledge(t *testing.T) {
	for name, mutate := range map[string]func(*content.CaseProgressRecord){
		"decision": func(record *content.CaseProgressRecord) {
			record.DecisionID = "different-decision"
			record.EventID = content.CaseProgressEventID(record.TurnID, record.DecisionID, record.EventKind)
		},
		"case": func(record *content.CaseProgressRecord) {
			record.CaseID = "acme.semmachina.bellweather.campaign.case.different-case"
		},
		"status": func(record *content.CaseProgressRecord) {
			record.Status = content.CaseProgressNotApplicable
			record.EventID = ""
			record.EventKind = ""
		},
		"event": func(record *content.CaseProgressRecord) {
			record.EventKind = vocabulary.CaseEventInvestigationStarted
			record.EventID = content.CaseProgressEventID(record.TurnID, record.DecisionID, record.EventKind)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, g, artifacts, recorder := progressFixture(t, payload.CaseDecisionObserve, progressVictimID)
			if _, err := p.Process(t.Context(), progressTurnID, progressTurnEntity); err != nil {
				t.Fatal(err)
			}
			mutate(artifacts.progress)
			if err := artifacts.progress.Validate(); err != nil {
				t.Fatalf("test mutation must remain self-valid: %v", err)
			}
			_, err := p.Process(t.Context(), progressTurnID, progressTurnEntity)
			var permanent *caseflow.PermanentProgressError
			if !errors.As(err, &permanent) {
				t.Fatalf("resident mismatch error = %v, want PermanentProgressError", err)
			}
			if len(recorder.requests) != 1 || artifacts.puts != 1 || len(g.writes) != 1 {
				t.Fatalf("resident mismatch repeated work: lifecycle=%d puts=%d writes=%d",
					len(recorder.requests), artifacts.puts, len(g.writes))
			}
		})
	}
}

func TestProgressorRejectsMalformedMixedTypeCommittedObjects(t *testing.T) {
	t.Run("turn reference", func(t *testing.T) {
		p, g, artifacts, recorder := progressFixture(t, payload.CaseDecisionInvestigate)
		g.turn.Triples = append(g.turn.Triples, message.Triple{
			Subject: progressTurnEntity, Predicate: vocabulary.TurnCaseDecisionRef.String(), Object: 42,
		})
		_, err := p.Process(t.Context(), progressTurnID, progressTurnEntity)
		var permanent *caseflow.PermanentProgressError
		if !errors.As(err, &permanent) {
			t.Fatalf("mixed turn reference error = %v, want PermanentProgressError", err)
		}
		if len(recorder.requests) != 0 || artifacts.puts != 0 || len(g.writes) != 0 {
			t.Fatal("mixed turn reference reached a write")
		}
	})

	t.Run("case victim", func(t *testing.T) {
		p, g, artifacts, recorder := progressFixture(t, payload.CaseDecisionObserve, progressVictimID)
		g.caseState.Triples = append(g.caseState.Triples, message.Triple{
			Subject: progressCaseID, Predicate: vocabulary.CaseMemberVictim.String(), Object: false,
		})
		_, err := p.Process(t.Context(), progressTurnID, progressTurnEntity)
		var permanent *caseflow.PermanentProgressError
		if !errors.As(err, &permanent) {
			t.Fatalf("mixed victim error = %v, want PermanentProgressError", err)
		}
		if len(recorder.requests) != 0 || artifacts.puts != 0 || len(g.writes) != 0 {
			t.Fatal("mixed victim reached a write")
		}
	})
}

func TestProgressor_MapsOnlyVictimObservationAndInvestigation(t *testing.T) {
	for name, testCase := range map[string]struct {
		kind    payload.CaseDecisionKind
		targets []string
		want    vocabulary.CaseLifecycleEventKind
	}{
		"other observation": {payload.CaseDecisionObserve, []string{progressActorID}, ""},
		"investigation":     {payload.CaseDecisionInvestigate, nil, vocabulary.CaseEventInvestigationStarted},
		"question":          {payload.CaseDecisionQuestion, []string{progressActorID}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			p, _, artifacts, recorder := progressFixture(t, testCase.kind, testCase.targets...)
			if _, err := p.Process(t.Context(), progressTurnID, progressTurnEntity); err != nil {
				t.Fatal(err)
			}
			if testCase.want == "" {
				if len(recorder.requests) != 0 || artifacts.progress.Status != content.CaseProgressNotApplicable {
					t.Fatalf("no-op progress = %#v lifecycle=%#v", artifacts.progress, recorder.requests)
				}
			} else if len(recorder.requests) != 1 || recorder.requests[0].Kind != testCase.want {
				t.Fatalf("lifecycle requests = %#v, want %s", recorder.requests, testCase.want)
			}
		})
	}
}
