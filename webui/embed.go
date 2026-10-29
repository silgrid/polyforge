// Package webui embeds the console's built frontend: the Vite build
// writes the SPA into dist/, and the aihub server serves that tree —
// and only that tree — under /web/. The single aihub binary is the one
// shipped artifact: no webui/ directory needs to exist next to it at
// runtime (the acceptance bar starts the binary from elsewhere).
//
// Nothing under dist is tracked: the frontend build (which every
// binary-producing make target runs first) creates the directory, so
// `make build` on a clean checkout works; a bare `go build` before any
// frontend build fails loudly on the embed pattern instead of shipping
// a console-less binary.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist answers the embedded dist tree (the shell index.html at its
// root, hashed assets under assets/).
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
