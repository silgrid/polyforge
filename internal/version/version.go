// Package version carries the build-injected version string of the one pf
// binary (the client, `pf daemon` and the `pf aihub` server).
//
// Un-injected (plain `go build`) builds report "dev". Release builds stamp
// the git tag at link time — see the Makefile's GO_LDFLAGS and `make release`:
//
//	go build -ldflags "-X github.com/silgrid/polyforge/internal/version.Version=<tag>" ./cmd/pf
//
// pf prints it via `pf --version` and the server via `pf aihub --version`.
package version

// Version is the version string of the build. It defaults to "dev" and is
// overwritten at link time by release builds (Makefile GO_LDFLAGS, git tag).
var Version = "dev"
