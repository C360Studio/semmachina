// Package ledger is the campaign archive: one immutable manifest per resolved
// turn, and the replay reader that proves the archive is honest.
//
// # Why a JetStream stream and not KV
//
// The kv-or-stream heuristic answers this twice, once for each half of the
// path, and both answers point the same way.
//
// The TRIGGER — `semmachina.turn.resolved`, published by the rule pack when a
// turn reaches a terminal phase — is a request: archive this turn. Exactly one
// handler should do it, the work has a real side effect (an append), and a
// restart must resume from the last acknowledgment rather than re-archive
// everything ever resolved. That is a stream, and it already is one: the writer
// binds a durable consumer to the stage stream filtered on that subject.
//
// The MANIFEST is the more interesting call, because a KV bucket keyed by
// turn_id would work mechanically — one key per turn, written once. It is still
// wrong. `ENTITY_STATES` is CURRENT TRUTH and the ledger is HISTORY; those are
// different jobs, and the affordances differ accordingly. A KV value is
// replaceable by construction, so putting an append-only archive in one makes
// "silently rewrite a turn that already happened" a single Put away. A stream
// append cannot overwrite. And the archive's natural read is not "the current
// value for this key" but "every turn in order, from a position I hold" — which
// is what a durable consumer gives the writer loop, the chronicler, and the
// exporter, each at its own pace. Neither is a fact-vs-request tie: a manifest
// is a record of a request having been resolved, which belongs to neither
// column and is why the archive is a third home rather than a bigger graph.
//
// # What the archive is for
//
// Reconstruction. `ENTITY_STATES` says what the world is NOW; a character's
// health overwritten by four later turns leaves no trace of the three values
// before it. The manifest chain plus the artifacts it references is what makes
// the earlier state — and the reason for it — recoverable. So the stream is
// file-backed with NO age, size, or count eviction: an evicted manifest is a
// turn the campaign can no longer account for, and nothing anywhere would
// report it.
//
// # References only
//
// A manifest carries identifiers, storage references, closed-vocabulary values
// and timestamps — never prose, never an intent list, never a verdict body.
// This is the rules-carry-references discipline applied to the archive, and it
// is enforced by the payload's own field set rather than by this comment.
//
// # Replay honesty
//
// The reader re-executes only what is DETERMINISTIC — the dice — and reads
// everything a persona authored from the reference the turn preserved. Re-running
// a narrator would produce a new rendition and call it a replay, which is the
// one thing an archive must never do.
//
// Nothing here can, and the guarantee is structural rather than careful: this
// package links none of the engine's persona machinery — not internal/persona,
// not internal/stage, not the mock model endpoint — and imports no model or
// agentic API directly, which a source scan and a dependency-tree check hold it
// to. (The upstream `agentic` and `model` packages do appear transitively, via
// the framework's own component and rule configuration types; that is plumbing
// linked by everything, and it is why the claim is stated as "no persona is
// reachable from here" rather than as an empty dependency tree.)
package ledger
