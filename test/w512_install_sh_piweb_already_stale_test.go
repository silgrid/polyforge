// carries: tools/install.sh, pi-web/bin/pi-web-served.js, pi-web/e2e/lane-stubs.mjs
package test

// a Pi Web that was already stale before this
// `tools/install.sh --with-pi-web` run — an earlier run's ACTION line was
// ignored, or the package was upgraded with a manual `npm i -g` — is
// detected too. The served == installed judgement (pi-web/bin/pi-web-served.js,
// shared with pi-web/deploy-piweb.sh) is the working directory of the
// launcher's server, not the launcher's argv path, which resolves to the new
// tree once npm has replaced the package. Such a run prints the ACTION line
// and restarts nothing; with --restart-pi-web it restarts the stale Pi Web
// and waits until the installed package is served.
//
// Driven on the w451 world (hermetic w174 installer, private tmux server, the
// deploy's loop shape with a stub launcher and server, an npm shim that
// renames then deletes the old tree). The verdicts come from the test's own
// probe (HTTP, /proc/<server>/cwd), never from installer output. The served
// version the ACTION line names is not pinned: npm deleted that tree and its
// package.json with it.

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// w512AssertStale is the external precondition: the running server's cwd is
// not the installed directory and its pages' /_next/static/* answer 5xx.
func w512AssertStale(t *testing.T, w *w451World, before w451Procs) {
	t.Helper()
	cwd, _ := w451Served(before.Server)
	_, statics := w.probe()
	broken := 0
	for _, c := range statics {
		if c >= 500 {
			broken++
		}
	}
	if cwd == w.installedDir(t) || broken == 0 {
		t.Fatalf("precondition: the probe must see served != installed with 5xx statics (cwd %q, installed %q, statics %v)", cwd, w.installedDir(t), statics)
	}
}

// w512AssertDetected: a default --with-pi-web run over an already-stale Pi
// Web prints exactly one ACTION line naming the installed version, the
// consequence and kill -TERM <launcher>, restarts nothing and does not touch
// the piweb tmux session.
func w512AssertDetected(t *testing.T, w *w451World, before w451Procs) {
	t.Helper()
	w.in.resetJournal(t)
	out, code := w.in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
	if code != 0 {
		t.Fatalf("install --with-pi-web exit %d:\n%s", code, out)
	}
	acts := w451PiwebActions(out)
	if len(acts) != 1 {
		t.Fatalf("an already-stale running Pi Web MUST get exactly one ACTION line; got %d:\n%s", len(acts), out)
	}
	for _, want := range []string{"0.2.0", "blank", "/_next/static/*", "5xx", "kill -TERM " + strconv.Itoa(before.Launcher)} {
		if !strings.Contains(acts[0], want) {
			t.Fatalf("the ACTION line MUST hold %q:\n%s", want, acts[0])
		}
	}
	if ps := w.procs(); len(ps) != 1 || !w451Alive(before.Launcher) || !w451Alive(before.Server) {
		t.Fatalf("the default run MUST NOT restart Pi Web: procs %v (before %v)", ps, before)
	}
	if mut := w451TmuxMutations(w.in); len(mut) != 0 {
		t.Fatalf("the default run MUST NOT touch the piweb tmux session: %v", mut)
	}
}

// w512AssertRestarted: --with-pi-web --restart-pi-web over an already-stale
// Pi Web exits 0 with a new launcher, which serves the installed 0.2.0
// healthily, and prints no ACTION line.
func w512AssertRestarted(t *testing.T, w *w451World, before w451Procs) {
	t.Helper()
	out, code := w.in.runArgs(t, "--with-pi-web", "--restart-pi-web", "w174key", "127.0.0.1")
	if code != 0 {
		t.Fatalf("install --with-pi-web --restart-pi-web exit %d:\n%s", code, out)
	}
	if ps := w.procs(); len(ps) < 2 || ps[len(ps)-1].Launcher == before.Launcher {
		t.Fatalf("--restart-pi-web MUST restart the already-stale Pi Web: procs %v (before %v)\n%s", ps, before, out)
	}
	w.awaitHealthy(t, "0.2.0", 30*time.Second)
	if acts := w451PiwebActions(out); len(acts) != 0 {
		t.Fatalf("a restarted Pi Web needs no ACTION line: %v", acts)
	}
}

// TestW512AlreadyStalePiWebIsDetected (AC2): install the new version without
// restarting Pi Web, then run install.sh again: the stale Pi Web is detected
// (ACTION line), and with --restart-pi-web it is restarted.
func TestW512AlreadyStalePiWebIsDetected(t *testing.T) {
	t.Run("an ignored ACTION", func(t *testing.T) {
		w := w451NewWorld(t, "v0.2.0", "v0.1.9", "v0.2.0")
		w.npmInstall(t, "v0.1.9")
		before := w.startLoop(t)
		out, code := w.in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
		if code != 0 {
			t.Fatalf("install --with-pi-web exit %d:\n%s", code, out)
		}
		if acts := w451PiwebActions(out); len(acts) != 1 {
			t.Fatalf("precondition: the upgrading run prints the ACTION line (ignored here); got %d:\n%s", len(acts), out)
		}
		w512AssertStale(t, w, before)
		w512AssertDetected(t, w, before)
		w512AssertRestarted(t, w, before)
	})
	t.Run("a manual npm install", func(t *testing.T) {
		w := w451NewWorld(t, "v0.2.0", "v0.1.9", "v0.2.0")
		w.npmInstall(t, "v0.1.9")
		before := w.startLoop(t)
		w.npmInstall(t, "v0.2.0")
		w512AssertStale(t, w, before)
		w512AssertDetected(t, w, before)
		w512AssertRestarted(t, w, before)
	})
}
