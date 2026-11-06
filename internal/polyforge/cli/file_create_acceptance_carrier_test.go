package cli

// file_create_acceptance_carrier_test.go: the file-create acceptance-carrier
// advisory. For an execution ticket the verb prints the
// chosen bundle's step table on stderr (resident hint). Warn only: the
// request, the exit code and stdout (including --json) stay unchanged; a
// registration ticket or an unreachable daemon gets no advisory at all.
// The e2e-specific warning keyed on an e2e_author_mode var went with the
// lane that declared it: it fired on every develop ticket
// mentioning e2e, though develop's build writes e2e tests.

import (
	"bytes"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

const (
	carrierHint    = "file create: bundle steps:"
	carrierWarning = "file create: acceptance-carrier warning:"
)

// carrierDaemon serves GET /status with a workflows dir holding a develop
// bundle; it answers the daemon socket path.
func carrierDaemon(t *testing.T) string {
	t.Helper()
	wf := t.TempDir()
	bundles := map[string]string{
		"develop": "steps:\n  - { id: build }\n  - { id: review }\n  - { id: sync }\n  - { id: verification }\n  - { id: publish }\n  - { id: merge }\n",
	}
	for name, body := range bundles {
		writeLane(t, wf, name, body)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"machine_id":"m-c","workflows_dir":"`+wf+`","jobs":[],"queue":{}}`)
	})
	return newFakeDaemon(t, mux)
}

func carrierEnv(t *testing.T, sock string) (map[string]string, *reqRecorder) {
	t.Helper()
	rec := &reqRecorder{}
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_cli"
	env["PF_DAEMON_SOCKET"] = sock
	return env, rec
}

func TestFileCreateAcceptanceCarrierAdvisory(t *testing.T) {
	const e2eContent = "## Acceptance\n- the new E2E scenario passes\n"
	cases := []struct {
		name     string
		args     []string
		hint     string // expected hint line fragment; "" = no hint
		wantWarn bool
	}{
		{
			"develop + e2e content: hint, no e2e warning",
			[]string{"--bundle", "develop", "--content", e2eContent},
			carrierHint + " develop = build -> review -> sync -> verification -> publish -> merge",
			false,
		},
		{
			"develop without e2e content: hint only",
			[]string{"--bundle", "develop", "--content", "unit tests carried by build"},
			carrierHint + " develop = build -> review -> sync -> verification -> publish -> merge",
			false,
		},
		{
			"unknown bundle: silent",
			[]string{"--bundle", "no-such-lane", "--content", e2eContent},
			"",
			false,
		},
		{
			"registration ticket: silent",
			[]string{"--content", e2eContent},
			"",
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, rec := carrierEnv(t, carrierDaemon(t))
			args := append([]string{"file", "create", "--project", "polyforge", "--goal", "ship it"}, tc.args...)
			code, stdout, stderr := runPf(t, env, args...)
			if code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr)
			}
			if tc.hint == "" {
				if strings.Contains(stderr, carrierHint) || strings.Contains(stderr, carrierWarning) {
					t.Fatalf("unexpected advisory: %q", stderr)
				}
			} else {
				if !strings.Contains(stderr, tc.hint) {
					t.Fatalf("hint %q missing; stderr=%q", tc.hint, stderr)
				}
				if !strings.Contains(stderr, "carried by <step id>") {
					t.Errorf("hint does not ask for the carrier step: %q", stderr)
				}
			}
			if got := strings.Contains(stderr, carrierWarning); got != tc.wantWarn {
				t.Fatalf("e2e warning present=%v, want %v; stderr=%q", got, tc.wantWarn, stderr)
			}
			if strings.ContainsAny(stderr, "{}") {
				t.Errorf("advisory carries a brace (merged-output JSON slicing): %q", stderr)
			}
			if strings.Contains(stdout, carrierHint) || strings.Contains(stdout, carrierWarning) {
				t.Errorf("advisory leaked to stdout: %q", stdout)
			}
			if c := tc.args[len(tc.args)-1]; !strings.Contains(rec.last().Body, `"content":`+jsonBody(c)) {
				t.Errorf("request content altered: %s", rec.last().Body)
			}
		})
	}
}

// TestFileCreateAcceptanceCarrierDaemonUnreachable: no daemon — AIHub
// business goes through the daemon, so the create is refused explicitly
// (DAEMON-410) and nothing reaches the hub.
func TestFileCreateAcceptanceCarrierDaemonUnreachable(t *testing.T) {
	env, rec := carrierEnv(t, filepath.Join(t.TempDir(), "absent.sock"))
	code, _, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "ship it",
		"--bundle", "develop", "--content", "the new e2e scenario passes")
	if code == 0 || !strings.Contains(stderr, "DAEMON-410") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("the create reached the hub with the daemon down (%d requests)", n)
	}
}

// TestFileCreateAcceptanceCarrierJSONOutputUnchanged: --json stdout is
// byte-identical when the advisory fires; only stderr changes.
func TestFileCreateAcceptanceCarrierJSONOutputUnchanged(t *testing.T) {
	env, rec := carrierEnv(t, carrierDaemon(t))
	code, stdout, stderr := runPf(t, env, "--json", "file", "create", "--project", "polyforge",
		"--goal", "ship it", "--bundle", "develop", "--content", "the new e2e scenario passes")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, carrierHint) || strings.Contains(stderr, carrierWarning) {
		t.Fatalf("expected the step-table advisory alone; stderr=%q", stderr)
	}
	if strings.TrimSpace(stdout) != cannedWI {
		t.Fatalf("--json stdout changed: %q", stdout)
	}
	if body := rec.last().Body; !strings.Contains(body, `"bundle_name":"develop"`) {
		t.Fatalf("request altered: %s", body)
	}
}

// TestFileCreateAcceptanceCarrierStdin: the check runs on the final
// request, so a --stdin body carrying the bundle gets the same advisory.
func TestFileCreateAcceptanceCarrierStdin(t *testing.T) {
	env, _ := carrierEnv(t, carrierDaemon(t))
	body := `{"project":"polyforge","goal":"ship it","bundle_name":"develop","content":"acceptance: a new e2e scenario"}`
	code, stdout, stderr := runFileCreateStdin(t, env, body, "file", "create", "--stdin")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, carrierHint+" develop =") || strings.Contains(stderr, carrierWarning) {
		t.Fatalf("stdin path misses the advisory; stderr=%q", stderr)
	}
	if strings.Contains(stdout, carrierWarning) {
		t.Errorf("advisory leaked to stdout: %q", stdout)
	}
}

// runFileCreateStdin drives the full CLI with a stdin body (runPf always
// passes empty stdin); the env handling matches runPf.
func runFileCreateStdin(t *testing.T, env map[string]string, stdin string, args ...string) (int, string, string) {
	t.Helper()
	clearEnv(t)
	if _, ok := env["XDG_CONFIG_HOME"]; !ok {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	env = viaCellProxy(t, env)
	for k, v := range env {
		t.Setenv(k, v)
	}
	var stdout, stderr bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}
