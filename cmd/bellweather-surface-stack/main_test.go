package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestValidateOptionsRequiresExplicitLoopbackPorts(t *testing.T) {
	base := options{playerAddr: "127.0.0.1:41001", graphAddr: "127.0.0.1:41002", diagnosticAddr: "127.0.0.1:41003"}
	for _, test := range []struct {
		name string
		edit func(*options)
	}{
		{"wildcard player", func(o *options) { o.playerAddr = "0.0.0.0:41001" }},
		{"hostname player", func(o *options) { o.playerAddr = "localhost:41001" }},
		{"ephemeral graph", func(o *options) { o.graphAddr = "127.0.0.1:0" }},
		{"missing diagnostic", func(o *options) { o.diagnosticAddr = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := base
			test.edit(&o)
			if err := validateOptions(o); err == nil {
				t.Fatal("validateOptions() error = nil")
			}
		})
	}
	if err := validateOptions(base); err != nil {
		t.Fatalf("validateOptions(valid) = %v", err)
	}
}

func TestSurfaceStackDefaultsToTheFlashLiteBellweatherInstance(t *testing.T) {
	if defaultConfigPath != "configs/instance.gemini35-flash-lite.bellweather.example.json" {
		t.Fatalf("surface stack default config = %q, want the Flash-Lite instance", defaultConfigPath)
	}
}

func TestRequirePaidOptInDoesNotEchoSecrets(t *testing.T) {
	secret := "super-secret-key"
	err := requirePaidOptIn(func(key string) string {
		if key == "SEMMACHINA_PAID_SMOKE" {
			return "1"
		}
		return ""
	})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("requirePaidOptIn() = %v", err)
	}
}

