// Package campaign owns the campaign entity: the world instance's
// instantiation sentinel and the home of its replay seed.
//
// A campaign is INSTANCE state, never template content. A template is an
// immutable versioned product that can be materialized into any number of
// campaigns; a template that named its own campaign — or its own seed — could
// be instantiated exactly once, and every campaign made from it would roll
// identical dice. So nothing in this package is authorable by a world package,
// and the campaign entity's type segment is deliberately outside the world's
// entity-kind vocabulary.
//
// Two responsibilities, one entity, on purpose:
//
//   - The SEED is the root of every deterministic thing the engine does. Each
//     roll's seed is derived from it and the turn id, so replay, the writer
//     loop, and any audit of a recorded roll all bottom out here.
//   - The SENTINEL is the world importer's gate. One atomic create answers
//     "has this world been instantiated?" without an exists-check race, and the
//     entity it creates is the one the seed needed anyway.
package campaign
