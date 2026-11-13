package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// verbs_doctor_test.go: the `pf doctor` unit pins — the report shape,
// the exit codes (0 all-pass / 1 any-fail / 2 usage), each check's
// distinct failure, and the not-faked-green contract on an unloadable
// config.

// doctorDoc decodes the --json report.
type doctorDoc struct {
	OK     bool `json:"ok"`
	Checks []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	} `json:"checks"`
}

func doctorStatus(t *testing.T, env map[string]string, args ...string) (int, string, doctorDoc) {
	t.Helper()
	code, stdout, stderr := runPf(t, env, args...)
	var doc doctorDoc
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &doc); err != nil {
		t.Fatalf("doctor --json is not the pinned {ok, checks} shape: %v (stdout %q stderr %q)", err, stdout, stderr)
	}
	return code, stdout, doc
}

// doctorFind answers the check with the given name.
func doctorFind(t *testing.T, doc doctorDoc, name string) (string, string) {
	t.Helper()
	for _, c := range doc.Checks {
		if c.Name == name {
			return c.Status, c.Detail
		}
	}
	t.Fatalf("no check named %q in %+v", name, doc.Checks)
	return "", ""
}

// doctorServerConfig points a scratch config at srv with a live mapped
// workspace and the given daemon socket, and answers the runPf env.
func doctorServerConfig(t *testing.T, server, apiKey, socket string, projects ...string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("server = \"" + server + "\"\napi_key = \"" + apiKey + "\"\n")
	// the doctor's Hub reads go through the daemon's data-plane proxy:
	// the front stands in for it (and relays GET /status to socket; ""
	// = a live daemon, a dead path = the daemon is down)
	if socket == "" {
		socket = liveDaemonSocket(t, "m-d")
	}
	b.WriteString("daemon_socket = \"" + cellFront(t, server, apiKey, socket) + "\"\n")
	for _, p := range projects {
		b.WriteString("\n[workspace.\"" + filepath.Join(dir, "ws-"+p) + "\"]\nprojects = [\"" + p + "\"]\n")
		if err := os.MkdirAll(filepath.Join(dir, "ws-"+p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"XDG_CONFIG_HOME": xdg}
}

// liveDaemonSocket serves GET /status on a real unix socket.
func liveDaemonSocket(t *testing.T, machineID string) string {
	t.Helper()
	return liveDaemonSocketLanes(t, machineID, "")
}

// liveDaemonSocketLanes is liveDaemonSocket reporting lanes as the lane
// set the daemon runs work items on ("" = no workflows dir).
func liveDaemonSocketLanes(t *testing.T, machineID, lanes string) string {
	t.Helper()
	pfDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pfDir, "pf"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// a daemon reports the PATH its in-process commands run under
		//: this test process's, which carries git; its
		// steps' PATH also holds its own pf
		commandPath, _ := json.Marshal(os.Getenv("PATH"))
		stepPath, _ := json.Marshal(pfDir + string(os.PathListSeparator) + os.Getenv("PATH"))
		lanesJSON, _ := json.Marshal(lanes)
		io.WriteString(w, `{"machine_id":"`+machineID+`","auto":false,"shutting_down":false,"env_names":["HOME","PATH"],"command_path":`+string(commandPath)+`,"step_path":`+string(stepPath)+`,"workflows_dir":`+string(lanesJSON)+`,"jobs":[],"queue":{"pending":0,"running":0,"max_concurrent":1},"reload":{},"model_health":{"window":{"duration":"720h0m0s","from":"f","to":"t"},"warnings":[]}}`)
	})
	return newFakeDaemon(t, mux)
}

// aiSever is a fake aihub for the doctor: per-project GET
// /api/v1/projects/<name>/ready-queue (the member-readable project read).
func aiSever(t *testing.T, rec *reqRecorder, known map[string]bool, status int, body string) *httptest.Server {
	t.Helper()
	return newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && strings.HasSuffix(r.URL.Path, "/ready-queue") {
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/ready-queue")
			if status != 0 {
				w.WriteHeader(status)
				io.WriteString(w, body)
				return
			}
			if known[name] {
				io.WriteString(w, `{"project":"`+name+`","items":[],"running":[],"paused":[]}`)
				return
			}
			w.WriteHeader(404)
			io.WriteString(w, `{"code":"CORE-404","detail":"no such project"}`)
			return
		}
		io.WriteString(w, `{}`)
	})
}

