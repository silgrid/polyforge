// carries: dist/workstation/smoke.sh, test/testdata/workstation-smoke-stub/docker
package test

// w168_clean_room_test.go — polyforge#168 (91-clean-room): the clean-room
// acceptance's two deliverable halves. TestWorkstationSmokeOnboarding drives
// `sh smoke.sh onboarding` through the stub engine and a REAL loopback HTTP
// server standing in for Pi Web's /api/onboarding (the stub runs the smoke's
// in-container python validator for real, so the JSON contract, the Basic
// authentication and the bounded poll are exercised, never faked): a fresh
// volume's unfinished wizard, the doctor's necessary rows, the one default
// Cell on the local AIHub and the clean host each pass, and a wrong wizard,
// a wrong doctor row, a wrong Cell or a dirty host each fail naming the
// miss. TestW168CleanRoomChecklist pins the manual checklist and the
// sanitized evidence template in docs/reference/release.md — the acceptance
// IS the document (AC4), so its text is the behaviour.
//
// Expected values are the ticket's and its settled rulings', never the
// implementation's: AC1's unfinished wizard/local default Cell/clean host,
// the owner's Q2 (the doctor necessary rows + cell list are the joint proof)
// and Q4 (an HTTP 200 alone is not a pass), polyforge#167's closed onboarding
// view (its four steps, nextStep and the first-boot connection whitelist),
// and polyforge#176's first-boot constants (http://127.0.0.1:8420, default).

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// w168Password is the smoke's own fixed test password (settled Q5): the
// tests never inject PI_WEB_PASSWORD, so the pass also proves the script
// carries its default.
const w168Password = "pf-workstation-smoke-test-password"

// w168DoctorRows are the default Cell's necessary health rows (owner Q2):
// the local AIHub's data plane, the mapping it serves and the daemon that
// fronts it. Every other doctor row may warn or skip in a clean room.
var w168DoctorRows = []string{
	"config", "workspace mapping", "workspace dirs", "server reach",
	"daemon socket", "daemon supervision", "workflow lanes",
	"aihub projects", "auth",
}

// w168FreshView is the closed onboarding view of a fresh volume
// (polyforge#167's shape, polyforge#176's local first boot).
func w168FreshView() map[string]any {
	return map[string]any{
		"imageManaged": true,
		"version":      1,
		"steps": map[string]any{
			"aihub": "incomplete", "account": "incomplete",
			"workspace": "incomplete", "ticket": "incomplete",
		},
		"nextStep":  "aihub",
		"dismissed": false,
		"connection": map[string]any{
			"phase": "done", "mode": "local", "defaultCell": "default",
			"server": "http://127.0.0.1:8420", "project": "workstation",
			"userReady": true, "keyReady": true,
		},
	}
}