func TestProbeGraphQLRequiresKnownLocationAndRelationshipsShape(t *testing.T) {
	locationID := "c360.semmachina.run.bellweather-maze.location.fete-green-place"
	original := graphqlHTTPClient
	graphqlHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		body := `{"data":{"relationships":[]}}`
		if strings.Contains(request.Query, "entitiesByPrefix") {
			body = `{"data":{"entitiesByPrefix":[{"id":"` + locationID + `"}]}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	defer func() { graphqlHTTPClient = original }()
	if err := probeGraphQL(context.Background(), "http://graph.invalid/graphql", "c360.semmachina.run.bellweather-maze.location", locationID); err != nil {
		t.Fatalf("probeGraphQL() = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProbeGraphQLRefusesAbsentNullAndErrors(t *testing.T) {
	locationID := "c360.semmachina.run.bellweather-maze.location.fete-green-place"
	for _, test := range []struct{ name, relationshipBody string }{
		{"absent", `{"data":{}}`},
		{"null", `{"data":{"relationships":null}}`},
		{"graphql error", `{"errors":[{"message":"broken"}],"data":{"relationships":[]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			withGraphQLTransport(t, func(r *http.Request) string {
				var request struct {
					Query string `json:"query"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				if strings.Contains(request.Query, "entitiesByPrefix") {
					return `{"data":{"entitiesByPrefix":[{"id":"` + locationID + `"}]}}`
				}
				return test.relationshipBody
			})
			if err := probeGraphQL(context.Background(), "http://graph.invalid/graphql", "c360.semmachina.run.bellweather-maze.location", locationID); err == nil {
				t.Fatal("probeGraphQL() error = nil")
			}
		})
	}
}

func TestProbeGraphQLRefusesMalformedRelationshipMembers(t *testing.T) {
	locationID := "c360.semmachina.run.bellweather-maze.location.fete-green-place"
	for _, body := range []string{
		`{"data":{"relationships":[{}]}}`,
		`{"data":{"relationships":[{"from":"a","to":"b"}]}}`,
		`{"data":{"relationships":[{"from":"","to":"b","predicate":"p"}]}}`,
		`{"data":{"relationships":[{"from":"a","from_entity_id":"a","to_entity_id":"b","edge_type":"p"}]}}`,
	} {
		withGraphQLTransport(t, func(r *http.Request) string {
			var request struct {
				Query string `json:"query"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			if strings.Contains(request.Query, "entitiesByPrefix") {
				return `{"data":{"entitiesByPrefix":[{"id":"` + locationID + `"}]}}`
			}
			return body
		})
		if err := probeGraphQL(context.Background(), "http://graph.invalid/graphql", "c360.semmachina.run.bellweather-maze.location", locationID); err == nil {
			t.Fatalf("probeGraphQL() accepted %s", body)
		}
	}
}

func TestProbeGraphQLAcceptsBeta159Relationships(t *testing.T) {
	locationID := "c360.semmachina.run.bellweather-maze.location.fete-green-place"
	withGraphQLTransport(t, func(r *http.Request) string {
		var request struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(request.Query, "entitiesByPrefix") {
			return `{"data":{"entitiesByPrefix":[{"id":"` + locationID + `"}]}}`
		}
		return `{"data":{"relationships":[{"from_entity_id":"a","to_entity_id":"b","edge_type":"world.relation.knows"}]}}`
	})
	if err := probeGraphQL(context.Background(), "http://graph.invalid/graphql", "c360.semmachina.run.bellweather-maze.location", locationID); err != nil {
		t.Fatalf("probeGraphQL() = %v", err)
	}
}

func TestProbeGraphQLRejectsConflictingDualRelationships(t *testing.T) {
	locationID := "c360.semmachina.run.bellweather-maze.location.fete-green-place"
	withGraphQLTransport(t, func(r *http.Request) string {
		var request struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(request.Query, "entitiesByPrefix") {
			return `{"data":{"entitiesByPrefix":[{"id":"` + locationID + `"}]}}`
		}
		return `{"data":{"relationships":[{"from":"a","to":"b","predicate":"p","from_entity_id":"x","to_entity_id":"b","edge_type":"p"}]}}`
	})
	if err := probeGraphQL(context.Background(), "http://graph.invalid/graphql", "c360.semmachina.run.bellweather-maze.location", locationID); err == nil {
		t.Fatal("probeGraphQL() accepted conflicting dual relationship")
	}
}

type stubConn struct{ net.Conn }

func (stubConn) Close() error { return nil }

func TestAwaitEngineReadyDialsExplicitPlayerAddress(t *testing.T) {
	original := playerDialContext
	var addresses []string
	playerDialContext = func(_ context.Context, network, address string) (net.Conn, error) {
		addresses = append(addresses, network+":"+address)
		if len(addresses) == 1 {
			return nil, errors.New("not ready")
		}
		return stubConn{}, nil
	}
	defer func() { playerDialContext = original }()
	if err := awaitEngineReady(context.Background(), "127.0.0.1:43101"); err != nil {
		t.Fatalf("awaitEngineReady() = %v", err)
	}
	if got := addresses[len(addresses)-1]; got != "tcp:127.0.0.1:43101" {
		t.Fatalf("dial = %q", got)
	}
}

func TestPostGraphQLFailureBoundaries(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		original := graphqlHTTPClient
		graphqlHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("bad")), Header: make(http.Header)}, nil
		})}
		defer func() { graphqlHTTPClient = original }()
		if err := postGraphQL(context.Background(), "http://graph.invalid", "query{x}", nil, &map[string]any{}); err == nil {
			t.Fatal("error = nil")
		}
	})
	t.Run("body limit", func(t *testing.T) {
		withGraphQLTransport(t, func(*http.Request) string { return strings.Repeat("x", maxProbeBody+1) })
		if err := postGraphQL(context.Background(), "http://graph.invalid", "query{x}", nil, &map[string]any{}); err == nil {
			t.Fatal("error = nil")
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		original := graphqlHTTPClient
		graphqlHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
		defer func() { graphqlHTTPClient = original }()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := postGraphQL(ctx, "http://graph.invalid", "query{x}", nil, &map[string]any{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})
}

func withGraphQLTransport(t *testing.T, body func(*http.Request) string) {
	t.Helper()
	original := graphqlHTTPClient
	graphqlHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body(r))), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { graphqlHTTPClient = original })
}

