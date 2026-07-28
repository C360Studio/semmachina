// Package payload holds SemMachina's five turn-loop message types:
// PlayerAction, Verdict, RollResult, EffectBatch, and TurnManifest.
//
// Every type implements message.Payload (Schema, Validate, MarshalJSON,
// UnmarshalJSON) and is registered through RegisterPayloads so the production
// decoder (message.NewDecoder) can reconstruct it from the wire. The
// BaseMessage envelope is added by the publisher, never by a payload's own
// MarshalJSON; the alias indirection in each MarshalJSON exists only to avoid
// infinite recursion.
//
// Validate is where the closed vocabulary becomes load-bearing. Decoding does
// NOT validate — BaseMessage.UnmarshalJSON reconstructs whatever the wire
// says — so a component that decodes a payload and skips Validate has skipped
// the vocabulary gate entirely. Every rule-matched field is checked against
// internal/vocabulary here, so the adjudicator tool boundary, the effect
// applier, and the rule pack all enforce one definition.
//
// The bulky-versus-rule-matched split runs through the whole package. A
// Verdict's four scalars (plausibility, risk, consequence, requires_roll) are
// the only part that ever becomes triples — see Verdict.Triples. The banded
// effect-intent set is bulky and is consumed only by the applier, so it
// travels by reference, following the upstream decide-executor precedent
// (small rule-matchable metadata triples on the entity, bulky result content
// fetched by reference).
package payload
