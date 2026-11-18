// Package version carries the build-injected version string of the one pf
// binary (the client, `pf daemon` and the `pf aihub` server).
//
// Un-injected (plain `go build`) builds report "dev". Release builds stamp
// the git tag at link time — see the Makefile's GO_LDFLAGS and `make release`:
//
//	go build -ldflags "-X github.com/silgrid/polyforge/internal/version.Version=<tag>" ./cmd/pf
//
// pf prints it via `pf --version` and the server via `pf aihub --version`.
// The same stamps carry Commit, the source commit of a development-channel
// build (tools/install.sh --dev → make dev-build, polyforge#136): empty on
// a release build (an exact tag), the green-tag commit a dev build was cut
// from otherwise. `pf --json version` reports it in its `commit` field;
// `pf --version`, `pf version` and `pf doctor`'s `pf build` row show the
// version, and the doctor row the commit too.
package version

// Version is the version string of the build. It defaults to "dev" and is
// overwritten at link time by release builds (Makefile GO_LDFLAGS, git tag).
var Version = "dev"

// Commit is the source commit a development-channel build was cut from,
// stamped at link time beside Version (make dev-build). It is empty on a
// release build (the tag names the commit) and never part of the version
// string: a vX.Y.Z-dev.N version compares by SemVer everywhere, and the
// commit rides its own field.
var Commit = ""
