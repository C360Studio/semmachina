package playersocket

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/c360studio/semmachina/internal/payload"
)

// RequestType discriminates WebSocket request documents that are not legacy
// bare SubmitAction documents. SubmitAction remains bare for player/v1
// compatibility; every additional operation must carry an explicit type.
type RequestType string

const (
	// RequestRetrieve asks the authenticated session for one durable result.
	RequestRetrieve RequestType = "retrieve_result"
)

var requestTypes = []RequestType{RequestRetrieve}

// RequestTypeStrings returns the player/v1 operation discriminators in lexical order.
func RequestTypeStrings() []string { return sortedStrings(requestTypes) }

// RetrieveBy is the closed set of durable result lookup keys.
type RetrieveBy string

// The supported result lookup keys.
const (
	RetrieveByTurn   RetrieveBy = "turn"
	RetrieveByAction RetrieveBy = "action"
	RetrieveLatest   RetrieveBy = "latest"
)

var retrieveBys = []RetrieveBy{RetrieveByTurn, RetrieveByAction, RetrieveLatest}

// RetrieveByStrings returns the player/v1 retrieval keys in lexical order.
func RetrieveByStrings() []string { return sortedStrings(retrieveBys) }

// RetrieveRequest is a transport request for a result the authenticated player
// owns. It never accepts a player id: Latest derives identity from the session,
// and named lookups authorize the turn's ownership scalar against that same
// identity before any private artifact is composed into a delivery.
type RetrieveRequest struct {
	Protocol payload.PlayerProtocolVersion `json:"protocol"`
	Type     RequestType                   `json:"type"`
	By       RetrieveBy                    `json:"by"`
	ID       string                        `json:"id,omitempty"`
}

// Validate checks the request is unambiguous and fully names its lookup.
func (r *RetrieveRequest) Validate() error {
	if _, err := payload.ParsePlayerProtocolVersion(string(r.Protocol)); err != nil {
		return err
	}
	if r.Type != RequestRetrieve {
		return fmt.Errorf("request type %q is not %q", r.Type, RequestRetrieve)
	}
	if !slices.Contains(retrieveBys, r.By) {
		return fmt.Errorf("retrieval key %q is not one of %v", r.By, retrieveBys)
	}
	switch r.By {
	case RetrieveLatest:
		if r.ID != "" {
			return errors.New("a latest retrieval derives the player from the authenticated session and carries no id")
		}
	case RetrieveByTurn:
		if _, err := payload.ActionIDForTurn(r.ID); err != nil {
			return fmt.Errorf("id: %w", err)
		}
	case RetrieveByAction:
		if err := payload.RequireActionID(r.ID); err != nil {
			return fmt.Errorf("id: %w", err)
		}
	}
	return nil
}

// decodeRetrieveRequest is strict because requests point inward: accepting an
// unknown field would teach a client that a misspelled lookup constraint worked.
func decodeRetrieveRequest(raw []byte) (*RetrieveRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request RetrieveRequest
	if err := decoder.Decode(&request); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("a retrieval frame carries more than one JSON document")
		}
		return nil, err
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return &request, nil
}

// inspectRequestType distinguishes absence (the legacy bare SubmitAction) from a
// present discriminator this version does not understand. That distinction is
// why an unknown operation is never reported as a malformed submission.
func inspectRequestType(raw []byte) (RequestType, bool, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return "", false, err
	}
	encoded, present := document["type"]
	if !present {
		return "", false, nil
	}
	token := bytes.TrimSpace(encoded)
	if len(token) == 0 || token[0] != '"' {
		return "", true, errors.New("the operation type must be a JSON string")
	}
	var operation RequestType
	if err := json.Unmarshal(token, &operation); err != nil {
		return "", true, err
	}
	if operation == "" {
		return "", true, errors.New("the operation type must be a non-empty JSON string")
	}
	return operation, true, nil
}

// RetrieveStatus is the two-valued answer to one result retrieval.
type RetrieveStatus string

// The closed retrieval outcomes.
const (
	RetrieveFound   RetrieveStatus = "found"
	RetrieveRefused RetrieveStatus = "refused"
)

var retrieveStatuses = []RetrieveStatus{RetrieveFound, RetrieveRefused}

// RetrieveStatusStrings returns the player/v1 retrieval outcomes in lexical order.
func RetrieveStatusStrings() []string { return sortedStrings(retrieveStatuses) }

// RetrieveRefusalCode gives each player-remediable outcome a stable shape.
type RetrieveRefusalCode string

