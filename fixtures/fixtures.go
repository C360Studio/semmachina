// Package fixtures embeds the first-party world packages that ship with the
// engine.
//
// Embedding rather than reading from disk is what keeps "worlds are data" from
// degrading into "worlds are data if you remember to copy a directory": the
// starter world travels inside every binary and every test binary, so a broken
// package is a build-time or test-time failure rather than a deployment that
// boots into an empty world.
//
// A world package is still ordinary data — this package hands out an fs.FS and
// knows nothing about manifests, entities, or the importer.
package fixtures

import (
	"embed"
	"io/fs"
)

// StarterWorldPath is the embedded path of the first-party starter world.
const StarterWorldPath = "worlds/starter"

// BellweatherMazePath is the embedded first-party mystery acceptance world.
const BellweatherMazePath = "worlds/bellweather-maze"

//go:embed all:worlds
var worlds embed.FS

// Worlds returns the embedded world-package tree.
func Worlds() fs.FS { return worlds }

// StarterWorld returns the starter world package rooted at its own directory,
// so a caller sees manifest.yaml at the root exactly as it would from disk.
func StarterWorld() (fs.FS, error) {
	return fs.Sub(worlds, StarterWorldPath)
}

// BellweatherMaze returns the authored mystery package rooted at its manifest.
func BellweatherMaze() (fs.FS, error) {
	return fs.Sub(worlds, BellweatherMazePath)
}
