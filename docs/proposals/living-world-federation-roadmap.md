# Living-World Federation Roadmap

**Status:** Hypothetical post-freeze roadmap; no implementation commitment, delivery date, or upstream
SemStreams commitment is made by this document.

**Date:** 2026-08-05.

## Thesis

SemMachina could evolve from one process per world into one logical world executed by multiple zone
authorities. Federation would not mean copying the whole graph everywhere, and a zone would not be a
separate save or an NPC container. A zone would be a geographic partition carrying a renewable lease to
write the entities currently present there.

Stable identities, durable history, relationships, and narrative continuity would survive movement between
zones. NPCs would travel because schedules, needs, relationships, danger, trade, and player actions make them
travel. Runtime authority would follow that fictional movement; infrastructure would not teleport fictional
people merely to flatten utilization.

This is a possible extension of the [founding proposal](fiction-first-rpg.md), not a replacement for its MVP
boundary. The current product still supports one active world and experience per process and broker. The
[world clock and NPC-cognition stages](../../openspec/project.md) and the explicit living-world migration
rules in the [world-authoring guide](../guides/world-authoring.md) remain prerequisites.

## Scope during code freeze

This document records a direction to test after the code freeze. It does not authorize code changes, a new
SemStreams primitive, a hardware purchase, or a production topology. In particular, it does not claim that:

- SemStreams federation already supplies authority, causality, or entity handoff;
- NATS leaf nodes or gateways automatically create an ad hoc simulation mesh;
- the six-part entity ID is by itself a distributed ordering or ownership protocol;
- local LLM capacity has been measured at the population targets below; or
- seamless travel, dynamic zone splitting, or cross-world commerce is ready.

The first proof should use static zones and deterministic fault injection. Dynamic repartitioning belongs
after measured evidence shows that static authority partitions and cognition degradation are insufficient.

## Invariants

### Zones partition authority, not identity

A zone is the current geographic home of write authority for place-bound entities. It is not an ownership
namespace for the people inside it. An NPC may leave any zone permitted by world rules without being
recreated, forked, or assigned a new identity.

Entity identity is stable across location changes, process restarts, zone handoffs, and deployment topology
changes. Authority location and authority epoch are routing metadata, not part of identity. Every transferred
fact retains its source and provenance.

The first proof should keep whole-entity authority. Splitting different mutable portions of one NPC across
authorities would multiply consistency edges and is deferred until a measured use case requires it.

### One writer owns authoritative state

At any instant, an entity has one writable authority epoch. Foreign copies are projections only. An old
authority cannot resume writing after a newer epoch commits. During a network partition, stale read-only
projections may remain visible until expiry, but conflicting authoritative writes are not merged later.

Delivery may be at least once. Effects must therefore be idempotent and fenced by operation identity,
authority epoch, and expected entity revision. This roadmap does not use "exactly once" as a promise.

### Zones are not NPC cages

Geography determines where an NPC is simulated; it does not constrain whom the NPC knows, owes, fears,
trades with, or may visit. Cross-zone relationships remain stable graph references. Relevant remote state
arrives through bounded, typed federation lanes rather than by copying the entire remote world.

Home, kinship, employment, faction, property, debt, market access, danger, and travel cost are **soft
anchors**. They bias movement and return behavior, but they are world facts and decision inputs rather than
infrastructure placement locks. Population concentration is an expected simulation outcome, not a routing
defect.

### Cognition proposes; authority commits

An LLM cognition worker may run anywhere, but it owns no world state. It returns a structured proposal that
names the NPC revision and authority epoch it observed. The current zone authority validates and commits or
rejects that proposal. A response arriving after the NPC moves or changes is stale by construction.

Recovery and historical replay consume committed world events and stored NPC decisions. They do not silently
rerun an LLM and invent a different past. No LLM call may run inside the authority-handoff critical section.

### Replication is bounded by interest

