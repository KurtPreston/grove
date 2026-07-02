// Package examples ships grove's example configurations. The *.jsonc files in
// this directory are human-facing references you can copy into a project; one of
// them, grove.jsonc, doubles as the starter that `grove clone` seeds into a
// fresh project. Embedding it here keeps the shipped starter and the documented
// example from ever drifting apart.
package examples

import _ "embed"

// Starter is the commented grove.jsonc written verbatim into a fresh project by
// config.Seed, embedded from examples/grove.jsonc.
//
//go:embed grove.jsonc
var Starter string
