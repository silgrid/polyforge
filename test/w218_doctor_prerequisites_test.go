package test

// polyforge#218 e2e: the doctor `prerequisites` row — the one additive
// warn-only row. Spec (artifacts/spec.md, "## Requirements"): "pf doctor
// MUST run its existing check set correctly on darwin ... plus the one
// additive warn-only `prerequisites` row reporting missing tools
// (owner-sanctioned), and no new doctor concept introduced"; the blocking
// criterion: "The doctor `prerequisites` row is warn-only; the existing
// check set is otherwise unchanged; no new doctor concept". Today doctor
// has no such row at all.
//
// Driven through the real pf binary's --json face (the verb's own
// documented document) with a PATH whose only missing tool is gh (one of
// the spec's gh, curl, python3, git): the row must WARN naming gh — a
// missing tool never fails the run, and doctor never installs anything.
// The darwin-runtime half (the whole check set on a configured Mac) is
// deferred to the follow-up per the spec.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w218DoctorEnv answers a clean machine environment for the doctor run:
// the test process's env minus the step-agent markers (PF_* / POLYFORGE_* —
// `pf` refuses most verbs inside a workflow step, and this suite runs
// inside one) and the real install's dirs, with HOME/XDG/PATH pointed at
// the scratch machine (the same discipline as w174Env.env).
func w218DoctorEnv(home, pathDir string) []string {
	var out []string
	for _, kv := range os.Environ() {
		k := strings.SplitN(kv, "=", 2)[0]
		switch k {
		case "TMUX", "TMUX_PANE", "HOME", "TMUX_TMPDIR", "PATH", "XDG_CONFIG_HOME",
			"PI_CODING_AGENT_DIR", "CLAUDE_CONFIG_DIR", "POLYFORGE_HARNESS_ROOT", "POLYFORGE_OPERATOR_AGENT_DIR",
			"POLYFORGE_MACHINE_ID", "POLYFORGE_DISPLAY_NAME", "XDG_DATA_HOME", "XDG_STATE_HOME":
			continue
		}
		if strings.HasPrefix(k, "PF_") || strings.HasPrefix(k, "POLYFORGE_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "PATH="+pathDir)
}

func TestW218DoctorPrerequisitesRowWarns(t *testing.T) {
	pf := filepath.Join(v1Bins(t), "pf")
	home := t.TempDir()
	// a PATH with every tool present except gh
	farm := w218FarmDir(t, "curl", "python3", "git", "tmux")
	var out, errb bytes.Buffer
	c := exec.Command(pf, "--json", "doctor")
	c.Env = w218DoctorEnv(home, farm)
	c.Stdout, c.Stderr = &out, &errb
	// the exit code is not the assertion (unrelated rows may fail with no
	// config); the row is
	if err := c.Run(); err != nil {
		t.Logf("pf --json doctor exited non-zero (expected: no config, rows report): %v\nstderr: %s", err, errb.String())
	}
	var rep struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("pf --json doctor must emit the checks document; observed:\n%s\nstderr: %s\nerror: %v", out.String(), errb.String(), err)
	}
	for _, ch := range rep.Checks {
		if strings.Contains(strings.ToLower(ch.Name), "prerequisit") {
			if ch.Status != "warn" {
				t.Fatalf("the prerequisites row must be warn-only (a missing tool never fails the run); got status %q, detail %q", ch.Status, ch.Detail)
			}
			if !strings.Contains(ch.Name+ch.Detail, "gh") {
				t.Fatalf("the prerequisites row must name the missing tool (gh is absent from PATH); name %q, detail %q", ch.Name, ch.Detail)
			}
			return
		}
	}
	t.Fatalf("pf doctor must carry the warn-only prerequisites row: with gh absent from PATH the row must warn naming it; checks: %+v", rep.Checks)
}
