package knowledge_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/knowledge"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/turn"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const testTurnEntityID = "acme.semmachina.keep.starter.turn.turn-1"

func trigger(t *testing.T) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"entity_id": testTurnEntityID,
		"subject":   rulepack.SubjectKnowledge,
		"source":    "rule_engine",
	})
	if err != nil {
		t.Fatalf("encode trigger: %v", err)
	}
	return data
}

type fakeDurable struct {
	config    natsclient.StreamConsumerConfig
	heartbeat time.Duration
	handler   func(context.Context, []byte) error
}

func (f *fakeDurable) ConsumeDurable(
	_ context.Context, config natsclient.StreamConsumerConfig, heartbeat time.Duration,
	handler func(context.Context, []byte) error,
) error {
	f.config, f.heartbeat, f.handler = config, heartbeat, handler
	return nil
}

type fakeLoader struct {
	result knowledge.LoadResult
	err    error
	calls  int
}

func (f *fakeLoader) Load(context.Context, string) (knowledge.LoadResult, error) {
	f.calls++
	return f.result, f.err
}

type fakeCommitter struct {
	grantErr       error
	grantCalls     int
	notApplicable  int
	lastTurnEntity string
}

type fakeDetails struct {
	stored []*content.FailureDetail
	ref    content.Ref
	err    error
}

func (f *fakeDetails) PutFailureDetail(
	_ context.Context, _ string, detail *content.FailureDetail,
) (content.Ref, error) {
	detailCopy := *detail
	f.stored = append(f.stored, &detailCopy)
	return f.ref, f.err
}

func (f *fakeCommitter) Grant(
	_ context.Context, turnEntityID string, _ knowledge.Preflight, _ knowledge.ShareAuthorizer,
) (content.Ref, error) {
	f.grantCalls++
	f.lastTurnEntity = turnEntityID
	return content.Ref{}, f.grantErr
}

func (f *fakeCommitter) GrantNotApplicable(
	_ context.Context, _ string, turnEntityID string,
) (content.Ref, error) {
	f.notApplicable++
	f.lastTurnEntity = turnEntityID
	return content.Ref{}, f.grantErr
}

type fakeFailer struct {
	calls  int
	reason vocabulary.FailureReason
	ref    content.Ref
	err    error
}

func (f *fakeFailer) Fail(
	context.Context, string, string, vocabulary.FailureReason, content.Ref,
) (turn.Transition, error) {
	panic("fakeFailer.Fail must be initialized with recordingFailer")
}

type recordingFailer struct{ *fakeFailer }

func (f recordingFailer) Fail(
	_ context.Context, _, _ string, reason vocabulary.FailureReason, ref content.Ref,
) (turn.Transition, error) {
	f.calls++
	f.reason, f.ref = reason, ref
	return turn.Transition{}, f.err
}

func newConsumer(
	t *testing.T, loader *fakeLoader, committer *fakeCommitter, failer *fakeFailer, details ...*fakeDetails,
) *knowledge.Consumer {
	t.Helper()
	detailStore := &fakeDetails{ref: content.Ref{Instance: "CONTENT", Key: "turn/turn-1/failure"}}
	if len(details) > 0 {
		detailStore = details[0]
	}
	consumer, err := knowledge.NewConsumer(&fakeDurable{}, loader, committer,
		recordingFailer{fakeFailer: failer}, detailStore, knowledge.DenyShares{})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	return consumer
}

func TestConsumer_StartBindsTheSeparateKnowledgeDurable(t *testing.T) {
	durable := &fakeDurable{}
	consumer, err := knowledge.NewConsumer(durable, &fakeLoader{}, &fakeCommitter{},
		recordingFailer{&fakeFailer{}}, &fakeDetails{}, knowledge.DenyShares{})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	if err := consumer.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if durable.config.StreamName != rulepack.StageStream ||
		durable.config.ConsumerName != rulepack.KnowledgeConsumerName ||
		durable.config.FilterSubject != rulepack.SubjectKnowledge {
		t.Fatalf("bound %+v, want the knowledge durable on TURN_STAGES", durable.config)
	}
	if durable.config.DeliverPolicy != "all" || durable.config.AckPolicy != "explicit" ||
		durable.config.MaxDeliver != 0 || durable.config.AckWait != knowledge.DefaultAckWait {
		t.Fatalf("unsafe delivery policy: %+v", durable.config)
	}
	if durable.heartbeat != knowledge.DefaultHeartbeat || durable.handler == nil {
		t.Fatalf("heartbeat=%s handler=%v", durable.heartbeat, durable.handler != nil)
	}
}

