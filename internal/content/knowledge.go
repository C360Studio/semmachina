package content

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/c360studio/semstreams/pkg/types"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	// MaxKnowledgeReceiptEntries is the hard aggregate grant bound per turn.
	MaxKnowledgeReceiptEntries = 24
	// MaxTestimonyProseBytes bounds one private authored testimony artifact.
	MaxTestimonyProseBytes = 32 << 10

	knowledgeReceiptIdentityDomain = "knowledge-receipt/v1"
	testimonyIdentityDomain        = "testimony/v1"
)

// KnowledgeReceiptStatus distinguishes a real mystery commit from a deterministic no-op.
type KnowledgeReceiptStatus string

const (
	// KnowledgeCommitted records a fully authorized mystery grant batch.
	KnowledgeCommitted KnowledgeReceiptStatus = "committed"
	// KnowledgeNotApplicable records the deterministic non-mystery no-op.
	KnowledgeNotApplicable KnowledgeReceiptStatus = "not-applicable"
)

// KnowledgeReceiptEntry records structural identities only; testimony prose stays behind its ref.
type KnowledgeReceiptEntry struct {
	RecipientID  string `json:"recipient_id"`
	EvidenceID   string `json:"evidence_id"`
	KnowledgeID  string `json:"knowledge_id"`
	RevelationID string `json:"revelation_id"`
	TestimonyRef string `json:"testimony_ref,omitempty"`
}

// KnowledgeReceipt is the unregistered aggregate ObjectStore artifact.
type KnowledgeReceipt struct {
	TurnID     string                  `json:"turn_id"`
	DecisionID string                  `json:"decision_id,omitempty"`
	Status     KnowledgeReceiptStatus  `json:"status"`
	Entries    []KnowledgeReceiptEntry `json:"entries"`
}

// Validate enforces canonical order and one logical entry per recipient/evidence pair.
func (r *KnowledgeReceipt) Validate() error {
	if r == nil {
		return errors.New("knowledge receipt is nil")
	}
	if err := vocabulary.ValidateIDSegment(r.TurnID); err != nil {
		return fmt.Errorf("turn_id: %w", err)
	}
	if len(r.Entries) > MaxKnowledgeReceiptEntries {
		return fmt.Errorf("knowledge receipt has %d entries; limit is %d", len(r.Entries), MaxKnowledgeReceiptEntries)
	}
	switch r.Status {
	case KnowledgeCommitted:
		if r.DecisionID == "" {
			return errors.New("committed knowledge receipt requires decision_id")
		}
	case KnowledgeNotApplicable:
		if r.DecisionID != "" || len(r.Entries) != 0 {
			return errors.New("not-applicable knowledge receipt forbids decision_id and entries")
		}
	default:
		return fmt.Errorf("unknown knowledge receipt status %q", r.Status)
	}
	previous := ""
	for index, entry := range r.Entries {
		if err := entry.validate(); err != nil {
			return fmt.Errorf("entry %d: %w", index, err)
		}
		key := entry.RecipientID + "\x00" + entry.EvidenceID
		if key <= previous {
			return errors.New("knowledge receipt entries must be sorted and unique by recipient/evidence")
		}
		previous = key
	}
	return nil
}

func (e KnowledgeReceiptEntry) validate() error {
	for name, id := range map[string]string{
		"recipient_id": e.RecipientID, "evidence_id": e.EvidenceID,
		"knowledge_id": e.KnowledgeID, "revelation_id": e.RevelationID,
	} {
		if err := types.ValidateEntityID(id); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if e.TestimonyRef != "" {
		if _, err := ParseRef(e.TestimonyRef); err != nil {
			return fmt.Errorf("testimony_ref: %w", err)
		}
	}
	return nil
}

// SortedKnowledgeEntries returns a canonical copy suitable for a receipt.
func SortedKnowledgeEntries(entries []KnowledgeReceiptEntry) []KnowledgeReceiptEntry {
	out := slices.Clone(entries)
	slices.SortFunc(out, func(a, b KnowledgeReceiptEntry) int {
		if n := strings.Compare(a.RecipientID, b.RecipientID); n != 0 {
			return n
		}
		return strings.Compare(a.EvidenceID, b.EvidenceID)
	})
	return out
}

// KnowledgeReceiptID is the specified framed tuple identity.
func KnowledgeReceiptID(turnID, decisionID string) string {
	return framedID(knowledgeReceiptIdentityDomain, turnID, decisionID)
}

// Testimony is private attributed speech/belief state. It contains no truth field.
type Testimony struct {
	TurnID        string                  `json:"turn_id"`
	DecisionID    string                  `json:"decision_id"`
	BeliefID      string                  `json:"belief_id"`
	SourceActorID string                  `json:"source_actor_id"`
	RecipientID   string                  `json:"recipient_id"`
	EvidenceID    string                  `json:"evidence_id"`
	Stance        vocabulary.BeliefStance `json:"stance"`
	Prose         string                  `json:"prose"`
}

// Validate holds testimony to attribution without interpreting its prose.
func (t *Testimony) Validate() error {
	if t == nil {
		return errors.New("testimony is nil")
	}
	if err := vocabulary.ValidateIDSegment(t.TurnID); err != nil {
		return fmt.Errorf("turn_id: %w", err)
	}
	if t.DecisionID == "" {
		return errors.New("testimony requires decision_id")
	}
	for name, id := range map[string]string{
		"belief_id": t.BeliefID, "source_actor_id": t.SourceActorID,
		"recipient_id": t.RecipientID, "evidence_id": t.EvidenceID,
	} {
		if err := types.ValidateEntityID(id); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if !t.Stance.Valid() {
		return fmt.Errorf("testimony stance %q is not closed", t.Stance)
	}
	if t.Prose == "" || len(t.Prose) > MaxTestimonyProseBytes {
		return fmt.Errorf("testimony prose must contain 1..%d bytes", MaxTestimonyProseBytes)
	}
	return nil
}

// TestimonyID is the specified framed tuple identity.
func TestimonyID(turnID, decisionID, beliefID, recipientID, evidenceID string) string {
	return framedID(testimonyIdentityDomain, turnID, decisionID, beliefID, recipientID, evidenceID)
}

func framedID(parts ...string) string {
	hash := sha256.New()
	var size [4]byte
	for _, part := range parts {
		binary.BigEndian.PutUint32(size[:], uint32(len(part)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Ensure these artifacts remain entity-only and never enter the payload registry.
var _ interface{ Validate() error } = (*KnowledgeReceipt)(nil)
var _ interface{ Validate() error } = (*Testimony)(nil)
