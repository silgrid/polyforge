package test

// polyforge#218 e2e: the darwin cross-build gate — part of the lane's own
// bar. Spec (artifacts/spec.md, "## Requirements"): "The tree MUST
// cross-build for darwin/amd64 and darwin/arm64", scenario "darwin
// cross-build gate in the lane's own bar": GIVEN the repository at any
// commit the lane proposes, WHEN `GOOS=darwin GOARCH=arm64 go build ./...`
// and `GOOS=darwin GOARCH=amd64 go build ./...` run (GOWORK=off), THEN both
// MUST exit 0. Today the build fails at internal/polyforge/daemon/
// peercred.go — unix.GetsockoptUcred / unix.SO_PEERCRED are Linux-only —
// so no pf or daemon binary can exist on a Mac.
//
// The gate lives in the e2e suite in test/, so the lane's own test bar
// runs it as part of the bar (the spec's acceptance: "a darwin cross-build
// gate ... as part of the lane's own bar").

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestW218DarwinCrossBuildGate(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			c := exec.Command(goBin, "build", "./...")
			c.Dir = repo
			c.Env = append(os.Environ(), "GOWORK=off", "GOOS=darwin", "GOARCH="+arch)
			out, err := c.CombinedOutput()
			if err != nil {
				t.Fatalf("GOOS=darwin GOARCH=%s go build ./... must exit 0 (the tree must cross-build for darwin; today's blocker is internal/polyforge/daemon/peercred.go: unix.GetsockoptUcred / unix.SO_PEERCRED are Linux-only); observed failure:\n%s", arch, out)
			}
		})
	}
}