func TestDoctorHealthyMachineAllPassExit0(t *testing.T) {
	srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
	// the daemon runs work items on the shipped lanes
	lanes, _ := filepath.Abs(filepath.Join("..", "..", "..", "workflows"))
	sock := liveDaemonSocketLanes(t, "m-h", lanes)
	env := doctorServerConfig(t, srv.URL, "k_ok", sock, "polyforge")
	// a healthy machine declares the workspace path's memory policy in
	// cli.toml (no block is the `pf init` warn)
	cfgPath := filepath.Join(env["XDG_CONFIG_HOME"], "polyforge", "cli.toml")
	ws := filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge")
	if raw, err := os.ReadFile(cfgPath); err != nil {
		t.Fatal(err)
	} else if err := os.WriteFile(cfgPath, append(raw, "\n[memory_policy.\""+ws+"\".default]\nrecord = [\"r\"]\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	env["HOME"] = displayHome(t, `{"statusLine":{"type":"command","command":"echo main"}}`,
		`{"packages":["~/.local/share/polyforge/release/current"]}`)
	env["PI_CODING_AGENT_DIR"] = ""
	env["POLYFORGE_DATA_DIR"] = ""
	layoutRelease(t, env["HOME"])
	layoutClaudeLink(t, env["HOME"])
	// both installed harnesses carry the diagram guidance
	for _, p := range []string{".claude/CLAUDE.md", ".pi/agent/AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(env["HOME"], p), []byte(diagramGuidanceBlock), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env["PATH"] = fakeTmuxPath(t, 0) // the restart loop is up (polyforge#174)
	// a healthy machine's project default names a real model: the shipped
	// lanes' candidates are placeholders no gateway serves, which the
	// workspace yaml row warns about
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"), []byte("steps:\n  polyforge:\n    default: { models: [pi:prov/model-a] }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, doc := doctorStatus(t, env, "--json", "doctor")
	if code != 0 {
		t.Fatalf("healthy machine: exit %d, want 0 (output %s)", code, stdout)
	}
	if !doc.OK {
		t.Fatalf("healthy machine: ok = false (%+v)", doc.Checks)
	}
	for _, c := range doc.Checks {
		// the disk watermark row reads the real host's
		// filesystem; it warns only when the host is above the watermark.
		if c.Name == docChDisk && c.Status == "warn" && strings.Contains(c.Detail, "above the 70% watermark") {
			continue
		}
		if c.Status != "pass" {
			t.Fatalf("check %q = %q (%s), want pass", c.Name, c.Status, c.Detail)
		}
	}
	// Human mode: every check line prints, exit 0.
	code, stdout, _ = runPf(t, env, "doctor")
	if code != 0 || !strings.Contains(stdout, "pass") {
		t.Fatalf("human doctor: exit %d output %q", code, stdout)
	}
}

func TestDoctorMappingChecksFailDistinctly(t *testing.T) {
	t.Run("mapping absent", func(t *testing.T) {
		srv := aiSever(t, &reqRecorder{}, nil, 0, "")
		env := doctorServerConfig(t, srv.URL, "k", "")
		code, _, doc := doctorStatus(t, env, "--json", "doctor")
		if code != 1 || doc.OK {
			t.Fatalf("exit %d ok %v", code, doc.OK)
		}
		st, detail := doctorFind(t, doc, "workspace mapping")
		if st != "fail" || !strings.Contains(detail, "[workspace") {
			t.Fatalf("mapping check = %s %q", st, detail)
		}
	})
	t.Run("workspace dir missing", func(t *testing.T) {
		srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
		dir := t.TempDir()
		xdg := filepath.Join(dir, "xdg")
		if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
			t.Fatal(err)
		}
		missing := filepath.Join(dir, "gone-ws")
		cfg := "server = \"" + srv.URL + "\"\napi_key = \"k\"\n\n[workspace.\"" + missing + "\"]\nprojects = [\"polyforge\"]\n"
		if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, doc := doctorStatus(t, map[string]string{"XDG_CONFIG_HOME": xdg}, "--json", "doctor")
		if code != 1 || doc.OK {
			t.Fatalf("exit %d ok %v", code, doc.OK)
		}
		st, detail := doctorFind(t, doc, "workspace dirs")
		if st != "fail" || !strings.Contains(detail, "gone-ws") {
			t.Fatalf("dirs check = %s %q", st, detail)
		}
	})
	t.Run("project unknown to aihub", func(t *testing.T) {
		srv := aiSever(t, &reqRecorder{}, map[string]bool{}, 0, "")
		env := doctorServerConfig(t, srv.URL, "k", "", "polyforge")
		code, stdout, doc := doctorStatus(t, env, "--json", "doctor")
		if code != 1 || doc.OK {
			t.Fatalf("exit %d ok %v (output %s)", code, doc.OK, stdout)
		}
		st, detail := doctorFind(t, doc, "aihub projects")
		if st != "fail" || !strings.Contains(detail, "polyforge") {
			t.Fatalf("projects check = %s %q", st, detail)
		}
		if !strings.Contains(detail, "ws-polyforge") {
			t.Fatalf("projects check detail %q must name the project's workspace", detail)
		}
	})
	t.Run("auth rejected", func(t *testing.T) {
		srv := aiSever(t, &reqRecorder{}, nil, 401, `{"code":"AUTH-401","detail":"bad key"}`)
		env := doctorServerConfig(t, srv.URL, "k_bad", "", "polyforge")
		code, _, doc := doctorStatus(t, env, "--json", "doctor")
		if code != 1 || doc.OK {
			t.Fatalf("exit %d ok %v", code, doc.OK)
		}
		st, detail := doctorFind(t, doc, "auth")
		if st != "fail" {
			t.Fatalf("auth check = %s %q", st, detail)
		}
	})
}

func TestDoctorConnectivityFailsDistinctly(t *testing.T) {
	t.Run("server unreachable", func(t *testing.T) {
		env := doctorServerConfig(t, deadAihubURL(t), "k", "", "polyforge")
		code, _, doc := doctorStatus(t, env, "--json", "doctor")
		if code != 1 || doc.OK {
			t.Fatalf("exit %d ok %v", code, doc.OK)
		}
		st, _ := doctorFind(t, doc, "server reach")
		if st != "fail" {
			t.Fatalf("server check not failing: %+v", doc.Checks)
		}
		// The aihub-flavored checks must not fake green behind a dead server.
		for _, name := range []string{"aihub projects", "auth"} {
			if st, _ := doctorFind(t, doc, name); st != "skip" && st != "fail" {
				t.Fatalf("%s = %s behind an unreachable server (faked green?)", name, st)
			}
		}
	})
	t.Run("daemon socket dead", func(t *testing.T) {
		srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
		dead := filepath.Join(t.TempDir(), "dead.sock")
		env := doctorServerConfig(t, srv.URL, "k", dead, "polyforge")
		code, _, doc := doctorStatus(t, env, "--json", "doctor")
		if code != 1 || doc.OK {
			t.Fatalf("exit %d ok %v", code, doc.OK)
		}
		st, detail := doctorFind(t, doc, "daemon socket")
		if st != "fail" || !strings.Contains(detail, dead) {
			t.Fatalf("socket check = %s %q", st, detail)
		}
	})
}

func TestDoctorUnloadableConfigDoesNotFakeGreen(t *testing.T) {
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldShape := "server = \"http://x:1\"\napi_key = \"k\"\n[other]\nx = \"1\"\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(oldShape), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"XDG_CONFIG_HOME": xdg}
	code, stdout, doc := doctorStatus(t, env, "--json", "doctor")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (output %s)", code, stdout)
	}
	if doc.OK {
		t.Fatalf("ok = true on an unloadable config")
	}
	st, detail := doctorFind(t, doc, "config")
	if st != "fail" || !strings.Contains(detail, "other") {
		t.Fatalf("config check = %s %q", st, detail)
	}
	skips := 0
	for _, c := range doc.Checks {
		switch c.Name {
		case docChClaudeStatusline, docChPiPackage, docChClaudeLayout, docChPiLayout, docChV1Leftovers, docChRetiredChat:
			continue // the config-independent harness family (polyforge#142, #174)
		}
		if c.Status == "skip" {
			skips++
		}
		if c.Status == "pass" {
			t.Fatalf("check %q faked pass on an unloadable config", c.Name)
		}
	}
	if skips == 0 {
		t.Fatalf("no config-dependent check reported skip (%+v)", doc.Checks)
	}
}

