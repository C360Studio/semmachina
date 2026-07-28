// Package content is the engine's durable home for everything too bulky, too
// unbounded, or too FICTIONAL to live on a triple.
//
// The graph's rule-matching surface carries small closed-vocabulary scalars and
// nothing else (M1, and F6 behind it). Everything a turn produces that is not
// one of those — the player's own words, the verdict's banded intents, the
// dice, the narration — reaches the graph as a REFERENCE, and this package is
// where the referenced thing actually lives.
//
// Four rules shape all of it, and each closes a specific hole:
//
//   - The store is semstreams' ObjectStore, not a local one (M6). Retention,
//     chunking, the ADR-068 no-TTL guarantee, and the StorageInstance contract
//     that makes a reference resolvable are already solved upstream, and a
//     hand-rolled parallel store would inherit none of them.
//   - A key is DERIVED, never chosen: turn/<turn_id>/<slot>, where the slot is
//     the reference predicate's own category segment. So the same turn's same
//     artifact always lands at the same key, a redelivery re-puts identically
//     instead of accumulating a second copy, and the predicate on the triple and
//     the location of the object cannot disagree.
//   - The stored bytes are the artifact's canonical JSON, not a BaseMessage
//     envelope. An envelope carries a fresh UUID per construction, so an
//     envelope-wrapped re-put would write different bytes to the same key and
//     "identical re-put" would be true of the location and false of the content.
//     The type discriminator an envelope would supply is already carried by the
//     key's slot, which comes from the closed predicate set.
//   - Prose first, reference last. The object is written BEFORE the triple that
//     points at it, always. A reference to a missing object is a correctness bug
//     — a turn nothing can re-prompt, a narration nothing can deliver — while an
//     object nobody references is garbage, and garbage is collectable.
//
// Nothing here writes to ENTITY_STATES. graph-ingest remains the sole writer;
// this package produces the reference, and the component that owns the entity
// puts it on the graph.
package content
