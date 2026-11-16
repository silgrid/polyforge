package cli

// w72_unconfirmed_gate_test.go (polyforge#72, u10 — AC6): the unconfirmed
// gate. An analysis the mechanism wrote carries the unconfirmed marker;
// until the owner confirms it, the auto-pull wave skips the ticket and
// `pf queue` marks it 「需要交互：分析待确认」 (json analysis_unconfirmed),
// the new `pf file analysis confirm <id>` strips the marker under the
// content CAS, and a manual `pf submit` is untouched — the submit check
// only judges presence, so a human submits an unconfirmed ticket as is.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w72MarkedContent is one ticket body the mechanism wrote: a real draft
// under the unconfirmed marker.
func w72MarkedContent() string {
	return "## 背景\n\nx\n\n## Acceptance\n\n- AC1 — carried by build\n\n## 分析\n\n" +
		workflow.AutoAnalysisMarker("task_0123456789abcdef") +
		"\n\n### 1. 改动范围\n\n- a\n"
}

func TestW72QueueMarksAnalysisUnconfirmed(t *testing.T) {
	lane := func(project, bundle string) *workflow.Lane { return nil } // nil: requires acceptance
	items := []WIView{
		{WorkItem: api.WorkItem{ID: "wi_unconfirmed", ProjectName: "p", Status: api.StatusQueued,
			BundleName: strPtr("develop"), Content: strPtr(w72MarkedContent())}},
		{WorkItem: api.WorkItem{ID: "wi_confirmed", ProjectName: "p", Status: api.StatusQueued,
			BundleName: strPtr("develop"), Content: strPtr("## 分析\n\n### 1. 改动范围\n\n- a\n")}},
	}
	markNeedsInteraction(items, lane)
	if !items[0].AnalysisUnconfirmed || items[0].AnalysisMissing {
		t.Fatalf("the unconfirmed analysis must mark needs-interaction: %+v", items[0])
	}
	if items[1].AnalysisUnconfirmed || items[1].AnalysisMissing {
		t.Fatalf("a confirmed analysis is not unconfirmed: %+v", items[1])
	}
	row := strings.Join(wiRow(items[0]), " ")
	if !strings.Contains(row, "需要交互：分析待确认") {
		t.Fatalf("the queue row must carry the mark: %s", row)
	}
}

func TestW72FileAnalysisConfirmRemovesMarker(t *testing.T) {
	rec := &reqRecorder{}
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			c := w72MarkedContent()
			io.WriteString(w, `{"id":"wi_abc","slug":"pf-42","project":"polyforge","status":"queued",
				"content":`+w72JSONStr(c)+`,"updated_at":"2026-11-16T00:00:00Z"}`)
			return
		}
		if r.Method == "PATCH" {
			io.WriteString(w, `{"id":"wi_abc","slug":"pf-42"}`)
		}
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_cli"
	code, _, stderr := runPf(t, env, "file", "analysis", "confirm", "wi_abc")
	if code != 0 {
		t.Fatalf("confirm = %d: %s", code, stderr)
	}
	// the recorder is the one that saw the body (it drains it before the
	// handler runs)
	var patch *capturedRequest
	for i := range rec.reqs {
		if rec.reqs[i].Method == "PATCH" {
			if patch != nil {
				t.Fatalf("confirm must PATCH exactly once; saw two:\n%v", rec.reqs)
			}
			patch = &rec.reqs[i]
		}
	}
	if patch == nil {
		t.Fatalf("confirm never PATCHed; requests: %v", rec.reqs)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(patch.Body), &body); err != nil {
		t.Fatalf("the PATCH body: %v (%s)", err, patch.Body)
	}
	content, _ := body["content"].(string)
	if strings.Contains(content, "pf:auto-analysis") {
		t.Fatalf("the confirmed content must carry no marker:\n%s", content)
	}
	if !strings.Contains(content, "### 1. 改动范围") {
		t.Fatalf("the draft must survive the confirm:\n%s", content)
	}
	if got, _ := body["expected_updated_at"].(string); got != "2026-11-16T00:00:00Z" {
		t.Fatalf("the confirm must ride the content CAS (expected_updated_at), got %v", body)
	}
}

// TestW72SubmitUnaffectedByUnconfirmed: the submit check judges presence
// only — a manual `pf submit` of an unconfirmed ticket is not blocked.
func TestW72SubmitUnaffectedByUnconfirmed(t *testing.T) {
	c := w72MarkedContent()
	wi := &api.WorkItem{BundleName: strPtr("develop"), ProjectName: "p", Content: &c}
	if err := submitAnalysisCheck(&Context{}, wi, "pf-42"); err != nil {
		t.Fatalf("a manual submit must not be blocked by the unconfirmed marker: %v", err)
	}
}

func w72JSONStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
