// carries: internal/polyforge/cli/render.go, internal/polyforge/cli/verbs_daemon.go
package cli

// queue_child_test.go (polyforge#77, AC2/AC4): a bundle-bound parent
// waits for its children — aihub's waiting_for lists each unwrapped child
// with via "child" beside the plain `blocks` entries (no via). The CLI
// keeps the two apart: the WAITING-FOR column and the detail line tell a
// child from an edge, a refused submit points a child at
// `pf file edit <child> --parent ""` and an edge at `pf file dep rm`, and
// a queued parent with a failed or canceled child is marked
// 需要交互：子票失败 (the merged --json carries child_failed: true) —
// beside, never instead of, the acceptance and analysis marks.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestVerbQueueChildWaitingFor(t *testing.T) {
	parent := `{"id":"wi_1","slug":"pf-1","status":"queued","priority":"normal",` +
		`"project":"polyforge","bundle_name":"develop","goal":"epic","created_at":"t0",` +
		`"content":"## Acceptance\n\n- AC1 x\n\n## 分析\n\n- report",` +
		`"waiting_for":[{"slug":"pf-2","status":"running","via":"child"},{"slug":"pf-4","status":"queued"}]}`
	child := `{"id":"wi_2","slug":"pf-2","status":"running","priority":"normal",` +
		`"project":"polyforge","bundle_name":"develop","goal":"child","created_at":"t0","parent_id":"wi_1"}`
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/dependencies"):
			io.WriteString(w, `{"blocking":[],"blocked_by":[{"wi_id":"wi_4","kind":"blocks","slug":"pf-4","status":"queued"}]}`)
		case strings.HasSuffix(r.URL.Path, "/work-items/pf-1"):
			io.WriteString(w, parent)
		default:
			io.WriteString(w, `{"items":[`+parent+`,`+child+`],"total":2}`)
		}
	})
	env := map[string]string{"PF_SERVER": srv.URL}
	code, stdout, stderr := runPf(t, env, "queue", "--project", "polyforge")
	if code != 0 {
		t.Fatalf("queue: code=%d stderr=%s", code, stderr)
	}
	golden(t, "QueueChildWaitingFor", stdout,
		`2 work item(s)
SLUG  PROJECT    STATUS   SESSION  PRIORITY  BUNDLE   WAITING-FOR                          GOAL
----  ---------  -------  -------  --------  -------  -----------------------------------  -----
pf-1  polyforge  queued   -        normal    develop  pf-2 (child, running), pf-4 (queued)  epic
pf-2  polyforge  running  -        normal    develop  -                                    child
`)

	// The detail line names both ways out, each for its own kind.
	code, stdout, stderr = runPf(t, env, "file", "show", "pf-1")
	if code != 0 {
		t.Fatalf("show: code=%d stderr=%s", code, stderr)
	}
	want := "  waiting-for: pf-2 (child, running), pf-4 (queued) (only a wrapped blocker releases; " +
		"`pf file dep rm` removes an edge, `pf file edit <child> --parent \"\"` detaches a child)\n"
	if !strings.Contains(stdout, want) {
		t.Errorf("show missing %q:\n%s", want, stdout)
	}
}

