package web

import "embed"

// Dist contains the built Web UI assets.
//
//go:embed dist
var Dist embed.FS