type recordingStopper struct {
	name  string
	order *[]string
}

func (s recordingStopper) Stop(time.Duration) error { *s.order = append(*s.order, s.name); return nil }

func TestStopReadSurfaceIsGatewayThenQuery(t *testing.T) {
	var order []string
	stopReadSurface(recordingStopper{"gateway", &order}, recordingStopper{"query", &order})
	if strings.Join(order, ",") != "gateway,query" {
		t.Fatalf("order = %v", order)
	}
}

type fallbackServer struct{ closed bool }

func (*fallbackServer) Shutdown(context.Context) error { return context.DeadlineExceeded }
func (s *fallbackServer) Close() error                 { s.closed = true; return nil }

func TestShutdownDiagnosticFallsBackToClose(t *testing.T) {
	server := &fallbackServer{}
	shutdownDiagnostic(server, time.Millisecond)
	if !server.closed {
		t.Fatal("Close was not called")
	}
}

func TestStopEngineSupervisorTimesOutWithoutAStopCallback(t *testing.T) {
	done := make(chan struct{})
	cancelled := false
	err := stopEngineSupervisor(func(error) { cancelled = true }, done, time.Millisecond)
	if !cancelled {
		t.Fatal("runtime was not cancelled")
	}
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %v", err)
	}
}

type fakeObserver struct {
	turnCalls, caseCalls int
	turnErr, caseErr     error
	blockTurn            bool
}

func (o *fakeObserver) observeTurn(ctx context.Context, _ string) (turnObservation, error) {
	o.turnCalls++
	if o.blockTurn {
		<-ctx.Done()
		return turnObservation{}, ctx.Err()
	}
	return turnObservation{
		Phase:           vocabulary.PhaseComplete,
		PhaseRecordedAt: time.Date(2026, time.August, 3, 12, 34, 56, 789, time.UTC),
		HintProof: hintProof{
			Proved: true, CaseDecisionKind: string(payload.CaseDecisionRequestHint),
			TriggerKind:   string(vocabulary.CompanionTriggerPlayerHint),
			TriggerSource: string(vocabulary.CompanionTriggerSourceCaseDecision),
		},
	}, o.turnErr
}

func (o *fakeObserver) casePhase(context.Context, string) (vocabulary.CasePhase, error) {
	o.caseCalls++
	return vocabulary.CasePhaseDiscovery, o.caseErr
}

func TestDiagnosticTurnIsSafeAuthoritativeJSON(t *testing.T) {
	obs := &fakeObserver{}
	h := diagnosticHandler(obs, "c360.semmachina.run.bellweather-maze", "case-id")
	req := httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["phase"] != string(vocabulary.PhaseComplete) || got["case_phase"] != string(vocabulary.CasePhaseDiscovery) ||
		got["phase_recorded_at"] != "2026-08-03T12:34:56.000000789Z" {
		t.Fatalf("response = %v", got)
	}
	if len(got) != 6 || got["failure"] != nil {
		t.Fatalf("diagnostic response is not closed: %v", got)
	}
	proof, ok := got["kit_hint_proof"].(map[string]any)
	if !ok || len(proof) != 4 || proof["proved"] != true ||
		proof["case_decision_kind"] != string(payload.CaseDecisionRequestHint) ||
		proof["trigger_kind"] != string(vocabulary.CompanionTriggerPlayerHint) ||
		proof["trigger_source"] != string(vocabulary.CompanionTriggerSourceCaseDecision) {
		t.Fatalf("kit_hint_proof = %#v, want exact canonical proof tuple", got["kit_hint_proof"])
	}
	for _, obsolete := range []string{"turn_version", "turn_updated_at"} {
		if _, exists := got[obsolete]; exists {
			t.Fatalf("response retained obsolete entity progress field %q: %v", obsolete, got)
		}
	}
	if _, exists := got["pending"]; exists {
		t.Fatalf("response mislabeled global queue state as per-turn pending: %v", got)
	}
	if _, exists := got["queue_position"]; exists {
		t.Fatalf("response retained the heavyweight queue cursor: %v", got)
	}
	if obs.turnCalls != 1 || obs.caseCalls != 1 {
		t.Fatalf("observer reads = turn:%d case:%d, want exactly one each", obs.turnCalls, obs.caseCalls)
	}
	if timing := rec.Header().Get("Server-Timing"); !regexp.MustCompile(`^observation;dur=\d+\.\d{3}$`).MatchString(timing) {
		t.Fatalf("sanitized observation timing = %q", timing)
	}
	for _, forbidden := range []string{"credential", "bearer", "api_key", "provider_body"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), forbidden) {
			t.Fatalf("response leaked %s", forbidden)
		}
	}
}

