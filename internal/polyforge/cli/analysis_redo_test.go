package cli

// analysis_redo_test.go (polyforge#233, u5 — AC1/AC3/AC4): the owner's
// `pf file analysis redo <id>` asks this machine's daemon to re-schedule a
// queued ticket's analysis. It prints the scheduled task or the queued
// state, and a daemon refusal is a clear non-zero error. `pf help file`
// names the command.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func pf233File(fields string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			io.WriteString(w, `{"id":"wi_redo","slug":"pf-233","project":"polyforge","status":"queued","updated_at":"2026-11-22T00:00:00Z"}`)
			return
		}
		io.WriteString(w, `{"id":"wi_redo"}`)
	}
}

func TestPF233FileAnalysisRedo(t *testing.T) {
	aihub := newFakeAihub(t, &reqRecorder{}, pf233File(""))
	var calls int
	var got map[string]any
	daemon := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/analysis/redo" {
			t.Errorf("unexpected daemon call: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		calls++
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("redo body: %v (%s)", err, raw)
		}
		io.WriteString(w, `{"state":"scheduled","task_id":"task_0123456789abcdef","request_id":"redo_0123456789abcdef"}`)
	}))
	env := withMappingConfig(t)
	env["PF_SERVER"] = aihub.URL
	env["PF_API_KEY"] = "k_cli"
	env["PF_DAEMON_SOCKET"] = daemon
	code, stdout, stderr := runPf(t, env, "file", "analysis", "redo", "wi_redo")
	if code != 0 {
		t.Fatalf("redo = %d: %s", code, stderr)
	}
	if calls != 1 {
		t.Fatalf("the CLI must ask the daemon once, got %d calls", calls)
	}
	if got["work_item_id"] != "wi_redo" || got["project"] != "polyforge" {
		t.Fatalf("the redo request must name the ticket and its project, got %v", got)
	}
	if !strings.Contains(stdout, "task_0123456789abcdef") {
		t.Fatalf("the output must name the scheduled analysis task:\n%s", stdout)
	}
	if !strings.Contains(stdout, "历次分析") {
		t.Fatalf("the output must say the old analysis was archived:\n%s", stdout)
	}
}

func TestPF233FileAnalysisRedoRefusal(t *testing.T) {
	aihub := newFakeAihub(t, &reqRecorder{}, pf233File(""))
	daemon := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"code":"DAEMON-409","detail":"analysis redo: pf-233 is running — only a queued or blocked ticket can be redone"}`)
	}))
	env := withMappingConfig(t)
	env["PF_SERVER"] = aihub.URL
	env["PF_API_KEY"] = "k_cli"
	env["PF_DAEMON_SOCKET"] = daemon
	code, stdout, stderr := runPf(t, env, "file", "analysis", "redo", "wi_redo")
	if code == 0 {
		t.Fatalf("a refused redo must exit non-zero; stdout=%s", stdout)
	}
	if !strings.Contains(stderr, "running") || !strings.Contains(stderr, "queued or blocked") {
		t.Fatalf("the refusal must carry the daemon's reason, got:\n%s", stderr)
	}
}

func TestPF233HelpFileNamesRedo(t *testing.T) {
	code, stdout, stderr := runPf(t, nil, "help", "file")
	if code != 0 {
		t.Fatalf("help file = %d: %s", code, stderr)
	}
	for _, want := range []string{
		"pf file analysis redo <id>",
		"旧分析",
		"历次分析",
		"old format",
		"stale base",
		"materially changed scope",
		"pending analysis request",
		"local execution job",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("pf help file must carry %q:\n%s", want, stdout)
		}
	}
}
