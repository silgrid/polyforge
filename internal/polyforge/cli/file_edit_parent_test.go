// carries: internal/polyforge/cli/verbs_aihub.go
package cli

// file_edit_parent_test.go (polyforge#77, AC5): `pf file edit <child>
// --parent ""` detaches a child from its parent — the PATCH carries
// parent_id: null and nothing else; a non-empty --parent (re-parenting)
// is refused locally, before anything reaches aihub, exactly as aihub
// only clears; aihub's own refusal of a running child surfaces with its
// reason.

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestVerbFileEditDetachParent(t *testing.T) {
	const detached = `{"id":"wi_abc","slug":"pf-42","seq":42,"project":"polyforge","goal":"the goal",` +
		`"status":"failed","priority":"normal","labels":[],"reporter":"usr_1","parent_id":null,` +
		`"created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-01T00:00:00Z"}`
	var rec reqRecorder
	running := false
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		if running {
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"code":"CORE-409","detail":"cannot detach a running work item from its parent: end its session first","extra":{"reason":"CHILD_RUNNING"}}`)
			return
		}
		io.WriteString(w, detached)
	})
	env := map[string]string{"PF_SERVER": srv.URL}

	// --parent "" is the detach: one PATCH, parent_id null and nothing
	// else; the answer renders as the detail record.
	code, stdout, stderr := runPf(t, env, "file", "edit", "pf-42", "--parent", "")
	if code != 0 {
		t.Fatalf("edit --parent \"\": code=%d stderr=%s", code, stderr)
	}
	all := rec.all()
	if len(all) != 1 || all[0].Method != "PATCH" || all[0].Path != "/api/v1/work-items/pf-42" {
		t.Fatalf("edit --parent \"\" requests = %+v, want one PATCH /api/v1/work-items/pf-42", all)
	}
	if all[0].Body != `{"parent_id":null}` {
		t.Errorf("edit --parent \"\" body = %s, want {\"parent_id\":null}", all[0].Body)
	}
	if !strings.Contains(stdout, "pf-42 (wi_abc)") || !strings.Contains(stdout, "status:   failed") {
		t.Errorf("edit --parent \"\" must render the detail record:\n%s", stdout)
	}

	// A non-empty --parent would re-parent: refused locally, nothing on
	// the wire, the refusal naming the one allowed form.
	before := len(rec.all())
	code, _, stderr = runPf(t, env, "file", "edit", "pf-42", "--parent", "pf-7")
	if code == 0 {
		t.Errorf("edit --parent pf-7: exit 0, want a refusal")
	}
	if !strings.Contains(stderr, `--parent ""`) {
		t.Errorf("edit --parent pf-7: the refusal must name the clearing form --parent \"\": %s", stderr)
	}
	if n := len(rec.all()); n != before {
		t.Errorf("edit --parent pf-7 reached aihub (%d new requests)", n-before)
	}

	// aihub refuses a running child: the reason surfaces.
	running = true
	code, _, stderr = runPf(t, env, "file", "edit", "pf-42", "--parent", "")
	if code != 1 || !strings.Contains(stderr, "CORE-409: CHILD_RUNNING") {
		t.Errorf("running child: code=%d stderr=%s, want exit 1 naming CORE-409: CHILD_RUNNING", code, stderr)
	}
	if got := rec.last(); got.Method != "PATCH" || got.Body != `{"parent_id":null}` {
		t.Errorf("running child: request = %s %s, want the detach PATCH", got.Method, got.Body)
	}
}
