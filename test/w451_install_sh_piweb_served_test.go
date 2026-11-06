// carries: tools/install.sh, pi-web/bin/pi-web-served.js, pi-web/e2e/lane-stubs.mjs
package test

// `tools/install.sh --with-pi-web` on a machine where Pi Web
// is running. npm replaces the package directory under the running server
// (it renames the old tree to @silgrid/.pi-web-XXXX and deletes it), so the
// old server keeps rendering HTML that names its own build's /_next/static
// files, which no longer exist: blank pages and 5xx until it restarts. The
// installer MUST make that loud — one `ACTION:` line naming the served and
// installed versions, the consequence and a runnable restart command — and
// MUST NOT restart Pi Web on its own; `--restart-pi-web` restarts it and
// waits until the installed package is served. "Stale" is the deploy's
// served == installed definition (pi-web/bin/pi-web-served.js, shared with
// pi-web/deploy-piweb.sh). No ACTION line and no restart when no Pi Web
// runs, or when the running one already serves the installed package.
//
// Driven on the hermetic w174 installer world (scratch HOME/PF_BIN, private
// tmux server, stubbed gh). The running Pi Web is a real process tree in the
// private tmux session `piweb`, window `piweb`, under the deploy's loop
// shape: a stub launcher (bin/pi-web.js) that spawns a stub server with its
// package directory as cwd, as the real launcher does; the server answers
// `GET /` with HTML naming /_next/static/<its BUILD_ID>/app.js and serves
// /_next/static/* from the package path it captured at start (500 when the
// file is missing), which is Next's observed behaviour after the swap. The
// verdicts come from the test's own probe of that service (HTTP, and
// /proc/<server>/cwd with its package.json), never from installer output.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// w451World is one installer machine with a scratch npm prefix.
type w451World struct {
	in      *w174Install
	prefix  string // the npm global prefix the npm shim installs into
	procLog string // one JSON line {launcher, server} per launcher start
	port    int
}

const w451Launcher = `#!/usr/bin/env node
"use strict";
// w451 stub launcher: spawns the stub server with the package directory as
// its cwd (as bin/pi-web.js spawns Next), forwards SIGTERM, journals pids.
const { spawn } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const pkgDir = path.dirname(__dirname);
const port = process.argv[process.argv.indexOf("--port") + 1];
const child = spawn(process.execPath, [path.join(pkgDir, "bin", "w451-server.js"), port], { cwd: pkgDir, stdio: "inherit" });
fs.appendFileSync(process.env.W451_PROC_LOG, JSON.stringify({ launcher: process.pid, server: child.pid }) + "\n");
process.on("SIGTERM", () => child.kill("SIGTERM"));
child.on("exit", (code) => process.exit(code === null ? 0 : code));
`

const w451Server = `"use strict";
// w451 stub server: the build it started with names its static files; they
// are served from the package path captured at start (500 when missing).
const fs = require("node:fs");
const http = require("node:http");
const path = require("node:path");
const root = process.cwd();
const build = fs.readFileSync(path.join(root, ".next", "BUILD_ID"), "utf8").trim();
http.createServer((req, res) => {
  if (req.url === "/") {
    res.writeHead(200, { "content-type": "text/html" });
    res.end('<html><head><script src="/_next/static/' + build + '/app.js"></script></head><body></body></html>');
    return;
  }
  if (req.url.startsWith("/_next/static/")) {
    let body;
    try { body = fs.readFileSync(path.join(root, ".next", "static", req.url.slice("/_next/static/".length))); } catch { res.writeHead(500); res.end("missing"); return; }
    res.writeHead(200); res.end(body);
    return;
  }
  res.writeHead(404); res.end();
}).listen(Number(process.argv[2]), "127.0.0.1");
process.on("SIGTERM", () => process.exit(0));
`

