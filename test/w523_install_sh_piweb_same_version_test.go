// carries: tools/install.sh, pi-web/bin/pi-web-served.js, pi-web/e2e/lane-stubs.mjs
package test

// `tools/install.sh --with-pi-web` where the release's Pi Web
// version is already installed skips `npm install -g`, so a running Pi Web
// keeps serving its package directory (npm would delete it under the
// server: blank pages, 5xx statics). Another installed version is replaced
// as before. `--reinstall-pi-web` (only together with --with-pi-web)
// reinstalls the same version; a Pi Web running over that reinstall is then
// stale by the same served == installed rule (its working directory was
// deleted), so it gets the ACTION line, or a restart with --restart-pi-web.
//
// Driven on the w451 world (hermetic w174 installer, private tmux server,
// the deploy's loop shape with a stub launcher and server, an npm shim that
// journals every call and renames then deletes the old tree). The verdicts
// come from the npm journal and the test's own probe (HTTP,
// /proc/<server>/cwd), never from the installer's account of itself.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// w523NpmInstalls answers the journaled `npm install -g` calls.
func w523NpmInstalls(w *w451World) []string {
	var got []string
	for _, l := range w344NpmLines(w.in) {
		if strings.HasPrefix(l, "npm install -g ") {
			got = append(got, l)
		}
	}
	return got
}

// w523InstalledVersion answers the version of the installed package.json.
func w523InstalledVersion(t *testing.T, w *w451World) string {
	t.Helper()
	var pkg struct {
		Version string `json:"version"`
	}
	raw := w174ReadFile(t, filepath.Join(w.installedDir(t), "package.json"))
	if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
		t.Fatalf("installed package.json: %v\n%s", err, raw)
	}
	return pkg.Version
}

// TestW523InstallPiWebSameVersionSkipsNpm: the running Pi Web serves the
// installed 0.2.0 and the release is 0.2.0 — no npm install, the service
// keeps serving its (undeleted) package directory healthily, no ACTION
// line, no restart, the piweb tmux session untouched.
func TestW523InstallPiWebSameVersionSkipsNpm(t *testing.T) {
	w := w451NewWorld(t, "v0.2.0", "v0.2.0")
	w.npmInstall(t, "v0.2.0")
	before := w.startLoop(t)
	w.in.resetJournal(t)
	out, code := w.in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
	if code != 0 {
		t.Fatalf("install --with-pi-web exit %d:\n%s", code, out)
	}
	if n := w523NpmInstalls(w); len(n) != 0 {
		t.Fatalf("the installed Pi Web is the release's version: npm install -g MUST NOT run: %v\n%s", n, out)
	}
	if cwd, ver := w451Served(before.Server); cwd != w.installedDir(t) || ver != "0.2.0" {
		t.Fatalf("the running server MUST keep serving the installed directory %s at 0.2.0: cwd %q version %q", w.installedDir(t), cwd, ver)
	}
	if ok, state := w.healthy(t, "0.2.0"); !ok {
		t.Fatalf("the running Pi Web MUST stay healthy: %s", state)
	}
	if acts := w451PiwebActions(out); len(acts) != 0 {
		t.Fatalf("a Pi Web serving the installed package: no ACTION line may appear: %v", acts)
	}
	if ps := w.procs(); len(ps) != 1 || !w451Alive(before.Launcher) || !w451Alive(before.Server) {
		t.Fatalf("the running Pi Web MUST NOT be restarted: procs %v (before %v)", ps, before)
	}
	if mut := w451TmuxMutations(w.in); len(mut) != 0 {
		t.Fatalf("the piweb tmux session may not be touched: %v", mut)
	}
	if !strings.Contains(out, "npm install -g skipped") {
		t.Fatalf("the run MUST say the npm install was skipped:\n%s", out)
	}
}

// TestW523InstallPiWebOtherVersionInstalls: another installed version is
// replaced by the release's tarball, as before.
func TestW523InstallPiWebOtherVersionInstalls(t *testing.T) {
	w := w451NewWorld(t, "v0.2.0", "v0.1.9", "v0.2.0")
	w.npmInstall(t, "v0.1.9")
	w.in.resetJournal(t)
	out, code := w.in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
	if code != 0 {
		t.Fatalf("install --with-pi-web exit %d:\n%s", code, out)
	}
	if n := w523NpmInstalls(w); len(n) != 1 || !strings.HasSuffix(n[0], "/silgrid-pi-web-v0.2.0.tgz") {
		t.Fatalf("want exactly one npm install -g of silgrid-pi-web-v0.2.0.tgz: %v\n%s", n, out)
	}
	if v := w523InstalledVersion(t, w); v != "0.2.0" {
		t.Fatalf("installed version %q, want 0.2.0", v)
	}
}

