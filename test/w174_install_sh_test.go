package test

// w174 (polyforge#174): tools/install.sh, run hermetically end to end.
//
// Nothing here reaches the real machine: PF_BIN installs into a scratch
// dir (never /usr/local/bin), HOME is scratch (pi settings, Claude
// settings and skills, the data dir, the config), tmux is a private
// server (TMUX_TMPDIR scratch, TMUX unset), gh is a stub serving a fake
// release, and PATH carries no real pf/pi/claude. The "released" pf is a
// wrapper that journals every invocation and execs the real test build,
// and tmux is a shim that journals and execs the real tmux — so one
// journal records the order install.sh drives them in.
//
// Pinned: the old layout (pi git package entry, copied Claude skill dir)
// migrates to the new one (pi local package naming release/current,
// Claude skill symlink); a second run changes nothing; an upgrade shuts
// the daemon down gracefully (its restart loop ends with that clean exit
// — install kills no tmux session), swaps the binary, then starts the new
// daemon through `pf daemon ensure`; pi is never invoked.
// Repo review 2026-10-16 V-3: the binary swap is one rename of an
// executable file staged next to $BIN; a prepare-phase failure (release
// clone) changes nothing; running jobs refuse the upgrade unless
// PF_INSTALL_FORCE=1.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// w174Install is one hermetic installer machine on top of w174Env.
type w174Install struct {
	*w174Env
	shim     string // gh, tmux, pi shims (first on PATH)
	rel      string // the fake release assets the gh stub serves
	src      string // the release tree `gh repo clone` copies
	bin      string // PF_BIN
	journal  string
	tagFile  string
	extraEnv []string
}

