package test

import (
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// v1_s07: repeated gate failures are bounded, driver/envelope edition.
// The legacy storm watched the record stream for consecutive provider
// errors; the driver-era equivalent storm window is the workflow's OWN
// gate-fail ladder: the review gate of the vehicle lane FAILs with a
// populated error verdict four times, the on_fail rewind-to-fix policy
// fires three times (a test-local micro copy with max_rewinds: 3 — micro
// ships 2, which reaches no ladder), and the fourth FAIL hits the bound:
// under workflow contract 4 `pf step handoff` refuses that fail at the
// call (it cites no owner message; design #483 §5.1), nothing is recorded,
// the gate's turn ends and its resumed session hands off blocked (the
// scripted stand-in for asking the owner); the owner cancels it. Every
// hand-off is on the call ledger, every rewind and archived attempt on
// execution.json.
func TestV1S07ProviderErrorStorm(t *testing.T) {
	inst := newV1Install(t)
	inst.microMaxRewinds = 3 // test-local: three rewinds on review→fix
	verdict := map[string]any{
		"fail_reason": "upstream connection reset after 2 retries (fake harness injected)",
		"verdict":     "fail",
	}
	wi := inst.newWI(t, "s07: consecutive gate failures, bounded",
		[]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed"},
			// FAIL #1 → rewind [fix, review) → fix.
			{"do": "envelope", "id": "review", "outcome": "gate_failed", "output": verdict},
			{"do": "envelope", "id": "fix", "outcome": "completed"},
			// FAIL #2 → rewind #2.
			{"do": "envelope", "id": "review", "outcome": "gate_failed", "output": verdict},
			{"do": "envelope", "id": "fix", "outcome": "completed"},
			// FAIL #3 → rewind #3 (the last the bounds allow).
			{"do": "envelope", "id": "review", "outcome": "gate_failed", "output": verdict},
			{"do": "envelope", "id": "fix", "outcome": "completed"},
			// FAIL #4 → the bounds are spent: the fail is refused at the
			// call; the gate's turn ends without a hand-off.
			{"do": "envelope", "id": "review", "outcome": "gate_failed", "output": verdict},
			// the resumed review cannot fail again without the owner: it
			// parks the work item; the owner cancels it.
			{"do": "envelope", "id": "review", "outcome": "incomplete",
				"blocker": map[string]any{"reason": "the rewind budget is spent: the owner decides"}},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	// The bounded park: exactly three executed rewinds, the fourth fail
	// refused at the call (exhaustThenPark checks the refusal), the gate
	// NEVER done — its resumed session parked the work item blocked.
	exhaustThenPark(t, d, wi, "review", 240*time.Second)
	capDir := d.capsuleDir(wi)
	x := readExecution(t, capDir)
	if n := int(x.RewindStats["count"].(float64)); n != 3 {
		t.Fatalf("rewind count %v, want exactly 3 (the fourth hit the bound)", x.RewindStats["count"])
	}
	if ver := x.Steps["review"]; ver == nil || ver.Status == "done" {
		t.Fatalf("the gate step must never be done, got %+v", ver)
	}
	// The three executed rewinds archived fix's superseded outputs as
	// gate_fail_rewind attempts — the preference order's archive face.
	fixArchives := 0
	for _, a := range x.Steps["fix"].Attempts {
		if a["cause"] == "gate_fail_rewind" {
			fixArchives++
		}
	}
	if fixArchives != 3 {
		t.Fatalf("fix archived %d gate_fail_rewind attempts, want 3: %+v", fixArchives, x.Steps["fix"].Attempts)
	}
	// Every dispatch's hand-off is on the call ledger, in plan order — the
	// three accepted fails carrying their reason; the fourth refused (no
	// accepted hand-off of dispatch 8); the resumed review's blocked.
	for i, want := range []string{
		stepcall.StatusDone, stepcall.StatusFail,
		stepcall.StatusDone, stepcall.StatusFail,
		stepcall.StatusDone, stepcall.StatusFail,
		stepcall.StatusDone,
	} {
		step := "fix"
		if i%2 == 1 {
			step = "review"
		}
		h := acceptedHandoff(t, capDir, step, i+1)
		if h.Handoff.Status != want {
			t.Fatalf("dispatch %d (%s) handed off %s, want %s", i+1, step, h.Handoff.Status, want)
		}
		if want == stepcall.StatusFail && !strings.Contains(h.Handoff.Reason, "connection reset") {
			t.Fatalf("the fail of dispatch %d carries no reason: %+v", i+1, h.Handoff)
		}
	}
	if l := stepcall.HandoffOf(ledgerLines(t, capDir), "review", 8); l != nil {
		t.Fatalf("the fourth fail was accepted: %+v", l)
	}
	if h := acceptedHandoff(t, capDir, "review", 9); h.Handoff.Status != stepcall.StatusBlocked {
		t.Fatalf("the resumed review handed off %s, want blocked", h.Handoff.Status)
	}

	// The park closed the WI blocked and the owner's cancel ended it
	// canceled (exhaustThenPark); the ladder itself was no death.

	// No needs_recovery for the whole ladder: the failed exit is no death.
	// The rewind cycles never left the driver (no re-derivation, no
	// storm) — the storm bound that matters is the workflow's own.
	kinds := d.eventKinds(t, wi)
	if n := countOf(kinds, "needs_recovery"); n != 0 {
		t.Fatalf("needs_recovery fired %d times, want 0 (a settled failed workflow's exit is no death): %v", n, kinds)
	}
	if n := countOf(kinds, "wi_agent_rederived"); n != 0 {
		t.Fatalf("re-derivation count %d, want 0 (the ladder ran inside one driver): %v", n, kinds)
	}
	// The failed close cleared the session (no holder on a terminal WI)
	// and the sealed chain carries the bounded ladder's archives. (The
	// terminal workflow_status itself rides the last C3 push wave, which
	// races the failed close — the local execution.json is the authority
	// for the verdict; the chain reliably carries the rewind archives.)
	if sess := inst.sessionOf(t, wi); sess.Fingerprint != "" {
		t.Fatalf("failed work item still names session %q", sess.Fingerprint)
	}
	waitUntil(t, 30*time.Second, "the sealed chain never carried the rewind archives", func() bool {
		return inst.bareHasPath(t, wi, "runtime-state/execution.json") &&
			strings.Contains(inst.bareFileAtMain(t, wi, "runtime-state/execution.json"), "gate_fail_rewind")
	})
	if n := len(spawnJournal(t, capDir)); n != 9 {
		t.Fatalf("spawn journal has %d entries, want 9 (eight step processes, then the resumed review that parks; nothing more is dispatched)", n)
	}
}
