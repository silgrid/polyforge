package runtime

// pi_subagent_probe_test.go (polyforge#228 AC2): the live isolation proof.
//
// The delivered probe script (testdata/pi_subagent_probe.py) runs REAL pi
// twice over the same scratch tree: the positive control reproduces the old
// loading (no isolation flags, project resources approved), the negative
// control is the production child profile (the five isolation flags). Eight
// unique markers stand for the #165 §2 automatic-loading surface; the
// expectations are 8/8 visible in the positive control and 0/8 in the
// negative one. The shadow case runs the builtin extension over a cwd that
// carries its own .pi/agents/worker.md: the child must get the builtin
// worker body, never the shadow marker, and a project scope must be refused
// with no second child.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
)

// probeSurface is the eight automatic-loading probes of #165 §2 and the
// channel that observes each one (the requirement's list, not the probe's).
var probeSurface = []string{
	"agent-agents",
	"agent-skill",
	"agent-prompt",
	"cwd-agents",
	"cwd-agents-skills",
	"cwd-pi-skills",
	"cwd-pi-prompts",
	"workspace-agents",
}

type probeEntry struct {
	Marker  string `json:"marker"`
	Channel string `json:"channel"`
	Visible bool   `json:"visible"`
}

type probeControl struct {
	Probes     map[string]probeEntry `json:"probes"`
	Requests   int                   `json:"requests"`
	Commands   int                   `json:"commands"`
	StderrTail string                `json:"stderr_tail"`
}

type probeShadow struct {
	ChildRequests        int        `json:"child_requests"`
	ChildArgv            [][]string `json:"child_argv"`
	BuiltinWorkerVisible bool       `json:"builtin_worker_visible"`
	ShadowMarkerVisible  bool       `json:"shadow_marker_visible"`
	ChildStderrTail      string     `json:"child_stderr_tail"`
	WorkerResult         struct {
		Agent       string `json:"agent"`
		AgentSource string `json:"agent_source"`
		ExitCode    int    `json:"exit_code"`
		IsError     bool   `json:"is_error"`
	} `json:"worker_result"`
	ProjectScopeResult struct {
		IsError bool `json:"is_error"`
		Refusal bool `json:"refusal"`
	} `json:"project_scope_result"`
}

type probeCaptureReport struct {
	Schema     string                  `json:"schema"`
	Mode       string                  `json:"mode"`
	PiVersion  string                  `json:"pi_version"`
	Error      string                  `json:"error"`
	OK         bool                    `json:"ok"`
	Digests    map[string]any          `json:"digests"`
	Controls   map[string]probeControl `json:"controls"`
	Shadow     probeShadow             `json:"shadow"`
	ProbePaths map[string]string       `json:"probe_paths"`
	Captures   string                  `json:"captures"`
}

