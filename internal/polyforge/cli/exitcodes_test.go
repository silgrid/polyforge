package cli

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// exitcodes_test.go: the #40 full-registry exit-code audit, pinned as
// per-verb goldens. Every verb in the commands registry is driven
// through Run() and its exit code is pinned per error class it can
// actually reach:
//
//	2xx → 0 | typed 4xx and envelope-less 4xx (HTTP-4xx synthesis) → 1
//	typed 5xx and envelope-less 5xx (HTTP-5xx synthesis) → 2
//	local validation / arity → 1 | top-level flag-parse → 2
//	transport failure (daemon socket down, aihub unreachable) → 1
//
// Classes a verb cannot produce (backup has no local validation;
// the daemon verb is
// help-only) are absent from that verb's table and reported in the fix
// summary as semantic blanks, never silently skipped.

// TestAihubUnreachableExits1: the aihub-face transport-failure golden —
// connection-refused to PF_SERVER is a client-class failure, the twin of
// TestDaemonUnreachableExits1 (the daemon-face pin).
func TestAihubUnreachableExits1(t *testing.T) {
	code, _, stderr := runPf(t, map[string]string{
		"PF_SERVER": deadAihubURL(t),
	}, "cancel", "wi_x")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "pf:") {
		t.Errorf("stderr = %q", stderr)
	}
}

// wireClasses is the wire-answer matrix both faces share: the same
// classes, one canned body shape per class.
var wireClasses = []struct {
	name   string
	status int
	body   string
	want   int
}{
	{"typed-4xx", 404, `{"code":"CORE-404","detail":"no such thing"}`, 1},
	{"bare-4xx", 404, "plain not-found page", 1},
	{"typed-5xx", 500, `{"code":"CORE-500","detail":"boom"}`, 2},
	{"bare-5xx", 502, "proxy garbage", 2},
}

