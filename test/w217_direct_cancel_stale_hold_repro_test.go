package test

// w217 (polyforge#217, root-cause lane, step "repro"): the direct-cancel
// stale hold — a data-plane terminal state never releases the daemon
// hold.
//
// The reported operator sequence (2026-10-19, #215), reproduced verbatim
// against the REAL binaries (aihub, pf daemon, pf CLI, fakepi — the v1
// dogfood harness):
//
//   - a live daemon holds a work item as a RETAINED PAUSED record
//     (pf pause parked the driver on a needs_input question; the hub
//     keeps the session row, clean_pause set);
//   - the operator cancels it with `pf cancel` — the verb's own wire,
//     DIRECT to aihub, no daemon involvement. The hub takes the cancel
//     from paused and clears the session row in the same transaction
//     (a terminal work item never stays held hub-side);
//   - the holder daemon's job table keeps the record paused: `pf ops
//     status` shows paused while the hub says canceled;
//   - `pf ops end-session` — the only named recovery door — refuses the
//     now-terminal work item (CORE-410), and `pf resume` refuses the
//     same way (its clean_pause PATCH hits the terminal gate); no
//     operator path releases the stale hold short of a daemon restart,
//     which interrupts every live lane on the machine.
//
// The scenario asserts the contract the goal names: after the data-plane
// cancel, the holder daemon's job table must not keep the record held
// against a terminal hub record — the hold is released WITHOUT a daemon
// restart. The release may land by itself (a cancel routed through the
// daemon, or a holder that reconciles hub terminal status) within the
// first window, or through the operator doors the scenario exercises
// when nothing self-released (pf resume, pf ops end-session — the doors
// that exist today). Today the test FAILS at exactly the reported
// point: both windows expire with the record still held paused, and
// both doors answer CORE-410.
import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// TestW217DirectCancelStaleDaemonHold is the mechanical repro: the exact
// operator sequence of the report, asserted against the goal's
// contract. It fails today for the reported reason (the stale hold is
// never released without a daemon restart) and passes once the fix
// releases it — automatically or through the exercised doors.
func TestW217DirectCancelStaleDaemonHold(t *testing.T) {
	inst := newV1Install(t)
	const qid = "q_w217"
	wi := inst.newWI(t, "w217: direct cancel vs the daemon hold (retained paused record)",
		[]map[string]any{
			// The grill envelope parks the driver on an open question:
			// the designed-still shape the operator pause lands on (the
			// retention retains the record, the session row stays).
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "w217: park here so the pause retention holds."},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	waitQuestion(t, d, wi, qid, 30*time.Second)

	// The operator pause: the daemon RETAINS the record paused — the
	// reported held shape. The hub keeps the session row (clean_pause).
	d.pf(t, "pause", wi)
	inst.waitWIStatus(t, wi, "paused", 30*time.Second)
	if h := w217Hold(t, d, wi); h == nil || h.Status != "paused" {
		t.Fatalf("w217: the paused hold never engaged (status %+v)", h)
	}

	// The operator's cancel — `pf cancel`'s own wire, DIRECT to aihub
	// (the data-plane path; the holder daemon is not involved). The hub
	// takes it from paused and clears the session row atomically.
	d.pf(t, "cancel", wi)
	inst.waitWIStatus(t, wi, "canceled", 30*time.Second)

	// The contract: the holder daemon must not keep the record held
	// against the terminal hub record — released WITHOUT a daemon
	// restart. First window: a release that lands by itself (a cancel
	// routed through the daemon settles here immediately; a holder that
	// reconciles hub terminal status lands within its notice/sweep
	// time).
	deadline := time.Now().Add(90 * time.Second)
	for w217Hold(t, d, wi) != nil && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	if h := w217Hold(t, d, wi); h != nil {
		// Nothing self-released: exercise the operator doors that exist
		// today — pf resume (the held record's own door) and pf ops
		// end-session (the named recovery door). Both refuse the
		// terminal-held record CORE-410 today; a release that lands on
		// touch answers here. Their outputs feed the failure message;
		// the doors' own exit shapes are NOT asserted (a fix may change
		// what they answer — only the hold's release is the contract).
		resumeExit, resumeOut, resumeErr := w217CLI(d, "resume", wi)
		endExit, endOut, endErr := w217CLI(d, "ops", "end-session", wi,
			"--reason", "w217: release the stale terminal-held record")
		deadline = time.Now().Add(30 * time.Second)
		for w217Hold(t, d, wi) != nil && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
		}
		if h := w217Hold(t, d, wi); h != nil {
			t.Fatalf("w217: pf cancel (direct to aihub) canceled %s, but the daemon job table still holds it %q — ops shows paused while the hub says canceled; no operator path released the stale hold without a daemon restart"+w217Door("pf resume", resumeExit, resumeOut, resumeErr)+w217Door("pf ops end-session", endExit, endOut, endErr),
				wiSlug(t, inst, wi), h.Status)
		}
	}
}

// w217Hold reads the daemon's status face and answers the held job
// record for wi — the paused/retained entry the operator sees in
// `pf ops status` — or nil when the hold is released (the record is
// gone from the job table; a released record is dropped, not
// re-stated).
func w217Hold(t *testing.T, d *v1Daemon, wi string) *cli.StatusJob {
	t.Helper()
	st := d.status(t)
	for i := range st.Jobs {
		if st.Jobs[i].WorkItemID == wi {
			return &st.Jobs[i]
		}
	}
	return nil
}

// w217CLI runs the real pf CLI against d's socket and server, any exit
// allowed, both streams captured — the door probes must not assert
// today's refusal shapes.
func w217CLI(d *v1Daemon, args ...string) (int, string, string) {
	cmd := exec.Command(d.inst.pfPath, append([]string{"--config", hermeticConfigPath(d.t)}, args...)...)
	cmd.Env = append(os.Environ(), "PF_SERVER="+d.inst.url, "PF_API_KEY="+d.inst.adminKey,
		"PF_USER_ID=usr_root", "PF_DAEMON_SOCKET="+d.sock)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return code, stdout.String(), stderr.String()
}

// w217Door renders one exercised door's answer for the failure message.
func w217Door(name string, code int, stdout, stderr string) string {
	out := strings.TrimSpace(strings.Join(strings.Fields(stdout), " "))
	err := strings.TrimSpace(strings.Join(strings.Fields(stderr), " "))
	if err != "" {
		out += " " + err
	}
	return "; " + name + " answered exit " + strconv.Itoa(code) + ": " + out
}
