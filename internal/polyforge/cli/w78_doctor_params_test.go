package cli

// w78_doctor_params_test.go: pf doctor's `workspace yaml` row names a
// project param value no lane of the set declares for its step
// (polyforge#78, AC2) as what it is — a param the step's lanes do not
// declare — never as a key that names no step of an installed lane: the
// step exists, so that wording would send the reader to rename a step
// key that is right.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW78DoctorNamesAnUndeclaredParam(t *testing.T) {
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
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	// fix is a step of the lane; it declares no param `nope`, and gate is
	// a step of no lane
	yaml := "steps:\n  polyforge:\n    fix: { params: { nope: \"x\" } }\n    gate: { models: [pi:c] }\n"
	if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	st, detail := doctorFind(t, doc, "workspace yaml")
	if st != "warn" {
		t.Fatalf("an undeclared param value warns: %s %q", st, detail)
	}
	if !strings.Contains(detail, "1 param value(s) set a param no lane of the set declares for its step") || !strings.Contains(detail, "steps.polyforge.fix.params.nope") {
		t.Fatalf("the row names the undeclared param as one: %q", detail)
	}
	// the step key that names no step is still called so — and only it
	if !strings.Contains(detail, "1 stale key(s) name no step of an installed lane") || !strings.Contains(detail, "steps.polyforge.gate") {
		t.Fatalf("the stale step key keeps its own wording: %q", detail)
	}
}