Zones exchange the smallest state required for perception, coordination, and handoff. High-rate local detail
stays local. Neighboring, socially relevant, or globally salient state is projected at an explicit fidelity
and expiry. Federation must not replicate an unbounded event ledger or full graph to every zone.

## Three federation lanes

These are semantic lanes. A proof may carry them over one transport, but their permissions and failure
semantics remain distinct.

### Projection lane: what may this zone know?

The projection lane carries bounded, read-only summaries for perception, maps, relationships, rumors,
weather, prices, hazards, and later federated legends. It is eventually consistent. Each state class declares
its expiry and merge policy; a received projection never grants write authority.

### Interaction lane: which authority must decide?

The interaction lane carries addressed actions, reactions, questions, and committed outcomes. Messages are
durable and may be redelivered. The current owner validates operation identity, epoch, expected revision,
permissions, and world rules before producing an effect.

Cross-zone dialogue can use this lane. A tightly coupled combat or scene should instead acquire one scene
coordinator or co-locate its participants; every turn must not become a distributed transaction.

### Authority lane: who may write next?

The authority lane transfers the right to write an entity or declared transfer group. It uses a fenced
prepare/commit protocol and is never resolved by last-writer-wins or CRDT merge. NPC and player travel use
this lane. Cross-world traveler import might reuse its shape later, but would require a stricter trust policy.

Keeping the lanes separate prevents an observed remote fact or delivered command from accidentally becoming
write authority.

## SemLink as a pattern donor

SemLink is useful prior art, not a game protocol or proof of SemMachina capacity. Its
[companion-mesh ADR](../../../semlink/docs/adr/003-companion-mesh-product-boundary.md) records the central
lesson: a stable ID, local graph revision, and network transport are not distributed causality. Its
[mesh perspective matrix](../../../semlink/openspec/changes/archive/2026-07-12-add-mesh-perspective-matrix/design.md)
also demonstrates a good proof shape: exact origins, bounded deltas, duplicate delivery, line topology,
partition healing, and late joiners.

SemMachina should borrow these ideas:

- exchange selected deltas and compact watermarks rather than full graphs;
- key catch-up by origin, entity, and state class;
- declare expiry and merge semantics per state class;
- keep raw or high-rate local detail local by default;
- never interpret command receipt as execution permission; and
- assert exact visible origins so count-only convergence cannot hide identity errors.

A candidate game envelope is:

```text
world_id
origin_zone_id
entity_id
state_class
schema_version
authority_epoch
origin_sequence or logical_time
operation_id
observed_at
expires_at
merge_policy
payload_hash
provenance
```

The exact ordering primitive remains open. A local KV revision cannot be promoted to a cross-zone causal
clock without an explicit substrate contract.

## Fenced authority handoff

Handoff is an ownership change, not eventual replication:

1. The source emits `prepare` with a transfer ID, entity revision, current epoch, state, pending timers,
   durable event cursor, and provenance.
2. The destination validates schema, identity, destination rules, and capacity, then stages a read-only copy.
3. The destination acknowledges readiness without becoming writable.
4. The authority directory atomically changes `{zone, epoch}` with compare-and-set.
5. The source stops committing, then drains or forwards late work.
6. The destination activates rules, schedules, and cognition as the sole writer.
7. Both sides record one durable completion result keyed by the transfer ID.

Before the directory commit, timeout may abort safely. After commit, recovery completes forward and never
restores the old writer. Duplicate prepare and commit messages are idempotent. Border hysteresis prevents
rapid back-and-forth handoffs. Parties, vehicles, or tightly coupled scenes transfer as a declared group.

A cognition result carries the entity revision and authority epoch from dispatch. Either mismatch rejects the
result before graph mutation. This is the required test for an LLM response that returns during or after
handoff.

## Emergent mobility and uneven worlds

A living world is allowed to become uneven. Trade, safety, war, pilgrimage, employment, and relationships can
draw NPCs into one place. The first operational responses are interest filtering, cognition level of detail,
priority queues, and additional compute. Dynamic zone splitting is later machinery, triggered by measured
pressure; it must not rewrite fictional geography or character motives.