func TestNewConsumerRequiresFailureDetailStore(t *testing.T) {
	if _, err := knowledge.NewConsumer(&fakeDurable{}, &fakeLoader{}, &fakeCommitter{},
		recordingFailer{&fakeFailer{}}, nil, knowledge.DenyShares{}); err == nil {
		t.Fatal("NewConsumer accepted no failure detail store")
	}
}

func TestConsumer_AcksCommittedAndNotApplicableWork(t *testing.T) {
	for _, tc := range []struct {
		name       string
		applicable bool
	}{
		{name: "committed", applicable: true},
		{name: "not applicable", applicable: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader := &fakeLoader{result: knowledge.LoadResult{
				TurnID: "turn-1", Applicable: tc.applicable, Preflight: knowledge.Preflight{},
			}}
			committer, failer := &fakeCommitter{}, &fakeFailer{}
			if err := newConsumer(t, loader, committer, failer).Handle(t.Context(), trigger(t)); err != nil {
				t.Fatalf("Handle returned redelivery after commit: %v", err)
			}
			if tc.applicable && committer.grantCalls != 1 {
				t.Fatalf("Grant calls=%d, want 1", committer.grantCalls)
			}
			if !tc.applicable && committer.notApplicable != 1 {
				t.Fatalf("GrantNotApplicable calls=%d, want 1", committer.notApplicable)
			}
			if failer.calls != 0 {
				t.Fatalf("successful work failed the turn %d times", failer.calls)
			}
		})
	}
}

func TestConsumer_PermanentAuthorizationFailureDurablyFailsThenAcks(t *testing.T) {
	const detailCanary = "API-KEY-DETAIL-CANARY"
	rejection := &knowledge.AuthorizationError{Reason: knowledge.ReasonWrongActor, Detail: detailCanary}
	loader := &fakeLoader{result: knowledge.LoadResult{TurnID: "turn-1", Applicable: true}}
	committer := &fakeCommitter{grantErr: rejection}
	failer := &fakeFailer{}
	details := &fakeDetails{ref: content.Ref{Instance: "CONTENT", Key: "turn/turn-1/failure"}}

	err := newConsumer(t, loader, committer, failer, details).Handle(t.Context(), trigger(t))
	if err != nil {
		t.Fatalf("permanent rejection was redelivered after durable failure: %v", err)
	}
	if failer.calls != 1 || failer.reason != vocabulary.FailureKnowledgeUnauthorized {
		t.Fatalf("failure calls=%d reason=%q", failer.calls, failer.reason)
	}
	if failer.ref != details.ref {
		t.Fatalf("authorization failure ref = %+v, want %+v", failer.ref, details.ref)
	}
	if len(details.stored) != 1 {
		t.Fatalf("stored details = %d, want one", len(details.stored))
	}
	stored := details.stored[0]
	if stored.Reason != vocabulary.FailureKnowledgeUnauthorized ||
		stored.Class != content.FailureClassDeterministic ||
		stored.AuthorizationReason != vocabulary.AuthorizationWrongActor ||
		stored.Message != "knowledge authorization was refused" || strings.Contains(stored.Message, detailCanary) {
		t.Fatalf("stored unsafe or incomplete detail: %+v", stored)
	}
}

func TestConsumer_PermanentForeignTurnPreflightFailureWritesOnlyTerminalFailure(t *testing.T) {
	loader := &fakeLoader{err: &knowledge.AuthorizationError{Reason: knowledge.ReasonWrongTurn}}
	committer := &fakeCommitter{}
	failer := &fakeFailer{}
	details := &fakeDetails{ref: content.Ref{Instance: "CONTENT", Key: "turn/turn-1/failure"}}

	if err := newConsumer(t, loader, committer, failer, details).Handle(t.Context(), trigger(t)); err != nil {
		t.Fatalf("foreign-turn rejection was redelivered after durable failure: %v", err)
	}
	if committer.grantCalls != 0 || committer.notApplicable != 0 {
		t.Fatalf("foreign-turn preflight reached persistence: grants=%d no-ops=%d",
			committer.grantCalls, committer.notApplicable)
	}
	if failer.calls != 1 || failer.reason != vocabulary.FailureKnowledgeUnauthorized ||
		failer.ref != details.ref || len(details.stored) != 1 ||
		details.stored[0].AuthorizationReason != vocabulary.AuthorizationWrongTurn {
		t.Fatalf("terminal failure = calls %d reason %q ref %+v", failer.calls, failer.reason, failer.ref)
	}
}

