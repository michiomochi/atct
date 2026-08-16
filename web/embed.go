package web

import "embed"

// Dist contains the built Web UI assets.
//
// The all: prefix is required. Without it, go:embed skips files and directories
// whose names begin with "." or "_", and Astro emits its bundles into _astro/
// and its dynamic routes into goals/_/. Embedding plain "dist" ships index.html
// alone, so every asset request falls through to the SPA fallback and the
// browser receives HTML where it asked for JavaScript.
//
//go:embed all:dist
var Dist embed.FS