Use three different terms in later designs:

- **character migration** is fictional travel caused by the simulation;
- **authority handoff** is runtime ownership following that travel; and
- **zone relocation or split** is operational movement of compute or boundaries.

Ambient inhabitants remain deterministic state machines or aggregate cohorts. Named and scheduled NPCs have
durable state and wake on schedules or salient events. Only a bounded subset is LLM-capable, and human
proximity can temporarily raise its cognition tier.

## Proof ladder after the freeze

1. **Authority semantics:** run two static zones with stable IDs and an explicit authority directory, without
   network federation.
2. **Projection lane:** connect isolated runtimes through a deterministic harness that injects duplicates,
   reordering, partition/heal, line topology, and late joiners.
3. **Single-entity travel:** move one NPC across an explicit boundary and inject a crash at every handoff step.
4. **Cognition fencing:** return a mock LLM proposal before, during, and after movement; only the current
   revision and epoch may commit.
5. **Human encounter:** meet the migrated NPC in a player scene with beliefs, inventory, history, pending
   goals, and provenance intact.
6. **Group travel and hotspots:** test parties, convoys, border hysteresis, a crowded destination, and bounded
   cognition degradation.
7. **Three-zone topology:** prove full-mesh and line-topology catch-up with exact origin visibility.
8. **Measured capacity trial:** run real model services and record spend, queueing, handoff, recovery, memory,
   and power evidence.
9. **Dynamic split or relocation:** proceed only if the measured trial demonstrates a need.
10. **Cross-world federation:** start with read-only legends; defer traveler import, commerce, and mutual
    trust.

## Capacity-planning assumptions — 2026-08-05

Everything in this section is an assumptions register and initial benchmark target, **not a capacity claim**.
Prices, model artifacts, inference runtimes, and store configurations must be rechecked at purchase and test
time.

### Candidate local hosts