func TestDoctorUsageErrorExit2(t *testing.T) {
	code, _, _ := runPf(t, map[string]string{}, "doctor", "--bogus")
	if code != 2 {
		t.Fatalf("doctor --bogus: exit %d, want 2", code)
	}
	// the retention prune flag is --prune-capsules; the old
	// name has no alias.
	if code, _, _ := runPf(t, map[string]string{}, "doctor", "--prune-git-dirs"); code != 2 {
		t.Fatalf("doctor --prune-git-dirs: exit %d, want 2 (renamed to --prune-capsules, no alias)", code)
	}
}

// TestDoctorWorkspaceYamlReportsUnknownStepKeys (polyforge#169):
// a per-step key of a mapped workspace's .polyforge.yaml that
// is no step id of the daemon's installed workflows — the retired
// verify-judge key among them — is stale: the check warns, naming each
// such key, the rewrite and the upgrade section; a known step id passes;
// without the daemon's workflows dir the keys are unjudged (warn, never a
// faked pass).
func TestDoctorWorkspaceYamlReportsUnknownStepKeys(t *testing.T) {
	wf := t.TempDir()
	writeLane(t, wf, "autopilot", "steps:\n  - { id: fix }\n")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"machine_id":"m-y","workflows_dir":"`+wf+`","jobs":[],"queue":{}}`)
	})
	sock := newFakeDaemon(t, mux)
	srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
	env := doctorServerConfig(t, srv.URL, "k", sock, "polyforge")
	yaml := filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge", ".polyforge.yaml")

	if err := os.WriteFile(yaml, []byte("steps:\n  polyforge:\n    default: { models: [pi:a] }\n    fix: { models: [pi:b] }\n    gate: { models: [pi:c] }\n    verify-judge: { models: [pi:d] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, "workspace yaml"); st != "warn" || !strings.Contains(detail, "steps.polyforge.gate, ") || !strings.Contains(detail, "steps.polyforge.verify-judge") ||
		strings.Contains(detail, "steps.polyforge.fix") || strings.Contains(detail, "steps.polyforge.default") || !strings.Contains(detail, "docs/upgrade.md") || !strings.Contains(detail, workspace.KeyRenameSection) {
		t.Fatalf("stale step keys: %s %q, want warn naming only steps.polyforge.gate and steps.polyforge.verify-judge, with the upgrade section", st, detail)
	}

	if err := os.WriteFile(yaml, []byte("steps:\n  polyforge:\n    fix: { models: [pi:b] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, "workspace yaml"); st != "pass" {
		t.Fatalf("known step key: %s %q, want pass", st, detail)
	}

	if err := os.WriteFile(yaml, []byte("models:\n  polyforge:\n    fix: [b]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, "workspace yaml"); st != "fail" || !strings.Contains(detail, `unknown top-level key "models"`) {
		t.Fatalf("legacy models: section: %s %q, want fail naming the unknown key", st, detail)
	}

	if err := os.WriteFile(yaml, []byte("steps:\n  ghost:\n    fix: { models: [pi:b] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, "workspace yaml"); st != "fail" || !strings.Contains(detail, "ghost are not mapped to this workspace in cli.toml (it maps: polyforge)") {
		t.Fatalf("unmapped project: %s %q, want fail naming it", st, detail)
	}

	env = doctorServerConfig(t, srv.URL, "k", liveDaemonSocket(t, "m-old"), "polyforge")
	yaml = filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge", ".polyforge.yaml")
	if err := os.WriteFile(yaml, []byte("steps:\n  polyforge:\n    fix: { models: [pi:b] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, "workspace yaml"); st != "warn" || !strings.Contains(detail, "reported no workflows dir") {
		t.Fatalf("no workflows dir reported: %s %q, want warn", st, detail)
	}

	env = doctorServerConfig(t, srv.URL, "k", filepath.Join(t.TempDir(), "dead.sock"), "polyforge")
	yaml = filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge", ".polyforge.yaml")
	if err := os.WriteFile(yaml, []byte("steps:\n  polyforge:\n    fix: { models: [pi:b] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, "workspace yaml"); st != "warn" || !strings.Contains(detail, "daemon is unreachable") {
		t.Fatalf("daemon unreachable: %s %q, want warn naming it", st, detail)
	}
}

// TestRetiredEnvCheck (decision 2026-10-16 G2): doctor warns about a
// retired harness variable in its own shell and relays the daemon's.
func TestRetiredEnvCheck(t *testing.T) {
	none := func(string) string { return "" }
	if c := retiredEnvCheck(none, nil); c.Status != "pass" {
		t.Fatalf("clean env: %+v", c)
	}
	c := retiredEnvCheck(func(k string) string {
		if k == "POLYFORGE_OPERATOR_AGENT_DIR" {
			return "/op"
		}
		return ""
	}, []string{"POLYFORGE_HARNESS_ROOT=/x is set but no longer read"})
	if c.Status != "warn" || !strings.Contains(c.Detail, `[daemon] operator_agent_dir = "/op"`) || !strings.Contains(c.Detail, "daemon: POLYFORGE_HARNESS_ROOT=/x") {
		t.Fatalf("retired env check = %+v", c)
	}
}

// env hygiene: the "daemon env" check judges the running daemon's
// environment NAMES (GET /status env_names) — ANTHROPIC_* and other
// credential-looking names outside the ensure allow-list warn with the
// clean-restart fix; the allow-listed set passes; names only, never
// values.
func TestDaemonEnvCheck(t *testing.T) {
	if c := daemonEnvCheck(false, nil, nil); c.Status != "skip" {
		t.Fatalf("daemon down: %+v", c)
	}
	if c := daemonEnvCheck(true, nil, nil); c.Status != "skip" || !strings.Contains(c.Detail, "older daemon") {
		t.Fatalf("older daemon: %+v", c)
	}
	clean := []string{"HOME", "LANG", "LC_ALL", "PATH", "POLYFORGE_DATA_DIR", "POLYFORGE_SUPERVISOR", "PWD", "https_proxy", "SSH_AUTH_SOCK_DIR"}
	if c := daemonEnvCheck(true, clean, []string{"https_proxy"}); c.Status != "pass" {
		t.Fatalf("clean daemon env: %+v", c)
	}
	dirty := append(clean, "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", "FOO_TOKEN", "POLYFORGE_V1_API_KEY", "DB_PASSWORD", "aws_secret")
	c := daemonEnvCheck(true, dirty, nil)
	if c.Status != "warn" {
		t.Fatalf("dirty daemon env: %+v", c)
	}
	for _, want := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", "FOO_TOKEN", "POLYFORGE_V1_API_KEY", "DB_PASSWORD", "aws_secret", "pf ops shutdown", "pf daemon ensure"} {
		if !strings.Contains(c.Detail, want) {
			t.Fatalf("warn detail lacks %q: %s", want, c.Detail)
		}
	}
	for _, clean := range []string{"HOME", "PATH,", "https_proxy"} {
		if strings.Contains(c.Detail, clean) {
			t.Fatalf("warn detail names the allow-listed %q: %s", clean, c.Detail)
		}
	}
}

// env hygiene: a pi models.json `$NAME` outside the allow-list warns
// (pi steps see only the daemon's allow-listed environment).
func TestDaemonEnvCheckPiModelsRefs(t *testing.T) {
	c := daemonEnvCheck(true, []string{"HOME", "PATH"}, []string{"GOPATH", "OPENROUTER_API_KEY", "ZAI_KEY"})
	if c.Status != "warn" || !strings.Contains(c.Detail, "$OPENROUTER_API_KEY, $ZAI_KEY") || strings.Contains(c.Detail, "$GOPATH") || !strings.Contains(c.Detail, "auth.json") {
		t.Fatalf("pi models refs: %+v", c)
	}
}

// ---- prerequisites (polyforge#218): the one warn-only row covering
// gh, curl, python3, tmux and git. Warn-only: a gap makes the setup
// path fail later with its own message; the doctor reports, never
// installs, and never turns a healthy setup red.

func TestPrerequisitesCheck(t *testing.T) {
	everyone := func(string) (string, error) { return "/usr/bin/found", nil }
	if c := prerequisitesCheck(everyone); c.Status != "pass" || !strings.Contains(c.Detail, "gh, curl, python3, tmux, git") {
		t.Fatalf("complete PATH: %+v", c)
	}
	c := prerequisitesCheck(func(tool string) (string, error) {
		if tool == "tmux" || tool == "python3" {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + tool, nil
	})
	if c.Status != "warn" {
		t.Fatalf("missing tools must warn, never fail: %+v", c)
	}
	for _, frag := range []string{"tmux", "brew install tmux", "python3", "xcode-select --install", "the doctor never installs anything"} {
		if !strings.Contains(c.Detail, frag) {
			t.Fatalf("warn detail lacks %q: %+v", frag, c)
		}
	}
	if strings.Contains(c.Detail, "gh") && !strings.Contains(c.Detail, "gh —") {
		t.Fatalf("present tools must not be named: %+v", c)
	}
}

// the full doctor: the "prerequisites" row passes on a PATH-complete
// profile and warns (never fails) when a tool is off PATH.
func TestDoctorPrerequisitesRow(t *testing.T) {
	env := doctorServerConfig(t, "http://aihub.invalid:1", "k", "")
	dir := t.TempDir()
	for _, tool := range []string{"gh", "curl", "python3", "tmux", "git"} {
		if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	full := map[string]string{}
	for k, v := range env {
		full[k] = v
	}
	full["PATH"] = dir
	code, _, doc := doctorStatus(t, full, "--json", "doctor")
	st, detail := doctorFind(t, doc, "prerequisites")
	if st != "pass" {
		t.Fatalf("prerequisites on a PATH-complete profile: %q (code %d, doc %+v)", st, code, doc)
	}
	missingPath := t.TempDir() // none of the five anywhere on this PATH
	bare := map[string]string{}
	for k, v := range env {
		bare[k] = v
	}
	bare["PATH"] = missingPath
	_, _, doc = doctorStatus(t, bare, "--json", "doctor")
	st, detail = doctorFind(t, doc, "prerequisites")
	if st != "warn" {
		t.Fatalf("prerequisites without any setup tool: %q (%q)", st, detail)
	}
	if !strings.Contains(detail, "gh — macOS: brew install gh") {
		t.Fatalf("warn detail must say what to obtain: %q", detail)
	}
}

// TestDoctorReloadRow pins s13 #2: the warn-only `config reload` row
// reports a pending restart-only key and a flag-overridden key, never
// fails, and explains its skips.
func TestDoctorReloadRow(t *testing.T) {
	r := &doctorRun{sockOK: true, status: StatusResponse{Reload: &ReloadStatus{
		Pending: []string{"server"}, Overridden: []string{"claude_bin"},
	}}}
	st, detail := reloadCheck(r)
	if st != "warn" || !strings.Contains(detail, "pending restart: server") || !strings.Contains(detail, "overridden by flag: claude_bin") {
		t.Fatalf("reload row = %q %q, want warn naming both sets", st, detail)
	}
	r.status.Reload = &ReloadStatus{}
	if st, detail := reloadCheck(r); st != "pass" {
		t.Fatalf("a reload with nothing pending = %q %q, want pass", st, detail)
	}
	if st, _ := reloadCheck(&doctorRun{sockOK: false}); st != "skip" {
		t.Fatalf("reload row without a daemon = %q, want skip", st)
	}
	if st, _ := reloadCheck(&doctorRun{sockOK: true}); st != "skip" {
		t.Fatalf("reload row on a daemon without reload state = %q, want skip", st)
	}
}
