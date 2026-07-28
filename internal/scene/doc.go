// Package scene is the context assembler: the fixed, scene-scoped graph query a
// persona's world is built from.
//
// It is the component that makes "per-turn cost is flat" true (M7). Context is
// graph-retrieval-bounded, never append-everything, and the bound here is
// STRUCTURAL rather than conventional:
//
//   - The query SHAPE is fixed. A constant number of reads — the turn, the
//     scene, the scene's incoming edges, one batch of members, one batch of
//     their neighbours — with no recursion, no depth parameter, and nothing a
//     caller can widen. Depth is 1 because there is no code that goes further.
//   - The query SCOPE is the scene. Membership comes from the edges pointing at
//     the scene, so a world of a thousand rooms costs exactly what this room
//     costs; the map's size never enters the retrieval.
//   - The query SIZE is capped and REPORTED. A scene that exceeds the entity cap
//     is a loud refusal naming the scene, never a silent truncation, and every
//     assembled view carries its own measured weight so "how big was this
//     context" is a question with an answer.
//
// Two disciplines it does not bend:
//
//   - It READS. graph-ingest remains the sole ENTITY_STATES writer, and the
//     interface this package is handed carries no mutation at all — that is a
//     compile-time fact, not a review note.
//   - It reads at EXECUTION time. No snapshot travels with the action: a persona
//     judges the world as it is when the persona runs, not as it was when the
//     player hit send. Under email-cadence play those can be days apart, and the
//     world may have moved.
//
// Not thematic retrieval. There is no embedding, no relevance score, and no
// query text — deliberately. What a persona is shown is decided by where the
// turn is happening, which is a fact, rather than by what a retriever thought
// was similar, which is a guess with a cost curve.
package scene