The current Mac purchase target is a **Mac mini M4 Pro with 48GB unified memory**. Apple's live
[Mac mini store](https://www.apple.com/shop/buy-mac/mac-mini) lists the 48GB/1TB configuration at **$2,499**
on 2026-08-05. Use roughly **$2.3k-$2.5k** as a configuration band only if a live 512GB quote is available;
otherwise use the cited $2,499 configuration. Although Apple's 2024
[support specification](https://support.apple.com/en-us/121555) describes a 64GB option, the current store
configuration is the procurement authority for this roadmap and the target remains 48GB.

The larger candidate is a **Ryzen AI Max+ 395 system with 128GB unified memory**, referred to below as Halo.
AMD specifies 128GB maximum memory and a 45-120W cTDP range on the
[processor page](https://www.amd.com/en/products/processors/laptop/ryzen/ai-300-series/amd-ryzen-ai-max-plus-395.html).
AMD's branded
[Ryzen AI Halo platform](https://www.amd.com/en/products/processors/desktops/ryzen/ryzen-ai-halo.html) lists a
$3,999 retail comparison price. Other 128GB systems make a volatile rough market band of
**$2.3k-$4k** plausible, but current examples such as
[Minisforum](https://store.minisforum.com/products/minisforum-ms-s1-max-mini-pc) and
[Corsair](https://www.corsair.com/us/en/p/gaming-computers/cs-9080003-na/) sit toward the upper end. Requote
the exact system rather than treating the lower bound as available inventory.

The electricity assumption is **80-120W average** for an always-on Halo-class host, not a vendor guarantee.
Thirty 24-hour days imply 58-86kWh per month, or about **$9-$13/month at $0.15/kWh**. Measure wall power for
idle, mixed simulation, sustained decode, and recovery runs; do not substitute cTDP for measured system draw.

### Model tiers and Q4 memory

The proposed service tiers are:

| Tier | Model class | Role |
|---|---|---|
| Ambient | Deterministic rules or aggregates | Background population |
| Decision | 4B Q4 dense | Structured NPC choices |
| Planner | 12-14B Q4 dense | On-screen plans and narration |
| Planner MoE | 30-35B A3B Q4 | Higher-quality planning or narration |
| Offline | 70B Q4 dense | Halo-only replay, evaluation, or writer work |

These are shared capability services, not one model per NPC or zone. NPC memory and world facts remain in
the graph; each request retrieves a bounded working set. One host could initially route decision work to
[Qwen3-4B-Instruct-2507](https://huggingface.co/Qwen/Qwen3-4B-Instruct-2507), dense planning to
[Phi-4 14B](https://huggingface.co/microsoft/phi-4), or sparse planning to
[Qwen3-30B-A3B-Instruct-2507](https://huggingface.co/Qwen/Qwen3-30B-A3B-Instruct-2507). These are benchmark
candidates, not product selections; exact model, quantization, context, grammar, and runtime versions are
part of the evidence.

Q4 means approximately four bits per weight before quantization metadata. The ideal packed-weight floor is
therefore about `parameter_count / 2` bytes, but real service memory also includes scales, metadata, runtime
buffers, tokenizer state, graph/context assembly, and KV caches. Initial working-set reservations are:

| Model class | Ideal Q4 weights | Initial service reservation |
|---|---:|---:|
| 4B dense | ~2GB | 3-5GB |
| 12-14B dense | ~6-7GB | 9-14GB |
| 30-35B A3B MoE | ~15-17.5GB | 20-30GB |
| 70B dense | ~35GB | 45-65GB |

These are budgeting ranges, not measured footprints. Four-bit loading reduces weight memory roughly as
described in the [Hugging Face quantization documentation](https://huggingface.co/docs/bitsandbytes/index),
but artifact format and backend matter.

For a mixture-of-experts model, **total parameters determine the resident weight memory** while activated
parameters influence per-token compute. Qwen's official
[30B-A3B model card](https://huggingface.co/Qwen/Qwen3-30B-A3B) illustrates the distinction with 30.5B total
and 3.3B activated parameters. "A3B" does not make the model occupy 3B-model memory.

On the 48GB Mac, the 4B decision service plus one 12-14B planner is the conservative resident target. A
30-35B A3B planner is a measured alternative, not an assumed co-resident addition. The 70B tier is Halo-only
and offline; it is not admitted to the continuous player or NPC loop.

### Service time and offered load

For model tier `m`, estimate uncached service time as:

```text
S_m = L_fixed,m + I_m / R_prefill,m + O_m / R_decode,m
```

`I_m` and `O_m` are input and output tokens. `R_prefill,m` and `R_decode,m` are measured tokens per second,
and `L_fixed,m` covers request, scheduling, and tool overhead excluding queue time. Initial planning gives
**no prompt-cache credit**: every request pays its full input prefill. Cache reuse may be measured later as
upside, never required to pass the first gate.

Estimate the model-tier arrival rate as:

```text
lambda_m = P_active * r_turn * k_turn,m
         + N_scheduled * r_tick * k_tick,m
         + lambda_event * k_event,m
```

`P_active` is continuously active players, `N_scheduled` is scheduled NPCs, the `r` terms are rates, and the
`k` terms are expected calls to tier `m` per trigger. With `C_m` effective concurrent service slots:

```text
rho_m = lambda_m * S_m / C_m
```

The initial admission target is **`rho_m <= 0.55`** at every model tier. The unused 45% is burst, variance,
narrator priority, recovery, and measurement headroom. Queue depth, wait time, and service-time distributions
must still be measured; average utilization alone cannot prove an interactive experience.

The first planning fixture assumes one human turn every three minutes. Each player turn requests one 4B
adjudication of roughly 1,200 input and 120 output tokens plus one planner/narrator call of roughly 2,500
input and 250 output tokens. An on-screen key NPC may request one 4B decision every ten minutes; an off-screen
key NPC may request one every hour. Ambient inhabitants make no routine model call. These rates are workload
definitions for comparison, not a proposed final game cadence.

### Initial benchmark targets — not claims

| Host | Static zones | Named/scheduled NPCs | LLM-capable identities | Active players | Example shape |
|---|---:|---:|---:|---:|---|
| Mac mini M4 Pro 48GB | 4 | 100-300 | 10-30 | 1-2 | Town plus three regions |
| Ryzen AI Max+ 395 128GB | 8-16 | 300-1,000 | 30-100 | Several settlements and wilds |

Ambient population can be thousands or tens of thousands because it is deterministic or aggregate and does
not imply an LLM call per person. Connected spectators, asynchronous players, and players waiting on world
events can exceed continuously active player counts. Capacity follows simultaneous turns and cognition work,
not account count or map size.

The upper player count assumes either a fast sparse planner, a lighter narrator, or a second inference
worker. If every turn uses a co-located dense 12-14B narrator under conservative throughput, plan from the
bottom of each range. Likewise, 100 LLM-capable identities do not mean 100 concurrent requests; only the
scheduled or event-woken subset consumes inference.

## Post-freeze measurement matrix

Each run must record model and quant artifact hashes, runtime/backend versions, context and output limits,
host power mode, temperature, wall power, and whether any cache was enabled.

| Run | Shape | Required evidence |
|---|---|---|
| Baseline | One zone, deterministic population | CPU, memory, mutations, timers, idle power |
| Projection | Two and three zones with faults | Bytes, watermarks, expiry, backlog, catch-up time |
| Handoff | Solo and grouped crossings | Latency, state bytes, epoch, crash recovery |
| Cognition | 4B plus planner under movement | Service time, queue wait, stale rejection, tokens |
| Hotspot | Most active identities in one zone | LOD changes, fanout, player latency, memory |
| Soak | 24-hour scheduled world | Drift, leaks, retries, power, cost, recovery |

The matrix must cover reactive and scheduled clock policies separately. A reactive world has zero idle model
work by policy; a scheduled world does not.

## Acceptance gates

The roadmap may advance only when a reproducible artifact demonstrates all applicable gates:

1. **Authority:** no dual-writer commit occurs under duplicate, reordered, delayed, or partitioned delivery.
2. **Handoff:** every injected crash point either aborts before directory commit or completes forward after it.
3. **Idempotency:** redelivery produces one authoritative effect and one durable completion identity.
4. **Cognition:** no stale epoch or entity revision reaches graph mutation.
5. **Projection:** catch-up remains bounded and never falls back to an unbounded full-world copy.
6. **Capacity:** the target mix stays at or below 55% offered utilization with at least 20% measured memory
   headroom and no unbounded queue growth.
7. **Experience:** player-priority work remains within a threshold declared before the run; the report includes
   the full latency distribution rather than only an average.
8. **Soak:** the 24-hour run shows no monotonic memory, stream, tombstone, retry, or scheduled-work leak.
9. **Honesty:** the report labels simulated, mock-model, real-model, and hardware evidence separately.

Passing a smaller host does not extrapolate to a larger population. Failing a benchmark should first adjust
event rates, interest shapes, model tiers, and LOD policy; it should not silently lower the recorded workload.

## Open design questions

- Which future SemStreams contract should own the authority directory, epoch fencing, and logical ordering?
- Does a scene temporarily own its participants, or must it route all writes to entity authorities?
- Which projected state classes use TTL, set semantics, append-limited evidence, or no merge at all?
- How do world-clock deadlines behave while a destination is unavailable?
- What trust and schema checks distinguish intra-world handoff from cross-world traveler import?
- Which measured pressure justifies splitting a geographic zone instead of scaling its cognition services?

Until those questions and the proof ladder close, living-world federation remains a deliberate hypothesis:
SemStreams may carry the messages, but SemMachina must define who knows, who decides, and who may write next.
