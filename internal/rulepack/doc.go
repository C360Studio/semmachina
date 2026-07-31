// Package rulepack is the deciding half of the turn loop.
//
// It owns the turn-sequencing rule pack: the JSON that watches a turn entity in
// ENTITY_STATES and publishes a stage trigger when the turn is ready for its
// next hop. The pack is DATA by project rule — the Go here embeds it, decodes
// it, and refuses it when it disagrees with the engine's own vocabularies, but
// never reimplements it.
//
// The split with internal/stage is the house rule: rules trigger, components
// execute. Nothing in this package writes a phase, reads a verdict, or carries
// content; a rule publishes a turn entity ID onto a stage subject and stops.
//
// # Phases sequence, artifacts gate
//
// Every phase except accepted is written on stage ENTRY, so a mid-chain rule
// matching the phase alone fires as the previous stage STARTS and races it for
// the artifact it needs — a defect that passes every test and appears under
// load. Check therefore enforces at load time that a mid-chain rule also matches
// the artifact its predecessor produces. The two exemptions are principled: the
// first hop, because accepted is written by intake's atomic create and is a
// finished fact rather than an entry marker, and transition conditions, which
// fire on the phase move rather than on its presence.
//
// # A rule that loads is not a rule that will fire
//
// Upstream validation rejects a condition field or action predicate that is not
// registered (vocabulary.RegisterPredicates) and any condition naming a
// rule-opaque predicate, which is how "no rule branches on fiction" becomes a
// load failure instead of a review convention. Callers register the engine's
// predicates before asking for Definitions.
package rulepack
