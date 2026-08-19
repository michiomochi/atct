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
// Keep the .gitkeep sentinel in dist. go:embed rejects a missing or empty
// directory, and dist itself is generated at release time rather than committed,
// so without the sentinel a fresh checkout would not compile. It only works
// because this pattern uses all: -- plain go:embed ignores dotfiles.
//
// astro.config.mjs sets vite.build.emptyOutDir to false for the same reason: the
// build wipes its output directory by default and would take the sentinel with
// it, leaving a deleted file in every git status after every build.
//
//go:embed all:dist
var Dist embed.FS
