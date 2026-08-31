package aioutputs

import "embed"

// Files contains strict schemas for every Teaching Agent result consumed by Go.
//
//go:embed *.schema.json
var Files embed.FS
