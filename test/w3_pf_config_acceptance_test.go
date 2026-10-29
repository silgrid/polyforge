package test

// w3_pf_config_acceptance_test.go: end-to-end acceptance for
// `pf config` (the single cli.toml write/validate authority) and the
// daemon's per-key hot reload (live / next-claim / restart-only), authored
// against artifacts/spec.md BEFORE the implementation lands.
//
// Every scenario drives only the project's real faces: the real pf binary,
// a real `pf daemon` process on its own UDS, the cli.toml file, the real
// filesystem and (for the mapping/held-job lanes) a real aihub + fakepi.
// No implementation internals are asserted.
//
// Baseline posture: `pf config` does not exist at authoring time, so every
// scenario is an assertion-level red (an unknown-command exit, a missing
// pending-restart report or a missing live apply), never a build red — this
// file compiles and vets against the tree as it stands today.
//
// The atomicity scenario arms the repo's existing crashpoint mechanism
// (PF_CRASH_AT, internal/polyforge/crashpoint) with the two points the plan
// pins for the config writer: config.before-rename / config.after-rename.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// w3PF runs the real pf binary once with an optional stdin payload and
// returns (combined output, exit code) with NO assertion on the code. A
// process that dies on a signal (the crashpoint scenarios) reports -1.
func w3PF(t *testing.T, inst *v1Install, e w113Env, stdin string, args ...string) (string, int) {
	t.Helper()
	pfPath := filepath.Join(v1Bins(t), "pf")
	if inst != nil && inst.pfPath != "" {
		pfPath = inst.pfPath
	}
	if e.home == "" {
		e.home = t.TempDir()
	}
	xdg := filepath.Join(e.home, ".config")
	if err := os.MkdirAll(xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + e.home,
		"XDG_CONFIG_HOME=" + xdg,
	}
	if !e.noConn && inst != nil {
		env = append(env,
			"PF_SERVER="+inst.url,
			"PF_API_KEY="+inst.adminKey,
			"PF_USER_ID=usr_root",
		)
	}
	if e.socket != "" {
		env = append(env, "PF_DAEMON_SOCKET="+e.socket)
	}
	env = append(env, e.extra...)
	argv := make([]string, 0, len(args)+4)
	if e.config != "" {
		argv = append(argv, "--config", e.config)
	}
	if e.json {
		argv = append(argv, "--json")
	}
	argv = append(argv, args...)
	cmd := exec.Command(pfPath, argv...)
	cmd.Env = env
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

// w3ConfigPath writes a valid cli.toml (server + one [workspace] section per
// entry) at dir/cli.toml and returns its path. The mapping is mandatory: the
// shared validator refuses a write that would leave the file daemon-invalid.
func w3ConfigPath(t *testing.T, dir, server, apiKey string, ws []w113WS) string {
	t.Helper()
	return w113ConfigFile(t, filepath.Join(dir, "cli.toml"), server, apiKey, "", ws)
}

// w3NewDaemon prepares a daemon whose --config is a valid cli.toml carrying
// the given mapping; with inst==nil the connection settings are a dead
// loopback server (the daemon never pings the hub at startup).
func w3NewDaemon(t *testing.T, inst *v1Install, ws []w113WS, args ...string) (*w113Daemon, string) {
	t.Helper()
	server, apiKey := "http://127.0.0.1:1", "w3key"
	if inst != nil {
		server, apiKey = inst.url, inst.adminKey
	}
	cfg := w3ConfigPath(t, t.TempDir(), server, apiKey, ws)
	opts := []w113DOpt{w113DOptConfig(cfg)}
	if len(args) > 0 {
		opts = append(opts, w113DOptArgs(args...))
	}
	return w113NewDaemon(t, inst, opts...), cfg
}

// w3Status reads GET /status off a daemon socket.
func w3Status(t *testing.T, sock string) cli.StatusResponse {
	t.Helper()
	raw, err := cli.NewDaemonClient(sock).Status()
	if err != nil {
		t.Fatalf("daemon status on %s: %v", sock, err)
	}
	var st cli.StatusResponse
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("status undecodable: %v (%s)", err, raw)
	}
	return st
}

