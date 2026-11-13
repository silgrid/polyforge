// carries: tools/install.sh, pi-web/bin/pi-web-install-check.js, pi-web/e2e/lane-stubs.mjs

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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// TestInstallVersionLineCutover: tools/install.sh refuses
// every downgrade — a latest release older than the installed pf — before
// one byte changes, with the one named exception: any v0.2.x release over a
// v2.20.x pf (the retired internal line's cutover to the unified product
// line; the installer always installs the latest release, so a v2.20.x
// machine must still cross once v0.2.1 ships).
func TestInstallVersionLineCutover(t *testing.T) {
	installed := func(t *testing.T, in *w174Install, ver string) string {
		t.Helper()
		body := "#!/bin/sh\n[ \"$1\" = --version ] && { echo " + ver + "; exit 0; }\nexit 0\n"
		if err := os.MkdirAll(filepath.Dir(in.bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(in.bin, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return body
	}
	for _, c := range []struct{ from, to string }{
		{"v2.20.22", "v0.1.9"},
		{"v0.2.2", "v0.2.0"},
		{"v2.19.9", "v0.2.0"},
	} {
		t.Run("refused "+c.from+" -> "+c.to, func(t *testing.T) {
			in := w174NewInstall(t)
			in.release(t, c.to)
			before := installed(t, in, c.from)
			cfgBefore := w174ReadFile(t, in.cfg)
			in.resetJournal(t)
			out, code := in.run(t)
			if code == 0 || !strings.Contains(out, "a downgrade is refused") || !strings.Contains(out, "this machine still runs pf "+c.from) {
				t.Fatalf("installing %s over pf %s MUST be refused before anything changes, saying what the machine runs; exit %d:\n%s", c.to, c.from, code, out)
			}
			if got := w174ReadFile(t, in.bin); got != before {
				t.Fatalf("the refused downgrade replaced the installed pf")
			}
			if left, _ := filepath.Glob(in.bin + ".new.*"); len(left) != 0 {
				t.Fatalf("a staged binary was left behind: %v", left)
			}
			if _, err := os.Stat(filepath.Join(in.data, "release", c.to)); !os.IsNotExist(err) {
				t.Fatalf("the refused downgrade fetched the release checkout %s (err=%v)", c.to, err)
			}
			if got := w174ReadFile(t, in.cfg); got != cfgBefore {
				t.Fatalf("the refused downgrade rewrote the config")
			}
			if daemons := in.daemons(); len(daemons) != 0 {
				t.Fatalf("the refused downgrade started a daemon: %v", daemons)
			}
		})
	}
	for _, to := range []string{"v0.2.0", "v0.2.1"} {
		t.Run("accepted v2.20.22 -> "+to, func(t *testing.T) {
			in := w174NewInstall(t)
			in.release(t, to)
			installed(t, in, "v2.20.22")
			out, code := in.run(t)
			if code != 0 {
				t.Fatalf("the named cutover v2.20.22 -> %s MUST install; exit %d:\n%s", to, code, out)
			}
			got, err := exec.Command(in.bin, "--version").Output()
			if err != nil || strings.TrimSpace(string(got)) != to {
				t.Fatalf("pf after the cutover = %q (%v), want %s", got, err, to)
			}
		})
	}
}

// TestInstallSuffixVersionTransitions (AC1): tools/install.sh
// orders a suffixed release vX.Y.Z-<name>.N by SemVer 2.0 precedence — a
// pre-release is lower than the same core release, numeric identifiers
// compare as numbers — so the downgrade refusal holds across suffixed
// versions too. Each case names the row of the AC1 table it carries (the
// suffix name in the fixtures is the neutral `fork`). A refusal changes
// nothing (the binary, the config, the daemons stay as they were); an
// accepted transition installs the target. A non-release installed pf (dev,
// a --version that fails, none installed) has nothing to compare: allowed.
func TestInstallSuffixVersionTransitions(t *testing.T) {
	// installed writes the installed pf: none when script is empty.
	installed := func(t *testing.T, in *w174Install, script string) string {
		t.Helper()
		if script == "" {
			return ""
		}
		body := "#!/bin/sh\n" + script + "\nexit 0\n"
		if err := os.MkdirAll(filepath.Dir(in.bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(in.bin, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return body
	}
	reports := func(ver string) string { return "[ \"$1\" = --version ] && { echo " + ver + "; exit 0; }" }
	for _, c := range []struct {
		row      int
		from, to string
		script   string // the installed pf (empty: none installed)
		allowed  bool
	}{
		{1, "v0.2.0-fork.1", "v0.2.0-fork.2", reports("v0.2.0-fork.1"), true},
		{2, "v0.2.0-fork.2", "v0.2.0-fork.1", reports("v0.2.0-fork.2"), false},
		{3, "v0.2.0-fork.9", "v0.2.0-fork.10", reports("v0.2.0-fork.9"), true},
		{4, "v0.2.0-fork.5", "v0.2.1-fork.1", reports("v0.2.0-fork.5"), true},
		{5, "v0.2.1", "v0.2.0", reports("v0.2.1"), false},
		{6, "v0.2.12", "v0.2.0-fork.1", reports("v0.2.12"), false},
		{7, "dev", "v0.2.0-fork.1", reports("dev"), true},
		{7, "unknown", "v0.2.0-fork.1", "[ \"$1\" = --version ] && exit 1", true},
		{7, "none", "v0.2.0-fork.1", "", true},
	} {
		verdict := "refused"
		if c.allowed {
			verdict = "allowed"
		}
		t.Run(fmt.Sprintf("AC1 row %d %s %s -> %s", c.row, verdict, c.from, c.to), func(t *testing.T) {
			in := w174NewInstall(t)
			in.release(t, c.to)
			before := installed(t, in, c.script)
			cfgBefore := w174ReadFile(t, in.cfg)
			in.resetJournal(t)
			out, code := in.run(t)
			if c.allowed {
				if code != 0 || strings.Contains(out, "a downgrade is refused") {
					t.Fatalf("AC1 row %d: installing %s over pf %s MUST be allowed; exit %d:\n%s", c.row, c.to, c.from, code, out)
				}
				got, err := exec.Command(in.bin, "--version").Output()
				if err != nil || strings.TrimSpace(string(got)) != c.to {
					t.Fatalf("AC1 row %d: pf after installing %s over %s = %q (%v), want %s", c.row, c.to, c.from, got, err, c.to)
				}
				return
			}
			if code == 0 || !strings.Contains(out, "a downgrade is refused") || !strings.Contains(out, "this machine still runs pf "+c.from) {
				t.Fatalf("AC1 row %d: installing %s over pf %s MUST be refused before anything changes, saying what the machine runs; exit %d:\n%s", c.row, c.to, c.from, code, out)
			}
			if got := w174ReadFile(t, in.bin); got != before {
				t.Fatalf("AC1 row %d: the refused downgrade replaced the installed pf", c.row)
			}
			if left, _ := filepath.Glob(in.bin + ".new.*"); len(left) != 0 {
				t.Fatalf("AC1 row %d: a staged binary was left behind: %v", c.row, left)
			}
			if _, err := os.Stat(filepath.Join(in.data, "release", c.to)); !os.IsNotExist(err) {
				t.Fatalf("AC1 row %d: the refused downgrade fetched the release checkout %s (err=%v)", c.row, c.to, err)
			}
			if got := w174ReadFile(t, in.cfg); got != cfgBefore {
				t.Fatalf("AC1 row %d: the refused downgrade rewrote the config", c.row)
			}
			if daemons := in.daemons(); len(daemons) != 0 {
				t.Fatalf("AC1 row %d: the refused downgrade started a daemon: %v", c.row, daemons)
			}
		})
	}
}

// w344PiwebRelease adds the Pi Web tarball of tag to the fake release and its
// line to SHA256SUMS (corrupt: a wrong sum), and an npm shim first on PATH.
// The tarball is a real installable package: the tree of
// `node pi-web/e2e/lane-stubs.mjs piweb-package-tree <dir> --scripts=skipped`
// (the repository's bin/ with the install checker, a patch-package stub and
// a node-pty stand-in whose native module only its install script builds)
// plus a package.json carrying the repository's postinstall; treeFlags add
// fixture variants (`--node-pty=fail`). The npm shim models npm 11 skipping
// install scripts its allowScripts policy does not cover. Every call
// journals `npm <args> sha=<sha256 of the last argument>`; `root -g`
// answers the scratch prefix's lib/node_modules; `install -g <tgz>` exits
// W344_NPM_RC when non-zero, otherwise unpacks the tarball into the prefix,
// warns that the scripts are not covered by allowScripts, runs none and
// exits 0; `rebuild node-pty ...` runs node-pty's install script only when
// its argv carries both --ignore-scripts=false and
// --dangerously-allow-all-scripts (else it warns and runs nothing). The
// test's node is linked into the shim directory, so the checker and the
// fixture run on the harness PATH. Answers the scratch npm prefix.
func w344PiwebRelease(t *testing.T, in *w174Install, tag string, corrupt bool, treeFlags ...string) string {
	t.Helper()
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("w344: node is not on the test's PATH: %v", err)
	}
	if err := os.Symlink(nodeBin, filepath.Join(in.shim, "node")); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	root := v1Root(t)
	work := t.TempDir()
	pkgDir := filepath.Join(work, "package")
	args := append([]string{filepath.Join(root, "pi-web", "e2e", "lane-stubs.mjs"), "piweb-package-tree", pkgDir, "--scripts=skipped"}, treeFlags...)
	if out, err := exec.Command(nodeBin, args...).CombinedOutput(); err != nil {
		t.Fatalf("w344: lane-stubs piweb-package-tree: %v\n%s", err, out)
	}
	var repoPkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(w174ReadFile(t, filepath.Join(root, "pi-web", "package.json"))), &repoPkg); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]any{
		"name":    "@silgrid/pi-web",
		"version": strings.TrimPrefix(tag, "v"),
		"bin":     map[string]string{"pi-web": "bin/pi-web.js"},
		"scripts": map[string]string{"postinstall": repoPkg.Scripts["postinstall"]},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), append(manifest, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	tgz := "silgrid-pi-web-" + tag + ".tgz"
	if out, err := exec.Command("tar", "-czf", filepath.Join(in.rel, tgz), "-C", work, "package").CombinedOutput(); err != nil {
		t.Fatalf("w344: pack %s: %v\n%s", tgz, err, out)
	}
	line := w344TgzSum(t, in, tag)
	if corrupt {
		line = strings.Repeat("0", 64)
	}
	f, err := os.OpenFile(filepath.Join(in.rel, "SHA256SUMS"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "%s  %s\n", line, tgz)
	f.Close()
	prefix := filepath.Join(work, "npm-prefix")
	warn := "npm warn install-scripts 3 packages have install scripts not yet covered by allowScripts: " +
		"@silgrid/pi-web@" + strings.TrimPrefix(tag, "v") + " (postinstall: " + repoPkg.Scripts["postinstall"] + "), " +
		"node-pty@0.0.0-fixture (install: node scripts/install.js)"
	npm := "#!/bin/sh\n# w344/w452 npm shim: npm 11 skipping install scripts allowScripts does not cover\n" +
		"last=; for a; do last=$a; done\n" +
		"echo \"npm $* sha=$(sha256sum \"$last\" 2>/dev/null | cut -d' ' -f1)\" >> \"$W174_JOURNAL\"\n" +
		"prefix='" + prefix + "'\n" +
		"case \"$1\" in\n" +
		"root) printf '%s\\n' \"$prefix/lib/node_modules\"; exit 0 ;;\n" +
		"install)\n" +
		"  rc=${W344_NPM_RC:-0}; [ \"$rc\" = 0 ] || exit \"$rc\"\n" +
		"  pkg=\"$prefix/lib/node_modules/@silgrid/pi-web\"\n" +
		"  rm -rf \"$pkg\" && mkdir -p \"$pkg\" && tar -xzf \"$last\" --strip-components=1 -C \"$pkg\" || exit 1\n" +
		"  echo '" + warn + "' >&2\n" +
		"  exit 0 ;;\n" +
		"rebuild)\n" +
		"  ign=0; allow=0\n" +
		"  for a; do case \"$a\" in --ignore-scripts=false) ign=1 ;; --dangerously-allow-all-scripts) allow=1 ;; esac; done\n" +
		"  [ -d \"$PWD/node_modules/node-pty\" ] || exit 1\n" +
		"  if [ \"$ign\" = 1 ] && [ \"$allow\" = 1 ]; then cd \"$PWD/node_modules/node-pty\" && exec node scripts/install.js; fi\n" +
		"  echo 'npm warn install-scripts 1 package has install scripts not yet covered by allowScripts: node-pty@0.0.0-fixture (install: node scripts/install.js)' >&2\n" +
		"  exit 0 ;;\n" +
		"esac\n" +
		"exit ${W344_NPM_RC:-0}\n"
	if err := os.WriteFile(filepath.Join(in.shim, "npm"), []byte(npm), 0o755); err != nil {
		t.Fatal(err)
	}
	return prefix
}

// w344TgzSum answers the sha256 of the packed Pi Web tarball of tag.
func w344TgzSum(t *testing.T, in *w174Install, tag string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(in.rel, "silgrid-pi-web-"+tag+".tgz"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func w344NpmLines(in *w174Install) []string {
	var out []string
	for _, l := range in.journalLines() {
		if strings.HasPrefix(l, "npm ") {
			out = append(out, l)
		}
	}
	return out
}

// TestInstallWithPiWebOptIn: tools/install.sh installs pf
// only by default; --with-pi-web installs the Pi Web tarball of the SAME
// release after verifying its SHA256SUMS line (a corrupted sum refuses
// before anything is installed), and a failing npm install leaves pf as
// installed.
func TestInstallWithPiWebOptIn(t *testing.T) {
	t.Run("default installs pf only", func(t *testing.T) {
		in := w174NewInstall(t)
		in.release(t, "v0.2.0")
		w344PiwebRelease(t, in, "v0.2.0", false)
		out, code := in.run(t)
		if code != 0 {
			t.Fatalf("install exit %d:\n%s", code, out)
		}
		if n := w344NpmLines(in); len(n) != 0 {
			t.Fatalf("without --with-pi-web nothing of Pi Web may be installed: %v", n)
		}
		if !strings.Contains(out, "--with-pi-web") {
			t.Fatalf("the closing hint must name the opt-in flag:\n%s", out)
		}
	})
	t.Run("opt-in installs the same release's verified tarball", func(t *testing.T) {
		in := w174NewInstall(t)
		in.release(t, "v0.2.0")
		w344PiwebRelease(t, in, "v0.2.0", false)
		out, code := in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
		if code != 0 {
			t.Fatalf("install --with-pi-web exit %d:\n%s", code, out)
		}
		var installs []string
		for _, l := range w344NpmLines(in) {
			if strings.HasPrefix(l, "npm install -g ") {
				installs = append(installs, l)
			}
		}
		if len(installs) != 1 || !strings.Contains(installs[0], "/silgrid-pi-web-v0.2.0.tgz sha="+w344TgzSum(t, in, "v0.2.0")) {
			t.Fatalf("want exactly one npm install -g of the verified silgrid-pi-web-v0.2.0.tgz: %v\n%s", installs, out)
		}
		if got, err := exec.Command(in.bin, "--version").Output(); err != nil || strings.TrimSpace(string(got)) != "v0.2.0" {
			t.Fatalf("pf = %q (%v), want v0.2.0", got, err)
		}
	})
	t.Run("a corrupted sum refuses before anything is installed", func(t *testing.T) {
		in := w174NewInstall(t)
		in.release(t, "v0.2.0")
		w344PiwebRelease(t, in, "v0.2.0", true)
		cfgBefore := w174ReadFile(t, in.cfg)
		in.resetJournal(t)
		out, code := in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
		if code == 0 || !strings.Contains(out, "SHA mismatch on silgrid-pi-web-v0.2.0.tgz") {
			t.Fatalf("a corrupted Pi Web sum must refuse; exit %d:\n%s", code, out)
		}
		if n := w344NpmLines(in); len(n) != 0 {
			t.Fatalf("npm ran after the refusal: %v", n)
		}
		w218AssertMachineUntouched(t, in, "v0.2.0", cfgBefore)
	})
	t.Run("a failing npm install leaves pf installed", func(t *testing.T) {
		in := w174NewInstall(t)
		in.release(t, "v0.2.0")
		w344PiwebRelease(t, in, "v0.2.0", false)
		in.extraEnv = append(in.extraEnv, "W344_NPM_RC=1")
		out, code := in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
		if code == 0 || !strings.Contains(out, "npm install -g silgrid-pi-web-v0.2.0.tgz failed") {
			t.Fatalf("a failing Pi Web install must exit non-zero naming it; exit %d:\n%s", code, out)
		}
		if got, err := exec.Command(in.bin, "--version").Output(); err != nil || strings.TrimSpace(string(got)) != "v0.2.0" {
			t.Fatalf("pf must stay installed after the Pi Web failure: %q (%v)", got, err)
		}
	})
}

// TestInstallWithPiWebScriptsNotAllowed: npm 11 skips the
// install scripts its allowScripts policy does not cover, warns and still
// exits 0. tools/install.sh --with-pi-web MUST then confirm the scripts'
// effects itself (the patch applied, the terminal prepared, node-pty able to
// start a pty), whatever the machine's npm configuration, and when it cannot,
// exit non-zero naming the step, with pf as installed.
func TestInstallWithPiWebScriptsNotAllowed(t *testing.T) {
	t.Run("scripts skipped, the install check completes them", func(t *testing.T) {
		in := w174NewInstall(t)
		in.release(t, "v0.2.0")
		prefix := w344PiwebRelease(t, in, "v0.2.0", false)
		out, code := in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
		if code != 0 {
			t.Fatalf("install --with-pi-web with the install scripts skipped MUST complete them and exit 0; exit %d:\n%s", code, out)
		}
		for _, want := range []string{
			"==> Install check: patch-package ok",
			"==> Install check: node-pty ok (rebuilt)",
			"==> Install check: all steps ok",
			"Pi Web installed from silgrid-pi-web-v0.2.0.tgz",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("the install output MUST hold %q (the install check ran and passed); observed:\n%s", want, out)
			}
		}
		pkg := filepath.Join(prefix, "lib", "node_modules", "@silgrid", "pi-web")
		for _, p := range []string{
			filepath.Join(pkg, "node_modules", ".fixture", "patched"),
			filepath.Join(pkg, "node_modules", "node-pty", "build", "Release", "pty.node"),
		} {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("the install scripts' effect %s MUST exist after the install: %v\n%s", p, err, out)
			}
		}
		if got, err := exec.Command(in.bin, "--version").Output(); err != nil || strings.TrimSpace(string(got)) != "v0.2.0" {
			t.Fatalf("pf = %q (%v), want v0.2.0", got, err)
		}
	})
	t.Run("node-pty cannot be built, the install fails naming it", func(t *testing.T) {
		in := w174NewInstall(t)
		in.release(t, "v0.2.0")
		w344PiwebRelease(t, in, "v0.2.0", false, "--node-pty=fail")
		out, code := in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
		if code == 0 {
			t.Fatalf("install --with-pi-web where node-pty cannot be built MUST exit non-zero; observed exit 0:\n%s", out)
		}
		if !strings.Contains(out, "ERROR: Pi Web install check failed at node-pty:") {
			t.Fatalf("the failure MUST name the node-pty step; observed:\n%s", out)
		}
		if !strings.Contains(out, "pf v0.2.0, release/current and the daemon are installed and unchanged") {
			t.Fatalf("the failure MUST say pf v0.2.0 is installed and unchanged; observed:\n%s", out)
		}
		if strings.Contains(out, "Pi Web installed from") {
			t.Fatalf("a failed install check MUST NOT report Pi Web as installed; observed:\n%s", out)
		}
		if got, err := exec.Command(in.bin, "--version").Output(); err != nil || strings.TrimSpace(string(got)) != "v0.2.0" {
			t.Fatalf("pf must stay installed after the Pi Web failure: %q (%v)", got, err)
		}
	})
}
