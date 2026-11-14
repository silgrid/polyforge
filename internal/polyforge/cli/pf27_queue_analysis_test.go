package cli

// pf27_queue_analysis_test.go (polyforge#27, AC3): `pf queue` marks a
// queued execution ticket whose content carries no analysis section —
// the auto-pull wave skips it — as 需要交互：未分析 in its STATUS cell,
// beside the acceptance mark when it is missing too (both marks then),
// and the merged `--json` document carries `analysis_missing: true` on
// it alone. A ticket with a report (or 「免分析」) under `## 分析`, a
// running one and a registration ticket (no bundle) are not marked;
// a lane declaring `acceptance: none` is never marked either.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPF27QueueMarksAnalysisMissing(t *testing.T) {
	const (
		none     = `{"id":"wi_none","slug":"alpha-none","project":"alpha","status":"queued","priority":"normal","goal":"neither section","bundle_name":"develop","content":null,"created_at":"t"}`
		accOnly  = `{"id":"wi_acc","slug":"alpha-acc","project":"alpha","status":"queued","priority":"normal","goal":"acceptance only","bundle_name":"develop","content":"## Acceptance\n\n- AC1 x","created_at":"t"}`
		analyzed = `{"id":"wi_an","slug":"alpha-an","project":"alpha","status":"queued","priority":"normal","goal":"both sections","bundle_name":"develop","content":"## Acceptance\n\n- AC1 x\n\n## 分析\n\n- report","created_at":"t"}`
		run      = `{"id":"wi_run","slug":"alpha-run","project":"alpha","status":"running","priority":"normal","goal":"running","bundle_name":"develop","content":"just prose","created_at":"t"}`
		reg      = `{"id":"wi_reg","slug":"alpha-reg","project":"alpha","status":"queued","priority":"normal","goal":"registration","bundle_name":null,"content":null,"created_at":"t"}`
	)
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") == "alpha" {
			io.WriteString(w, `{"items":[`+none+`,`+accOnly+`,`+analyzed+`,`+run+`,`+reg+`],"total":5}`)
			return
		}
		io.WriteString(w, `{"items":[],"total":0}`)
	})
	env := withTwoProjectMapping(t)
	env["PF_SERVER"] = srv.URL

	const (
		accLabel = "需要交互：缺验收"
		anLabel  = "需要交互：未分析"
	)
	code, stdout, stderr := runPf(t, env, "queue")
	if code != 0 {
		t.Fatalf("pf queue: code=%d stderr=%s", code, stderr)
	}
	row := func(slug string) string {
		for _, l := range strings.Split(stdout, "\n") {
			if strings.Contains(l, slug) {
				return l
			}
		}
		t.Fatalf("pf queue: no row for %s:\n%s", slug, stdout)
		return ""
	}
	if line := row("alpha-none"); !strings.Contains(line, "queued ("+accLabel+")") || !strings.Contains(line, "("+anLabel+")") {
		t.Errorf("pf queue: a queued ticket missing both sections must carry both marks:\n%s", line)
	}
	if line := row("alpha-acc"); !strings.Contains(line, "queued ("+anLabel+")") || strings.Contains(line, accLabel) {
		t.Errorf("pf queue: a queued ticket with acceptance but no analysis must carry the analysis mark alone:\n%s", line)
	}
	for _, slug := range []string{"alpha-an", "alpha-run", "alpha-reg"} {
		if line := row(slug); strings.Contains(line, accLabel) || strings.Contains(line, anLabel) {
			t.Errorf("pf queue: %s is marked although it needs no interaction:\n%s", slug, line)
		}
	}

	code, stdout, stderr = runPf(t, env, "--json", "queue")
	if code != 0 {
		t.Fatalf("pf --json queue: code=%d stderr=%s", code, stderr)
	}
	var doc struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &doc); err != nil {
		t.Fatalf("merged --json doc: %v (%q)", err, stdout)
	}
	if len(doc.Items) != 5 {
		t.Fatalf("merged --json items = %d, want 5 (%q)", len(doc.Items), stdout)
	}
	for _, it := range doc.Items {
		var id string
		_ = json.Unmarshal(it["id"], &id)
		acc, hasAcc := it["acceptance_missing"]
		an, hasAn := it["analysis_missing"]
		switch id {
		case "wi_none":
			if !hasAcc || string(acc) != "true" || !hasAn || string(an) != "true" {
				t.Errorf("merged --json: %s must carry both \"acceptance_missing\": true and \"analysis_missing\": true (got %s/%s)", id, acc, an)
			}
		case "wi_acc":
			if hasAcc || !hasAn || string(an) != "true" {
				t.Errorf("merged --json: %s must carry \"analysis_missing\": true and no acceptance_missing (got %s, present %v)", id, an, hasAcc)
			}
		default:
			if hasAcc || hasAn {
				t.Errorf("merged --json: %s must carry neither mark (got %s/%s)", id, acc, an)
			}
		}
	}
}

// TestPF27QueueAnalysisMarkFollowsTheLaneAcceptance: the analysis mark,
// like the acceptance mark, is the lane's own declaration — a queued
// execution ticket with acceptance but no analysis on a lane declaring
// `acceptance: none` is not marked, and on a lane requiring acceptance
// it is (the lanes are the w516 test's own).
func TestPF27QueueAnalysisMarkFollowsTheLaneAcceptance(t *testing.T) {
	const (
		onNone  = `{"id":"wi_nolist","slug":"alpha-nolist","project":"alpha","status":"queued","priority":"normal","goal":"no analysis, lane none","bundle_name":"nolist","content":"## Acceptance\n\n- AC1 x","created_at":"t"}`
		onPlain = `{"id":"wi_plain","slug":"alpha-plain","project":"alpha","status":"queued","priority":"normal","goal":"no analysis, lane default","bundle_name":"plain","content":"## Acceptance\n\n- AC1 x","created_at":"t"}`
	)
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") == "alpha" {
			io.WriteString(w, `{"items":[`+onNone+`,`+onPlain+`],"total":2}`)
			return
		}
		io.WriteString(w, `{"items":[],"total":0}`)
	})
	env := withTwoProjectMapping(t)
	env["PF_SERVER"] = srv.URL
	env["PF_DAEMON_SOCKET"] = w516StatusDaemon(t, w516Lanes(t))

	const label = "需要交互：未分析"
	code, stdout, stderr := runPf(t, env, "queue")
	if code != 0 {
		t.Fatalf("pf queue: code=%d stderr=%s", code, stderr)
	}
	row := func(slug string) string {
		for _, l := range strings.Split(stdout, "\n") {
			if strings.Contains(l, slug) {
				return l
			}
		}
		t.Fatalf("pf queue: no row for %s:\n%s", slug, stdout)
		return ""
	}
	if strings.Contains(row("alpha-nolist"), label) {
		t.Errorf("pf queue: a ticket on a lane declaring acceptance none is marked %s:\n%s", label, stdout)
	}
	if !strings.Contains(row("alpha-plain"), label) {
		t.Errorf("pf queue: a ticket without analysis on a lane requiring acceptance is not marked %s:\n%s", label, stdout)
	}
}
