package cli

// verbs_config_test.go: the writer-flock order of `pf config set|unset`
// (F1). The flock serializes the local read-modify-write; the
// daemon notification is a cross-process round trip and must run after the
// release — otherwise a slow daemon holds every other `pf config` writer
// for the client timeout.

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// probeWriterFlock tries to take path's flock exactly as a concurrent
// `pf config` writer would: a separate open plus LOCK_EX|LOCK_NB.
func probeWriterFlock(path string) (state, detail string) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return "missing", err.Error()
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return "held", err.Error()
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return "free", ""
}

// TestConfigSetNotifiesDaemonAfterReleasingTheWriterFlock pins the order:
// the atomic write (and the no-mapping note) finish, the writer flock is
// released, and only then does `pf config set` send POST /config/reload.
// The fake daemon probes the lock from inside the handler — the instant
// the CLI is waiting on the round trip, which is not a timing race.
func TestConfigSetNotifiesDaemonAfterReleasingTheWriterFlock(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cli.toml")
	// A mapping-less file keeps the write on the flat-key path: no
	// mapping change, so no /config/check preflight — the only daemon
	// round trip is /config/reload.
	if err := os.WriteFile(cfgPath, []byte("server = \"http://127.0.0.1:1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The unix socket path stays short: t.TempDir() alone can exceed the
	// sun_path limit on the platform.
	sockDir, err := os.MkdirTemp("", "pfcfg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "daemon.sock")

	var mu sync.Mutex
	state, detail, reloads := "no-reload", "", 0
	mux := http.NewServeMux()
	mux.HandleFunc("/config/reload", func(w http.ResponseWriter, r *http.Request) {
		s, d := probeWriterFlock(cfgPath + ".lock")
		mu.Lock()
		state, detail = s, d
		reloads++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"outcomes":[{"key":"claude_bin","class":"live","status":"applied"}]}`)
	})
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen %s: %v", sock, err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	var out bytes.Buffer
	ctx := &Context{ConfigPath: cfgPath, FlagSocket: sock, Stdout: &out, Stderr: &out}
	if err := runConfigSet(ctx, []string{"claude_bin", "/opt/claude-b"}); err != nil {
		t.Fatalf("pf config set claude_bin: %v (output %q)", err, out.String())
	}

	mu.Lock()
	gotState, gotDetail, gotReloads := state, detail, reloads
	mu.Unlock()
	if gotReloads == 0 {
		t.Fatalf("the daemon never received POST /config/reload (output %q)", out.String())
	}
	switch gotState {
	case "missing":
		t.Fatalf("premise broken: %s did not exist when /config/reload arrived (%s) — "+
			"this test probes the writer flock at the documented sibling path", cfgPath+".lock", gotDetail)
	case "held":
		t.Fatalf("the writer flock (%s) was still held while the CLI waited on POST /config/reload (%s) — "+
			"a concurrent `pf config` writer is blocked behind the daemon round trip", cfgPath+".lock", gotDetail)
	case "free":
	default:
		t.Fatalf("could not determine the writer-flock state at notify time: %s (%s)", gotState, gotDetail)
	}

	// the write itself still landed, and the outcome still comes from the
	// daemon's answer
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `claude_bin = "/opt/claude-b"`) {
		t.Fatalf("the edit was not written:\n%s", raw)
	}
	if !strings.Contains(out.String(), "claude_bin: applied (live)") {
		t.Fatalf("the daemon's reload outcome was not reported: %q", out.String())
	}
}

// stepsWriteFixture is newWfFixture with a dense yaml and its sha.
func stepsWriteFixture(t *testing.T) (wfFixture, string) {
	t.Helper()
	f := newWfFixture(t, "# keep me\nsteps:\n  alpha:\n    grill:\n      models: [\"pi:before\"]\n    code: { models: [\"pi:c\"] }\n")
	return f, filepath.Join(f.ws, ".polyforge.yaml")
}

func fileHash(t *testing.T, path string) (string, []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256Hex(raw), raw
}

// TestStepsChainWriteRefusesStaleSha pins the CAS: a stale --expect-hash
// is CORE-409 and the file stays byte-identical; the matching hash lands.
func TestStepsChainWriteRefusesStaleSha(t *testing.T) {
	f, path := stepsWriteFixture(t)
	sha, raw := fileHash(t, path)
	code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.grill.models", `["pi:x"]`, "--cwd", f.ws, "--expect-hash", strings.Repeat("0", 64))
	if code == 0 || !strings.Contains(stderr, "CORE-409") {
		t.Fatalf("a stale sha must be refused with CORE-409: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != string(raw) {
		t.Fatal("a refused write changed the file")
	}
	code, _, stderr = runPf(t, f.env, "config", "set", "steps.alpha.grill.models", `["pi:x"]`, "--cwd", f.ws, "--expect-hash", sha)
	if code != 0 {
		t.Fatalf("the matching write failed: %s", stderr)
	}
	if _, now := fileHash(t, path); !strings.Contains(string(now), "      models: [\"pi:x\"]\n") || !strings.Contains(string(now), "# keep me") {
		t.Fatalf("write did not land in place:\n%s", now)
	}
}

// TestStepsCandidateRefusedByLoader pins that a malformed candidate is
// refused by the strict loader (no other validator) with the file intact.
func TestStepsCandidateRefusedByLoader(t *testing.T) {
	f, path := stepsWriteFixture(t)
	sha, raw := fileHash(t, path)
	code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.grill.models", `["not-a-candidate"]`, "--cwd", f.ws, "--expect-hash", sha)
	if code == 0 || !strings.Contains(stderr, "steps.alpha.grill.models[0]") {
		t.Fatalf("a malformed candidate must be the loader's refusal: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != string(raw) {
		t.Fatal("a refused candidate changed the file")
	}
}

// TestStepsWriteRefusedWithoutWorkflowsDir pins the write gate: a daemon
// that names no workflows dir (so no step id can be checked) refuses a
// set and an unset with CORE-501, before the file is touched; a removed
// step-attr key is no workspace key at all.
func TestStepsWriteRefusedWithoutWorkflowsDir(t *testing.T) {
	f, path := stepsWriteFixture(t)
	sha, raw := fileHash(t, path)
	code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.grill.attrs.question_batch_size", "3", "--cwd", f.ws, "--expect-hash", sha)
	if code == 0 || !strings.Contains(stderr, `unknown workspace key "steps.alpha.grill.attrs.question_batch_size"`) {
		t.Fatalf("a step-attr key must be refused as no workspace key: %d %s", code, stderr)
	}
	f.env["PF_DAEMON_SOCKET"] = newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(jsonBody(map[string]any{"machine_id": "m", "jobs": []any{}})))
	}))
	code, _, stderr = runPf(t, f.env, "config", "set", "steps.alpha.grill.models", `["pi:x"]`, "--cwd", f.ws, "--expect-hash", sha)
	if code == 0 || !strings.Contains(stderr, "CORE-501") || !strings.Contains(stderr, "no workflows dir") {
		t.Fatalf("want CORE-501 naming the missing workflows dir: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != string(raw) {
		t.Fatal("the gate's refusal changed the file")
	}
	code, _, stderr = runPf(t, f.env, "config", "unset", "steps.alpha.code.models", "--cwd", f.ws)
	if code == 0 || !strings.Contains(stderr, "CORE-501") {
		t.Fatalf("an unset is gated too: %d %s", code, stderr)
	}
}

// TestStepsChainUnsetFallsBack pins the CLI clear: the chain-only entry is
// gone and the read reports it undeclared.
func TestStepsChainUnsetFallsBack(t *testing.T) {
	f, path := stepsWriteFixture(t)
	sha, _ := fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "unset", "steps.alpha.code.models", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("unset failed: %s", stderr)
	}
	if _, now := fileHash(t, path); strings.Contains(string(now), "code:") {
		t.Fatalf("the chain-only entry must be gone:\n%s", now)
	}
	if e := stepRead(t, readWorkspaceDoc(t, f), "alpha", "code"); e["declared"] != false {
		t.Fatalf("a cleared chain must read undeclared: %v", e)
	}
}

// TestConfigSetWritesSummarizeAndPolicyTables pins the cli.toml write face
// of the two moved families: summarize.* are flat keys that
// refuse a path flag, memory_policy.<entry>.<list> needs one, and unset
// removes exactly the addressed list — every write landing in cli.toml.
func TestConfigSetWritesSummarizeAndPolicyTables(t *testing.T) {
	ctx, ws := w22WorkspaceCtx(t)
	read := func() string {
		raw, err := os.ReadFile(ctx.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if err := runConfigSet(ctx, []string{"summarize.models", "pi:a,claude:b"}); err != nil {
		t.Fatalf("summarize.models set: %v", err)
	}
	if err := runConfigSet(ctx, []string{"summarize.language", "Japanese"}); err != nil {
		t.Fatalf("summarize.language set: %v", err)
	}
	if err := runConfigSet(ctx, []string{"summarize.interval_sec", "9"}); err == nil || !strings.Contains(err.Error(), "interval_sec") {
		t.Fatalf("an out-of-range interval must be the validator's refusal, got %v", err)
	}
	if err := runConfigSet(ctx, []string{"summarize.language", "x", "--cwd", ws}); err == nil || !strings.Contains(err.Error(), "summarize.language") {
		t.Fatalf("summarize.* with --cwd must refuse naming the key, got %v", err)
	}
	text := read()
	if !strings.Contains(text, "[summarize]\nmodels = [\"pi:a\", \"claude:b\"]\nlanguage = \"Japanese\"\n") {
		t.Fatalf("summarize table not written:\n%s", text)
	}
	if err := runConfigSet(ctx, []string{"memory_policy.alpha.record", `["a, b"]`}); err == nil || !strings.Contains(err.Error(), "--workspace") {
		t.Fatalf("a memory_policy write without a path must refuse, got %v", err)
	}
	if err := runConfigSet(ctx, []string{"memory_policy.gamma.record", `["x"]`, "--workspace", ws}); err == nil || !strings.Contains(err.Error(), "gamma") {
		t.Fatalf("a foreign project entry must be the validator's refusal, got %v", err)
	}
	if err := runConfigSet(ctx, []string{"memory_policy.alpha.record", `["a, b"]`, "--workspace", ws}); err != nil {
		t.Fatalf("memory_policy set: %v", err)
	}
	if err := runConfigSet(ctx, []string{"memory_policy.default.skip", `[]`, "--cwd", ws}); err != nil {
		t.Fatalf("memory_policy set via --cwd: %v", err)
	}
	cfg, err := LoadFileConfig(ctx.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MemoryPolicy[ws]["alpha"].Record; len(got) != 1 || got[0] != "a, b" {
		t.Fatalf("alpha record = %#v", got)
	}
	before := read()
	if err := runConfigUnset(ctx, []string{"memory_policy.alpha.record", "--workspace", ws}); err != nil {
		t.Fatalf("memory_policy unset: %v", err)
	}
	after := read()
	if strings.Contains(after, "a, b") || !strings.Contains(after, "language = \"Japanese\"") || !strings.Contains(after, ".default]\nskip = []") {
		t.Fatalf("unset must remove exactly the alpha entry:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(ws, ".polyforge.yaml")); !os.IsNotExist(err) {
		t.Fatalf("a cli.toml write touched the workspace yaml (stat err %v)", err)
	}
}

// cellScopeFixture writes a Host file and one named Cell file under a
// scratch XDG config dir, with no daemon (the socket names nothing).
func cellScopeFixture(t *testing.T) (dir string) {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("PF_DAEMON_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	t.Setenv("PF_PROFILE", "")
	dir = filepath.Join(xdg, "polyforge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cli.toml"), []byte("server = \"http://hub-d:1\"\napi_key = \"kd\"\n\n[daemon]\nmax_concurrent_jobs = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cli-alpha.toml"), []byte("server = \"http://hub-a:1\"\napi_key = \"ka\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runPfCode(argv ...string) (int, string, string) {
	var stdout, stderr strings.Builder
	code := Run(argv, nil, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestConfigCellScope: a named Cell's writes land in its own file, a
// Host-only key under it is refused naming the file and the --host route,
// --host takes Host-only keys only, and --host with --profile is refused.
func TestConfigCellScope(t *testing.T) {
	dir := cellScopeFixture(t)
	hostBefore, _ := os.ReadFile(filepath.Join(dir, "cli.toml"))

	if code, out, errOut := runPfCode("--profile", "alpha", "config", "set", "cell_max_concurrent_jobs", "1"); code != 0 {
		t.Fatalf("named Cell write: exit %d (%s%s)", code, out, errOut)
	}
	alpha, _ := os.ReadFile(filepath.Join(dir, "cli-alpha.toml"))
	if !strings.Contains(string(alpha), "cell_max_concurrent_jobs = 1") {
		t.Fatalf("the write did not land in cli-alpha.toml:\n%s", alpha)
	}
	if code, out, errOut := runPfCode("--profile", "alpha", "config", "set", "model.source", "shared-default"); code != 0 {
		t.Fatalf("model.source: exit %d (%s%s)", code, out, errOut)
	}
	alpha, _ = os.ReadFile(filepath.Join(dir, "cli-alpha.toml"))
	if !strings.Contains(string(alpha), "[model]") || !strings.Contains(string(alpha), `source = "shared-default"`) {
		t.Fatalf("[model] source not written:\n%s", alpha)
	}

	code, _, errOut := runPfCode("--profile", "alpha", "config", "set", "max_concurrent_jobs", "9")
	if code == 0 || !strings.Contains(errOut, "Host-only") || !strings.Contains(errOut, "cli-alpha.toml") || !strings.Contains(errOut, "--host") {
		t.Fatalf("a Host-only key under a named Cell must be refused naming the file and --host: exit %d %q", code, errOut)
	}
	if code, _, errOut := runPfCode("config", "set", "server", "http://x:1", "--host"); code == 0 || !strings.Contains(errOut, "Cell key") {
		t.Fatalf("--host with a Cell key: exit %d %q", code, errOut)
	}
	if code, _, errOut := runPfCode("--profile", "alpha", "config", "get", "server", "--host"); code == 0 || !strings.Contains(errOut, "contradictory") {
		t.Fatalf("--host with --profile: exit %d %q", code, errOut)
	}
	if hostAfter, _ := os.ReadFile(filepath.Join(dir, "cli.toml")); string(hostAfter) != string(hostBefore) {
		t.Fatalf("a refused or Cell-scoped write changed cli.toml:\n%s", hostAfter)
	}
	if code, out, errOut := runPfCode("config", "set", "max_concurrent_jobs", "3", "--host"); code != 0 {
		t.Fatalf("--host Host key write: exit %d (%s%s)", code, out, errOut)
	}
	if hostAfter, _ := os.ReadFile(filepath.Join(dir, "cli.toml")); !strings.Contains(string(hostAfter), "max_concurrent_jobs = 3") {
		t.Fatalf("--host did not write cli.toml:\n%s", hostAfter)
	}
	// list follows the scope: a named Cell lists Cell keys only.
	code, out, _ := runPfCode("--profile", "alpha", "--json", "config", "list")
	var doc struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
	}
	_ = json.Unmarshal([]byte(out), &doc)
	keys := map[string]bool{}
	for _, it := range doc.Items {
		keys[it.Key] = true
	}
	if code != 0 || keys["max_concurrent_jobs"] || !keys["cell_max_concurrent_jobs"] {
		t.Fatalf("named Cell list must carry Cell keys only: exit %d %s", code, out)
	}
}

// TestConfigValidateWholeHostSet: validate checks every file, isolating an
// invalid named Cell (reported, exit 1) from the valid Host file.
func TestConfigValidateWholeHostSet(t *testing.T) {
	dir := cellScopeFixture(t)
	if code, out, errOut := runPfCode("config", "validate"); code != 0 {
		t.Fatalf("a valid set: exit %d (%s%s)", code, out, errOut)
	}
	if err := os.WriteFile(filepath.Join(dir, "cli-beta.toml"), []byte("server = \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runPfCode("config", "validate")
	if code == 0 || !strings.Contains(out, "cli-beta.toml") || !strings.Contains(out, "config ok: "+filepath.Join(dir, "cli.toml")) {
		t.Fatalf("an invalid named Cell must be reported beside the valid Host file: exit %d %s", code, out)
	}
}

// TestStepsWriteRefusesAStaleKey: `pf config set`
// never writes a key no installed step carries — it would govern nothing —
// naming the rewrite (the retired verify-judge key: the dispatching step's
// id); unsetting one a file already carries is allowed (the doctor's
// warning names it).
func TestStepsWriteRefusesAStaleKey(t *testing.T) {
	f, path := stepsWriteFixture(t)
	sha, raw := fileHash(t, path)
	for _, key := range []string{"steps.alpha.verify-judge.models", "steps.alpha.gril.models"} {
		code, _, stderr := runPf(t, f.env, "config", "set", key, `["pi:x"]`, "--cwd", f.ws, "--expect-hash", sha)
		if code == 0 || !strings.Contains(stderr, "no step of an installed lane") || !strings.Contains(stderr, "docs/upgrade.md") {
			t.Fatalf("%s: want the stale-key refusal, got %d %s", key, code, stderr)
		}
		if _, now := fileHash(t, path); string(now) != string(raw) {
			t.Fatalf("%s: a refused write changed the file", key)
		}
	}
	stale := strings.Replace(string(raw), "    code: { models: [\"pi:c\"] }\n", "    code: { models: [\"pi:c\"] }\n    verify-judge: { models: [\"pi:j\"] }\n", 1)
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	sha, _ = fileHash(t, path)
	if code, _, stderr := runPf(t, f.env, "config", "unset", "steps.alpha.verify-judge.models", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("unsetting a stale key: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); strings.Contains(string(now), "verify-judge") {
		t.Fatalf("the stale key was not removed:\n%s", now)
	}
}
