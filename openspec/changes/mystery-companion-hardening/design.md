# Design — Mystery Companion Hardening

## Context

The archived acceptance intentionally stops at the guarantees the current SemStreams release can
support. Package validation, import classification, world-rule checks, and effect gates protect
canonical truth locally. Hint progression is deterministic and bounded. Sequential companion
redelivery converges on one logical task and resolution. The remaining guarantees cross substrate
atomicity or revision boundaries and therefore stay blocked upstream.

## Goals / Non-Goals

**Goals:**

- Reject canonical truth mutation through graph-ingest and operator write surfaces.
- Reset the hint ladder once, and only once, for a newly committed companion knowledge revision.
- Reuse one durable initial provider request across every companion-task crash window.

**Non-Goals:**

- Change the accepted case, companion, projection, narration, or player protocol contracts.
- Build local CAS, revision, or request-publication substitutes.
- Expand active-active or multi-world support beyond the linked guarantees.

## Decisions

### D1 — Canonical truth protection closes at the substrate write boundary

SemMachina will continue to register canonical solution and truth-status predicates as protected.
Once #818 exposes the required enforcement point, graph-ingest and authorized operator mutation
paths will reject changes to resident protected values. Tests will prove the original authored
value remains resident after each rejected write class.

### D2 — Knowledge reset is revision-driven and conditional

The reset trigger will carry or resolve an authoritative knowledge revision. The bond update will
use an expected-revision condition so a newly committed grant resets once while a duplicate,
rejected grant, stale delivery, or another actor's grant does not. Process-local locking remains a
coordination aid, not the authority.

### D3 — Provider dispatch identity is claimed before publication

Companion task handling will durably bind `TaskID → LoopID → initial RequestID` before publishing
the initial provider request. Recovery will reuse that binding, and request publication will be
idempotent. The proof will cover every crash boundary between task delivery, identity claim,
request publication, response processing, and resolution commit.

## Risks / Trade-offs

- Upstream APIs may differ from the issue sketches; update this design before implementation if
  their released contracts change the authority boundary.
- A dependency bump may affect unrelated SemStreams integration; run the complete repository gate.
- Provider-side behavior beyond an idempotently published request remains outside SemMachina's
  authority unless the provider offers and honors its own idempotency contract.

## Migration Plan

1. Wait for each linked issue to close in a released SemStreams tag and update the pinned module.
2. Add failing domain and integration tests against the released primitive.
3. Implement the smallest SemMachina wiring at the existing component boundary.
4. Run architecture, code, security, race, deterministic E2E, lint, build, and strict spec gates.

## Open Questions

No local design choice can unblock implementation while #818, #851, and #807 remain open.
