package cli

// w47_submit_progress_analysis_test.go (polyforge#47, AC6): the pre-submit
// analysis check does not apply to a ticket that already made release
// progress — its aihub event history carries a session lifecycle event (a
// claim, an attach, a pause, an end, a supersession) or a resume. The
// `pf ops end-session` takeover flow resubmits exactly such a ticket with
// a plain `pf submit`, and the check used to block it. A fresh ticket (no
// such event) is checked as before, and an events read that fails does
// not stop the takeover either: the daemon's claim stays the authority.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// w47SubmitProgress runs `pf submit wi_abc` for the canned work item bound
// to the develop bundle with acceptance but no analysis section, against
// a fake aihub whose `?include=events` answer carries events ("" = an
// empty history; readFail makes that read fail) and a fake daemon that
// accepts the claim. It answers the exit code, stdout, stderr and whether
// the claim route was hit.
func w47SubmitProgress(t *testing.T, events string, readFail bool, extra ...string) (int, string, string, bool) {
	t.Helper()
	str := func(s string) *string { return &s }
	develop := str("develop")
	_ = develop
	var wi map[string]any
	if err := json.Unmarshal([]byte(cannedWI), &wi); err != nil {
		t.Fatal(err)
	}
	wi["bundle_name"] = "develop"
	wi["content"] = "## Acceptance\n\n- AC1 it works — carried by build\n"
	body, _ := json.Marshal(wi)
	var rec reqRecorder
	aihub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(r.URL.RawQuery, "include=events") {
			w.Write(body)
			return
		}
		if readFail {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":{"code":"CORE-500","detail":"injected"}}`)
			return
		}
		var withEvents map[string]any
		if err := json.Unmarshal(body, &withEvents); err != nil {
			t.Errorf("fixture: %v", err)
			return
		}
		withEvents["attrs"] = map[string]any{"events": json.RawMessage(events)}
		json.NewEncoder(w).Encode(withEvents)
	})
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/claim", `{"accepted":true,"work_item_id":"wi_abc"}`)
	args := append([]string{"submit", "wi_abc"}, extra...)
	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock,
	}, args...)
	claimed := false
	for _, r := range d.rec.all() {
		if r.Method == "POST" && r.Path == "/jobs/wi_abc/claim" {
			claimed = true
		}
	}
	return code, stdout, stderr, claimed
}

func TestW47SubmitAnalysisCheckSparesProgressTicket(t *testing.T) {
	const submitted = "submitted pf-42 (accepted by the local daemon)\n"

	t.Run("a session-claimed ticket without analysis is submitted", func(t *testing.T) {
		code, stdout, stderr, claimed := w47SubmitProgress(t,
			`[{"id":1,"created_at":"2026-11-01T00:00:00Z","type":"work_item_filed"},
			  {"id":2,"created_at":"2026-11-02T00:00:00Z","type":"wi_content_updated"},
			  {"id":3,"created_at":"2026-11-03T00:00:00Z","type":"session_claimed"}]`, false)
		if code != 0 || !claimed {
			t.Fatalf("code=%d claimed=%v stderr=%q", code, claimed, stderr)
		}
		if stdout != submitted {
			t.Errorf("stdout %q, want %q", stdout, submitted)
		}
		if strings.Contains(stderr, "--allow-no-analysis") {
			t.Errorf("a progress ticket must not hear the analysis reminder: %q", stderr)
		}
	})
	t.Run("a session-superseded ticket (the takeover shape) is submitted", func(t *testing.T) {
		code, _, stderr, claimed := w47SubmitProgress(t,
			`[{"id":3,"created_at":"2026-11-03T00:00:00Z","type":"session_superseded"},
			  {"id":4,"created_at":"2026-11-03T00:00:01Z","type":"wi_resumed"}]`, false)
		if code != 0 || !claimed {
			t.Fatalf("code=%d claimed=%v stderr=%q", code, claimed, stderr)
		}
	})
	t.Run("a fresh ticket without analysis is refused as before", func(t *testing.T) {
		code, _, stderr, claimed := w47SubmitProgress(t,
			`[{"id":1,"created_at":"2026-11-01T00:00:00Z","type":"work_item_filed"},
			  {"id":2,"created_at":"2026-11-02T00:00:00Z","type":"wi_content_updated"}]`, false)
		if code == 0 || claimed {
			t.Fatalf("code=%d claimed=%v — a fresh ticket must stay refused", code, claimed)
		}
		if !strings.Contains(stderr, "--allow-no-analysis") {
			t.Errorf("the refusal still names the way out: %q", stderr)
		}
	})
	t.Run("an events read that fails does not block the takeover", func(t *testing.T) {
		code, _, _, claimed := w47SubmitProgress(t, "", true)
		if code != 0 || !claimed {
			t.Fatalf("code=%d claimed=%v stderr=%q — an unreadable history must leave the claim to the daemon", code, claimed, "")
		}
	})
	t.Run("a progress ticket still reaches --allow-no-analysis when it wants it", func(t *testing.T) {
		code, _, _, claimed := w47SubmitProgress(t,
			`[{"id":3,"created_at":"2026-11-03T00:00:00Z","type":"session_claimed"}]`, false, "--allow-no-analysis")
		if code != 0 || !claimed {
			t.Fatalf("code=%d claimed=%v", code, claimed)
		}
	})
}
