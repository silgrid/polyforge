package cli

// w72_file_create_analysis_test.go (polyforge#72, u7 — AC1): `pf file
// create` asks THIS machine's daemon to schedule the pre-submit analysis
// of a fresh execution ticket — a bundle, a lane that requires
// acceptance, a body with no `## 分析` section — and says so: the task id
// (or 已排队) on stderr, a machine-readable line in --json mode, an
// explicit 「未安排分析」 notice when the daemon cannot be reached — never
// a failed create. A registration ticket, a lane declaring acceptance
// none, or a body that already carries an analysis (「免分析」 included)
// asks for nothing.

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// w72AnalysisDaemon serves the daemon faces the trigger needs: GET
// /status naming a workflows dir, and a recording POST /analysis/enqueue
// whose answer the test scripts. It answers the socket path.
func w72AnalysisDaemon(t *testing.T, wf string, answer func() string) (sock string, enqueued *[]string) {
	t.Helper()
	var bodies []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"machine_id":"m-w72","workflows_dir":"`+wf+`","jobs":[],"queue":{}}`)
	})
	mux.HandleFunc("POST /analysis/enqueue", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		io.WriteString(w, answer())
	})
	return newFakeDaemon(t, mux), &bodies
}

func w72AnalysisWorld(t *testing.T, laneFiles map[string]string, answer func() string) (map[string]string, *[]string) {
	t.Helper()
	wf := t.TempDir()
	for name, front := range laneFiles {
		writeLane(t, wf, name, front)
	}
	sock, enq := w72AnalysisDaemon(t, wf, answer)
	env := withMappingConfig(t)
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_cli"
	env["PF_DAEMON_SOCKET"] = sock
	return env, enq
}

const w72AcceptanceBody = "## 背景\n\nthe idea\n\n## Acceptance\n\n- AC1 x — carried by build\n"

func TestW72FileCreateEnqueuesAnalysis(t *testing.T) {
	env, enq := w72AnalysisWorld(t,
		map[string]string{"develop": "steps:\n  - { id: build }\n  - { id: review }\n"},
		func() string { return `{"state":"scheduled","task_id":"task_w7200000000aa"}` })
	code, _, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "w72 trigger",
		"--bundle", "develop", "--content", w72AcceptanceBody)
	if code != 0 {
		t.Fatalf("create = %d: %s", code, stderr)
	}
	if len(*enq) == 0 {
		t.Fatal("the create of an acceptance ticket without analysis must ask the daemon to schedule one (POST /analysis/enqueue)")
	}
	if !strings.Contains((*enq)[0], `"work_item_id":"wi_abc"`) || !strings.Contains((*enq)[0], `"project":"polyforge"`) {
		t.Fatalf("the enqueue request must name the created ticket and its project: %s", (*enq)[0])
	}
	if !strings.Contains(stderr, "分析已安排") || !strings.Contains(stderr, "task_w7200000000aa") {
		t.Fatalf("the notice must name the task: %s", stderr)
	}
}

func TestW72FileCreateAnalysisNoticeJSON(t *testing.T) {
	env, enq := w72AnalysisWorld(t,
		map[string]string{"develop": "steps:\n  - { id: build }\n"},
		func() string { return `{"state":"queued"}` })
	code, stdout, stderr := runPf(t, env, "--json", "file", "create", "--project", "polyforge", "--goal", "w72 json",
		"--bundle", "develop", "--content", w72AcceptanceBody)
	if code != 0 {
		t.Fatalf("create = %d: %s", code, stderr)
	}
	if len(*enq) == 0 {
		t.Fatal("the create must ask the daemon to schedule the analysis")
	}
	// stdout stays the server JSON as-is
	var wi map[string]any
	if err := json.Unmarshal([]byte(stdout), &wi); err != nil || wi["id"] != "wi_abc" {
		t.Fatalf("stdout must stay the server's work-item JSON: %s", stdout)
	}
	// the machine-readable analysis line rides stderr
	var note struct {
		Analysis struct {
			State  string `json:"state"`
			TaskID string `json:"task_id"`
		} `json:"analysis"`
	}
	line := strings.TrimSpace(stderr)
	if err := json.Unmarshal([]byte(line), &note); err != nil {
		t.Fatalf("stderr must carry the machine-readable analysis line: %s", stderr)
	}
	if note.Analysis.State != "queued" {
		t.Fatalf("the notice line = %s", line)
	}
}

func TestW72FileCreateSkipsAnalysisWhenExempt(t *testing.T) {
	env, enq := w72AnalysisWorld(t,
		map[string]string{
			"develop": "steps:\n  - { id: build }\n",
			"nolist":   "acceptance: none\nsteps:\n  - { id: work }\n",
		},
		func() string { return `{"state":"scheduled","task_id":"task_x"}` })
	cases := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"a lane declaring acceptance none", []string{"--bundle", "nolist", "--content", "plain body"}, ""},
		{"a body that already carries an analysis", []string{"--bundle", "develop", "--content", w72AcceptanceBody + "\n## 分析\n\n免分析（2026-11-16，owner）\n"}, ""},
		{"a registration ticket (no bundle)", []string{"--content", "plain body"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			*enq = nil
			code, _, stderr := runPf(t, env, append([]string{"file", "create", "--project", "polyforge", "--goal", "w72 exempt"}, c.args...)...)
			if code != 0 {
				t.Fatalf("create = %d: %s", code, stderr)
			}
			if len(*enq) != 0 {
				t.Errorf("no analysis may be scheduled: %v", *enq)
			}
			if c.stderr != "" && !strings.Contains(stderr, c.stderr) {
				t.Errorf("stderr = %s, want %q", stderr, c.stderr)
			}
		})
	}
}

func TestW72FileCreateAnalysisNoticeOnDaemonDown(t *testing.T) {
	wf := t.TempDir()
	writeLane(t, wf, "develop", "steps:\n  - { id: build }\n")
	sock := filepath.Join(t.TempDir(), "no-daemon.sock") // never served
	env := withMappingConfig(t)
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_cli"
	env["PF_DAEMON_SOCKET"] = sock
	code, stdout, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "w72 down",
		"--bundle", "develop", "--content", w72AcceptanceBody)
	if code != 0 {
		t.Fatalf("a daemon that cannot be reached must not fail the create: %d %s", code, stderr)
	}
	if !strings.Contains(stdout, "wi_abc") {
		t.Fatalf("the created ticket must still be rendered: %s", stdout)
	}
	if !strings.Contains(stderr, "未安排分析") {
		t.Fatalf("the notice must say the analysis was not scheduled: %s", stderr)
	}
}
