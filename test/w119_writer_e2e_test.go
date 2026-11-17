package test

// carries: internal/polyforge/daemon/proxy.go, internal/polyforge/daemon/stepmem.go, internal/polyforge/cli/verbs_aihub.go, internal/polyforge/cli/render.go, internal/polyforge/cli/stepctx.go, internal/polyforge/aihubclient/client.go

// w119_writer_e2e_test.go — polyforge#119 AC4, the end-to-end proof the
// unit faces cannot give: a real aihub and a real `pf daemon`, one
// project, one shared keyword, two writes — a human session's
// `pf remember` (through the daemon's /aihub proxy) and a step agent's
// `pf step remember` (the fakepi fix step runs it as a scripted command)
// — land with DIFFERENT writer kinds, each with its daemon-stamped
// detail object (session: the machine and the Cell; step: the work
// item, step, attempt and model of the calling dispatch, the model
// read back against the capsule's own execution record), and the human
// CLI's --writer-kind filter returns exactly the one row it names.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// TestW107DaemonCLIWriterStampE2E is the AC4 scenario.
func TestW107DaemonCLIWriterStampE2E(t *testing.T) {
	inst := newV1Install(t)
	// the step's scripted `pf step remember` needs the real pf binary on
	// the step's command PATH
	commandPath := inst.binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	d := newV1Daemon(t, inst, optDaemonKeys("command_path = "+strconv.Quote(commandPath)))
	d.start()

	// The human CLI's config: the daemon's workspace mapped to the
	// project (the placement verbs' mapping check, #113).
	cliCfg := filepath.Join(t.TempDir(), "cli.toml")
	if err := os.WriteFile(cliCfg, []byte("[workspace.\""+d.workspace+"\"]\nprojects = [\""+v1Project+"\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pfHuman := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(inst.pfPath, append([]string{"--config", cliCfg}, args...)...)
		// NO PF_SERVER here: with it the harness would swap in the stand-in
		// cell front and send /aihub/* straight to the hub, bypassing the
		// real daemon whose writer stamp is the point; without it every
		// business call dials the daemon socket itself.
		cmd.Env = append(os.Environ(),
			"PF_USER_ID=usr_root",
			"PF_DAEMON_SOCKET="+d.sock,
		)
		v1BusinessCmd(t, cmd)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("pf %v: %v (stdout %q stderr %q)", args, err, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	// The session write: `pf remember` through the daemon's /aihub proxy.
	const kw = "w119e2ewriter"
	out := pfHuman("remember", "--project", v1Project, "--type", "fact.note",
		"--title", "w119 session write", "the "+kw+" session row")
	var sessionID string
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "stored %s", &sessionID); err != nil || !strings.HasPrefix(sessionID, "mem_") {
		t.Fatalf("session remember answer carries no memory id: %q", out)
	}

	// The step write: a work item on the micro lane whose fix step runs
	// `pf step remember` as its scripted command, then hands off.
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project:    v1Project,
		Goal:       "w119: the step writer stamp end to end",
		Content:    strPtr(v1AcceptanceContent),
		BundleName: strPtr(vehicleLane),
	})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	var item struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	wi := item.ID
	stepCmd := "pf step remember --type experience.wrap --title 'w119: the step write' 'the " + kw + " step row'"
	inst.setPlanFull(t, wi, []map[string]any{
		// the run commands fire before the envelope write (fakepi drive.go:
		// Run first, then the envelope) — the step stores its summary and
		// hands off in the same dispatch
		{"do": "envelope", "id": "fix", "run": []string{stepCmd}},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}, nil)
	inst.trackWI(wi)
	// on failure: the daemon log and the capsule's own records say why
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("daemon log tail:\n%s", d.logTail())
			for _, rel := range []string{"runtime-state/spawn-journal.jsonl", "runtime-state/step-calls.jsonl"} {
				if raw, err := os.ReadFile(filepath.Join(d.capsuleDir(wi), rel)); err == nil {
					t.Logf("%s:\n%s", rel, raw)
				}
			}
		}
	})
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)

	// Both writes are on the hub under the shared keyword.
	type hit struct {
		ID            string          `json:"id"`
		WriterKind    *string         `json:"writer_kind"`
		WriterDetails json.RawMessage `json:"writer_details"`
	}
	var hits []hit
	waitDeadline(t, 30*time.Second, func() string {
		return fmt.Sprintf("the two %s writes never both landed (have %d)", kw, len(hits))
	}, func() bool {
		hits = nil
		var page struct {
			Items []hit `json:"items"`
		}
		path := "/api/v1/memories/search?project=" + url.QueryEscape(v1Project) + "&q=" + url.QueryEscape(kw) + "&excerpt=1"
		if err := inst.client.Do("GET", path, nil, &page); err != nil {
			return false
		}
		hits = page.Items
		return len(hits) == 2
	})
	var sessionRow, stepRow *hit
	for i := range hits {
		if hits[i].ID == sessionID {
			sessionRow = &hits[i]
		} else {
			stepRow = &hits[i]
		}
	}
	if sessionRow == nil || stepRow == nil {
		t.Fatalf("the two rows are not the session %s and another one: %+v", sessionID, hits)
	}

	// The session row: kind session, the daemon's machine and Cell.
	if sessionRow.WriterKind == nil || *sessionRow.WriterKind != "session" {
		t.Fatalf("session row writer_kind = %v, want session", sessionRow.WriterKind)
	}
	var sessDetails map[string]any
	if err := json.Unmarshal(sessionRow.WriterDetails, &sessDetails); err != nil {
		t.Fatalf("session writer_details: %v (%s)", err, sessionRow.WriterDetails)
	}
	if sessDetails["machine"] != d.machineID || sessDetails["cell"] != "default" {
		t.Fatalf("session writer_details = %v, want machine %q cell default", sessDetails, d.machineID)
	}

	// The step row: kind step, the calling dispatch's identity. The model
	// is checked against the capsule's own execution record and the
	// attempt against its spawn journal — both read independently of the
	// daemon's stamp.
	if stepRow.WriterKind == nil || *stepRow.WriterKind != "step" {
		t.Fatalf("step row writer_kind = %v, want step", stepRow.WriterKind)
	}
	var stepDetails map[string]any
	if err := json.Unmarshal(stepRow.WriterDetails, &stepDetails); err != nil {
		t.Fatalf("step writer_details: %v (%s)", err, stepRow.WriterDetails)
	}
	if stepDetails["work_item"] != wi || stepDetails["step"] != "fix" {
		t.Fatalf("step writer_details = %v, want work item %s step fix", stepDetails, wi)
	}
	ex, err := os.ReadFile(filepath.Join(d.capsuleDir(wi), "runtime-state", "execution.json"))
	if err != nil {
		t.Fatalf("execution record: %v", err)
	}
	var record capsule.Execution
	if err := json.Unmarshal(ex, &record); err != nil {
		t.Fatalf("execution record: %v", err)
	}
	fixStep := record.Steps["fix"]
	if fixStep == nil {
		t.Fatalf("the execution record carries no fix step: %s", ex)
	}
	if stepDetails["model"] != fixStep.Candidate {
		t.Fatalf("step writer_details model = %v, want the dispatch's candidate %q", stepDetails["model"], fixStep.Candidate)
	}
	attempt, ok := stepDetails["attempt"].(float64)
	if !ok || attempt < 1 {
		t.Fatalf("step writer_details attempt = %v, want the dispatch's spawn number", stepDetails["attempt"])
	}
	entries, err := capsule.ReadSpawnJournal(d.capsuleDir(wi))
	if err != nil {
		t.Fatalf("spawn journal: %v", err)
	}
	spawned := false
	for _, e := range entries {
		if e.StepID == "fix" && int64(attempt) == int64(e.SpawnN) {
			spawned = true
		}
	}
	if !spawned {
		t.Fatalf("no journaled fix dispatch carries spawn %v: %+v", attempt, entries)
	}

	// The human CLI's filter, through the real daemon proxy: --writer-kind
	// step returns only the step row, session only the session row.
	stepOut := pfHuman("recall", "--project", v1Project, "--writer-kind", "step", kw)
	if !strings.Contains(stepOut, "1 hit(s)") || !strings.Contains(stepOut, stepRow.ID) || strings.Contains(stepOut, sessionID) {
		t.Fatalf("recall --writer-kind step = %q, want only %s", stepOut, stepRow.ID)
	}
	sessOut := pfHuman("recall", "--project", v1Project, "--writer-kind", "session", kw)
	if !strings.Contains(sessOut, "1 hit(s)") || !strings.Contains(sessOut, sessionID) || strings.Contains(sessOut, stepRow.ID) {
		t.Fatalf("recall --writer-kind session = %q, want only %s", sessOut, sessionID)
	}
}
