// carries: internal/polyforge/cellcfg/*, internal/polyforge/cli/*, internal/polyforge/daemon/*, internal/common/protocol/*, internal/polyforge/runtime/*, internal/polyforge/aihubclient/*
package test

// w348_cell_e2e_acceptance_test.go: end-to-end acceptance for the single-host
// multi-AIHub work item (DaemonHost + ProfileCell), authored against
// artifacts/spec.md BEFORE the implementation lands.
//
// Every scenario drives the project's real faces only: the real `pf` binary,
// one or more real `pf aihub` processes on loopback, a real `pf daemon` on its
// own UDS, the `cli.toml` + `cli-<profile>.toml` files and the real filesystem
// and process table. No implementation internals are asserted.
//
// Baseline posture: the pre-implementation tree serves exactly one Cell from
// `cli.toml`; `cli-<profile>.toml`, `--profile`, `pf profile`, `pf cell` and
// `pf migrate cell-layout` do not exist yet. Every scenario is therefore an
// assertion-level red (an unknown command/flag, a 404 route, a missing field
// or a direct-Hub success that must have failed) and never a build red: this
// file compiles and vets against the tree as it stands.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// ---------------------------------------------------------------------------
// Configuration fixtures: the Host file and one file per named Cell.
// ---------------------------------------------------------------------------

type w348WS struct {
	path     string
	projects []string
}

func w348ProjectList(projects []string) string {
	parts := make([]string, len(projects))
	for i, p := range projects {
		parts[i] = strconv.Quote(p)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func w348ConfigText(server, apiKey, socket, userID string, maxJobs, cellMax int, ws []w348WS, extra string) string {
	var b strings.Builder
	if server != "" {
		fmt.Fprintf(&b, "server = %q\n", server)
	}
	if apiKey != "" {
		fmt.Fprintf(&b, "api_key = %q\n", apiKey)
	}
	if userID != "" {
		fmt.Fprintf(&b, "user_id = %q\n", userID)
	}
	if socket != "" {
		fmt.Fprintf(&b, "daemon_socket = %q\n", socket)
	}
	if maxJobs > 0 || cellMax > 0 {
		b.WriteString("\n[daemon]\n")
		if maxJobs > 0 {
			fmt.Fprintf(&b, "max_concurrent_jobs = %d\n", maxJobs)
		}
		if cellMax > 0 {
			fmt.Fprintf(&b, "cell_max_concurrent_jobs = %d\n", cellMax)
		}
	}
	for _, w := range ws {
		fmt.Fprintf(&b, "\n[workspace.%q]\nprojects = %s\n", w.path, w348ProjectList(w.projects))
	}
	if extra != "" {
		b.WriteString("\n")
		b.WriteString(extra)
		b.WriteString("\n")
	}
	return b.String()
}

func w348WriteFile(t *testing.T, path, text string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---------------------------------------------------------------------------
// The pf CLI as a real process under a curated environment.
// ---------------------------------------------------------------------------

type w348Env struct {
	home    string
	xdg     string
	sock    string
	dataDir string
	extra   []string
}

func w348Run(t *testing.T, e w348Env, dir, stdin string, args ...string) (string, int) {
	t.Helper()
	pfPath := filepath.Join(v1Bins(t), "pf")
	if e.home == "" {
		e.home = t.TempDir()
	}
	if e.xdg == "" {
		e.xdg = filepath.Join(e.home, ".config")
	}
	if err := os.MkdirAll(e.xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + e.home,
		"XDG_CONFIG_HOME=" + e.xdg,
	}
	if e.sock != "" {
		env = append(env, "PF_DAEMON_SOCKET="+e.sock)
	}
	if e.dataDir != "" {
		env = append(env, "POLYFORGE_DATA_DIR="+e.dataDir)
	}
	env = append(env, e.extra...)
	cmd := exec.Command(pfPath, submitAllowNoAcceptance(args)...)
	cmd.Env = env
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
			out.WriteString("\n[run error: " + err.Error() + "]")
		}
	}
	return out.String(), code
}

func w348RunWant(t *testing.T, wantExit int, e w348Env, dir string, args ...string) string {
	t.Helper()
	out, code := w348Run(t, e, dir, "", args...)
	if wantExit < 0 && code != 0 {
		t.Fatalf("pf %v: exit %d, want success (output %q)", args, code, out)
	}
	if wantExit >= 0 && code != wantExit {
		t.Fatalf("pf %v: exit %d, want %d (output %q)", args, code, wantExit, out)
	}
	return out
}

func w348JSON(t *testing.T, what, out string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(w113JSONDocOf(out)), &doc); err != nil {
		t.Fatalf("%s: output is not a JSON document: %v (%q)", what, err, out)
	}
	return doc
}

func w348Field(doc any, path ...string) (any, bool) {
	cur := doc
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// w348FindKey returns the first value stored under key anywhere in doc.
func w348FindKey(doc any, key string) (any, bool) {
	switch x := doc.(type) {
	case map[string]any:
		if v, ok := x[key]; ok {
			return v, true
		}
		for _, child := range x {
			if v, ok := w348FindKey(child, key); ok {
				return v, true
			}
		}
	case []any:
		for _, child := range x {
			if v, ok := w348FindKey(child, key); ok {
				return v, true
			}
		}
	}
	return nil, false
}

func w348Num(t *testing.T, doc any, path ...string) float64 {
	t.Helper()
	v, ok := w348Field(doc, path...)
	if !ok {
		t.Fatalf("document carries no %v (got %v)", path, doc)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("%v is not a number: %v", path, v)
	}
	return f
}

// ---------------------------------------------------------------------------
// A Host machine: one config dir, one data dir, one socket, one daemon.
// ---------------------------------------------------------------------------

type w348Host struct {
	t       *testing.T
	home    string
	xdg     string
	dataDir string
	sock    string
	cmd     *exec.Cmd
	logf    *os.File
	logPath string
	wait    chan error
}

func w348NewHost(t *testing.T) *w348Host {
	t.Helper()
	h := &w348Host{
		t:       t,
		home:    t.TempDir(),
		xdg:     filepath.Join(t.TempDir(), ".config"),
		dataDir: t.TempDir(),
		sock:    filepath.Join(shortTmp(t, "w348sock"), "d.sock"),
		logPath: filepath.Join(t.TempDir(), "daemon.log"),
	}
	if err := os.MkdirAll(filepath.Join(h.xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.stop)
	return h
}

func (h *w348Host) hostConfigPath() string {
	return filepath.Join(h.xdg, "polyforge", "cli.toml")
}

func (h *w348Host) cellConfigPath(profile string) string {
	return filepath.Join(h.xdg, "polyforge", "cli-"+profile+".toml")
}

func (h *w348Host) writeHost(server, apiKey string, maxJobs int, ws []w348WS) string {
	h.t.Helper()
	return w348WriteFile(h.t, h.hostConfigPath(),
		w348ConfigText(server, apiKey, h.sock, "usr_root", maxJobs, 0, ws, ""))
}

func (h *w348Host) writeCell(profile, server, apiKey string, cellMax int, ws []w348WS) string {
	h.t.Helper()
	return w348WriteFile(h.t, h.cellConfigPath(profile),
		w348ConfigText(server, apiKey, "", "usr_root", 0, cellMax, ws, ""))
}

func (h *w348Host) env() w348Env {
	return w348Env{home: h.home, xdg: h.xdg, sock: h.sock, dataDir: h.dataDir}
}

func (h *w348Host) envWith(extra ...string) w348Env {
	e := h.env()
	e.extra = append(append([]string{}, e.extra...), extra...)
	return e
}

func (h *w348Host) pf(dir string, args ...string) (string, int) {
	return w348Run(h.t, h.env(), dir, "", args...)
}

func (h *w348Host) pfEnv(extra []string, dir string, args ...string) (string, int) {
	return w348Run(h.t, h.envWith(extra...), dir, "", args...)
}

func (h *w348Host) logTail() string {
	raw, err := os.ReadFile(h.logPath)
	if err != nil {
		return fmt.Sprintf("(unreadable log: %v)", err)
	}
	if len(raw) > 4096 {
		raw = raw[len(raw)-4096:]
	}
	return string(raw)
}

// boot starts the daemon and waits for EITHER its UDS face to answer
// (served=true) or the process to exit (served=false).
func (h *w348Host) boot(bound time.Duration) (served bool, output string, code int) {
	h.t.Helper()
	logf, err := os.Create(h.logPath)
	if err != nil {
		h.t.Fatal(err)
	}
	h.logf = logf
	binDir := v1Bins(h.t)
	// Registered AFTER v1Bins' process sweep, so LIFO teardown stops the
	// daemon before the sweep inspects the bin dir.
	h.t.Cleanup(h.stop)
	args := []string{
		"daemon",
		"--socket", h.sock,
		"--data-dir", h.dataDir,
		"--workflows-dir", copyWorkflowsTrees(h.t, nil, filepath.Join(v1Root(h.t), "workflows")),
		"--pi-bin", filepath.Join(binDir, "fakepi"),
		"--claude-bin", "/nonexistent/claude-test-guard",
	}
	cmd := exec.Command(filepath.Join(binDir, "pf"), args...)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + h.home,
		"XDG_CONFIG_HOME=" + h.xdg,
		fakepiTestPIDEnv + "=" + os.Getenv(fakepiTestPIDEnv),
		"GIT_AUTHOR_NAME=pf-e2e",
		"GIT_AUTHOR_EMAIL=pf-e2e@invalid",
		"GIT_COMMITTER_NAME=pf-e2e",
		"GIT_COMMITTER_EMAIL=pf-e2e@invalid",
	}
	cmd.Dir = h.dataDir
	cmd.Stdout, cmd.Stderr = logf, logf
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	h.cmd = cmd
	h.wait = make(chan error, 1)
	go func() { h.wait <- cmd.Wait() }()
	answered, exited, werr := awaitAnsweredFace(h.sock, time.Now().Add(bound), h.wait)
	switch {
	case answered:
		return true, h.logTail(), 0
	case exited:
		h.cmd = nil
		c := -1
		if ee, ok := werr.(*exec.ExitError); ok {
			c = ee.ExitCode()
		}
		return false, h.logTail(), c
	}
	h.stop()
	h.t.Fatalf("daemon neither served nor exited within %v (log tail: %s)", bound, h.logTail())
	return false, "", 0
}

func (h *w348Host) start(bound time.Duration) {
	h.t.Helper()
	served, output, code := h.boot(bound)
	if !served {
		h.t.Fatalf("daemon exited during startup: exit %d (log tail: %s)", code, output)
	}
}

func (h *w348Host) stop() {
	if h.logf != nil {
		_ = h.logf.Close()
		h.logf = nil
	}
	if h.cmd == nil || h.cmd.Process == nil {
		return
	}
	_ = h.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-h.wait:
	case <-time.After(10 * time.Second):
		killGroup(h.cmd)
		<-h.wait
	}
	killGroup(h.cmd)
	h.cmd = nil
}