func TestVerbQueueMarksChildFailed(t *testing.T) {
	const (
		both = `"content":"## Acceptance\n\n- AC1 x\n\n## 分析\n\n- report"`
		// failed child: the child mark alone.
		pFailed = `{"id":"wi_pf","slug":"alpha-pf","project":"alpha","status":"queued","priority":"normal","goal":"failed child","bundle_name":"develop",` + both + `,"created_at":"t",` +
			`"waiting_for":[{"slug":"alpha#2","status":"failed","via":"child"},{"slug":"alpha#9","status":"running","via":"child"}]}`
		// canceled child and neither section: all three marks.
		pCanceled = `{"id":"wi_pc","slug":"alpha-pc","project":"alpha","status":"queued","priority":"normal","goal":"canceled child","bundle_name":"develop","content":null,"created_at":"t",` +
			`"waiting_for":[{"slug":"alpha#3","status":"canceled","via":"child"}]}`
		// a child still running: no child mark.
		pRunning = `{"id":"wi_pr","slug":"alpha-pr","project":"alpha","status":"queued","priority":"normal","goal":"running child","bundle_name":"develop",` + both + `,"created_at":"t",` +
			`"waiting_for":[{"slug":"alpha#4","status":"running","via":"child"}]}`
		// a failed `blocks` blocker is not a failed child.
		pEdge = `{"id":"wi_pe","slug":"alpha-pe","project":"alpha","status":"queued","priority":"normal","goal":"failed edge","bundle_name":"develop",` + both + `,"created_at":"t",` +
			`"waiting_for":[{"slug":"alpha#5","status":"failed"}]}`
		// no waiting at all, acceptance only: the analysis mark alone.
		accOnly = `{"id":"wi_acc","slug":"alpha-acc","project":"alpha","status":"queued","priority":"normal","goal":"acceptance only","bundle_name":"develop","content":"## Acceptance\n\n- AC1 x","created_at":"t"}`
	)
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") == "alpha" {
			io.WriteString(w, `{"items":[`+pFailed+`,`+pCanceled+`,`+pRunning+`,`+pEdge+`,`+accOnly+`],"total":5}`)
			return
		}
		io.WriteString(w, `{"items":[],"total":0}`)
	})
	env := withTwoProjectMapping(t)
	env["PF_SERVER"] = srv.URL

	const (
		childLabel = "需要交互：子票失败"
		accLabel   = "需要交互：缺验收"
		anLabel    = "需要交互：未分析"
	)
	code, stdout, stderr := runPf(t, env, "queue")
	if code != 0 {
		t.Fatalf("pf queue: code=%d stderr=%s", code, stderr)
	}
	row := func(slug string) string {
		for _, l := range strings.Split(stdout, "\n") {
			if strings.Contains(l, slug+" ") {
				return l
			}
		}
		t.Fatalf("pf queue: no row for %s:\n%s", slug, stdout)
		return ""
	}
	if line := row("alpha-pf"); !strings.Contains(line, "queued ("+childLabel+")") || strings.Contains(line, accLabel) || strings.Contains(line, anLabel) {
		t.Errorf("pf queue: a parent with a failed child must carry the child mark alone:\n%s", line)
	}
	if line := row("alpha-pc"); !strings.Contains(line, "("+childLabel+")") || !strings.Contains(line, "("+accLabel+")") || !strings.Contains(line, "("+anLabel+")") {
		t.Errorf("pf queue: a parent with a canceled child and neither section must carry all three marks:\n%s", line)
	}
	for _, slug := range []string{"alpha-pr", "alpha-pe"} {
		if line := row(slug); strings.Contains(line, childLabel) || strings.Contains(line, accLabel) || strings.Contains(line, anLabel) {
			t.Errorf("pf queue: %s is marked although no child failed:\n%s", slug, line)
		}
	}
	if line := row("alpha-acc"); !strings.Contains(line, "queued ("+anLabel+")") || strings.Contains(line, childLabel) {
		t.Errorf("pf queue: a ticket without analysis must carry the analysis mark alone:\n%s", line)
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
		cf, hasCF := it["child_failed"]
		_, hasAcc := it["acceptance_missing"]
		_, hasAn := it["analysis_missing"]
		switch id {
		case "wi_pf":
			if !hasCF || string(cf) != "true" || hasAcc || hasAn {
				t.Errorf("merged --json: %s must carry \"child_failed\": true alone (got %s, acc %v, an %v)", id, cf, hasAcc, hasAn)
			}
		case "wi_pc":
			if !hasCF || string(cf) != "true" || !hasAcc || !hasAn {
				t.Errorf("merged --json: %s must carry child_failed, acceptance_missing and analysis_missing (got %s, acc %v, an %v)", id, cf, hasAcc, hasAn)
			}
		case "wi_acc":
			if hasCF || hasAcc || !hasAn {
				t.Errorf("merged --json: %s must carry analysis_missing alone (child %v, acc %v, an %v)", id, hasCF, hasAcc, hasAn)
			}
		default:
			if hasCF || hasAcc || hasAn {
				t.Errorf("merged --json: %s must carry no mark (child %v, acc %v, an %v)", id, hasCF, hasAcc, hasAn)
			}
		}
	}
}

// TestSubmitGuidanceByVia: aihub refuses the claim DEPENDENCY_BLOCKED;
// the submit's guidance follows each waiting_for entry's via — a child
// is detached with `pf file edit <child> --parent ""`, an edge removed
// with `pf file dep rm <blocker> <slug>`.
func TestSubmitGuidanceByVia(t *testing.T) {
	const blocked = `{"code":"CORE-409","detail":"work item has an unfinished blocking dependency or child","extra":{"reason":"DEPENDENCY_BLOCKED"}}`
	submit := func(t *testing.T, waitingFor string) string {
		t.Helper()
		wi := strings.TrimSuffix(cannedWI, "}") + `,"waiting_for":` + waitingFor + `}`
		aihub := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, wi)
		})
		d := newDaemonFixture(t)
		d.answers["POST /jobs/wi_abc/claim"] = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, blocked)
		}
		code, _, se := runPf(t, map[string]string{"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock}, "submit", "wi_abc")
		if code != 1 {
			t.Fatalf("the refused submit: exit %d, want 1 (stderr %q)", code, se)
		}
		return se
	}

	t.Run("edge", func(t *testing.T) {
		se := submit(t, `[{"slug":"pf-3","status":"failed"}]`)
		for _, want := range []string{"waiting for pf-3 (failed)", "pf file dep rm pf-3 pf-42"} {
			if !strings.Contains(se, want) {
				t.Errorf("edge guidance misses %q: %s", want, se)
			}
		}
		if strings.Contains(se, "--parent") {
			t.Errorf("edge guidance must not point at detaching a child: %s", se)
		}
	})
	t.Run("child", func(t *testing.T) {
		se := submit(t, `[{"slug":"pf-7","status":"running","via":"child"}]`)
		for _, want := range []string{"waiting for pf-7 (child, running)", `pf file edit pf-7 --parent ""`} {
			if !strings.Contains(se, want) {
				t.Errorf("child guidance misses %q: %s", want, se)
			}
		}
		if strings.Contains(se, "pf file dep rm") {
			t.Errorf("child guidance must not point at removing an edge: %s", se)
		}
	})
	t.Run("both", func(t *testing.T) {
		se := submit(t, `[{"slug":"pf-3","status":"queued"},{"slug":"pf-7","status":"failed","via":"child"}]`)
		for _, want := range []string{"pf file dep rm pf-3 pf-42", `pf file edit pf-7 --parent ""`} {
			if !strings.Contains(se, want) {
				t.Errorf("mixed guidance misses %q: %s", want, se)
			}
		}
		if strings.Contains(se, "pf file dep rm pf-7") || strings.Contains(se, "pf file edit pf-3") {
			t.Errorf("mixed guidance crosses the two kinds: %s", se)
		}
	})
}
