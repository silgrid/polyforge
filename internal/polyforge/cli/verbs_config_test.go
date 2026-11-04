package cli

// verbs_config_test.go: the writer-flock order of `pf config set|unset`
// ( F1). The flock serializes the local read-modify-write; the
// daemon notification is a cross-process round trip and must run after the
// release — otherwise a slow daemon holds every other `pf config` writer
// for the client timeout.

import (
	"bytes"
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
func stepsWriteFixture(t *testing.T, caps []string) (wfFixture, string) {
	t.Helper()
	f := newWfFixture(t, "# keep me\nsteps:\n  alpha:\n    grill:\n      models: [\"pi:before\"]\n    code: { models: [\"pi:c\"] }\n", caps)
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
	f, path := stepsWriteFixture(t, []string{CapabilityStepsAttrs})
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
	f, path := stepsWriteFixture(t, []string{CapabilityStepsAttrs})
	sha, raw := fileHash(t, path)
	code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.grill.models", `["not-a-candidate"]`, "--cwd", f.ws, "--expect-hash", sha)
	if code == 0 || !strings.Contains(stderr, "steps.alpha.grill.models[0]") {
		t.Fatalf("a malformed candidate must be the loader's refusal: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != string(raw) {
		t.Fatal("a refused candidate changed the file")
	}
	for key, want := range map[string]string{
		"steps.alpha.grill.attrs.mystery_knob":          "undeclared attr",
		"steps.alpha.default.attrs.question_batch_size": "`default` takes no attrs",
		"steps.alpha.grill.attrs.question_batch_size":   "",
	} {
		value := "1"
		if want == "" {
			value = "many"
			want = "must be an integer"
		}
		code, _, stderr := runPf(t, f.env, "config", "set", key, value, "--cwd", f.ws, "--expect-hash", sha)
		if code == 0 || !strings.Contains(stderr, want) {
			t.Fatalf("%s: want a refusal naming %q, got %d %s", key, want, code, stderr)
		}
		if _, now := fileHash(t, path); string(now) != string(raw) {
			t.Fatalf("%s: a refused attr changed the file", key)
		}
	}
	code, _, stderr = runPf(t, f.env, "config", "set", "steps.alpha.grill.attrs.question_batch_size", "-1", "--cwd", f.ws, "--expect-hash", sha)
	if code == 0 || !strings.Contains(stderr, "steps.alpha.grill.attrs.question_batch_size: must be a non-negative integer") {
		t.Fatalf("a negative int attr must be the loader's refusal: %d %s", code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != string(raw) {
		t.Fatal("a refused negative attr changed the file")
	}
	if code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.grill.attrs.question_batch_size", "2", "--cwd", f.ws, "--expect-hash", sha); code != 0 {
		t.Fatalf("a declared attr write failed: %s", stderr)
	}
	doc := readWorkspaceDoc(t, f)
	if v := stepRead(t, doc, "alpha", "grill")["attrs"].(map[string]any)["question_batch_size"].(map[string]any)["value"]; v != 2.0 {
		t.Fatalf("the written attr reads back as %v", v)
	}
}

// TestStepsAttrWriteRefusedBelowCapability pins the capability gate: a
// daemon without steps_attrs refuses the write with CORE-501 naming the
// floor, before the file is touched.
func TestStepsAttrWriteRefusedBelowCapability(t *testing.T) {
	f, path := stepsWriteFixture(t, nil)
	sha, raw := fileHash(t, path)
	code, _, stderr := runPf(t, f.env, "config", "set", "steps.alpha.grill.attrs.question_batch_size", "3", "--cwd", f.ws, "--expect-hash", sha)
	if code == 0 || !strings.Contains(stderr, "CORE-501") || !strings.Contains(stderr, StepsAttrsMinimumVersion) {
		t.Fatalf("want CORE-501 naming %s: %d %s", StepsAttrsMinimumVersion, code, stderr)
	}
	if _, now := fileHash(t, path); string(now) != string(raw) {
		t.Fatal("the capability refusal changed the file")
	}
	code, _, stderr = runPf(t, f.env, "config", "unset", "steps.alpha.code.models", "--cwd", f.ws)
	if code == 0 || !strings.Contains(stderr, "CORE-501") {
		t.Fatalf("an unset is gated too: %d %s", code, stderr)
	}
}

// TestStepsChainUnsetFallsBack pins the CLI clear: the chain-only entry is
// gone and the read reports it undeclared.
func TestStepsChainUnsetFallsBack(t *testing.T) {
	f, path := stepsWriteFixture(t, []string{CapabilityStepsAttrs})
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
