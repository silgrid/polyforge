package cli

// w433_lane_set_wiring_test.go: the workspace yaml's
// per-step keys are judged over the lane set of the workspace the yaml
// belongs to — the built-in lanes and that workspace's user lanes — at
// both CLI sites that judge them: the doctor's `workspace yaml` row (a
// user lane's step key is not stale) and `pf config set` (a user lane's
// step key is written). An unreadable
// user lane root disables only the user lanes: the built-in lanes still
// answer at both sites, and the root is named.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// w433NotesLane writes the user lane notes of the workspace ws: one agent
// step draft.
func w433NotesLane(t *testing.T, ws string) string {
	t.Helper()
	dir := filepath.Join(workflow.UserLanesRoot(ws), "notes")
	for rel, body := range map[string]string{
		workflow.WorkflowFile: workflowtest.Header("notes") + "steps:\n  - id: draft\n",
		"steps/draft.md":      workflowtest.Definition("draft"),
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// breakUserLanes puts a file where the user lane root of ws belongs.
func breakUserLanes(t *testing.T, ws string) string {
	t.Helper()
	root := workflow.UserLanesRoot(ws)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestW433DoctorWorkspaceYamlJudgesTheWorkspaceLaneSet(t *testing.T) {
	wf := t.TempDir()
	writeLane(t, wf, "autopilot", "steps:\n  - { id: fix }\n")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"machine_id":"m-y","workflows_dir":"`+wf+`","jobs":[],"queue":{}}`)
	})
	sock := newFakeDaemon(t, mux)
	srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
	env := doctorServerConfig(t, srv.URL, "k", sock, "polyforge")
	ws := filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge")
	w433NotesLane(t, ws)
	if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"), []byte("steps:\n  polyforge:\n    fix: { models: [pi:b] }\n    draft: { models: [pi:d] }\n    gate: { models: [pi:c] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, "workspace yaml"); st != "warn" || !strings.Contains(detail, "1 stale key(s)") || !strings.Contains(detail, "steps.polyforge.gate") || strings.Contains(detail, "steps.polyforge.draft") {
		t.Fatalf("a user lane's step key is not stale, a key of no lane is: %s %q", st, detail)
	}
	// the user lane root unreadable: no key is called stale (the built-in
	// fix least of all), the row names the root
	root := breakUserLanes(t, ws)
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, "workspace yaml"); st != "warn" || !strings.Contains(detail, "its step keys are not judged") || !strings.Contains(detail, root) || strings.Contains(detail, "stale key(s)") {
		t.Fatalf("an unreadable user lane root warns naming it and calls no key stale: %s %q", st, detail)
	}
}

func TestW433ConfigSetJudgesTheWorkspaceLaneSet(t *testing.T) {
	f, path := stepsWriteFixture(t)
	w433NotesLane(t, f.ws)
	sha, _ := fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.draft.models", `["pi:x"]`, "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("steps.alpha.draft.models: a user lane's step key must be written: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); !strings.Contains(string(now), "draft:") || !strings.Contains(string(now), "pi:x") {
		t.Fatalf("the user lane's key did not land:\n%s", now)
	}
	// the user lane root unreadable: a built-in step is still written; a
	// key of no built-in step is refused naming the root
	root := breakUserLanes(t, f.ws)
	sha, _ = fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.code.models", `["pi:y"]`, "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("a built-in step key must be written while the user lanes are unreadable: %d %s", code, stderr)
	}
	sha, _ = fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.other.models", `["pi:z"]`, "--cwd", f.ws, "--expect-hash", sha); code == 0 || !strings.Contains(stderr, root) {
		t.Fatalf("a key of no built-in step is refused naming the unreadable root: %d %s", code, stderr)
	}
}
