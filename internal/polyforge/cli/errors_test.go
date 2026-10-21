package cli

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Error rendering matrix: every error-code family maps to its exit code
// and renders with the code prominent. 4xx → 1, 5xx → 2 (2xx → 0 is the
// success path covered by every request-shape test).

func TestErrorCodeExitMatrix(t *testing.T) {
	cases := []struct {
		code     string
		http     int // status the fake server answers with (decorative)
		wantExit int
	}{
		{"CORE-400", 400, 1},
		{"CORE-401", 401, 1},
		{"CORE-403", 403, 1},
		{"CORE-404", 404, 1},
		{"CORE-409", 409, 1},
		{"CORE-413", 413, 1},
		{"MEM-403", 403, 1},
		{"MEM-404", 404, 1},
		{"AUTH-401", 401, 1},
		{"GIT-409", 409, 1},
		{"DAEMON-400", 400, 1},
		{"DAEMON-404", 404, 1},
		{"DAEMON-409", 409, 1},
		{"CAP-409", 409, 1},
		{"CORE-500", 500, 2},
		{"CORE-503", 503, 2},
		{"MEM-500", 500, 2},
		{"DAEMON-500", 500, 2},
		{"DAEMON-503", 503, 2},
		{"DAEMON-507", 507, 2},
		{"DAEMON-511", 511, 2},
		{"GIT-500", 500, 2},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			var rec reqRecorder
			srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.http)
				fmt.Fprintf(w, `{"code":%q,"detail":"wire detail"}`, tc.code)
			})
			code, _, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL}, "cancel", "wi_x")
			if code != tc.wantExit {
				t.Errorf("exit = %d, want %d", code, tc.wantExit)
			}
			wantMsg := "pf: " + tc.code + ": wire detail"
			if !strings.Contains(stderr, wantMsg) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, wantMsg)
			}
		})
	}
}

// TestErrorJSONModeReemitsEnvelope: --json re-emits the typed envelope on
// stderr instead of the human line.
func TestErrorJSONModeReemitsEnvelope(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		io.WriteString(w, `{"code":"CORE-409","detail":"already claimed"}`)
	})
	code, _, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL}, "--json", "cancel", "wi_x")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, `"code":"CORE-409"`) || strings.Contains(stderr, "pf: CORE-409") {
		t.Errorf("stderr = %q, want the re-emitted envelope only", stderr)
	}
}

// TestErrorNoEnvelopeDegradesToHTTPCode: a plain-text crash page must
// degrade to HTTP-<status> rather than masking the failure. Pinned on
// BOTH faces: the daemon client and the aihub client (which used to
// synthesize AUTH-401 for any non-envelope body, misclassifying proxy
// 502/504 pages as auth failures — #14).
func TestErrorNoEnvelopeDegradesToHTTPCode(t *testing.T) {
	d := newDaemonFixture(t)
	d.answers["GET /jobs/wi_x/watch"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		io.WriteString(w, "proxy garbage")
	}
	code, _, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "watch", "wi_x", "--once")
	if code != 2 {
		t.Errorf("exit = %d, want 2 (5xx class)", code)
	}
	if !strings.Contains(stderr, "HTTP-502") || !strings.Contains(stderr, "proxy garbage") {
		t.Errorf("stderr = %q", stderr)
	}
}

// TestAihubNoEnvelopeDegradesToHTTPCode: the aihub face synthesizes the
// status-derived code too — 5xx HTML answers exit 2, 4xx exit 1, and a
// 401 that DOES carry the typed envelope still wins over the synthesis.
func TestAihubNoEnvelopeDegradesToHTTPCode(t *testing.T) {
	cases := []struct {
		status   int
		body     string
		wantExit int
		wantCode string
	}{
		{502, "<html>bad gateway</html>", 2, "HTTP-502"},
		{504, "gateway timeout", 2, "HTTP-504"},
		{404, "not found page", 1, "HTTP-404"},
		{401, "unauthorized", 1, "HTTP-401"},
	}
	for _, tc := range cases {
		t.Run(tc.wantCode, func(t *testing.T) {
			var rec reqRecorder
			srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			code, _, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL}, "cancel", "wi_x")
			if code != tc.wantExit {
				t.Errorf("exit = %d, want %d", code, tc.wantExit)
			}
			if !strings.Contains(stderr, tc.wantCode) || !strings.Contains(stderr, tc.body) {
				t.Errorf("stderr = %q, want %s + body", stderr, tc.wantCode)
			}
		})
	}
	// Envelope wins over synthesis on the same status.
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"code":"AUTH-401","detail":"wire detail"}`)
	})
	code, _, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL}, "cancel", "wi_x")
	if code != 1 || !strings.Contains(stderr, "AUTH-401: wire detail") {
		t.Errorf("envelope 401: exit=%d stderr=%q", code, stderr)
	}
}

// TestUsageErrors: parse failures, unknown commands and missing verbs
// exit 2 with no server contact.
func TestUsageErrors(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "{}")
	})
	env := map[string]string{"PF_SERVER": srv.URL}

	if code, _, _ := runPf(t, env); code != 2 {
		t.Errorf("no args: exit = %d, want 2", code)
	}
	if code, _, _ := runPf(t, env, "no-such-verb"); code != 2 {
		t.Errorf("unknown verb: exit = %d, want 2", code)
	}
	if code, _, _ := runPf(t, env, "cancel"); code != 1 {
		t.Errorf("verb arity error: exit = %d, want 1", code)
	}
	if code, _, _ := runPf(t, env, "cancel", "--no-such-flag", "x"); code != 2 {
		t.Errorf("bad flag: exit = %d, want 2", code)
	}
	if n := len(rec.all()); n != 0 {
		t.Errorf("usage errors must not contact the server; got %d requests", n)
	}
}

// TestDaemonUnreachableExits1: a daemon socket that is not listening is
// a client-class failure.
func TestDaemonUnreachableExits1(t *testing.T) {
	code, _, stderr := runPf(t, map[string]string{
		"PF_DAEMON_SOCKET": "/nonexistent/polyforge.sock",
	}, "pause", "wi_x")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "pf:") {
		t.Errorf("stderr = %q", stderr)
	}
}
