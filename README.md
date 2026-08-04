# SemMachina

A fiction-first AI RPG built framework-native on
[SemStreams](https://github.com/C360Studio/semstreams).

Narrative positioning dictates mechanics (PbtA-style); SemStreams is the
connective tissue between the LLM storyteller and authoritative game state. The
world has a truth independent of any story told about it — narrative drift
becomes detectable and correctable because narration can be diffed against the
graph. Secondary product: the chronicler/writer loop — a running prose
collection during play, replayable into long-form manuscript.

**Status:** Stage 3, the template/place proof, and the minimal creator surface are complete and
archived. The repository currently targets SemStreams `v1.0.0-beta.*` and contains a bootable
turn-loop engine, durable world import and restart gates, mystery and companion proofs, and one
immutable world package that can select different bounded voice and mechanics overlays while engine
sequencing remains fixed. The creator surface adds a one-world, one-player SvelteKit session over
the existing player and graph contracts.

The MVP deployment boundary is one active world and experience per process/broker. Persona records
live in the broker-global `PERSONAS` bucket, so two concurrently active worlds with different voices
must use separate brokers/stacks; a different `world_ns` isolates graph identity but not persona
voice.

## Start here

| Document | What it holds |
|---|---|
| [`Founding proposal`](docs/proposals/fiction-first-rpg.md) | Idea review and engine decomposition |
| [`World authoring guide`](docs/guides/world-authoring.md) | Packages, overlays, places, rules, and migration |
| [`Gemini smoke runbook`](docs/runbooks/bellweather-gemini-smoke.md) | Operator-only paid smoke |
| [`openspec/project.md`](openspec/project.md) | Purpose, product boundary (game repo vs engine asks), conventions |
| [`CLAUDE.md`](CLAUDE.md) | Working context for AI-assisted development |

## Try the Bellweather creator surface

This is a local presenter demo of the actual Go, SvelteKit, NATS, SemStreams, and GraphQL stack. It
starts one Bellweather world for one player. The browser opens headed, signs in with a fresh
ephemeral creator credential, waits for the world and action controls, and then hands control to
you; it does not submit an action during bootstrap.

Prerequisites are Docker, Node/npm, Go, the repository's Playwright browser, and installed project
dependencies. Copy `.env.example` to the ignored `.env` file and set an authorized
`GEMINI_API_KEY`.

To verify startup, login, map, and clock without submitting an action or calling a model provider:

```sh
task smoke:surface:preflight
```

After explicitly authorizing provider spend, start the interactive demo:

```sh
SEMMACHINA_PAID_SMOKE=1 task demo:surface
```

Each action you submit may incur Gemini API charges. Close the browser or press Ctrl-C in the
terminal to stop the demo and clean up its local processes.

What this demo contains:

- Graph-backed world and turn state through the real local stack.
- Gemini 3.5 Flash-Lite calls only after you submit an action.
- Bounded turn and persona execution.
- Structured, conditional companion decisions and hints; the companion does not speak on every
  action.

Keep the limits in view:

- This is a self-signed local demo, not production deployment or security proof.
- It is not a finished game, an authoring UI, or a multi-world host.
- Model output is nondeterministic, and an action can fail.
- Graph-backed state makes narrative drift inspectable; it does not make drift impossible.
- Turn and persona work is bounded, but there is no session-wide spend cap. The presenter controls
  how many paid actions are submitted.

See the [creator-surface operations runbook](docs/runbooks/creator-surface.md) for configuration,
security boundaries, preflight, paid acceptance, and troubleshooting.

## Development

Go 1.26+. Spec-driven: non-trivial work starts with an OpenSpec change
(`/opsx:new` in Claude Code, or `openspec` CLI) — proposal + tasks + spec deltas
before code.