func TestConsumer_TransientFailuresAreRedelivered(t *testing.T) {
	transient := errors.New("store unavailable")
	for _, tc := range []struct {
		name      string
		loaderErr error
		grantErr  error
		failErr   error
	}{
		{name: "preflight read", loaderErr: transient},
		{name: "commit", grantErr: transient},
		{name: "durable failure write", grantErr: &knowledge.AuthorizationError{Reason: knowledge.ReasonWrongCase}, failErr: transient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader := &fakeLoader{result: knowledge.LoadResult{TurnID: "turn-1", Applicable: true}, err: tc.loaderErr}
			committer := &fakeCommitter{grantErr: tc.grantErr}
			failer := &fakeFailer{err: tc.failErr}
			if err := newConsumer(t, loader, committer, failer).Handle(t.Context(), trigger(t)); err == nil {
				t.Fatal("transient failure was acknowledged instead of returned for redelivery")
			}
		})
	}
}

func TestConsumer_DetailStoreFailureRedeliversBeforeFailingTheTurn(t *testing.T) {
	const detailCanary = "AUTHORIZATION-DETAIL-SECRET"
	loader := &fakeLoader{result: knowledge.LoadResult{TurnID: "turn-1", Applicable: true}}
	committer := &fakeCommitter{grantErr: &knowledge.AuthorizationError{
		Reason: knowledge.ReasonWrongCase, Detail: detailCanary,
	}}
	failer := &fakeFailer{}
	details := &fakeDetails{err: errors.New("DETAIL-STORE-SECRET-CANARY")}
	consumer := newConsumer(t, loader, committer, failer, details)
	if err := consumer.Handle(t.Context(), trigger(t)); err == nil {
		t.Fatal("detail-store failure was acknowledged")
	} else if strings.Contains(err.Error(), "DETAIL-STORE-SECRET-CANARY") {
		t.Fatalf("detail-store error leaked through delivery result: %v", err)
	}
	if failer.calls != 0 {
		t.Fatalf("turn failed %d time(s) before its diagnostic was durable", failer.calls)
	}
	if len(details.stored) != 1 || strings.Contains(details.stored[0].Message, detailCanary) {
		t.Fatalf("unsafe stored candidate: %+v", details.stored)
	}
	details.err = nil
	details.ref = content.Ref{Instance: "CONTENT", Key: "turn/turn-1/failure"}
	if err := consumer.Handle(t.Context(), trigger(t)); err != nil {
		t.Fatalf("retry did not converge after detail store recovered: %v", err)
	}
	if failer.calls != 1 || failer.ref != details.ref || len(details.stored) != 2 ||
		!reflect.DeepEqual(details.stored[0], details.stored[1]) {
		t.Fatalf("details=%+v failer=%+v", details.stored, failer)
	}
}

func TestConsumer_RedeliveryConvergesOnSameFailureDetail(t *testing.T) {
	loader := &fakeLoader{result: knowledge.LoadResult{TurnID: "turn-1", Applicable: true}}
	committer := &fakeCommitter{grantErr: &knowledge.AuthorizationError{Reason: knowledge.ReasonWrongActor}}
	failer := &fakeFailer{err: errors.New("turn store unavailable")}
	details := &fakeDetails{ref: content.Ref{Instance: "CONTENT", Key: "turn/turn-1/failure"}}
	consumer := newConsumer(t, loader, committer, failer, details)
	if err := consumer.Handle(t.Context(), trigger(t)); err == nil {
		t.Fatal("failed terminal write was acknowledged")
	}
	failer.err = nil
	if err := consumer.Handle(t.Context(), trigger(t)); err != nil {
		t.Fatalf("redelivery did not converge: %v", err)
	}
	if len(details.stored) != 2 || !reflect.DeepEqual(details.stored[0], details.stored[1]) ||
		failer.calls != 2 || failer.ref != details.ref {
		t.Fatalf("details=%+v failer=%+v", details.stored, failer)
	}
}

func TestConsumer_PoisonTriggerTerminatesBeforeAnyRead(t *testing.T) {
	loader, committer, failer := &fakeLoader{}, &fakeCommitter{}, &fakeFailer{}
	err := newConsumer(t, loader, committer, failer).Handle(t.Context(), []byte("{"))
	if err == nil || !strings.Contains(err.Error(), "decode stage trigger") {
		t.Fatalf("poison trigger error=%v", err)
	}
	if loader.calls != 0 || committer.grantCalls != 0 || failer.calls != 0 {
		t.Fatalf("poison trigger touched loader=%d granter=%d failer=%d",
			loader.calls, committer.grantCalls, failer.calls)
	}
}
