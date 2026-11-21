package driver

// c3_fast_check_lines_test.go: develop's build and sync declare the
// fast-check proof, and each lane's `command` — {repo} shown as <repo> —
// appears verbatim in the step's rendered prompt, so a step agent finds
// the exact line a refusal quotes (polyforge#211). The daemon holds no
// repository's command: the declared lines name only the lane's
// fast_checks.py.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestC3FastCheckLines(t *testing.T) {
	wf, err := LoadWorkflowGraph(filepath.Join(shippedWorkflowsDir, developBundle))
	if err != nil {
		t.Fatalf("LoadWorkflowGraph: %v", err)
	}
	declared := map[string]string{}
	for _, s := range wf.Steps {
		if s.FastCheckProof != nil {
			declared[s.ID] = s.FastCheckProof.Command
			if s.FastCheckProof.Result == "" {
				t.Errorf("%s: the declaration carries no result path", s.ID)
			}
			if strings.ContainsAny(s.FastCheckProof.Command, "\r\n") {
				t.Errorf("%s: the command is not one line", s.ID)
			}
			if !strings.Contains(s.FastCheckProof.Command, "fast_checks.py") {
				t.Errorf("%s: the command does not run the lane's fast_checks.py: %q", s.ID, s.FastCheckProof.Command)
			}
			for _, tool := range []string{"go ", "npm ", "make "} {
				if strings.Contains(s.FastCheckProof.Command, tool) {
					t.Errorf("%s: the command names %q, a repository tool the daemon must not know", s.ID, tool)
				}
			}
		}
	}
	if len(declared) != 2 || declared["build"] == "" || declared["sync"] == "" {
		t.Fatalf("develop declares fast_check_proof on %v, want build and sync alone", declared)
	}
	for _, id := range []string{"build", "sync"} {
		line := strings.ReplaceAll(declared[id], "{repo}", "<repo>")
		if prompt := spawnStepPrompt(t, developBundle, id); !strings.Contains(prompt, line) {
			t.Errorf("%s: the rendered prompt does not carry the declaration's line %q", id, line)
		}
	}
}