func TestDiagnosticReadyExercisesObserverDependencyWithoutATurn(t *testing.T) {
	obs := &fakeObserver{}
	h := diagnosticHandler(obs, "prefix", "case-id")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ready":true}` {
		t.Fatalf("ready response = %d %s", rec.Code, rec.Body.String())
	}
	if obs.caseCalls != 1 || obs.turnCalls != 0 {
		t.Fatalf("ready reads = turn:%d case:%d, want imported case only", obs.turnCalls, obs.caseCalls)
	}
}

func TestDiagnosticClassifiesClosedFailuresExactly(t *testing.T) {
	for _, test := range []struct {
		name, path, body, retryAfter, cacheControl string
		obs                                        *fakeObserver
		status                                     int
	}{
		{name: "invalid id", path: "/turns/not.valid", status: http.StatusBadRequest,
			body: `{"error":"invalid_turn_id"}`, obs: &fakeObserver{}},
		{name: "accepted turn invariant", path: "/turns/turn-42", status: http.StatusNotFound,
			body: `{"error":"accepted_turn_invariant"}`, obs: &fakeObserver{turnErr: errAcceptedTurnInvariant}},
		{name: "failure state invariant", path: "/turns/turn-42", status: http.StatusInternalServerError,
			body: `{"error":"failure_state_invariant"}`, obs: &fakeObserver{turnErr: errFailureStateInvariant}},
		{name: "turn not materialized", path: "/turns/turn-42", status: http.StatusNotFound,
			body: `{"error":"turn_not_materialized"}`, retryAfter: "1", cacheControl: "no-store",
			obs: &fakeObserver{turnErr: errTurnNotMaterialized}},
		{name: "observer unavailable", path: "/turns/turn-42", status: http.StatusServiceUnavailable,
			body: `{"error":"observer_unavailable"}`, retryAfter: "1", obs: &fakeObserver{turnErr: errors.New("down")}},
		{name: "ready unavailable", path: "/ready", status: http.StatusServiceUnavailable,
			body: `{"error":"observer_unavailable"}`, retryAfter: "1", obs: &fakeObserver{caseErr: errors.New("down")}},
		{name: "ready case invariant", path: "/ready", status: http.StatusInternalServerError,
			body: `{"error":"case_phase_invariant"}`, obs: &fakeObserver{caseErr: errCasePhaseInvariant}},
		{name: "turn case invariant", path: "/turns/turn-42", status: http.StatusInternalServerError,
			body: `{"error":"case_phase_invariant"}`, obs: &fakeObserver{caseErr: errCasePhaseInvariant}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			diagnosticHandler(test.obs, "prefix", "case-id").ServeHTTP(rec,
				httptest.NewRequest(http.MethodGet, test.path, nil))
			if rec.Code != test.status || strings.TrimSpace(rec.Body.String()) != test.body ||
				rec.Header().Get("Retry-After") != test.retryAfter ||
				rec.Header().Get("Cache-Control") != test.cacheControl {
				t.Fatalf("response = %d retry=%q cache=%q body=%q", rec.Code,
					rec.Header().Get("Retry-After"), rec.Header().Get("Cache-Control"), rec.Body.String())
			}
		})
	}
}

