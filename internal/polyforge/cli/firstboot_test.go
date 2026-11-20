package cli

// firstboot_test.go — polyforge#176 (84-components-C): the first boot's
// behaviour, one function, TestFirstBoot. The local boot establishes
// every fact (the seed install, the identity, cli.toml's default Cell,
// the harness references, the phase record without a credential); an
// interrupted boot retries without duplicating the user; the external
// mode takes the given server and key and writes nothing remote; the
// Q6 gate skips a completed boot and refuses either half alone.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/polyforge/components"
)

// fbOpts builds one firstBootOpts over fresh temp dirs, local mode.
func fbOpts(t *testing.T) (firstBootOpts, string) {
	t.Helper()
	// the identity stage's init entry refuses inside a step or task
	// environment; a test binary dispatched by one must clear the
	// markers to exercise the boot itself (the refusal has its own
	// tests, internal/aihub/init_test.go)
	t.Setenv("PF_WI_ID", "")
	t.Setenv("PF_TASK_ID", "")
	home := t.TempDir()
	root := t.TempDir()
	aihubDir := t.TempDir()
	dataDir := filepath.Join(home, ".local", "share", "polyforge")
	o := firstBootOpts{
		Home:           home,
		ConfigPath:     filepath.Join(home, ".config", "polyforge", "cli.toml"),
		DataDir:        dataDir,
		AihubDataDir:   aihubDir,
		ComponentsRoot: filepath.Join(root, "components"),
		WorkspaceDir:   filepath.Join(root, "workspace"),
		SeedRoot:       filepath.Join(root, "seed"),
		AgentDir:       filepath.Join(home, ".pi", "agent"),
		RepoSlug:       "silgrid/polyforge",
		BaseVersion:    "v0.2.4-dev.1",
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
	}
	if err := os.MkdirAll(o.WorkspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return o, aihubDir
}

// fbSeed builds one complete seed at <SeedRoot>/releases/<ver>: the
// five-component tree the install verifies, with digests and manifest
// (the same shape internal/polyforge/components' own tests assemble).
func fbSeed(t *testing.T, o firstBootOpts) string {
	t.Helper()
	ver := "v0.2.4-dev.1"
	dir := filepath.Join(o.SeedRoot, "releases", ver)
	w := func(rel, body string, mode os.FileMode) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	j := func(rel string, v any) {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		w(rel, string(raw)+"\n", 0o644)
	}
	w("bin/pf", "#!/bin/sh\n", 0o755)
	j("package.json", map[string]any{"name": "polyforge", "version": ver})
	w("skills/polyforge/SKILL.md", "# the routing skill\n", 0o644)
	w("workflows/develop/README.md", "# lane\n", 0o644)
	j("pi-web/package.json", map[string]any{"name": "pi-web", "version": "0.1.0"})
	j("harness/pi/package.json", map[string]any{"name": "polyforge-harness-pi"})
	j("harness/claude/package.json", map[string]any{"name": "polyforge-harness-claude"})
	m := &components.Manifest{
		SchemaVersion: components.SchemaVersion,
		Version:       ver,
		Commit:        "test",
		Platform:      o.Platform,
		ChannelRepo:   "silgrid/polyforge",
		MinBase:       "v0.2.0",
		Components: []components.Component{
			{Name: components.ComponentPF, Path: components.PathPF, Version: ver},
			{Name: components.ComponentRelease, Path: components.PathRelease, Version: ver},
			{Name: components.ComponentPiWeb, Path: components.PathPiWeb, Version: "0.1.0"},
			{Name: components.ComponentHarnessPi, Path: components.PathHarnessPi, Version: "1.4.0"},
			{Name: components.ComponentHarnessClaude, Path: components.PathHarnessClaude, Version: "2.1.326"},
		},
	}
	if err := m.FillDigests(dir); err != nil {
		t.Fatalf("FillDigests: %v", err)
	}
	if err := components.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fbRecord reads the first-boot record.
func fbRecord(t *testing.T, o firstBootOpts) firstBootState {
	t.Helper()
	raw, err := os.ReadFile(firstBootPath(o.DataDir))
	if err != nil {
		t.Fatalf("the record: %v", err)
	}
	var st firstBootState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("the record: %v", err)
	}
	return st
}

// fbUsers counts the aihub store's users.
func fbUsers(t *testing.T, aihubDir string) int {
	t.Helper()
	db, err := core.Open(aihubDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// fbUnrevokedHashes lists the aihub store's unrevoked key hashes.
func fbUnrevokedHashes(t *testing.T, aihubDir string) map[string]bool {
	t.Helper()
	db, err := core.Open(aihubDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT key_hash FROM api_keys WHERE revoked_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatal(err)
		}
		out[h] = true
	}
	return out
}

func TestFirstBoot(t *testing.T) {
	t.Run("the local boot establishes every fact", func(t *testing.T) {
		o, aihubDir := fbOpts(t)
		fbSeed(t, o)
		var out strings.Builder
		if err := runFirstBoot(o, &out); err != nil {
			t.Fatalf("the first boot must succeed:\n%v\n%s", err, out.String())
		}
		// the seed install: current points at the seed's version
		target, err := components.CurrentTarget(o.ComponentsRoot)
		if err != nil || target != filepath.Join("releases", "v0.2.4-dev.1") {
			t.Fatalf("current -> %q (%v), want the seed's version", target, err)
		}
		// cli.toml: the default Cell (server, key, user), the engine,
		// the workspace mapping — derived from the ticket, not from the
		// implementation's output
		cfg, err := LoadFileConfig(o.ConfigPath)
		if err != nil {
			t.Fatalf("the written cli.toml must load: %v", err)
		}
		if cfg.Server != firstBootLocalServer {
			t.Fatalf("the default Cell's server = %q, want %s", cfg.Server, firstBootLocalServer)
		}
		if cfg.APIKey == "" || cfg.UserID == "" {
			t.Fatalf("the default Cell must carry the api_key and the user_id: %+v", cfg)
		}
		if cfg.Daemon.ContainerEngine != "podman" {
			t.Fatalf("container_engine = %q, want podman", cfg.Daemon.ContainerEngine)
		}
		if proj := cfg.Mapping.ProjectsOf(o.WorkspaceDir); len(proj) != 1 || proj[0] != "workstation" {
			t.Fatalf("the /workspace mapping = %v, want projects=[workstation]", proj)
		}
		// the key is the daily user's unrevoked key — and never the
		// bootstrap admin's: the identity stage ran first, so the
		// server-side bootstrap (usr_root) has nothing left to seed
		if !fbUnrevokedHashes(t, aihubDir)[core.Sha256Hex(cfg.APIKey)] {
			t.Fatalf("the default Cell's key must be the identity's unrevoked key")
		}
		if _, err := os.Stat(filepath.Join(aihubDir, "bootstrap-admin.key")); !os.IsNotExist(err) {
			t.Fatalf("bootstrap-admin.key must not exist (the identity predates the server): %v", err)
		}
		if n := fbUsers(t, aihubDir); n != 1 {
			t.Fatalf("users = %d, want exactly 1", n)
		}
		// the harness references
		pi := fbSettings(t, filepath.Join(o.AgentDir, "settings.json"))
		if pk := pi["packages"].([]any); len(pk) != 1 || pk[0] != filepath.Join(o.ComponentsRoot, "current") {
			t.Fatalf("the pi packages entry = %v, want /components/current", pk)
		}
		// the record: done, every fact named, no credential
		rec := fbRecord(t, o)
		if rec.Phase != "done" || rec.Mode != "local" || rec.DefaultCell != "default" ||
			rec.Server != firstBootLocalServer || !rec.UserReady || !rec.KeyReady || !rec.ImageManaged {
			t.Fatalf("the record = %+v", rec)
		}
		fbAssertNoCredential(t, firstBootPath(o.DataDir), cfg.APIKey)
	})

	t.Run("an interrupted boot retries without duplicating the user", func(t *testing.T) {
		o, aihubDir := fbOpts(t)
		fbSeed(t, o)
		// the config stage fails: the config path is a directory, so
		// the read inside the writer fails after identity committed
		if err := os.MkdirAll(o.ConfigPath, 0o755); err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		if err := runFirstBoot(o, &out); err == nil {
			t.Fatal("a config-stage failure must fail the boot")
		}
		rec := fbRecord(t, o)
		if rec.Phase != "config/failed" {
			t.Fatalf("the record's phase = %q, want config/failed", rec.Phase)
		}
		if rec.Error == "" {
			t.Fatal("the failed record must carry the reason")
		}
		if n := fbUsers(t, aihubDir); n != 1 {
			t.Fatalf("users = %d after the interrupted boot, want 1", n)
		}
		// the retry: the same volume, the config path fixed
		if err := os.Remove(o.ConfigPath); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err := runFirstBoot(o, &out); err != nil {
			t.Fatalf("the retry must succeed:\n%v\n%s", err, out.String())
		}
		if n := fbUsers(t, aihubDir); n != 1 {
			t.Fatalf("users = %d after the retry, want still 1 — a retry must find, never duplicate", n)
		}
		if rec := fbRecord(t, o); rec.Phase != "done" {
			t.Fatalf("the retried record's phase = %q, want done", rec.Phase)
		}
		cfg, err := LoadFileConfig(o.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if !fbUnrevokedHashes(t, aihubDir)[core.Sha256Hex(cfg.APIKey)] {
			t.Fatal("the retry's reissued key must be the unrevoked one in cli.toml")
		}
	})

	t.Run("the external mode takes the given server and key and writes nothing remote", func(t *testing.T) {
		o, aihubDir := fbOpts(t)
		fbSeed(t, o)
		hits := 0
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits++
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer stub.Close()
		o.ExtServer = stub.URL
		o.ExtKey = "the-operators-own-key"
		var out strings.Builder
		if err := runFirstBoot(o, &out); err != nil {
			t.Fatalf("the external boot must succeed:\n%v\n%s", err, out.String())
		}
		if hits != 0 {
			t.Fatalf("the external mode must make no remote call at all (the stub saw %d)", hits)
		}
		cfg, err := LoadFileConfig(o.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Server != stub.URL || cfg.APIKey != "the-operators-own-key" {
			t.Fatalf("the default Cell = %s/%s, want the given server and key", cfg.Server, cfg.APIKey)
		}
		if proj := cfg.Mapping.ProjectsOf(o.WorkspaceDir); proj != nil {
			t.Fatalf("without a project name the external mode must not map /workspace (Q4): %v", proj)
		}
		// no local identity: the aihub volume is untouched
		if _, err := os.Stat(filepath.Join(aihubDir, "aihub.db")); !os.IsNotExist(err) {
			t.Fatalf("the external mode must not open the local store: %v", err)
		}
		rec := fbRecord(t, o)
		if rec.Mode != "external" || rec.UserReady || !rec.KeyReady {
			t.Fatalf("the external record = %+v, want mode=external, userReady=false, keyReady=true", rec)
		}
		if rec.Phase != "done" {
			t.Fatalf("the record's phase = %q, want done", rec.Phase)
		}
		fbAssertNoCredential(t, firstBootPath(o.DataDir), o.ExtKey)
	})

	t.Run("an external project name maps /workspace locally", func(t *testing.T) {
		o, _ := fbOpts(t)
		fbSeed(t, o)
		o.ExtServer = "https://hub.example.invalid"
		o.ExtKey = "k"
		o.ExtProject = "existing-project"
		var out strings.Builder
		if err := runFirstBoot(o, &out); err != nil {
			t.Fatalf("the external boot with a project must succeed:\n%v\n%s", err, out.String())
		}
		cfg, err := LoadFileConfig(o.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if proj := cfg.Mapping.ProjectsOf(o.WorkspaceDir); len(proj) != 1 || proj[0] != "existing-project" {
			t.Fatalf("the /workspace mapping = %v, want the given project", proj)
		}
		if rec := fbRecord(t, o); rec.Project != "existing-project" {
			t.Fatalf("the record = %+v, want the project named", rec)
		}
	})

	t.Run("Q6: done plus the default Cell skips; either half alone refuses", func(t *testing.T) {
		o, aihubDir := fbOpts(t)
		fbSeed(t, o)
		var out strings.Builder
		if err := runFirstBoot(o, &out); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadFileConfig(o.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		firstKey := cfg.APIKey
		// the completed boot: skipped, and the first choice stands (no
		// reissue — the key in cli.toml still authenticates, the store
		// still holds it unrevoked)
		out.Reset()
		if err := runFirstBoot(o, &out); err != nil {
			t.Fatalf("a completed boot must be skipped, not re-run: %v", err)
		}
		if !strings.Contains(out.String(), "already done") {
			t.Fatalf("the skip must say so:\n%s", out.String())
		}
		cfg, err = LoadFileConfig(o.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.APIKey != firstKey {
			t.Fatal("the skip must not reissue the key — the first choice is made exactly once")
		}
		if !fbUnrevokedHashes(t, aihubDir)[core.Sha256Hex(firstKey)] {
			t.Fatal("the skipped boot must leave the first key unrevoked")
		}
		// done without the default Cell: refused, with both files named
		raw, err := os.ReadFile(o.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		fixed := strings.Replace(string(raw), "api_key = ", "# api_key = ", 1)
		if err := os.WriteFile(o.ConfigPath, []byte(fixed), 0o600); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		err = runFirstBoot(o, &out)
		if err == nil {
			t.Fatal("done without the default Cell must refuse to start")
		}
		for _, name := range []string{firstBootPath(o.DataDir), o.ConfigPath} {
			if !strings.Contains(err.Error(), name) && !strings.Contains(out.String(), name) {
				t.Fatalf("the refusal must name %s: %v / %s", name, err, out.String())
			}
		}
		// a default Cell without a record: refused too (a hand-made
		// cell is not a completed first boot)
		fresh, _ := fbOpts(t)
		if err := os.MkdirAll(filepath.Dir(fresh.ConfigPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fresh.ConfigPath, []byte("server = \"https://hub.example.invalid\"\napi_key = \"k\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runFirstBoot(fresh, &out); err == nil {
			t.Fatal("a default Cell without a done record must refuse to start")
		}
	})

	t.Run("a half-configured external mode refuses before anything runs", func(t *testing.T) {
		o, _ := fbOpts(t)
		fbSeed(t, o)
		o.ExtServer = "https://hub.example.invalid"
		var out strings.Builder
		if err := runFirstBoot(o, &out); err == nil {
			t.Fatal("a server without a key must refuse")
		}
		o.ExtServer, o.ExtKey = "", "some-key"
		if err := runFirstBoot(o, &out); err == nil {
			t.Fatal("a key without a server must refuse")
		}
		if _, err := os.Stat(firstBootPath(o.DataDir)); !os.IsNotExist(err) {
			t.Fatalf("a pre-flight refusal must publish no record: %v", err)
		}
	})

	t.Run("a not-image-managed run records the fact and still boots", func(t *testing.T) {
		o, _ := fbOpts(t)
		// no seed under SeedRoot: a dev run, not the image
		var out strings.Builder
		if err := runFirstBoot(o, &out); err == nil {
			// the harness stage refuses on a dangling current: that is
			// the honest answer on a machine with no components
			t.Fatalf("without a seed and no components current, the boot must fail at harness")
		}
		rec := fbRecord(t, o)
		if rec.Phase != "harness/failed" || rec.ImageManaged {
			t.Fatalf("the record = %+v, want harness/failed and imageManaged=false", rec)
		}
		if !strings.Contains(out.String(), "no seed") && !strings.Contains(rec.Error, "current") {
			t.Fatalf("the run must say why: %s / %s", out.String(), rec.Error)
		}
	})
}

// fbSettings reads one settings JSON.
func fbSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// fbAssertNoCredential asserts the record carries no credential: not
// the key, no 64-hex digest, no token-shaped field.
func fbAssertNoCredential(t *testing.T, path, key string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, key) {
		t.Fatalf("%s must not carry the key", path)
	}
	for _, f := range []string{"api_key", "key_hash", "token", "bearer"} {
		if strings.Contains(strings.ToLower(body), f) {
			t.Fatalf("%s must not name a credential field %q:\n%s", path, f, body)
		}
	}
	// a 64-hex digest never appears either
	for i := 0; i+64 <= len(body); i++ {
		if isHex64(body[i : i+64]) {
			t.Fatalf("%s must not carry a 64-hex digest", path)
		}
	}
}

// isHex64 answers whether s is 64 hex characters.
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
