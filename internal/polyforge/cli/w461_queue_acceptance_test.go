package cli

// w461_queue_acceptance_test.go (AC1): `pf queue` marks a
// queued execution ticket whose content lists no acceptance criterion —
// the auto-pull wave skips it — as 需要交互：缺验收 in its STATUS cell, and
// the merged `--json` document carries `acceptance_missing: true` on it
// alone. A ticket with acceptance, a running one and a registration ticket
// (no bundle) are not marked.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestW461QueueShowsNeedsInteractionWithoutAcceptance(t *testing.T) {
	const (
		none = `{"id":"wi_none","slug":"alpha-none","project":"alpha","status":"queued","priority":"normal","goal":"no acceptance","bundle_name":"develop","content":null,"created_at":"t"}`
		zh   = `{"id":"wi_zh","slug":"alpha-zh","project":"alpha","status":"queued","priority":"normal","goal":"zh acceptance","bundle_name":"develop","content":"## 验收\n\n- AC1 x","created_at":"t"}`
		run  = `{"id":"wi_run","slug":"alpha-run","project":"alpha","status":"running","priority":"normal","goal":"running","bundle_name":"develop","content":"just prose","created_at":"t"}`
		reg  = `{"id":"wi_reg","slug":"alpha-reg","project":"alpha","status":"queued","priority":"normal","goal":"registration","bundle_name":null,"content":null,"created_at":"t"}`
	)
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ready-queue") {
			if strings.Contains(r.URL.Path, "/alpha/") {
				io.WriteString(w, `{"project":"alpha","items":[`+none+`,`+zh+`,`+reg+`],"running":[`+run+`],"paused":[]}`)
				return
			}
			io.WriteString(w, `{"project":"beta","items":[],"running":[],"paused":[]}`)
			return
		}
		if r.URL.Query().Get("project") == "alpha" {
			io.WriteString(w, `{"items":[`+none+`,`+zh+`,`+run+`,`+reg+`],"total":4}`)
			return
		}
		io.WriteString(w, `{"items":[],"total":0}`)
	})
	env := withTwoProjectMapping(t)
	env["PF_SERVER"] = srv.URL

	const label = "需要交互：缺验收"
	checkTable := func(face, stdout string) {
		t.Helper()
		for _, slug := range []string{"alpha-none", "alpha-zh", "alpha-run", "alpha-reg"} {
			var line string
			for _, l := range strings.Split(stdout, "\n") {
				if strings.Contains(l, slug) {
					line = l
					break
				}
			}
			if line == "" {
				if slug == "alpha-run" && strings.Contains(face, "--ready") {
					continue // the ready face renders its items segment only
				}
				t.Errorf("%s: no row for %s:\n%s", face, slug, stdout)
				continue
			}
			if slug == "alpha-none" {
				if !strings.Contains(line, "queued ("+label+")") {
					t.Errorf("%s: the row of a queued execution ticket without acceptance must read STATUS %q:\n%s", face, "queued ("+label+")", stdout)
				}
			} else if strings.Contains(line, label) {
				t.Errorf("%s: %s is marked %s although it is not a queued execution ticket without acceptance:\n%s", face, slug, label, stdout)
			}
		}
	}

	code, stdout, stderr := runPf(t, env, "queue")
	if code != 0 {
		t.Fatalf("pf queue: code=%d stderr=%s", code, stderr)
	}
	checkTable("pf queue", stdout)

	code, stdout, stderr = runPf(t, env, "queue", "--project", "alpha", "--ready")
	if code != 0 {
		t.Fatalf("pf queue --project alpha --ready: code=%d stderr=%s", code, stderr)
	}
	checkTable("pf queue --project alpha --ready", stdout)

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
	if len(doc.Items) != 4 {
		t.Fatalf("merged --json items = %d, want 4 (%q)", len(doc.Items), stdout)
	}
	for _, it := range doc.Items {
		var id string
		_ = json.Unmarshal(it["id"], &id)
		v, has := it["acceptance_missing"]
		if id == "wi_none" {
			if !has || string(v) != "true" {
				t.Errorf("merged --json: %s must carry \"acceptance_missing\": true (got %s, present %v)", id, v, has)
			}
		} else if has {
			t.Errorf("merged --json: %s must not carry acceptance_missing (got %s)", id, v)
		}
	}
}
