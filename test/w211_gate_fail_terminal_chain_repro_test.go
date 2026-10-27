package test

// w211_gate_fail_terminal_chain_repro_test.go — polyforge#211 (the #204
// incident shape), root-cause lane, step "repro": a gate FAIL straight to
// terminal must land the final execution state on the capsule git chain
// (a regression bar; see below).
//
// The reported failure theory was that terminal transitions never ride
// an execution-state push. #211's root-cause REFUTED it: in #204 the
// terminal version (failed_gate + verdict) was published and pushed.
// The real cause was the pi session-dir/locator mismatch: pi wrote its
// sessions flat into step-agent-sessions/ while LocateLiveTranscript
// scanned a per-capsule subdir, so session/ stayed empty. That cause is
// pinned by TestSpawnPiSessionDirIsWhereTheLocatorLooks in
// internal/polyforge/runtime/spawn_test.go. fakepi writes straight into
// session/, so this file does not exercise that locator. The tests in
// this file pass on base 2a879f0d. They are kept (owner ruling, #211) as
// regression bars that guard the working terminal-push path.
//
// The scenario below drives the REAL stack end to end (real aihub, real
// pf daemon, the real Go driver, fakepi as the step harness): a
// release-bundle work item whose first and only dispatched step is a
// GATE WITH NO on_fail rewind edge, so its FAIL verdict goes straight
// to workflow_status=failed_gate — no step_done, no rewind, the
// verdict-carrying transition is the workflow's last write. The
// scenario asserts the contract the goal names: after the failed
// terminal close settles, the capsule git chain (the aihub-side bare
// repo) carries the terminal-carrying execution.json — the final
// version AND the FAIL verdict output — not the step_start version the
// #204 audit found, and the chain carries the session transcript the
// dispatch wrote.
//
// The transcript-sync half of the claim (the commit-boundary copy of
// the live harness transcript onto the capsule session path) is
// additionally mechanized at the component level in
// internal/polyforge/observe/terminal_boundary_repro_test.go — the real
// monitor, the real git chain, the real boundary sync; this file owns
// the end-to-end terminal close.
import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestW211GateFailStraightToTerminalLandsFinalStateOnChain is the
// mechanical repro: the exact reported shape (a gate FAIL straight to
// terminal, a single final dispatch with no mid-step state change),
// asserted against the goal's contract. It passes on base 2a879f0d. It
// is a regression bar for the terminal-push path, not a repro of
// #211's cause (see the file header).
func TestW211GateFailStraightToTerminalLandsFinalStateOnChain(t *testing.T) {
	inst := newV1Install(t)

	// A release-bundle work item: the bundle's first step (release) is
	// a gate WITHOUT an on_fail rewind edge — its FAIL verdict is
	// terminal (WF-102), no step_done ever lands on the gate. The single
	// final dispatch: no mid-step state change follows the step_start
	// version — the verdict-carrying transition is the workflow's last
	// write.
	wi := newWIWithBundle(t, inst,
		"w211: gate FAIL straight to terminal — the final version and the "+
			"verdict must reach the capsule git chain",
		"release",
		[]map[string]any{
			{"do": "envelope", "id": "release", "outcome": "gate_failed",
				"output": map[string]any{
					"verdict": "fail",
					"fail_reason": "w211: the release precondition failed — the default " +
						"branch head carries no release procedure",
				}},
		})

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)

	// The hub takes the failed terminal close (SessionEnd(failed) -
	// the work item ends failed, the capsule sealed).
	inst.waitWIStatus(t, wi, "failed", 90*time.Second)
	// ... and the daemon's close fully drained (the seal push, the job
	// release) before the scenario asserts the chain.
	d.settleClose(wi)

	// The capsule's own final state: the gate FAILED straight to
	// terminal, the verdict recorded on the gate step.
	capsule := d.capsuleDir(wi)
	x := readExecution(t, capsule)
	if x.WorkflowStatus != "failed_gate" {
		t.Fatalf("the workflow did not end failed_gate: %s", execDigest(x))
	}
	if st := x.Steps["release"]; st == nil || st.Status != "in_progress" {
		t.Fatalf("the gate must stay in_progress holding its verdict (never done): %s", execDigest(x))
	}
	finalVersion := x.Version
	verdict, _ := x.Steps["release"].Output["verdict"].(string)
	if verdict != "fail" {
		t.Fatalf("the FAIL verdict is not on the gate step: output %v", x.Steps["release"].Output)
	}

	// THE CLAIM (#204): the terminal-carrying version never persisted on
	// the git chain — the chain's execution.json was the step_start one
	// (last published = 2s after step start). Assert the contract: the
	// aihub-side chain carries the FINAL version, the terminal status and
	// the FAIL verdict — the audit material of the failed-terminal job.
	bare := inst.bareRepo(wi)
	chain := chainExecution(t, bare)
	if chain == nil {
		t.Fatalf("SYMPTOM (#204): the chain carries no execution.json at all")
	}
	if chain.Version != finalVersion || chain.WorkflowStatus != "failed_gate" {
		t.Fatalf("SYMPTOM (#204): the chain's execution.json is %s — the terminal-carrying version %d "+
			"(failed_gate, the FAIL verdict) never persisted on the git chain; want version %d",
			execDigest(chain), finalVersion, finalVersion)
	}
	if st := chain.Steps["release"]; st == nil {
		t.Fatalf("SYMPTOM (#204): the chain's terminal state carries no release step: %s", execDigest(chain))
	} else if v, _ := st.Output["verdict"].(string); v != "fail" {
		t.Fatalf("SYMPTOM (#204): the FAIL verdict never reached the chain: release output %v", st.Output)
	}

	// The transcript the dispatch wrote (the capsule session path) rides
	// the chain with the terminal state — the audit material of the job.
	sessions := chainSessionFiles(t, bare)
	if len(sessions) == 0 {
		t.Fatalf("SYMPTOM (#204): the chain carries no session transcript (session/ empty)")
	}
}

