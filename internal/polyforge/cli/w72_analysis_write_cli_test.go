package cli

// w72_analysis_write_cli_test.go (polyforge#72, u8 — AC3's CLI half):
// inside a task agent `pf task analysis-write` is the ONE write the local
// task guard lets through — every other write verb (the task subactions
// included) keeps its DAEMON-403. The command's content flag reads text,
// @file and stdin exactly like file create's.

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestW72AnalysisWriteAllowedOnlyInAnalysisTask(t *testing.T) {
	var bodies []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /task/analysis-write", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		io.WriteString(w, `{"state":"written"}`)
	})
	sock := newFakeDaemon(t, mux)
	env := map[string]string{
		"PF_DAEMON_SOCKET": sock,
		"PF_TASK_ID":       "task_0123456789abcdef",
		"PF_DAEMON_ID":     "m-w72-cli",
	}
	// the one allowed write: content reaches the daemon untouched
	code, _, stderr := runPf(t, env, "task", "analysis-write", "--content", "### 1. 改动范围\n\n- internal/x\n")
	if code != 0 {
		t.Fatalf("the analysis write must pass the local task guard: %d %s", code, stderr)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], "### 1. 改动范围") {
		t.Fatalf("the write body must carry the draft: %v", bodies)
	}
	// @file reads the draft from a file
	bodies = nil
	dir := t.TempDir()
	file := dir + "/draft.md"
	if err := os.WriteFile(file, []byte("### 2. 证明命令\n\n- go test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runPf(t, env, "task", "analysis-write", "--content", "@"+file)
	if code != 0 {
		t.Fatalf("@file write = %d %s", code, stderr)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], "### 2. 证明命令") {
		t.Fatalf("the @file body must carry the draft: %v", bodies)
	}
	// every other task write subaction keeps its local refusal
	for _, verb := range [][]string{
		{"task", "spawn", "--project", "p", "x"},
		{"task", "cancel", "task_0123456789abcdef"},
	} {
		code, _, stderr := runPf(t, env, verb...)
		if code == 0 || !strings.Contains(stderr, "DAEMON-403") {
			t.Fatalf("pf %v inside a task must keep its local DAEMON-403 refusal: %d %s", verb, code, stderr)
		}
	}
	// and the whole write-verb face outside the exception stays refused
	for _, verb := range [][]string{
		{"file", "create", "--project", "e2e", "--goal", "x"},
		{"submit", "wi_x"},
	} {
		code, _, stderr := runPf(t, env, verb...)
		if code == 0 || !strings.Contains(stderr, "DAEMON-403") {
			t.Fatalf("pf %v inside a task must stay refused: %d %s", verb, code, stderr)
		}
	}
}
