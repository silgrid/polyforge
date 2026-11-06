// carries: internal/polyforge/cli/verbs_daemon.go, internal/polyforge/cli/daemon.go, internal/polyforge/daemon/routes.go, internal/polyforge/daemon/daemon.go, internal/polyforge/runtime/runtime.go, internal/polyforge/observe/events.go

// w503_submit_claim_verdict_e2e_test.go: — `pf submit`
// answers the claim's verdict on the spot. Every scenario drives the real
// pf, aihub and fakepi binaries through the v1 harness.

package test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// w503SubmitAnswer is the `pf --json submit` answer shape this suite reads.
type w503SubmitAnswer struct {
	Accepted     bool     `json:"accepted"`
	WorkItemID   string   `json:"work_item_id"`
	State        string   `json:"state"`
	QueuedReason string   `json:"queued_reason"`
	MachineID    string   `json:"machine_id"`
	CapsulePath  string   `json:"capsule_path"`
	MissingRepos []string `json:"missing_repos"`
}

// w503AssertClaimHeld checks, with no wait, that aihub already holds the
// claim of wi: the work item has left `queued`, and while it is not
// past it (running) it carries the claiming session's fingerprint.
func w503AssertClaimHeld(t *testing.T, inst *v1Install, wi, face string) {
	t.Helper()
	view := w140View(t, inst, wi)
	status, _ := view["status"].(string)
	if status == "" || status == "queued" {
		t.Fatalf("%s: right after pf submit returned, aihub shows %s still %q: the claim line was printed before the claim (view %v)", face, wi, status, view)
	}
	if status != "running" {
		return // already ran past its session; it was claimed
	}
	if fp, _ := view["session_fingerprint"].(string); fp == "" {
		t.Fatalf("%s: right after pf submit returned, %s (status %q) carries no session fingerprint: aihub does not hold the claim (view %v)", face, wi, status, view)
	}
}

// TestW503SubmitClaimLineOnlyAfterTheClaim (AC2): an unblocked submit with a
// free slot succeeds as before, and what it says about the claim is true
// when it is printed — the --json answer reads state "claimed" with the
// placement, the human face prints the placement line, and aihub already
// holds the claim at the moment pf returns.
func TestW503SubmitClaimLineOnlyAfterTheClaim(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	d.start()

	// --json face
	wiJ := inst.newWI(t, "w503 json claim", envelopeMicro(), nil)
	out := d.pf(t, "--json", "submit", wiJ)
	var a w503SubmitAnswer
	if err := json.Unmarshal([]byte(w113JSONDocOf(out)), &a); err != nil {
		t.Fatalf("submit --json answer undecodable: %v (%q)", err, out)
	}
	if !a.Accepted || a.WorkItemID == "" {
		t.Fatalf("submit --json must stay accepted: %+v", a)
	}
	if a.State != "claimed" {
		t.Fatalf("submit --json state %q, want \"claimed\" (a free slot claims before the answer) — answer %q", a.State, out)
	}
	w503AssertClaimHeld(t, inst, wiJ, "--json")
	if a.MachineID != "m-e2e" {
		t.Fatalf("submit --json machine_id %q, want m-e2e (answer %q)", a.MachineID, out)
	}
	if capJ := d.capsuleDir(wiJ); a.CapsulePath != capJ {
		t.Fatalf("submit --json capsule_path %q, want %q", a.CapsulePath, capJ)
	}
	if a.QueuedReason != "" {
		t.Fatalf("a claimed submit carries no queued_reason, got %q", a.QueuedReason)
	}

	// human face
	wiH := inst.newWI(t, "w503 human claim", envelopeMicro(), nil)
	outH := d.pf(t, "submit", wiH)
	w503AssertClaimHeld(t, inst, wiH, "human")
	if !strings.Contains(outH, "placement: claimed by machine m-e2e") {
		t.Fatalf("human output must print the placement line once claimed (got %q)", outH)
	}
	if !strings.Contains(outH, "accepted") {
		t.Fatalf("human output keeps the acceptance line (got %q)", outH)
	}

	for _, wi := range []string{wiJ, wiH} {
		w113WaitCapsule(t, d.capsuleDir(wi), 90*time.Second)
	}
}

