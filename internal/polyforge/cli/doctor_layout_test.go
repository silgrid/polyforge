package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// doctor_layout_test.go (polyforge#174): the daemon supervision check
// (offline fail / unsupervised warn / supervised pass), the install
// layout checks (pi local release package, Claude skill symlink) and the
// v1 leftovers check with its backup-first cleanup commands. Every HOME
// is a scratch dir; tmux is a fake on PATH.

// fakeTmuxPath answers a PATH whose tmux exits with code (has-session:
// 0 = the loop session exists), followed by the ambient PATH.
func fakeTmuxPath(t *testing.T, code int) string {
	t.Helper()
	dir := t.TempDir()
	writeScript(t, dir, "tmux", "#!/bin/sh\nexit "+strconv.Itoa(code)+"\n")
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// layoutRelease creates the release checkout install.sh maintains under
// <home>/.local/share/polyforge/release/current (a link to v9).
func layoutRelease(t *testing.T, home string) string {
	t.Helper()
	rel := filepath.Join(home, ".local", "share", "polyforge", "release")
	files := map[string]string{
		"v9/package.json":                                   `{"pi":{"skills":["./skills/polyforge"],"extensions":["./skills/polyforge/extensions/pf-chat.ts"]}}`,
		"v9/skills/polyforge/SKILL.md":                      "# polyforge\n",
		"v9/skills/polyforge/extensions/pf-chat.ts":         "// pf-chat\n",
		"v9/skills/polyforge/hooks/claude-session-start.sh": "#!/bin/sh\n",
	}
	for name, body := range files {
		p := filepath.Join(rel, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("v9", filepath.Join(rel, "current")); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	return filepath.Join(rel, "current")
}

// layoutClaudeLink links <home>/.claude/skills/polyforge to the release.
func layoutClaudeLink(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".local", "share", "polyforge", "release", "current", "skills", "polyforge")
	if err := os.Symlink(target, filepath.Join(dir, "polyforge")); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorDaemonSupervision(t *testing.T) {
	srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
	t.Run("daemon offline fails with the ensure command", func(t *testing.T) {
		env := doctorServerConfig(t, srv.URL, "k", filepath.Join(t.TempDir(), "dead.sock"), "polyforge")
		env["PATH"] = fakeTmuxPath(t, 0)
		code, _, doc := doctorStatus(t, env, "--json", "doctor")
		if code != 1 || doc.OK {
			t.Fatalf("offline daemon: exit %d ok %v, want 1/false", code, doc.OK)
		}
		st, detail := doctorFind(t, doc, "daemon socket")
		if st != "fail" || !strings.Contains(detail, "pf daemon ensure") {
			t.Fatalf("daemon socket = %s %q, want fail naming `pf daemon ensure`", st, detail)
		}
		if st, _ := doctorFind(t, doc, docChSupervision); st != "skip" {
			t.Fatalf("supervision with the daemon down = %s, want skip", st)
		}
	})
	t.Run("live unsupervised daemon warns with the fix", func(t *testing.T) {
		env := doctorServerConfig(t, srv.URL, "k", liveDaemonSocket(t, "m-u"), "polyforge")
		env["PATH"] = fakeTmuxPath(t, 1) // no polyforge-daemon session
		code, _, doc := doctorStatus(t, env, "--json", "doctor")
		st, detail := doctorFind(t, doc, docChSupervision)
		if st != "warn" || !strings.Contains(detail, "daemon.lock' true; do sleep 2; done; pf daemon ensure") || !strings.Contains(detail, "polyforge-daemon") {
			t.Fatalf("unsupervised = %s %q, want warn with the lock-waiting fix", st, detail)
		}
		if code != 0 || !doc.OK {
			t.Fatalf("a supervision warn must not move the exit code: exit %d ok %v", code, doc.OK)
		}
	})
	t.Run("live daemon without tmux installed warns", func(t *testing.T) {
		env := doctorServerConfig(t, srv.URL, "k", liveDaemonSocket(t, "m-n"), "polyforge")
		env["PATH"] = t.TempDir()
		_, _, doc := doctorStatus(t, env, "--json", "doctor")
		if st, detail := doctorFind(t, doc, docChSupervision); st != "warn" || !strings.Contains(detail, "install tmux") {
			t.Fatalf("no tmux = %s %q, want warn naming tmux", st, detail)
		}
	})
	t.Run("live supervised daemon passes", func(t *testing.T) {
		env := doctorServerConfig(t, srv.URL, "k", liveDaemonSocket(t, "m-s"), "polyforge")
		env["PATH"] = fakeTmuxPath(t, 0)
		_, _, doc := doctorStatus(t, env, "--json", "doctor")
		if st, detail := doctorFind(t, doc, docChSupervision); st != "pass" || !strings.Contains(detail, "polyforge-daemon") {
			t.Fatalf("supervised = %s %q, want pass", st, detail)
		}
	})
}

func TestPiLayoutCheck(t *testing.T) {
	t.Setenv("POLYFORGE_DATA_DIR", "")
	cases := []struct {
		name, settings, status, detail string
		release                        bool
	}{
		{"no pi", "", "skip", "not set up", true},
		{"settings absent", "-", "warn", "re-run tools/install.sh", true},
		{"git entry (old layout)", `{"packages":["git:git@github.com:silgrid/polyforge.git@v2.7.0"]}`, "warn", "git clone", true},
		{"local release (tilde)", `{"packages":["npm:x","~/.local/share/polyforge/release/current"]}`, "pass", "release/current", true},
		{"local release (relative, as pi install writes it)", `{"packages":["../../.local/share/polyforge/release/current"]}`, "pass", "release/current", true},
		{"pinned version dir", `{"packages":["~/.local/share/polyforge/release/v9"]}`, "warn", "not the release tools/install.sh manages", true},
		{"release missing", `{"packages":["~/.local/share/polyforge/release/current"]}`, "warn", "no release is checked out", false},
		{"no polyforge package", `{"packages":["npm:x"]}`, "warn", "no polyforge pi package", true},
		{"malformed", "[1,", "warn", "cannot tell", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := displayHome(t, "", tc.settings)
			if tc.release {
				layoutRelease(t, home)
			}
			c := piLayoutCheck(filepath.Join(home, ".pi", "agent"), home)
			if c.Status != tc.status || !strings.Contains(c.Detail, tc.detail) {
				t.Fatalf("got %s %q, want %s carrying %q", c.Status, c.Detail, tc.status, tc.detail)
			}
		})
	}
}

func TestClaudeLayoutCheck(t *testing.T) {
	t.Setenv("POLYFORGE_DATA_DIR", "")
	t.Run("no claude", func(t *testing.T) {
		if c := claudeLayoutCheck(t.TempDir()); c.Status != "skip" {
			t.Fatalf("got %s %q", c.Status, c.Detail)
		}
	})
	t.Run("symlink to the release passes", func(t *testing.T) {
		home := displayHome(t, "-", "")
		layoutRelease(t, home)
		layoutClaudeLink(t, home)
		if c := claudeLayoutCheck(home); c.Status != "pass" {
			t.Fatalf("got %s %q", c.Status, c.Detail)
		}
	})
	t.Run("copied directory warns", func(t *testing.T) {
		home := displayHome(t, "-", "")
		layoutRelease(t, home)
		if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "polyforge"), 0o755); err != nil {
			t.Fatal(err)
		}
		if c := claudeLayoutCheck(home); c.Status != "warn" || !strings.Contains(c.Detail, "copied directory") {
			t.Fatalf("got %s %q", c.Status, c.Detail)
		}
	})
	t.Run("link elsewhere warns", func(t *testing.T) {
		home := displayHome(t, "-", "")
		if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), filepath.Join(home, ".claude", "skills", "polyforge")); err != nil {
			t.Fatal(err)
		}
		if c := claudeLayoutCheck(home); c.Status != "warn" || !strings.Contains(c.Detail, "links to") {
			t.Fatalf("got %s %q", c.Status, c.Detail)
		}
	})
	t.Run("not installed warns", func(t *testing.T) {
		home := displayHome(t, "-", "")
		if c := claudeLayoutCheck(home); c.Status != "warn" || !strings.Contains(c.Detail, "not installed") {
			t.Fatalf("got %s %q", c.Status, c.Detail)
		}
	})
}

