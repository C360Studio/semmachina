# SemMachina

A fiction-first AI RPG built framework-native on
[SemStreams](https://github.com/C360Studio/semstreams).

Narrative positioning dictates mechanics (PbtA-style); SemStreams is the
connective tissue between the LLM storyteller and authoritative game state. The
world has a truth independent of any story told about it — narrative drift
becomes detectable and correctable because narration can be diffed against the
graph. Secondary product: the chronicler/writer loop — a running prose
collection during play, replayable into long-form manuscript.

**Status:** pre-code. Repo is initialized for spec-driven development
([OpenSpec](https://github.com/Fission-AI/OpenSpec)) against SemStreams
`v1.0.0-beta.*`, retargeting v1 on release.

## Start here

| Document | What it holds |
|---|---|
| [`docs/proposals/fiction-first-rpg.md`](docs/proposals/fiction-first-rpg.md) | Founding document — idea review and engine decomposition |
| [`openspec/project.md`](openspec/project.md) | Purpose, product boundary (game repo vs engine asks), conventions |
| [`CLAUDE.md`](CLAUDE.md) | Working context for AI-assisted development |

## Development

Go 1.26+. Spec-driven: non-trivial work starts with an OpenSpec change
(`/opsx:new` in Claude Code, or `openspec` CLI) — proposal + tasks + spec deltas
before code.