func w174NewInstall(t *testing.T) *w174Install {
	t.Helper()
	e := w174NewEnv(t, true)
	// install.sh builds the release layout itself
	if err := os.RemoveAll(filepath.Join(e.data, "release")); err != nil {
		t.Fatal(err)
	}
	in := &w174Install{w174Env: e, shim: t.TempDir(), rel: t.TempDir(), src: t.TempDir(),
		bin: filepath.Join(e.home, "bin", "pf"), journal: filepath.Join(t.TempDir(), "journal"), tagFile: filepath.Join(t.TempDir(), "tag")}
	root := v1Root(t)
	cp := exec.Command("cp", "-R", filepath.Join(root, "skills"), filepath.Join(root, "workflows"), filepath.Join(root, "package.json"), in.src+"/")
	if out, err := cp.CombinedOutput(); err != nil {
		t.Fatalf("release tree: %v %s", err, out)
	}
	gh := `#!/bin/sh
# w174 stub gh: a local fake release; the tag lives in $W174_TAG_FILE
case "$1 $2" in
"release list") cat "$W174_TAG_FILE"; echo ;;  # what --jq '.[0].tagName' prints
"release download")
  dir=; pat=; prev=
  for a in "$@"; do
    case "$prev" in
      --dir) dir=$a ;;
      --pattern) pat=$a ;;
    esac
    prev=$a
  done
  cp "$W174_REL/SHA256SUMS" "$dir/"
  [ -z "$pat" ] || [ -f "$W174_REL/$pat" ] || exit 1
  [ -z "$pat" ] || cp "$W174_REL/$pat" "$dir/$pat"
  ;;
"repo clone")
  [ "${W174_CLONE_FAIL:-}" = 1 ] && exit 1
  mkdir -p "$4" && cp -R "$W174_SRC"/. "$4"/ ;;
esac
exit 0
`
	tmuxReal, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	mvReal, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	chmodReal, err := exec.LookPath("chmod")
	if err != nil {
		t.Fatal(err)
	}
	lnReal, err := exec.LookPath("ln")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"gh":   gh,
		"tmux": "#!/bin/sh\necho \"tmux $*\" >> \"$W174_JOURNAL\"\nexec " + tmuxReal + " \"$@\"\n",
		"pi":   "#!/bin/sh\necho \"pi $*\" >> \"$W174_JOURNAL\"\nexit 1\n",
		// mv journals whether its source is executable at the moment of the
		// move (the binary swap must move a ready, executable file)
		"mv": "#!/bin/sh\nsrc=; last=\nfor a in \"$@\"; do src=$last; last=$a; done\n" +
			"x=0; [ -x \"$src\" ] && x=1\necho \"mv x=$x $*\" >> \"$W174_JOURNAL\"\nexec " + mvReal + " \"$@\"\n",
		"chmod": "#!/bin/sh\necho \"chmod $*\" >> \"$W174_JOURNAL\"\nexec " + chmodReal + " \"$@\"\n",
		// W174_LN_FAIL=1 fails the release/current switch (ln -sfn): a
		// failure injected after the daemon was stopped
		"ln": "#!/bin/sh\necho \"ln $*\" >> \"$W174_JOURNAL\"\n" +
			"[ \"${W174_LN_FAIL:-}\" = 1 ] && [ \"$1\" = -sfn ] && exit 1\nexec " + lnReal + " \"$@\"\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(in.shim, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// no real pf / pi / claude on PATH: the shims, the system tools, python3
	in.path = in.shim + ":/usr/bin:/bin"
	if py, err := exec.LookPath("python3"); err == nil {
		in.path += ":" + filepath.Dir(py)
	}
	// Claude Code and pi are "set up" in this HOME
	for _, d := range []string{filepath.Join(e.home, ".claude"), filepath.Join(e.home, ".pi", "agent")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return in
}

// release publishes tag: a pf wrapper reporting tag, journaling every
// call and exec'ing the real build.
func (in *w174Install) release(t *testing.T, tag string) {
	t.Helper()
	// With W174_ENSURE_DURING_DRAIN=1, `ops shutdown` also plays a harness
	// session starting mid-upgrade: once the old daemon's socket is gone
	// it runs `pf daemon ensure` and journals its exit code.
	wrapper := fmt.Sprintf(`#!/bin/sh
echo "pf $*" >> "$W174_JOURNAL"
[ "$1" = --version ] && { echo %[1]s; exit 0; }
if [ "$1 $2" = "ops shutdown" ] && [ "${W174_ENSURE_DURING_DRAIN:-}" = 1 ]; then
  %[2]s "$@"; rc=$?
  n=0
  while curl -s -m 1 --unix-socket "$W174_SOCK" http://x/status >/dev/null 2>&1 && [ $n -lt 100 ]; do sleep 0.2; n=$((n+1)); done
  out=$(%[2]s daemon ensure --config "$W174_CFG" --timeout 3s 2>&1); erc=$?
  echo "drain-ensure rc=$erc $out" | tr '\n' ' ' >> "$W174_JOURNAL"; echo >> "$W174_JOURNAL"
  exit $rc
fi
exec %[2]s "$@"
`, tag, in.pf)
	if err := os.WriteFile(filepath.Join(in.rel, "pf-linux-amd64"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(wrapper))
	if err := os.WriteFile(filepath.Join(in.rel, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  pf-linux-amd64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.tagFile, []byte(tag), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run executes tools/install.sh once; answers (output, exit code).
func (in *w174Install) run(t *testing.T) (string, int) {
	t.Helper()
	return in.runArgs(t, "w174key", "127.0.0.1")
}

// runArgs executes tools/install.sh once with the given arguments.
func (in *w174Install) runArgs(t *testing.T, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", append([]string{filepath.Join(v1Root(t), "tools", "install.sh")}, args...)...)
	cmd.Env = append(in.env(), "PF_BIN="+in.bin, "W174_REL="+in.rel, "W174_SRC="+in.src,
		"W174_JOURNAL="+in.journal, "W174_TAG_FILE="+in.tagFile, "W174_SOCK="+in.sock, "W174_CFG="+in.cfg)
	cmd.Env = append(cmd.Env, in.extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("install.sh: %v (%s)", err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

func (in *w174Install) journalLines() []string {
	raw, _ := os.ReadFile(in.journal)
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func (in *w174Install) resetJournal(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(in.journal, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// journalIndex answers the first journal line index starting with prefix (-1 if none).
func journalIndex(lines []string, prefix string) int {
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	return -1
}

// loops counts the restart-loop sessions (exact name) on the private server.
func (in *w174Install) loops() int {
	n := 0
	for _, s := range in.sessions() {
		if s == "polyforge-daemon" {
			n++
		}
	}
	return n
}

func w174ReadFile(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestW174InstallMigratesOldLayoutThenIsIdempotent: a machine with the
// pre-#174 layout (pi git package entry pinned to a version, a copied
// Claude skill dir) gets the local package entry and the symlink, other
// pi packages untouched, the daemon started under the loop by ensure;
// a second run changes nothing.
func TestW174InstallMigratesOldLayoutThenIsIdempotent(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w174")
	piSettings := filepath.Join(in.home, ".pi", "agent", "settings.json")
	// another polyforge checkout also listed (a dev clone): install warns
	devClone := filepath.Join(t.TempDir(), "dev-polyforge")
	if err := os.MkdirAll(filepath.Join(devClone, "skills", "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devClone, "skills", "polyforge", "SKILL.md"), []byte("# dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldPi := `{"defaultModel":"x","packages":["npm:other-pkg",{"source":"git:git@github.com:silgrid/polyforge.git@v2.7.0","prompts":[]},{"source":"git:github.com/someone/else"},"` + devClone + `"]}`
	if err := os.WriteFile(piSettings, []byte(oldPi), 0o600); err != nil {
		t.Fatal(err)
	}
	// ~/.claude/settings.json is absent: install creates it (it may later
	// carry env.ANTHROPIC_* — created 0600, never the umask default)
	oldSkill := filepath.Join(in.home, ".claude", "skills", "polyforge")
	if err := os.MkdirAll(oldSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldSkill, "SKILL.md"), []byte("# old copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := in.run(t)
	if code != 0 {
		t.Fatalf("install.sh: exit %d\n%s", code, out)
	}
	relCur := filepath.Join(in.data, "release", "current")
	// pi: the git entry replaced in place by the local release, others kept
	var pi struct {
		DefaultModel string `json:"defaultModel"`
		Packages     []any  `json:"packages"`
	}
	if err := json.Unmarshal([]byte(w174ReadFile(t, piSettings)), &pi); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(pi.Packages)
	want, _ := json.Marshal([]any{"npm:other-pkg", map[string]any{"source": relCur, "prompts": []any{}}, map[string]any{"source": "git:github.com/someone/else"}, devClone})
	if string(got) != string(want) || pi.DefaultModel != "x" {
		t.Fatalf("pi settings after migration: packages %s (want %s), defaultModel %q", got, want, pi.DefaultModel)
	}
	if baks, _ := filepath.Glob(piSettings + ".bak-polyforge-*"); len(baks) != 1 {
		t.Fatalf("pi settings backups = %v, want one", baks)
	}
	if !strings.Contains(out, "WARN: pi also loads another polyforge checkout ("+devClone+")") {
		t.Fatalf("no warning about the second polyforge checkout:\n%s", out)
	}
	// Claude: the copied dir replaced by a symlink, the hook registered on the stable path
	if _, err := os.Stat(filepath.Join(oldSkill, "SKILL.md")); err != nil {
		t.Fatalf("the symlinked skill does not resolve: %v", err)
	}
	if strings.Contains(w174ReadFile(t, filepath.Join(oldSkill, "SKILL.md")), "old copy") {
		t.Fatal("the old copied skill is still in place")
	}
	// every top-level release skill is linked independently (polyforge and
	// output-format), no leftover copy of the pre-#174 layout
	for _, name := range []string{"polyforge", "output-format"} {
		if target, err := os.Readlink(filepath.Join(in.home, ".claude", "skills", name)); err != nil || target != filepath.Join(relCur, "skills", name) {
			t.Fatalf("claude skill %s is not the release symlink: %q %v", name, target, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(in.home, ".claude", "skills", "*")); len(left) != 2 {
		t.Fatalf("skills dir = %v, want both release skill links (no leftover copy)", left)
	}
	hook := filepath.Join(oldSkill, "hooks", "claude-session-start.sh")
	if !strings.Contains(w174ReadFile(t, filepath.Join(in.home, ".claude", "settings.json")), hook) {
		t.Fatal("the SessionStart hook is not registered on the stable skill path")
	}
	// settings rewrites keep 0600 (repo review 2026-10-16 V-4): the pi
	// file was 0600 before the rewrite, the claude file is new
	for _, p := range []string{piSettings, filepath.Join(in.home, ".claude", "settings.json")} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode after install = %v (%v), want 0600", p, fi.Mode().Perm(), err)
		}
	}
	// daemon: started by ensure under the loop
	lines := in.journalLines()
	if journalIndex(lines, "pf daemon ensure") < 0 {
		t.Fatalf("install.sh did not start the daemon through `pf daemon ensure`; journal:\n%s", strings.Join(lines, "\n"))
	}
	if i := journalIndex(lines, "pi "); i >= 0 {
		t.Fatalf("install.sh invoked pi (%q): the package is a settings entry now", lines[i])
	}
	if s := in.sessions(); len(s) != 1 || s[0] != "polyforge-daemon" {
		t.Fatalf("tmux sessions = %v", s)
	}
	pid := in.oneDaemon(t, 10*time.Second)

	// second run: nothing changes
	snap := map[string]string{}
	for _, p := range []string{piSettings, filepath.Join(in.home, ".claude", "settings.json"), in.cfg} {
		snap[p] = w174ReadFile(t, p)
	}
	in.resetJournal(t)
	out2, code := in.run(t)
	if code != 0 {
		t.Fatalf("second install.sh: exit %d\n%s", code, out2)
	}
	for p, before := range snap {
		if after := w174ReadFile(t, p); after != before {
			t.Fatalf("second run changed %s:\nbefore %s\nafter  %s", p, before, after)
		}
	}
	if baks, _ := filepath.Glob(piSettings + ".bak-polyforge-*"); len(baks) != 1 {
		t.Fatalf("second run wrote another pi settings backup: %v", baks)
	}
	if target, _ := os.Readlink(oldSkill); target != filepath.Join(relCur, "skills", "polyforge") {
		t.Fatalf("second run moved the skill link: %q", target)
	}
	for _, want := range []string{"pf v0.0.1-w174 already current", "release checkout v0.0.1-w174 present", "claude skill -> release/current already", "pi package -> release/current already", "SessionStart hook already registered", "daemon already running"} {
		if !strings.Contains(out2, want) {
			t.Fatalf("second run output lacks %q:\n%s", want, out2)
		}
	}
	lines = in.journalLines()
	for _, banned := range []string{"tmux kill-session", "tmux new-session", "pf ops shutdown"} {
		if i := journalIndex(lines, banned); i >= 0 {
			t.Fatalf("second run ran %q:\n%s", lines[i], strings.Join(lines, "\n"))
		}
	}
	if again := in.oneDaemon(t, 2*time.Second); again != pid {
		t.Fatalf("second run replaced the daemon: %d -> %d", pid, again)
	}
}

// TestW174InstallKeepsForeignSkillDir (workbench#32 review B3): the
// pre-#174 destructive migration belongs to the polyforge routing skill
// only. A user's own ~/.claude/skills/<other-name> entry must survive the
// install untouched, with a WARN — never moved aside and rm -rf'd. This
// case fails on the loop that applied the migration to every skill name.
func TestW174InstallKeepsForeignSkillDir(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w174")
	// the user's own skill, sharing the generic name of the new release skill
	own := filepath.Join(in.home, ".claude", "skills", "output-format")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(own, "OWN.md")
	if err := os.WriteFile(marker, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := in.run(t)
	if code != 0 {
		t.Fatalf("install.sh: exit %d\n%s", code, out)
	}
	fi, err := os.Lstat(own)
	if err != nil {
		t.Fatalf("the user's own skill dir was removed: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("the user's own skill dir was replaced by a symlink: %s", own)
	}
	if got := w174ReadFile(t, marker); got != "mine\n" {
		t.Fatalf("the user's own skill marker was changed: %q", got)
	}
	if !strings.Contains(out, "WARN:") || !strings.Contains(out, own) {
		t.Fatalf("install must WARN and name the untouched skill entry %s:\n%s", own, out)
	}
	// the routing skill is still linked: only the foreign name is skipped
	relCur := filepath.Join(in.data, "release", "current")
	if target, err := os.Readlink(filepath.Join(in.home, ".claude", "skills", "polyforge")); err != nil || target != filepath.Join(relCur, "skills", "polyforge") {
		t.Fatalf("the routing skill link was not created: %q %v", target, err)
	}
	// the SessionStart hook still resolves through the routing skill link
	if _, err := os.Stat(filepath.Join(in.home, ".claude", "skills", "polyforge", "hooks", "claude-session-start.sh")); err != nil {
		t.Fatalf("the SessionStart hook is not reachable through the routing skill link: %v", err)
	}
}

// TestW174InstallUpgradeOrderAndLock: an upgrade shuts the daemon down
// gracefully (its loop ends with the clean exit), swaps, and the new
// daemon comes from `pf daemon ensure`; a harness session's ensure during
// the drain starts nothing (install.sh holds the ensure lock); unrelated
// tmux sessions — including a similarly named one — survive.
func TestW174InstallUpgradeOrderAndLock(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w174")
	if out, code := in.run(t); code != 0 {
		t.Fatalf("install v0.0.1: exit %d\n%s", code, out)
	}
	oldPid := in.oneDaemon(t, 10*time.Second)
	for _, name := range []string{"piweb", "polyforge-daemon2"} {
		if out, err := in.tmux("new-session", "-d", "-s", name, "sleep 600"); err != nil {
			t.Fatalf("tmux new-session %s: %v %s", name, err, out)
		}
	}

	oldBin := w174ReadFile(t, in.bin)
	in.release(t, "v0.0.2-w174")
	newBin := w174ReadFile(t, filepath.Join(in.rel, "pf-linux-amd64"))
	in.resetJournal(t)
	in.extraEnv = []string{"W174_ENSURE_DURING_DRAIN=1"}
	stopWatch := w174WatchBinary(t, in.bin, oldBin, newBin)
	out, code := in.run(t)
	if bad := stopWatch(); bad != "" {
		t.Fatalf("repo review 2026-10-16 V-3(a): during the upgrade %s was observed %s", in.bin, bad)
	}
	if code != 0 {
		t.Fatalf("upgrade to v0.0.2: exit %d\n%s", code, out)
	}
	lines := in.journalLines()
	// V-3(a): the swap is one rename of an executable file staged in
	// $BIN's own directory; $BIN itself is never chmod'ed afterwards
	swap := -1
	for i, l := range lines {
		if f := strings.Fields(l); len(f) >= 3 && f[0] == "mv" && f[len(f)-1] == in.bin {
			swap = i
			src := f[len(f)-2]
			if filepath.Dir(src) != filepath.Dir(in.bin) || f[1] != "x=1" {
				t.Fatalf("binary swap %q: want an executable source staged in %s", l, filepath.Dir(in.bin))
			}
		}
		if strings.HasPrefix(l, "chmod ") && strings.HasSuffix(l, " "+in.bin) {
			t.Fatalf("install.sh chmod'ed the live binary (%q): the swap must move an executable file", l)
		}
	}
	if swap < 0 || swap < journalIndex(lines, "pf ops shutdown") {
		t.Fatalf("binary swap (%d) must follow the graceful shutdown; journal:\n%s", swap, strings.Join(lines, "\n"))
	}
	if left, _ := filepath.Glob(in.bin + ".new.*"); len(left) != 0 {
		t.Fatalf("staged binary left behind: %v", left)
	}
	shut := journalIndex(lines, "pf ops shutdown")
	drain := journalIndex(lines, "drain-ensure ")
	ensure := journalIndex(lines, "pf daemon ensure --config")
	if shut < 0 || drain < 0 || ensure < 0 || !(shut < drain && drain < ensure) {
		t.Fatalf("upgrade order must be: graceful shutdown (%d) < [mid-drain ensure (%d)] < install's ensure (%d); journal:\n%s", shut, drain, ensure, strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[drain], "drain-ensure rc=1 ") || !strings.Contains(lines[drain], "another `pf daemon ensure`") {
		t.Fatalf("an ensure during the drain must wait on install's lock and start nothing: %q", lines[drain])
	}
	if i := journalIndex(lines, "tmux kill-session"); i >= 0 {
		t.Fatalf("the loop should have ended with the clean shutdown, yet install killed a session: %q", lines[i])
	}
	if got := w174ReadFile(t, in.bin); !strings.Contains(got, "v0.0.2-w174") {
		t.Fatal("the binary was not swapped")
	}
	if cur, _ := os.Readlink(filepath.Join(in.data, "release", "current")); cur != "v0.0.2-w174" {
		t.Fatalf("release/current -> %q", cur)
	}
	newPid := in.oneDaemon(t, 10*time.Second)
	if newPid == oldPid {
		t.Fatal("the old daemon survived the upgrade")
	}
	if in.loops() != 1 {
		t.Fatalf("restart loops after the upgrade = %d (sessions %v), want 1", in.loops(), in.sessions())
	}
	// unrelated sessions survive install AND a later ensure
	if code, o, e := in.ensure(t); code != 0 {
		t.Fatalf("ensure after the upgrade: %d %s %s", code, o, e)
	}
	got := strings.Join(in.sessions(), " ")
	for _, name := range []string{"piweb", "polyforge-daemon2"} {
		if !strings.Contains(" "+got+" ", " "+name+" ") {
			t.Fatalf("unrelated tmux session %s did not survive (sessions %q)", name, got)
		}
	}
}

// TestW174InstallUpgradeFromUnsupervisedNohupDaemon: today's machines run
// a bare `nohup pf daemon` and no loop; the upgrade stops it gracefully
// and ends with exactly one loop and one (new) daemon.
func TestW174InstallUpgradeFromUnsupervisedNohupDaemon(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w174")
	if out, code := in.run(t); code != 0 {
		t.Fatalf("install v0.0.1: exit %d\n%s", code, out)
	}
	// back to today's state: stop the loop's daemon, start a bare nohup one
	stop := exec.Command(in.pf, "--config", in.cfg, "ops", "shutdown")
	stop.Env = in.env()
	if out, err := stop.CombinedOutput(); err != nil {
		t.Fatalf("ops shutdown: %v %s", err, out)
	}
	waitUntil(t, 30*time.Second, "the loop's daemon did not end", func() bool { return len(in.daemons()) == 0 && in.loops() == 0 })
	id := strings.TrimSpace(w174ReadFile(t, filepath.Join(in.data, "daemon-id")))
	nohup := exec.Command("sh", "-c", "nohup \"$0\" daemon --config \"$1\" -machine-id \"$2\" -data-dir \"$3\" -socket \"$4\" -workflows-dir \"$3/release/current/workflows\" >/dev/null 2>&1 &",
		in.pf, in.cfg, id, in.data, in.sock)
	nohup.Env = in.env()
	if out, err := nohup.CombinedOutput(); err != nil {
		t.Fatalf("nohup daemon: %v %s", err, out)
	}
	oldPid := in.oneDaemon(t, 10*time.Second)
	if in.loops() != 0 {
		t.Fatal("fixture: a loop exists")
	}

	in.release(t, "v0.0.2-w174")
	in.resetJournal(t)
	out, code := in.run(t)
	if code != 0 {
		t.Fatalf("upgrade: exit %d\n%s", code, out)
	}
	newPid := in.oneDaemon(t, 10*time.Second)
	if newPid == oldPid {
		t.Fatal("the nohup daemon survived the upgrade")
	}
	if in.loops() != 1 {
		t.Fatalf("restart loops = %d (sessions %v), want 1", in.loops(), in.sessions())
	}
	environ, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(newPid), "environ"))
	if !strings.Contains(string(environ), "POLYFORGE_SUPERVISOR=tmux:polyforge-daemon") {
		t.Fatal("the new daemon is not the loop's")
	}
}

// w174WatchBinary polls bin until the returned stop is called; stop
// answers "" when bin was always present, executable and byte-identical
// to one of the two releases, else what was seen.
func w174WatchBinary(t *testing.T, bin string, versions ...string) func() string {
	t.Helper()
	done := make(chan struct{})
	result := make(chan string, 1)
	go func() {
		bad := ""
		for {
			select {
			case <-done:
				result <- bad
				return
			default:
			}
			st, err := os.Stat(bin)
			switch {
			case err != nil:
				bad = "missing: " + err.Error()
			case st.Mode()&0o111 == 0:
				bad = "non-executable (" + st.Mode().String() + ")"
			default:
				if raw, err := os.ReadFile(bin); err == nil {
					ok := false
					for _, v := range versions {
						ok = ok || string(raw) == v
					}
					if !ok {
						bad = fmt.Sprintf("partial (%d bytes)", len(raw))
					}
				}
			}
			if bad != "" {
				<-done
				result <- bad
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	return func() string { close(done); return <-result }
}

// w174FakeDaemon serves this machine's socket in place of the daemon and
// holds its data-dir lock (daemon.lock, as a live daemon does): GET
// /status answers jobs; POST /shutdown is recorded, closes the listener
// (the socket file goes with it) and releases the lock, as a daemon's
// exit would.
type w174FakeDaemon struct {
	ln       net.Listener
	srv      *http.Server
	lock     *os.File
	shutdown chan struct{}
	// refuseShutdown makes POST /shutdown answer 500 and keep serving
	// (a shutdown request that stops nothing).
	refuseShutdown atomic.Bool
}

// stop closes the listener and releases the lock.
func (fd *w174FakeDaemon) stop() {
	_ = fd.srv.Close()
	_ = fd.lock.Close()
}

// A statusCode other than 200 answers GET /status with that code and no
// job list.
func w174StartFakeDaemon(t *testing.T, sock string, statusCode int, jobs []map[string]any) *w174FakeDaemon {
	t.Helper()
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(sock), "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("fake daemon: daemon.lock: %v", err)
	}
	fd := &w174FakeDaemon{ln: ln, lock: lock, shutdown: make(chan struct{})}
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		if statusCode != http.StatusOK {
			http.Error(w, "unavailable", statusCode)
			return
		}
		raw, _ := json.Marshal(map[string]any{"machine_id": "m-fake", "auto": false, "workspaces": []string{}, "jobs": jobs,
			"queue": map[string]int{"pending": 0, "running": len(jobs)}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		if fd.refuseShutdown.Load() {
			http.Error(w, `{"error":{"code":"DAEMON-500","message":"refused by the test"}}`, http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
		once.Do(func() {
			close(fd.shutdown)
			go fd.stop()
		})
	})
	fd.srv = &http.Server{Handler: mux}
	go func() { _ = fd.srv.Serve(ln) }()
	t.Cleanup(fd.stop)
	return fd
}

func (fd *w174FakeDaemon) shutdownRequested() bool {
	select {
	case <-fd.shutdown:
		return true
	default:
		return false
	}
}

// w174InstallV1ThenStop installs v0.0.1 and stops its daemon (and so its
// loop): the socket is free for a fake daemon.
func w174InstallV1ThenStop(t *testing.T) *w174Install {
	t.Helper()
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w174")
	if out, code := in.run(t); code != 0 {
		t.Fatalf("install v0.0.1: exit %d\n%s", code, out)
	}
	in.oneDaemon(t, 10*time.Second)
	in.shutdown(t)
	waitUntil(t, 30*time.Second, "the v0.0.1 daemon and its loop did not end", func() bool { return len(in.daemons()) == 0 && in.loops() == 0 })
	return in
}

// w174AssertStillV1 pins "nothing changed": the v0.0.1 binary, release/current
// on v0.0.1, no staged binary or half clone left, no shutdown or swap run.
func w174AssertStillV1(t *testing.T, in *w174Install, v1Bin string) {
	t.Helper()
	if got := w174ReadFile(t, in.bin); got != v1Bin {
		t.Fatal("the installed binary changed")
	}
	if cur, _ := os.Readlink(filepath.Join(in.data, "release", "current")); cur != "v0.0.1-w174" {
		t.Fatalf("release/current -> %q, want v0.0.1-w174", cur)
	}
	for _, pat := range []string{in.bin + ".new.*", filepath.Join(in.data, "release", "*.tmp.*")} {
		if left, _ := filepath.Glob(pat); len(left) != 0 {
			t.Fatalf("leftovers: %v", left)
		}
	}
	lines := in.journalLines()
	for _, banned := range []string{"pf ops shutdown", "pf daemon ensure", "tmux kill-session", "tmux new-session"} {
		if i := journalIndex(lines, banned); i >= 0 {
			t.Fatalf("ran %q:\n%s", lines[i], strings.Join(lines, "\n"))
		}
	}
	for _, l := range lines {
		if f := strings.Fields(l); len(f) >= 3 && f[0] == "mv" && f[len(f)-1] == in.bin {
			t.Fatalf("swapped the binary: %q", l)
		}
	}
}

// TestW174InstallPrepFailureLeavesMachineUntouched (repo review
// 2026-10-16 V-3(b)): the release checkout is fetched before anything is
// stopped or swapped; a clone failure exits non-zero with the old binary,
// the old release/current and the old daemon (same pid, under its loop)
// untouched, and says so.
func TestW174InstallPrepFailureLeavesMachineUntouched(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w174")
	if out, code := in.run(t); code != 0 {
		t.Fatalf("install v0.0.1: exit %d\n%s", code, out)
	}
	pid := in.oneDaemon(t, 10*time.Second)
	v1Bin := w174ReadFile(t, in.bin)

	in.release(t, "v0.0.2-w174")
	in.resetJournal(t)
	in.extraEnv = []string{"W174_CLONE_FAIL=1"}
	out, code := in.run(t)
	if code == 0 {
		t.Fatalf("a failed release clone must fail the install:\n%s", out)
	}
	if !strings.Contains(out, "cannot clone") || !strings.Contains(out, "nothing on this machine was changed") {
		t.Fatalf("the failure must name the cause and say nothing changed:\n%s", out)
	}
	w174AssertStillV1(t, in, v1Bin)
	if _, err := os.Stat(filepath.Join(in.data, "release", "v0.0.2-w174")); !os.IsNotExist(err) {
		t.Fatalf("a failed clone left release/v0.0.2-w174: %v", err)
	}
	if again := in.oneDaemon(t, 5*time.Second); again != pid {
		t.Fatalf("the old daemon was replaced: %d -> %d", pid, again)
	}
	if in.loops() != 1 {
		t.Fatalf("restart loops = %d, want the old one", in.loops())
	}

	// the cause fixed, the same command upgrades
	in.extraEnv = nil
	if out, code = in.run(t); code != 0 {
		t.Fatalf("upgrade after the fix: exit %d\n%s", code, out)
	}
	if cur, _ := os.Readlink(filepath.Join(in.data, "release", "current")); cur != "v0.0.2-w174" {
		t.Fatalf("release/current -> %q", cur)
	}
	newPid := in.oneDaemon(t, 10*time.Second)
	if newPid == pid {
		t.Fatal("the old daemon survived the upgrade")
	}
	pid = newPid

	// an incomplete checkout that IS release/current is never removed:
	// prepare fails instead (the daemon and the tree stay as they are)
	cur := filepath.Join(in.data, "release", "v0.0.2-w174")
	if err := os.Remove(filepath.Join(cur, "skills", "polyforge", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	in.resetJournal(t)
	out, code = in.run(t)
	if code == 0 || !strings.Contains(out, "is incomplete") || !strings.Contains(out, "nothing on this machine was changed") {
		t.Fatalf("an incomplete current checkout must fail prepare (exit %d):\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(cur, "workflows")); err != nil {
		t.Fatalf("the in-use checkout was removed: %v", err)
	}
	if lines := in.journalLines(); journalIndex(lines, "pf ops shutdown") >= 0 {
		t.Fatalf("shut the daemon down:\n%s", strings.Join(lines, "\n"))
	}
	if again := in.oneDaemon(t, 5*time.Second); again != pid {
		t.Fatalf("the daemon was replaced: %d -> %d", pid, again)
	}
}

// TestW174InstallRefusesWhileJobsRunThenForceProceeds (repo review
// 2026-10-16 V-3(c)): an upgrade needs a graceful shutdown, which
// terminates live agent trees; with a running job the installer refuses,
// lists it (a paused job does not block) and changes nothing.
// PF_INSTALL_FORCE=1 shuts down anyway and completes the upgrade.
func TestW174InstallRefusesWhileJobsRunThenForceProceeds(t *testing.T) {
	in := w174InstallV1ThenStop(t)
	v1Bin := w174ReadFile(t, in.bin)
	fake := w174StartFakeDaemon(t, in.sock, http.StatusOK, []map[string]any{
		{"work_item_id": "wi-7", "slug": "e2e#7", "status": "running"},
		{"work_item_id": "wi-8", "slug": "e2e#8", "status": "paused"},
		{"work_item_id": "wi-9", "slug": "e2e#9", "status": "end_pending"},
		{"work_item_id": "wi-10", "slug": "e2e#10", "status": "recovering"},
	})

	in.release(t, "v0.0.2-w174")
	in.resetJournal(t)
	out, code := in.run(t)
	if code == 0 {
		t.Fatalf("an upgrade with a running job must be refused:\n%s", out)
	}
	for _, want := range []string{"Upgrade refused", "e2e#7 (running)", "e2e#10 (recovering)", "PF_INSTALL_FORCE=1", "pf pause", "nothing on this machine was changed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("refusal lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "e2e#8") || strings.Contains(out, "e2e#9") {
		t.Fatalf("paused and end_pending jobs (no agent tree) must not block the upgrade:\n%s", out)
	}
	if fake.shutdownRequested() {
		t.Fatal("the daemon was asked to shut down despite the refusal")
	}
	w174AssertStillV1(t, in, v1Bin)

	in.resetJournal(t)
	in.extraEnv = []string{"PF_INSTALL_FORCE=1"}
	out, code = in.run(t)
	if code != 0 {
		t.Fatalf("forced upgrade: exit %d\n%s", code, out)
	}
	if !fake.shutdownRequested() || !strings.Contains(out, "e2e#7 (running)") {
		t.Fatalf("the forced upgrade must shut the daemon down and name the jobs it interrupts:\n%s", out)
	}
	if got := w174ReadFile(t, in.bin); !strings.Contains(got, "v0.0.2-w174") {
		t.Fatal("the binary was not swapped")
	}
	if cur, _ := os.Readlink(filepath.Join(in.data, "release", "current")); cur != "v0.0.2-w174" {
		t.Fatalf("release/current -> %q", cur)
	}
	in.oneDaemon(t, 10*time.Second)
	if in.loops() != 1 {
		t.Fatalf("restart loops = %d (sessions %v), want 1", in.loops(), in.sessions())
	}
}

// TestW174InstallJobCheckFailsClosed (repo
// review 2026-10-16 V-3): the job check fails closed — a socket
// that does not answer /status with a job list refuses the upgrade (no
// shutdown, nothing changed); paused and end_pending jobs alone (no agent
// tree) let it proceed without the override.
func TestW174InstallJobCheckFailsClosed(t *testing.T) {
	in := w174InstallV1ThenStop(t)
	v1Bin := w174ReadFile(t, in.bin)
	fake := w174StartFakeDaemon(t, in.sock, http.StatusServiceUnavailable, nil)
	in.release(t, "v0.0.2-w174")
	in.resetJournal(t)
	out, code := in.run(t)
	if code == 0 || !strings.Contains(out, "job list could not be read") || !strings.Contains(out, "PF_INSTALL_FORCE=1") {
		t.Fatalf("an unreadable job list must refuse the upgrade (exit %d):\n%s", code, out)
	}
	if fake.shutdownRequested() {
		t.Fatal("the daemon was asked to shut down although its jobs are unknown")
	}
	w174AssertStillV1(t, in, v1Bin)
	fake.stop()

	fake = w174StartFakeDaemon(t, in.sock, http.StatusOK, []map[string]any{
		{"work_item_id": "wi-8", "slug": "e2e#8", "status": "paused"},
		{"work_item_id": "wi-9", "slug": "e2e#9", "status": "end_pending"},
	})
	in.resetJournal(t)
	out, code = in.run(t)
	if code != 0 {
		t.Fatalf("paused/end_pending jobs must not block the upgrade: exit %d\n%s", code, out)
	}
	if !fake.shutdownRequested() {
		t.Fatal("the upgrade did not shut the daemon down")
	}
	if cur, _ := os.Readlink(filepath.Join(in.data, "release", "current")); cur != "v0.0.2-w174" {
		t.Fatalf("release/current -> %q", cur)
	}
	in.oneDaemon(t, 10*time.Second)
}

// TestW174InstallAbortAfterStopReportsState (repo review 2026-10-16 V-3):
// a failure after the daemon was asked to stop (here: the
// release/current switch) exits non-zero and says where the machine
// stands — daemon stopped, which binary, where release/current points,
// what to run — without restarting anything itself.
func TestW174InstallAbortAfterStopReportsState(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w174")
	if out, code := in.run(t); code != 0 {
		t.Fatalf("install v0.0.1: exit %d\n%s", code, out)
	}
	in.oneDaemon(t, 10*time.Second)

	in.release(t, "v0.0.2-w174")
	in.resetJournal(t)
	in.extraEnv = []string{"W174_LN_FAIL=1"}
	out, code := in.run(t)
	if code == 0 {
		t.Fatalf("a failed release switch must fail the install:\n%s", out)
	}
	for _, want := range []string{"cannot switch", "AFTER the daemon was asked to shut down", "pf binary " + in.bin + ": v0.0.2-w174",
		"release/current -> v0.0.1-w174", "pf daemon ensure", "re-run this installer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("abort report lacks %q:\n%s", want, out)
		}
	}
	lines := in.journalLines()
	if i := journalIndex(lines, "pf daemon ensure"); i >= 0 {
		t.Fatalf("the aborted install started the daemon itself: %q", lines[i])
	}
	if len(in.daemons()) != 0 || in.loops() != 0 {
		t.Fatalf("after the abort: daemons %v loops %d, want none", in.daemons(), in.loops())
	}
	if left, _ := filepath.Glob(filepath.Join(in.data, "release", ".current.tmp.*")); len(left) != 0 {
		t.Fatalf("leftover: %v", left)
	}

	// re-running finishes the upgrade
	in.extraEnv = nil
	if out, code := in.run(t); code != 0 {
		t.Fatalf("re-run: exit %d\n%s", code, out)
	}
	if cur, _ := os.Readlink(filepath.Join(in.data, "release", "current")); cur != "v0.0.2-w174" {
		t.Fatalf("release/current -> %q", cur)
	}
	in.oneDaemon(t, 10*time.Second)
}

// TestW174InstallShutdownFailureStopsAtOnce (review of decision
// 2026-10-16 G11): with the curl fallback gone, a `pf ops shutdown` that
// fails while the daemon stays live must end the upgrade at once — exit 1,
// nothing swapped, and never the "stopped" wording of a daemon that runs.
func TestW174InstallShutdownFailureStopsAtOnce(t *testing.T) {
	in := w174InstallV1ThenStop(t)
	v1Bin := w174ReadFile(t, in.bin)
	fake := w174StartFakeDaemon(t, in.sock, http.StatusOK, nil)
	fake.refuseShutdown.Store(true)

	in.release(t, "v0.0.2-w174")
	in.resetJournal(t)
	start := time.Now()
	out, code := in.run(t)
	if code != 1 {
		t.Fatalf("a failed shutdown must exit 1, got %d:\n%s", code, out)
	}
	if d := time.Since(start); d > 60*time.Second {
		t.Fatalf("the installer waited %v after a failed shutdown request", d)
	}
	for _, want := range []string{"pf ops shutdown failed", "nothing on this machine was changed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"daemon stopped", "stopped or stopping", "AFTER the daemon was asked"} {
		if strings.Contains(out, bad) {
			t.Fatalf("output claims %q for a daemon that still runs:\n%s", bad, out)
		}
	}
	if got := w174ReadFile(t, in.bin); got != v1Bin {
		t.Fatal("the installed binary changed")
	}
	if cur, _ := os.Readlink(filepath.Join(in.data, "release", "current")); cur != "v0.0.1-w174" {
		t.Fatalf("release/current -> %q, want v0.0.1-w174", cur)
	}
	if lines := in.journalLines(); journalIndex(lines, "pf daemon ensure") >= 0 {
		t.Fatalf("ran ensure after a failed shutdown:\n%s", strings.Join(lines, "\n"))
	}
}

// ---------------------------------------------------------------------
// polyforge#218: darwin as a supported work-machine platform. The
// installer's ONLY per-OS branch is the asset pick (`uname -s`/`uname -m`
// → pf-darwin-arm64 / pf-darwin-amd64 / pf-linux-amd64) and the checksum
// verifier gains the macOS shasum fallback; prerequisites are checked and
// reported up front, never installed.

// w174FilteredPath builds a PATH prelude: shim plus a symlink farm of the
// system dirs minus the named tools — the hermetic "not on PATH". The shim
// too is copied over (minus the missing tool), so a stub named like the
// missing tool does not satisfy the check.
func w174FilteredPath(t *testing.T, shim string, missing ...string) string {
	t.Helper()
	farm := t.TempDir()
	gone := map[string]bool{}
	for _, m := range missing {
		gone[m] = true
	}
	shim2 := filepath.Join(t.TempDir(), "shim")
	if err := os.MkdirAll(shim2, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(shim)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if gone[e.Name()] {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(shim, e.Name()))
		_ = rerr
		if err := os.WriteFile(filepath.Join(shim2, e.Name()), raw, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"/usr/bin", "/bin"} {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || gone[e.Name()] {
				continue
			}
			// regular files AND named links (python3 → python3.11 etc.)
			_ = os.Symlink(filepath.Join(d, e.Name()), filepath.Join(farm, e.Name()))
		}
	}
	return shim2 + string(os.PathListSeparator) + farm
}

// w174DarwinRelease publishes a release whose pf asset is the darwin one
// (named asset) covered by SHA256SUMS, carrying the same pf shim wrapper
// as release(t, tag). Returns the wrapper content: the installed binary
// must equal it, proving the darwin asset — not the linux one — was
// installed.
func (in *w174Install) w174DarwinRelease(t *testing.T, tag, asset string) string {
	t.Helper()
	wrapper := fmt.Sprintf(`#!/bin/sh
echo "pf $*" >> "$W174_JOURNAL"
[ "$1" = --version ] && { echo %s; exit 0; }
exec %s "$@"
`, tag, in.pf)
	if err := os.WriteFile(filepath.Join(in.rel, asset), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(wrapper))
	if err := os.WriteFile(filepath.Join(in.rel, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.tagFile, []byte(tag), 0o644); err != nil {
		t.Fatal(err)
	}
	return wrapper
}

// w174UnameShim answers `uname -s`/`uname -m` from a fixed pair (the
// installer's asset pick is keyed on exactly that). A Darwin machine also
// gets the macOS tools the installer runs on the staged pf (w237MacTools).
func w174UnameShim(t *testing.T, dir, kernel, arch string) {
	t.Helper()
	body := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -s) echo %s ;; -m) echo %s ;; *) echo %s ;; esac\n", kernel, arch, arch)
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if kernel == "Darwin" {
		w237MacTools(t, dir)
	}
}

// w237MacTools writes journaling stubs of macOS's xattr and codesign
// (polyforge#237) into dir. W237_XATTR_FAIL=1 / W237_CODESIGN_FAIL=1 make
// the call fail; W237_CODESIGN_KILL=1 makes the signed file one that gets
// killed (SIGKILL, the `Killed: 9` Gatekeeper answers) when run.
func w237MacTools(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"xattr": "#!/bin/sh\necho \"xattr $*\" >> \"$W174_JOURNAL\"\n" +
			"[ \"${W237_XATTR_FAIL:-}\" = 1 ] && { echo \"xattr: [Errno 1] Operation not permitted\" >&2; exit 1; }\nexit 0\n",
		"codesign": "#!/bin/sh\necho \"codesign $*\" >> \"$W174_JOURNAL\"\n" +
			"[ \"${W237_CODESIGN_FAIL:-}\" = 1 ] && { echo \"codesign: the codesign_allocate helper tool cannot be found\" >&2; exit 1; }\n" +
			"for f in \"$@\"; do :; done\n" +
			"[ \"${W237_CODESIGN_KILL:-}\" = 1 ] && printf '#!/bin/sh\\nkill -9 $$\\n' > \"$f\"\n" +
			"echo \"$f: replacing existing signature\" >&2\nexit 0\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestW174InstallDarwinAssetPickAndShasumFallback: on a Mac (uname shim;
// NO sha256sum on PATH) the installer downloads the pf-darwin-arm64 /
// pf-darwin-amd64 asset — the installed pf IS the darwin asset
// byte-for-byte, not the linux one — and verifies it through
// `shasum -a 256` (macOS has no sha256sum): the fallback is what made the
// install succeed.
func TestW174InstallDarwinAssetPickAndShasumFallback(t *testing.T) {
	for name, arch := range map[string]string{"arm64": "arm64", "amd64 (x86_64)": "x86_64"} {
		t.Run(name, func(t *testing.T) {
			in := w174NewInstall(t)
			asset := map[string]string{"arm64": "pf-darwin-arm64", "x86_64": "pf-darwin-amd64"}[arch]
			wrapper := in.w174DarwinRelease(t, "v0.0.3-w218", asset)
			w174UnameShim(t, in.shim, "Darwin", arch)
			// shasum shim delegating to the real verifier, in macOS's output
			// format (`<hex>  <file>`): with no sha256sum on PATH this is the
			// only checksum tool, so success proves the fallback ran.
			shasumReal, err := exec.LookPath("sha256sum")
			if err != nil {
				t.Skip("no real sha256sum to back the shasum shim")
			}
			if err := os.WriteFile(filepath.Join(in.shim, "shasum"),
				[]byte("#!/bin/sh\n[ \"$1 $2\" = \"-a 256\" ] && { shift 2; exec "+shasumReal+" \"$@\"; }\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			in.path = w174FilteredPath(t, in.shim, "sha256sum")
			out, code := in.run(t)
			if code != 0 {
				t.Fatalf("install.sh on a Mac (Darwin %s): exit %d\n%s", arch, code, out)
			}
			if got := w174ReadFile(t, in.bin); got != wrapper {
				t.Fatalf("installed pf is NOT the %s asset (want the wrapper byte-for-byte):\n%.200s", asset, got)
			}
			if !strings.Contains(out, "pf v0.0.3-w218 downloaded, SHA-verified, staged") {
				t.Fatalf("no download/verify step in the output:\n%s", out)
			}
		})
	}
}

// TestW174InstallMissingPrerequisiteRefuses (polyforge#218): a machine
// missing one of gh/curl/python3/git is reported with what to obtain and
// the installer refuses BEFORE anything is touched (step 1's promise:
// nothing on this machine was changed).
func TestW174InstallMissingPrerequisiteRefuses(t *testing.T) {
	for _, tool := range []string{"gh", "python3"} {
		t.Run(tool, func(t *testing.T) {
			in := w174NewInstall(t)
			in.path = w174FilteredPath(t, in.shim, tool)
			out, code := in.run(t)
			if code == 0 {
				t.Fatalf("install.sh on a PATH without %s must fail:\n%s", tool, out)
			}
			for _, want := range []string{"missing required tools", tool, "nothing on this machine was changed"} {
				if !strings.Contains(out, want) {
					t.Fatalf("refusal lacks %q:\n%s", want, out)
				}
			}
			if _, err := os.Stat(in.bin); !os.IsNotExist(err) {
				t.Fatalf("a pf binary landed in %s despite the refusal", in.bin)
			}
			if _, err := os.Stat(filepath.Join(in.data, "release", "v0.0.1-w174")); !os.IsNotExist(err) {
				t.Fatalf("a release checkout was staged despite the refusal: %v", err)
			}
			if left, _ := filepath.Glob(in.bin + ".new.*"); len(left) != 0 {
				t.Fatalf("staged binary left behind: %v", left)
			}
			for _, l := range in.journalLines() {
				if l != "" {
					t.Fatalf("the refusal still ran shim tools: %q", l)
				}
			}
		})
	}
}

// TestW174InstallReleaseSwitchWithoutGnuMvT (polyforge#218 review B5):
// the release/current swap must not depend on GNU-only `mv -T` — BSD/macOS
// mv(1) (synopsis `mv [-f | -i | -n] [-hv] source target`) has no -T and
// exits 64 with "illegal option -- T". An mv shim that rejects -T exactly
// like BSD mv is put on PATH; the upgrade must still complete, swap the
// binary and switch release/current. The switch itself goes through
// python3's os.replace (rename(2)), a checked prerequisite.
func TestW174InstallReleaseSwitchWithoutGnuMvT(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w174")
	if out, code := in.run(t); code != 0 {
		t.Fatalf("install v0.0.1: exit %d\n%s", code, out)
	}
	in.oneDaemon(t, 10*time.Second)

	// BSD mv: `-T` is an illegal option; every other invocation still
	// performs a real move (delegated to the host's mv).
	mvReal, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.shim, "mv"), []byte(
		"#!/bin/sh\n"+
			"for a in \"$@\"; do\n"+
			"  if [ \"$a\" = -T ]; then\n"+
			"    echo \"mv: illegal option -- T\" >&2\n"+
			"    echo \"usage: mv [-f | -i | -n] [-hv] source target\" >&2\n"+
			"    echo \"       mv [-f | -i | -n] [-hv] source ... directory\" >&2\n"+
			"    exit 64\n"+
			"  fi\n"+
			"done\n"+
			"src=; last=\n"+
			"for a in \"$@\"; do src=$last; last=$a; done\n"+
			"x=0; [ -x \"$src\" ] && x=1\n"+
			"echo \"mv x=$x $*\" >> \"$W174_JOURNAL\"\n"+
			"exec "+mvReal+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	in.release(t, "v0.0.2-w174")
	in.resetJournal(t)
	out, code := in.run(t)
	if code != 0 {
		t.Fatalf("upgrade with BSD-style mv (no -T support) must still succeed: exit %d\n%s", code, out)
	}
	if cur, _ := os.Readlink(filepath.Join(in.data, "release", "current")); cur != "v0.0.2-w174" {
		t.Fatalf("release/current -> %q, want v0.0.2-w174", cur)
	}
	if got := w174ReadFile(t, in.bin); !strings.Contains(got, "v0.0.2-w174") {
		t.Fatal("the binary was not swapped")
	}
	if left, _ := filepath.Glob(filepath.Join(in.data, "release", ".current.tmp.*")); len(left) != 0 {
		t.Fatalf("leftover: %v", left)
	}
	in.oneDaemon(t, 10*time.Second)
	if in.loops() != 1 {
		t.Fatalf("restart loops = %d (sessions %v), want 1", in.loops(), in.sessions())
	}
}
