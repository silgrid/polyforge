package test

// w426_removed_bundle_display_e2e_test.go: AC4 — a work
// item bound to a bundle the daemon's release no longer installs (the four
// lanes removed, or any other name) keeps displaying in the
// pf CLI: `pf file show`, the merged `pf --json queue` Pi Web's work-item
// list reads, and `pf watch --once` answer it exactly as they answer a
// work item on an installed bundle. Nothing here is special-cased by name:
// the display paths read the hub record and the capsule's own snapshot of
// the bundle, never the installed workflows. Only a claim needs the
// installed bundle, and it refuses one that is gone, naming it.

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// w426Pf runs the real pf CLI against inst and d's socket and answers its
// stdout, stderr and exit code (never failing on a non-zero exit).
func w426Pf(t *testing.T, inst *v1Install, d *v1Daemon, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(inst.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(), "PF_SERVER="+inst.url, "PF_API_KEY="+inst.adminKey, "PF_USER_ID=usr_root", "PF_DAEMON_SOCKET="+d.sock)
	v1BusinessCmd(t, cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// w426NewWI files one execution ticket on bundle (no plan: it is never run
// on a daemon that lacks the bundle).
func w426NewWI(t *testing.T, inst *v1Install, goal, bundle string) string {
	t.Helper()
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{Project: v1Project, Goal: goal, BundleName: strPtr(bundle)})
	if err != nil {
		t.Fatalf("create work item on %s: %v", bundle, err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil || wi.ID == "" {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	inst.trackWI(wi.ID)
	return wi.ID
}

// w426QueueRow answers wi's row of the merged JSON queue (nil when absent).
func w426QueueRow(t *testing.T, out, wi string) map[string]any {
	t.Helper()
	var doc struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("pf --json queue answer undecodable: %v (%s)", err, out)
	}
	for _, it := range doc.Items {
		if it["id"] == wi {
			return it
		}
	}
	return nil
}

// TestW426TerminalWorkItemOnRemovedBundleStillDisplays: a work item that
// ran to its end on a bundle the next release no longer installs (it ran on
// micro, then the daemon restarts on a tree without micro — the shape of
// any lane removal), canceled work items bound to the four lanes
// removed and to a name no release ever installed, and a
// control on the installed develop bundle all display through file show,
// the JSON queue and watch --once, the same way; a queued one on a removed
// bundle is refused at submit, naming it.
func TestW426TerminalWorkItemOnRemovedBundleStillDisplays(t *testing.T) {
	inst, _ := newV1InstallTaskRepo(t)
	ran := inst.newWI(t, "w426: ran on a lane the next release removes", envelopeMicro(), nil)
	d := newV1Daemon(t, inst)
	d.start()
	if out := d.pf(t, "submit", ran); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	inst.waitWIStatus(t, ran, "wrapped", 120*time.Second)
	d.stop()

	// the next release: the daemon's workflows dir no longer installs the
	// lane the work item ran on
	inst.withoutLane, inst.strippedWF = vehicleLane, ""
	d2 := newV1Daemon(t, inst)
	d2.start()
	for _, gone := range []string{vehicleLane, "autopilot", "bug-fix", "chore", "feature-full"} {
		if _, err := os.Stat(filepath.Join(inst.workflowsPath(), gone, workflow.WorkflowFile)); !os.IsNotExist(err) {
			t.Fatalf("the restarted daemon must not install %s (stat: %v)", gone, err)
		}
	}

	type shown struct{ wi, bundle, status string }
	cases := []shown{{ran, vehicleLane, "wrapped"}}
	for _, bundle := range []string{"feature-full", "chore", "bug-fix", "autopilot", "never-installed-lane", "develop"} {
		wi := w426NewWI(t, inst, "w426: canceled on "+bundle, bundle)
		d2.pf(t, "cancel", wi)
		inst.waitWIStatus(t, wi, "canceled", 30*time.Second)
		cases = append(cases, shown{wi, bundle, "canceled"})
	}

	queue := d2.pf(t, "--json", "queue", "--project", v1Project)
	var controlWatch int = -1
	for _, c := range cases {
		out := d2.pf(t, "file", "show", c.wi)
		if !strings.Contains(out, "bundle:   "+c.bundle) || !strings.Contains(out, c.status) {
			t.Errorf("pf file show %s (bundle %s) does not show its bundle and %s status:\n%s", c.wi, c.bundle, c.status, out)
		}
		row := w426QueueRow(t, queue, c.wi)
		if row == nil || row["bundle_name"] != c.bundle || row["status"] != c.status {
			t.Errorf("the JSON queue does not list %s with bundle %q and status %q: %v", c.wi, c.bundle, c.status, row)
		}
		stdout, stderr, code := w426Pf(t, inst, d2, "watch", c.wi, "--once")
		if c.bundle == "develop" {
			controlWatch = code
		}
		if strings.Contains(stdout+stderr, "workflow bundle") || strings.Contains(stdout+stderr, workflow.WorkflowFile) {
			t.Errorf("pf watch %s --once (bundle %s) answers through the installed bundle:\nstdout %s\nstderr %s", c.wi, c.bundle, stdout, stderr)
		}
		if c.wi == ran && (code != 0 || !strings.Contains(stdout, c.wi)) {
			t.Errorf("pf watch %s --once (it ran on %s): exit %d, want 0 naming it:\nstdout %s\nstderr %s", c.wi, c.bundle, code, stdout, stderr)
		}
	}
	for _, c := range cases[1:] {
		if _, _, code := w426Pf(t, inst, d2, "watch", c.wi, "--once"); code != controlWatch {
			t.Errorf("pf watch %s --once (canceled on %s) exits %d, the installed-bundle control %d: a removed bundle must display like any other", c.wi, c.bundle, code, controlWatch)
		}
	}

	// only a claim needs the installed bundle: a queued work item on a
	// removed one is refused at submit, naming the bundle, and stays queued
	queued := w426NewWI(t, inst, "w426: queued on a removed lane", "feature-full")
	stdout, stderr, code := w426Pf(t, inst, d2, "submit", queued)
	if code == 0 || !strings.Contains(stdout+stderr, "WFC-123 the work item's lane feature-full is not in this daemon's lane set") {
		t.Fatalf("submit of a work item on a removed bundle: exit %d, want a refusal naming the bundle:\nstdout %s\nstderr %s", code, stdout, stderr)
	}
	if st := inst.wiStatus(t, queued); st != "queued" {
		t.Fatalf("a refused submit must leave the work item queued, got %q", st)
	}
	// workflow contract 1 (AC8): the queue view lists the
	// queued work item a claim refuses for its missing lane (WFC-123)
	// under its table — never silently (the merged --json view's
	// lanes_missing: internal/polyforge/cli's queue tests)
	if out, _, code := w426Pf(t, inst, d2, "queue", "--project", v1Project); code != 0 || !strings.Contains(out, "WFC-123: ") || !strings.Contains(out, "(lane feature-full)") {
		t.Fatalf("pf queue does not list the item on the missing lane under its table: exit %d\n%s", code, out)
	}
}
