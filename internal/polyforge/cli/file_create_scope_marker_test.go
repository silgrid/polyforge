package cli

// file_create_scope_marker_test.go: the file-create scoped-tier placement
// guard. When the final create request's goal mentions the
// scoped marker but the content carries no exact marker line, the verb
// warns on stderr. Warn only: the request, the created work item, the
// exit code and stdout (including --json) stay unchanged. The warning
// condition must agree with the shared scopetier predicate the driver
// itself uses to decide the tier.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/scopetier"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// runFileCreateStdin drives the full CLI with a stdin body (runPf always
// passes empty stdin); the env handling matches runPf.
func runFileCreateStdin(t *testing.T, env map[string]string, stdin string, args ...string) (int, string, string) {
	t.Helper()
	clearEnv(t)
	if _, ok := env["XDG_CONFIG_HOME"]; !ok {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	var stdout, stderr bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestFileCreateScopeMarkerPlacementWarning pins the flag path: the #44
// shape (goal marker, no content) and every near-miss warn; an exact
// content line and a goal without the marker do not. The expected
// warning is derived from the shared scopetier predicate so the guard
// and the driver cannot drift (oracle e). The request stays verbatim
// (oracle d).
func TestFileCreateScopeMarkerPlacementWarning(t *testing.T) {
	cases := []struct {
		name       string
		goal       string
		content    string
		hasContent bool
	}{
		{" shape: goal marker, content absent", "ship the guard (verification: scoped)", "", false},
		{"goal marker, empty content", "ship the guard (verification: scoped)", "", true},
		{"goal marker, content near-miss", "ship the guard (verification: scoped)", "- verification: scoped", true},
		{"goal marker, wrong-case content", "ship the guard (verification: scoped)", "Verification: Scoped", true},
		{"goal marker, embedded in prose", "ship the guard (verification: scoped)", "note: verification: scoped is the marker", true},
		{"goal marker, exact content line", "ship the guard (verification: scoped)", "background\nverification: scoped\n", true},
		{"goal marker, exact line padded", "ship the guard (verification: scoped)", "  \tverification: scoped \t ", true},
		{"goal spelling is case-insensitive", "ship the guard (Verification: Scoped)", "", false},
		{"no goal marker, no content", "ship the guard", "", false},
		{"no goal marker, near-miss content", "ship the guard", "- verification: scoped", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec reqRecorder
			srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, cannedWI)
			})
			env := withMappingConfig(t)
			env["PF_SERVER"] = srv.URL
			env["PF_API_KEY"] = "k_cli"
			args := []string{"file", "create", "--project", "polyforge", "--goal", tc.goal}
			if tc.hasContent {
				args = append(args, "--content", tc.content)
			}
			code, stdout, stderr := runPf(t, env, args...)
			if code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr)
			}
			// Oracle e: the warning condition is derived from the
			// shared predicate.
			want := strings.Contains(strings.ToLower(tc.goal), strings.ToLower(scopetier.Marker)) &&
				!(tc.hasContent && scopetier.OptedIn(tc.content))
			if got := strings.Contains(stderr, "file create: warning:"); got != want {
				t.Fatalf("warning present=%v, want %v; stderr=%q", got, want, stderr)
			}
			if want {
				for _, tok := range []string{"own line", "goal", "full verification tier"} {
					if !strings.Contains(stderr, tok) {
						t.Errorf("warning %q misses %q", stderr, tok)
					}
				}
			} else if strings.Contains(stderr, "warning") {
				t.Errorf("unexpected warning on stderr: %q", stderr)
			}
			if strings.Contains(stdout, "warning") {
				t.Errorf("the warning leaked to stdout: %q", stdout)
			}
			// Oracle d: the request carries goal/content verbatim.
			body := rec.last().Body
			if !strings.Contains(body, `"goal":`+jsonBody(tc.goal)) {
				t.Errorf("request goal altered: %s", body)
			}
			if tc.hasContent && !strings.Contains(body, `"content":`+jsonBody(tc.content)) {
				t.Errorf("request content altered: %s", body)
			}
		})
	}
}

// TestFileCreateScopeMarkerPlacementWarningStdin pins the --stdin path:
// the check applies to the final JSON request (grill row 3). A body with
// a goal marker and no content warns; an exact content line does not.
func TestFileCreateScopeMarkerPlacementWarningStdin(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"stdin #44 shape", `{"project":"polyforge","goal":"ship (verification: scoped)"}`},
		{"stdin near-miss content", `{"project":"polyforge","goal":"ship (verification: scoped)","content":"- verification: scoped"}`},
		{"stdin exact content marker", `{"project":"polyforge","goal":"ship (verification: scoped)","content":"verification: scoped\n"}`},
		{"stdin no goal marker", `{"project":"polyforge","goal":"ship"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec reqRecorder
			srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, cannedWI)
			})
			env := withMappingConfig(t)
			env["PF_SERVER"] = srv.URL
			env["PF_API_KEY"] = "k_cli"
			code, stdout, stderr := runFileCreateStdin(t, env, tc.body, "file", "create", "--stdin")
			if code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr)
			}
			var req aihubclient.CreateWorkItemRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatalf("bad test body: %v", err)
			}
			want := strings.Contains(strings.ToLower(req.Goal), strings.ToLower(scopetier.Marker)) &&
				!(req.Content != nil && scopetier.OptedIn(*req.Content))
			if got := strings.Contains(stderr, "file create: warning:"); got != want {
				t.Fatalf("warning present=%v, want %v; stderr=%q", got, want, stderr)
			}
			if strings.Contains(stdout, "warning") {
				t.Errorf("the warning leaked to stdout: %q", stdout)
			}
			if body := rec.last().Body; !strings.Contains(body, `"goal":`) {
				t.Errorf("stdin request not sent verbatim: %s", body)
			}
		})
	}
}

// TestFileCreateScopeMarkerWarningJSONOutputUnchanged pins that --json
// stdout is byte-identical when the warning fires: only stderr changes.
func TestFileCreateScopeMarkerWarningJSONOutputUnchanged(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_cli"
	code, stdout, stderr := runPf(t, env,
		"--json", "file", "create", "--project", "polyforge",
		"--goal", "ship (verification: scoped)", "--bundle", "autopilot")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "file create: warning:") {
		t.Fatalf("expected the placement warning; stderr=%q", stderr)
	}
	if strings.TrimSpace(stdout) != cannedWI {
		t.Fatalf("--json stdout changed: %q", stdout)
	}
	if body := rec.last().Body; !strings.Contains(body, `"bundle_name":"autopilot"`) {
		t.Fatalf("request altered: %s", body)
	}
}
