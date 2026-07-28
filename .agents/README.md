# SemMachina Agent Profiles

The files in `contracts/` are the tracked, platform-neutral behavioral authority. Platform adapters are
intentionally thin and must point to exactly one canonical contract. The pattern (and much of the content) is
lifted from semstreams' `.agents/` layout — see `~/Code/c360/semstreams/.agents/README.md`.

## Role and platform mapping

- SemMachina developer
  - Canonical: `.agents/contracts/semmachina-developer.md`
  - Claude: `.claude/agents/semmachina-developer.md`
- SemMachina reviewer
  - Canonical: `.agents/contracts/semmachina-reviewer.md`
  - Claude: `.claude/agents/semmachina-reviewer.md`

Only Claude adapters exist today; add other platform adapters (Codex, etc.) beside them if those tools are
introduced, keeping each adapter a thin pointer to the canonical contract.

## Shared decision skills

The files in `skills/` are the tracked, platform-neutral canonical instructions for the shared decision
heuristics, adapted from semstreams' canonical versions with game-domain flavoring. The `.claude/skills/`
entries of the same names are thin adapters (frontmatter for Claude discovery + a one-line pointer).

- `.agents/skills/kv-or-stream/SKILL.md` — KV Watch vs JetStream Stream (4-test heuristic)
- `.agents/skills/orchestration-check/SKILL.md` — rule vs component vs lifecycle boundary
- `.agents/skills/new-payload/SKILL.md` — payload-registry checklist
- `.agents/skills/query-pattern/SKILL.md` — GraphQL vs MCP vs NATS Direct

When semstreams updates its canonical skill, diff against ours and port what applies — these are flavored
copies, not pointers, so drift is possible and periodic reconciliation is expected.

All other `.claude/skills/` entries (openspec workflow, …) are Claude-workflow tooling and remain
platform-specific by design — do not mirror them.

## Manual read-only parity smoke

Run after changing a contract, adapter, or routing rule. It only reads tracked files.

1. Confirm both canonical contracts and both Claude adapters exist.
2. Confirm each adapter names exactly its matching `.agents/contracts/...` path and says to read it fully first.
3. Confirm the Claude reviewer tool list contains `Read`, `Bash`, `Grep`, `Glob`, and `Skill`, but not `Edit`,
   `Write`, `Task`, or another delegation tool.
4. Confirm `CLAUDE.md` routes the same logical roles (developer for nontrivial implementation, reviewer
   pre-merge, generic Go agents as second pass only).
5. Inspect adapter size with `wc -l .claude/agents/semmachina-*.md`; adapters stay short with no copied
   checklist.
6. Confirm each shared-skill adapter in `.claude/skills/{kv-or-stream,new-payload,orchestration-check,query-pattern}/SKILL.md`
   names exactly its matching `.agents/skills/...` path, says to read it fully first, and contains no copied
   body (`wc -l` ≈ 8).
