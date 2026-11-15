package test

// w39_browser_suite_repeatable_test.go: the browser suite must be
// repeatable on one server (the ticket's acceptance): two consecutive
// full runs against the SAME install and data, and both runs' S24 pass.
// S24 ends by re-deactivating the dormant fixture project through the
// same v1 door the driver seeded it with, so the second run's first
// context answer hides it again. Red before that re-arm: the first run
// restores w30dormant and leaves it active, and the second run's S24
// fails on its first context assertion (polyforge#39, review r1#1 —
// this is the unit's test; the re-arm is the implementation).

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestW39BrowserSuiteRepeatableOnOneServer(t *testing.T) {
	if msg := browserPrereqError(t); msg != "" {
		t.Fatalf("the console browser suite's prerequisites are missing: %s", msg)
	}
	inst := newV1Install(t)
	fixtures := seedBrowserFixtures(t, inst)
	raw, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatalf("marshal browser fixtures: %v", err)
	}

	// Both runs answer the same install: the suite is only repeatable
	// when every scenario leaves the server as it found it. The output
	// is read whole after the process exits, so every assertion reads a
	// settled state.
	for run := 1; run <= 2; run++ {
		cmd := exec.Command("node", filepath.Join("webui", "e2e", "run.mjs"))
		cmd.Dir = v1Root(t)
		cmd.Env = append(browserSuiteEnv(t),
			"PF_WEB_URL="+inst.url,
			"PF_WEB_KEY="+inst.adminKey,
			"PF_WEB_FIXTURES="+string(raw),
		)
		out, err := cmd.CombinedOutput()
		if !strings.Contains(string(out), "PASS S24") {
			t.Fatalf("run %d of the browser suite on one server does not report S24 passing (exit %v):\n%s", run, err, out)
		}
		if err != nil {
			t.Fatalf("run %d of the browser suite failed besides S24: %v\n%s", run, err, out)
		}
	}
}
