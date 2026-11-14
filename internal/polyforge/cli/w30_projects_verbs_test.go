package cli

// w30_projects_verbs_test.go — polyforge#30's CLI surface against a fake
// aihub (the rec pattern): `pf projects deactivate|restore <id-or-name>`
// send exactly one POST each and report in one line, the typed 409
// refusal (the blocking work items) renders through the shared envelope,
// `pf projects list` asks for the default (active-only) view with no
// include flag, `--show-deactivated` adds the include flag, and the
// deactivated row is marked in the rendered table (AC6).

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// runProjects drives the full CLI the way runPf does.
func runProjects(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	clearEnv(t)
	if _, ok := env["XDG_CONFIG_HOME"]; !ok {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	env = viaCellProxy(t, env)
	for k, v := range env {
		t.Setenv(k, v)
	}
	var stdout, stderr bytes.Buffer
	code := Run(args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestProjectsDeactivateRestoreVerbs: one POST per door, one report line
// each, no prompt (the operation is reversible — only the hard delete
// confirms).
func TestProjectsDeactivateRestoreVerbs(t *testing.T) {
	for _, tc := range []struct {
		sub    string
		path   string
		want   string
		status string
	}{
		{"deactivate", "/api/v1/projects/doomed/deactivate", "deactivated project doomed", "deactivated"},
		{"restore", "/api/v1/projects/doomed/restore", "restored project doomed", "active"},
	} {
		var rec reqRecorder
		srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"id":"prj_doomed","name":"doomed","status":"`+tc.status+`"}`)
		})
		env := map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k"}
		code, so, se := runProjects(t, env, "projects", tc.sub, "doomed")
		if code != 0 {
			t.Fatalf("projects %s: exit %d (stderr %q)", tc.sub, code, se)
		}
		reqs := rec.all()
		if len(reqs) != 1 || reqs[0].Method != "POST" || reqs[0].Path != tc.path {
			t.Fatalf("projects %s requests: %v, want exactly one POST %s", tc.sub, reqs, tc.path)
		}
		if !strings.Contains(so, tc.want) {
			t.Fatalf("projects %s stdout: %q, want %q", tc.sub, so, tc.want)
		}
	}
}

// TestProjectsDeactivateRendersTypedRefusal: the typed 409 (the blocking
// work items) renders through the shared envelope naming the slugs and
// the remedy.
func TestProjectsDeactivateRendersTypedRefusal(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		io.WriteString(w, `{"code":"CORE-409","detail":"project doomed cannot be deactivated while it has non-terminal work items (wrap, fail or cancel them first, or wait for them to finish): doomed#1, doomed#2","extra":{"reason":"NON_TERMINAL_WORK_ITEMS","work_items":["doomed#1","doomed#2"]}}`)
	})
	env := map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k"}
	code, _, se := runProjects(t, env, "projects", "deactivate", "doomed")
	if code != 1 {
		t.Fatalf("the refused deactivate: exit %d, want 1 (stderr %q)", code, se)
	}
	for _, want := range []string{"CORE-409", "doomed#1", "doomed#2", "cannot be deactivated"} {
		if !strings.Contains(se, want) {
			t.Fatalf("the refusal rendering misses %q: %q", want, se)
		}
	}
}

// TestProjectsListViewSelectsDeactivated: the default list carries no
// include flag; --show-deactivated adds include_deactivated=1 and the
// rendered deactivated row is marked.
func TestProjectsListViewSelectsDeactivated(t *testing.T) {
	const canned = `{"items":[{"id":"prj_live","name":"live","status":"active"},{"id":"prj_ghost","name":"ghost","status":"deactivated"}],"total":2}`
	for _, tc := range []struct {
		name     string
		args     []string
		wantPath string
	}{
		{"default hides (no flag)", []string{"projects", "list"}, "/api/v1/projects"},
		{"--show-deactivated includes", []string{"projects", "list", "--show-deactivated"}, "/api/v1/projects?include_deactivated=1"},
	} {
		var rec reqRecorder
		srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, canned)
		})
		env := map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k"}
		code, so, se := runProjects(t, env, tc.args...)
		if code != 0 {
			t.Fatalf("%s: exit %d (stderr %q)", tc.name, code, se)
		}
		if last := rec.last(); last.Path != tc.wantPath {
			t.Fatalf("%s: requested %q, want %q", tc.name, last.Path, tc.wantPath)
		}
		if !strings.Contains(so, "live") || !strings.Contains(so, "ghost") {
			t.Fatalf("%s: the canned rows do not render: %q", tc.name, so)
		}
		if !strings.Contains(so, "deactivated") {
			t.Fatalf("%s: the deactivated row carries no marker: %q", tc.name, so)
		}
	}
}
