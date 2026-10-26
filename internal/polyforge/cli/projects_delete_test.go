package cli

// projects_delete_test.go — the project hard delete verb
// (`pf projects delete <id-or-name> [--yes]`) against
// a fake aihub (the rec pattern): the non-TTY refusal (no --yes on a
// piped stdin — an error naming the escape hatch, zero requests),
// --yes (exactly one plain DELETE), the TTY confirmation (the dry-run
// preflight, then the real DELETE on the typed project name; a wrong
// name cancels after the dry run alone), the typed server refusal
// rendering through the shared envelope, --json's raw passthrough, and
// the help face.

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// cannedDeleteResult is the delete answer of the fake aihub.
const cannedDeleteResult = `{"id":"prj_doomed","name":"doomed","work_items":2,"dry_run":false}`

// runDelete drives the full CLI the way runPf does, with an explicit
// stdin (the confirmation's input face).
func runDelete(t *testing.T, env map[string]string, stdin io.Reader, args ...string) (int, string, string) {
	t.Helper()
	clearEnv(t)
	if _, ok := env["XDG_CONFIG_HOME"]; !ok {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	var stdout, stderr bytes.Buffer
	code := Run(args, stdin, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// withTerminalStdin pins stdinIsTerminal for one test (no pty harness
// exists; the var is the seam).
func withTerminalStdin(t *testing.T, terminal bool) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return terminal }
	t.Cleanup(func() { stdinIsTerminal = old })
}

// TestProjectsDeleteNonTTYRefuses: without --yes on a non-TTY stdin the
// verb refuses naming the escape hatch and performs ZERO requests.
func TestProjectsDeleteNonTTYRefuses(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedDeleteResult)
	})
	env := map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k"}
	code, _, se := runDelete(t, env, strings.NewReader(""), "projects", "delete", "doomed")
	if code != 1 {
		t.Fatalf("non-TTY refusal: exit %d, want 1", code)
	}
	if !strings.Contains(se, "--yes") {
		t.Fatalf("the non-TTY refusal does not name the --yes escape hatch: %q", se)
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("the non-TTY refusal performed %d requests, want 0", n)
	}
}

// TestProjectsDeleteYes: --yes deletes without prompting — exactly one
// plain DELETE (no dry_run).
func TestProjectsDeleteYes(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedDeleteResult)
	})
	env := map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k"}
	code, so, se := runDelete(t, env, strings.NewReader(""), "projects", "delete", "doomed", "--yes")
	if code != 0 {
		t.Fatalf("--yes: exit %d (stderr %q), want 0", code, se)
	}
	reqs := rec.all()
	if len(reqs) != 1 || reqs[0].Method != "DELETE" || reqs[0].Path != "/api/v1/projects/doomed" {
		t.Fatalf("--yes requests: %v, want exactly one plain DELETE", reqs)
	}
	if !strings.Contains(so, "deleted project doomed") {
		t.Fatalf("--yes stdout: %q", so)
	}
}

// TestProjectsDeleteTTYConfirm: a terminal first runs the dry-run
// preflight (the preview on stderr), then the real DELETE on the typed
// project name; a wrong name cancels after the dry run alone.
func TestProjectsDeleteTTYConfirm(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("dry_run") == "1" {
			io.WriteString(w, `{"id":"prj_doomed","name":"doomed","work_items":2,"dry_run":true}`)
			return
		}
		io.WriteString(w, cannedDeleteResult)
	})
	env := map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k"}
	withTerminalStdin(t, true)

	// The right name: dry run, then the real delete.
	code, so, se := runDelete(t, env, strings.NewReader("doomed\n"), "projects", "delete", "doomed")
	if code != 0 {
		t.Fatalf("TTY confirm: exit %d (stderr %q), want 0", code, se)
	}
	if !strings.Contains(se, "terminal check: passed") || !strings.Contains(se, "doomed") {
		t.Fatalf("the preview is missing from stderr: %q", se)
	}
	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("TTY confirm requests: %v, want dry run + real delete", reqs)
	}
	if reqs[0].Path != "/api/v1/projects/doomed?dry_run=1" || reqs[1].Path != "/api/v1/projects/doomed" {
		t.Fatalf("TTY confirm request paths: %v", reqs)
	}
	if !strings.Contains(so, "deleted project doomed") {
		t.Fatalf("TTY confirm stdout: %q", so)
	}

	// A wrong name: only the dry run, no write, exit 1.
	before := len(rec.all())
	code, _, se = runDelete(t, env, strings.NewReader("nope\n"), "projects", "delete", "doomed")
	if code != 1 {
		t.Fatalf("wrong name: exit %d, want 1", code)
	}
	if !strings.Contains(se, "canceled") {
		t.Fatalf("the wrong-name cancel does not say canceled: %q", se)
	}
	if n := len(rec.all()) - before; n != 1 {
		t.Fatalf("wrong name performed %d requests, want the dry run alone", n)
	}
}

// TestProjectsDeleteTypedError: a typed server refusal renders through
// the shared envelope (the code and the offending slug prominent), and
// --json prints the raw response instead.
func TestProjectsDeleteTypedError(t *testing.T) {
	const envelope = `{"code":"CORE-409","detail":"project doomed has non-terminal work items: doomed#1"}`
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, envelope)
	})
	env := map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k"}
	code, _, se := runDelete(t, env, strings.NewReader(""), "projects", "delete", "doomed", "--yes")
	if code != 1 {
		t.Fatalf("typed refusal: exit %d, want 1", code)
	}
	if !strings.Contains(se, "CORE-409") || !strings.Contains(se, "doomed#1") {
		t.Fatalf("the typed refusal stderr lacks the code or the slug: %q", se)
	}
	// --json: the raw envelope on stderr instead.
	code, _, se = runDelete(t, env, strings.NewReader(""), "--json", "projects", "delete", "doomed", "--yes")
	if code != 1 {
		t.Fatalf("--json typed refusal: exit %d, want 1", code)
	}
	if strings.TrimSpace(se) != envelope {
		t.Fatalf("--json stderr is not the raw envelope: %q", se)
	}
}

// TestProjectsDeleteJSONPassthrough: --json hands the success body
// through verbatim.
func TestProjectsDeleteJSONPassthrough(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedDeleteResult)
	})
	env := map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k"}
	code, so, _ := runDelete(t, env, strings.NewReader(""), "--json", "projects", "delete", "doomed", "--yes")
	if code != 0 {
		t.Fatalf("--json delete: exit %d, want 0", code)
	}
	if strings.TrimSpace(so) != cannedDeleteResult {
		t.Fatalf("--json stdout is not the raw body: %q", so)
	}
}

// TestProjectsDeleteHelp: `pf help projects` covers the delete verb and
// the backup-first instruction.
func TestProjectsDeleteHelp(t *testing.T) {
	code, so, _ := runDelete(t, map[string]string{}, strings.NewReader(""), "help", "projects")
	if code != 0 {
		t.Fatalf("pf help projects: exit %d", code)
	}
	if !strings.Contains(so, "projects delete") {
		t.Fatalf("`pf help projects` does not cover the delete verb: %q", so)
	}
	if !strings.Contains(so, "backup") {
		t.Fatalf("`pf help projects` does not carry the backup-first instruction: %q", so)
	}
}
