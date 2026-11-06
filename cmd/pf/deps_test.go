package main

// deps_test.go: the pf binary carries no Go step-command machinery. The
// commands a workflow step runs are the workflow's own scripts
// (workflows/_shared/scripts, Python); the Go packages they replaced —
// the step command registry and its argv[0] dispatch, the verification
// runner and its container executor, the facts and integrate libraries and
// the merge library — are gone, and `go list -deps` of this binary names
// none of them.

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestPfCarriesNoStepCommandPackages(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	// -e: a tree without the console build (webui/dist) still lists
	cmd := exec.Command(gobin, "list", "-e", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := strings.Fields(string(out))
	const pkgs = "github.com/silgrid/polyforge/internal/polyforge/"
	if !slices.Contains(deps, pkgs+"daemon") || !slices.Contains(deps, pkgs+"cli") {
		t.Fatalf("go list -deps names neither the daemon nor the CLI: the listing is not this binary's\n%s", out)
	}
	for _, gone := range []string{"stepcmd", "verifyrun", "facts", "integrate", "gitmerge"} {
		if slices.Contains(deps, pkgs+gone) {
			t.Errorf("pf still depends on %s%s", pkgs, gone)
		}
	}
}
