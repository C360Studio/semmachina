// Package world loads package-shaped world templates and materializes them
// into a campaign.
//
// # Templates and instances
//
// A TEMPLATE is an immutable versioned product: a directory holding
// manifest.yaml, entities.jsonl, rules/, and personas/. An INSTANCE is a
// mutable campaign materialized from a template into a world namespace. The
// distinction is what makes "worlds are data" true in practice — the same
// package can be played twice, by different people, in one graph, with no edit.
//
// Loading is three steps with three different failure surfaces, kept apart on
// purpose:
//
//	LoadPackage  — everything checkable from the package alone: manifest v0,
//	               engine compatibility, the predicate alphabet, vocabulary
//	               membership, kind compatibility, object shapes, and attribute
//	               bounds. Fails with a file and a line.
//	Resolve      — everything that needs instance identity: the six-part ID
//	               mapping, `local:` reference rewriting, dangling references,
//	               and the instance-configured player binding. Pure and
//	               deterministic; reads no clock and no environment.
//	Import       — publishing. The only step that touches the network.
//
// # What the engine never learns from a package
//
// Two facts are engine-owned and a template cannot declare them: an entity's
// kind triple, which is PROJECTED from its declared type so the triple and the
// ID can never disagree, and the played-character binding, which is instance
// configuration. A template that could name its own player would be
// instantiable exactly once.
//
// # Materialization
//
// Entities reach the graph as Graphable payloads on the ordinary fact lane, so
// graph-ingest remains the sole ENTITY_STATES writer. Identity is deterministic
// and the fact lane merges by replacing same-predicate triples, so re-importing
// an unchanged package converges rather than duplicating.
//
// That convergence has a sharp edge worth stating plainly: a re-import
// overwrites every predicate the template declares, including values play has
// since changed. Re-importing a template into a LIVING campaign resets the
// template's own facts. Nothing here guards against that, because nothing here
// knows whether a campaign is live; a template-update policy is a separate
// decision from a template-load mechanism.
package world
