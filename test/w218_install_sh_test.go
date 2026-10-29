package test

// polyforge#218 e2e: tools/install.sh — the prerequisite check-and-report,
// the platform pick's refusal, and the portable release/current switch.
// Spec (artifacts/spec.md, "## Requirements"):
//
//   - "macOS setup MUST check-and-report missing prerequisites and MUST NOT
//     install anything": a missing prerequisite (gh, curl, python3, git) is
//     reported, and the tool itself installs nothing — the installer
//     refuses BEFORE one byte on this machine changes. Today the installer
//     checks only gh; with curl missing it runs the whole install to
//     completion (curl is only reached through a live daemon socket).
//
//   - "an unsupported platform [is] refused up front": a machine whose
//     uname matches no released pf asset is refused naming the supported
//     matrix (linux-amd64, darwin-arm64, darwin-amd64), before changing
//     anything. Today the installer has no platform detection at all: it
//     unconditionally fetches pf-linux-amd64.
//
//   - "The release/current switch MUST be a portable atomic rename with a
//     regression test that rejects GNU-only mv -T": under a journaling mv
//     shim that exits 64 on `-T` the way BSD mv does (and passes every
//     other call through), the upgrade MUST still complete — the switch
//     happened, release/current points at the new release, the binary is
//     swapped, no .current.tmp.* leftover remains. Today the switch is
//     `{ ln -sfn && mv -T }` — GNU-only — so under the shim the upgrade
//     dies at the switch ("cannot switch release/current").
//
// Driven hermetically on top of the w174 installer world (w174NewInstall):
// scratch HOME/PF_BIN, a private tmux server (TMUX_TMPDIR scratch; the
// operator's real server is never reachable), a stubbed gh serving a fake
// release, no network. The B7 guard rides the switch scenario: the step-4
// NOTE line must stay its base text (Linux-visible output byte-unchanged
// is the existing w174 suite's own proof).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// w218FarmDir answers a directory of symlinks to the given tools (each
// resolved on the test's own PATH): a filtered PATH carrying exactly those
// tools and nothing else — the machine "where a prerequisite is missing".
func w218FarmDir(t *testing.T, tools ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range tools {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("w218 farm: %s not on the test's PATH: %v", tool, err)
		}
		if err := os.Symlink(p, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// w218UnameUnsupportedDir answers a dir whose `uname` answers an OS no
// released pf asset matches, for every invocation shape (-s, -m, -sm, a
// bare call): the spec's "a machine whose uname matches no released pf
// asset".
func w218UnameUnsupportedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n# w218: an OS no released pf asset matches (any uname call)\necho 'AIX pv7'\n"
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// w218BsdMvDir answers a dir whose `mv` is a BSD-style mv: it journals
// every call and exits 64 on `-T` (the usage error BSD mv answers — it has
// no -T), passing every other call through to the real mv.
func w218BsdMvDir(t *testing.T) string {
	t.Helper()
	realMv, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := "#!/bin/sh\n" +
		"# w218: a BSD-style mv — no -T (exit 64), everything else passes through\n" +
		"echo \"mv $*\" >> \"$W174_JOURNAL\"\n" +
		"for a in \"$@\"; do [ \"$a\" = \"-T\" ] && { echo \"mv: illegal option -- T\" >&2; exit 64; }; done\n" +
		"exec " + realMv + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "mv"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// w218AssertMachineUntouched pins "the installer refuses before one byte
// on this machine changes": no binary installed or staged, no release
// checkout, the config exactly as it was, no daemon, no restart loop, and
// no mutation of the private tmux server.
func w218AssertMachineUntouched(t *testing.T, in *w174Install, ver, cfgBefore string) {
	t.Helper()
	if _, err := os.Stat(in.bin); !os.IsNotExist(err) {
		t.Fatalf("the installer must refuse before one byte changes: %s was written (err=%v)", in.bin, err)
	}
	if left, _ := filepath.Glob(in.bin + ".new.*"); len(left) != 0 {
		t.Fatalf("the installer must refuse before one byte changes: a staged binary was left behind: %v", left)
	}
	if _, err := os.Stat(filepath.Join(in.data, "release", ver)); !os.IsNotExist(err) {
		t.Fatalf("the installer must refuse before one byte changes: the release checkout %s exists (err=%v)", ver, err)
	}
	if got := w174ReadFile(t, in.cfg); got != cfgBefore {
		t.Fatalf("the installer must refuse before one byte changes: the config was rewritten (was %q, now %q)", cfgBefore, got)
	}
	if daemons := in.daemons(); len(daemons) != 0 {
		t.Fatalf("the installer must refuse before one byte changes: a daemon was started: %v", daemons)
	}
	if n := in.loops(); n != 0 {
		t.Fatalf("the installer must refuse before one byte changes: restart loops = %d (sessions %v)", n, in.sessions())
	}
	for _, l := range in.journalLines() {
		if strings.HasPrefix(l, "pf ") {
			t.Fatalf("the installer must refuse before one byte changes: it ran %q", l)
		}
		if strings.HasPrefix(l, "tmux new-session") || strings.HasPrefix(l, "tmux kill-session") {
			t.Fatalf("the installer must refuse before one byte changes: it mutated the private tmux server: %q", l)
		}
	}
}

// TestW218InstallRefusesMissingPrerequisite: with a prerequisite the
// installer relies on (curl — one of the spec's gh, curl, python3, git)
// missing from PATH, the installer MUST refuse up front reporting it, and
// MUST NOT install anything — no byte on the machine changes. Today the
// installer never checks curl: it runs the whole install to completion
// (exit 0, binary installed, config written, daemon started).
func TestW218InstallRefusesMissingPrerequisite(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w218")
	cfgBefore := w174ReadFile(t, in.cfg)
	// a PATH with every tool the installer flow needs except curl (the
	// shims — gh, tmux, pi, mv, chmod, ln — come first on PATH as always;
	// sh included: the supervision loop's pane resolves it via PATH)
	farm := w218FarmDir(t, "sh", "mktemp", "mkdir", "cp", "rm", "sha256sum", "grep", "awk", "sed",
		"readlink", "basename", "dirname", "cat", "env", "flock", "python3", "git",
		"wc", "date", "nohup", "sleep")
	in.path = in.shim + string(os.PathListSeparator) + farm
	in.resetJournal(t)
	out, code := in.run(t)
	if code == 0 {
		t.Fatalf("an installer run with a missing prerequisite (curl) MUST refuse up front (report it, install nothing), not run to completion; observed exit 0:\n%s", out)
	}
	if !strings.Contains(out, "curl") {
		t.Fatalf("the refusal MUST report the missing prerequisite by name (curl); observed:\n%s", out)
	}
	w218AssertMachineUntouched(t, in, "v0.0.1-w218", cfgBefore)
}

// TestW218InstallRefusesUnsupportedPlatform: a machine whose uname matches
// no released pf asset MUST be refused up front, naming the supported
// matrix (linux-amd64, darwin-arm64, darwin-amd64), before anything on the
// machine changes. Today the installer has no platform detection: a uname
// answering an unsupported OS changes nothing — the linux binary is
// fetched and installed unconditionally.
func TestW218InstallRefusesUnsupportedPlatform(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w218")
	cfgBefore := w174ReadFile(t, in.cfg)
	// a uname answering an OS no released asset matches, first on PATH
	in.path = w218UnameUnsupportedDir(t) + string(os.PathListSeparator) + in.path
	in.resetJournal(t)
	out, code := in.run(t)
	if code == 0 {
		t.Fatalf("an installer run on a machine whose uname matches no released pf asset MUST refuse up front, not run to completion; observed exit 0:\n%s", out)
	}
	for _, m := range []string{"linux-amd64", "darwin-arm64", "darwin-amd64"} {
		if !strings.Contains(out, m) {
			t.Fatalf("the refusal MUST name the supported matrix (missing %s: linux-amd64, darwin-arm64, darwin-amd64); observed:\n%s", m, out)
		}
	}
	w218AssertMachineUntouched(t, in, "v0.0.1-w218", cfgBefore)
}

// TestW218InstallSwitchSurvivesBsdMv: the release/current switch MUST be a
// portable atomic rename — under a BSD-style mv (a journaling shim that
// exits 64 on -T and passes every other call through) an upgrade MUST
// still complete: the switch happened, release/current points at the new
// release, the binary is swapped, no .current.tmp.* leftover remains, and
// the new daemon runs under its loop again. Today the switch is
// `{ ln -sfn && mv -T }` — GNU-only — so under the shim the upgrade dies
// at the switch. The step-4 NOTE line must stay its base text (B7:
// Linux-visible output byte-unchanged).
func TestW218InstallSwitchSurvivesBsdMv(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w218")
	if out, code := in.run(t); code != 0 {
		t.Fatalf("install v0.0.1-w218: exit %d\n%s", code, out)
	}
	oldPid := in.oneDaemon(t, 10*time.Second)

	// a BSD-style mv first on PATH for the upgrade
	in.path = w218BsdMvDir(t) + string(os.PathListSeparator) + in.path
	in.release(t, "v0.0.2-w218")
	in.resetJournal(t)
	out, code := in.run(t)
	if code != 0 {
		t.Fatalf("the upgrade MUST complete under a BSD mv (the release/current switch must not rely on GNU-only mv -T); observed exit %d:\n%s", code, out)
	}
	// the switch happened: release/current points at the new release, the
	// binary is the new one, no temporary switch artifact is left behind
	if cur, _ := os.Readlink(filepath.Join(in.data, "release", "current")); cur != "v0.0.2-w218" {
		t.Fatalf("release/current -> %q, want v0.0.2-w218 (the switch must happen under a BSD mv)", cur)
	}
	if got := w174ReadFile(t, in.bin); !strings.Contains(got, "v0.0.2-w218") {
		t.Fatal("the binary was not swapped")
	}
	if left, _ := filepath.Glob(filepath.Join(in.data, "release", ".current.tmp.*")); len(left) != 0 {
		t.Fatalf("a temporary switch artifact was left behind: %v", left)
	}
	// the switch never called mv -T (the shim would have exited 64 and
	// failed the upgrade above; the journal names the mechanism)
	for _, l := range in.journalLines() {
		if strings.Contains(l, "mv -T ") {
			t.Fatalf("the release/current switch called mv -T (%q): GNU-only, refused by BSD mv", l)
		}
	}
	// the upgrade completed: a (new) daemon runs under its loop again
	if newPid := in.oneDaemon(t, 10*time.Second); newPid == oldPid {
		t.Fatal("the old daemon survived the upgrade")
	}
	if n := in.loops(); n != 1 {
		t.Fatalf("restart loops = %d (sessions %v), want 1", n, in.sessions())
	}
	// B7 guard: the step-4 NOTE line is pinned byte for byte. Its text
	// moved with's heartbeat permission model (owner ruling
	// carried by): the first signed heartbeat registers the
	// machine, and the note names the real refusal causes.
	if !strings.Contains(out, "NOTE: the daemon's first signed heartbeat registers this machine; if it fails, daemon.log names the cause:") {
		t.Fatalf("the step-4 NOTE line must be its pinned text (B7: Linux-visible output byte-pinned); observed:\n%s", out)
	}
}
