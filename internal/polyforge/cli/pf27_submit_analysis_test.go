package cli

// pf27_submit_analysis_test.go (polyforge#27, AC1/AC2): `pf submit`
// refuses an execution ticket (one with a bundle) on a lane that
// requires acceptance when its content carries no analysis section —
// no `## 分析` / `## Analysis` section, or a heading with no content —
// before the local daemon is contacted, naming the way out: analyze
// (a read-only `pf task spawn` errand) and write the report into the
// ticket's `## 分析` section, or the owner's explicit 免分析;
// `--allow-no-analysis` submits it anyway. A section holding 「免分析」
// with its date passes, and a lane declaring `acceptance: none` is not
// checked at all.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPF27SubmitAnalysisCheck(t *testing.T) {
	str := func(s string) *string { return &s }
	develop := str("develop")
	const submitted = "submitted pf-42 (accepted by the local daemon)\n"

	const withAcceptance = "## Acceptance\n\n- AC1 it works — carried by build\n"
	const analyzed = withAcceptance + "\n## 分析\n\n- 改动范围：internal/x；证明：go test ./internal/x/\n"
	const waived = withAcceptance + "\n## 分析\n\n免分析（2026-11-14，owner 明确跳过）\n"

	refused := func(t *testing.T, code int, stderr string, claimed bool) {
		t.Helper()
		if code == 0 {
			t.Fatalf("exit 0, want a refusal; stderr=%q", stderr)
		}
		if claimed {
			t.Error("the daemon's claim route was hit: the refusal must come before the daemon")
		}
		for _, want := range []string{"pf-42", "## 分析", "## Analysis", "免分析", "pf task spawn", "pf file edit", "--allow-no-analysis"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("the reminder does not name %q: %q", want, stderr)
			}
		}
		// The refusal is the lane's declaration: it names no
		// particular lane or step.
		if strings.Contains(stderr, "develop") || strings.Contains(stderr, "build") {
			t.Errorf("the reminder names a particular lane or step: %q", stderr)
		}
	}
	accepted := func(t *testing.T, code int, stdout, stderr string, claimed bool) {
		t.Helper()
		if code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if !claimed {
			t.Error("the claim route was not hit")
		}
		if stdout != submitted {
			t.Errorf("stdout %q, want today's %q", stdout, submitted)
		}
		if strings.Contains(stderr, "--allow-no-analysis") {
			t.Errorf("an accepted submit prints the reminder: %q", stderr)
		}
	}

	t.Run("acceptance but no analysis section: refused", func(t *testing.T) {
		code, _, stderr, claimed := w460Submit(t, develop, str(withAcceptance))
		refused(t, code, stderr, claimed)
	})
	t.Run("an analysis heading with no content: refused", func(t *testing.T) {
		code, _, stderr, claimed := w460Submit(t, develop, str(withAcceptance+"\n## 分析\n"))
		refused(t, code, stderr, claimed)
	})
	t.Run("a report under ## 分析: submitted", func(t *testing.T) {
		code, stdout, stderr, claimed := w460Submit(t, develop, str(analyzed))
		accepted(t, code, stdout, stderr, claimed)
	})
	t.Run("免分析 with its date: submitted", func(t *testing.T) {
		code, stdout, stderr, claimed := w460Submit(t, develop, str(waived))
		accepted(t, code, stdout, stderr, claimed)
	})
	t.Run("no analysis with --allow-no-analysis: submitted", func(t *testing.T) {
		code, stdout, stderr, claimed := w460Submit(t, develop, str(withAcceptance), "--allow-no-analysis")
		accepted(t, code, stdout, stderr, claimed)
	})
	t.Run("--allow-no-acceptance alone does not waive the analysis check", func(t *testing.T) {
		code, _, stderr, claimed := w460Submit(t, develop, str("## 背景\n\nan idea\n"), "--allow-no-acceptance")
		refused(t, code, stderr, claimed)
	})
	t.Run("a lane declaring acceptance none: not checked", func(t *testing.T) {
		code, stdout, stderr, claimed := pf27SubmitOnLanes(t, w516Lanes(t), "nolist", withAcceptance)
		accepted(t, code, stdout, stderr, claimed)
	})
	t.Run("a lane requiring acceptance: refused", func(t *testing.T) {
		code, _, stderr, claimed := pf27SubmitOnLanes(t, w516Lanes(t), "plain", withAcceptance)
		refused(t, code, stderr, claimed)
	})
	t.Run("a registration ticket without a bundle: not refused locally", func(t *testing.T) {
		code, stdout, stderr, claimed := w460Submit(t, nil, str(withAcceptance))
		accepted(t, code, stdout, stderr, claimed)
	})
}

// pf27SubmitOnLanes runs `pf submit wi_abc` for the canned work item
// bound to bundle with the given content, against a fake daemon whose
// /status names wf as the workflows dir (so the lane's `acceptance`
// declaration is read) and which accepts the claim; it answers the exit
// code, stdout, stderr and whether the claim route was hit — w516SubmitOn
// with the content as a parameter.
func pf27SubmitOnLanes(t *testing.T, wf, bundle, content string) (int, string, string, bool) {
	t.Helper()
	var wi map[string]any
	if err := json.Unmarshal([]byte(cannedWI), &wi); err != nil {
		t.Fatal(err)
	}
	wi["bundle_name"] = bundle
	wi["content"] = content
	body, _ := json.Marshal(wi)
	var rec reqRecorder
	aihub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	var claimed atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"machine_id":"m-a","workflows_dir":"`+wf+`","jobs":[],"queue":{}}`)
	})
	mux.HandleFunc("POST /jobs/wi_abc/claim", func(w http.ResponseWriter, r *http.Request) {
		claimed.Store(true)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"accepted":true,"work_item_id":"wi_abc"}`)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = aihub.URL
	env["PF_DAEMON_SOCKET"] = newFakeDaemon(t, mux)
	code, stdout, stderr := runPf(t, env, "submit", "wi_abc")
	return code, stdout, stderr, claimed.Load()
}