func TestDiagnosticObservationBudgetCancelsBeforeClientDeadline(t *testing.T) {
	obs := &fakeObserver{blockTurn: true}
	h := diagnosticHandlerWithTimeout(obs, "prefix", "case-id", 20*time.Millisecond)
	clientCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	started := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil).WithContext(clientCtx))
	if elapsed := time.Since(started); elapsed >= 250*time.Millisecond {
		t.Fatalf("server observation took %s, want cancellation well before client deadline", elapsed)
	}
	if rec.Code != http.StatusGatewayTimeout || strings.TrimSpace(rec.Body.String()) != `{"error":"observation_timeout"}` {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}

type recordingEntityReader struct {
	states map[string]*graph.EntityState
	errs   map[string]error
	reads  map[string]int
}

func (r *recordingEntityReader) GetEntity(ctx context.Context, id string) (*graph.EntityState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.reads[id]++
	if err := r.errs[id]; err != nil {
		return nil, err
	}
	return r.states[id], nil
}

func TestProductionObserverReturnsPhaseTripleProgressWithZeroEntityUpdatedAt(t *testing.T) {
	turnID, caseID := "prefix.turn.turn-42", "prefix.case.main"
	at := time.Date(2026, time.August, 3, 13, 0, 0, 0, time.FixedZone("test-offset", -5*60*60))
	reader := &recordingEntityReader{
		reads: map[string]int{}, errs: map[string]error{}, states: map[string]*graph.EntityState{
			turnID: {ID: turnID, Version: 1, Triples: []message.Triple{
				{Predicate: vocabulary.TurnPhaseCurrent.String(), Object: string(vocabulary.PhaseFailed), Timestamp: at},
				{Predicate: vocabulary.TurnFailureReason.String(), Object: string(vocabulary.FailureEffectInvalid)},
			}},
			caseID: {ID: caseID, Triples: []message.Triple{{
				Predicate: vocabulary.CaseLifecyclePhase.String(), Object: string(vocabulary.CasePhaseDiscovery),
			}}},
		},
	}
	obs := &productionObserver{graph: reader}
	rec := httptest.NewRecorder()
	diagnosticHandler(obs, "prefix", caseID).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["phase"] != string(vocabulary.PhaseFailed) || body["phase_recorded_at"] != at.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("response = %v", body)
	}
	if reader.reads[turnID] != 1 || reader.reads[caseID] != 1 {
		t.Fatalf("entity reads = turn:%d case:%d", reader.reads[turnID], reader.reads[caseID])
	}
}

