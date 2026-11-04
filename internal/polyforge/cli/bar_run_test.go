package cli

// bar_run_test.go: `pf bar-run -- '<line>'` runs the carried command line
// byte-identically in the current directory, posts {argv: [line], cwd,
// exit, duration_ms} to the daemon's /step/bar-run, exits with the
// command's own status (a failed run is posted too), and refuses outside a
// step and when the record cannot be posted.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func barRunEnv(t *testing.T, status int) (map[string]string, *reqRecorder) {
	t.Helper()
	rec := &reqRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /step/bar-run", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if status != 200 {
			w.WriteHeader(status)
			io.WriteString(w, `{"code":"DAEMON-403","detail":"/step/bar-run serves the deliver step only (the caller is step \"code\" of wi_st)"}`)
			return
		}
		io.WriteString(w, `{"repo":"demo","spawn_n":3,"head":"abc"}`)
	})
	sock := newFakeDaemon(t, mux)
	return map[string]string{"PF_WI_ID": "wi_st", "PF_DAEMON_ID": "m-test", "PF_DAEMON_SOCKET": sock}, rec
}

func TestBarRunRunsTheLineAndRecordsIt(t *testing.T) {
	env, rec := barRunEnv(t, 200)
	dir := t.TempDir()
	line := `printf '%s|' "a b" > out.txt; test -s out.txt`
	code, _, stderr := runPfInCwd(t, dir, env, "bar-run", "--", line)
	if code != 0 || !strings.Contains(stderr, "recorded exit 0") {
		t.Fatalf("bar-run: exit %d %s", code, stderr)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); string(b) != "a b|" {
		t.Fatalf("the line did not run in the cwd byte-identically: %q", b)
	}
	var body struct {
		Argv       []string `json:"argv"`
		Cwd        string   `json:"cwd"`
		Exit       int      `json:"exit"`
		DurationMS int64    `json:"duration_ms"`
	}
	got := rec.last()
	if got.Path != "/step/bar-run" || json.Unmarshal([]byte(got.Body), &body) != nil {
		t.Fatalf("daemon request = %+v", got)
	}
	if len(body.Argv) != 1 || body.Argv[0] != line || body.Cwd != dir || body.Exit != 0 || body.DurationMS < 0 {
		t.Fatalf("posted %+v, want the line as one argv element", body)
	}
}

func TestBarRunFailedCommandIsRecordedAndSurfaced(t *testing.T) {
	env, rec := barRunEnv(t, 200)
	code, _, stderr := runPfInCwd(t, t.TempDir(), env, "bar-run", "--", "exit 7")
	if code != 7 || !strings.Contains(stderr, "exited 7 (recorded)") {
		t.Fatalf("failed run: exit %d %s", code, stderr)
	}
	if !strings.Contains(rec.last().Body, `"exit":7`) {
		t.Fatalf("the failed run was not posted: %+v", rec.last())
	}
}

func TestBarRunRefusals(t *testing.T) {
	env, rec := barRunEnv(t, 403)
	code, _, stderr := runPfInCwd(t, t.TempDir(), env, "bar-run", "--", "true")
	if code != 1 || !strings.Contains(stderr, "NOT recorded") || !strings.Contains(stderr, "deliver step only") {
		t.Fatalf("refused record: exit %d %s", code, stderr)
	}
	n := len(rec.all())
	if code, _, stderr := runPf(t, map[string]string{}, "bar-run", "--", "true"); code != 1 || !strings.Contains(stderr, "deliver step only") {
		t.Fatalf("outside a step: exit %d %s", code, stderr)
	}
	if code, _, stderr := runPf(t, env, "bar-run", "--", "a", "b"); code != 1 || !strings.Contains(stderr, "exactly one command line") {
		t.Fatalf("two arguments: exit %d %s", code, stderr)
	}
	if len(rec.all()) != n {
		t.Fatalf("a refused invocation reached the daemon")
	}
}
