package cli

// w30_queue_submit_test.go — polyforge#30's data-plane defaults:
// `pf queue`'s merged view (the default, over every mapped project)
// skips deactivated projects — their rows never render and their list is
// never even queried (AC6) — while an explicit --project still passes
// through unvalidated (the deactivated project's reads keep answering);
// and `pf submit` surfaces aihub's typed PROJECT_DEACTIVATED claim
// refusal with the explanation and the restore command (AC8) — a
// regression pin of the shared rendering, so the deactivated hint stays
// a submit answer whatever else moves around it.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w30QueueEnv answers a fake aihub and a config env whose one mapped
// workspace carries the projects live (active) and frozen (deactivated).
func w30QueueEnv(t *testing.T) (*httptest.Server, map[string]string, *reqRecorder) {
	t.Helper()
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/live":
			io.WriteString(w, `{"id":"prj_live","name":"live","status":"active"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/frozen":
			io.WriteString(w, `{"id":"prj_frozen","name":"frozen","status":"deactivated"}`)
		case r.URL.Path == "/api/v1/work-items":
			io.WriteString(w, `{"items":[{"id":"wi_q_`+r.URL.Query().Get("project")+`","slug":"`+r.URL.Query().Get("project")+`#1","seq":1,"project":"`+r.URL.Query().Get("project")+`","goal":"row of `+r.URL.Query().Get("project")+`","status":"queued","priority":"normal","labels":[],"reporter":"usr_1","created_at":"2026-11-01T00:00:00Z","updated_at":"2026-11-01T00:00:00Z"}],"total":1}`)
		default:
			io.WriteString(w, `{}`)
		}
	})
	ws := t.TempDir()
	xdg := t.TempDir()
	writeTestToml(t, xdg, "cli.toml", "server = \""+srv.URL+"\"\napi_key = \"k\"\nuser_id = \"u\"\n\n[workspace.\""+ws+"\"]\nprojects = [\"live\", \"frozen\"]\n")
	env := map[string]string{"XDG_CONFIG_HOME": xdg, "PF_SERVER": srv.URL, "PF_API_KEY": "k"}
	env = viaCellProxy(t, env)
	return srv, env, &rec
}

// writeTestToml writes one config file the CLI resolves.
func writeTestToml(t *testing.T, xdg, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestW30QueueSkipsDeactivated: the merged default view renders only the
// active project's rows — the deactivated project's list is never even
// queried — while the explicit --project pass-through keeps answering its
// reads (AC2's read half) through the very same face.
func TestW30QueueSkipsDeactivated(t *testing.T) {
	_, env, rec := w30QueueEnv(t)

	code, so, se := runPf(t, env, "queue")
	if code != 0 {
		t.Fatalf("queue merged: code=%d stderr=%s", code, se)
	}
	if !strings.Contains(so, "row of live") {
		t.Fatalf("the active project's row does not render: %q", so)
	}
	for _, r := range rec.all() {
		if strings.Contains(r.Path, "/api/v1/work-items") && strings.Contains(r.Path, "frozen") {
			t.Fatalf("the merged view queried the deactivated project's list: %q — the default must skip it", r.Path)
		}
	}
	if strings.Contains(so, "row of frozen") {
		t.Fatalf("the deactivated project's row rendered in the default view: %q", so)
	}

	// The explicit pass-through: the deactivated project's reads keep
	// answering (the mapping governs placement, not knowledge).
	code, so, se = runPf(t, env, "queue", "--project", "frozen")
	if code != 0 {
		t.Fatalf("queue --project frozen: code=%d stderr=%s", code, se)
	}
	if !strings.Contains(so, "row of frozen") {
		t.Fatalf("the explicit pass-through lost the deactivated project's rows: %q", so)
	}
}

// TestW30SubmitNamesDeactivatedProject: aihub's typed claim refusal —
// the deactivated project's PROJECT_DEACTIVATED — renders as the
// submit's answer with the explanation and the restore command (AC8).
func TestW30SubmitNamesDeactivatedProject(t *testing.T) {
	var rec reqRecorder
	aihub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t)
	d.answers["POST /jobs/wi_abc/claim"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"code":"CORE-403","detail":"project polyforge is deactivated: it is read-only, restore it first (pf projects restore polyforge)","extra":{"reason":"PROJECT_DEACTIVATED"}}`)
	}
	code, _, se := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock,
	}, "submit", "wi_abc")
	if code != 1 {
		t.Fatalf("the refused submit: exit %d, want 1 (stderr %q)", code, se)
	}
	for _, want := range []string{"was not submitted", "deactivated", "pf projects restore polyforge"} {
		if !strings.Contains(se, want) {
			t.Fatalf("the submit refusal misses %q: %s", want, se)
		}
	}
}