// w503PfRun runs the real pf against d's socket and answers its stdout,
// stderr and exit code — pfExit keeps stdout only, and a refusal is
// printed on stderr.
func w503PfRun(t *testing.T, d *v1Daemon, args ...string) (string, string, int) {
	t.Helper()
	u := d.inst
	cmd := exec.Command(u.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(),
		"PF_SERVER="+u.url,
		"PF_API_KEY="+u.adminKey,
		"PF_USER_ID=usr_root",
		"PF_DAEMON_SOCKET="+d.sock,
	)
	v1BusinessCmd(t, cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// TestW503SubmitRefusedByUnfinishedBlockerFailsOnTheSpot (AC1): a submit
// whose claim aihub refuses — the work item has an unmerged `blocks`
// blocker — fails on the spot: exit 1, the refusal code, the blocker's
// slug and status and what to do, no acceptance or placement line, and
// nothing left on the daemon's queue the moment pf returns.
func TestW503SubmitRefusedByUnfinishedBlockerFailsOnTheSpot(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	d.start()

	blocker := inst.newWI(t, "w503 blocker, never submitted", envelopeMicro(), nil)
	wi := inst.newWI(t, "w503 blocked submit", envelopeMicro(), nil)
	if _, err := inst.client.AddBlocksDependency(blocker, wi); err != nil {
		t.Fatalf("add blocks edge %s -> %s: %v", blocker, wi, err)
	}
	blockerSlug := w161Slug(t, inst, blocker)

	stdout, stderr, code := w503PfRun(t, d, "submit", wi)
	out := stdout + stderr
	t.Logf("pf submit (refused) exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)

	// Right after pf returned, with no wait: settle-before-answer means
	// the entry has already left the queue.
	st := d.status(t)
	if st.Queue.Pending != 0 || st.Queue.Running != 0 {
		t.Fatalf("a refused submit leaves the daemon queue empty, got pending %d running %d", st.Queue.Pending, st.Queue.Running)
	}
	for _, j := range st.Jobs {
		if j.WorkItemID == wi {
			t.Fatalf("a refused submit leaves no job row, got %+v", j)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(d.dataDir, "submit-queue.json")); err == nil {
		var q struct {
			Entries []struct {
				WorkItemID string `json:"work_item_id"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(raw, &q); err != nil {
			t.Fatalf("submit-queue.json undecodable: %v (%s)", err, raw)
		}
		for _, e := range q.Entries {
			if e.WorkItemID == wi {
				t.Fatalf("a refused submit leaves no persisted queue entry, got %s", raw)
			}
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read submit-queue.json: %v", err)
	}
	if status, _ := w140View(t, inst, wi)["status"].(string); status != "queued" {
		t.Fatalf("a refused submit leaves the work item queued on aihub, got %q", status)
	}

	if code != 1 {
		t.Fatalf("a refused submit exits 1, got %d (output %q)", code, out)
	}
	if !strings.Contains(out, "CORE-409") {
		t.Fatalf("a refused submit prints aihub's refusal code CORE-409 (output %q)", out)
	}
	if strings.Contains(out, "accepted") {
		t.Fatalf("a refused submit prints no acceptance (output %q)", out)
	}
	if strings.Contains(out, "placement:") {
		t.Fatalf("a refused submit prints no placement line (output %q)", out)
	}
	if !strings.Contains(out, blockerSlug) {
		t.Fatalf("a refused submit names the unmerged blocker %s (output %q)", blockerSlug, out)
	}
	if want := blockerSlug + " (queued)"; !strings.Contains(out, want) {
		t.Fatalf("a refused submit names the blocker with its status, %q (output %q)", want, out)
	}
	if !strings.Contains(out, "pf file dep rm") {
		t.Fatalf("a refused submit says how to drop the edge: pf file dep rm (output %q)", out)
	}
}

// TestW503SubmitQueuedWhenSlotsFullAndLaterRefusalRecorded (AC3): with
// every execution slot held, a submit says it is queued waiting for a
// slot — never that a machine claimed it — and points at `pf watch`;
// once a slot frees and aihub refuses the claim, the refusal is still the
// work item's submit_rejected event.
func TestW503SubmitQueuedWhenSlotsFullAndLaterRefusalRecorded(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	d.extraArgs = append(d.extraArgs, "--max-concurrent-jobs", "1")
	d.start()

	// the one slot is held by a work item parked in its first step
	holder := inst.newWI(t, "w503 slot holder", []map[string]any{{"do": "envelope", "id": "fix", "park": true}}, nil)
	t.Cleanup(func() { killOrphans(holder) })
	d.pf(t, "submit", holder)
	waitSpawnCount(t, d, holder, 1, 30*time.Second)

	blocker := inst.newWI(t, "w503 blocker, never submitted", envelopeMicro(), nil)
	wi := inst.newWI(t, "w503 queued submit", envelopeMicro(), nil)
	if _, err := inst.client.AddBlocksDependency(blocker, wi); err != nil {
		t.Fatalf("add blocks edge %s -> %s: %v", blocker, wi, err)
	}

	out := d.pf(t, "submit", wi)
	t.Logf("pf submit (slots full) output:\n%s", out)
	if st := d.status(t); st.Queue.Pending != 1 {
		t.Fatalf("with every slot held the submit waits on the queue: pending %d, want 1", st.Queue.Pending)
	}
	if !strings.Contains(out, "queued") {
		t.Fatalf("a submit waiting for a slot says it is queued (output %q)", out)
	}
	if !strings.Contains(out, "waiting for a free execution slot") {
		t.Fatalf("a submit waiting for a slot says why it is queued: waiting for a free execution slot (output %q)", out)
	}
	if !strings.Contains(out, "pf watch") {
		t.Fatalf("a queued submit points at pf watch for a later refusal (output %q)", out)
	}
	if strings.Contains(out, "claimed by machine") {
		t.Fatalf("a queued submit says no machine claimed it (output %q)", out)
	}
	if strings.Contains(out, "placement:") {
		t.Fatalf("a queued submit prints no placement line (output %q)", out)
	}

	// a slot frees: the dispatcher takes the entry and aihub refuses it
	d.pf(t, "ops", "max", "2")
	var rejected *protocol.EventEntry
	waitDeadline(t, 60*time.Second, func() string {
		return fmt.Sprintf("no submit_rejected event for %s after the slot freed (kinds %v)", wi, d.eventKinds(t, wi))
	}, func() bool {
		for _, e := range d.eventsOf(t, wi) {
			if e.Kind == "submit_rejected" {
				e := e
				rejected = &e
				return true
			}
		}
		return false
	})
	if !strings.Contains(string(rejected.Body), "CORE-409") {
		t.Fatalf("the later refusal's submit_rejected carries aihub's CORE-409, got %s", rejected.Body)
	}
}

// TestW503RefusalHeardOnTheSpotStillRecordsSubmitRejected (review r1#1): a
// refusal the submitting caller heard on the spot is still the work item's
// durable submit_rejected event — what `pf watch` shows and the owner's
// decision feed surfaces — carrying aihub's CORE-409.
func TestW503RefusalHeardOnTheSpotStillRecordsSubmitRejected(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	d.start()

	blocker := inst.newWI(t, "w503 blocker, never submitted", envelopeMicro(), nil)
	wi := inst.newWI(t, "w503 blocked submit, refusal recorded", envelopeMicro(), nil)
	if _, err := inst.client.AddBlocksDependency(blocker, wi); err != nil {
		t.Fatalf("add blocks edge %s -> %s: %v", blocker, wi, err)
	}

	stdout, stderr, code := w503PfRun(t, d, "submit", wi)
	if code != 1 {
		t.Fatalf("a refused submit exits 1, got %d (stdout %q, stderr %q)", code, stdout, stderr)
	}

	var rejected *protocol.EventEntry
	waitDeadline(t, 25*time.Second, func() string {
		return fmt.Sprintf("no submit_rejected event for %s after a refusal heard on the spot (kinds %v)", wi, d.eventKinds(t, wi))
	}, func() bool {
		for _, e := range d.eventsOf(t, wi) {
			if e.Kind == "submit_rejected" {
				e := e
				rejected = &e
				return true
			}
		}
		return false
	})
	if !strings.Contains(string(rejected.Body), "CORE-409") {
		t.Fatalf("the on-the-spot refusal's submit_rejected carries aihub's CORE-409, got %s", rejected.Body)
	}
}