// runCaptureProbe runs the delivered probe in capture mode and returns its
// report; a long command writes to a log and the log is read afterwards.
func runCaptureProbe(t *testing.T, agentCopy string) probeCaptureReport {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("testdata", "pi_subagent_probe.py"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the delivered live probe testdata/pi_subagent_probe.py is missing: %v", err)
	}
	out := filepath.Join(t.TempDir(), "capture.json")
	logPath := filepath.Join(t.TempDir(), "probe.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command("python3", script, "--step-agent-copy", agentCopy, "--capture",
		"--work-dir", filepath.Join(t.TempDir(), "probe-work"), "--out", out)
	cmd.Dir = t.TempDir()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("the live capture probe could not start: %v", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var runErr error
	select {
	case runErr = <-wait:
	case <-time.After(300 * time.Second):
		_ = cmd.Process.Kill()
		log, _ := os.ReadFile(logPath)
		t.Fatalf("the live capture probe did not finish within 300s\nprobe log:\n%s", log)
	}
	raw, rerr := os.ReadFile(out)
	if rerr != nil {
		log, _ := os.ReadFile(logPath)
		t.Fatalf("the live capture probe wrote no report: %v (exit %v)\nprobe log:\n%s", rerr, runErr, log)
	}
	var report probeCaptureReport
	if jerr := json.Unmarshal(raw, &report); jerr != nil {
		t.Fatalf("the capture report is not JSON: %v\n%s", jerr, raw)
	}
	if runErr != nil {
		t.Fatalf("the live capture probe exited non-zero: %v\nreport: %s", runErr, raw)
	}
	return report
}

func embedDigest(t *testing.T, rel string) string {
	t.Helper()
	raw, err := stepagentassets.FS.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// TestPiSubagentLiveIsolationProbe: the refreshed step-agent copy's builtin
// resources are the embedded ones, the eight automatic-loading probes are
// all visible under old loading and none under the production isolation
// flags, and the cwd's .pi/agents/worker.md shadow never reaches the child.
func TestPiSubagentLiveIsolationProbe(t *testing.T) {
	agentCopy := exportProbeStepAgent(t)
	report := runCaptureProbe(t, agentCopy)
	if !report.OK || report.Error != "" {
		t.Fatalf("the capture probe did not complete: ok=%v error=%q", report.OK, report.Error)
	}
	if report.Mode != "capture" {
		t.Fatalf("probe mode = %q", report.Mode)
	}
	if got := report.Digests["worker_sha256"]; got != embedDigest(t, "agents/worker.md") {
		t.Fatalf("the probed worker is not the embedded one: %v", got)
	}
	if got := report.Digests["extension_sha256"]; got != embedDigest(t, "extensions/subagent/index.ts") {
		t.Fatalf("the probed extension is not the embedded one: %v", got)
	}
	positive, ok := report.Controls["positive"]
	if !ok {
		t.Fatalf("the report carries no positive control: %+v", report.Controls)
	}
	negative, ok := report.Controls["negative"]
	if !ok {
		t.Fatalf("the report carries no negative control: %+v", report.Controls)
	}
	for _, id := range probeSurface {
		p, ok := positive.Probes[id]
		if !ok {
			t.Errorf("the positive control has no probe %q", id)
			continue
		}
		if !p.Visible {
			t.Errorf("positive control (old loading): probe %q (%s, channel %s) must be visible", id, p.Marker, p.Channel)
		}
		n, ok := negative.Probes[id]
		if !ok {
			t.Errorf("the negative control has no probe %q", id)
			continue
		}
		if n.Visible {
			t.Errorf("negative control (the five isolation flags): probe %q (%s, channel %s) must be invisible", id, n.Marker, n.Channel)
		}
	}
	if positive.Requests == 0 {
		t.Error("the positive control made no model request, so the system-channel probes were not observed")
	}
	if negative.Requests == 0 {
		t.Error("the negative control made no model request, so the system-channel probes were not observed")
	}

	shadow := report.Shadow
	if shadow.ChildRequests != 1 {
		t.Fatalf("the shadow case started %d child(ren), want exactly the builtin worker call", shadow.ChildRequests)
	}
	if !shadow.BuiltinWorkerVisible {
		t.Error("the child request lacks the builtin worker body")
	}
	if shadow.ShadowMarkerVisible {
		t.Error("the child request carries the cwd .pi/agents/worker.md shadow marker")
	}
	if shadow.WorkerResult.Agent != "worker" || shadow.WorkerResult.AgentSource != "user" ||
		shadow.WorkerResult.ExitCode != 0 || shadow.WorkerResult.IsError {
		t.Fatalf("the shadow case's worker result = %+v, want the builtin worker (agent worker, source user, exit 0)", shadow.WorkerResult)
	}
	if !shadow.ProjectScopeResult.IsError || !shadow.ProjectScopeResult.Refusal {
		t.Fatalf("agentScope=project must be refused without a child: %+v", shadow.ProjectScopeResult)
	}
	if len(shadow.ChildArgv) == 0 {
		t.Error("the shadow case recorded no child argv from /proc")
	}
}
