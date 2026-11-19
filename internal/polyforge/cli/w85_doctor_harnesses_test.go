package cli

// carries: internal/polyforge/cli/verbs_doctor.go, internal/polyforge/cli/doctor_harnesses.go, internal/polyforge/cli/w85_doctor_harnesses_test.go

// w85_doctor_harnesses_test.go (polyforge#142, 85a2 AC2): `pf --json
// doctor` answers a top-level `cells` object — one entry per Cell of the
// disk set (the default Cell and every named one, an invalid Cell
// included) — each carrying the daemon's structured harnesses facts of
// THAT Cell: the credential set/unset, the model-credential source and
// its kind (operator / shared-default / directory), the harness config
// roots, and the Cell's execution_disabled reason. A Cell with no claude
// step is probed exactly like one with claude steps (the probe is no
// longer claude-steps-conditional), a value never appears, and a daemon
// that does not answer leaves an honest probe_error, never a faked
// fact. Expected shapes come from the ticket's Q2 A ruling and 「要的
// 结果」field list, never from the implementation's own output.

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// w85CellsDoc decodes the cells object of the --json report: the shape
// the account page reads (85a2), not the checks rows.
type w85CellsDoc struct {
	OK    bool                      `json:"ok"`
	Cells map[string]map[string]any `json:"cells"`
}

// w85Cell returns one Cell's entry, failing the test when absent.
func w85Cell(t *testing.T, doc w85CellsDoc, profile string) map[string]any {
	t.Helper()
	cell, ok := doc.Cells[profile]
	if !ok {
		t.Fatalf("cells has no %q entry (got %v)", profile, w85CellKeys(doc))
	}
	return cell
}

func w85CellKeys(doc w85CellsDoc) []string {
	keys := make([]string, 0, len(doc.Cells))
	for k := range doc.Cells {
		keys = append(keys, k)
	}
	return keys
}

// w85CellField returns cell[field] as a map, failing when not one.
func w85CellField(t *testing.T, cell map[string]any, field string) map[string]any {
	t.Helper()
	raw, ok := cell[field].(map[string]any)
	if !ok {
		t.Fatalf("cells entry has no %q object: %v", field, cell)
	}
	return raw
}

func w85Str(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("want a string, got %v (%T)", v, v)
	}
	return s
}

// w85HarnessFacts is one Cell's harnesses object as the fixture's fake
// daemon answers it — the same wire shape the daemon's /status?harnesses=1
// reports, plus one undeclared key carrying a sentinel: the CLI's typed
// relay must drop it (a value-shaped string can never sneak through an
// undeclared field).
func w85HarnessFacts(stepAgent, claudeConfig, source, sourceKind, piCredential, claudeToken, credentialsFile string) string {
	pi := `{"bin":"pi","found":false,"config_root":` + jsonBody(stepAgent) +
		`,"credential":` + jsonBody(piCredential) +
		`,"source":` + jsonBody(source) + `,"source_kind":` + jsonBody(sourceKind) +
		`,"note":"sk-SENTINEL-W85-undeclared"}`
	claude := `{"bin":"claude","found":false,` +
		`"settings_env":{"ANTHROPIC_AUTH_TOKEN":` + jsonBody(claudeToken) + `,"ANTHROPIC_API_KEY":"unset","ANTHROPIC_BASE_URL":"unset","CLAUDE_CONFIG_DIR":"unset"},` +
		`"harness_dirs":{"step_agent":{"path":` + jsonBody(stepAgent) + `,"exists":true},"claude_config":{"path":` + jsonBody(claudeConfig) + `,"exists":true}},` +
		`"config_root":` + jsonBody(claudeConfig) +
		`,"source":` + jsonBody(source) + `,"source_kind":` + jsonBody(sourceKind) +
		`,"credentials_file":` + jsonBody(credentialsFile) + `}`
	return `"harnesses":{"pi":` + pi + `,"claude":` + claude + `}`
}