func (h *w348Host) alive() bool {
	if h.cmd == nil || h.cmd.Process == nil {
		return false
	}
	return h.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// w348UDS answers one request on the daemon's Unix socket.
func w348UDS(t *testing.T, sock, method, path string, headers map[string]string, body string) (int, string) {
	t.Helper()
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return net.Dial("unix", sock)
			},
		},
	}
	req, err := http.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// w348DaemonPIDs lists the live pf daemon processes started with this
// Host's data dir: a process whose argv carries the exact argument `daemon`
// and the data dir as its own argument (`--data-dir <dir>`, or the `=` form).
// A substring match also counted other processes naming the data dir — the
// daemon's own `docker ps --filter label=pf.verify.daemon=<data dir>` while it
// sweeps leftover verification containers, or a supervising
// `sh -c` loop — and made the no-restart check flaky.
func w348DaemonPIDs(t *testing.T, dataDir string) []int {
	t.Helper()
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil {
			continue
		}
		if w348IsDaemonArgv(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"), dataDir) {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out
}

// w348IsDaemonArgv reports whether argv is a pf daemon started with dataDir.
func w348IsDaemonArgv(argv []string, dataDir string) bool {
	isDaemon, hasDir := false, false
	for i, a := range argv {
		switch {
		case a == "daemon":
			isDaemon = true
		case (a == "--data-dir" || a == "-data-dir") && i+1 < len(argv) && argv[i+1] == dataDir:
			hasDir = true
		case a == "--data-dir="+dataDir || a == "-data-dir="+dataDir:
			hasDir = true
		}
	}
	return isDaemon && hasDir
}

// ---------------------------------------------------------------------------
// Old (pre-Cell) layout fixtures for the migration scenarios.
// ---------------------------------------------------------------------------

const w348OldMachineKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func w348OldLayout(t *testing.T, dataDir string) {
	t.Helper()
	for _, d := range []string{"events", "git", "bar-runs", "step-agent", "step-agent-sessions", "claude-config", "summarize-cwd"} {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	w348WriteFile(t, filepath.Join(dataDir, "daemon-id"), "m-w348-old\n")
	w348WriteFile(t, filepath.Join(dataDir, "machine-key"), w348OldMachineKey+"\n")
	w348WriteFile(t, filepath.Join(dataDir, "runtime-state.json"), "{}\n")
	w348WriteFile(t, filepath.Join(dataDir, "events", "0000000001.json"), `{"kind":"submitted"}`+"\n")
	w348WriteFile(t, filepath.Join(dataDir, "git", "keep"), "old git state\n")
	w348WriteFile(t, filepath.Join(dataDir, "step-agent", "AGENTS.md"), "old harness asset\n")
	w348WriteFile(t, filepath.Join(dataDir, "claude-config", "settings.json"), `{"env":{}}`+"\n")
	w348WriteFile(t, filepath.Join(dataDir, "summarize-cwd", ".keep"), "")
	w348WriteFile(t, filepath.Join(dataDir, "chats.json"), `{"version":1,"chats":{"pi:session-w348":"chat_w348"}}`+"\n")
	w348WriteFile(t, filepath.Join(dataDir, "chat-subs.json"),
		`{"version":1,"chats":{"chat-w348":{"subs":{"wi_old":{"wi":"wi_old","cursor":3}}}}}`+"\n")
}

func w348TreeHashes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		rel, _ := filepath.Rel(root, path)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func w348SameTree(t *testing.T, want, got map[string]string, what string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s changed the file set: %d files before, %d after", what, len(want), len(got))
	}
	for name, sum := range want {
		if got[name] != sum {
			t.Fatalf("%s changed %s (want %s, got %s)", what, name, sum, got[name])
		}
	}
}

func w348ReadString(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func w348Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// w348HubProjects answers the project names one Hub currently records: the
// server-side evidence for "no request reached this Hub".
func w348HubProjects(t *testing.T, inst *v1Install) map[string]bool {
	t.Helper()
	raw, err := inst.client.ListProjects()
	if err != nil {
		t.Fatalf("list projects on the Hub: %v", err)
	}
	var doc struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("list projects answer is unreadable: %v (%s)", err, raw)
	}
	out := map[string]bool{}
	for _, it := range doc.Items {
		out[it.Name] = true
	}
	return out
}

// w348ReportsCellCeiling reports whether the document carries the optional
// Cell ceiling as the config key or as a per-Cell effective value.
func w348ReportsCellCeiling(doc any, want float64) bool {
	if v, ok := w348FindKey(doc, "cell_max_concurrent_jobs"); ok && v == want {
		return true
	}
	var walk func(v any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			name, _ := x["profile"].(string)
			if name == "" {
				name, _ = x["name"].(string)
			}
			if name == "alpha" {
				for _, key := range []string{"max_concurrent", "effective", "effective_max_concurrent_jobs"} {
					if n, ok := x[key].(float64); ok && n == want {
						return true
					}
				}
			}
			for _, child := range x {
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range x {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(doc)
}

// ---------------------------------------------------------------------------
// Scenario 1 — one Host process and one socket serve two Cells.
// ---------------------------------------------------------------------------

func TestW348OneHostOneSocketServesTwoCells(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)

	if pids := w348DaemonPIDs(t, h.dataDir); len(pids) != 1 {
		t.Fatalf("one Host must be one process; found %d daemons for %s", len(pids), h.dataDir)
	}
	status, body := w348UDS(t, h.sock, "GET", "/profiles", map[string]string{"X-PF-Profile": "alpha"}, "")
	if status != 200 {
		t.Fatalf("GET /profiles: status %d (%s) — the one Host must answer its Cells' registry", status, body)
	}
	low := strings.ToLower(body)
	for _, want := range []string{"default", "alpha"} {
		if !strings.Contains(low, want) {
			t.Fatalf("GET /profiles does not name Cell %q: %s", want, body)
		}
	}
	// Both Cells answer over the same socket (the one UDS face).
	out, code := h.pf("", "--profile", "alpha", "--json", "profile")
	if code != 0 {
		t.Fatalf("pf --profile alpha --json profile: exit %d (%q)", code, out)
	}
	if !strings.Contains(strings.ToLower(out), "alpha") {
		t.Fatalf("the resolver did not answer alpha over the same Host: %q", out)
	}
}

// ---------------------------------------------------------------------------
// Scenario 2 — every business call egresses from its Cell's Hub.
// ---------------------------------------------------------------------------

func TestW348BusinessCallsEgressFromTheirCellHub(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	if _, err := alpha.client.CreateProject(aihubclient.ProjectCreateRequest{Name: "alpha-only"}); err != nil {
		t.Fatalf("create alpha-only project on alpha's Hub: %v", err)
	}
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"alpha-only"}}})
	h.start(30 * time.Second)

	defList := w348RunWant(t, -1, h.env(), "", "--json", "projects", "list")
	if strings.Contains(defList, "alpha-only") {
		t.Fatalf("the default Cell saw alpha's Hub project: %q", defList)
	}
	alphaList, code := h.pf("", "--profile", "alpha", "--json", "projects", "list")
	if code != 0 {
		t.Fatalf("pf --profile alpha projects list: exit %d (%q)", code, alphaList)
	}
	if !strings.Contains(alphaList, "alpha-only") {
		t.Fatalf("alpha's business call did not egress through alpha's Hub: %q", alphaList)
	}
	get, code := h.pf("", "--profile", "alpha", "--json", "projects", "get", "alpha-only")
	if code != 0 || !strings.Contains(get, "alpha-only") {
		t.Fatalf("alpha's management call did not reach alpha's Hub: exit %d (%q)", code, get)
	}
}

// ---------------------------------------------------------------------------
// Scenario 3 — daemon down: no direct Hub fallback; local repair still works.
// ---------------------------------------------------------------------------

