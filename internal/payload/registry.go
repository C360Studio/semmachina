package payload

import (
	"errors"
	"fmt"

	"github.com/c360studio/semstreams/payloadregistry"
)

// RegisterPayloads registers every SemMachina turn-loop payload with the
// supplied registry. Call it at binary bootstrap, after
// payloadbuiltins.Register and before component lifecycle starts, then plumb
// the registry through component.Dependencies.PayloadRegistry.
//
// This is an explicit function rather than an init(): semstreams beta.18
// retired the payload-registry singleton, so package-level registration no
// longer exists and BaseMessage.UnmarshalJSON fails fast unless it was
// constructed by message.NewDecoder(reg). The half-wired-binary failure mode
// the init() pattern was meant to avoid is now a compile-visible bootstrap
// call: a binary that forgets this line decodes nothing and says so.
//
// Errors are aggregated so a misconfigured deployment sees every collision on
// a single boot instead of one per restart.
func RegisterPayloads(reg *payloadregistry.Registry) error {
	registrations := []*payloadregistry.Registration{
		{
			Domain: Domain, Category: CategoryPlayerAction, Version: SchemaVersion,
			Description: "Canonical transport-neutral player action",
			Factory:     func() any { return &PlayerAction{} },
		},
		{
			Domain: Domain, Category: CategoryVerdict, Version: SchemaVersion,
			Description: "Fiction adjudicator verdict with banded effect intents",
			Factory:     func() any { return &Verdict{} },
		},
		{
			Domain: Domain, Category: CategoryRollResult, Version: SchemaVersion,
			Description: "Seeded deterministic resolution result",
			Factory:     func() any { return &RollResult{} },
		},
		{
			Domain: Domain, Category: CategoryEffectBatch, Version: SchemaVersion,
			Description: "Validated effect batch for the selected outcome band",
			Factory:     func() any { return &EffectBatch{} },
		},
		{
			Domain: Domain, Category: CategoryTurnManifest, Version: SchemaVersion,
			Description: "Append-only campaign-ledger manifest for one resolved turn",
			Factory:     func() any { return &TurnManifest{} },
		},
		{
			Domain: Domain, Category: CategoryWorldEntity, Version: SchemaVersion,
			Description: "Materialized world entity from a template package (Graphable)",
			Factory:     func() any { return &WorldEntity{} },
		},
	}

	var errs []error
	for _, r := range registrations {
		if err := reg.Register(r); err != nil {
			errs = append(errs, fmt.Errorf("register %s: %w", r.MessageType(), err))
		}
	}
	return errors.Join(errs...)
}
