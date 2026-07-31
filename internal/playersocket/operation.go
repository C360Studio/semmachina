package playersocket

import (
	"errors"
	"fmt"

	"github.com/c360studio/semmachina/internal/payload"
)

// OperationStatus is the closed outcome of a typed operation the server cannot dispatch.
type OperationStatus string

const (
	// OperationRefused is the only outcome: supported operations have their own response type.
	OperationRefused OperationStatus = "refused"
)

var operationStatuses = []OperationStatus{OperationRefused}

// OperationStatusStrings returns the player/v1 undispatchable-operation outcomes in lexical order.
func OperationStatusStrings() []string { return sortedStrings(operationStatuses) }

// OperationRefusalCode distinguishes a malformed discriminator from an unsupported operation.
type OperationRefusalCode string

const (
	// OperationMalformed means type was present but was not a string.
	OperationMalformed OperationRefusalCode = "malformed_operation"
	// OperationUnsupported means type was a string outside player/v1's operation set.
	OperationUnsupported OperationRefusalCode = "unsupported_operation"
)

var operationRefusalCodes = []OperationRefusalCode{OperationMalformed, OperationUnsupported}

// OperationRefusalCodeStrings returns the player/v1 operation refusal codes in lexical order.
func OperationRefusalCodeStrings() []string { return sortedStrings(operationRefusalCodes) }

// OperationRefusal is the typed diagnosis for an undispatchable operation.
type OperationRefusal struct {
	Code      OperationRefusalCode `json:"code"`
	Operation string               `json:"operation,omitempty"`
	Message   string               `json:"message"`
}

// OperationResponse answers a request with a present type that could not be dispatched.
type OperationResponse struct {
	Protocol payload.PlayerProtocolVersion `json:"protocol"`
	Status   OperationStatus               `json:"status"`
	Refusal  *OperationRefusal             `json:"refusal"`
}

// Validate checks the operation response is a complete player/v1 refusal.
func (r *OperationResponse) Validate() error {
	if _, err := payload.ParsePlayerProtocolVersion(string(r.Protocol)); err != nil {
		return err
	}
	if r.Status != OperationRefused {
		return fmt.Errorf("operation status %q is not %q", r.Status, OperationRefused)
	}
	if r.Refusal == nil {
		return errors.New("an operation refusal carries no diagnosis")
	}
	if r.Refusal.Code != OperationMalformed && r.Refusal.Code != OperationUnsupported {
		return fmt.Errorf("operation refusal code %q is not recognized", r.Refusal.Code)
	}
	if r.Refusal.Code == OperationMalformed && r.Refusal.Operation != "" {
		return errors.New("a malformed non-string operation cannot carry an operation name")
	}
	if r.Refusal.Code == OperationUnsupported && r.Refusal.Operation == "" {
		return errors.New("an unsupported operation refusal carries no operation name")
	}
	if r.Refusal.Message == "" {
		return errors.New("an operation refusal carries no message")
	}
	return nil
}

func operationRefused(operation string, code OperationRefusalCode, message string) *OperationResponse {
	return &OperationResponse{
		Protocol: payload.PlayerProtocolV1,
		Status:   OperationRefused,
		Refusal:  &OperationRefusal{Code: code, Operation: operation, Message: message},
	}
}
