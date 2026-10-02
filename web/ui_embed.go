//go:build ui

// Package web holds the web UI's built files, embedded from out/ by the ui build tag.
package web

import (
	"embed"
	"io/fs"
)

// out is the built UI. "all:" keeps Next.js's _next directory, which a plain pattern
// would skip for starting with an underscore.
//
//go:embed all:out
var out embed.FS

// Files returns the built web UI.
func Files() fs.FS {
	files, err := fs.Sub(out, "out")
	if err != nil {
		panic("web: the embedded UI has no out directory: " + err.Error()) // impossible: go:embed checked it
	}
	return files
}