// The closed retrieval refusal reasons.
const (
	RetrieveMalformed   RetrieveRefusalCode = "malformed_request"
	RetrieveNotFound    RetrieveRefusalCode = "not_found"
	RetrieveNotReady    RetrieveRefusalCode = "not_ready"
	RetrieveUnavailable RetrieveRefusalCode = "unavailable"
)

var retrieveRefusalCodes = []RetrieveRefusalCode{
	RetrieveMalformed, RetrieveNotFound, RetrieveNotReady, RetrieveUnavailable,
}

// RetrieveRefusalCodeStrings returns the player/v1 retrieval refusal codes in lexical order.
func RetrieveRefusalCodeStrings() []string { return sortedStrings(retrieveRefusalCodes) }

// RetrieveRefusal explains why no result was returned.
type RetrieveRefusal struct {
	Code    RetrieveRefusalCode `json:"code"`
	Message string              `json:"message"`
}

// Validate checks the refusal states one recognized, actionable outcome.
func (r *RetrieveRefusal) Validate() error {
	if r == nil {
		return errors.New("a refused retrieval carries no refusal")
	}
	if !slices.Contains(retrieveRefusalCodes, r.Code) {
		return fmt.Errorf("retrieval refusal code %q is not one of %v", r.Code, retrieveRefusalCodes)
	}
	if r.Message == "" {
		return errors.New("a retrieval refusal carries no message")
	}
	return nil
}

// RetrieveResponse answers one retrieval and echoes its lookup shape. The
// authenticated player id is deliberately absent from both request and answer.
type RetrieveResponse struct {
	Protocol payload.PlayerProtocolVersion `json:"protocol"`
	Status   RetrieveStatus                `json:"status"`
	By       RetrieveBy                    `json:"by"`
	ID       string                        `json:"id,omitempty"`
	Delivery *payload.TurnDelivery         `json:"delivery,omitempty"`
	Refusal  *RetrieveRefusal              `json:"refusal,omitempty"`
}

// Validate checks the response states exactly one complete outcome.
func (r *RetrieveResponse) Validate() error {
	if _, err := payload.ParsePlayerProtocolVersion(string(r.Protocol)); err != nil {
		return err
	}
	if !slices.Contains(retrieveBys, r.By) {
		return fmt.Errorf("retrieval key %q is not one of %v", r.By, retrieveBys)
	}
	if r.By == RetrieveLatest && r.ID != "" {
		return errors.New("a latest retrieval response carries no requested id")
	}
	if r.By != RetrieveLatest && r.ID == "" {
		return errors.New("a named retrieval response carries no requested id")
	}
	switch r.By {
	case RetrieveByTurn:
		if _, err := payload.ActionIDForTurn(r.ID); err != nil {
			return fmt.Errorf("id: %w", err)
		}
	case RetrieveByAction:
		if err := payload.RequireActionID(r.ID); err != nil {
			return fmt.Errorf("id: %w", err)
		}
	}
	switch r.Status {
	case RetrieveFound:
		if r.Delivery == nil || r.Refusal != nil {
			return errors.New("a found retrieval carries exactly one delivery and no refusal")
		}
		if err := r.Delivery.Validate(); err != nil {
			return err
		}
		if r.By == RetrieveByTurn && r.Delivery.Result.TurnID != r.ID {
			return fmt.Errorf("turn lookup %q returned turn %q", r.ID, r.Delivery.Result.TurnID)
		}
		if r.By == RetrieveByAction && r.Delivery.Result.ActionID != r.ID {
			return fmt.Errorf("action lookup %q returned action %q", r.ID, r.Delivery.Result.ActionID)
		}
		return nil
	case RetrieveRefused:
		if r.Delivery != nil {
			return errors.New("a refused retrieval also carries a delivery")
		}
		return r.Refusal.Validate()
	default:
		return fmt.Errorf("retrieval status %q is neither %q nor %q", r.Status, RetrieveFound, RetrieveRefused)
	}
}

func sortedStrings[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	slices.Sort(out)
	return out
}

func retrievalFound(request *RetrieveRequest, delivery *payload.TurnDelivery) *RetrieveResponse {
	return &RetrieveResponse{
		Protocol: payload.PlayerProtocolV1, Status: RetrieveFound,
		By: request.By, ID: request.ID, Delivery: delivery,
	}
}

func retrievalRefused(by RetrieveBy, id string, code RetrieveRefusalCode, message string) *RetrieveResponse {
	if !slices.Contains(retrieveBys, by) {
		by = RetrieveLatest
		id = ""
	}
	return &RetrieveResponse{
		Protocol: payload.PlayerProtocolV1, Status: RetrieveRefused, By: by, ID: id,
		Refusal: &RetrieveRefusal{Code: code, Message: message},
	}
}
