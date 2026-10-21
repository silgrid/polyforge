package cli

// polyforge#161: `pf file create --after`, `pf file dep add|rm|list`, the
// dependency block of `pf file show` and the waiting_for rendering of
// `pf queue` — request shapes against a recording fake aihub, server
// refusals surfaced with their reason.

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

const cannedDeps = `{"blocking":[{"wi_id":"wi_c","kind":"blocks","slug":"polyforge#7","status":"queued"},` +
	`{"wi_id":"wi_r","kind":"related","slug":"polyforge#8","status":"queued"}],` +
	`"blocked_by":[{"wi_id":"wi_a","kind":"blocks","slug":"polyforge#3","status":"failed"}]}`

const cycleEnvelope = `{"code":"CORE-409","detail":"edge would close a dependency cycle","extra":{"reason":"DEPENDENCY_CYCLE"}}`

// --after travels in the create body (one request; aihub writes the
// edges inside the create transaction).
func TestVerbFileCreateAfter(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	code, _, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "g",
		"--after", "polyforge#3", "--after", "wi_b")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	all := rec.all()
	if len(all) != 1 || all[0].Method != "POST" || all[0].Path != "/api/v1/work-items" ||
		!strings.Contains(all[0].Body, `"after":["polyforge#3","wi_b"]`) {
		t.Fatalf("requests = %+v", all)
	}
	// Without --after the body carries no after key.
	if code, _, _ := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "g"); code != 0 ||
		strings.Contains(rec.last().Body, `"after"`) {
		t.Fatalf("plain create body = %s", rec.last().Body)
	}
}

// A refused blocker refuses the create: the server's typed error is the
// verb's error, nothing is printed as created.
func TestVerbFileCreateAfterRefused(t *testing.T) {
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"code":"CORE-400","detail":"blocker polyforge#9 must be an execution ticket — bind a bundle first (a ticket with no bundle runs nothing; it closes only when someone runs `+"`pf wrap`"+` on it, so nothing would release the edge)","extra":{"reason":"BLOCKER_NOT_EXECUTABLE"}}`)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	code, stdout, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "g",
		"--after", "polyforge#9")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "CORE-400: blocker polyforge#9 must be an execution ticket") {
		t.Fatalf("code=%d stdout=%q stderr=%s", code, stdout, stderr)
	}
	// Local validation: an empty --after sends nothing.
	var rec reqRecorder
	srv2 := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, cannedWI) })
	env["PF_SERVER"] = srv2.URL
	if code, _, _ := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "g", "--after", " "); code != 1 || len(rec.all()) != 0 {
		t.Fatalf("empty --after: code=%d requests=%d", code, len(rec.all()))
	}
}

func TestVerbFileDep(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedDeps)
	})
	env := map[string]string{"PF_SERVER": srv.URL}

	code, stdout, stderr := runPf(t, env, "file", "dep", "add", "polyforge#3", "polyforge#5")
	if code != 0 {
		t.Fatalf("add: code=%d stderr=%s", code, stderr)
	}
	got := rec.last()
	if got.Method != "POST" || got.Path != "/api/v1/work-items/polyforge%235/dependencies" ||
		got.Body != `{"blocking_wi_id":"polyforge#3","kind":"blocks"}` {
		t.Errorf("add = %s %s %s", got.Method, got.Path, got.Body)
	}
	// Only blocks edges render (related is not exposed).
	golden(t, "FileDepAdd", stdout, `polyforge#5
  dependencies:
    after:  polyforge#3 (failed)
    before: polyforge#7 (queued)