// TestW523InstallPiWebReinstallSwitch: --reinstall-pi-web reinstalls the
// same version, needs --with-pi-web, and leaves no running Pi Web silently
// stale.
func TestW523InstallPiWebReinstallSwitch(t *testing.T) {
	t.Run("reinstalls the same version", func(t *testing.T) {
		w := w451NewWorld(t, "v0.2.0", "v0.2.0")
		w.npmInstall(t, "v0.2.0")
		marker := filepath.Join(w.installedDir(t), "w523-marker")
		if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		w.in.resetJournal(t)
		out, code := w.in.runArgs(t, "--with-pi-web", "--reinstall-pi-web", "w174key", "127.0.0.1")
		if code != 0 {
			t.Fatalf("install --with-pi-web --reinstall-pi-web exit %d:\n%s", code, out)
		}
		if n := w523NpmInstalls(w); len(n) != 1 || !strings.HasSuffix(n[0], "/silgrid-pi-web-v0.2.0.tgz") {
			t.Fatalf("--reinstall-pi-web MUST run exactly one npm install -g of silgrid-pi-web-v0.2.0.tgz: %v\n%s", n, out)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("--reinstall-pi-web MUST replace the package directory: %s still there (err %v)", marker, err)
		}
		if v := w523InstalledVersion(t, w); v != "0.2.0" {
			t.Fatalf("installed version %q, want 0.2.0", v)
		}
	})
	t.Run("needs --with-pi-web", func(t *testing.T) {
		w := w451NewWorld(t, "v0.2.0", "v0.2.0")
		cfgBefore := w174ReadFile(t, w.in.cfg)
		w.in.resetJournal(t)
		out, code := w.in.runArgs(t, "--reinstall-pi-web", "w174key", "127.0.0.1")
		if code != 2 || !strings.Contains(out, "--reinstall-pi-web") {
			t.Fatalf("--reinstall-pi-web alone MUST exit 2 naming the switch; exit %d:\n%s", code, out)
		}
		if n := w344NpmLines(w.in); len(n) != 0 {
			t.Fatalf("npm ran after the refusal: %v", n)
		}
		w218AssertMachineUntouched(t, w.in, "v0.2.0", cfgBefore)
	})
	t.Run("reinstall over a running Pi Web", func(t *testing.T) {
		w := w451NewWorld(t, "v0.2.0", "v0.2.0")
		w.npmInstall(t, "v0.2.0")
		before := w.startLoop(t)
		w.in.resetJournal(t)
		out, code := w.in.runArgs(t, "--with-pi-web", "--reinstall-pi-web", "w174key", "127.0.0.1")
		if code != 0 {
			t.Fatalf("install --with-pi-web --reinstall-pi-web exit %d:\n%s", code, out)
		}
		if n := w523NpmInstalls(w); len(n) != 1 {
			t.Fatalf("--reinstall-pi-web MUST run exactly one npm install -g: %v\n%s", n, out)
		}
		if cwd, _ := w451Served(before.Server); cwd == w.installedDir(t) {
			t.Fatalf("precondition: the reinstall deleted the running server's directory (cwd %q)", cwd)
		}
		acts := w451PiwebActions(out)
		if len(acts) != 1 {
			t.Fatalf("a Pi Web left serving the deleted tree MUST get exactly one ACTION line; got %d:\n%s", len(acts), out)
		}
		if m := w451KillCmd.FindStringSubmatch(acts[0]); m == nil || m[1] != strconv.Itoa(before.Launcher) {
			t.Fatalf("the ACTION line MUST name kill -TERM %d:\n%s", before.Launcher, acts[0])
		}
		if ps := w.procs(); len(ps) != 1 || !w451Alive(before.Launcher) || !w451Alive(before.Server) {
			t.Fatalf("without --restart-pi-web Pi Web MUST NOT be restarted: procs %v (before %v)", ps, before)
		}
	})
	t.Run("reinstall and restart a running Pi Web", func(t *testing.T) {
		w := w451NewWorld(t, "v0.2.0", "v0.2.0")
		w.npmInstall(t, "v0.2.0")
		before := w.startLoop(t)
		out, code := w.in.runArgs(t, "--with-pi-web", "--reinstall-pi-web", "--restart-pi-web", "w174key", "127.0.0.1")
		if code != 0 {
			t.Fatalf("install --with-pi-web --reinstall-pi-web --restart-pi-web exit %d:\n%s", code, out)
		}
		ps := w.procs()
		if len(ps) < 2 || ps[len(ps)-1].Launcher == before.Launcher {
			t.Fatalf("--restart-pi-web MUST restart the Pi Web the reinstall left stale: procs %v (before %v)\n%s", ps, before, out)
		}
		w.awaitHealthy(t, "0.2.0", 30*time.Second)
		if acts := w451PiwebActions(out); len(acts) != 0 {
			t.Fatalf("a restarted Pi Web needs no ACTION line: %v", acts)
		}
	})
}
