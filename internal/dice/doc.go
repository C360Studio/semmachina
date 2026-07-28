// Package dice is the seeded, versioned resolution component.
//
// It is where the engine's determinism claim (M4) is either true or not. Every
// roll is a pure function of three recorded things — the campaign seed, the
// turn id, and the verdict's modifiers — so a replay, a writer-loop pass, or an
// audit re-executes a turn and gets the same dice, byte for byte, forever.
//
// That claim is only as strong as its weakest entropy source, so this package
// has NO other one: no wall clock, no global RNG, no ambient state, and no
// hidden per-roller generator carried between rolls. Each roll constructs its
// own generator from its own derived seed and throws it away. The rule is
// enforced structurally — see the package purity test, which reads this
// package's source and fails on a forbidden import or a top-level rand call —
// because "we were careful" is not a property replay can rely on.
//
// Two things this package deliberately does NOT do:
//
//   - It does not decide WHETHER to roll from the (plausibility, risk) mapping.
//     Narrative positioning dictates mechanics, so the adjudicator's REPORTED
//     requires_roll is the gate, and vocabulary.RequiresRoll is advisory data
//     recorded for quality metrics (design D12). A dice component that consulted
//     the mapping would quietly take the roll gate back from the fiction.
//   - It does not re-derive whether the verdict's modifiers are legal. That
//     bound lives in internal/payload, on the payload that carries them; the
//     dice consume modifiers and refuse to emit a record that would not validate.
package dice