`)

	if code, _, stderr := runPf(t, env, "file", "dep", "rm", "polyforge#3", "polyforge#5"); code != 0 {
		t.Fatalf("rm: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); got.Method != "DELETE" || got.Path != "/api/v1/work-items/polyforge%235/dependencies/polyforge%233/blocks" {
		t.Errorf("rm = %s %s", got.Method, got.Path)
	}

	if code, _, stderr := runPf(t, env, "file", "dep", "list", "polyforge#5"); code != 0 {
		t.Fatalf("list: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); got.Method != "GET" || got.Path != "/api/v1/work-items/polyforge%235/dependencies" {
		t.Errorf("list = %s %s", got.Method, got.Path)
	}
	if code, stdout, _ := runPf(t, env, "--json", "file", "dep", "list", "polyforge#5"); code != 0 || strings.TrimSpace(stdout) != cannedDeps {
		t.Errorf("--json list = %d %q", code, stdout)
	}

	// Argument shape is validated locally.
	n := len(rec.all())
	for _, args := range [][]string{{"file", "dep", "add", "polyforge#3"}, {"file", "dep", "list"}, {"file", "dep", "frob", "x"}, {"file", "dep"}} {
		if code, _, _ := runPf(t, env, args...); code != 1 {
			t.Errorf("%v: code=%d", args, code)
		}
	}
	if len(rec.all()) != n {
		t.Errorf("a malformed dep call reached the server")
	}
}

func TestVerbFileDepCycleSurfacesReason(t *testing.T) {
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, cycleEnvelope)
	})
	env := map[string]string{"PF_SERVER": srv.URL}
	code, _, stderr := runPf(t, env, "file", "dep", "add", "polyforge#5", "polyforge#3")
	if code != 1 || !strings.Contains(stderr, "CORE-409: DEPENDENCY_CYCLE: edge would close a dependency cycle") {
		t.Fatalf("cycle: code=%d stderr=%s", code, stderr)
	}
	// --json keeps the server envelope verbatim.
	code, _, stderr = runPf(t, env, "--json", "file", "dep", "add", "polyforge#5", "polyforge#3")
	if code != 1 || strings.TrimSpace(stderr) != cycleEnvelope {
		t.Fatalf("--json cycle: code=%d stderr=%s", code, stderr)
	}
}

func TestVerbFileShowWaitingAndDeps(t *testing.T) {
	waitingWI := strings.TrimSuffix(cannedWI, "}") + `,"waiting_for":[{"slug":"polyforge#3","status":"failed"}]}`
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/dependencies") {
			io.WriteString(w, cannedDeps)
			return
		}
		io.WriteString(w, waitingWI)
	})
	env := map[string]string{"PF_SERVER": srv.URL}
	code, stdout, stderr := runPf(t, env, "file", "show", "pf-42")
	if code != 0 {
		t.Fatalf("show: code=%d stderr=%s", code, stderr)
	}
	for _, want := range []string{
		"  waiting-for: polyforge#3 (failed) (only a wrapped blocker releases; `pf file dep rm` removes an edge)\n",
		"  dependencies:\n    after:  polyforge#3 (failed)\n    before: polyforge#7 (queued)\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("show missing %q:\n%s", want, stdout)
		}
	}
}

func TestVerbQueueWaitingForColumn(t *testing.T) {
	waiting := `{"id":"wi_2","slug":"pf-2","status":"queued","priority":"normal",` +
		`"project":"polyforge","goal":"after pf-1","created_at":"t0",` +
		`"waiting_for":[{"slug":"pf-1","status":"running"}]}`
	running := `{"id":"wi_1","slug":"pf-1","status":"running","priority":"high",` +
		`"project":"polyforge","goal":"first","created_at":"t0"}`
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[`+running+`,`+waiting+`],"total":2}`)
	})
	code, stdout, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL}, "queue", "--project", "polyforge")
	if code != 0 {
		t.Fatalf("queue: code=%d stderr=%s", code, stderr)
	}
	golden(t, "QueueWaitingFor", stdout,
		`2 work item(s)
SLUG  PROJECT    STATUS   SESSION  PRIORITY  BUNDLE  WAITING-FOR     GOAL
----  ---------  -------  -------  --------  ------  --------------  ----------
pf-1  polyforge  running  -        high      -       -               first
pf-2  polyforge  queued   -        normal    -       pf-1 (running)  after pf-1
`)
}
