package cli

import "github.com/silgrid/polyforge/internal/version"

// doctor_build.go: the `pf build` row of `pf doctor` (polyforge#136) — the
// one place the machine's own build answers: the version the binary carries
// (internal/version.Version) and, for a development-channel build, the
// source commit it was cut from (internal/version.Commit, stamped by
// make dev-build). A release build shows the tag alone. Host-level,
// needs nothing beyond a loadable config (the ladder's own floor; the
// config-free query is the version verb): always pass — the row reports,
// it never fails.

// docChBuild is the build row's name.
const docChBuild = "pf build"

// buildCheck reports the running build's version and, when set, its source
// commit (a development-channel build).
func buildCheck() doctorCheck {
	c := doctorCheck{Name: docChBuild, Status: "pass", Detail: version.Version}
	if version.Commit != "" {
		c.Detail = version.Version + " from " + version.Commit + " (development build)"
	}
	return c
}
