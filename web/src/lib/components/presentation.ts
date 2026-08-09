// The fiction/crunch presentation dial: fiction mode leads with narration and
// keeps mechanics collapsed by default, crunch mode expands mechanics
// immediately. Shared so CreatorSurface, ResolutionCard, and the design
// harness can't drift into incompatible literal unions.
export type Presentation = 'fiction' | 'crunch';