// TestAihubFaceVerbExitCodeGoldens: every direct-to-aihub verb (data
// plane, admin face), one golden table per verb.
func TestAihubFaceVerbExitCodeGoldens(t *testing.T) {
	type spec struct {
		name     string
		args     func(t *testing.T) []string // a valid, wire-reaching invocation
		localBad []string                    // local validation failure; NO wire contact
		parseBad []string                    // top-level flag-parse failure; NO wire contact
	}
	specs := []spec{
		{"file", func(t *testing.T) []string { return []string{"file", "show", "wi_x"} },
			[]string{"file"}, []string{"file", "--no-such-flag"}},
		{"queue", func(t *testing.T) []string { return []string{"queue", "--project", "polyforge"} },
			// A mapped queue can no longer fail locally on arity alone
			// (no --project is a valid merged run now); the local
			// validation golden is the --limit range check instead.
			[]string{"queue", "--limit", "0"}, []string{"queue", "--no-such-flag"}},
		{"cancel", func(t *testing.T) []string { return []string{"cancel", "wi_x"} },
			[]string{"cancel"}, []string{"cancel", "--no-such-flag"}},
		{"recall", func(t *testing.T) []string {
			return []string{"recall", "--project", "polyforge", "query words"}
		}, []string{"recall"}, []string{"recall", "--no-such-flag"}},
		{"remember", func(t *testing.T) []string {
			return []string{"remember", "--type", "experience.debug", "--project", "polyforge", "the lesson"}
		}, []string{"remember", "content without --type"}, []string{"remember", "--no-such-flag"}},
		{"users", func(t *testing.T) []string { return []string{"users", "list"} },
			[]string{"users"}, []string{"users", "--no-such-flag"}},
		{"api-keys", func(t *testing.T) []string { return []string{"api-keys", "list"} },
			[]string{"api-keys"}, []string{"api-keys", "--no-such-flag"}},
		{"projects", func(t *testing.T) []string { return []string{"projects", "list"} },
			[]string{"projects"}, []string{"projects", "--no-such-flag"}},
		{"backup", func(t *testing.T) []string { return []string{"backup"} },
			nil, []string{"backup", "--no-such-flag"}},
	}
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			// Every aihub-face verb runs under a mapping config: the
			// placement verbs' --project validates against it (polyforge#113);
			// for every other verb the config is inert.
			mapEnv := func(base map[string]string) map[string]string {
				m := withMappingConfig(t)
				for k, v := range base {
					m[k] = v
				}
				return m
			}
			// 2xx → 0.
			var rec reqRecorder
			srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{}`)
			})
			env0 := mapEnv(map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k_cli"})
			code, _, stderr := runPf(t, env0, s.args(t)...)
			if code != 0 {
				t.Errorf("success: exit = %d, want 0 (stderr %q)", code, stderr)
			}
			// Wire error classes.
			for _, cls := range wireClasses {
				t.Run(cls.name, func(t *testing.T) {
					var rec reqRecorder
					srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(cls.status)
						io.WriteString(w, cls.body)
					})
					code, _, _ := runPf(t, mapEnv(map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k_cli"}), s.args(t)...)
					if code != cls.want {
						t.Errorf("exit = %d, want %d", code, cls.want)
					}
				})
			}
			// Transport failure (aihub unreachable) → 1.
			code, _, _ = runPf(t, mapEnv(map[string]string{"PF_SERVER": deadAihubURL(t), "PF_API_KEY": "k_cli"}), s.args(t)...)
			if code != 1 {
				t.Errorf("transport: exit = %d, want 1", code)
			}
			// Local validation → 1, no wire contact.
			if s.localBad != nil {
				var rec reqRecorder
				srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
					io.WriteString(w, `{}`)
				})
				code, _, _ = runPf(t, mapEnv(map[string]string{"PF_SERVER": srv.URL}), s.localBad...)
				if code != 1 {
					t.Errorf("local validation: exit = %d, want 1", code)
				}
				if n := len(rec.all()); n != 0 {
					t.Errorf("local validation must not contact the server; got %d requests", n)
				}
			}
			// Top-level flag-parse → 2, no wire contact.
			{
				var rec reqRecorder
				srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
					io.WriteString(w, `{}`)
				})
				code, _, _ = runPf(t, mapEnv(map[string]string{"PF_SERVER": srv.URL}), s.parseBad...)
				if code != 2 {
					t.Errorf("flag parse: exit = %d, want 2", code)
				}
				if n := len(rec.all()); n != 0 {
					t.Errorf("parse errors must not contact the server; got %d requests", n)
				}
			}
		})
	}
}

// TestDaemonFaceVerbExitCodeGoldens: every daemon-UDS-face verb (the 8
// execution-plane routing verbs, ops), one golden table per verb.
// submit/resume additionally run the aihub precheck, answered 200 by a
// fake aihub so the daemon route stays the class under test.
func TestDaemonFaceVerbExitCodeGoldens(t *testing.T) {
	type spec struct {
		name      string
		args      []string
		route     string // the one wire route a valid invocation hits
		okBody    string
		needAihub bool // submit/resume: the aihub precheck runs first
		localBad  []string
		parseBad  []string
	}
	const watchBody = `{"work_item":"wi_x","project":"p","status":"running"}`
	specs := []spec{
		{"submit", []string{"submit", "wi_x"}, "POST /jobs/wi_x/claim",
			`{"accepted":true,"work_item_id":"wi_x"}`, true,
			[]string{"submit"}, []string{"submit", "--no-such-flag"}},
		{"watch", []string{"watch", "wi_x", "--once"}, "GET /jobs/wi_x/watch",
			watchBody, false, []string{"watch", "--once"}, []string{"watch", "--no-such-flag"}},
		{"answer", []string{"answer", "wi_x", "--question", "q_1", "--text", "yes", "--by", "usr_1"},
			"POST /jobs/wi_x/answer", `{"applied":true,"delivered":true}`, false,
			[]string{"answer", "wi_x"}, []string{"answer", "--no-such-flag"}},
		{"pause", []string{"pause", "wi_x"}, "POST /jobs/wi_x/pause", `{}`, false,
			[]string{"pause"}, []string{"pause", "--no-such-flag"}},
		{"wrap", []string{"wrap", "wi_x"}, "POST /jobs/wi_x/wrap", `{}`, false,
			[]string{"wrap"}, []string{"wrap", "--no-such-flag"}},
		{"ops", []string{"ops", "status"}, "GET /status",
			`{"machine_id":"m1","auto":false,"shutting_down":false,"jobs":[],` +
				`"queue":{"pending":0,"running":0,"max_concurrent":2}}`, false,
			[]string{"ops", "bogus-action"}, []string{"ops", "--no-such-flag"}},
	}
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			aihubEnv := func() (map[string]string, *reqRecorder) {
				if !s.needAihub {
					return map[string]string{}, nil
				}
				rec := &reqRecorder{}
				srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
					io.WriteString(w, cannedWI)
				})
				return map[string]string{"PF_SERVER": srv.URL}, rec
			}
			// 2xx → 0.
			{
				env, _ := aihubEnv()
				d := newDaemonFixture(t)
				d.answers[s.route] = func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, s.okBody)
				}
				env["PF_DAEMON_SOCKET"] = d.sock
				code, _, stderr := runPf(t, env, s.args...)
				if code != 0 {
					t.Errorf("success: exit = %d, want 0 (stderr %q)", code, stderr)
				}
			}
			// Wire error classes, served on the verb's own route. The
			// daemon envelope family is DAEMON-*; the synthesis class
			// (bare body) shares the client's parseWireError path.
			daemonClasses := []struct {
				name   string
				status int
				body   string
				code   string // the typed code the stderr must carry ("" for bare bodies)
				want   int
			}{
				{"typed-4xx", 404, `{"code":"DAEMON-404","detail":"no local capsule"}`, "DAEMON-404", 1},
				{"bare-4xx", 404, "plain not-found page", "", 1},
				{"typed-5xx", 500, `{"code":"DAEMON-500","detail":"boom"}`, "DAEMON-500", 2},
				{"bare-5xx", 502, "proxy garbage", "", 2},
			}
			for _, cls := range daemonClasses {
				t.Run(cls.name, func(t *testing.T) {
					env, _ := aihubEnv()
					d := newDaemonFixture(t)
					d.answers[s.route] = func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(cls.status)
						io.WriteString(w, cls.body)
					}
					env["PF_DAEMON_SOCKET"] = d.sock
					code, _, stderr := runPf(t, env, s.args...)
					if code != cls.want {
						t.Errorf("exit = %d, want %d", code, cls.want)
					}
					if cls.code != "" && !strings.Contains(stderr, cls.code) {
						t.Errorf("stderr = %q, want the typed code %q", stderr, cls.code)
					}
				})
			}
			// Transport failure (daemon socket down) → 1.
			{
				env, _ := aihubEnv()
				env["PF_DAEMON_SOCKET"] = "/nonexistent/polyforge.sock"
				code, _, _ := runPf(t, env, s.args...)
				if code != 1 {
					t.Errorf("transport: exit = %d, want 1", code)
				}
			}
			// Local validation → 1, no wire contact.
			if s.localBad != nil {
				env, rec := aihubEnv()
				d := newDaemonFixture(t)
				env["PF_DAEMON_SOCKET"] = d.sock
				code, _, _ := runPf(t, env, s.localBad...)
				if code != 1 {
					t.Errorf("local validation: exit = %d, want 1", code)
				}
				if n := len(d.rec.all()) + len(recOrZero(rec)); n != 0 {
					t.Errorf("local validation must not contact any face; got %d requests", n)
				}
			}
			// Top-level flag-parse → 2, no wire contact.
			{
				env, rec := aihubEnv()
				d := newDaemonFixture(t)
				env["PF_DAEMON_SOCKET"] = d.sock
				code, _, _ := runPf(t, env, s.parseBad...)
				if code != 2 {
					t.Errorf("flag parse: exit = %d, want 2", code)
				}
				if n := len(d.rec.all()) + len(recOrZero(rec)); n != 0 {
					t.Errorf("parse errors must not contact any face; got %d requests", n)
				}
			}
		})
	}
}

func recOrZero(r *reqRecorder) []capturedRequest {
	if r == nil {
		return nil
	}
	return r.all()
}

// TestDaemonVerbHelpOnlyExitGoldens: the `daemon` registration is
// help-only (cmd/pf/main.go intercepts the real launch), so through
// Run() it can only ever succeed or fail to parse. Wire and transport
// classes are unreachable for this verb — a reported blank, not a
// silent skip.
func TestDaemonVerbHelpOnlyExitGoldens(t *testing.T) {
	code, stdout, _ := runPf(t, nil, "daemon")
	if code != 0 {
		t.Errorf("help-only run: exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "pf daemon") {
		t.Errorf("stdout = %q, want the daemon help text", stdout)
	}
	if code, _, _ := runPf(t, nil, "daemon", "--no-such-flag"); code != 2 {
		t.Errorf("flag parse: exit = %d, want 2", code)
	}
	// Sanity: the registry really is the full verb set (12 routing +
	// 5 admin + doctor + version + init + daemon + chat + watch + result
	// (the step's result delivery, #214) + verify-run (a verification
	// step's mechanical run); watch replaced the
	// retired watch-probe; doctor is the #113 machine health face; version
	// the #196 build string (the `--version` flag's verb form); init
	// the #177 workspace layout).
	want := []string{
		"file", "queue", "cancel", "recall", "remember", "memory",
		"submit", "answer", "pause", "resume",
		"wrap", "ops", "chat", "watch", "task", "result", "verify-run", "msg", "users", "api-keys", "machines", "projects",
		"backup",
		"doctor", "version", "init", "config", "daemon",
	}
	if len(commands) != len(want) {
		t.Fatalf("registry has %d commands, want %d", len(commands), len(want))
	}
	for i, name := range want {
		if commands[i].Name != name {
			t.Errorf("commands[%d] = %q, want %q", i, commands[i].Name, name)
		}
	}
}