// w168CloneView deep-copies a view so a subtest mutates only its own.
func w168CloneView(t *testing.T, view map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// w168Server is the fake Pi Web: it enforces the Basic authentication the
// smoke must send, writes the completed steps a POST asks for, and serves
// the scenario's view. changeAfter > 0 makes every GET past that count
// answer a state with the ticket step back to incomplete — the persist
// pass's changed-value probe.
type w168Server struct {
	mu          sync.Mutex
	view        map[string]any
	raw         string
	status      int
	gets        int
	changeAfter int
}

func (s *w168Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("pi:"+w168Password))
	if r.Header.Get("Authorization") != want {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		s.gets++
		if s.status != 0 {
			w.WriteHeader(s.status)
			return
		}
		if s.raw != "" {
			io.WriteString(w, s.raw)
			return
		}
		view := s.view
		if s.changeAfter > 0 && s.gets > s.changeAfter {
			view = w168CloneViewNoT(s.view)
			view["steps"].(map[string]any)["ticket"] = "incomplete"
			view["nextStep"] = "ticket"
		}
		_ = json.NewEncoder(w).Encode(view)
	case http.MethodPost:
		var body struct {
			Action string `json:"action"`
			Step   string `json:"step"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Action == "complete" {
			if steps, ok := s.view["steps"].(map[string]any); ok {
				steps[body.Step] = "complete"
			}
			s.view["nextStep"] = "aihub"
		}
		_ = json.NewEncoder(w).Encode(s.view)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// w168CloneViewNoT is w168CloneView for the handler (no *testing.T there).
func w168CloneViewNoT(view map[string]any) map[string]any {
	raw, _ := json.Marshal(view)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// w168StartServer starts the fake Pi Web and answers its URL.
func w168StartServer(t *testing.T, server *w168Server) *httptest.Server {
	t.Helper()
	if server.view == nil {
		server.view = w168FreshView()
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return httpServer
}

// w168Doctor builds the {ok, checks} document with the necessary rows pass
// (overrides change one row's status) plus a warn row a clean room keeps.
func w168Doctor(overrides map[string]string) map[string]any {
	checks := make([]any, 0, len(w168DoctorRows)+1)
	for _, name := range w168DoctorRows {
		status := "pass"
		if want, ok := overrides[name]; ok {
			status = want
		}
		checks = append(checks, map[string]any{
			"name": name, "status": status, "detail": "stub: " + name, "cell": "default",
		})
	}
	checks = append(checks, map[string]any{
		"name": "disk watermark", "status": "warn", "detail": "stub: a clean room may warn here", "cell": "default",
	})
	return map[string]any{"ok": true, "checks": checks}
}

// w168Cells builds the GET /profiles registry with the one default Cell.
func w168Cells(profile, state string, valid bool, server string) map[string]any {
	if profile == "" {
		return map[string]any{"profiles": []any{}}
	}
	return map[string]any{"profiles": []any{map[string]any{
		"profile": profile, "state": state, "valid": valid, "server": server,
		"workspaces": []string{"/workspace"}, "pending_change": "",
	}}}
}

// w168Scenario writes the stub scenario's doctor and Cell registry answers.
func w168Scenario(t *testing.T, doctor, cells any) string {
	t.Helper()
	dir := t.TempDir()
	for name, doc := range map[string]any{"doctor.json": doctor, "cell-list.json": cells} {
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// w168Bin answers a restricted PATH: the POSIX tools the smoke uses and the
// stub docker, never a python3/curl/pf — the clean-room host the ticket
// promises (Docker + the system sh only), and the proof the in-container
// checks use no host tool.
func w168Bin(t *testing.T) string {
	t.Helper()
	root := w535Root(t)
	bin := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "test", "testdata", "workstation-smoke-stub", "docker"), filepath.Join(bin, "docker")); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"sh", "env", "cat", "cp", "date", "dirname", "grep", "mkdir", "mktemp", "ps", "rm", "sed", "sleep", "tr"} {
		if err := os.Symlink(filepath.Join("/usr/bin", tool), filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// w168RunOnboarding runs `smoke.sh onboarding` against a scenario and a
// fake Pi Web, on the Docker+sh-only PATH.
func w168RunOnboarding(t *testing.T, dir, url string, extraEnv ...string) (string, error) {
	t.Helper()
	return w168RunOnboardingBin(t, w168Bin(t), dir, url, extraEnv...)
}

func w168RunOnboardingBin(t *testing.T, bin, dir, url string, extraEnv ...string) (string, error) {
	t.Helper()
	script := filepath.Join(w535Root(t), "dist", "workstation", "smoke.sh")
	cmd := exec.Command("sh", script, "onboarding")
	cmd.Env = append([]string{
		"PATH=" + bin,
		"HOME=" + os.Getenv("HOME"),
		"W535_SMOKE_SCENARIO=" + dir,
		"WORKSTATION_SMOKE_ONBOARDING_URL=" + url,
		"WORKSTATION_SMOKE_ONBOARDING_DEADLINE=10",
	}, extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestWorkstationSmokeOnboarding is AC1's script half: the smoke passes only
// on a fresh, local, image-managed first open with the doctor and Cell
// registry agreeing and a clean host, and fails naming each miss.
func TestWorkstationSmokeOnboarding(t *testing.T) {
	root := w535Root(t)
	if err := exec.Command("sh", "-n", filepath.Join(root, "dist", "workstation", "smoke.sh")).Run(); err != nil {
		t.Fatalf("polyforge#168: smoke.sh must parse as POSIX sh: %v", err)
	}
	passScenario := func(t *testing.T) string {
		return w168Scenario(t,
			w168Doctor(map[string]string{}),
			w168Cells("default", "serving", true, "http://127.0.0.1:8420"))
	}

	t.Run("the clean-room first open passes", func(t *testing.T) {
		server := w168StartServer(t, &w168Server{})
		out, err := w168RunOnboarding(t, passScenario(t), server.URL)
		if err != nil {
			t.Fatalf("the onboarding pass must pass on a fresh local image-managed machine:\n%s", out)
		}
		for _, want := range []string{
			"the host carries no pf/aihub/pi/claude component",
			"onboarding state incomplete (next aihub), connection local http://127.0.0.1:8420",
			"doctor necessary rows pass",
			"Cell registry = default serving valid server http://127.0.0.1:8420",
			"onboarding: PASS",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("the passing onboarding run must print %q:\n%s", want, out)
			}
		}
	})
	t.Run("a completed wizard is not a fresh first open", func(t *testing.T) {
		view := w168FreshView()
		steps := view["steps"].(map[string]any)
		for step := range steps {
			steps[step] = "complete"
		}
		server := w168StartServer(t, &w168Server{view: view})
		out, err := w168RunOnboarding(t, passScenario(t), server.URL)
		if err == nil {
			t.Fatalf("a completed wizard on a fresh volume must fail — the first open is not the guide:\n%s", out)
		}
		if !strings.Contains(out, "want incomplete") {
			t.Fatalf("the failure must name the completed step:\n%s", out)
		}
	})
	t.Run("a host-mode Pi Web is not the image's first open", func(t *testing.T) {
		view := w168FreshView()
		view["imageManaged"] = false
		server := w168StartServer(t, &w168Server{view: view})
		out, err := w168RunOnboarding(t, passScenario(t), server.URL)
		if err == nil {
			t.Fatalf("an imageManaged=false answer must fail — a host-mode Pi Web has no wizard:\n%s", out)
		}
		if !strings.Contains(out, "not the image-managed workstation") {
			t.Fatalf("the failure must name imageManaged:\n%s", out)
		}
	})
	t.Run("invalid JSON fails, an HTTP 200 is not a pass", func(t *testing.T) {
		server := w168StartServer(t, &w168Server{raw: "not-json"})
		out, err := w168RunOnboarding(t, passScenario(t), server.URL)
		if err == nil {
			t.Fatalf("an unparsable onboarding answer must fail:\n%s", out)
		}
		if !strings.Contains(out, "not valid JSON") {
			t.Fatalf("the failure must name the invalid JSON:\n%s", out)
		}
	})
	t.Run("an unknown field fails the closed whitelist", func(t *testing.T) {
		view := w168FreshView()
		view["surprise"] = true
		server := w168StartServer(t, &w168Server{view: view})
		out, err := w168RunOnboarding(t, passScenario(t), server.URL)
		if err == nil {
			t.Fatalf("an unknown onboarding field must fail — the view is a closed whitelist:\n%s", out)
		}
		if !strings.Contains(out, "unknown field") {
			t.Fatalf("the failure must name the unknown field:\n%s", out)
		}
	})
	t.Run("a skipped doctor row fails naming the row", func(t *testing.T) {
		server := w168StartServer(t, &w168Server{})
		dir := w168Scenario(t,
			w168Doctor(map[string]string{"server reach": "skip"}),
			w168Cells("default", "serving", true, "http://127.0.0.1:8420"))
		out, err := w168RunOnboarding(t, dir, server.URL)
		if err == nil {
			t.Fatalf("a skipped necessary doctor row must fail the clean-room pass:\n%s", out)
		}
		if !strings.Contains(out, "\"server reach\"") {
			t.Fatalf("the failure must name the doctor row:\n%s", out)
		}
	})
	t.Run("a remote Cell server fails the local-default proof", func(t *testing.T) {
		server := w168StartServer(t, &w168Server{})
		dir := w168Scenario(t,
			w168Doctor(map[string]string{}),
			w168Cells("default", "serving", true, "http://hub.example.invalid:8420"))
		out, err := w168RunOnboarding(t, dir, server.URL)
		if err == nil {
			t.Fatalf("a default Cell pointing at a remote AIHub must fail the single-user proof:\n%s", out)
		}
		if !strings.Contains(out, "the default Cell's server is") {
			t.Fatalf("the failure must name the Cell server:\n%s", out)
		}
	})
	t.Run("a missing default Cell fails the registry proof", func(t *testing.T) {
		server := w168StartServer(t, &w168Server{})
		dir := w168Scenario(t, w168Doctor(map[string]string{}), w168Cells("", "", false, ""))
		out, err := w168RunOnboarding(t, dir, server.URL)
		if err == nil {
			t.Fatalf("a registry without the default Cell must fail:\n%s", out)
		}
		if !strings.Contains(out, "want exactly the default Cell") {
			t.Fatalf("the failure must name the missing default Cell:\n%s", out)
		}
	})
	t.Run("a dirty host fails the clean-room precondition", func(t *testing.T) {
		server := w168StartServer(t, &w168Server{})
		bin := w168Bin(t)
		if err := os.WriteFile(filepath.Join(bin, "pf"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := w168RunOnboardingBin(t, bin, passScenario(t), server.URL)
		if err == nil {
			t.Fatalf("a host carrying pf must fail the clean-room pass:\n%s", out)
		}
		if !strings.Contains(out, "the host carries pf") {
			t.Fatalf("the failure must name the host component:\n%s", out)
		}
	})
	t.Run("a Pi Web without #167 fails naming the missing contract", func(t *testing.T) {
		server := w168StartServer(t, &w168Server{status: http.StatusNotFound})
		out, err := w168RunOnboarding(t, passScenario(t), server.URL)
		if err == nil {
			t.Fatalf("a Pi Web without /api/onboarding must fail:\n%s", out)
		}
		if !strings.Contains(out, "polyforge#167") {
			t.Fatalf("the failure must name the missing onboarding contract:\n%s", out)
		}
	})
	t.Run("an unready Pi Web is a bounded timeout, not a hang", func(t *testing.T) {
		server := w168StartServer(t, &w168Server{status: http.StatusServiceUnavailable})
		out, err := w168RunOnboarding(t, passScenario(t), server.URL,
			"WORKSTATION_SMOKE_ONBOARDING_DEADLINE=1")
		if err == nil {
			t.Fatalf("a Pi Web that never answers must fail at the deadline:\n%s", out)
		}
		if !strings.Contains(out, "did not answer /api/onboarding within") {
			t.Fatalf("the failure must name the bounded wait:\n%s", out)
		}
	})
}