func TestProductionObserverClassifiesReturnedEntityAnomaliesAsAcceptedTurnInvariant(t *testing.T) {
	for _, test := range []struct {
		name   string
		states map[string]*graph.EntityState
		errs   map[string]error
	}{
		{name: "nil entity", states: map[string]*graph.EntityState{
			"prefix.turn.turn-42": nil,
		}, errs: map[string]error{}},
		{name: "phase missing", states: map[string]*graph.EntityState{
			"prefix.turn.turn-42": {ID: "prefix.turn.turn-42"},
		}, errs: map[string]error{}},
		{name: "phase invalid", states: map[string]*graph.EntityState{
			"prefix.turn.turn-42": {
				ID: "prefix.turn.turn-42", Version: 1,
				Triples: []message.Triple{{Predicate: vocabulary.TurnPhaseCurrent.String(), Object: "not-a-phase", Timestamp: time.Now()}},
			},
		}, errs: map[string]error{}},
		{name: "phase duplicated", states: map[string]*graph.EntityState{
			"prefix.turn.turn-42": {
				ID: "prefix.turn.turn-42", Version: 1,
				Triples: []message.Triple{
					{Predicate: vocabulary.TurnPhaseCurrent.String(), Object: string(vocabulary.PhaseAdjudicating), Timestamp: time.Now()},
					{Predicate: vocabulary.TurnPhaseCurrent.String(), Object: string(vocabulary.PhaseResolving), Timestamp: time.Now()},
				},
			},
		}, errs: map[string]error{}},
		{name: "phase is not a string", states: map[string]*graph.EntityState{
			"prefix.turn.turn-42": {
				ID: "prefix.turn.turn-42", Version: 1,
				Triples: []message.Triple{{Predicate: vocabulary.TurnPhaseCurrent.String(), Object: 42, Timestamp: time.Now()}},
			},
		}, errs: map[string]error{}},
		{name: "turn is a stub", states: map[string]*graph.EntityState{
			"prefix.turn.turn-42": {
				ID: "prefix.turn.turn-42", MessageType: graph.StubMessageType, Version: 1,
				Triples: []message.Triple{{Predicate: vocabulary.TurnPhaseCurrent.String(), Object: string(vocabulary.PhaseAdjudicating), Timestamp: time.Now()}},
			},
		}, errs: map[string]error{}},
		{name: "phase timestamp is zero", states: map[string]*graph.EntityState{
			"prefix.turn.turn-42": {
				ID: "prefix.turn.turn-42", Version: 1,
				Triples: []message.Triple{{Predicate: vocabulary.TurnPhaseCurrent.String(), Object: string(vocabulary.PhaseAdjudicating)}},
			},
		}, errs: map[string]error{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &recordingEntityReader{
				states: test.states, errs: test.errs, reads: map[string]int{},
			}
			rec := httptest.NewRecorder()
			diagnosticHandler(&productionObserver{graph: reader}, "prefix", "prefix.case.main").ServeHTTP(rec,
				httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil))
			if rec.Code != http.StatusNotFound ||
				strings.TrimSpace(rec.Body.String()) != `{"error":"accepted_turn_invariant"}` {
				t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
			}
			if reader.reads["prefix.turn.turn-42"] != 1 || reader.reads["prefix.case.main"] != 0 {
				t.Fatalf("entity reads = turn:%d case:%d, want 1/0",
					reader.reads["prefix.turn.turn-42"], reader.reads["prefix.case.main"])
			}
		})
	}
}

func TestProductionObserverClassifiesOnlyTypedMissingEntityAsTransitional(t *testing.T) {
	turnID, caseID := "prefix.turn.turn-42", "prefix.case.main"
	reader := &recordingEntityReader{
		states: map[string]*graph.EntityState{},
		errs:   map[string]error{turnID: fmt.Errorf("read graph: %w", graphio.ErrEntityNotFound)},
		reads:  map[string]int{},
	}
	rec := httptest.NewRecorder()
	diagnosticHandler(&productionObserver{graph: reader}, "prefix", caseID).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil))
	if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != `{"error":"turn_not_materialized"}` {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != "1" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("retry=%q cache=%q", rec.Header().Get("Retry-After"), rec.Header().Get("Cache-Control"))
	}
	if reader.reads[turnID] != 1 || reader.reads[caseID] != 0 {
		t.Fatalf("entity reads = turn:%d case:%d, want 1/0", reader.reads[turnID], reader.reads[caseID])
	}
}

func TestProductionObserverAllowsIdenticalCasePhaseDuplicates(t *testing.T) {
	caseID := "prefix.case.main"
	reader := &recordingEntityReader{
		states: map[string]*graph.EntityState{caseID: {
			ID: caseID, Triples: []message.Triple{
				{Predicate: vocabulary.CaseLifecyclePhase.String(), Object: string(vocabulary.CasePhaseDiscovery)},
				{Predicate: vocabulary.CaseLifecyclePhase.String(), Object: string(vocabulary.CasePhaseDiscovery)},
			},
		}}, errs: map[string]error{}, reads: map[string]int{},
	}
	rec := httptest.NewRecorder()
	diagnosticHandler(&productionObserver{graph: reader}, "prefix", caseID).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ready":true}` {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != "" || rec.Header().Get("Cache-Control") != "" {
		t.Fatalf("retry=%q cache=%q", rec.Header().Get("Retry-After"), rec.Header().Get("Cache-Control"))
	}
	if reader.reads[caseID] != 1 {
		t.Fatalf("case reads = %d, want one", reader.reads[caseID])
	}
}

