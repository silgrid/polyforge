package cli

// file_create_acceptance_carrier_test.go: the file-create acceptance-carrier
// advisory. For an execution ticket the verb prints the
// chosen bundle's step table on stderr (resident hint), and warns
// specifically when the content mentions e2e but no step of that bundle
// declares the e2e_author_mode var. Warn only: the request, the exit code
// and stdout (including --json) stay unchanged; a registration ticket or an
// unreachable daemon gets no advisory at all.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	carrierHint    = "file create: bundle steps:"
	carrierWarning = "file create: acceptance-carrier warning:"
)

// carrierDaemon serves GET /status with a workflows dir holding a chore
// bundle (no step authors e2e) and a feature-full bundle whose e2e-author
// step declares e2e_author_mode; it answers the daemon socket path.
func carrierDaemon(t *testing.T) string {
	t.Helper()
	wf := t.TempDir()
	bundles := map[string]string{
		"chore":        "---\nsteps:\n  - { id: plan }\n  - { id: code }\n  - { id: review }\n  - { id: verification }\n  - { id: deliver }\n---\n",
		"feature-full": "---\nsteps:\n  - { id: plan }\n  - { id: e2e-author, vars: { e2e_author_mode: required } }\n  - { id: code }\n  - { id: verification }\n---\n",
	}
	for name, body := range bundles {
		if err := os.MkdirAll(filepath.Join(wf, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wf, name, "workflow.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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
			"chore + e2e content: hint and narrow warning",
			[]string{"--bundle", "chore", "--content", e2eContent},
			carrierHint + " chore = plan -> code -> review -> verification -> deliver",
			true,
		},
		{
			"feature-full + e2e content: hint, no narrow warning",
			[]string{"--bundle", "feature-full", "--content", e2eContent},
			carrierHint + " feature-full = plan -> e2e-author -> code -> verification",
			false,
		},
		{
			"chore without e2e content: hint only",
			[]string{"--bundle", "chore", "--content", "unit tests carried by code"},
			carrierHint + " chore = plan -> code -> review -> verification -> deliver",
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
				t.Fatalf("narrow warning present=%v, want %v; stderr=%q", got, tc.wantWarn, stderr)
			}
			if tc.wantWarn {
				for _, tok := range []string{`"chore"`, "e2e_author_mode", "pf file edit --bundle", "explicit waiver"} {
					if !strings.Contains(stderr, tok) {
						t.Errorf("warning misses %q: %q", tok, stderr)
					}
				}
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

// TestFileCreateAcceptanceCarrierDaemonUnreachable: no daemon, no advisory,
// and the create still succeeds.
func TestFileCreateAcceptanceCarrierDaemonUnreachable(t *testing.T) {
	env, rec := carrierEnv(t, filepath.Join(t.TempDir(), "absent.sock"))
	code, _, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "ship it",
		"--bundle", "chore", "--content", "the new e2e scenario passes")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if strings.Contains(stderr, carrierHint) || strings.Contains(stderr, carrierWarning) {
		t.Fatalf("advisory without a daemon: %q", stderr)
	}
	if body := rec.last().Body; !strings.Contains(body, `"bundle_name":"chore"`) {
		t.Fatalf("create not sent: %s", body)
	}
}

// TestFileCreateAcceptanceCarrierJSONOutputUnchanged: --json stdout is
// byte-identical when both advisory lines fire; only stderr changes.
func TestFileCreateAcceptanceCarrierJSONOutputUnchanged(t *testing.T) {
	env, rec := carrierEnv(t, carrierDaemon(t))
	code, stdout, stderr := runPf(t, env, "--json", "file", "create", "--project", "polyforge",
		"--goal", "ship it", "--bundle", "chore", "--content", "the new e2e scenario passes")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, carrierHint) || !strings.Contains(stderr, carrierWarning) {
		t.Fatalf("expected both advisory lines; stderr=%q", stderr)
	}
	if strings.TrimSpace(stdout) != cannedWI {
		t.Fatalf("--json stdout changed: %q", stdout)
	}
	if body := rec.last().Body; !strings.Contains(body, `"bundle_name":"chore"`) {
		t.Fatalf("request altered: %s", body)
	}
}

// TestFileCreateAcceptanceCarrierStdin: the check runs on the final
// request, so a --stdin body carrying bundle and content warns the same.
func TestFileCreateAcceptanceCarrierStdin(t *testing.T) {
	env, _ := carrierEnv(t, carrierDaemon(t))
	body := `{"project":"polyforge","goal":"ship it","bundle_name":"chore","content":"acceptance: a new e2e scenario"}`
	code, stdout, stderr := runFileCreateStdin(t, env, body, "file", "create", "--stdin")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, carrierHint+" chore =") || !strings.Contains(stderr, carrierWarning) {
		t.Fatalf("stdin path misses the advisory; stderr=%q", stderr)
	}
	if strings.Contains(stdout, carrierWarning) {
		t.Errorf("advisory leaked to stdout: %q", stdout)
	}
}
