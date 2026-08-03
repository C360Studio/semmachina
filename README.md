# SemMachina

A fiction-first AI RPG built framework-native on
[SemStreams](https://github.com/C360Studio/semstreams).

Narrative positioning dictates mechanics (PbtA-style); SemStreams is the
connective tissue between the LLM storyteller and authoritative game state. The
world has a truth independent of any story told about it — narrative drift
becomes detectable and correctable because narration can be diffed against the
graph. Secondary product: the chronicler/writer loop — a running prose
collection during play, replayable into long-form manuscript.

**Status:** Stage 3 is complete and the template/place proof is archived on SemStreams
`v1.0.0-beta.*`. The repository contains a bootable turn-loop engine, durable world import and
restart gates, mystery and companion proofs, and one immutable world package that can select
different bounded voice and mechanics overlays while engine sequencing remains fixed.

The MVP deployment boundary is one active world and experience per process/broker. Persona records
live in the broker-global `PERSONAS` bucket, so two concurrently active worlds with different voices
must use separate brokers/stacks; a different `world_ns` isolates graph identity but not persona
voice.

## Start here

| Document | What it holds |
|---|---|
| [`Founding proposal`](docs/proposals/fiction-first-rpg.md) | Idea review and engine decomposition |
| [`World authoring guide`](docs/guides/world-authoring.md) | Packages, experience packs, places, rules, and migration |
| [`Gemini smoke runbook`](docs/runbooks/bellweather-gemini-smoke.md) | Operator-only paid smoke |
| [`openspec/project.md`](openspec/project.md) | Purpose, product boundary (game repo vs engine asks), conventions |
| [`CLAUDE.md`](CLAUDE.md) | Working context for AI-assisted development |

## Development

Go 1.26+. Spec-driven: non-trivial work starts with an OpenSpec change
(`/opsx:new` in Claude Code, or `openspec` CLI) — proposal + tasks + spec deltas
before code.
