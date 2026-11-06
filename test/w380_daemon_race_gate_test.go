// carries: .polyforge/verify-scope.conf, .github/workflows/pf-ci.yml, docs/contributing.md
package test

// w380_daemon_race_gate_test.go: the daemon package's
// tests run under the race detector as part of the repository's full bar,
// so a new data race there cannot land silently: verification runs the
// engine's full tier before a merge (a daemon/ change is forced full), and
// the full-bar job of pf-ci.yml runs the same command after every merge.

import (
	"regexp"
	"slices"
	"testing"
)

// w380RaceCmd matches a go test command that runs the daemon package
// under the race detector.
var w380RaceCmd = regexp.MustCompile(`(^|\s)go test\s(.*\s)?-race(\s.*)?\s\./internal/polyforge/daemon/?(\s|$)`)

// TestW380DaemonRaceCheckRunsInTheFullBar: the engine's full-tier report
// for this repository has a full-phase command running the daemon
// package's tests with -race, and the full-bar job runs it verbatim.
func TestW380DaemonRaceCheckRunsInTheFullBar(t *testing.T) {
	root := e2e344Root(t)
	r := w375FullTier(t, root)
	var race string
	for _, c := range r.Commands {
		if c.Phase == "full" && w380RaceCmd.MatchString(c.Cmd) {
			race = c.Cmd
			break
		}
	}
	if race == "" {
		t.Fatalf("the full tier runs no `go test -race ./internal/polyforge/daemon/`: %+v", r.Commands)
	}
	wf := w375ReadWorkflow(t, root, w375Workflow)
	job, ok := wf.Jobs["full-bar"]
	if !ok {
		t.Fatalf("job full-bar is missing from .github/workflows/%s", w375Workflow)
	}
	if !slices.Contains(w375Lines(job), race) {
		t.Fatalf("the full-bar job does not run the daemon race check %q", race)
	}
}