// w451NewWorld lays out the machine: release tag (pf + the Pi Web tarballs
// of each pack tag), node and the npm shim on the installer's PATH. The npm
// shim models npm's global install on one prefix: `root -g` answers
// <prefix>/lib/node_modules; `install -g <tgz>` renames an existing
// @silgrid/pi-web to @silgrid/.pi-web-<n>, unpacks the new tree, links
// <prefix>/bin/pi-web to its bin/pi-web.js, then deletes the renamed tree.
func w451NewWorld(t *testing.T, tag string, packs ...string) *w451World {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("w451 reads /proc")
	}
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	in := w174NewInstall(t)
	in.release(t, tag)
	if err := os.Symlink(nodeBin, filepath.Join(in.shim, "node")); err != nil {
		t.Fatal(err)
	}
	w := &w451World{in: in, prefix: filepath.Join(t.TempDir(), "npm-prefix"), procLog: filepath.Join(t.TempDir(), "procs.jsonl")}
	in.extraEnv = append(in.extraEnv, "PIWEB_TMUX_SESSION=piweb")
	for _, p := range packs {
		w.pack(t, nodeBin, p)
	}
	npm := "#!/bin/sh\n# w451 npm shim: a global install that renames, then deletes, the old tree\n" +
		"last=; for a; do last=$a; done\n" +
		"echo \"npm $*\" >> \"$W174_JOURNAL\"\n" +
		"prefix='" + w.prefix + "'\n" +
		"case \"$1\" in\n" +
		"root) printf '%s\\n' \"$prefix/lib/node_modules\"; exit 0 ;;\n" +
		"install)\n" +
		"  mods=\"$prefix/lib/node_modules/@silgrid\"; pkg=\"$mods/pi-web\"; old=\n" +
		"  mkdir -p \"$mods\" \"$prefix/bin\" || exit 1\n" +
		"  if [ -e \"$pkg\" ]; then old=\"$mods/.pi-web-$$\"; mv \"$pkg\" \"$old\" || exit 1; fi\n" +
		"  mkdir -p \"$pkg\" && tar -xzf \"$last\" --strip-components=1 -C \"$pkg\" || exit 1\n" +
		"  ln -sfn ../lib/node_modules/@silgrid/pi-web/bin/pi-web.js \"$prefix/bin/pi-web\" || exit 1\n" +
		"  [ -z \"$old\" ] || rm -rf \"$old\"\n" +
		"  exit 0 ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(in.shim, "npm"), []byte(npm), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	w.port = l.Addr().(*net.TCPAddr).Port
	l.Close()
	// registered after the w174 teardown, so it runs first: the Pi Web
	// processes are not the tmux server's to reap once it is killed
	t.Cleanup(func() {
		for _, p := range w.procs() {
			_ = syscall.Kill(p.Launcher, syscall.SIGKILL)
			_ = syscall.Kill(p.Server, syscall.SIGKILL)
		}
	})
	return w
}