// pinV1Source registers src's owner/repo hash as a v1 source for one
// test (so a fixture without the polyforge name still exercises the
// hashed rule) and restores the pin list afterwards.
func pinV1Source(t *testing.T, src string) {
	t.Helper()
	key := ownerRepoKey(src)
	if key == "" {
		t.Fatalf("no owner/repo in %q", src)
	}
	v1PkgOwnerRepoHashes = append(v1PkgOwnerRepoHashes, fmt.Sprintf("%x", sha256.Sum256([]byte(key))))
	t.Cleanup(func() {
		v1PkgOwnerRepoHashes = v1PkgOwnerRepoHashes[:len(v1PkgOwnerRepoHashes)-1]
	})
}

// TestOwnerRepoKey: the normalization feeding the v1 hash pin — scp,
// https and github.com forms of one repo, with and without @version and
// .git, all yield the same lower-cased owner/repo; a source with fewer
// than two path components yields "".
func TestOwnerRepoKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"git:git@github.com:silgrid/polyforge.git", "silgrid/polyforge"},
		{"git:git@github.com:silgrid/polyforge.git@v2.7.0", "silgrid/polyforge"},
		{"https://github.com/silgrid/polyforge.git", "silgrid/polyforge"},
		{"github.com/silgrid/polyforge@v2.7.0", "silgrid/polyforge"},
		{"git@github.com:SILGRID/Polyforge.git", "silgrid/polyforge"},
		{"polyforge", ""},
		{"", ""},
	} {
		if got := ownerRepoKey(tc.in); got != tc.want {
			t.Errorf("ownerRepoKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestV1LeftoversCheck: a HOME carrying every v1 artifact warns and the
// printed commands — run in that HOME with fake pi/claude — back up
// first and then remove exactly the v1 artifacts; a clean HOME passes.
func TestV1LeftoversCheck(t *testing.T) {
	t.Setenv("POLYFORGE_DATA_DIR", "")
	t.Run("clean home passes", func(t *testing.T) {
		home := displayHome(t, "{}", `{"packages":["~/.local/share/polyforge/release/current"]}`)
		layoutRelease(t, home)
		layoutClaudeLink(t, home)
		if c := v1LeftoversCheck(home, filepath.Join(home, ".pi", "agent")); c.Status != "pass" {
			t.Fatalf("got %s %q", c.Status, c.Detail)
		}
	})
	t.Run("a hashed v1 source without the polyforge name still warns", func(t *testing.T) {
		// The real v1 pi package source (the 1.1.x harness repo) does not
		// carry the polyforge name; it is pinned by sha256 of its
		// owner/repo (polyforge#262). The pin is pointed at a fictional
		// owner/repo here so the fixture carries no internal literal — a
		// name-only rule would pass it.
		v1Src := "git:git@github.com:acme/legacy-harness.git"
		pinV1Source(t, v1Src)
		home := displayHome(t, "{}", `{"packages":["`+v1Src+`"]}`)
		c := v1LeftoversCheck(home, filepath.Join(home, ".pi", "agent"))
		if c.Status != "warn" || !strings.Contains(c.Detail, v1Src) {
			t.Fatalf("got %s %q, want warn naming %s", c.Status, c.Detail, v1Src)
		}
		if !strings.Contains(c.Detail, "pi remove") {
			t.Fatalf("detail lacks the removal command: %s", c.Detail)
		}
	})
	t.Run("a v2 git clone is the pi install layout's finding, not a v1 leftover", func(t *testing.T) {
		home := displayHome(t, "{}", `{"packages":["git:git@github.com:silgrid/polyforge.git@v2.7.0"]}`)
		if c := v1LeftoversCheck(home, filepath.Join(home, ".pi", "agent")); c.Status != "pass" {
			t.Fatalf("got %s %q, want pass", c.Status, c.Detail)
		}
	})
	t.Run("v2 pf-chat settings extensions pass", func(t *testing.T) {
		// polyforge#262 B1: a v2 progress-display extension always lives
		// under skills/polyforge/extensions/pf-chat.ts, so its path contains
		// polyforge. The extensions arm must not run the packages name rule:
		// piDisplayCheck (doctor_display.go) and doctor.md's `pi progress
		// display` row both treat these entries as a passing v2 setup.
		home := displayHome(t, "{}",
			`{"extensions":["/src/polyforge/skills/polyforge/extensions/pf-chat.ts","~/.local/share/polyforge/release/current/skills/polyforge/extensions/pf-chat.ts"]}`)
		if c := v1LeftoversCheck(home, filepath.Join(home, ".pi", "agent")); c.Status != "pass" {
			t.Fatalf("got %s %q, want pass", c.Status, c.Detail)
		}
	})
	t.Run("v1 artifacts warn with working backup-first commands", func(t *testing.T) {
		v1Src := "git:git@github.com:acme/old-polyforge.git"
		home := displayHome(t, `{"enabledPlugins":{"polyforge@acme-market":true}}`,
			`{"packages":["`+v1Src+`","~/.local/share/polyforge/release/current"],"extensions":["/x/plugins/polyforge/pi/extensions/polyforge/index.js"]}`)
		agent := filepath.Join(home, ".pi", "agent")
		for _, p := range []string{
			filepath.Join(agent, "extensions", "polyforge", "index.js"),
			filepath.Join(home, ".claude", "skills", "using-polyforge", "SKILL.md"),
			filepath.Join(home, ".claude", "skills", "pf-spec", "SKILL.md"),
			filepath.Join(home, ".claude", "skills", "unrelated", "SKILL.md"),
		} {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("v1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		plugDir := filepath.Join(home, ".claude", "plugins")
		if err := os.MkdirAll(plugDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(plugDir, "installed_plugins.json"), []byte(`{"version":2,"plugins":{"polyforge@acme-market":[{}],"other@mkt":[{}]}}`), 0o644); err != nil {
			t.Fatal(err)
		}

		c := v1LeftoversCheck(home, agent)
		if c.Status != "warn" {
			t.Fatalf("got %s %q, want warn", c.Status, c.Detail)
		}
		for _, want := range []string{v1Src, "extensions/polyforge", "using-polyforge", "pf-spec", "polyforge@acme-market", "polyforge-v1-backup-", "pi remove", "claude plugin uninstall", "by hand"} {
			if !strings.Contains(c.Detail, want) {
				t.Fatalf("detail lacks %q: %s", want, c.Detail)
			}
		}
		if strings.Contains(c.Detail, "other@mkt") || strings.Contains(c.Detail, "unrelated") || strings.Contains(c.Detail, "pi package ~/.local/share/polyforge/release/current") {
			t.Fatalf("detail names a non-v1 artifact: %s", c.Detail)
		}

		// run the printed commands (the text after "): " up to the manual note)
		_, cmds, ok := strings.Cut(c.Detail, "restart the harness sessions): ")
		if !ok {
			t.Fatalf("no command section in %q", c.Detail)
		}
		cmds, _, _ = strings.Cut(cmds, " — then, by hand")
		bin := t.TempDir()
		journal := filepath.Join(t.TempDir(), "journal")
		writeScript(t, bin, "pi", "#!/bin/sh\necho \"pi $*\" >> "+journal+"\n")
		writeScript(t, bin, "claude", "#!/bin/sh\necho \"claude $*\" >> "+journal+"\n")
		cmd := exec.Command("sh", "-c", cmds)
		cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cleanup commands failed: %v\n%s\n%s", err, cmds, out)
		}
		backups, _ := filepath.Glob(filepath.Join(home, "polyforge-v1-backup-*"))
		if len(backups) != 1 {
			t.Fatalf("want exactly one backup dir, got %v", backups)
		}
		for _, rel := range []string{"pi-settings.json", "pi-extension-polyforge/index.js", "claude-skill-using-polyforge/SKILL.md", "claude-skill-pf-spec/SKILL.md", "installed_plugins.json", "claude-settings.json"} {
			if _, err := os.Stat(filepath.Join(backups[0], rel)); err != nil {
				t.Fatalf("backup lacks %s: %v", rel, err)
			}
		}
		for _, gone := range []string{filepath.Join(agent, "extensions", "polyforge"), filepath.Join(home, ".claude", "skills", "using-polyforge")} {
			if _, err := os.Lstat(gone); err == nil {
				t.Fatalf("%s still present after cleanup", gone)
			}
		}
		if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "unrelated")); err != nil {
			t.Fatal("cleanup touched a non-v1 skill")
		}
		raw, _ := os.ReadFile(journal)
		if !strings.Contains(string(raw), "pi remove "+v1Src) || !strings.Contains(string(raw), "claude plugin uninstall polyforge@acme-market") {
			t.Fatalf("harness removal commands not run: %q", raw)
		}
	})
}