// TestW211FinalGateFailAfterStepDoneLandsFinalStateOnChain is the
// claim's full contrast shape: earlier steps DID complete (so the last
// step_done's push covers their state), then the FINAL gate FAILs
// straight to terminal after one long dispatch with no mid-step state
// change — the terminal-carrying version is the only write after the
// last step_done's push. Asserted against the same contract: the chain
// carries the terminal version and the verdict.
func TestW211FinalGateFailAfterStepDoneLandsFinalStateOnChain(t *testing.T) {
	inst := newV1Install(t)

	wi := newWIWithBundle(t, inst,
		"w211: the final gate FAILs straight to terminal after the lane's "+
			"step_dones — the terminal version must reach the chain",
		"release",
		[]map[string]any{
			// The lane's one step_done: the release gate passes (its
			// completed result is the last step_done push covers).
			{"do": "envelope", "id": "release", "outcome": "completed",
				"output": map[string]any{
					"verdict":             "pass",
					"version":             "2.211.0",
					"commit":              "0000000000000000000000000000000000000001",
					"artifacts_published": []string{"v2.211.0"},
				}},
			// The FINAL dispatch: one long quiet gate (no mid-step state
			// change — the envelope lands after the delay) whose FAIL
			// verdict goes straight to terminal (verify has no on_fail).
			{"do": "envelope", "id": "verify", "outcome": "gate_failed", "ms": 3000,
				"output": map[string]any{
					"verdict": "fail",
					"checks": []map[string]any{
						{"name": "release-artifacts", "status": "fail",
							"detail": "the release tag is absent from the origin"},
					},
					"fail_reason": "w211: the release artifact check failed — the tag never landed",
				}},
		})

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)

	inst.waitWIStatus(t, wi, "failed", 90*time.Second)
	d.settleClose(wi)

	// The capsule's final state: verify FAILED straight to terminal,
	// release done before it.
	capsule := d.capsuleDir(wi)
	x := readExecution(t, capsule)
	if x.WorkflowStatus != "failed_gate" {
		t.Fatalf("the workflow did not end failed_gate: %s", execDigest(x))
	}
	if st := x.Steps["release"]; st == nil || st.Status != "done" {
		t.Fatalf("release must be done before the final gate FAIL: %s", execDigest(x))
	}
	if st := x.Steps["verify"]; st == nil || st.Status != "in_progress" {
		t.Fatalf("the final gate must stay in_progress holding its verdict (never done): %s", execDigest(x))
	}
	finalVersion := x.Version

	// THE CLAIM: the terminal-carrying version — the only write after
	// the last step_done's push — never persisted on the chain. Assert
	// the contract: the chain carries it.
	bare := inst.bareRepo(wi)
	chain := chainExecution(t, bare)
	if chain == nil {
		t.Fatalf("SYMPTOM (#204): the chain carries no execution.json at all")
	}
	if chain.Version != finalVersion || chain.WorkflowStatus != "failed_gate" {
		t.Fatalf("SYMPTOM (#204): the chain's execution.json is %s — the terminal-carrying version %d "+
			"(failed_gate, the FAIL verdict) never persisted on the git chain; want version %d",
			execDigest(chain), finalVersion, finalVersion)
	}
	if st := chain.Steps["verify"]; st == nil {
		t.Fatalf("SYMPTOM (#204): the chain's terminal state carries no verify step: %s", execDigest(chain))
	} else if v, _ := st.Output["verdict"].(string); v != "fail" {
		t.Fatalf("SYMPTOM (#204): the FAIL verdict never reached the chain: verify output %v", st.Output)
	}
	if sessions := chainSessionFiles(t, bare); len(sessions) == 0 {
		t.Fatalf("SYMPTOM (#204): the chain carries no session transcript (session/ empty)")
	}
}

// chainExecution answers the remote chain's execution.json view (nil
// when the chain has none).
func chainExecution(t *testing.T, bare string) *executionView {
	t.Helper()
	out, err := gitShow(t, bare, "refs/heads/main:runtime-state/execution.json")
	if err != nil {
		return nil
	}
	var x executionView
	if json.Unmarshal([]byte(out), &x) != nil {
		return nil
	}
	return &x
}

// chainSessionFiles answers the session transcript files the remote
// chain carries (nil when the chain has none).
func chainSessionFiles(t *testing.T, bare string) []string {
	t.Helper()
	out, err := gitShow(t, bare, "refs/heads/main:session")
	if err != nil {
		return nil
	}
	var files []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files
}

// gitShow runs `git --git-dir <bare> show <rev:path>` and answers its
// stdout; the caller decides what an absent path (the error) means.
func gitShow(t *testing.T, bare, revPath string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", "--git-dir", bare, "show", revPath)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=pf-e2e", "GIT_AUTHOR_EMAIL=pf-e2e@invalid",
		"GIT_COMMITTER_NAME=pf-e2e", "GIT_COMMITTER_EMAIL=pf-e2e@invalid",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
