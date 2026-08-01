package graphio_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semmachina/internal/graphio"
)

func TestEntitiesByPredicateValueUsesBoundedNATSDirectQuery(t *testing.T) {
	reply, err := json.Marshal(graph.NewQueryResponse(graph.PredicateData{
		Entities: []string{"acme.semmachina.world.pack.knowledge.one"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	requester := &fakeRequester{reply: reply}
	store, err := graphio.NewStore(requester)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.EntitiesByPredicateValue(t.Context(), "knowledge.actor.holder", "actor-1", 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "acme.semmachina.world.pack.knowledge.one" {
		t.Fatalf("entities = %v", got)
	}
	if requester.subject != graphio.SubjectIndexQueryPredicate {
		t.Fatalf("subject = %q", requester.subject)
	}
	var request struct {
		Predicate string  `json:"predicate"`
		Value     *string `json:"value"`
		Limit     int     `json:"limit"`
	}
	if err := json.Unmarshal(requester.request, &request); err != nil {
		t.Fatal(err)
	}
	if request.Predicate != "knowledge.actor.holder" ||
		request.Value == nil || *request.Value != "actor-1" || request.Limit != 9 {
		t.Fatalf("request = %+v", request)
	}
}

func TestEntitiesByPredicateValueRejectsInvalidRequestsWithoutIO(t *testing.T) {
	tests := map[string]struct {
		predicate string
		value     string
		limit     int
	}{
		"empty predicate": {value: "actor-1", limit: 1},
		"empty value":     {predicate: "knowledge.actor.holder", limit: 1},
		"zero limit":      {predicate: "knowledge.actor.holder", value: "actor-1"},
		"negative limit":  {predicate: "knowledge.actor.holder", value: "actor-1", limit: -1},
	}
	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			requester := &fakeRequester{}
			store := newStore(t, requester)
			if _, err := store.EntitiesByPredicateValue(
				t.Context(), testCase.predicate, testCase.value, testCase.limit,
			); err == nil {
				t.Fatal("invalid predicate-value query was accepted")
			}
			if requester.requests != 0 {
				t.Fatalf("invalid query made %d request(s)", requester.requests)
			}
		})
	}
}

func TestEntitiesByPredicateValuePreservesRequesterFailure(t *testing.T) {
	want := errors.New("classified index unavailable")
	store := newStore(t, &fakeRequester{err: want})
	_, err := store.EntitiesByPredicateValue(
		t.Context(), "knowledge.actor.holder", "actor-1", 2,
	)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped requester failure", err)
	}
}

func TestEntitiesByPredicateValueRejectsMalformedSuccess(t *testing.T) {
	t.Run("invalid JSON", func(t *testing.T) {
		store := newStore(t, &fakeRequester{reply: []byte("not-json")})
		_, err := store.EntitiesByPredicateValue(
			t.Context(), "knowledge.actor.holder", "actor-1", 2,
		)
		if err == nil || !strings.Contains(err.Error(), "decode") {
			t.Fatalf("error = %v, want decode refusal", err)
		}
	})

	t.Run("response exceeds requested hard limit", func(t *testing.T) {
		reply, err := json.Marshal(graph.NewQueryResponse(graph.PredicateData{
			Entities: []string{"one", "two", "three"},
		}))
		if err != nil {
			t.Fatal(err)
		}
		store := newStore(t, &fakeRequester{reply: reply})
		_, err = store.EntitiesByPredicateValue(
			t.Context(), "knowledge.actor.holder", "actor-1", 2,
		)
		if err == nil || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("error = %v, want hard response-bound refusal", err)
		}
	})
}
