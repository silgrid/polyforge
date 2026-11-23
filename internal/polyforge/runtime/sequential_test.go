package runtime

// sequential_test.go (polyforge#228 AC1): the scheduler proof.
//
// polyforge#193's serialization is not the extension's own queue: the
// extension registers `executionMode: "sequential"` and pi's agent loop
// (pi-agent-core's executeToolCalls) runs one tool call at a time when any
// offered tool declares it. A source-text assertion cannot see that. This
// test drives a REAL pi against a loopback fake provider that answers one
// assistant turn with two independent `subagent` calls, and holds alpha's
// response for a bounded moment: if the calls overlapped, beta's child
// request would arrive before alpha's response was released. The probe
// reports raw request times and the children's /proc argv; the assertions
// below are this test's, not the probe's.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// exportProbeStepAgent lays out a scratch step-agent source (fake
// credentials and provider config only) and exports it through the REAL
// refresh the daemon runs, so the live probes load exactly the embedded
// resource tree without reading operator or daemon configuration.
func exportProbeStepAgent(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "auth.json"), []byte(`{"probe":{"type":"api_key","key":"probe-fake"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "models.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := refreshStepAgentMinimalSet(src, dst); err != nil {
		t.Fatalf("the daemon refresh must export the built-in step agent tree: %v", err)
	}
	return dst
}

type probeScheduleReport struct {
	Schema     string `json:"schema"`
	Mode       string `json:"mode"`
	PiVersion  string `json:"pi_version"`
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	Sequential struct {
		AlphaRequestMs  int64    `json:"alpha_request_ms"`
		AlphaResponseMs int64    `json:"alpha_response_ms"`
		BetaRequestMs   int64    `json:"beta_request_ms"`
		Tasks           []string `json:"tasks"`
		ParentToolEnd   int      `json:"parent_tool_end"`
		ParentSettled   bool     `json:"parent_settled"`
	} `json:"sequential"`
	ChildArgv [][]string `json:"child_argv"`
	Captures  string     `json:"captures"`
}

// runPiProbe runs the delivered live probe script in one mode and returns
// its report. A long run writes its output to a log and the log is read
// after it, never piped.
func runPiProbe(t *testing.T, args ...string) probeScheduleReport {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("testdata", "pi_subagent_probe.py"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the delivered live probe testdata/pi_subagent_probe.py is missing: %v", err)
	}
	out := filepath.Join(t.TempDir(), "probe.json")
	full := append([]string{script}, args...)
	full = append(full, "--out", out)
	cmd := exec.Command("python3", full...)
	cmd.Dir = t.TempDir()
	logPath := filepath.Join(t.TempDir(), "probe.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("the live probe could not start: %v", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err = <-wait:
	case <-time.After(240 * time.Second):
		_ = cmd.Process.Kill()
		log, _ := os.ReadFile(logPath)
		t.Fatalf("the live probe did not finish within 240s\nprobe log:\n%s", log)
	}
	raw, rerr := os.ReadFile(out)
	if rerr != nil {
		log, _ := os.ReadFile(logPath)
		t.Fatalf("the live probe wrote no report: %v (exit %v)\nprobe log:\n%s", rerr, err, log)
	}
	var report probeScheduleReport
	if jerr := json.Unmarshal(raw, &report); jerr != nil {
		t.Fatalf("the live probe report is not JSON: %v\n%s", jerr, raw)
	}
	if err != nil {
		t.Fatalf("the live probe exited non-zero: %v\nreport: %s", err, raw)
	}
	return report
}

// TestBuiltInSubagentCallsDoNotOverlap: two subagent calls in one model
// turn run one after the other — beta's child must not reach the provider
// before alpha's response was released — and both children carry the five
// isolation flags.
func TestBuiltInSubagentCallsDoNotOverlap(t *testing.T) {
	agentCopy := exportProbeStepAgent(t)
	report := runPiProbe(t, "--step-agent-copy", agentCopy, "--schedule")
	if report.Error != "" {
		t.Fatalf("the schedule probe failed: %s", report.Error)
	}
	seq := report.Sequential
	if seq.AlphaRequestMs == 0 || seq.BetaRequestMs == 0 || seq.AlphaResponseMs == 0 {
		t.Fatalf("the probe did not observe both children and alpha's response: %+v", seq)
	}
	if seq.BetaRequestMs < seq.AlphaResponseMs {
		t.Fatalf("the calls overlapped: beta's child request (%dms) arrived before alpha's response was released (%dms)", seq.BetaRequestMs, seq.AlphaResponseMs)
	}
	if len(seq.Tasks) != 2 || seq.Tasks[0] != "alpha" || seq.Tasks[1] != "beta" {
		t.Fatalf("both children must run, in the model's order: %v", seq.Tasks)
	}
	if seq.ParentToolEnd != 2 || !seq.ParentSettled {
		t.Fatalf("the parent must see both tool ends and settle: %+v", seq)
	}
	if len(report.ChildArgv) == 0 {
		t.Fatal("the probe recorded no child argv from /proc")
	}
	five := []string{"--no-skills", "--no-context-files", "--no-prompt-templates", "--no-extensions", "--no-approve"}
	found := false
	for _, argv := range report.ChildArgv {
		has := 0
		for _, flag := range five {
			for _, a := range argv {
				if a == flag {
					has++
					break
				}
			}
		}
		if has == len(five) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no recorded child argv carries the five isolation flags: %v", report.ChildArgv)
	}
	if !report.OK {
		t.Fatalf("the probe did not report success: %+v", report)
	}
	if !strings.HasPrefix(report.PiVersion, "1.") {
		t.Fatalf("the probe must name the real pi version: %q", report.PiVersion)
	}
}
