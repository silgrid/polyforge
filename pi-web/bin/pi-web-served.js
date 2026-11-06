"use strict";

// Served == installed: the one definition of "the pi-web the service runs is
// the package npm installed", shared by
// deploy-piweb.sh and tools/install.sh --with-pi-web. The
// served package is the working directory of the launcher's server
// bin/pi-web.js runs Next's server with the package
// directory as its cwd, and npm replaces that directory on every install
// (it renames the old tree and deletes it), so the server of a pi-web that
// was not restarted keeps a deleted cwd, however it became stale.
//
//   node pi-web-served.js served <tmux-session>
//     Finds the launcher among the processes of the session's 'piweb' window
//     (each pane and its direct children): the first process carrying an
//     absolute `/…/pi-web` or `/…/pi-web.js` argv token. It judges the
//     launcher's child, Next's server, by its working directory:
//       `<version>|<dir>|<launcher pid>` when the cwd is a live
//         @silgrid/pi-web package directory (its own package.json);
//       `unknown|<dir> (deleted)|<launcher pid>` when that directory has been
//         removed: stale whatever the version, its version is gone with it;
//       `unidentified` when the window runs a process without such a token,
//         or the server's cwd is not a @silgrid/pi-web package;
//       nothing while there is no session, no window, no process or no
//         server yet.
//     The pid is the launcher's, so `kill -TERM <pid>` takes its graceful
//     path (it forwards the signal to Next).
//     The cwd is read from /proc/<pid>/cwd on Linux and with
//     `lsof -a -p <pid> -d cwd -Fn` on macOS, where a missing directory reads
//     as deleted. The macOS branch is not verified on macOS hardware.
//   node pi-web-served.js installed <npm-global-root>
//     Prints `<version>|<package dir>` of <root>/@silgrid/pi-web, or nothing.
//
// Served == installed exactly when the served line without its `|<pid>`
// equals the installed line: directory AND version. Always exits 0 (2 on a
// usage error).

/* eslint-disable @typescript-eslint/no-require-imports */
const { spawnSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const PKG = "@silgrid/pi-web";
const DELETED = " (deleted)";

// "<version>|<dir>" of the PKG package whose package.json is p (symlinks
// resolved), or "".
function packageOf(p) {
  try {
    const dir = path.dirname(fs.realpathSync(p));
    const pkg = JSON.parse(fs.readFileSync(path.join(dir, "package.json"), "utf8"));
    return pkg.name === PKG && pkg.version ? `${pkg.version}|${dir}` : "";
  } catch {
    return "";
  }
}

function run(cmd, args) {
  const env = { ...process.env };
  delete env.TMUX;
  delete env.TMUX_PANE;
  const r = spawnSync(cmd, args, { encoding: "utf8", env });
  return r.status === 0 && typeof r.stdout === "string" ? r.stdout : "";
}

const children = (pid) => run("pgrep", ["-P", pid]).split(/\s+/).filter(Boolean);

// { dir, deleted } of pid's working directory, or null when unreadable.
function cwdOf(pid) {
  if (process.platform === "darwin") {
    const line = run("lsof", ["-a", "-p", pid, "-d", "cwd", "-Fn"])
      .split("\n")
      .find((l) => l.startsWith("n"));
    if (!line) return null;
    const dir = line.slice(1);
    return { dir, deleted: !fs.existsSync(dir) };
  }
  let dir;
  try {
    dir = fs.readlinkSync(`/proc/${pid}/cwd`);
  } catch {
    return null;
  }
  if (dir.endsWith(DELETED)) return { dir: dir.slice(0, -DELETED.length), deleted: true };
  return { dir, deleted: false };
}

// The served line for the launcher pid: judged by its server child's cwd.
function judge(launcher) {
  for (const pid of children(launcher)) {
    const cwd = cwdOf(pid);
    if (!cwd) continue;
    if (cwd.deleted) return `unknown|${cwd.dir}${DELETED}|${launcher}`;
    const pkg = packageOf(path.join(cwd.dir, "package.json"));
    return pkg ? `${pkg}|${launcher}` : "unidentified";
  }
  return "";
}

function served(session) {
  const panes = run("tmux", ["list-panes", "-s", "-t", session, "-F", "#{window_name} #{pane_pid}"]);
  let seen = false;
  for (const line of panes.split("\n")) {
    const [win, panePid] = line.trim().split(/\s+/);
    if (win !== "piweb" || !panePid) continue;
    for (const pid of [panePid, ...children(panePid)]) {
      const args = run("ps", ["-p", pid, "-o", "args="]).trim();
      if (!args) continue;
      if (pid !== panePid) seen = true;
      if (args.split(/\s+/).some((tok) => /^\/.*\/pi-web(\.js)?$/.test(tok))) return judge(pid);
    }
  }
  return seen ? "unidentified" : "";
}

const [mode, arg] = process.argv.slice(2);
if (mode === "served" && arg) process.stdout.write(served(arg));
else if (mode === "installed" && arg) process.stdout.write(packageOf(path.join(arg, PKG, "package.json")));
else {
  process.stderr.write("usage: pi-web-served.js served <tmux-session> | installed <npm-global-root>\n");
  process.exit(2);
}
