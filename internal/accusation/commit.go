package accusation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/graphio"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const resultSource = "accusation-verifier"

// RecordStore is the universal accusation-barrier artifact surface.
type RecordStore interface {
	PutAccusationRecord(context.Context, string, *content.AccusationRecord) (content.Ref, error)
	GetAccusationRecord(context.Context, content.Ref) (*content.AccusationRecord, error)
}

// CommitGraph is the exact-result journal surface.
type CommitGraph interface {
	GetEntity(context.Context, string) (*graph.EntityState, error)
	MergeTriples(context.Context, string, []message.Triple, ...graphio.MergeOption) (*graph.EntityState, error)
}

// CommitOption configures deterministic commit metadata.
type CommitOption func(*Committer)

// WithNow installs a timestamp source for tests.
func WithNow(now func() time.Time) CommitOption { return func(c *Committer) { c.now = now } }

// Committer writes the complete record first, then the sole barrier reference.
type Committer struct {
	graph CommitGraph
	store RecordStore
	now   func() time.Time
}

// NewCommitter builds the idempotent result journal.
func NewCommitter(graphStore CommitGraph, artifacts RecordStore, opts ...CommitOption) (*Committer, error) {
	if graphStore == nil || artifacts == nil {
		return nil, errors.New("accusation committer requires graph and result store")
	}
	c := &Committer{graph: graphStore, store: artifacts, now: time.Now}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// CommitResult completes the barrier with a verified result.
func (c *Committer) CommitResult(
	ctx context.Context, turnEntityID string, result *payload.AccusationResult,
) (content.Ref, error) {
	if result == nil {
		return content.Ref{}, integrity("accusation commit requires a result")
	}
	return c.Commit(ctx, turnEntityID, &content.AccusationRecord{
		TurnID: result.TurnID, Status: content.AccusationResultRecorded, Result: result,
	})
}

// CommitNotApplicable completes the universal barrier without verification.
func (c *Committer) CommitNotApplicable(
	ctx context.Context, turnID, turnEntityID string,
) (content.Ref, error) {
	return c.Commit(ctx, turnEntityID, &content.AccusationRecord{
		TurnID: turnID, Status: content.AccusationNotApplicable,
	})
}

// Commit converges duplicate delivery on one exact resident record and refuses
// partial or mismatched resident state as an integrity failure.
func (c *Committer) Commit(ctx context.Context, turnEntityID string, record *content.AccusationRecord) (content.Ref, error) {
	if record == nil {
		return content.Ref{}, integrity("accusation commit requires a record")
	}
	if err := record.Validate(); err != nil {
		return content.Ref{}, integrity("invalid accusation record: %v", err)
	}
	if err := payload.RequireTurnEntityID(record.TurnID, turnEntityID); err != nil {
		return content.Ref{}, integrity("accusation record turn identity: %v", err)
	}
	state, err := c.graph.GetEntity(ctx, turnEntityID)
	if err != nil {
		if errors.Is(err, graphio.ErrEntityNotFound) {
			return content.Ref{}, integrity("accusation turn is missing during commit: %v", err)
		}
		return content.Ref{}, fmt.Errorf("read turn before accusation commit: %w", err)
	}
	refs := stringObjects(state, vocabulary.TurnAccusationRef)
	if len(refs) > 0 {
		if len(refs) != 1 {
			return content.Ref{}, integrity("resident accusation record is ambiguous")
		}
		ref, parseErr := content.ParseRef(refs[0])
		if parseErr != nil {
			return content.Ref{}, integrity("resident accusation reference: %v", parseErr)
		}
		resident, readErr := c.store.GetAccusationRecord(ctx, ref)
		if readErr != nil {
			if permanentArtifactError(readErr) {
				return content.Ref{}, integrity("resolve resident accusation record: %v", readErr)
			}
			return content.Ref{}, fmt.Errorf("resolve resident accusation record: %w", readErr)
		}
		if !reflect.DeepEqual(resident, record) {
			return content.Ref{}, integrity("resident accusation record does not match deterministic retry")
		}
		return ref, nil
	}

	ref, err := c.store.PutAccusationRecord(ctx, turnEntityID, record)
	if err != nil {
		return content.Ref{}, fmt.Errorf("store accusation record: %w", err)
	}
	at := c.now().UTC()
	triples := []message.Triple{resultTriple(turnEntityID, vocabulary.TurnAccusationRef, ref.String(), at)}
	if _, err := c.graph.MergeTriples(ctx, turnEntityID, triples); err != nil {
		return content.Ref{}, fmt.Errorf("record accusation barrier on turn: %w", err)
	}
	return ref, nil
}

func stringObjects(state *graph.EntityState, predicate vocabulary.Predicate) []string {
	if state == nil {
		return nil
	}
	var values []string
	for _, triple := range state.Triples {
		if triple.Subject == state.ID && triple.Predicate == predicate.String() {
			value, ok := triple.Object.(string)
			if !ok {
				return []string{"<non-string>"}
			}
			values = append(values, value)
		}
	}
	return values
}

func resultTriple(subject string, predicate vocabulary.Predicate, object string, at time.Time) message.Triple {
	return message.Triple{Subject: subject, Predicate: predicate.String(), Object: object,
		Source: resultSource, Timestamp: at, Confidence: 1, Context: subject}
}