// w3SHA is the sha256 of path (the atomicity oracle's fact).
func w3SHA(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// w3DiffLines answers how many lines differ between a and b, failing if the
// line count itself moved (the byte-preserving contract keeps line count).
func w3DiffLines(t *testing.T, a, b string) int {
	t.Helper()
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(al) != len(bl) {
		t.Fatalf("line count changed: %d -> %d", len(al), len(bl))
	}
	n := 0
	for i := range al {
		if al[i] != bl[i] {
			n++
		}
	}
	return n
}

// w3TreeContains walks dir for the first file whose bytes contain needle
// (paths in skip are ignored — e.g. the config file itself, which legitimately
// holds an api_key).
func w3TreeContains(t *testing.T, dir, needle string, skip ...string) string {
	t.Helper()
	skipSet := map[string]bool{}
	for _, s := range skip {
		skipSet[s] = true
	}
	found := ""
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || skipSet[path] {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr == nil && strings.Contains(string(raw), needle) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// ---------------------------------------------------------------------------
// R1/R9 — the command family, effective value + source, whole-section edits.
// ---------------------------------------------------------------------------

// TestW3ConfigCommandFamilyAndSource pins R1: get/set/unset/list/validate
// exist, report the effective value with its source (flag|env|file|default),
// edit a whole [workspace] section as one unit, and never emit api_key.
func TestW3ConfigCommandFamilyAndSource(t *testing.T) {
	ws1, ws2 := t.TempDir(), t.TempDir()
	home := t.TempDir()
	cfg := w3ConfigPath(t, home, "http://old", "SENTINELKEY", []w113WS{
		{key: ws1, projects: []string{"e2e"}},
		{key: ws2, projects: []string{"otherproj"}},
	})
	env := w113Env{home: home, config: cfg}

	if out, code := w3PF(t, nil, env, "", "config", "set", "server", "http://new"); code != 0 {
		t.Fatalf("config set server: exit %d (%q)", code, out)
	}
	out, code := w3PF(t, nil, env, "", "config", "get", "server")
	if code != 0 || !strings.Contains(out, "http://new") || !strings.Contains(strings.ToLower(out), "file") {
		t.Fatalf("config get server must report the new value with source file; got %q (exit %d)", out, code)
	}
	list, code := w3PF(t, nil, env, "", "config", "list")
	if code != 0 || !strings.Contains(list, "server") || !strings.Contains(list, "http://new") {
		t.Fatalf("config list must carry every key with its effective value; got %q (exit %d)", list, code)
	}
	if strings.Contains(list, "SENTINELKEY") {
		t.Fatalf("config list emitted the api_key value:\n%s", list)
	}

	// env beats the file; the flag beats the env.
	envOut, _ := w3PF(t, nil, w113Env{home: home, config: cfg, extra: []string{"PF_SERVER=http://fromenv"}},
		"", "config", "get", "server")
	if !strings.Contains(envOut, "http://fromenv") || !strings.Contains(strings.ToLower(envOut), "env") {
		t.Fatalf("config get must report the env source; got %q", envOut)
	}
	flagOut, _ := w3PF(t, nil, env, "", "--server", "http://fromflag", "config", "get", "server")
	if !strings.Contains(flagOut, "http://fromflag") || !strings.Contains(strings.ToLower(flagOut), "flag") {
		t.Fatalf("config get must report the flag source; got %q", flagOut)
	}

	// unset removes the line so the built-in default applies.
	if out, code := w3PF(t, nil, env, "", "config", "unset", "server"); code != 0 {
		t.Fatalf("config unset server: exit %d (%q)", code, out)
	}
	defOut, _ := w3PF(t, nil, env, "", "config", "get", "server")
	if !strings.Contains(strings.ToLower(defOut), "default") {
		t.Fatalf("config get after unset must report source default; got %q", defOut)
	}

	// Whole [workspace] section as one unit.
	if out, code := w3PF(t, nil, env, "", "config", "set", `[workspace."`+ws1+`"]`, "projects=e2e,demo"); code != 0 {
		t.Fatalf("config set [workspace]: exit %d (%q)", code, out)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `[workspace."`+ws1+`"]`) ||
		!strings.Contains(string(raw), `"e2e"`) || !strings.Contains(string(raw), `"demo"`) {
		t.Fatalf("the whole section was not replaced as a unit:\n%s", raw)
	}
	if out, code := w3PF(t, nil, env, "", "config", "unset", `[workspace."`+ws1+`"]`); code != 0 {
		t.Fatalf("config unset [workspace]: exit %d (%q)", code, out)
	}
	raw, err = os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `[workspace."`+ws1+`"]`) {
		t.Fatalf("the whole section was not removed:\n%s", raw)
	}
	if !strings.Contains(string(raw), `[workspace."`+ws2+`"]`) {
		t.Fatalf("unsetting one section damaged another:\n%s", raw)
	}
}

// ---------------------------------------------------------------------------
// R2 — byte preservation and validate-without-writing.
// ---------------------------------------------------------------------------

// TestW3ConfigWritePreservesBytes pins R2 (comments, blank lines and the
// order of every other key survive a set) and R1's validate-without-writing.
func TestW3ConfigWritePreservesBytes(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := filepath.Join(home, "cli.toml")
	orig := "# install template comment\n" +
		"\n" +
		"server = \"http://old\"\n" +
		"# keep this operator comment\n" +
		"user_id = \"usr_root\"\n" +
		"\n" +
		"[workspace.\"" + ws + "\"]\n" +
		"projects = [\"e2e\"]\n"
	if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	env := w113Env{home: home, config: cfg}

	// validate reports without writing: bytes and mtime unchanged.
	before, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	beforeSHA := w3SHA(t, cfg)
	if out, code := w3PF(t, nil, env, "", "config", "validate"); code != 0 {
		t.Fatalf("config validate on a valid file: exit %d (%q)", code, out)
	}
	after, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if w3SHA(t, cfg) != beforeSHA || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("config validate wrote the file (sha or mtime changed)")
	}

	// a single set changes exactly the target line.
	if out, code := w3PF(t, nil, env, "", "config", "set", "user_id", "someone"); code != 0 {
		t.Fatalf("config set user_id: exit %d (%q)", code, out)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if n := w3DiffLines(t, orig, got); n != 1 {
		t.Fatalf("a set changed %d lines, want exactly the target one:\n%s", n, got)
	}
	for _, keep := range []string{"# install template comment", "# keep this operator comment",
		`[workspace."` + ws + `"]`, `projects = ["e2e"]`, `server = "http://old"`} {
		if !strings.Contains(got, keep) {
			t.Fatalf("a byte-preserving set dropped %q:\n%s", keep, got)
		}
	}
	if !strings.Contains(got, `user_id = "someone"`) {
		t.Fatalf("the target line was not updated:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// R3/O1 — crash atomicity and mode.
// ---------------------------------------------------------------------------

// TestW3ConfigCrashAtomicAndMode pins O1: killed at a crashpoint around the
// rename, the file's sha256 is exactly the old or the new bytes (never a
// mix, never empty), an existing file keeps its mode, and a first write
// creates the file 0600.
func TestW3ConfigCrashAtomicAndMode(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := filepath.Join(home, "cli.toml")
	orig := "server = \"http://old\"\n\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	env := w113Env{home: home, config: cfg}

	// The new bytes, learned through an ordinary successful write.
	if out, code := w3PF(t, nil, env, "", "config", "set", "server", "http://new"); code != 0 {
		t.Fatalf("config set server: exit %d (%q)", code, out)
	}
	newSHA := w3SHA(t, cfg)
	fi, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("an existing file's mode changed to %o, want 644", fi.Mode().Perm())
	}

	for _, point := range []string{"config.before-rename", "config.after-rename"} {
		if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(cfg, 0o644); err != nil {
			t.Fatal(err)
		}
		oldSHA := w3SHA(t, cfg)
		out, code := w3PF(t, nil, w113Env{home: home, config: cfg,
			extra: []string{"PF_CRASH_AT=" + point}}, "", "config", "set", "server", "http://new")
		if code == 0 {
			t.Fatalf("PF_CRASH_AT=%s: the process did not die at the point (output %q)", point, out)
		}
		sha := w3SHA(t, cfg)
		if sha != oldSHA && sha != newSHA {
			t.Fatalf("PF_CRASH_AT=%s: sha256 is neither old nor new bytes (mix/empty): %s", point, sha)
		}
		if point == "config.before-rename" && sha != oldSHA {
			t.Fatalf("the file changed before the rename: %s", sha)
		}
		if point == "config.after-rename" && sha != newSHA {
			t.Fatalf("the file did not carry the new bytes after the rename: %s", sha)
		}
	}

	// A first write (workspace section first: the mapping is what makes the
	// resulting file daemon-acceptable) creates the file 0600.
	newPath := filepath.Join(t.TempDir(), "cold", "cli.toml")
	if out, code := w3PF(t, nil, w113Env{home: home, config: newPath}, "",
		"config", "set", `[workspace."`+ws+`"]`, "projects=e2e"); code != 0 {
		t.Fatalf("first write: exit %d (%q)", code, out)
	}
	nfi, err := os.Stat(newPath)
	if err != nil {
		t.Fatalf("first write did not create the file: %v", err)
	}
	if nfi.Mode().Perm() != 0o600 {
		t.Fatalf("a newly created config file has mode %o, want 600", nfi.Mode().Perm())
	}
}

// ---------------------------------------------------------------------------
// R4 — concurrent writers serialize.
// ---------------------------------------------------------------------------

// TestW3ConfigConcurrentSetsSerialize pins R4: several concurrent `pf config
// set` processes on the same file all land (a file lock serializes the
// read-modify-write), and the final file still passes the shared validator.
func TestW3ConfigConcurrentSetsSerialize(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := w3ConfigPath(t, home, "http://old", "oldkey", []w113WS{{key: ws, projects: []string{"e2e"}}})
	pfPath := filepath.Join(v1Bins(t), "pf")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config")}

	sets := [][]string{
		{"config", "set", "server", "http://concurrent-a"},
		{"config", "set", "user_id", "concurrent-b"},
		{"config", "set", "claude_bin", "/opt/concurrent-c"},
		{"config", "set", "daemon_socket", "/tmp/concurrent-d.sock"},
	}
	cmds := make([]*exec.Cmd, len(sets))
	for i, s := range sets {
		cmd := exec.Command(pfPath, append([]string{"--config", cfg}, s...)...)
		cmd.Env = env
		if err := cmd.Start(); err != nil {
			t.Fatalf("start %v: %v", s, err)
		}
		cmds[i] = cmd
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("concurrent %v: %v", sets[i], err)
		}
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{"http://concurrent-a", `user_id = "concurrent-b"`,
		`claude_bin = "/opt/concurrent-c"`, `daemon_socket = "/tmp/concurrent-d.sock"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("a concurrent update was lost (%q missing):\n%s", want, got)
		}
	}
	if out, code := w3PF(t, nil, w113Env{home: home, config: cfg}, "", "config", "validate"); code != 0 {
		t.Fatalf("the concurrently written file does not validate: exit %d (%q)", code, out)
	}
}

// ---------------------------------------------------------------------------
// R5/O2 — shared validation: refusal, untouched, repair, independent daemon.
// ---------------------------------------------------------------------------

// TestW3ConfigSharedValidationRefusalRepairAndDaemon pins O2: an edit the
// shared validator refuses leaves the file's sha256 unchanged and names the
// key; a hand-broken file is repairable through the same path; and every
// pf-config-accepted file starts an independent daemon process.
func TestW3ConfigSharedValidationRefusalRepairAndDaemon(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := filepath.Join(home, "cli.toml")
	orig := "server = \"http://127.0.0.1:1\"\n\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	env := w113Env{home: home, config: cfg}

	// Refusal: an invalid resulting file (cap 0 is a parseDaemonTable
	// refusal) is not written.
	beforeSHA := w3SHA(t, cfg)
	out, code := w3PF(t, nil, env, "", "config", "set", "max_concurrent_jobs", "0")
	if code == 0 {
		t.Fatalf("an invalid write was accepted: %q", out)
	}
	if !strings.Contains(out, "max_concurrent_jobs") {
		t.Fatalf("the refusal must name the offending key; got %q", out)
	}
	if w3SHA(t, cfg) != beforeSHA {
		t.Fatalf("a refused write changed the file")
	}

	// Repair: a hand-broken target line is rewritten through pf config.
	if err := os.WriteFile(cfg, []byte("server = http://broken\n\n[workspace.\""+ws+"\"]\nprojects = [\"e2e\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := w3PF(t, nil, env, "", "config", "set", "server", "http://127.0.0.1:1"); code != 0 {
		t.Fatalf("repairing an invalid file failed: exit %d (%q)", code, out)
	}
	if out, code := w3PF(t, nil, env, "", "config", "validate"); code != 0 {
		t.Fatalf("the repaired file does not validate: exit %d (%q)", code, out)
	}

	// A cold file produced entirely through pf config starts an independent
	// daemon process.
	coldHome := t.TempDir()
	cold := filepath.Join(coldHome, ".config", "polyforge", "cli.toml")
	coldEnv := w113Env{home: coldHome, config: cold}
	if out, code := w3PF(t, nil, coldEnv, "", "config", "set", `[workspace."`+ws+`"]`, "projects=e2e"); code != 0 {
		t.Fatalf("cold set [workspace]: exit %d (%q)", code, out)
	}
	if out, code := w3PF(t, nil, coldEnv, "coldkey\n", "config", "set", "api_key"); code != 0 {
		t.Fatalf("cold set api_key: exit %d (%q)", code, out)
	}
	d := w113NewDaemon(t, nil, w113DOptConfig(cold))
	d.start()
	if _, err := os.Stat(d.sock); err != nil {
		t.Fatalf("an independent daemon did not serve on a pf-config-accepted file: %v", err)
	}
}

// ---------------------------------------------------------------------------
// R6/O3 — api_key is write-only.
// ---------------------------------------------------------------------------

// TestW3ConfigAPIKeyWriteOnly pins O3: a non-tty stdin pipe is accepted with
// no prompt; a value on argv is refused; and the sentinel occurs nowhere but
// cli.toml (stdout, stderr, --json, daemon log, events log, workspace).
func TestW3ConfigAPIKeyWriteOnly(t *testing.T) {
	const sentinel = "w3sekretSENTINELvalue"
	ws := t.TempDir()
	d, cfg := w3NewDaemon(t, nil, []w113WS{{key: ws, projects: []string{"e2e"}}})
	d.start()

	out, code := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, sentinel+"\n",
		"config", "set", "api_key")
	if code != 0 {
		t.Fatalf("piped api_key set: exit %d (%q)", code, out)
	}
	if strings.Contains(out, sentinel) {
		t.Fatalf("the value leaked to the command output:\n%s", out)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), sentinel) {
		t.Fatalf("the piped value was not written to cli.toml:\n%s", raw)
	}

	// argv is refused and never echoed.
	argvOut, argvCode := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", "api_key", sentinel+"ARGV")
	if argvCode == 0 {
		t.Fatalf("an api_key value on argv was accepted: %q", argvOut)
	}
	if strings.Contains(argvOut, sentinel) {
		t.Fatalf("the refused argv value leaked:\n%s", argvOut)
	}

	// get/list/--json report only set/unset + source.
	for _, args := range [][]string{{"config", "list"}, {"config", "get", "api_key"}} {
		if got, _ := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "", args...); strings.Contains(got, sentinel) {
			t.Fatalf("pf %v emitted the value:\n%s", args, got)
		}
	}
	if got, _ := w3PF(t, nil, w113Env{config: cfg, socket: d.sock, json: true}, "", "config", "list"); strings.Contains(got, sentinel) {
		t.Fatalf("pf --json config list emitted the value:\n%s", got)
	}

	// The sentinel never reaches the daemon log, the events log or any
	// capsule/workspace file.
	if p := w3TreeContains(t, d.logPath, sentinel); p != "" {
		t.Fatalf("the api_key sentinel reached the daemon log at %s", p)
	}
	if p := w3TreeContains(t, filepath.Join(d.dataDir, "events"), sentinel); p != "" {
		t.Fatalf("the api_key sentinel reached the events log at %s", p)
	}
	if p := w3TreeContains(t, ws, sentinel, cfg); p != "" {
		t.Fatalf("the api_key sentinel reached the workspace at %s", p)
	}
}

// ---------------------------------------------------------------------------
// R7 — step agents are refused the whole family.
// ---------------------------------------------------------------------------

// TestW3ConfigStepAgentRefused pins R7: a caller in a step agent's process
// tree is refused every pf config subcommand (DAEMON-403 convention).
func TestW3ConfigStepAgentRefused(t *testing.T) {
	ws := t.TempDir()
	cfg := w3ConfigPath(t, t.TempDir(), "http://127.0.0.1:1", "", []w113WS{{key: ws, projects: []string{"e2e"}}})
	stepEnv := w113Env{config: cfg, extra: []string{"PF_WI_ID=wi_step", "PF_DAEMON_ID=daemon_step"}}
	for _, args := range [][]string{
		{"config", "get", "server"},
		{"config", "list"},
		{"config", "validate"},
		{"config", "set", "server", "http://x"},
	} {
		out, code := w3PF(t, nil, stepEnv, "", args...)
		if code == 0 {
			t.Fatalf("a step agent reached pf %v: %q", args, out)
		}
		if !strings.Contains(out, "DAEMON-403") && !strings.Contains(strings.ToLower(out), "refused") {
			t.Fatalf("pf %v inside a step must carry the step-origin guard refusal; got %q", args, out)
		}
	}
}

// ---------------------------------------------------------------------------
// R8/R9/R10/R15/O4a — live cap applies without restart; classes/outcomes.
// ---------------------------------------------------------------------------

// TestW3ConfigReloadLiveCapAndOutcomes pins O4a: a max_concurrent_jobs reload
// is applied live (daemon PID unchanged, GET /status and `pf ops status`
// show the new cap), the per-key outcome is rendered in text and --json, and
// each reloadable key reports its class (live / next-claim / restart-only).
func TestW3ConfigReloadLiveCapAndOutcomes(t *testing.T) {
	ws := t.TempDir()
	d, cfg := w3NewDaemon(t, nil, []w113WS{{key: ws, projects: []string{"e2e"}}})
	d.start()
	pid := d.cmd.Process.Pid
	if d.cmd.Process == nil {
		t.Fatal("daemon process is gone")
	}

	out, code := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", "max_concurrent_jobs", "3")
	if code != 0 {
		t.Fatalf("config set max_concurrent_jobs: exit %d (%q)", code, out)
	}
	if !strings.Contains(strings.ToLower(out), "applied") && !strings.Contains(strings.ToLower(out), "live") {
		t.Fatalf("the per-key outcome must report the live class; got %q", out)
	}
	if d.cmd.Process.Pid != pid {
		t.Fatalf("the daemon PID changed across a live reload (%d -> %d)", pid, d.cmd.Process.Pid)
	}
	if got := w3Status(t, d.sock).Queue.MaxConcurrent; got != 3 {
		t.Fatalf("GET /status cap = %d, want the reloaded 3", got)
	}
	opsOut, opsCode := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "", "ops", "status")
	if opsCode != 0 || !strings.Contains(opsOut, "max: 3") {
		t.Fatalf("pf ops status must show the new cap; got %q (exit %d)", opsOut, opsCode)
	}

	// --json carries the same per-key outcome.
	jout, jcode := w3PF(t, nil, w113Env{config: cfg, socket: d.sock, json: true}, "",
		"config", "set", "max_concurrent_jobs", "2")
	if jcode != 0 {
		t.Fatalf("config set --json: exit %d (%q)", jcode, jout)
	}
	if !json.Valid([]byte(w3JSONDocOf(jout))) || !strings.Contains(jout, "max_concurrent_jobs") {
		t.Fatalf("--json must carry the per-key outcome document; got %q", jout)
	}

	// Classes: restart-only for server, next-claim for a [workspace] edit.
	sout, scode := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", "server", "http://127.0.0.1:2")
	if scode != 0 {
		t.Fatalf("config set server: exit %d (%q)", scode, sout)
	}
	slow := strings.ToLower(sout)
	if !strings.Contains(slow, "restart") && !strings.Contains(slow, "pending") {
		t.Fatalf("server is restart-only and must be reported so; got %q", sout)
	}
	wout, wcode := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", `[workspace."`+ws+`"]`, "projects=e2e")
	if wcode != 0 {
		t.Fatalf("config set [workspace]: exit %d (%q)", wcode, wout)
	}
	if !strings.Contains(strings.ToLower(wout), "next") {
		t.Fatalf("[workspace] is next-claim and must be reported so; got %q", wout)
	}
	// The restart-only change must not have taken the cap with it.
	if got := w3Status(t, d.sock).Queue.MaxConcurrent; got != 2 {
		t.Fatalf("cap moved after a restart-only reload: %d, want 2", got)
	}
}

// w3JSONDocOf trims surrounding text off a CLI --json output.
func w3JSONDocOf(s string) string {
	return w113JSONDocOf(s)
}

// ---------------------------------------------------------------------------
// R11/O4b — a mapping addition is live at the next claim (DAEMON-414 gone).
// ---------------------------------------------------------------------------

// TestW3ConfigReloadMappingAdditionWithoutRestart pins O4b: with the
// workspace yaml declaring a project the start-time mapping lacks, a claim
// is refused DAEMON-414; after a reload adds the project's mapping, claims
// for both the existing and the added project dispatch with the daemon PID
// unchanged.
func TestW3ConfigReloadMappingAdditionWithoutRestart(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "w3new")
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n"+
		"  e2e:\n    default: { models: [pi:glm-5.3-flash] }\n"+
		"  w3new:\n    default: { models: [pi:glm-5.3-flash] }\n")
	d, cfg := w3NewDaemon(t, inst, []w113WS{{key: ws, projects: []string{"e2e"}}})
	d.start()
	pid := d.cmd.Process.Pid

	// Pre-reload: the yaml declares w3new, the startup mapping lacks it.
	pre := w113NewWI(t, inst, "e2e", "w3 o4b pre-reload", envelopeAutopilot())
	preOut, preCode := w3PF(t, inst, w113Env{config: cfg, socket: d.sock}, "", "submit", pre)
	if preCode == 0 {
		t.Fatalf("a claim over a yaml-declared-but-unmapped project was accepted: %q", preOut)
	}
	if !strings.Contains(preOut, "DAEMON-414") {
		t.Fatalf("the pre-reload refusal must be the DAEMON-414 mapping yaml check; got %q", preOut)
	}

	// The reload adds the project: next-claim, no restart.
	if out, code := w3PF(t, inst, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", `[workspace."`+ws+`"]`, "projects=e2e,w3new"); code != 0 {
		t.Fatalf("config set mapping: exit %d (%q)", code, out)
	}

	wiA := w113NewWI(t, inst, "e2e", "w3 o4b e2e claim", envelopeAutopilot())
	d.pfWant(inst, -1, "submit", wiA)
	capA := w113Capsule(t, inst, ws, wiA)
	w113WaitCapsule(t, capA, 90*time.Second)
	w113WaitCompleted(t, capA, 120*time.Second)

	wiB := w113NewWI(t, inst, "w3new", "w3 o4b added-project claim", envelopeAutopilot())
	d.pfWant(inst, -1, "submit", wiB)
	capB := w113Capsule(t, inst, ws, wiB)
	w113WaitCapsule(t, capB, 90*time.Second)
	w113WaitCompleted(t, capB, 120*time.Second)

	if d.cmd.Process.Pid != pid {
		t.Fatalf("the daemon restarted across the mapping reload (%d -> %d)", pid, d.cmd.Process.Pid)
	}
}

// ---------------------------------------------------------------------------
// R12 — removal/re-route with an in-flight job is refused.
// ---------------------------------------------------------------------------

// TestW3ConfigReloadRemovalInFlightRefused pins R12: re-routing a project
// that has an in-flight (held) job is refused, the file is untouched and the
// affected work item is listed; a pure addition succeeds.
func TestW3ConfigReloadRemovalInFlightRefused(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	d, cfg := w3NewDaemon(t, inst, []w113WS{{key: ws, projects: []string{"e2e"}}})
	d.start()

	wi := w113NewWI(t, inst, "e2e", "w3 r12 held job", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": "q12",
			"question": "hold this job open"},
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	d.pfWant(inst, -1, "submit", wi)
	waitUntil(t, 90*time.Second, "the r12 job never became held (needs_input)", func() bool {
		for _, j := range w3Status(t, d.sock).Jobs {
			if j.WorkItemID == wi && j.NeedsInput {
				return true
			}
		}
		return false
	})

	// A pure addition is always accepted.
	if out, code := w3PF(t, inst, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", `[workspace."`+ws+`"]`, "projects=e2e,w3other"); code != 0 {
		t.Fatalf("a pure mapping addition was refused: exit %d (%q)", code, out)
	}

	// A re-route that strands the held e2e job is refused, names the job and
	// leaves the file untouched.
	beforeSHA := w3SHA(t, cfg)
	out, code := w3PF(t, inst, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", `[workspace."`+ws+`"]`, "projects=w3other")
	if code == 0 {
		t.Fatalf("re-routing a project with an in-flight job was accepted: %q", out)
	}
	if !strings.Contains(out, wi) {
		t.Fatalf("the refusal must name the affected work item %s; got %q", wi, out)
	}
	if w3SHA(t, cfg) != beforeSHA {
		t.Fatalf("a refused re-route changed the file")
	}
}

// TestW3ConfigReloadRemovalWithoutInFlight pins R12's second scenario: a
// mapping removal with NO in-flight job succeeds, leaves the daemon
// running, and takes effect at the next claim (a claim for the removed
// project is refused DAEMON-414).
func TestW3ConfigReloadRemovalWithoutInFlight(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "w3new")
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n"+
		"  e2e:\n    default: { models: [pi:glm-5.3-flash] }\n"+
		"  w3new:\n    default: { models: [pi:glm-5.3-flash] }\n")
	d, cfg := w3NewDaemon(t, inst, []w113WS{{key: ws, projects: []string{"e2e", "w3new"}}})
	d.start()
	pid := d.cmd.Process.Pid

	if out, code := w3PF(t, inst, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", `[workspace."`+ws+`"]`, "projects=e2e"); code != 0 {
		t.Fatalf("removing a project with no in-flight job was refused: exit %d (%q)", code, out)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `projects = ["e2e"]`) {
		t.Fatalf("the removal did not land in the file:\n%s", raw)
	}
	if d.cmd.Process.Pid != pid {
		t.Fatalf("the daemon restarted across the removal (%d -> %d)", pid, d.cmd.Process.Pid)
	}

	// The removal took effect at the next claim: w3new is still declared
	// by the workspace yaml but no longer mapped, so the claim is refused
	// DAEMON-414 (the same check the pre-reload addition scenario uses).
	wi := w113NewWI(t, inst, "w3new", "w3 r12 removal took effect", envelopeAutopilot())
	out, code := w3PF(t, inst, w113Env{config: cfg, socket: d.sock}, "", "submit", wi)
	if code == 0 {
		t.Fatalf("a claim for the removed project was accepted: %q", out)
	}
	if !strings.Contains(out, "DAEMON-412") && !strings.Contains(out, "DAEMON-414") {
		t.Fatalf("the post-removal claim must be refused by the mapping check; got %q", out)
	}
	if !strings.Contains(out, "w3new") {
		t.Fatalf("the refusal must name the removed project; got %q", out)
	}
}

// ---------------------------------------------------------------------------
// R13/O4c — restart-only keys are pending, then apply after a restart.
// ---------------------------------------------------------------------------

// TestW3ConfigReloadRestartOnlyPending pins O4c: a server reload leaves the
// running daemon (PID and effective value) unchanged and is reported as
// pending restart in the per-key outcome and `pf ops status`; a fresh daemon
// on the same file carries the new baseline (not pending).
func TestW3ConfigReloadRestartOnlyPending(t *testing.T) {
	ws := t.TempDir()
	d, cfg := w3NewDaemon(t, nil, []w113WS{{key: ws, projects: []string{"e2e"}}})
	d.start()
	pid := d.cmd.Process.Pid

	out, code := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", "server", "http://127.0.0.1:2")
	if code != 0 {
		t.Fatalf("config set server: exit %d (%q)", code, out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "pending") && !strings.Contains(low, "restart") {
		t.Fatalf("a restart-only change must be reported pending; got %q", out)
	}
	if d.cmd.Process.Pid != pid {
		t.Fatalf("the daemon PID changed across a restart-only reload (%d -> %d)", pid, d.cmd.Process.Pid)
	}
	opsOut, _ := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "", "ops", "status")
	if !strings.Contains(opsOut, "server") ||
		(!strings.Contains(strings.ToLower(opsOut), "pending") && !strings.Contains(strings.ToLower(opsOut), "restart")) {
		t.Fatalf("pf ops status must show server as pending restart; got %q", opsOut)
	}
	// The pending set lists ONLY the changed key: a flat server change must
	// not report harness_root/operator_agent_dir pending.
	if strings.Contains(opsOut, "harness_root") || strings.Contains(opsOut, "operator_agent_dir") {
		t.Fatalf("the pending set must list only the changed key; got %q", opsOut)
	}

	// A fresh daemon loads the new value as its baseline and serves: the
	// restart-only edit applied on restart, not on the running process.
	d.stop()
	d2 := w113NewDaemon(t, nil, w113DOptConfig(cfg))
	d2.start()
	if out, code := w3PF(t, nil, w113Env{config: cfg, socket: d2.sock}, "", "ops", "status"); code != 0 {
		t.Fatalf("the restarted daemon does not serve after a restart-only edit: exit %d (%q)", code, out)
	}
}

// ---------------------------------------------------------------------------
// R14/O5 — a bad reload is inert and never stops the daemon.
// ---------------------------------------------------------------------------

// TestW3ConfigReloadBadFileInert pins O5: a hand-broken file reload reports
// an error, applies nothing, leaves the daemon PID and effective config
// unchanged, and the daemon stays up.
func TestW3ConfigReloadBadFileInert(t *testing.T) {
	ws := t.TempDir()
	d, cfg := w3NewDaemon(t, nil, []w113WS{{key: ws, projects: []string{"e2e"}}})
	d.start()
	pid := d.cmd.Process.Pid
	beforeCap := w3Status(t, d.sock).Queue.MaxConcurrent

	broken := "server = \"http://127.0.0.1:1\"\n" +
		"[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n" +
		"[daemon]\nmax_concurrent_jobs = 0\n"
	if err := os.WriteFile(cfg, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "", "config", "reload")
	if code == 0 {
		t.Fatalf("a reload of a broken file succeeded: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "error") && !strings.Contains(out, "max_concurrent_jobs") {
		t.Fatalf("the bad reload must report an error naming the key; got %q", out)
	}
	if d.cmd == nil || d.cmd.Process.Pid != pid {
		t.Fatalf("the daemon died or restarted on a bad reload")
	}
	if got := w3Status(t, d.sock).Queue.MaxConcurrent; got != beforeCap {
		t.Fatalf("a bad reload half-applied (cap %d -> %d)", beforeCap, got)
	}
	// R14: exactly one config_reload_failed event, carrying the key names
	// only (never a value).
	eventsPath := filepath.Join(d.dataDir, "events", "daemon.jsonl")
	rawEvents, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("the daemon events log is unreadable: %v", err)
	}
	failed := 0
	for _, line := range strings.Split(strings.TrimSpace(string(rawEvents)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e struct {
			Kind string `json:"kind"`
			Body struct {
				Keys []string `json:"keys"`
			} `json:"body"`
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Kind != "config_reload_failed" {
			continue
		}
		failed++
		if len(e.Body.Keys) != 1 || e.Body.Keys[0] != "max_concurrent_jobs" {
			t.Fatalf("config_reload_failed must carry the key names only; got %s", line)
		}
	}
	if failed != 1 {
		t.Fatalf("a bad reload must emit exactly one config_reload_failed event; got %d (%s)", failed, rawEvents)
	}
}

// ---------------------------------------------------------------------------
// R16 — flag override and pf ops max last-action-wins.
// ---------------------------------------------------------------------------

// TestW3ConfigReloadFlagOverrideAndOpsMax pins R16: a daemon started with an
// explicit --max-concurrent-jobs flag ignores a reloaded table value and
// reports it overridden; with no flag, reload and `pf ops max` follow the
// last action.
func TestW3ConfigReloadFlagOverrideAndOpsMax(t *testing.T) {
	wsA := t.TempDir()
	d1, cfg1 := w3NewDaemon(t, nil, []w113WS{{key: wsA, projects: []string{"e2e"}}}, "--max-concurrent-jobs", "3")
	d1.start()
	out, code := w3PF(t, nil, w113Env{config: cfg1, socket: d1.sock}, "",
		"config", "set", "max_concurrent_jobs", "9")
	if code != 0 {
		t.Fatalf("config set max_concurrent_jobs: exit %d (%q)", code, out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "overridden") && !strings.Contains(low, "flag") {
		t.Fatalf("a flag-pinned key must be reported overridden; got %q", out)
	}
	if got := w3Status(t, d1.sock).Queue.MaxConcurrent; got != 3 {
		t.Fatalf("the flag-started daemon adopted the reloaded cap: %d, want 3", got)
	}

	wsB := t.TempDir()
	d2, cfg2 := w3NewDaemon(t, nil, []w113WS{{key: wsB, projects: []string{"e2e"}}})
	d2.start()
	if out, code := w3PF(t, nil, w113Env{config: cfg2, socket: d2.sock}, "", "ops", "max", "2"); code != 0 {
		t.Fatalf("pf ops max 2: exit %d (%q)", code, out)
	}
	if got := w3Status(t, d2.sock).Queue.MaxConcurrent; got != 2 {
		t.Fatalf("pf ops max 2 not applied: %d", got)
	}
	if out, code := w3PF(t, nil, w113Env{config: cfg2, socket: d2.sock}, "",
		"config", "set", "max_concurrent_jobs", "7"); code != 0 {
		t.Fatalf("config set max_concurrent_jobs: exit %d (%q)", code, out)
	}
	if got := w3Status(t, d2.sock).Queue.MaxConcurrent; got != 7 {
		t.Fatalf("a reloaded cap must override the earlier pf ops max: %d, want 7", got)
	}
	if out, code := w3PF(t, nil, w113Env{config: cfg2, socket: d2.sock}, "", "ops", "max", "5"); code != 0 {
		t.Fatalf("pf ops max 5: exit %d (%q)", code, out)
	}
	if got := w3Status(t, d2.sock).Queue.MaxConcurrent; got != 5 {
		t.Fatalf("a later pf ops max must override the reloaded cap: %d, want 5", got)
	}
	// R16 / finding 6c: an unrelated reload must not undo pf ops max.
	if out, code := w3PF(t, nil, w113Env{config: cfg2, socket: d2.sock}, "",
		"config", "set", "server", "http://127.0.0.1:3"); code != 0 {
		t.Fatalf("config set server: exit %d (%q)", code, out)
	}
	if got := w3Status(t, d2.sock).Queue.MaxConcurrent; got != 5 {
		t.Fatalf("an unrelated reload undid pf ops max: cap %d, want 5", got)
	}
}

// TestW3ConfigReloadFlagPinnedStepTimeout pins R16 for the supervision
// bounds (finding 6a): a daemon started with an explicit --step-timeout
// keeps that T through a step_timeout reload and through an unrelated
// stall_window reload, and reports the pinned key overridden.
func TestW3ConfigReloadFlagPinnedStepTimeout(t *testing.T) {
	ws := t.TempDir()
	d, cfg := w3NewDaemon(t, nil, []w113WS{{key: ws, projects: []string{"e2e"}}}, "--step-timeout", "5m")
	d.start()
	pid := d.cmd.Process.Pid

	out, code := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", "step_timeout", "9m")
	if code != 0 {
		t.Fatalf("config set step_timeout: exit %d (%q)", code, out)
	}
	if low := strings.ToLower(out); !strings.Contains(low, "overridden") && !strings.Contains(low, "flag") {
		t.Fatalf("a flag-pinned step_timeout must be reported overridden; got %q", out)
	}
	if st := w3Status(t, d.sock).Supervision; st == nil || st.StepTimeout != "5m0s" {
		t.Fatalf("the pinned step_timeout was overwritten: %+v", st)
	}

	// An unrelated supervision reload must not lose the pin: the buggy
	// SetSupervision(path) call overwrote the pinned T with the file value.
	if out, code := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "",
		"config", "set", "stall_window", "4m"); code != 0 {
		t.Fatalf("config set stall_window: exit %d (%q)", code, out)
	}
	st := w3Status(t, d.sock).Supervision
	if st == nil {
		t.Fatalf("the daemon reports no supervision status")
	}
	if st.StepTimeout != "5m0s" {
		t.Fatalf("an unrelated reload lost the pinned step_timeout: %+v", st)
	}
	if st.StallWindow != "4m0s" {
		t.Fatalf("the unpinned stall_window did not apply: %+v", st)
	}
	if d.cmd.Process.Pid != pid {
		t.Fatalf("the daemon restarted across a pinned reload (%d -> %d)", pid, d.cmd.Process.Pid)
	}
}

// ---------------------------------------------------------------------------
// R8 — hand edits need an explicit reload; a write with no daemon reports next start.
// ---------------------------------------------------------------------------

// TestW3ConfigHandEditNeedsReload pins R8's hand-edit half: a hand edit to a
// live key alone leaves the running daemon's effective config unchanged, and
// an explicit `pf config reload` applies it (no watcher/polling).
func TestW3ConfigHandEditNeedsReload(t *testing.T) {
	ws := t.TempDir()
	d, cfg := w3NewDaemon(t, nil, []w113WS{{key: ws, projects: []string{"e2e"}}})
	d.start()
	pid := d.cmd.Process.Pid

	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(raw) + "\n[daemon]\nmax_concurrent_jobs = 6\n"
	if err := os.WriteFile(cfg, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	// The hand edit alone is inert: no watcher, no polling.
	if got := w3Status(t, d.sock).Queue.MaxConcurrent; got != 4 {
		t.Fatalf("a hand edit took effect without `pf config reload`: cap %d, want the default 4", got)
	}

	if out, code := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "", "config", "reload"); code != 0 {
		t.Fatalf("config reload: exit %d (%q)", code, out)
	}
	if got := w3Status(t, d.sock).Queue.MaxConcurrent; got != 6 {
		t.Fatalf("pf config reload did not apply the hand edit: cap %d, want 6", got)
	}
	if d.cmd.Process.Pid != pid {
		t.Fatalf("the daemon restarted across an explicit reload (%d -> %d)", pid, d.cmd.Process.Pid)
	}
}

// TestW3ConfigWriteWithoutDaemonReportsNextStart pins R8's no-daemon half: a
// write still succeeds with no running daemon and reports that it takes effect
// at the next daemon start.
func TestW3ConfigWriteWithoutDaemonReportsNextStart(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := w3ConfigPath(t, home, "http://127.0.0.1:1", "", []w113WS{{key: ws, projects: []string{"e2e"}}})

	out, code := w3PF(t, nil, w113Env{home: home, config: cfg,
		socket: filepath.Join(home, "no-daemon.sock")}, "", "config", "set", "server", "http://nextstart")
	if code != 0 {
		t.Fatalf("a write with no daemon running failed: exit %d (%q)", code, out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "next") && !strings.Contains(low, "start") {
		t.Fatalf("a no-daemon write must report that it takes effect at the next start; got %q", out)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "http://nextstart") {
		t.Fatalf("the no-daemon write did not land:\n%s", raw)
	}
}

// ---------------------------------------------------------------------------
// R17 — install.sh and the docs write through / name pf config.
// ---------------------------------------------------------------------------

// TestW3InstallAndDocsThroughPFConfig pins R17: tools/install.sh lands its
// first-install config through `pf config` (the heredoc writer is gone) and
// the docs/init skill name `pf config` as the one recommended way while the
// old "edit the table, then restart" instruction is deleted.
func TestW3InstallAndDocsThroughPFConfig(t *testing.T) {
	root := v1Root(t)
	install, err := os.ReadFile(filepath.Join(root, "tools", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(install), "config set") {
		t.Fatalf("tools/install.sh does not write its config through `pf config set`")
	}
	if strings.Contains(string(install), "CFGEOF") {
		t.Fatalf("tools/install.sh still carries the ad-hoc heredoc config writer")
	}

	for _, rel := range []string{"docs/reference/config.md", "skills/polyforge/verbs/init.md"} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		if strings.Contains(s, "Edit the table, then restart") {
			t.Fatalf("%s still teaches the old edit-then-restart path", rel)
		}
		if !strings.Contains(s, "pf config") {
			t.Fatalf("%s must name pf config as the one recommended way", rel)
		}
	}

	// The reload entry point's reachability (local UDS, step agents refused)
	// is documented where the daemon routes are. Uds-routes.md must name the
	// route and its refused caller class.
	routes, err := os.ReadFile(filepath.Join(root, "docs/reference/uds-routes.md"))
	if err != nil {
		t.Fatal(err)
	}
	rs := string(routes)
	if !strings.Contains(rs, "config/reload") && !strings.Contains(rs, "config reload") {
		t.Fatalf("docs/reference/uds-routes.md must name the reload entry point")
	}
	if !strings.Contains(strings.ToLower(rs), "step agent") && !strings.Contains(strings.ToLower(rs), "local") {
		t.Fatalf("docs/reference/uds-routes.md must state the reload entry point's refused caller class (local UDS / step agents)")
	}
}