func TestProductionObserverClassifiesCasePhaseAnomaliesAsTerminal(t *testing.T) {
	caseID := "prefix.case.main"
	for _, test := range []struct {
		name  string
		state *graph.EntityState
		err   error
	}{
		{name: "phase conflicts", state: &graph.EntityState{ID: caseID, Triples: []message.Triple{
			{Predicate: vocabulary.CaseLifecyclePhase.String(), Object: string(vocabulary.CasePhaseDiscovery)},
			{Predicate: vocabulary.CaseLifecyclePhase.String(), Object: string(vocabulary.CasePhaseDenouement)},
		}}},
		{name: "phase types are mixed", state: &graph.EntityState{ID: caseID, Triples: []message.Triple{
			{Predicate: vocabulary.CaseLifecyclePhase.String(), Object: string(vocabulary.CasePhaseDiscovery)},
			{Predicate: vocabulary.CaseLifecyclePhase.String(), Object: 42},
		}}},
		{name: "phase is missing", state: &graph.EntityState{ID: caseID}},
		{name: "phase is unknown", state: &graph.EntityState{ID: caseID, Triples: []message.Triple{
			{Predicate: vocabulary.CaseLifecyclePhase.String(), Object: "not-a-case-phase"},
		}}},
		{name: "phase is empty", state: &graph.EntityState{ID: caseID, Triples: []message.Triple{
			{Predicate: vocabulary.CaseLifecyclePhase.String(), Object: ""},
		}}},
		{name: "case is nil", state: nil},
		{name: "case is a stub", state: &graph.EntityState{
			ID: caseID, MessageType: graph.StubMessageType, Triples: []message.Triple{
				{Predicate: vocabulary.CaseLifecyclePhase.String(), Object: string(vocabulary.CasePhaseDiscovery)},
			},
		}},
		{name: "case entity is missing", err: graphio.ErrEntityNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &recordingEntityReader{
				states: map[string]*graph.EntityState{caseID: test.state},
				errs:   map[string]error{caseID: test.err}, reads: map[string]int{},
			}
			rec := httptest.NewRecorder()
			diagnosticHandler(&productionObserver{graph: reader}, "prefix", caseID).ServeHTTP(rec,
				httptest.NewRequest(http.MethodGet, "/ready", nil))
			if rec.Code != http.StatusInternalServerError ||
				strings.TrimSpace(rec.Body.String()) != `{"error":"case_phase_invariant"}` ||
				rec.Header().Get("Retry-After") != "" || rec.Header().Get("Cache-Control") != "" {
				t.Fatalf("response = %d retry=%q cache=%q body=%q", rec.Code,
					rec.Header().Get("Retry-After"), rec.Header().Get("Cache-Control"), rec.Body.String())
			}
			if reader.reads[caseID] != 1 {
				t.Fatalf("case reads = %d, want one", reader.reads[caseID])
			}
		})
	}
}

func TestDiagnosticTurnMapsCasePhaseInvariantAfterOneTurnAndCaseRead(t *testing.T) {
	obs := &fakeObserver{caseErr: errCasePhaseInvariant}
	rec := httptest.NewRecorder()
	diagnosticHandler(obs, "prefix", "case-id").ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/turns/turn-42", nil))
	if rec.Code != http.StatusInternalServerError ||
		strings.TrimSpace(rec.Body.String()) != `{"error":"case_phase_invariant"}` ||
		rec.Header().Get("Retry-After") != "" || rec.Header().Get("Cache-Control") != "" {
		t.Fatalf("response = %d retry=%q cache=%q body=%q", rec.Code,
			rec.Header().Get("Retry-After"), rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	if obs.turnCalls != 1 || obs.caseCalls != 1 {
		t.Fatalf("observer reads = turn:%d case:%d, want 1/1", obs.turnCalls, obs.caseCalls)
	}
}
