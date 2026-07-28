---
name: orchestration-check
description: Determine whether logic belongs in a reactive rule, lifecycle workflow, component, or agentic persona. Use when adding orchestration logic, designing multi-step processes, or reviewing boundary violations.
argument-hint: [pattern or logic being evaluated]
---

# Orchestration Layer Check (SemMachina)

## What pattern are you evaluating?

$ARGUMENTS

## The house design rule

**Agentic judges fiction, rules match structure, components execute work.** The LLM is the only layer
allowed to read fiction, and it exits through structured triples (closed vocabulary, rule-matchable).
If your "rule" needs to understand narrative, it is persona work; if your "persona" is doing deterministic
bookkeeping, it is a rule or component.

## The Layers

| Layer | Responsibility | Owns | Does NOT Own |
|-------|---------------|------|--------------|
| **Rule engine** | State detection, triggers, bounded action sequencing | Conditions, actions, match state, iteration caps | Actual work execution or semantic judgment |
| **Lifecycle harness** | Declared phase discipline for named entities | Phase graph, transition validation, operator-writable state contract | Work execution or hidden private storage |
| **Component** | Execute single units of work | Execution mechanics, internal state, output emission | Workflow awareness, caller context |
| **Agentic persona** | Judgment over unstructured narrative | Reading fiction, emitting structured verdict/decision/delta triples | Deterministic sequencing, direct state writes |

## Quick Decision

| Pattern | Use |
|---------|-----|
| Verdict class X lands --> fire dice component | Single-trigger reactive rule |
| Turn sequencing (action -> adjudicate -> roll -> narrate) | Rule chain, one rule per transition |
| Combat rounds / NPC exchanges (bounded loop) | Coordinated rule set + `MaxIterations` over a lifecycle entity |
| "Is this proposed action plausible?" | Agentic persona (adjudicator) exiting via verdict triple |
| Roll dice, assemble context, write prose to ObjectStore | Component |
| Campaign / scene / story-arc with phases and resume | Lifecycle `Participant` (ADR-049 upstream; supersedes 047 — phases are graph triples) |

## The 5 Rules

1. **Rules trigger, they don't execute work** -- A rule fires bounded actions, not business logic.
   - Anti-pattern: Rule A sets `step=1`, Rule B watches `step=1` and sets `step=2`...
   - Fix: Put durable progress on a lifecycle-managed entity (the scene) and let rules fire components.

2. **Lifecycle coordinates entity phase, it doesn't execute** -- Lifecycle validates transitions; components
   do work.

3. **Components are workflow-agnostic** -- The dice component doesn't know whether combat or a skill check
   called it; behavior differences arrive as configuration, not caller identity.

4. **State ownership is exclusive** -- Only one layer owns any piece of state.

   | State | Owner |
   |-------|-------|
   | Trigger conditions | Rules |
   | Rule match/iteration counters | Rule engine |
   | Campaign/scene/arc phase | Lifecycle-managed graph entity |
   | Persona execution state (pending tools, loop phase) | Agentic loop component (upstream) |
   | World entities (character/item/location) | Knowledge graph (`ENTITY_STATES`) |

5. **If it has operator-visible phase/progress, model it explicitly** -- Simple handoffs use rules; durable
   multi-step progress (a scene, an encounter) uses lifecycle-managed entities. The operator-writable patch
   contract is the human-GM override surface.

## Anti-Patterns

- Rule chains that build up ad hoc step state instead of using a lifecycle entity
- A rule condition matching on prose or open-ended LLM strings (M1/M2 violation — persona work)
- Components checking workflow context to decide behavior (should be caller-agnostic)
- Rule payloads carrying content — rules pass references (loop IDs, entity IDs, storage refs), never prose
- An LLM-triggering path without an iteration cap, or a cap without an exhaust action (M5)

## State Storage Boundaries

| Category | Storage | In Knowledge Graph? |
|----------|---------|---------------------|
| World and lifecycle entities | `ENTITY_STATES` KV | Yes (Graphable) |
| Operational execution artifacts (loop state, trajectories) | Component-specific KV / ObjectStore | No, except ref triples |
| Player actions / work items | JetStream streams | No |
| Prose (narration, vignettes) | ObjectStore | Ref-triples only |

Do NOT write opaque execution artifacts to `ENTITY_STATES` -- it pollutes the world graph.

Read `~/Code/c360/semstreams/docs/concepts/14-orchestration-layers.md` for the full pattern catalog and
`~/Code/c360/semstreams/docs/concepts/25-phased-agentic-chains.md` for multi-step agentic workflows.