// pack writes silgrid-pi-web-<tag>.tgz (the repository's bin/ from
// lane-stubs, the stub launcher and server, a per-tag build) and its
// SHA256SUMS line.
func (w *w451World) pack(t *testing.T, nodeBin, tag string) {
	t.Helper()
	root := v1Root(t)
	work := t.TempDir()
	pkgDir := filepath.Join(work, "package")
	if out, err := exec.Command(nodeBin, filepath.Join(root, "pi-web", "e2e", "lane-stubs.mjs"), "piweb-package-tree", pkgDir, "--scripts=ran").CombinedOutput(); err != nil {
		t.Fatalf("lane-stubs piweb-package-tree: %v\n%s", err, out)
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
	build := "build-" + strings.ReplaceAll(tag, ".", "-")
	files := map[string]string{
		"package.json":                      string(manifest) + "\n",
		"bin/pi-web.js":                     w451Launcher,
		"bin/w451-server.js":                w451Server,
		".next/BUILD_ID":                    build + "\n",
		".next/static/" + build + "/app.js": "/* " + tag + " */\n",
	}
	for rel, body := range files {
		p := filepath.Join(pkgDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tgz := "silgrid-pi-web-" + tag + ".tgz"
	if out, err := exec.Command("tar", "-czf", filepath.Join(w.in.rel, tgz), "-C", work, "package").CombinedOutput(); err != nil {
		t.Fatalf("pack %s: %v\n%s", tgz, err, out)
	}
	f, err := os.OpenFile(filepath.Join(w.in.rel, "SHA256SUMS"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "%s  %s\n", w344TgzSum(t, w.in, tag), tgz)
	f.Close()
}

// npmInstall installs the tarball of tag the way an earlier run did (the
// npm shim, outside the installer).
func (w *w451World) npmInstall(t *testing.T, tag string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(w.in.shim, "npm"), "install", "-g", filepath.Join(w.in.rel, "silgrid-pi-web-"+tag+".tgz"))
	cmd.Env = append(w.in.env(), "W174_JOURNAL="+w.in.journal)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm install -g %s: %v\n%s", tag, err, out)
	}
}

// installedDir is the package directory npm installed (symlinks resolved).
func (w *w451World) installedDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(filepath.Join(w.prefix, "lib", "node_modules", "@silgrid", "pi-web"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

type w451Procs struct {
	Launcher int `json:"launcher"`
	Server   int `json:"server"`
}

func (w *w451World) procs() []w451Procs {
	raw, _ := os.ReadFile(w.procLog)
	var out []w451Procs
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var p w451Procs
		if json.Unmarshal([]byte(l), &p) == nil && p.Launcher > 0 {
			out = append(out, p)
		}
	}
	return out
}

// startLoop starts the deploy's loop shape in tmux session `piweb`, window
// `piweb`, and waits until its server answers GET / with 200.
func (w *w451World) startLoop(t *testing.T) w451Procs {
	t.Helper()
	loop := fmt.Sprintf("while true; do W451_PROC_LOG='%s' PATH='%s/bin:%s' pi-web --port %d; sleep 1; done", w.procLog, w.prefix, w.in.path, w.port)
	if out, err := w.in.tmux("new-session", "-d", "-s", "piweb", "-n", "piweb", loop); err != nil {
		t.Fatalf("tmux new-session piweb: %v %s", err, out)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if ps := w.procs(); len(ps) == 1 {
			if code, _ := w.probe(); code == 200 {
				return ps[0]
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the piweb loop did not come up: procs %v", w.procs())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

var w451StaticRef = regexp.MustCompile(`/_next/static/[^"' >]+`)

// probe is the external judgement: GET / and every /_next/static/* its HTML
// references; answers the / status and each reference's status.
func (w *w451World) probe() (int, map[string]int) {
	c := http.Client{Timeout: 3 * time.Second}
	base := "http://127.0.0.1:" + strconv.Itoa(w.port)
	resp, err := c.Get(base + "/")
	if err != nil {
		return 0, nil
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	statics := map[string]int{}
	for _, ref := range w451StaticRef.FindAllString(string(body), -1) {
		code := 0
		if r, err := c.Get(base + ref); err == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			code = r.StatusCode
		}
		statics[ref] = code
	}
	return resp.StatusCode, statics
}

// served answers the server process's cwd (as /proc shows it) and the
// version of the package.json there ("" when unreadable).
func w451Served(pid int) (string, string) {
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return "", ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	raw, err := os.ReadFile(filepath.Join(cwd, "package.json"))
	if err != nil || json.Unmarshal(raw, &pkg) != nil {
		return cwd, ""
	}
	return cwd, pkg.Version
}

func w451Alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// piwebActions answers the installer's ACTION lines about Pi Web.
func w451PiwebActions(out string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		low := strings.ToLower(l)
		if strings.Contains(l, "ACTION:") && (strings.Contains(low, "pi-web") || strings.Contains(low, "pi web")) {
			got = append(got, l)
		}
	}
	return got
}

// w451TmuxMutations answers the journaled tmux calls that would start,
// stop or replace a session, window or pane of `piweb`.
func w451TmuxMutations(in *w174Install) []string {
	var got []string
	for _, l := range in.journalLines() {
		if !strings.HasPrefix(l, "tmux ") || !strings.Contains(l, "piweb") {
			continue
		}
		for _, verb := range []string{"kill-", "respawn-", "new-", "send-keys"} {
			if strings.Contains(l, " "+verb) {
				got = append(got, l)
			}
		}
	}
	return got
}

// healthy reports whether the service serves the installed package now:
// the newest server's cwd is the installed directory at version want, / is
// 200 and every static reference is non-5xx.
func (w *w451World) healthy(t *testing.T, want string) (bool, string) {
	t.Helper()
	ps := w.procs()
	if len(ps) == 0 {
		return false, "no server"
	}
	cwd, ver := w451Served(ps[len(ps)-1].Server)
	code, statics := w.probe()
	ok := cwd == w.installedDir(t) && ver == want && code == 200 && len(statics) > 0
	for _, c := range statics {
		if c == 0 || c >= 500 {
			ok = false
		}
	}
	return ok, fmt.Sprintf("cwd=%q version=%q GET /=%d statics=%v", cwd, ver, code, statics)
}

func (w *w451World) awaitHealthy(t *testing.T, want string, bound time.Duration) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		ok, state := w.healthy(t, want)
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the service did not come to serve the installed %s within %s: %s", want, bound, state)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

var w451KillCmd = regexp.MustCompile(`kill -TERM (\d+)`)

// TestW451InstallWithPiWebStale (AC1, AC2): a Pi Web installed from the
// older tarball runs; the default `--with-pi-web` run prints exactly one
// ACTION line naming the served old version, the installed new one, the
// consequence and a runnable restart command, while the service, probed at
// that moment, still serves the old (deleted) tree with 5xx statics; no
// process was restarted. Running the printed command recovers the service.
func TestW451InstallWithPiWebStale(t *testing.T) {
	w := w451NewWorld(t, "v0.2.0", "v0.1.9", "v0.2.0")
	w.npmInstall(t, "v0.1.9")
	before := w.startLoop(t)
	w.in.resetJournal(t)
	out, code := w.in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
	if code != 0 {
		t.Fatalf("install --with-pi-web exit %d:\n%s", code, out)
	}
	// the external judgement, at the same moment
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
	acts := w451PiwebActions(out)
	if len(acts) != 1 {
		t.Fatalf("a stale running Pi Web MUST get exactly one ACTION line; got %d:\n%s", len(acts), out)
	}
	act := acts[0]
	for _, want := range []string{"0.1.9", "0.2.0", "blank", "/_next/static/*", "5xx"} {
		if !strings.Contains(act, want) {
			t.Fatalf("the ACTION line MUST hold %q:\n%s", want, act)
		}
	}
	m := w451KillCmd.FindStringSubmatch(act)
	if m == nil || m[1] != strconv.Itoa(before.Launcher) {
		t.Fatalf("the ACTION line MUST name the restart command kill -TERM %d:\n%s", before.Launcher, act)
	}
	if ps := w.procs(); len(ps) != 1 || !w451Alive(before.Launcher) || !w451Alive(before.Server) {
		t.Fatalf("the default run MUST NOT restart Pi Web: procs %v (before %v)", ps, before)
	}
	if mut := w451TmuxMutations(w.in); len(mut) != 0 {
		t.Fatalf("the default run MUST NOT touch the piweb tmux session: %v", mut)
	}
	// the printed command is directly runnable and recovers the service
	if out, err := exec.Command("sh", "-c", m[0]).CombinedOutput(); err != nil {
		t.Fatalf("the printed restart command %q failed: %v %s", m[0], err, out)
	}
	w.awaitHealthy(t, "0.2.0", 30*time.Second)
}

// TestW451InstallWithPiWebNoPiWeb (AC4a): no Pi Web runs — no ACTION line
// about it and nothing of tmux piweb is started or stopped.
func TestW451InstallWithPiWebNoPiWeb(t *testing.T) {
	w := w451NewWorld(t, "v0.2.0", "v0.2.0")
	w.in.resetJournal(t)
	out, code := w.in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
	if code != 0 {
		t.Fatalf("install --with-pi-web exit %d:\n%s", code, out)
	}
	if acts := w451PiwebActions(out); len(acts) != 0 {
		t.Fatalf("no running Pi Web: no ACTION line about it may appear: %v", acts)
	}
	if mut := w451TmuxMutations(w.in); len(mut) != 0 {
		t.Fatalf("no running Pi Web: nothing of piweb may be started or stopped: %v", mut)
	}
	if ps := w.procs(); len(ps) != 0 {
		t.Fatalf("no Pi Web may be started: %v", ps)
	}
}

// TestW451InstallWithPiWebFresh (AC4b): the running Pi Web already serves
// the release being installed — no ACTION line, no restart.
func TestW451InstallWithPiWebFresh(t *testing.T) {
	w := w451NewWorld(t, "v0.2.0", "v0.2.0")
	w.npmInstall(t, "v0.2.0")
	before := w.startLoop(t)
	w.in.resetJournal(t)
	out, code := w.in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
	if code != 0 {
		t.Fatalf("install --with-pi-web exit %d:\n%s", code, out)
	}
	if acts := w451PiwebActions(out); len(acts) != 0 {
		t.Fatalf("a Pi Web already serving the installed package: no ACTION line may appear: %v", acts)
	}
	if ps := w.procs(); len(ps) != 1 || !w451Alive(before.Launcher) || !w451Alive(before.Server) {
		t.Fatalf("a fresh Pi Web MUST NOT be restarted: procs %v (before %v)", ps, before)
	}
	if mut := w451TmuxMutations(w.in); len(mut) != 0 {
		t.Fatalf("a fresh Pi Web: the piweb tmux session may not be touched: %v", mut)
	}
}

// TestW451InstallRestartPiWeb (AC3, AC4): `--restart-pi-web` restarts a
// stale running Pi Web and returns once the installed package is served —
// judged by the test's own probe: the server's cwd is the installed
// directory at the installed version, GET / answers 200 and every
// /_next/static/* it references is non-5xx. With no running Pi Web, or a
// fresh one, the switch restarts nothing and prints no ACTION line; without
// --with-pi-web it is refused before anything changes.
func TestW451InstallRestartPiWeb(t *testing.T) {
	t.Run("stale is restarted onto the install", func(t *testing.T) {
		w := w451NewWorld(t, "v0.2.0", "v0.1.9", "v0.2.0")
		w.npmInstall(t, "v0.1.9")
		before := w.startLoop(t)
		out, code := w.in.runArgs(t, "--with-pi-web", "--restart-pi-web", "w174key", "127.0.0.1")
		if code != 0 {
			t.Fatalf("install --with-pi-web --restart-pi-web exit %d:\n%s", code, out)
		}
		ps := w.procs()
		if len(ps) < 2 || ps[len(ps)-1].Launcher == before.Launcher {
			t.Fatalf("--restart-pi-web MUST restart the stale Pi Web: procs %v (before %v)\n%s", ps, before, out)
		}
		if cwd, ver := w451Served(ps[len(ps)-1].Server); cwd != w.installedDir(t) || ver != "0.2.0" {
			t.Fatalf("at exit the served package MUST be the installed 0.2.0 (%s): cwd %q version %q\n%s", w.installedDir(t), cwd, ver, out)
		}
		w.awaitHealthy(t, "0.2.0", 30*time.Second)
		if acts := w451PiwebActions(out); len(acts) != 0 {
			t.Fatalf("a restarted Pi Web needs no ACTION line: %v", acts)
		}
	})
	t.Run("no running pi-web: nothing restarted", func(t *testing.T) {
		w := w451NewWorld(t, "v0.2.0", "v0.2.0")
		w.in.resetJournal(t)
		out, code := w.in.runArgs(t, "--with-pi-web", "--restart-pi-web", "w174key", "127.0.0.1")
		if code != 0 {
			t.Fatalf("install --with-pi-web --restart-pi-web exit %d:\n%s", code, out)
		}
		if acts := w451PiwebActions(out); len(acts) != 0 {
			t.Fatalf("no running Pi Web: no ACTION line about it may appear: %v", acts)
		}
		if mut := w451TmuxMutations(w.in); len(mut) != 0 || len(w.procs()) != 0 {
			t.Fatalf("no running Pi Web: nothing may be started or stopped: %v %v", mut, w.procs())
		}
	})
	t.Run("fresh pi-web: nothing restarted", func(t *testing.T) {
		w := w451NewWorld(t, "v0.2.0", "v0.2.0")
		w.npmInstall(t, "v0.2.0")
		before := w.startLoop(t)
		out, code := w.in.runArgs(t, "--with-pi-web", "--restart-pi-web", "w174key", "127.0.0.1")
		if code != 0 {
			t.Fatalf("install --with-pi-web --restart-pi-web exit %d:\n%s", code, out)
		}
		if acts := w451PiwebActions(out); len(acts) != 0 {
			t.Fatalf("a fresh Pi Web: no ACTION line may appear: %v", acts)
		}
		if ps := w.procs(); len(ps) != 1 || !w451Alive(before.Launcher) || !w451Alive(before.Server) {
			t.Fatalf("a fresh Pi Web MUST NOT be restarted: procs %v (before %v)\n%s", ps, before, out)
		}
	})
	t.Run("without --with-pi-web it is refused before any change", func(t *testing.T) {
		w := w451NewWorld(t, "v0.2.0", "v0.2.0")
		cfgBefore := w174ReadFile(t, w.in.cfg)
		w.in.resetJournal(t)
		out, code := w.in.runArgs(t, "--restart-pi-web", "w174key", "127.0.0.1")
		if code != 2 || !strings.Contains(out, "--restart-pi-web") {
			t.Fatalf("--restart-pi-web alone MUST exit 2 naming the switch; exit %d:\n%s", code, out)
		}
		w218AssertMachineUntouched(t, w.in, "v0.2.0", cfgBefore)
	})
}

// TestW451StaleActionNamesTheSwitchForTheNextUpgrade (review r1#1, owner
// ruling 2026-11-12T08:14:12): the ACTION line MUST NOT advise a re-run with
// --restart-pi-web; it names the switch for the next upgrade, and
// kill -TERM <pid> stays the fix. That wording stays pinned. The limit this
// test also pinned, a re-run with the switch restarting nothing (the old
// argv judgement saw the stale process as fresh), is superseded by
// the judgement is now the server's working directory, and
// TestW512AlreadyStalePiWebIsDetected pins that a re-run detects and, with
// the switch, restarts it.
func TestW451StaleActionNamesTheSwitchForTheNextUpgrade(t *testing.T) {
	w := w451NewWorld(t, "v0.2.0", "v0.1.9", "v0.2.0")
	w.npmInstall(t, "v0.1.9")
	before := w.startLoop(t)
	out, code := w.in.runArgs(t, "--with-pi-web", "w174key", "127.0.0.1")
	if code != 0 {
		t.Fatalf("install --with-pi-web exit %d:\n%s", code, out)
	}
	acts := w451PiwebActions(out)
	if len(acts) != 1 {
		t.Fatalf("want one pi-web ACTION line, got %d:\n%s", len(acts), out)
	}
	if strings.Contains(acts[0], "re-run") {
		t.Fatalf("the ACTION line MUST NOT advise a re-run (owner ruling: it names the switch for the next upgrade):\n%s", acts[0])
	}
	if !strings.Contains(acts[0], "(next time, pass --restart-pi-web with --with-pi-web to restart during the upgrade)") {
		t.Fatalf("the ACTION line MUST name the switch for the next upgrade:\n%s", acts[0])
	}
	if !strings.Contains(acts[0], "kill -TERM "+strconv.Itoa(before.Launcher)) {
		t.Fatalf("kill -TERM %d MUST stay the fix:\n%s", before.Launcher, acts[0])
	}
}
