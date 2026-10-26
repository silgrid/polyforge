package daemon

// stepresult_test.go: the step agent's result delivery (polyforge#214) —
// the daemon stamps the calling step's identity, lands the envelope
// atomically at exactly the derivation's drop path whatever the caller's
// cwd, and refuses (writing nothing) a caller that is no step of this
// daemon, a step of another work item, a disagreeing identity and a
// shape defect.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// dropOf reads the derivation's drop file ("" when absent).
func dropOf(t *testing.T, capDir string, n int64) string {
	t.Helper()
	raw, err := os.ReadFile(driver.PendingResultPath(capDir, n))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestStepResultStampsAndLandsAtTheDropPath(t *testing.T) {
	td := stepDaemon(t, "wi_rs1") // the caller resolves to wi_rs1 / fix / spawn 2
	ji, _ := td.rt.Info("wi_rs1")
	body := `{"outcome":"completed","output":{"summary":"done"},"command_evidence":[{"argv":["go","test"],"exit_code":0,"duration_ms":12}]}`
	code, answer := stepDo(t, td.sock, "POST", "/step/result", body)
	want := filepath.Join(ji.CapsuleDir, capsule.DirRuntimeState, "pending-results", "2.json")
	if code != 200 || !strings.Contains(answer, `"path":"`+want+`"`) {
		t.Fatalf("submit = %d %s", code, answer)
	}
	var res driver.StepResult
	if err := json.Unmarshal([]byte(dropOf(t, ji.CapsuleDir, 2)), &res); err != nil {
		t.Fatalf("drop unreadable: %v", err)
	}
	if res.WIID != "wi_rs1" || res.StepID != "fix" || res.SpawnN != 2 || res.Outcome != driver.OutcomeCompleted ||
		res.SubmittedAt.IsZero() || !strings.Contains(string(res.Output), `"done"`) || !strings.Contains(string(res.CommandEvidence), `"duration_ms":12`) {
		t.Fatalf("landed envelope = %+v", res)
	}
	// an identity that agrees (the canonical id or the slug) is accepted
	ok := `{"wi_id":"wi_rs1","step_id":"fix","spawn_n":2,"outcome":"needs_input","output":{},"question_id":"q-1","question":"which?"}`
	if code, answer := stepDo(t, td.sock, "POST", "/step/result", ok); code != 200 {
		t.Fatalf("agreeing identity = %d %s", code, answer)
	}
	if !strings.Contains(dropOf(t, ji.CapsuleDir, 2), `"question_id":"q-1"`) {
		t.Fatalf("a re-submit replaces the drop: %s", dropOf(t, ji.CapsuleDir, 2))
	}
}

func TestStepResultRefusalsWriteNothing(t *testing.T) {
	td := stepDaemon(t, "wi_rs2")
	ji, _ := td.rt.Info("wi_rs2")
	for _, tc := range []struct {
		body, want string
		status     int
	}{
		{`{"wi_id":"wi_other","outcome":"completed","output":{}}`, "belongs to work item wi_rs2 (the result names", 403},
		{`{"step_id":"review","outcome":"completed","output":{}}`, "the result names step", 403},
		{`{"spawn_n":1,"outcome":"completed","output":{}}`, "the calling step is spawn 2", 403},
		{`{"outcome":"completed","output":{}`, "not a valid JSON envelope", 400},
		{`{"outcome":"done","output":{}}`, "unknown outcome", 400},
		{`{"outcome":"completed","output":["x"]}`, "output is not a JSON object", 400},
		{`{"outcome":"needs_input","output":{}}`, "missing question_id and question", 400},
		{`{"outcome":"needs_input","output":{},"question_id":"bad id!","question":"q"}`, "is malformed", 400},
	} {
		if code, answer := stepDo(t, td.sock, "POST", "/step/result", tc.body); code != tc.status || !strings.Contains(answer, tc.want) {
			t.Fatalf("%s: %d %s, want %d %q", tc.body, code, answer, tc.status, tc.want)
		}
		if got := dropOf(t, ji.CapsuleDir, 2); got != "" {
			t.Fatalf("%s: a refused result was written: %s", tc.body, got)
		}
	}
	td.stepOriginOf = nil // the real resolver: this test process is no step
	if code, answer := stepDo(t, td.sock, "POST", "/step/result", `{"outcome":"completed","output":{}}`); code != 403 || !strings.Contains(answer, "serves step agents only") {
		t.Fatalf("non-step POST: %d %s", code, answer)
	}
	if got := dropOf(t, ji.CapsuleDir, 2); got != "" {
		t.Fatalf("a non-step caller's result was written: %s", got)
	}
}

// TestStepResultFromTheStepProcessTree dials the real UDS face from a
// child process tree: the step-origin guard lets the submit through, the
// envelope lands at the resolved derivation's drop path although the
// caller runs from another directory, and a caller claiming another work
// item writes nothing.
func TestStepResultFromTheStepProcessTree(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_rs3")
	td.submitClaim(t, "wi_rs4")
	ji, _ := td.rt.Info("wi_rs3")
	ji4, _ := td.rt.Info("wi_rs4")
	journal := func(_, step int, startedAt string) {
		writeJournal(t, ji.CapsuleDir, []capsule.SpawnJournalEntry{{WorkItem: "wi_rs3", StepID: "fix", SpawnN: 4, PID: step, StartedAt: startedAt}})
	}
	env := []string{"PF_WI_ID=wi_rs3", "PF_DAEMON_ID=m-test", "STEPMEM_PATH=/step/result",
		`STEPMEM_BODY={"outcome":"completed","output":{}}`}
	if out := stepTree(t, td, "step", journal, append(env, "STEPMEM_CALLER_WI=wi_rs4")...); !strings.Contains(out, "STEPMEM 403") || !strings.Contains(out, "claims PF_WI_ID=wi_rs4") {
		t.Fatalf("a caller claiming another work item: %s", out)
	}
	if got := dropOf(t, ji.CapsuleDir, 4) + dropOf(t, ji4.CapsuleDir, 4); got != "" {
		t.Fatalf("a refused caller's result was written: %s", got)
	}
	if out := stepTree(t, td, "step", journal, append(env, "STEPMEM_CWD="+t.TempDir())...); !strings.Contains(out, "STEPMEM 200") {
		t.Fatalf("step child: %s", out)
	}
	var res driver.StepResult
	if err := json.Unmarshal([]byte(dropOf(t, ji.CapsuleDir, 4)), &res); err != nil || res.WIID != "wi_rs3" || res.StepID != "fix" || res.SpawnN != 4 {
		t.Fatalf("landed envelope = %+v (%v)", res, err)
	}
}