func TestW348DaemonDownNoDirectFallbackLocalRepairWorks(t *testing.T) {
	def := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	h.stop()

	out, code := h.pf("", "--json", "projects", "list")
	if code == 0 {
		t.Fatalf("a business command succeeded with the daemon down — it must fail explicitly, never dial a Hub directly: %q", out)
	}
	direct := h.envWith("PF_SERVER="+def.url, "PF_API_KEY="+def.adminKey)
	out, code = w348Run(t, direct, "", "", "--json", "projects", "list")
	if code == 0 {
		t.Fatalf("PF_SERVER/PF_API_KEY bypassed the stopped daemon (direct fallback): %q", out)
	}
	if got := w348RunWant(t, 0, h.env(), "", "config", "validate"); strings.Contains(strings.ToLower(got), "unknown") {
		t.Fatalf("local config validate must still work with the daemon down: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Scenario 4 — --profile > PF_PROFILE > cwd > default.
// ---------------------------------------------------------------------------

func TestW348ProfileResolverOrder(t *testing.T) {
	wsDefault, wsAlpha, neutral := t.TempDir(), t.TempDir(), t.TempDir()
	h := w348NewHost(t)
	h.writeHost("http://127.0.0.1:1", "w348-default-key", 2, []w348WS{{path: wsDefault, projects: []string{"e2e"}}})
	h.writeCell("alpha", "http://127.0.0.1:2", "w348-alpha-key", 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	h.start(30 * time.Second)

	profileOf := func(e w348Env, dir, flagProfile string) string {
		t.Helper()
		args := []string{"--json"}
		if flagProfile != "" {
			args = append(args, "--profile", flagProfile)
		}
		args = append(args, "profile")
		out, code := w348Run(t, e, dir, "", args...)
		if code != 0 {
			t.Fatalf("pf %v: exit %d (%q)", args, code, out)
		}
		p, _ := w348Field(w348JSON(t, "pf profile", out), "profile")
		s, _ := p.(string)
		if s == "" {
			t.Fatalf("pf profile carries no profile name: %q", out)
		}
		return s
	}
	if got := profileOf(h.envWith("PF_PROFILE=alpha"), wsAlpha, "default"); got != "default" {
		t.Fatalf("--profile default must win over PF_PROFILE=alpha and cwd=%s; got %q", wsAlpha, got)
	}
	if got := profileOf(h.envWith("PF_PROFILE=alpha"), wsDefault, ""); got != "alpha" {
		t.Fatalf("PF_PROFILE=alpha must win over cwd=%s; got %q", wsDefault, got)
	}
	if got := profileOf(h.env(), wsAlpha, ""); got != "alpha" {
		t.Fatalf("cwd=%s maps alpha, got %q", wsAlpha, got)
	}
	if got := profileOf(h.env(), neutral, ""); got != "default" {
		t.Fatalf("an unmatched cwd must resolve to default, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Scenario 5 — an unknown profile fails explicitly and sends no request.
// ---------------------------------------------------------------------------

func TestW348UnknownProfileFailsNamingItsFile(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)

	out, code := h.pf("", "--profile", "beta", "--json", "projects", "list")
	if code == 0 {
		t.Fatalf("--profile beta succeeded; an unknown profile must fail explicitly: %q", out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "beta") || !strings.Contains(low, "cli-beta.toml") {
		t.Fatalf("the refusal must name beta and the expected cli-beta.toml; got %q", out)
	}
	out, code = h.pf("", "--profile", "beta", "projects", "create", "--name", "ghost")
	if code == 0 {
		t.Fatalf("a side-effecting call for an unknown profile succeeded: %q", out)
	}
	for name, inst := range map[string]*v1Install{"default": def, "alpha": alpha} {
		if w348HubProjects(t, inst)["ghost"] {
			t.Fatalf("a request reached %s's Hub for the unknown profile (ghost exists)", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 6 — Host-scope commands ignore profile routing; shutdown is Host-level.
// ---------------------------------------------------------------------------

func TestW348HostScopeIgnoresProfileAndShutdownIsHostLevel(t *testing.T) {
	wsAlpha := t.TempDir()
	h := w348NewHost(t)
	h.writeHost("http://127.0.0.1:1", "w348-default-key", 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", "http://127.0.0.1:2", "w348-alpha-key", 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	h.start(30 * time.Second)

	out, code := h.pfEnv([]string{"PF_PROFILE=alpha"}, wsAlpha, "--json", "ops", "status")
	if code != 0 {
		t.Fatalf("PF_PROFILE=alpha pf ops status: exit %d (%q)", code, out)
	}
	if got := w348Num(t, w348JSON(t, "ops status", out), "queue", "max_concurrent"); got != 2 {
		t.Fatalf("pf ops status under PF_PROFILE=alpha reports %v, want the Host aggregate ceiling 2", got)
	}
	w348RunWant(t, 0, h.envWith("PF_PROFILE=alpha"), wsAlpha, "ops", "max", "3")
	out = w348RunWant(t, -1, h.env(), "", "--json", "ops", "status")
	if got := w348Num(t, w348JSON(t, "ops status", out), "queue", "max_concurrent"); got != 3 {
		t.Fatalf("PF_PROFILE=alpha changed only a Cell: the Host ceiling is %v, want 3", got)
	}
	if out, code = h.pf("", "--profile", "alpha", "--json", "ops", "status"); code != 0 {
		t.Fatalf("pf --profile alpha ops status: exit %d (%q)", code, out)
	}
	out, code = h.pf("", "--profile", "alpha", "ops", "shutdown")
	if code == 0 {
		t.Fatalf("pf --profile alpha ops shutdown succeeded; shutdown is Host-level only: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "host") {
		t.Fatalf("the refusal must say shutdown is Host-level; got %q", out)
	}
	if !h.alive() {
		t.Fatalf("a refused scoped shutdown stopped the Host")
	}
	w348RunWant(t, 0, h.env(), "", "ops", "shutdown")
	deadline := time.Now().Add(15 * time.Second)
	for h.alive() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if h.alive() {
		t.Fatalf("plain pf ops shutdown did not stop the Host")
	}
}

// ---------------------------------------------------------------------------
// Scenario 7 — per-command connection overrides are refused, not ignored.
// ---------------------------------------------------------------------------

func TestW348BusinessConnectionOverridesAreRefused(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)

	out, code := h.pf("", "projects", "create", "--name", "ghost-a", "--server", alpha.url, "--api-key", alpha.adminKey)
	if code == 0 {
		t.Fatalf("--server/--api-key on a business verb were accepted: %q", out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "server") || (!strings.Contains(low, "config") && !strings.Contains(low, "cell")) {
		t.Fatalf("the refusal must name the offending option and the Cell configuration route; got %q", out)
	}
	out, code = h.pfEnv([]string{"PF_SERVER=" + alpha.url, "PF_API_KEY=" + alpha.adminKey}, "",
		"projects", "create", "--name", "ghost-b")
	if code == 0 {
		t.Fatalf("PF_SERVER/PF_API_KEY routed a business command: %q", out)
	}
	low = strings.ToLower(out)
	if !strings.Contains(low, "pf_server") && !strings.Contains(low, "server") {
		t.Fatalf("the environment-override refusal must name PF_SERVER; got %q", out)
	}
	for _, ghost := range []string{"ghost-a", "ghost-b"} {
		if w348HubProjects(t, alpha)[ghost] {
			t.Fatalf("the refused override still sent %s to alpha's Hub", ghost)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 8 — a step agent cannot drive Cell lifecycle or configuration writes.
// ---------------------------------------------------------------------------

func TestW348StepAgentCannotLifecycleOrConfigWrite(t *testing.T) {
	h := w348NewHost(t)
	h.writeHost("http://127.0.0.1:1", "w348-default-key", 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	stepEnv := []string{"PF_WI_ID=wi_w348_step", "PF_DAEMON_ID=m-w348-step"}

	out, code := h.pfEnv(stepEnv, "", "cell", "add", "alpha", "--server", "http://127.0.0.1:2", "--api-key", "k2")
	if code == 0 || !strings.Contains(out, "DAEMON-403") {
		t.Fatalf("a step agent must be refused Cell lifecycle with DAEMON-403; exit %d (%q)", code, out)
	}
	out, code = h.pfEnv(stepEnv, "", "config", "set", "max_concurrent_jobs", "9", "--host")
	if code == 0 || !strings.Contains(out, "DAEMON-403") {
		t.Fatalf("a step agent must be refused a Host configuration write with DAEMON-403; exit %d (%q)", code, out)
	}
	out = w348RunWant(t, -1, h.env(), "", "--json", "config", "list")
	items, _ := w348JSON(t, "config list", out)["items"].([]any)
	found := false
	for _, it := range items {
		m, _ := it.(map[string]any)
		if k, _ := m["key"].(string); k != "max_concurrent_jobs" {
			continue
		}
		found = true
		if v, _ := m["value"].(float64); v != 2 {
			t.Fatalf("the refused step-agent write changed max_concurrent_jobs to %v, want 2", m["value"])
		}
	}
	if !found {
		t.Fatalf("config list carries no max_concurrent_jobs item: %q", out)
	}
}

// ---------------------------------------------------------------------------
// Scenario 9 — a Cell is added at runtime by writing its file and reloading.
// ---------------------------------------------------------------------------

func TestW348CellAddedAtRuntimeByReload(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	if _, err := alpha.client.CreateProject(aihubclient.ProjectCreateRequest{Name: "alpha-only"}); err != nil {
		t.Fatalf("create alpha-only project: %v", err)
	}
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	if out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list"); code == 0 {
		t.Fatalf("alpha answered before it was configured: %q", out)
	}
	// The operator adds the Cell by writing its file; the Host needs the
	// explicit reload (no filesystem watching in phase 1).
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"alpha-only"}}})
	w348RunWant(t, 0, h.env(), "", "--profile", "alpha", "config", "reload")
	out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list")
	if code != 0 || !strings.Contains(out, "alpha-only") {
		t.Fatalf("the added Cell was not served after reload: exit %d (%q)", code, out)
	}
	if status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, ""); status != 200 || !strings.Contains(strings.ToLower(body), "alpha") {
		t.Fatalf("GET /profiles does not carry the added Cell: %d (%s)", status, body)
	}
	// No restart happened: the original Host process is still the one serving.
	if pids := w348DaemonPIDs(t, h.dataDir); len(pids) != 1 {
		t.Fatalf("adding a Cell restarted the Host; found %d daemons", len(pids))
	}
}

// ---------------------------------------------------------------------------
// Scenario 10 — removal retains state, and a different identity cannot take
// the same name back.
// ---------------------------------------------------------------------------

func TestW348RemovedCellKeepsStateAndRestoreIdentityIsChecked(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)

	w348RunWant(t, 0, h.env(), "", "cell", "remove", "alpha")
	if !w348Exists(filepath.Join(h.dataDir, "cells", "alpha")) {
		t.Fatalf("removing alpha destroyed its retained state dir cells/alpha")
	}
	if out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list"); code == 0 {
		t.Fatalf("the removed Cell still serves: %q", out)
	}
	// Restoring the same name with a different AIHub is refused with the
	// "create a new profile" route.
	h.writeCell("alpha", def.url, def.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	reload, _ := h.pf("", "--profile", "alpha", "config", "reload")
	restore, code := h.pf("", "--profile", "alpha", "--json", "projects", "list")
	if code == 0 {
		t.Fatalf("alpha was restored under a different AIHub identity: %q", restore)
	}
	if combined := strings.ToLower(reload + restore); !strings.Contains(combined, "new profile") {
		t.Fatalf("the identity-mismatch refusal must point at a new profile name; got %q", reload+restore)
	}
}

// ---------------------------------------------------------------------------
// Scenario 11 — an invalid named Cell is isolated; an invalid Host blocks start.
// ---------------------------------------------------------------------------

func TestW348InvalidNamedCellIsIsolatedButInvalidHostBlocks(t *testing.T) {
	def := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteFile(t, h.cellConfigPath("alpha"), "server = \n")
	h.start(30 * time.Second)
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles: status %d (%s) — the valid default Cell must still serve", status, body)
	}
	low := strings.ToLower(body)
	if !strings.Contains(low, "alpha") || (!strings.Contains(low, "invalid") && !strings.Contains(low, "error")) {
		t.Fatalf("the syntax-invalid named Cell must be reported invalid and visible: %s", body)
	}
	if out := w348RunWant(t, -1, h.env(), "", "--json", "projects", "list"); !strings.Contains(out, "e2e") {
		t.Fatalf("the valid default Cell stopped serving beside an invalid named Cell: %q", out)
	}
	// An invalid Host/default file still blocks startup, naming the file.
	h2 := w348NewHost(t)
	w348WriteFile(t, h2.hostConfigPath(), "server = \n")
	served, output, code := h2.boot(20 * time.Second)
	if served {
		h2.stop()
		t.Fatalf("the Host started on an invalid cli.toml")
	}
	if code == 0 || !strings.Contains(output, "cli.toml") {
		t.Fatalf("an invalid Host file must refuse to start and name cli.toml; exit %d (log %q)", code, output)
	}
}

// ---------------------------------------------------------------------------
// Scenario 12 — conflicting named Cells are both invalid, no load-order winner.
// ---------------------------------------------------------------------------

func TestW348ConflictingCellsAreBothInvalid(t *testing.T) {
	def := newV1Install(t)
	shared := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", shared.url, shared.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("beta", shared.url, shared.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	if out := w348RunWant(t, -1, h.env(), "", "--json", "projects", "list"); !strings.Contains(out, "e2e") {
		t.Fatalf("the default Cell stopped serving beside two conflicting named Cells: %q", out)
	}
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles: status %d (%s)", status, body)
	}
	low := strings.ToLower(body)
	for _, want := range []string{"alpha", "beta", "invalid"} {
		if !strings.Contains(low, want) {
			t.Fatalf("the conflict report does not carry %q (both conflicting parties must be invalid): %s", want, body)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 13 — one Hub going away degrades only its Cell.
// ---------------------------------------------------------------------------

func TestW348HubFailureDegradesOnlyItsCell(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	if out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list"); code != 0 {
		t.Fatalf("alpha did not serve while its Hub was reachable: %q", out)
	}
	alpha.stopAihub()

	out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list")
	if code == 0 {
		t.Fatalf("alpha's query succeeded after its Hub went away: %q", out)
	}
	low := strings.ToLower(out)
	namesCell := strings.Contains(low, "alpha") || strings.Contains(out, alpha.url)
	if !namesCell || (!strings.Contains(low, "error") && !strings.Contains(low, "connect") && !strings.Contains(low, "refus") && !strings.Contains(low, "unreach")) {
		t.Fatalf("alpha's failure must name the Cell and report the error explicitly, never as an empty result: %q", out)
	}
	if out := w348RunWant(t, -1, h.env(), "", "--json", "projects", "list"); !strings.Contains(out, "e2e") {
		t.Fatalf("the default Cell stopped serving because alpha's Hub went away: %q", out)
	}
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles: status %d (%s)", status, body)
	}
	if low := strings.ToLower(body); !strings.Contains(low, "alpha") || (!strings.Contains(low, "degrad") && !strings.Contains(low, "error")) {
		t.Fatalf("the degraded Cell must be visible on /profiles: %s", body)
	}
}

// ---------------------------------------------------------------------------
// Scenario 14 — status reports the two-level counts and the background bound.
// ---------------------------------------------------------------------------

func TestW348StatusReportsTwoLevelCountsAndBackgroundBound(t *testing.T) {
	h := w348NewHost(t)
	h.writeHost("http://127.0.0.1:1", "w348-default-key", 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", "http://127.0.0.1:2", "w348-alpha-key", 1, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)

	out := w348RunWant(t, -1, h.env(), "", "--json", "ops", "status")
	doc := w348JSON(t, "ops status", out)
	if got := w348Num(t, doc, "queue", "max_concurrent"); got != 2 {
		t.Fatalf("the Host ceiling must be 2, got %v", got)
	}
	bg, ok := w348FindKey(doc, "background_model_calls")
	if !ok {
		t.Fatalf("ops status carries no background_model_calls bound: %q", out)
	}
	if bound, ok := w348Field(bg, "bound"); !ok || bound != float64(2) {
		t.Fatalf("background_model_calls.bound must be 2, got %v (%q)", bg, out)
	}
	if !strings.Contains(strings.ToLower(out), "alpha") {
		t.Fatalf("ops status has no per-Cell block naming alpha: %q", out)
	}
	if !w348ReportsCellCeiling(doc, 1) {
		t.Fatalf("ops status must report alpha's optional Cell ceiling 1 (persisted/effective), got %q", out)
	}
}

// ---------------------------------------------------------------------------
// Scenario 15 — a pre-Cell data directory is refused, never auto-migrated.
// ---------------------------------------------------------------------------

func TestW348OldLayoutIsRefusedAtStartup(t *testing.T) {
	h := w348NewHost(t)
	h.writeHost("http://127.0.0.1:1", "w348-default-key", 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348OldLayout(t, h.dataDir)
	served, output, code := h.boot(20 * time.Second)
	if served {
		h.stop()
		t.Fatalf("the new runtime started on a pre-Cell data directory (no layout-version); it must refuse and name `pf migrate cell-layout`")
	}
	if code == 0 || !strings.Contains(output, "pf migrate cell-layout") {
		t.Fatalf("the old-layout refusal must name `pf migrate cell-layout`; exit %d (log %q)", code, output)
	}
	if w348Exists(filepath.Join(h.dataDir, "cells", "default")) {
		t.Fatalf("the refused startup wrote Cell state under cells/default")
	}
}

// ---------------------------------------------------------------------------
// Scenario 16 — plan writes nothing; apply migrates and preserves identity
// (the retired chat-subs.json is no longer annotated,).
// ---------------------------------------------------------------------------

func TestW348MigrationPlanAndApplyPreserveIdentity(t *testing.T) {
	h := w348NewHost(t)
	w348OldLayout(t, h.dataDir)
	before := w348TreeHashes(t, h.dataDir)
	chatsBefore := w348ReadString(t, filepath.Join(h.dataDir, "chats.json"))

	plan := w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout")
	if !strings.Contains(strings.ToLower(plan), "default") {
		t.Fatalf("the plan must list the items that would move into cells/default: %q", plan)
	}
	w348SameTree(t, before, w348TreeHashes(t, h.dataDir), "a plan-only migration")
	if w348Exists(filepath.Join(h.dataDir, "layout-version")) {
		t.Fatalf("a plan-only migration wrote layout-version")
	}

	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--apply")
	if !w348Exists(filepath.Join(h.dataDir, "cells", "default")) || !w348Exists(filepath.Join(h.dataDir, "layout-version")) {
		t.Fatalf("--apply did not publish cells/default behind layout-version")
	}
	if got := w348ReadString(t, filepath.Join(h.dataDir, "cells", "default", "daemon-id")); strings.TrimSpace(got) != "m-w348-old" {
		t.Fatalf("the machine id was not preserved: %q", got)
	}
	if got := w348ReadString(t, filepath.Join(h.dataDir, "cells", "default", "machine-key")); strings.TrimSpace(got) != w348OldMachineKey {
		t.Fatalf("the machine key was not preserved: %q", got)
	}
	// the retired chat state is dead weight the migration leaves alone
	// (`pf doctor --prune-chat-state` removes it)
	if got := w348ReadString(t, filepath.Join(h.dataDir, "chat-subs.json")); strings.Contains(got, "profile") {
		t.Fatalf("the migration rewrote the retired chat-subs.json: %s", got)
	}
	if got := w348ReadString(t, filepath.Join(h.dataDir, "chats.json")); got != chatsBefore {
		t.Fatalf("the migration changed chats.json: %q -> %q", chatsBefore, got)
	}

	// A re-run converges: the moved identity is unchanged (the manifest may
	// legitimately be re-written by a re-run).
	applied := w348TreeHashes(t, h.dataDir)
	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--apply")
	after := w348TreeHashes(t, h.dataDir)
	for _, name := range []string{"cells/default/daemon-id", "cells/default/machine-key", "chat-subs.json"} {
		if after[name] != applied[name] {
			t.Fatalf("a repeated --apply changed %s", name)
		}
	}
	// Nothing left to move is a clean plan that changes no byte.
	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout")
	w348SameTree(t, after, w348TreeHashes(t, h.dataDir), "a nothing-to-move plan")
}

// ---------------------------------------------------------------------------
// Scenario 17 — rollback is allowed inside the pre-new-write window.
// ---------------------------------------------------------------------------

func TestW348MigrationRollbackInsideWindow(t *testing.T) {
	h := w348NewHost(t)
	w348OldLayout(t, h.dataDir)
	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--apply")
	if !w348Exists(filepath.Join(h.dataDir, "layout-version")) {
		t.Fatalf("--apply published no layout-version")
	}
	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--rollback")
	if w348Exists(filepath.Join(h.dataDir, "layout-version")) {
		t.Fatalf("rollback left the migrated layout version in place")
	}
	if got := strings.TrimSpace(w348ReadString(t, filepath.Join(h.dataDir, "daemon-id"))); got != "m-w348-old" {
		t.Fatalf("rollback restored a different machine id: %q", got)
	}
	if got := strings.TrimSpace(w348ReadString(t, filepath.Join(h.dataDir, "machine-key"))); got != w348OldMachineKey {
		t.Fatalf("rollback restored a different machine key: %q", got)
	}
}

// ===========================================================================
// Appended scenarios — owner-approved append-only amendment (review s20#1 /
// s20#2). Every line above this marker is byte-identical to the original
// authoring commit; only additions follow.
// ===========================================================================

// w348ProfilesRows indexes GET /profiles by profile name.
func w348ProfilesRows(t *testing.T, body string) map[string]map[string]any {
	t.Helper()
	var doc struct {
		Profiles []map[string]any `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("GET /profiles is not a profile document: %v (%q)", err, body)
	}
	out := map[string]map[string]any{}
	for _, row := range doc.Profiles {
		if name, ok := row["profile"].(string); ok {
			out[name] = row
		}
	}
	return out
}

func w348ProfileRow(t *testing.T, body, profile string) map[string]any {
	t.Helper()
	rows := w348ProfilesRows(t, body)
	row, ok := rows[profile]
	if !ok {
		t.Fatalf("GET /profiles carries no row for %q: %s", profile, body)
	}
	return row
}

// w348CellMachineID reads the per-Cell machine id the step-agent ownership
// check keys on.
func w348CellMachineID(t *testing.T, h *w348Host, profile string) string {
	t.Helper()
	return strings.TrimSpace(w348ReadString(t, filepath.Join(h.dataDir, "cells", profile, "daemon-id")))
}

// w348StatusDoc reads the Host aggregate status once.
func w348StatusDoc(t *testing.T, h *w348Host) map[string]any {
	t.Helper()
	status, body := w348UDS(t, h.sock, "GET", "/status", nil, "")
	if status != 200 {
		t.Fatalf("GET /status: %d (%s)", status, body)
	}
	return w348JSON(t, "status", body)
}

func w348Jobs(doc map[string]any) []map[string]any {
	raw, _ := doc["jobs"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// w348WaitJobStatus polls GET /status until the job of (profile, wi) carries
// want (bounded; the daemon's dispatch is asynchronous).
func w348WaitJobStatus(t *testing.T, h *w348Host, profile, wi, want string, bound time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(bound)
	last := ""
	for time.Now().Before(deadline) {
		doc := w348StatusDoc(t, h)
		for _, j := range w348Jobs(doc) {
			p, _ := j["profile"].(string)
			id, _ := j["work_item_id"].(string)
			if p != profile || id != wi {
				continue
			}
			st, _ := j["status"].(string)
			last = st
			if st == want || (want == "parked" && st == "needs_input") {
				return j
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %s/%s never reached %q within %v (last %q, log tail: %s)", profile, wi, want, bound, last, h.logTail())
	return nil
}

// w348WaitTerminal waits (bounded) until the work item wi, run by the
// Host's Cell connected to inst, reached its terminal close: wrapped or
// failed at inst's hub. The hub is the authority, not GET /status: a job
// leaves /status the moment its close settles, so a poll that misses the
// short sealing window (a loaded host polls sparsely) would wait for a
// state that never shows again.
func w348WaitTerminal(t *testing.T, h *w348Host, inst *v1Install, wi string, bound time.Duration) {
	t.Helper()
	last := ""
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if last = inst.wiStatus(t, wi); last == "wrapped" || last == "failed" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("work item %s never reached its terminal close within %v (hub status %q, log tail: %s)", wi, bound, last, h.logTail())
}

// w348WaitProfiles polls GET /profiles until pred holds (bounded).
func w348WaitProfiles(t *testing.T, h *w348Host, what string, pred func(map[string]map[string]any) bool, bound time.Duration) map[string]map[string]any {
	t.Helper()
	deadline := time.Now().Add(bound)
	var last string
	for time.Now().Before(deadline) {
		status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
		if status == 200 {
			rows := w348ProfilesRows(t, body)
			if pred(rows) {
				return rows
			}
			last = body
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("GET /profiles never matched %q within %v (last %q)", what, bound, last)
	return nil
}

// w348CopyTree copies a data-dir fixture for the restore-backup scenario.
func w348CopyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o600)
	}); err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
}

// TestW348PendingCellMappingDoesNotPreRoute — AC3: a Cell's newer on-disk
// workspace mapping is not routing fact before the Cell applies it. The
// fixture is a PARSEABLE cli-alpha.toml naming wsNew: without a reload the
// resolver and the effective index must ignore it, and the same mapping
// must route once `pf --profile alpha config reload` applies it (proving
// the earlier non-route was the pending gate, not an unreadable file).
func TestW348PendingCellMappingDoesNotPreRoute(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	wsDefault, wsAlpha, wsNew := t.TempDir(), t.TempDir(), t.TempDir()
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: wsDefault, projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	h.start(30 * time.Second)

	if out, code := h.pf("", "--profile", "alpha", "--json", "profile"); code != 0 || !strings.Contains(strings.ToLower(out), "alpha") {
		t.Fatalf("the original alpha mapping did not route before the change: exit %d (%q)", code, out)
	}
	// The on-disk file now names wsNew and is parseable (same server and
	// key, a model declaration) but nothing reloaded it. No reader may treat
	// the pending mapping as routing fact.
	w348WriteFile(t, h.cellConfigPath("alpha"),
		w348ConfigText(alpha.url, alpha.adminKey, "", "usr_root", 0, 0,
			[]w348WS{{path: wsNew, projects: []string{"e2e"}}},
			"[model]\nsource = \"shared-default\""))
	out, code := w348Run(t, h.env(), wsNew, "", "--json", "profile")
	if code != 0 {
		t.Fatalf("pf profile from the not-yet-applied mapping: exit %d (%q)", code, out)
	}
	p, _ := w348Field(w348JSON(t, "pf profile", out), "profile")
	if p == "alpha" {
		t.Fatalf("the not-yet-applied on-disk mapping pre-routed business traffic to alpha: %q", out)
	}
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles: %d (%s)", status, body)
	}
	if strings.Contains(body, wsNew) {
		t.Fatalf("the not-yet-applied mapping entered the effective workspace index: %s", body)
	}
	// The old mapping still routes, because the Cell still serves it.
	out, code = w348Run(t, h.env(), wsAlpha, "", "--json", "profile")
	if code != 0 {
		t.Fatalf("pf profile from the effective mapping: exit %d (%q)", code, out)
	}
	p, _ = w348Field(w348JSON(t, "pf profile", out), "profile")
	if p != "alpha" {
		t.Fatalf("the still-effective alpha mapping stopped routing: %q", out)
	}
	// Applying exactly that file proves the mapping was routable all along.
	w348RunWant(t, -1, h.env(), "", "--profile", "alpha", "config", "reload")
	deadline := time.Now().Add(30 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		status, body = w348UDS(t, h.sock, "GET", "/profiles", nil, "")
		if status == 200 && strings.Contains(body, wsNew) {
			last = body
			break
		}
		last = body
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(last, wsNew) {
		t.Fatalf("the applied alpha mapping never entered the effective workspace index: %s", last)
	}
	out, code = w348Run(t, h.env(), wsNew, "", "--json", "profile")
	if code != 0 {
		t.Fatalf("pf profile from the applied mapping: exit %d (%q)", code, out)
	}
	p, _ = w348Field(w348JSON(t, "pf profile", out), "profile")
	if p != "alpha" {
		t.Fatalf("the applied mapping is not routable (got %q): %q", p, out)
	}
}

// TestW348StepAgentNamingAnotherCellIsRefused — AC4: a step agent whose Cell
// is default may not act on alpha, whatever header it sends; no side effect
// reaches alpha's Hub.
func TestW348StepAgentNamingAnotherCellIsRefused(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	mid := w348CellMachineID(t, h, "default")
	stepEnv := []string{"PF_WI_ID=wi_w348_cross", "PF_DAEMON_ID=" + mid}

	out, code := h.pfEnv(stepEnv, "", "--profile", "alpha", "watch", "wi_w348_cross", "--once")
	if code == 0 || !strings.Contains(out, "DAEMON-403") || !strings.Contains(strings.ToLower(out), "owning cell") {
		t.Fatalf("a step agent of default naming alpha must be refused DAEMON-403: exit %d (%q)", code, out)
	}
	out, code = h.pfEnv(stepEnv, "", "--profile", "alpha", "projects", "create", "--name", "cross-ghost")
	if code == 0 || !strings.Contains(out, "DAEMON-403") {
		t.Fatalf("a step agent's side-effecting cross-Cell request was not refused: exit %d (%q)", code, out)
	}
	if w348HubProjects(t, alpha)["cross-ghost"] {
		t.Fatalf("the refused cross-Cell request reached alpha's Hub")
	}
	out, _ = h.pfEnv(stepEnv, "", "watch", "wi_w348_cross", "--once")
	// The owning Cell's own read reaches the route; the unknown WI is a lookup
	// result, never the cross-Cell ownership refusal.
	if strings.Contains(out, "DAEMON-403") {
		t.Fatalf("the owning Cell's own read was refused as cross-Cell: %q", out)
	}
}

// TestW348UserChangeIsRefusedRequiringANewProfile — AC5: a credential for a
// different user cannot replace alpha in place; alpha keeps its identity,
// history and Hub.
func TestW348UserChangeIsRefusedRequiringANewProfile(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	if _, err := alpha.client.CreateProject(aihubclient.ProjectCreateRequest{Name: "alpha-only"}); err != nil {
		t.Fatalf("create alpha-only: %v", err)
	}
	rawUser, err := alpha.client.CreateUser(aihubclient.UserCreateRequest{DisplayName: "w348 other", Email: "w348-other@invalid", Role: "writer"})
	if err != nil {
		t.Fatalf("create another user: %v", err)
	}
	var other struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rawUser, &other); err != nil || other.ID == "" {
		t.Fatalf("create user answer: %s (%v)", rawUser, err)
	}
	rawKey, err := alpha.client.CreateApiKey(aihubclient.ApiKeyCreateRequest{UserID: other.ID, Name: "w348-other-key"})
	if err != nil {
		t.Fatalf("mint another user's key: %v", err)
	}
	var issued struct {
		Plain string `json:"plain"`
	}
	if err := json.Unmarshal(rawKey, &issued); err != nil || issued.Plain == "" {
		t.Fatalf("mint key answer: %s (%v)", rawKey, err)
	}
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"alpha-only"}}})
	h.start(30 * time.Second)
	if out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list"); code != 0 || !strings.Contains(out, "alpha-only") {
		t.Fatalf("alpha did not serve before the identity change: exit %d (%q)", code, out)
	}

	// Same server, another user: identity changed -> create a new profile.
	w348WriteCellWithModel(t, h, "alpha", alpha.url, issued.Plain, 0, []w348WS{{path: t.TempDir(), projects: []string{"alpha-only"}}})
	out, code := h.pf("", "--profile", "alpha", "config", "reload")
	reload := out
	if code == 0 {
		t.Fatalf("a different user's credential was accepted in place: %q", out)
	}
	low := strings.ToLower(reload)
	if !strings.Contains(low, "new profile") && !strings.Contains(low, "unverified") && !strings.Contains(low, "identity could not be verified") {
		t.Fatalf("a different user's credential must be refused in place (new profile or unverified identity): %q", reload)
	}
	if out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list"); code != 0 || !strings.Contains(out, "alpha-only") {
		t.Fatalf("the refused identity change disturbed alpha's old identity: exit %d (%q)", code, out)
	}
}

// TestW348CleanupLeavesNothingRoutable — AC5: permanent cleanup (CLI-only,
// confirmed) deletes the removed Cell's state and every Host reference; no
// stale route survives.
func TestW348CleanupLeavesNothingRoutable(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	w348RunWant(t, 0, h.env(), "", "cell", "remove", "alpha")
	if !w348Exists(filepath.Join(h.dataDir, "cells", "alpha")) {
		t.Fatalf("remove destroyed alpha's retained state dir before cleanup")
	}

	out, code := h.pf("", "cell", "cleanup", "alpha")
	if code == 0 {
		t.Fatalf("cleanup without --confirm acted: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "cells/alpha") && !strings.Contains(out, "alpha") {
		t.Fatalf("cleanup without --confirm must list its impact: %q", out)
	}
	if !w348Exists(filepath.Join(h.dataDir, "cells", "alpha")) {
		t.Fatalf("cleanup without --confirm deleted the state anyway")
	}
	w348RunWant(t, 0, h.env(), "", "cell", "cleanup", "alpha", "--confirm", "alpha")
	if w348Exists(filepath.Join(h.dataDir, "cells", "alpha")) {
		t.Fatalf("confirmed cleanup left the state dir behind")
	}
	if out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list"); code == 0 {
		t.Fatalf("a deleted Cell still routes: %q", out)
	}
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles: %d (%s)", status, body)
	}
	if _, ok := w348ProfilesRows(t, body)["alpha"]; ok {
		t.Fatalf("cleanup left alpha in the Host registry: %s", body)
	}
}

// TestW348MigrationKill9DuringApplyConverges — AC9: kill -9 in the middle of
// --apply leaves no layout-version; a re-run converges on the same identity.
func TestW348MigrationKill9DuringApplyConverges(t *testing.T) {
	h := w348NewHost(t)
	w348OldLayout(t, h.dataDir)
	crashEnv := h.envWith("PF_CRASH_AT=migrate-published-events")
	out, code := w348Run(t, crashEnv, "", "", "migrate", "cell-layout", "--apply")
	if code == 0 {
		t.Fatalf("the crash-point apply was not killed: %q", out)
	}
	if w348Exists(filepath.Join(h.dataDir, "layout-version")) {
		t.Fatalf("an interrupted apply published layout-version")
	}
	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--apply")
	if !w348Exists(filepath.Join(h.dataDir, "layout-version")) {
		t.Fatalf("the re-run did not publish layout-version")
	}
	if got := strings.TrimSpace(w348ReadString(t, filepath.Join(h.dataDir, "cells", "default", "daemon-id"))); got != "m-w348-old" {
		t.Fatalf("the converging re-run changed the machine id: %q", got)
	}
	if got := strings.TrimSpace(w348ReadString(t, filepath.Join(h.dataDir, "cells", "default", "machine-key"))); got != w348OldMachineKey {
		t.Fatalf("the converging re-run changed the machine key: %q", got)
	}
}

// TestW348RollbackRefusedAfterTheHostServedMigratedLayout — AC9: once a Host
// served the migrated layout, the pre-migration copy is not a rollback
// shortcut; the refusal offers forward repair.
func TestW348RollbackRefusedAfterTheHostServedMigratedLayout(t *testing.T) {
	h := w348NewHost(t)
	h.writeHost("http://127.0.0.1:1", "w348-default-key", 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348OldLayout(t, h.dataDir)
	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--apply")
	h.start(30 * time.Second)
	h.stop()

	out, code := h.pf("", "migrate", "cell-layout", "--rollback")
	if code == 0 {
		t.Fatalf("rollback was allowed after the Host served the new layout: %q", out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "rollback refused") || !strings.Contains(low, "forward") {
		t.Fatalf("the closed-window refusal must say rollback is refused and name forward repair: %q", out)
	}
	if !w348Exists(filepath.Join(h.dataDir, "layout-version")) || !w348Exists(filepath.Join(h.dataDir, "cells", "default")) {
		t.Fatalf("the refused rollback still changed the migrated layout")
	}
}

// TestW348RestoreBackupNeedsConfirmation — AC9: a full backup restore lists
// the data-loss scope and changes nothing without --confirm; with it the
// backup is restored and the replaced state is kept aside.
func TestW348RestoreBackupNeedsConfirmation(t *testing.T) {
	h := w348NewHost(t)
	w348OldLayout(t, h.dataDir)
	backup := filepath.Join(t.TempDir(), "pre-migration-backup")
	w348CopyTree(t, h.dataDir, backup)
	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--apply")
	// A new write since the backup: restoring it must be an explicit choice.
	w348WriteFile(t, filepath.Join(h.dataDir, "chats.json"), `{"version":1,"chats":{"pi:new":"chat_new"}}`+"\n")
	before := w348TreeHashes(t, h.dataDir)

	out, code := h.pf("", "migrate", "cell-layout", "--restore-backup", backup)
	if code == 0 {
		t.Fatalf("restore-backup acted without --confirm: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "layout-version") || !strings.Contains(strings.ToLower(out), "cells") {
		t.Fatalf("the unconfirmed restore must list the data-loss scope: %q", out)
	}
	w348SameTree(t, before, w348TreeHashes(t, h.dataDir), "an unconfirmed restore-backup")

	out = w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--restore-backup", backup, "--confirm")
	if !strings.Contains(out, "restored") {
		t.Fatalf("the confirmed restore did not report its result: %q", out)
	}
	if w348Exists(filepath.Join(h.dataDir, "layout-version")) {
		t.Fatalf("the restored pre-Cell backup carries no layout-version, yet it survived")
	}
	if got := strings.TrimSpace(w348ReadString(t, filepath.Join(h.dataDir, "daemon-id"))); got != "m-w348-old" {
		t.Fatalf("the restored backup answered a different machine id: %q", got)
	}
	if strings.Contains(w348ReadString(t, filepath.Join(h.dataDir, "chats.json")), "chat_new") {
		t.Fatalf("the post-backup write survived the confirmed restore")
	}
	var aside string
	ents, _ := os.ReadDir(filepath.Join(h.dataDir, "migration"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "replaced-") {
			aside = e.Name()
		}
	}
	if aside == "" {
		t.Fatalf("the replaced state was deleted instead of kept aside under migration/replaced-*")
	}
}

// TestW348VersionMismatchBlocksHostWhileLocalRepairWorks — AC9: an unknown
// layout version blocks the runtime, while a local repair verb still works.
func TestW348VersionMismatchBlocksHostWhileLocalRepairWorks(t *testing.T) {
	h := w348NewHost(t)
	h.writeHost("http://127.0.0.1:1", "w348-default-key", 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteFile(t, filepath.Join(h.dataDir, "layout-version"), "cells-v99\n")
	served, output, code := h.boot(20 * time.Second)
	if served {
		h.stop()
		t.Fatalf("the Host started on an unknown layout version")
	}
	low := strings.ToLower(output)
	if code == 0 || (!strings.Contains(low, "layout-version") && !strings.Contains(low, "layout version")) {
		t.Fatalf("the version-mismatch refusal must name the layout version; exit %d (log %q)", code, output)
	}
	if out := w348RunWant(t, 0, h.env(), "", "config", "validate"); strings.Contains(strings.ToLower(out), "unknown") {
		t.Fatalf("local repair stopped working beside a version mismatch: %q", out)
	}
}

// TestW348NoWorkspaceCellServesDataPlaneWithExecutionDisabled — AC8: a Cell
// with a complete connection and no workspace loads, answers Hub data-plane
// queries, and reports execution disabled until mapped.
func TestW348NoWorkspaceCellServesDataPlaneWithExecutionDisabled(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	if _, err := alpha.client.CreateProject(aihubclient.ProjectCreateRequest{Name: "alpha-only"}); err != nil {
		t.Fatalf("create alpha-only: %v", err)
	}
	wi := alpha.newWI(t, "w348 no-workspace cell", envelopeMicro(), nil)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, nil)
	h.start(30 * time.Second)

	out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list")
	if code != 0 || !strings.Contains(out, "alpha-only") {
		t.Fatalf("a no-workspace Cell must answer Hub data-plane queries: exit %d (%q)", code, out)
	}
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles: %d (%s)", status, body)
	}
	row := w348ProfileRow(t, body, "alpha")
	if valid, _ := row["valid"].(bool); !valid {
		t.Fatalf("the complete no-workspace Cell must be valid: %s", body)
	}
	if disabled, _ := row["execution_disabled"].(string); disabled == "" {
		t.Fatalf("the no-workspace Cell must report execution disabled: %v", row)
	}
	if ws, _ := row["workspaces"].([]any); len(ws) != 0 {
		t.Fatalf("the no-workspace Cell reports a mapped workspace: %v", row["workspaces"])
	}
	out, code = h.pf("", "--profile", "alpha", "submit", wi)
	if code == 0 {
		t.Fatalf("execution started on a no-workspace Cell: %q", out)
	}
	if low := strings.ToLower(out); !strings.Contains(low, "workspace") && !strings.Contains(low, "execution") && !strings.Contains(low, "model-credential") {
		t.Fatalf("the no-workspace execution refusal must name the reason: %q", out)
	}
}

// TestW348InvalidNamedCellProfileRowIsInvalid — review s20#2: parse
// /profiles and assert alpha's row carries valid=false (not a substring).
func TestW348InvalidNamedCellProfileRowIsInvalid(t *testing.T) {
	def := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteFile(t, h.cellConfigPath("alpha"), "server = \n")
	h.start(30 * time.Second)
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles: %d (%s)", status, body)
	}
	row := w348ProfileRow(t, body, "alpha")
	if valid, ok := row["valid"].(bool); !ok || valid {
		t.Fatalf("alpha's /profiles row must carry valid=false: %v", row)
	}
	if reason, _ := row["reason"].(string); reason == "" {
		t.Fatalf("alpha's invalid row must carry a reason: %v", row)
	}
	if row := w348ProfileRow(t, body, "default"); row["valid"] != true {
		t.Fatalf("the valid default row must stay valid: %v", row)
	}
}

// TestW348ConflictingCellProfileRowsAreBothInvalid — review s20#2: parse
// /profiles and assert valid=false on BOTH conflicting rows.
func TestW348ConflictingCellProfileRowsAreBothInvalid(t *testing.T) {
	def := newV1Install(t)
	shared := newV1Install(t)
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", shared.url, shared.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("beta", shared.url, shared.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles: %d (%s)", status, body)
	}
	rows := w348ProfilesRows(t, body)
	for _, profile := range []string{"alpha", "beta"} {
		row, ok := rows[profile]
		if !ok {
			t.Fatalf("the conflict report drops %q: %s", profile, body)
		}
		if valid, ok := row["valid"].(bool); !ok || valid {
			t.Fatalf("%s must be invalid in its own row (no load-order winner): %v", profile, row)
		}
	}
	if row := rows["default"]; row == nil || row["valid"] != true {
		t.Fatalf("the default Cell must stay valid beside a conflict: %v", rows["default"])
	}
}

// w348LaunchWithPlans starts the Host with FAKEPI_PLANS pointed at a scripted
// plan dir (the acceptance harnesses' fake-agent convention), then waits for
// its UDS face.
func w348LaunchWithPlans(t *testing.T, h *w348Host, plans, workflowsDir string, bound time.Duration) {
	t.Helper()
	logf, err := os.Create(h.logPath)
	if err != nil {
		t.Fatal(err)
	}
	h.logf = logf
	binDir := v1Bins(t)
	t.Cleanup(h.stop)
	args := []string{
		"daemon",
		"--socket", h.sock,
		"--data-dir", h.dataDir,
		"--workflows-dir", workflowsDir,
		"--pi-bin", filepath.Join(binDir, "fakepi"),
		"--claude-bin", "/nonexistent/claude-test-guard",
	}
	cmd := exec.Command(filepath.Join(binDir, "pf"), args...)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + h.home,
		"XDG_CONFIG_HOME=" + h.xdg,
		fakepiTestPIDEnv + "=" + os.Getenv(fakepiTestPIDEnv),
		"FAKEPI_PLANS=" + plans,
		"GIT_AUTHOR_NAME=pf-e2e",
		"GIT_AUTHOR_EMAIL=pf-e2e@invalid",
		"GIT_COMMITTER_NAME=pf-e2e",
		"GIT_COMMITTER_EMAIL=pf-e2e@invalid",
	}
	cmd.Dir = h.dataDir
	cmd.Stdout, cmd.Stderr = logf, logf
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h.cmd = cmd
	h.wait = make(chan error, 1)
	go func() { h.wait <- cmd.Wait() }()
	answered, exited, werr := awaitAnsweredFace(h.sock, time.Now().Add(bound), h.wait)
	if exited {
		h.cmd = nil
		c := -1
		if ee, ok := werr.(*exec.ExitError); ok {
			c = ee.ExitCode()
		}
		t.Fatalf("daemon exited during startup: exit %d (log tail: %s)", c, h.logTail())
	}
	if !answered {
		h.stop()
		t.Fatalf("daemon never answered on %s within %v (log tail: %s)", h.sock, bound, h.logTail())
	}
}

// w348Kill9 kills the Host process hard (the crash-restart scenarios).
func w348Kill9(t *testing.T, h *w348Host) {
	t.Helper()
	if h.cmd == nil || h.cmd.Process == nil {
		t.Fatal("kill -9: Host not running")
	}
	_ = h.cmd.Process.Kill()
	select {
	case <-h.wait:
	case <-time.After(10 * time.Second):
		t.Fatal("the killed Host did not exit")
	}
	h.cmd = nil
}

// w348ParkedPlan scripts one ask of the owner (`pf ask`) the fix step's
// resident process completes on the reply, and then review.
func w348ParkedPlan(question string) []map[string]any {
	return []map[string]any{
		askAction("fix", question, map[string]any{"do": "envelope", "id": "fix", "outcome": "completed"}),
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}
}

// w348RotatedKey mints a fresh same-user key on inst's Hub.
func w348RotatedKey(t *testing.T, inst *v1Install, name string) string {
	t.Helper()
	raw, err := inst.client.CreateApiKey(aihubclient.ApiKeyCreateRequest{UserID: "usr_root", Name: name})
	if err != nil {
		t.Fatalf("mint rotated key: %v", err)
	}
	var issued struct {
		Plain string `json:"plain"`
	}
	if err := json.Unmarshal(raw, &issued); err != nil || issued.Plain == "" {
		t.Fatalf("mint rotated key answer: %s (%v)", raw, err)
	}
	return issued.Plain
}

// TestW348FollowUpUsesPFProfileNotTheCwd — AC4, after (the
// chat subscription that once named the WorkRef's Cell is retired): an
// answer from a default-mapped cwd lands on alpha when PF_PROFILE names
// it — the cwd / PF_PROFILE rules every Cell-scoped command follows. The
// pending question is also an open entry of the Host decision feed naming
// its Cell, resolved by the answer.
func TestW348FollowUpUsesPFProfileNotTheCwd(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	wi := alpha.newWI(t, "w348 follow-up on WorkRef.profile", w348ParkedPlan("continue on alpha?"), nil)
	wsDefault, wsAlpha := t.TempDir(), t.TempDir()
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: wsDefault, projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)

	if out, code := h.pf("", "--profile", "alpha", "submit", wi); code != 0 {
		t.Fatalf("submit alpha job: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", wi, "parked", 30*time.Second)
	if e, ok := w348WaitOpenFeedEntry(t, h, wi, "needs_input", 10*time.Second); !ok || e.Profile != "alpha" || !e.Open() {
		t.Fatalf("the parked question is not an open alpha feed entry: %+v (found %v)", e, ok)
	}
	// The cwd points at default; PF_PROFILE routes this answer to alpha.
	out, code := h.pfEnv([]string{"PF_PROFILE=alpha"}, wsDefault,
		"answer", wi, "--text", "continue", "--by", "usr_root")
	if code != 0 {
		t.Fatalf("follow-up on the alpha WorkRef failed: exit %d (%q)", code, out)
	}
	deadline := time.Now().Add(30 * time.Second)
	leftNeedsInput := false
	for time.Now().Before(deadline) {
		doc := w348StatusDoc(t, h)
		for _, j := range w348Jobs(doc) {
			if p, _ := j["profile"].(string); p != "alpha" {
				continue
			}
			if id, _ := j["work_item_id"].(string); id != wi {
				continue
			}
			if ni, _ := j["needs_input"].(bool); !ni {
				leftNeedsInput = true
			}
		}
		if leftNeedsInput {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !leftNeedsInput {
		t.Fatalf("the alpha job stayed parked: the answer did not land on alpha (log tail: %s)", h.logTail())
	}
	w348WaitTerminal(t, h, alpha, wi, 60*time.Second)
	if e, ok := w348FeedEntry(t, h, wi, "needs_input"); !ok || e.Open() {
		t.Fatalf("the answered question is still open in the feed: %+v (found %v)", e, ok)
	}
}

// w348FeedEntry reads the Host decision feed through the real `pf watch
// decisions --json` and answers the newest entry of the work item and
// class.
func w348FeedEntry(t *testing.T, h *w348Host, wi, class string) (protocol.Decision, bool) {
	t.Helper()
	out := w348RunWant(t, 0, h.env(), "", "--json", "watch", "decisions")
	var feed protocol.DecisionFeed
	if err := json.Unmarshal([]byte(w113JSONDocOf(out)), &feed); err != nil {
		t.Fatalf("decision feed unreadable: %v (%s)", err, out)
	}
	for _, e := range feed.Decisions {
		if e.WI == wi && e.Class == class {
			return e, true
		}
	}
	return protocol.Decision{}, false
}

// w348WaitOpenFeedEntry polls the feed until the work item's class has an
// open entry (bounded): the status face reads the open ask from asks.jsonl
// at once, while the driver notices it on its ask poll before it emits
// the awaiting_owner event the feed is built from.
func w348WaitOpenFeedEntry(t *testing.T, h *w348Host, wi, class string, bound time.Duration) (protocol.Decision, bool) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		e, ok := w348FeedEntry(t, h, wi, class)
		if (ok && e.Open()) || !time.Now().Before(deadline) {
			return e, ok
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestW348KeyRotationDrainsAndSwitchesAfterIdle — AC5: a same-identity key
// rotation is accepted pending, refuses new submits, and applies by itself
// once the Cell is idle.
func TestW348KeyRotationDrainsAndSwitchesAfterIdle(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	hold := []map[string]any{
		{"do": "envelope", "id": "fix", "outcome": "completed", "ms": 12000},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}
	wi := alpha.newWI(t, "w348 rotation drain", hold, nil)
	wi2 := alpha.newWI(t, "w348 rotation new submit", envelopeMicro(), nil)
	wsAlpha := t.TempDir()
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 3, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "submit", wi); code != 0 {
		t.Fatalf("submit alpha job: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", wi, "running", 30*time.Second)

	rotated := w348RotatedKey(t, alpha, "w348-rotation")
	w348WriteCellWithModel(t, h, "alpha", alpha.url, rotated, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	out, code := h.pf("", "--profile", "alpha", "config", "reload")
	if code != 0 {
		t.Fatalf("the same-identity rotation was not accepted: exit %d (%q)", code, out)
	}
	rows := w348WaitProfiles(t, h, "alpha draining", func(rows map[string]map[string]any) bool {
		row := rows["alpha"]
		if row == nil {
			return false
		}
		draining, _ := row["draining"].(bool)
		pending, _ := row["pending_change"].(string)
		return draining || pending != ""
	}, 10*time.Second)
	if pending, _ := rows["alpha"]["pending_change"].(string); pending == "" {
		t.Logf("alpha reports draining without a pending_change name: %v", rows["alpha"])
	}

	out, code = h.pf("", "--profile", "alpha", "submit", wi2)
	if code == 0 || !strings.Contains(out, "DAEMON-409") {
		t.Fatalf("a new submit was accepted while the rotation drained: exit %d (%q)", code, out)
	}
	// The running work is never killed: it reaches its terminal state.
	w348WaitTerminal(t, h, alpha, wi, 60*time.Second)
	w348WaitProfiles(t, h, "alpha idle after drain", func(rows map[string]map[string]any) bool {
		row := rows["alpha"]
		if row == nil {
			return false
		}
		draining, _ := row["draining"].(bool)
		return !draining
	}, 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list"); code != 0 {
		t.Fatalf("alpha stopped serving after the rotation: exit %d (%q)", code, out)
	}
}

// TestW348Kill9DuringDrainRestoresEffectiveAndPending — AC5 (owner ruling
// r2): kill -9 during a pending drain restores the held job and the effective
// connection first; the pending change is not applied early.
func TestW348Kill9DuringDrainRestoresEffectiveAndPending(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	// the crash ends the asking process: the reply resumes fix as the
	// plan's second dispatch
	wi := alpha.newWI(t, "w348 kill9 drain", []map[string]any{
		askAction("fix", "hold through the crash", nil),
		{"do": "envelope", "id": "fix", "outcome": "completed"},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}, nil)
	wsAlpha := t.TempDir()
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 3, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "submit", wi); code != 0 {
		t.Fatalf("submit alpha job: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", wi, "parked", 30*time.Second)

	rotated := w348RotatedKey(t, alpha, "w348-kill9-rotation")
	w348WriteCellWithModel(t, h, "alpha", alpha.url, rotated, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	if out, code := h.pf("", "--profile", "alpha", "config", "reload"); code != 0 {
		t.Fatalf("rotation refused before the crash: exit %d (%q)", code, out)
	}
	w348WaitProfiles(t, h, "alpha draining", func(rows map[string]map[string]any) bool {
		row := rows["alpha"]
		draining, _ := row["draining"].(bool)
		pending, _ := row["pending_change"].(string)
		return row != nil && (draining || pending != "")
	}, 10*time.Second)

	w348Kill9(t, h)
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)

	// The held job is restored and the effective (old) connection still
	// serves; the newer on-disk key stays pending, never applied early.
	job := w348WaitJobStatus(t, h, "alpha", wi, "parked", 30*time.Second)
	if ni, _ := job["needs_input"].(bool); !ni {
		t.Fatalf("the restored job lost its held question: %v", job)
	}
	rows := w348ProfilesRows(t, func() string {
		status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
		if status != 200 {
			t.Fatalf("GET /profiles after restart: %d (%s)", status, body)
		}
		return body
	}())
	row := rows["alpha"]
	if row == nil {
		t.Fatalf("alpha disappeared after the crash-restart")
	}
	draining, _ := row["draining"].(bool)
	pending, _ := row["pending_change"].(string)
	if !draining && pending == "" {
		t.Fatalf("the pending rotation was applied early by the restart: %v", row)
	}
	if out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list"); code != 0 {
		t.Fatalf("the restored effective connection does not serve: exit %d (%q)", code, out)
	}
	// Held work is still answerable while the change waits (A1).
	out, code := h.pf("", "--profile", "alpha", "answer", wi, "--text", "continue", "--by", "usr_root")
	if code != 0 {
		t.Fatalf("the restored held work is not answerable: exit %d (%q)", code, out)
	}
	w348WaitTerminal(t, h, alpha, wi, 60*time.Second)
}

// w348WriteCellWithModel writes a named Cell with the explicit model source
// that opens its execution plane (the no-workspace/lifecycle scenarios use a
// plain writeCell instead).
func w348WriteCellWithModel(t *testing.T, h *w348Host, profile, server, apiKey string, cellMax int, ws []w348WS) string {
	t.Helper()
	return w348WriteFile(t, h.cellConfigPath(profile),
		w348ConfigText(server, apiKey, "", "usr_root", 0, cellMax, ws, "[model]\nsource = \"shared-default\""))
}

// w348WriteHostWithModel writes the Host file with a model source so the
// default Cell's execution plane is open.
func w348WriteHostWithModel(t *testing.T, h *w348Host, server, apiKey string, maxJobs int, ws []w348WS) string {
	t.Helper()
	// The default Cell's model credentials come from [daemon] operator_agent_dir,
	// so its file carries no [model] table (a named Cell's does).
	return h.writeHost(server, apiKey, maxJobs, ws)
}

// w348LongPlan is one long fix turn followed by a normal completion.
func w348LongPlan(ms int) []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "fix", "outcome": "completed", "ms": ms},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}
}

// TestW348WaitingJobFreesItsSlot — AC7: a job parked on a human with no
// executing process holds no execution slot; a queued job starts beside it.
func TestW348WaitingJobFreesItsSlot(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	held := alpha.newWI(t, "w348 waiting frees slot (held)", w348LongPlan(30000), nil)
	next := def.newWI(t, "w348 waiting frees slot (queued)", envelopeMicro(), nil)
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 1, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "submit", held); code != 0 {
		t.Fatalf("submit held job: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", held, "running", 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "pause", held); code != 0 {
		t.Fatalf("pause the held job: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", held, "paused", 30*time.Second)
	if out, code := h.pf("", "submit", next); code != 0 {
		t.Fatalf("submit queued job: exit %d (%q)", code, out)
	}
	w348WaitTerminal(t, h, def, next, 60*time.Second)
	if job := w348WaitJobStatus(t, h, "alpha", held, "paused", 1*time.Second); job == nil {
		t.Fatalf("the paused job disappeared")
	}
}

// TestW348RoundRobinServesTheOtherCellBeforeTheBusyOne — AC7: with one Host
// slot, a startable Cell that did not just run gets the next slot ahead of a
// second queued job on the Cell that just finished.
func TestW348RoundRobinServesTheOtherCellBeforeTheBusyOne(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	a1 := alpha.newWI(t, "w348 rr alpha first", w348LongPlan(9000), nil)
	a2 := alpha.newWI(t, "w348 rr alpha second", w348LongPlan(4000), nil)
	d1 := def.newWI(t, "w348 rr default", w348LongPlan(4000), nil)
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 1, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "submit", a1); code != 0 {
		t.Fatalf("submit alpha first: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", a1, "running", 30*time.Second)
	// Queue alpha's SECOND job before the default job: FIFO (and a race
	// between per-Cell dispatch ticks) would start it next, so only a
	// round-robin over runnable Cells can give the slot to default.
	if out, code := h.pf("", "--profile", "alpha", "submit", a2); code != 0 {
		t.Fatalf("submit alpha second queued: exit %d (%q)", code, out)
	}
	if out, code := h.pf("", "submit", d1); code != 0 {
		t.Fatalf("submit default queued: exit %d (%q)", code, out)
	}
	w348WaitTerminal(t, h, alpha, a1, 60*time.Second)
	deadline := time.Now().Add(30 * time.Second)
	started := ""
	for time.Now().Before(deadline) {
		for _, j := range w348Jobs(w348StatusDoc(t, h)) {
			st, _ := j["status"].(string)
			if st != "running" {
				continue
			}
			id, _ := j["work_item_id"].(string)
			p, _ := j["profile"].(string)
			if id == d1 {
				started = p
			}
			if id == a2 {
				started = "alpha-second"
			}
		}
		if started != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if started != "default" {
		t.Fatalf("round-robin did not serve the idle Cell next; the slot went to %q (log tail: %s)", started, h.logTail())
	}
}

// TestW348LoweringTheCeilingKillsNothing — AC7: lowering the Host ceiling
// stops new starts only; running work keeps running.
func TestW348LoweringTheCeilingKillsNothing(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	a := alpha.newWI(t, "w348 lower alpha", w348LongPlan(10000), nil)
	d := def.newWI(t, "w348 lower default", w348LongPlan(10000), nil)
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "submit", a); code != 0 {
		t.Fatalf("submit alpha: exit %d (%q)", code, out)
	}
	if out, code := h.pf("", "submit", d); code != 0 {
		t.Fatalf("submit default: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", a, "running", 30*time.Second)
	w348WaitJobStatus(t, h, "default", d, "running", 30*time.Second)
	w348RunWant(t, 0, h.env(), "", "ops", "max", "1")
	for _, job := range []struct{ profile, wi string }{{"alpha", a}, {"default", d}} {
		doc := w348StatusDoc(t, h)
		found := false
		for _, j := range w348Jobs(doc) {
			p, _ := j["profile"].(string)
			id, _ := j["work_item_id"].(string)
			if p != job.profile || id != job.wi {
				continue
			}
			found = true
			if st, _ := j["status"].(string); st == "failed" || st == "canceled" {
				t.Fatalf("lowering the ceiling killed %s/%s: %v", job.profile, job.wi, j)
			}
		}
		if !found {
			t.Fatalf("lowering the ceiling dropped %s/%s from the job table", job.profile, job.wi)
		}
	}
}

// w348WorkspaceYAML gives a workspace the task model candidates the task
// lane reads.
func w348WorkspaceYAML(t *testing.T, path, project string) {
	t.Helper()
	w348WriteFile(t, filepath.Join(path, ".polyforge.yaml"),
		"steps:\n  "+project+":\n    task:\n      models: [\"pi:e2e\"]\n")
}

// TestW348TasksCountAgainstTheCeilings — AC7: with the Host ceiling taken by
// a running work item, a one-off task is refused at the same ceiling.
func TestW348TasksCountAgainstTheCeilings(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	d := def.newWI(t, "w348 task ceiling job", w348LongPlan(10000), nil)
	wsAlpha := t.TempDir()
	w348WorkspaceYAML(t, wsAlpha, "e2e")
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 1, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "submit", d); code != 0 {
		t.Fatalf("submit default job: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "default", d, "running", 30*time.Second)
	out, code := h.pf("", "--profile", "alpha", "task", "spawn", "--project", "e2e", "w348 ceiling")
	if code == 0 || !strings.Contains(out, "DAEMON-409") {
		t.Fatalf("a task spawn bypassed the taken Host ceiling: exit %d (%q)", code, out)
	}
	if low := strings.ToLower(out); !strings.Contains(low, "ceiling") && !strings.Contains(low, "concurrent") {
		t.Fatalf("the task refusal must name the ceiling: %q", out)
	}
}

// TestW348UnknownOutcomeIsNotResent — AC6: a side-effecting request whose
// response is lost after the Hub applied it is not blindly resent.
func TestW348UnknownOutcomeIsNotResent(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	// posts is written on the proxy's handler goroutine and read here.
	var posts atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req, err := http.NewRequest(r.Method, alpha.url+r.URL.RequestURI(), strings.NewReader(string(body)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/projects") {
			posts.Add(1)
			// The Hub applied the create; the caller never sees the answer.
			_, _ = io.Copy(io.Discard, resp.Body)
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			http.Error(w, "answer lost", http.StatusBadGateway)
			return
		}
		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})}
	go func() { _ = proxy.Serve(ln) }()
	t.Cleanup(func() { _ = proxy.Close() })
	proxyURL := "http://" + ln.Addr().String()

	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", proxyURL, alpha.adminKey, 0, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	out, code := h.pf("", "--profile", "alpha", "projects", "create", "--name", "once-only")
	if code == 0 {
		t.Fatalf("a create whose answer was lost reported success: %q", out)
	}
	if n := posts.Load(); n != 1 {
		t.Fatalf("the side-effecting request was sent %d times (a blind retry); it must be sent once", n)
	}
	if !w348HubProjects(t, alpha)["once-only"] {
		t.Fatalf("the Hub did not apply the one create")
	}
}

// ---------------------------------------------------------------------------
// Appended amendment (review s23#1), rewritten by the chat
// delivery it pinned is retired; a decision of a named Cell is an entry of
// the Host decision feed carrying its WorkRef.
// ---------------------------------------------------------------------------

// w348WriteCellIdentity writes a named Cell file with an explicit user_id, so
// a scenario can give the Cell and the default Cell the SAME literal user id
// (the cross-Hub identity case) or a different one (the no-receiver case).
func w348WriteCellIdentity(t *testing.T, h *w348Host, profile, server, apiKey, userID string, cellMax int, ws []w348WS) string {
	t.Helper()
	return w348WriteFile(t, h.cellConfigPath(profile),
		w348ConfigText(server, apiKey, "", userID, 0, cellMax, ws, "[model]\nsource = \"shared-default\""))
}

type w348Sub struct {
	WI      string
	Profile string
}

// TestW348DecisionFeedCarriesTheWorkRef — AC4: a decision of a named Cell
// is a Host decision feed entry naming its Cell (profile) and its work
// item, and `pf watch decisions` shows the profile-qualified name.
func TestW348DecisionFeedCarriesTheWorkRef(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	wi := alpha.newWI(t, "w348 notification workref", w348ParkedPlan("carry the WorkRef"), nil)
	item, err := alpha.client.GetWorkItem(wi)
	if err != nil || item == nil || item.Slug == "" {
		t.Fatalf("read the alpha work item's display name: %v (%+v)", err, item)
	}
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellIdentity(t, h, "alpha", alpha.url, alpha.adminKey, "usr_root", 0,
		[]w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "submit", wi); code != 0 {
		t.Fatalf("submit alpha job: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", wi, "needs_input", 30*time.Second)
	e, ok := w348WaitOpenFeedEntry(t, h, wi, "needs_input", 10*time.Second)
	if !ok || e.Profile != "alpha" || e.Slug != item.Slug || e.QuestionID == "" || !e.Open() {
		t.Fatalf("the feed entry does not carry the (alpha, %s) WorkRef: %+v (found %v)", wi, e, ok)
	}
	out := w348RunWant(t, 0, h.env(), "", "watch", "decisions")
	if want := "alpha:" + item.Slug + " needs_input"; !strings.Contains(out, want) {
		t.Fatalf("`pf watch decisions` does not show %q: %q", want, out)
	}
}

// TestW348DaemonArgvMatchesOnlyTheDaemon pins the process match the
// no-restart check counts with: the daemon itself, never another process
// that merely names the data dir.
func TestW348DaemonArgvMatchesOnlyTheDaemon(t *testing.T) {
	dir := "/tmp/w348-host"
	for _, c := range []struct {
		argv []string
		want bool
	}{
		{[]string{"/bin/pf", "daemon", "--config", "/x/cli.toml", "--data-dir", dir}, true},
		{[]string{"/bin/pf", "daemon", "-data-dir=" + dir}, true},
		{[]string{"docker", "ps", "--all", "--filter", "label=pf.verify.daemon=" + dir}, false},
		{[]string{"/bin/sh", "-c", "while :; do /bin/pf daemon --data-dir " + dir + "; done"}, false},
		{[]string{"/bin/pf", "daemon", "--data-dir", dir + "-other"}, false},
	} {
		if got := w348IsDaemonArgv(c.argv, dir); got != c.want {
			t.Errorf("w348IsDaemonArgv(%q) = %v, want %v", c.argv, got, c.want)
		}
	}
}
