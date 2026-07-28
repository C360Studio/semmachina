---
name: kv-or-stream
description: Decide between KV Watch and JetStream Stream for a new communication path. Use when designing inter-component communication, adding new message flows, or choosing storage primitives.
argument-hint: [description of the communication being designed]
---

# KV Watch vs JetStream Stream Decision (SemMachina)

## What are you designing?

$ARGUMENTS

## The 4-Test Heuristic

Apply these tests in order. The first clear answer is usually sufficient.

### Test 1: Restart Test (sharpest)

If this processor restarted, should it re-process messages it already handled?

- **Yes** (re-process is correct recovery) --> **KV Watch**
- **No** (re-process would be wrong) --> **JetStream Stream**

The game's canonical framing: a restart must re-deliver the WORLD (facts) and must NOT replay the dragon
eating you (requests).

### Test 2: Fan-out vs Queue

Should multiple processors all react to this, or should only one handle it?

- **All react** (fan-out) --> **KV Watch**
- **Only one handles it** (queue) --> **JetStream Stream**

### Test 3: Processing Time

Is the processing fast and idempotent, or slow with real side effects?

- **Fast and idempotent** --> **KV Watch**
- **Slow or has side effects** --> **JetStream Stream**

### Test 4: Nature Test

Is this a fact about the world, or a request to do something?

- **Fact** (entity state, index entry, current status) --> **KV Watch**
- **Request** (execute task, call LLM, run tool) --> **JetStream Stream**

## Conflict Check

If any test gives conflicting answers, the concept may be two things conflated. Revisit whether it should be
split into separate concerns.

## Common Cases Reference (game-flavored)

| Communication | Primitive | Reason |
|--------------|-----------|--------|
| Character/item/location state changed | KV Watch (`ENTITY_STATES`) | Fact; fan-out; restart re-delivers the world |
| Player submits an action | JetStream Stream | Request; queue; must not replay on restart |
| Adjudicator verdict triple lands | KV Watch (graph fact) | Fact about the proposed action; rules match verdict class |
| Roll-result triple lands | KV Watch (graph fact) | Fact; deterministic; rules chain on it |
| LLM inference call (narrator, NPC decision) | JetStream Stream | Request; queue; slow; costly |
| Narration prose | ObjectStore + ref-triple | Neither — bulky content, addressable as state |
| NPC agent loop current state | KV | Fact; queryable; recoverable |
| Chronicler trigger (salient scene complete) | JetStream Stream | Request; queue; background priority |
| World-tick / ambient reaction | KV Watch | Fact-driven; fan-out; the always-on world is KV watch doing its job |

## Key Architecture Context

**The KV Twofer**: Every NATS KV bucket is backed by a JetStream stream. A single KV write gives you three
interfaces simultaneously:

- **State**: `kv.Get(key)` returns current value
- **Events**: `kv.Watch(pattern)` fires on every change (fan-out)
- **History**: Replay from any revision for audit trail

**Bootstrap phase**: When a KV watcher starts, it delivers ALL current values matching the pattern, then a
`nil` entry signals transition to live updates. Processors must distinguish bootstrap from live to avoid
treating existing entities as "new" events on restart — for the game: a rejoining world must not re-narrate
its whole state as fresh events.

**JetStream consumers**: With `DeliverPolicy: "new"` on a durable consumer, restart resumes from last ack.
No replay of already-handled messages.

**Using both is normal**: A component using KV for state AND JetStream for work items in the same process is
the standard pattern (see agentic-loop upstream).

Read `~/Code/c360/semstreams/docs/concepts/03-streams-vs-kv-watches.md` for full documentation.
Read `~/Code/c360/semstreams/docs/concepts/02-kv-twofer.md` for KV Twofer details.