// w85HostFixture builds the four-Cell machine: the default Cell
// (cli.toml), beta (cli-beta.toml, [model] source "shared-default"),
// gamma (cli-gamma.toml, [model] source an absolute directory), and
// delta (cli-delta.toml, a parse refusal). The fake Host socket answers
// GET /status per profile header with that Cell's harnesses facts and
// execution_disabled, and /aihub/* through to that Cell's own fake aihub
// (one shared server would conflict: Cells are per-identity).
func w85HostFixture(t *testing.T) (env map[string]string, gammaSource string) {
	t.Helper()
	hubs := map[string]*httptest.Server{}
	for _, profile := range []string{"default", "beta", "gamma"} {
		hubs[profile] = newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/v1/projects":
				io.WriteString(w, `{"items":[]}`)
			case strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && strings.HasSuffix(r.URL.Path, "/ready-queue"):
				io.WriteString(w, `{"project":"p","items":[],"running":[],"paused":[]}`)
			case r.URL.Path == "/api/v1/work-items":
				io.WriteString(w, `{"items":[]}`)
			default:
				io.WriteString(w, `{}`)
			}
		})
		t.Cleanup(func() { hubs[profile].Close() })
	}
	proxies := map[string]*httputil.ReverseProxy{}
	for profile, hub := range hubs {
		hubURL, err := url.Parse(hub.URL)
		if err != nil {
			t.Fatalf("hub URL %s: %v", hub.URL, err)
		}
		p := httputil.NewSingleHostReverseProxy(hubURL)
		base := p.Director
		p.Director = func(r *http.Request) {
			base(r)
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/aihub")
			r.URL.RawPath = ""
			r.Header.Del(protocol.ProfileHeader)
		}
		proxies[profile] = p
	}
	wsD, wsB, wsG := t.TempDir(), t.TempDir(), t.TempDir()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	cfgDir := filepath.Join(xdg, "polyforge")
	data := t.TempDir()
	gammaSource = t.TempDir()
	if err := os.WriteFile(filepath.Join(gammaSource, "auth.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{cfgDir, filepath.Join(data, "cells", "default", "step-agent"), wsB, wsG} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wf := t.TempDir()
	writeLane(t, wf, "develop", "steps:\n  - { id: build }\n  - { id: review }\n  - { id: sync }\n  - { id: verification }\n  - { id: publish }\n  - { id: merge }\n")
	bin := t.TempDir()
	for _, tool := range []string{"pf", "git"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	operatorSource := filepath.Join(dir, "operator-agent")
	rootOf := func(profile string) string { return filepath.Join(data, "cells", profile) }
	statusBody := func(profile, source, sourceKind, piCredential, claudeToken, credentialsFile string) string {
		return `{"machine_id":"m-w85-` + profile + `","workflows_dir":` + jsonBody(wf) +
			`,"command_path":` + jsonBody(bin) + `,"step_path":` + jsonBody(bin) +
			`,"env_names":["HOME","PATH"],"jobs":[],"queue":{"pending":0,"running":0,"max_concurrent":1},"reload":{},` +
			`"model_health":{"window":{"duration":"720h0m0s","from":"f","to":"t"},"warnings":[]},` +
			w85HarnessFacts(filepath.Join(rootOf(profile), "step-agent"), filepath.Join(rootOf(profile), "claude-config"), source, sourceKind, piCredential, claudeToken, credentialsFile) + `}`
	}
	bodies := map[string]string{
		// the default Cell's source is the operator's own agent dir
		"default": statusBody("default", operatorSource, "operator", "set", "set", "absent"),
		// a shared-default Cell shares the default Cell's source
		"beta": statusBody("beta", operatorSource, "shared-default", "unset", "unset", "present"),
		// an independent-source Cell names its own directory
		"gamma": statusBody("gamma", gammaSource, "directory", "set", "unset", "absent"),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		profile := r.Header.Get(protocol.ProfileHeader)
		if profile == "" {
			profile = "default"
		}
		body, ok := bodies[profile]
		if !ok {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		profile := r.Header.Get(protocol.ProfileHeader)
		if profile == "" {
			profile = "default"
		}
		if strings.HasPrefix(r.URL.Path, "/aihub/") {
			if p, ok := proxies[profile]; ok {
				p.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
	sock := filepath.Join(dir, "w85.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })

	host := "server = " + jsonBody(hubs["default"].URL) + "\napi_key = \"k\"\ndaemon_socket = " + jsonBody(sock) + "\n\n" +
		"[workspace.\"" + wsD + "\"]\nprojects = [\"dproj\"]\n"
	beta := "server = " + jsonBody(hubs["beta"].URL) + "\napi_key = \"kb\"\n\n[model]\nsource = \"shared-default\"\n\n" +
		"[workspace.\"" + wsB + "\"]\nprojects = [\"bproj\"]\n"
	gamma := "server = " + jsonBody(hubs["gamma"].URL) + "\napi_key = \"kg\"\n\n[model]\nsource = " + jsonBody(gammaSource) + "\n\n" +
		"[workspace.\"" + wsG + "\"]\nprojects = [\"gproj\"]\n"
	delta := "server = \n"
	for name, body := range map[string]string{"cli.toml": host, "cli-beta.toml": beta, "cli-gamma.toml": gamma, "cli-delta.toml": delta} {
		if err := os.WriteFile(filepath.Join(cfgDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env = map[string]string{"XDG_CONFIG_HOME": xdg, "POLYFORGE_DATA_DIR": data}
	return env, gammaSource
}

// TestW85DoctorHarnessesCells (AC2): every Cell of the disk set carries
// structured harnesses facts in --json — including a Cell with no claude
// step and the shared-default and independent-source kinds — with the
// source kind marked right, the execution-disabled reason relayed, an
// invalid Cell honest about its probe error, and no value anywhere.
func TestW85DoctorHarnessesCells(t *testing.T) {
	env, gammaSource := w85HostFixture(t)
	code, stdout, stderr := runPf(t, env, "--json", "doctor")
	// the invalid delta Cell fails its config row, so the run exits 1 —
	// every OTHER failing row would be a fixture defect
	var rows w20Doc
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("doctor --json: %v (%s)", err, stdout)
	}
	for _, c := range rows.Checks {
		if c.Status == "fail" && c.Cell != "delta" {
			t.Fatalf("an unexpected failing row: %+v (stderr %s)", c, stderr)
		}
	}
	if code != 1 {
		t.Fatalf("doctor: exit %d, want 1 (the invalid Cell's config row)", code)
	}
	var doc w85CellsDoc
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("doctor --json: %v (%s)", err, stdout)
	}
	for _, profile := range []string{"default", "beta", "gamma", "delta"} {
		if _, ok := doc.Cells[profile]; !ok {
			t.Fatalf("cells has no %q entry (got %v)", profile, w85CellKeys(doc))
		}
	}
	// the default Cell: the operator's own agent dir is the source
	def := w85Cell(t, doc, "default")
	defPi := w85CellField(t, w85CellField(t, def, "harnesses"), "pi")
	if w85Str(t, defPi["credential"]) != "set" || w85Str(t, defPi["source_kind"]) != "operator" {
		t.Fatalf("default pi facts: %v", defPi)
	}
	if got := w85Str(t, defPi["source"]); !strings.HasSuffix(got, "operator-agent") {
		t.Fatalf("default pi source: %q", got)
	}
	defClaude := w85CellField(t, w85CellField(t, def, "harnesses"), "claude")
	if w85Str(t, defClaude["credentials_file"]) != "absent" {
		t.Fatalf("default claude credentials_file: %v", defClaude["credentials_file"])
	}
	// a shared-default Cell: the kind is marked, no claude step needed
	beta := w85Cell(t, doc, "beta")
	betaPi := w85CellField(t, w85CellField(t, beta, "harnesses"), "pi")
	if w85Str(t, betaPi["credential"]) != "unset" || w85Str(t, betaPi["source_kind"]) != "shared-default" {
		t.Fatalf("beta (shared-default) pi facts: %v", betaPi)
	}
	if _, ok := beta["execution_disabled"]; !ok {
		t.Fatal("the beta entry carries no execution_disabled key")
	}
	betaClaude := w85CellField(t, w85CellField(t, beta, "harnesses"), "claude")
	if w85Str(t, betaClaude["credentials_file"]) != "present" {
		t.Fatalf("beta claude credentials_file: %v", betaClaude["credentials_file"])
	}
	// an independent-source Cell names its own directory
	gamma := w85Cell(t, doc, "gamma")
	gammaPi := w85CellField(t, w85CellField(t, gamma, "harnesses"), "pi")
	if w85Str(t, gammaPi["source"]) != gammaSource || w85Str(t, gammaPi["source_kind"]) != "directory" {
		t.Fatalf("gamma (directory source) pi facts: %v", gammaPi)
	}
	// an invalid Cell: no faked facts, the probe error says why
	delta := w85Cell(t, doc, "delta")
	if _, hasHarnesses := delta["harnesses"]; hasHarnesses {
		t.Fatalf("an invalid Cell answers harnesses facts: %v", delta)
	}
	if probeErr, _ := delta["probe_error"].(string); probeErr == "" {
		t.Fatalf("an invalid Cell carries no probe_error: %v", delta)
	}
	// never a value: the settings_env facts are set/unset only, the
	// undeclared sentinel field is dropped by the typed relay, and no
	// credential-shaped string appears anywhere in the document
	if strings.Contains(stdout, "sk-SENTINEL") {
		t.Fatalf("the report carries the sentinel value: %s", stdout)
	}
	for _, profile := range []string{"default", "beta", "gamma"} {
		claude := w85CellField(t, w85CellField(t, doc.Cells[profile], "harnesses"), "claude")
		envFacts, ok := claude["settings_env"].(map[string]any)
		if !ok {
			t.Fatalf("%s claude settings_env: %v", profile, claude["settings_env"])
		}
		for key, value := range envFacts {
			if v := w85Str(t, value); v != "set" && v != "unset" {
				t.Fatalf("%s settings_env[%s] = %q, want set/unset", profile, key, v)
			}
		}
		pi := w85CellField(t, w85CellField(t, doc.Cells[profile], "harnesses"), "pi")
		if v := w85Str(t, pi["credential"]); v != "set" && v != "unset" {
			t.Fatalf("%s pi credential = %q, want set/unset", profile, v)
		}
	}
}

// TestW85DoctorHarnessesDaemonDown: a daemon that does not answer leaves
// an honest probe_error on every valid Cell's entry — never a faked
// fact — and the report still names every Cell of the disk set.
func TestW85DoctorHarnessesDaemonDown(t *testing.T) {
	env, _ := w85HostFixture(t)
	// point the Host file's socket nowhere: every daemon read fails
	deadSock := filepath.Join(t.TempDir(), "no-daemon.sock")
	env["PF_DAEMON_SOCKET"] = deadSock
	code, stdout, _ := runPf(t, env, "--json", "doctor")
	if code == 0 {
		t.Fatal("a daemon-down doctor run reports ok")
	}
	var doc w85CellsDoc
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("doctor --json: %v (%s)", err, stdout)
	}
	if len(doc.Cells) == 0 {
		t.Fatal("a daemon-down report carries no cells object")
	}
	for _, profile := range []string{"default", "beta", "gamma"} {
		cell := w85Cell(t, doc, profile)
		if _, hasHarnesses := cell["harnesses"]; hasHarnesses {
			t.Fatalf("a daemon-down %s entry answers harnesses facts: %v", profile, cell)
		}
		if probeErr, _ := cell["probe_error"].(string); probeErr == "" {
			t.Fatalf("a daemon-down %s entry carries no probe_error: %v", profile, cell)
		}
	}
}
