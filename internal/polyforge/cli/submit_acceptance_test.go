package cli

// submit_acceptance_test.go: `pf submit` refuses an execution ticket (one
// with a bundle) whose content lists no acceptance criterion under
// `## Acceptance` or `## 验收` — before the local daemon is contacted —
// with a reminder that the ticket needs interaction first;
// `--allow-no-acceptance` submits it anyway. `pf file create` is never
// checked.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// w460Submit runs `pf submit wi_abc <extra...>` against a fake aihub
// serving the canned work item with bundle and content (nil = absent),
// and a daemon fixture accepting the claim; it answers the exit code,
// stdout, stderr and whether the claim route was hit.
func w460Submit(t *testing.T, bundle, content *string, extra ...string) (int, string, string, bool) {
	t.Helper()
	var wi map[string]any
	if err := json.Unmarshal([]byte(cannedWI), &wi); err != nil {
		t.Fatal(err)
	}
	if bundle != nil {
		wi["bundle_name"] = *bundle
	}
	if content != nil {
		wi["content"] = *content
	}
	body, _ := json.Marshal(wi)
	var rec reqRecorder
	aihub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
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

func TestW460SubmitAcceptanceCheck(t *testing.T) {
	str := func(s string) *string { return &s }
	develop := str("develop")
	const submitted = "submitted pf-42 (accepted by the local daemon)\n"

	refused := func(t *testing.T, code int, stderr string, claimed bool) {
		t.Helper()
		if code == 0 {
			t.Fatalf("exit 0, want a refusal; stderr=%q", stderr)
		}
		if claimed {
			t.Error("the daemon's claim route was hit: the refusal must come before the daemon")
		}
		for _, want := range []string{"pf-42", "## Acceptance", "## 验收", "interaction", "--allow-no-acceptance", "pf file edit"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("the reminder does not name %q: %q", want, stderr)
			}
		}
		// The refusal is the lane's declaration: it names
		// no particular lane or step.
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
		if strings.Contains(stderr, "--allow-no-acceptance") {
			t.Errorf("an accepted submit prints the reminder: %q", stderr)
		}
	}

	t.Run("no acceptance section: refused", func(t *testing.T) {
		code, _, stderr, claimed := w460Submit(t, develop, str("## 背景\n\nan idea\n"))
		refused(t, code, stderr, claimed)
	})
	t.Run("a heading with no list item: refused", func(t *testing.T) {
		code, _, stderr, claimed := w460Submit(t, develop, str("## Acceptance\n\nto be decided\n"))
		refused(t, code, stderr, claimed)
	})
	t.Run("## Acceptance with a criterion: submitted", func(t *testing.T) {
		code, stdout, stderr, claimed := w460Submit(t, develop, str("## Acceptance\n\n- AC1 it works — carried by build\n"))
		accepted(t, code, stdout, stderr, claimed)
	})
	t.Run("## 验收 with a criterion: submitted", func(t *testing.T) {
		code, stdout, stderr, claimed := w460Submit(t, develop, str("## 验收\n\n- AC1 it works — carried by build\n"))
		accepted(t, code, stdout, stderr, claimed)
	})
	t.Run("no section with --allow-no-acceptance: submitted", func(t *testing.T) {
		code, stdout, stderr, claimed := w460Submit(t, develop, str("## 背景\n\nan idea\n"), "--allow-no-acceptance")
		accepted(t, code, stdout, stderr, claimed)
	})
	t.Run("a registration ticket without a bundle: not refused locally", func(t *testing.T) {
		code, stdout, stderr, claimed := w460Submit(t, nil, str("an idea\n"))
		accepted(t, code, stdout, stderr, claimed)
	})
	t.Run("file create is not checked", func(t *testing.T) {
		env, rec := carrierEnv(t, carrierDaemon(t))
		code, _, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "an idea", "--bundle", "develop")
		if code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		sent := false
		for _, r := range rec.all() {
			if r.Method == "POST" {
				sent = true
			}
		}
		if !sent {
			t.Error("the create request was not sent")
		}
		if strings.Contains(stderr, "--allow-no-acceptance") || strings.Contains(stderr, "## 验收") {
			t.Errorf("file create carries the submit reminder: %q", stderr)
		}
	})
}
