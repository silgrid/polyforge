package daemon

// autowave_clone_warning_test.go: the auto-pull face
// of the missing-clone warning — an auto-pulled claim whose mapped
// workspace lacks the clones logs exactly one daemon warning naming
// each missing repo, is not blocked (the claim goes live), and emits no
// event kind a clones-present claim does not. Clones present: no line.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// autoClaimWithClones boots a test daemon with auto on, seeds one ready work
// item whose project repos are names (cloned under the mapped workspace
// when cloned), and claims it through the auto wave's own path —
// fetchAutoWave enqueues, startEntry claims. It answers the daemon's
// captured stderr (the warning's face) and the claim's event kinds.
func autoClaimWithClones(t *testing.T, names []string, cloned bool) (string, []string) {
	t.Helper()
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour // the wave and its claim are driven by hand
		c.DispatchTick = time.Hour
	})
	const wi = "wi_auto_clone"
	mkBare(t, td.cfg.GitRemoteBase, wi)
	td.hub.mu.Lock()
	td.hub.repoInventory = map[string][]string{} // no declared inventory: the project's repos are the check set
	td.hub.projectRepos = map[string][]string{"proj": names}
	bundle := "demo-bundle"
	td.hub.queues = append(td.hub.queues, api.WorkItem{
		ID: wi, ProjectName: "proj", Priority: api.PriorityNormal, BundleName: &bundle,
	})
	td.hub.mu.Unlock()
	if cloned {
		for _, n := range names {
			placementClone(t, td.cfg.Mapping.Workspaces()[0], n)
		}
	}
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}

	// capture everything the daemon writes to stderr: the warning's face.
	capFile, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	realStderr := os.Stderr
	os.Stderr = capFile
	restored := false
	restore := func() {
		if !restored {
			restored = true
			os.Stderr = realStderr
			capFile.Close()
		}
	}
	t.Cleanup(func() {
		failed := t.Failed()
		restore()
		if failed {
			if body, rerr := os.ReadFile(capFile.Name()); rerr == nil {
				t.Logf("captured daemon stderr:\n%s", body)
			}
		}
	})

	td.fetchAutoWave()
	e := td.q.Take()
	if e == nil {
		restore()
		t.Fatal("the auto wave enqueued nothing")
	}
	if e.Origin.Kind != runtime.OriginAuto {
		restore()
		t.Fatalf("the enqueued entry's origin = %q, want auto", e.Origin.Kind)
	}
	td.startEntry(*e)
	restore()
	td.waitLive(t, wi) // the claim is live and unblocked
	td.waitMonitored(t, wi)

	var kinds []string
	for _, ev := range td.obs.Events().SinceAfter(wi, 0) {
		kinds = append(kinds, ev.Kind)
	}
	body, err := os.ReadFile(capFile.Name())
	if err != nil {
		t.Fatalf("read the captured stderr: %v", err)
	}
	return string(body), kinds
}

// TestAutoClaimMissingClonesOneWarningNoNewEvent: the missing-clone
// claim logs exactly one warning naming each missing repo, goes live
// (not blocked) and emits no event kind the clones-present claim does
// not; clones present log no warning at all.
func TestAutoClaimMissingClonesOneWarningNoNewEvent(t *testing.T) {
	names := []string{"autora", "autorb"}

	happyLog, happyKinds := autoClaimWithClones(t, names, true)
	if strings.Contains(happyLog, "no local clone") {
		t.Fatalf("clones present: the daemon log must carry no clone warning (log %q)", happyLog)
	}

	log, kinds := autoClaimWithClones(t, names, false)
	for _, n := range names {
		if c := strings.Count(log, n); c != 1 {
			t.Fatalf("auto claim with missing clones: the daemon log must carry exactly one warning naming %s (found %d; log %q)", n, c, log)
		}
	}
	for _, k := range kinds {
		if !containsKind(happyKinds, k) {
			t.Fatalf("auto claim with missing clones emitted NEW event kind %q (clones-present kinds %v) — no new event type is allowed", k, happyKinds)
		}
	}
}

// containsKind answers whether ss carries s.
func containsKind(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
