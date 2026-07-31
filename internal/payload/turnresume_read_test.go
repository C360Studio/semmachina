package payload_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

func TestResumeAttemptsFromTriples_AcceptsTheGraphNumericShapes(t *testing.T) {
	cases := map[string]struct {
		object any
		want   int
	}{
		"absent":      {want: 0},
		"int":         {object: int(1), want: 1},
		"int64":       {object: int64(2), want: 2},
		"float64":     {object: float64(3), want: 3},
		"json.Number": {object: json.Number("4"), want: 4},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var triples []message.Triple
			if tc.object != nil {
				triples = []message.Triple{{
					Predicate: vocabulary.TurnResumeAttempts.String(), Object: tc.object,
				}}
			}
			got, err := payload.ResumeAttemptsFromTriples(triples)
			if err != nil {
				t.Fatalf("ResumeAttemptsFromTriples: %v", err)
			}
			if got != tc.want {
				t.Fatalf("attempts = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestResumeAttemptsFromTriples_RefusesAmbiguousOrInvalidValues(t *testing.T) {
	fact := func(object any) message.Triple {
		return message.Triple{Predicate: vocabulary.TurnResumeAttempts.String(), Object: object}
	}
	cases := map[string]struct {
		triples []message.Triple
		want    string
	}{
		"duplicate":  {triples: []message.Triple{fact(1), fact(2)}, want: "holds 2 values"},
		"fractional": {triples: []message.Triple{fact(1.5)}, want: "whole number"},
		"zero":       {triples: []message.Triple{fact(0)}, want: "persisted count must be positive"},
		"negative":   {triples: []message.Triple{fact(-1)}, want: "persisted count must be positive"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := payload.ResumeAttemptsFromTriples(tc.triples)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
