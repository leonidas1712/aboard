//go:build !ui

// Package web holds the web UI's built files for the aboard binary. The UI is a
// Next.js app in this directory; `make web` builds it into out/, and building aboard
// with the ui build tag (as `make install` and releases do) embeds out/. Without the
// tag, Files returns nil and the server says at / how to get the UI.
package web

import "io/fs"

// Files returns the built web UI, or nil when this binary was built without it.
func Files() fs.FS { return nil }
