// carries: deploy-piweb.sh, bin/pi-web.js, bin/pi-web-install-check.js, bin/pi-web-served.js, e2e/lane-stubs.mjs
// Served == installed deploy contract (re-pinned by).
//
// Reported failure: npm installed v0.1.7 into a global prefix whose bin dir was
// not on the service PATH, so `command -v pi-web` and the running loop process
// still pointed at an older package. The deploy reported success anyway
// (a warn-only self-check), and after the old prefix was removed the login
// page answered 500.
//
// Covered criteria:
//   - mismatch: the installed package's script resolves to an older package.
//     The deploy must exit non-zero and name the served and installed
//     versions; the lane must not report state=deployed.
//   - PATH-independent service: an old pi-web sits first on PATH while the new
//     install lives in another prefix. The deploy must serve the installed
//     package, and a restart from a hostile shell (PATH without the polyforge
//     or install bin dirs, no PF_BIN) must keep serving it.
//   - aligned: PATH's pi-web resolves to the newly installed package and the
//     loop runs it. No `warn: serving` line; one `==> Served == installed`
//     line names the version and the package directory.
//   - already stale: npm replaced the package directory under
//     a running pi-web (renamed the old tree, wrote the new one, deleted the
//     renamed tree) and nothing restarted it. The shared helper
//     bin/pi-web-served.js must not report it as served == installed, nor as
//     serving the new version, although its launcher's path now resolves to
//     the new tree.
//
// Fully offline: PATH stubs stand in for npm (it models a real global
// install: `install -g` copies an installed-package tree whose install scripts
// ran (e2e/lane-stubs.mjs writePiwebPackageTree, so the deploy's install check
// passes) under a scratch prefix and writes its package.json, `root -g` and
// `ls -g` read it back) and gh. Every pi-web package has the real launcher's
// process shape: bin/pi-web.js is the e2e/lane-stubs.mjs stub
// launcher, which spawns the package's entry, bin/pi-web-entry, a tiny Node
// script that stays alive, with the package directory as cwd, as the real
// launcher spawns Next's server. Each install prefix's bin
// dir holds the node running this suite, where a real npm prefix keeps it, so
// a hostile respawn does not depend on /usr/bin holding a node.
// HOME and TMPDIR are a scratch directory, every deploy uses its own
// scratch tmux session, and every child (deploy and harness tmux calls) runs
// without TMUX and TMUX_PANE. The already-stale scenario runs its loop on a
// private tmux server (a scratch TMUX_TMPDIR) and kills only its own session.
//
// Run: node e2e/deploy-served-installed.mjs   (from the repository root)
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { ghStubBody, laneRemoteEnv, linkNode, releaseStubEnv, writePiwebLauncher, writePiwebPackageTree, writeStub } from "./lane-stubs.mjs";
import { blockFail, blockPass, createCheck } from "./harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const deployScript = join(root, "deploy-piweb.sh");
// The deploy lane is the repository's one deploy entry, `.polyforge/deploy.sh`
// at the repository root, with `--component pi-web`.
const laneDeploy = join(root, "..", ".polyforge", "deploy.sh");

assert.ok(process.platform === "linux" || process.platform === "darwin", "deploy-piweb.sh targets Linux/macOS");
assert.equal(spawnSync("tmux", ["-V"], { encoding: "utf8" }).status, 0, "tmux must be installed for the deploy e2e");

const OLD = "0.1.5";
const NEW = "0.1.7";
const PKG = "@silgrid/pi-web";
const PAYLOAD = "stub pi-web release tarball payload for the served-installed e2e\n";
const HASH = createHash("sha256").update(PAYLOAD).digest("hex");

const scratch = mkdtempSync(join(tmpdir(), "piweb-served-installed-"));
const stubbin = join(scratch, "bin");
mkdirSync(stubbin);
// The installed package's tree (install scripts ran, the stub launcher and
// the entry below in bin/); the npm stub's install copies it before writing
// the scenario's package.json.
const piwebTree = join(scratch, "piweb-tree");
writePiwebPackageTree(piwebTree);
const sessions = {
  mismatch: `piweb-served-mismatch-${process.pid}`,
  lane: `piweb-served-lane-${process.pid}`,
  pathOld: `piweb-served-pathold-${process.pid}`,
  aligned: `piweb-served-aligned-${process.pid}`,
  noHelper: `piweb-served-nohelper-${process.pid}`,
};
// The already-stale scenario's session lives on its own private tmux server.
const staleSession = `piweb-served-stale-${process.pid}`;
const staleTmuxDir = join(scratch, "tmux-stale");

// The pi-web package entry (bin/pi-web-entry, the stub launcher's child):
// records which package it runs from (and the PATH / PF_BIN it inherited),
// then stays alive.
const piwebBin = [
  "#!/usr/bin/env node",
  'const { appendFileSync, readFileSync } = require("node:fs");',
  'const { join } = require("node:path");',
  'const pkg = join(__dirname, "..");',
  'const { version } = JSON.parse(readFileSync(join(pkg, "package.json"), "utf8"));',
  'appendFileSync(join(pkg, "run.jsonl"), `${JSON.stringify({ pid: process.pid, script: process.argv[1], version, path: process.env.PATH ?? "", pfBin: process.env.PF_BIN ?? null })}\\n`);',
  "setInterval(() => {}, 60_000);",
  "",
].join("\n");
const piwebBinTemplate = join(scratch, "pi-web.js.template");
writeFileSync(piwebBinTemplate, piwebBin);
writePiwebLauncher(piwebTree, piwebBinTemplate);

const packageJson = (version) => `${JSON.stringify({ name: PKG, version, bin: { "pi-web": "bin/pi-web.js" } })}\n`;

// npm stub: a global install under NPMSTUB_PREFIX, as npm lays it out.
// NPMSTUB_SCRIPT_MODE=link-old makes the installed package's launcher resolve
// (through realpath) to an older package's launcher, which runs that older
// package's entry in that older package's directory: the "installed but
// still serving the old package" shape.
const npmStub = [
  "#!/bin/sh",
  "# Stub npm (served-installed e2e): `install -g` writes the package under",
  "# NPMSTUB_PREFIX; `root -g` and `ls -g` read that install back.",
  'prefix="${NPMSTUB_PREFIX:?NPMSTUB_PREFIX must name the global prefix}"',
  `pkgdir="$prefix/lib/node_modules/${PKG}"`,
  'case "$1" in',
  '  root) printf \'%s\\n\' "$prefix/lib/node_modules"; exit 0 ;;',
  "  ls)",
  `    case "$*" in *"${PKG}"*) ;; *) exit 1 ;; esac`,
  '    [ -f "$pkgdir/package.json" ] || { printf \'%s\\n└── (empty)\\n\' "$prefix/lib"; exit 1; }',
  '    v="$(sed -n \'s/.*"version":"\\([^"]*\\)".*/\\1/p\' "$pkgdir/package.json")"',
  `    printf '%s\\n└── ${PKG}@%s\\n' "$prefix/lib" "$v"; exit 0 ;;`,
  "  install)",
  `    mkdir -p "$pkgdir" && cp -R '${piwebTree}/.' "$pkgdir/" || exit 1`,
  '    if [ "${NPMSTUB_NO_SERVED_HELPER:-}" = 1 ]; then rm -f "$pkgdir/bin/pi-web-served.js"; fi',
  '    mkdir -p "$pkgdir/bin" "$prefix/bin"',
  `    printf '{"name":"${PKG}","version":"%s","bin":{"pi-web":"bin/pi-web.js"}}\\n' "\${NPMSTUB_VERSION:?}" >"$pkgdir/package.json"`,
  '    if [ "${NPMSTUB_SCRIPT_MODE:-copy}" = "link-old" ]; then',
  '      ln -sf "${NPMSTUB_OLD_SCRIPT:?}" "$pkgdir/bin/pi-web.js"',
  "    fi",
  '    ln -sf "../lib/node_modules/' + PKG + '/bin/pi-web.js" "$prefix/bin/pi-web"',
  "    exit 0 ;;",
  "esac",
  "exit 0",
  "",
].join("\n");
writeStub(stubbin, "npm", npmStub);
writeStub(stubbin, "gh", ghStubBody);

// An old package on PATH: <dir>/lib/node_modules/@silgrid/pi-web at OLD (the
// stub launcher and the entry), with <dir>/bin/pi-web the npm-style symlink
// to its launcher.
const oldPackage = (dir) => {
  const pkgdir = join(dir, "lib", "node_modules", PKG);
  mkdirSync(join(pkgdir, "bin"), { recursive: true });
  writeFileSync(join(pkgdir, "package.json"), packageJson(OLD));
  writePiwebLauncher(pkgdir, piwebBinTemplate);
  const script = join(pkgdir, "bin", "pi-web.js");
  mkdirSync(join(dir, "bin"), { recursive: true });
  symlinkSync(`../lib/node_modules/${PKG}/bin/pi-web.js`, join(dir, "bin", "pi-web"));
  return { pkgdir, script };
};

const runs = (pkgdir) => {
  try {
    return readFileSync(join(pkgdir, "run.jsonl"), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
  } catch {
    return [];
  }
};

const waitForRuns = async (pkgdir, after = 0, timeoutMs = 10_000) => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (runs(pkgdir).length > after) return runs(pkgdir);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  return runs(pkgdir);
};

// A daemon-like environment: no operator service-shape variable, a loopback
// bind with no port/password (no configured-port probe) and no allowed hosts
// (the TLS edge probe is skipped), so only the served/installed contract is
// exercised.
const baseEnv = (session, pathDir, prefix, overrides = {}) => {
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (key.startsWith("PI_WEB_") || key.startsWith("PIWEB_") || key.startsWith("GHSTUB_") || key.startsWith("NPMSTUB_")) {
      delete env[key];
    }
  }
  delete env.PF_BIN;
  delete env.TMUX;
  delete env.TMUX_PANE;
  // The global prefix holds node, as a real npm prefix does, so the hostile
  // shell's /usr/bin need not.
  linkNode(join(prefix, "bin"));
  return Object.assign(env, {
    ...releaseStubEnv(join(scratch, "gh.argv"), `v${NEW}`),
    PATH: `${stubbin}:${pathDir}:${process.env.PATH}`,
    HOME: scratch,
    TMPDIR: scratch,
    PIWEB_TMUX_SESSION: session,
    PIWEB_HOST: "127.0.0.1",
    NPMSTUB_PREFIX: prefix,
    NPMSTUB_VERSION: NEW,
    ...laneRemoteEnv(laneDeploy, env),
    ...overrides,
  });
};

// The shell a human happened to use for `tmux respawn-window`: the old prefix
// first, no polyforge bin dir and no PF_BIN.
const hostileEnv = (session, oldBin) => {
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (key.startsWith("PI_WEB_") || key.startsWith("PIWEB_") || key.startsWith("GHSTUB_") || key.startsWith("NPMSTUB_")) {
      delete env[key];
    }
  }
  delete env.PF_BIN;
  delete env.TMUX;
  delete env.TMUX_PANE;
  return Object.assign(env, {
    PATH: `${oldBin}${delimiter}/usr/bin${delimiter}/bin`,
    HOME: scratch,
    TMPDIR: scratch,
    PIWEB_TMUX_SESSION: session,
  });
};

const run = (script, args, env) =>
  spawnSync("bash", [script, ...args], { cwd: root, encoding: "utf8", timeout: 120_000, env });
const output = (result) => `${result.stdout}\n${result.stderr}`;
const servedWarnings = (result) => output(result).match(/^warn: serving .*$/gm) ?? [];
const mentionsVersions = (report) => report.includes(OLD) && report.includes(NEW);

const pkgdirs = [];
// pids of the already-stale scenario's launcher and entry: its package
// directory, run.jsonl included, is deleted under them.
const stalePids = [];
const failures = [];
const servedHelper = join(root, "bin", "pi-web-served.js");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};
// The served line without its trailing `|<pid>`, the shape `installed` prints.
const withoutPid = (line) => line.slice(0, line.lastIndexOf("|"));
const scenario = createCheck({ failures, message: (error) => String(error.message), pass: blockPass, fail: blockFail });

try {
  await scenario("mismatch: an installed script that resolves to the old package fails the deploy", async () => {
    const base = join(scratch, "mismatch");
    const old = oldPackage(join(base, "old"));
    const prefix = join(base, "new-prefix");
    pkgdirs.push(old.pkgdir, join(prefix, "lib", "node_modules", PKG));
    const result = run(
      deployScript,
      [],
      baseEnv(sessions.mismatch, join(base, "old", "bin"), prefix, {
        NPMSTUB_SCRIPT_MODE: "link-old",
        NPMSTUB_OLD_SCRIPT: old.script,
      }),
    );
    const report = output(result);
    assert.notEqual(result.status, 0, `serving an older package than the installed one must fail the deploy\n${report}`);
    assert.ok(mentionsVersions(report), `the failure must name both served and installed versions\n${report}`);
    assert.match(report, /serv(ed|ing)/i, `the failure must say what is actually served\n${report}`);
    assert.doesNotMatch(report, /^==> Served == installed/m, `a mismatch must not report alignment\n${report}`);
  });

  await scenario("mismatch via the lane: no state=deployed and a non-zero exit", async () => {
    const base = join(scratch, "lane");
    const old = oldPackage(join(base, "old"));
    const prefix = join(base, "new-prefix");
    pkgdirs.push(old.pkgdir, join(prefix, "lib", "node_modules", PKG));
    const result = run(
      laneDeploy,
      [`v${NEW}`, "--confirm", `v${NEW}`, "--component", "pi-web"],
      baseEnv(sessions.lane, join(base, "old", "bin"), prefix, {
        NPMSTUB_SCRIPT_MODE: "link-old",
        NPMSTUB_OLD_SCRIPT: old.script,
        GHSTUB_LOG: join(base, "gh.argv"),
        GHSTUB_LATEST_TAG: `v${NEW}`,
        GHSTUB_PAYLOAD: PAYLOAD,
        GHSTUB_HASH: HASH,
        GHSTUB_CHECKSUM_MODE: "match",
      }),
    );
    const report = output(result);
    assert.notEqual(result.status, 0, `the lane must fail when the served package is not the installed one\n${report}`);
    assert.doesNotMatch(result.stdout, /^state=deployed$/m, `a mismatch must never report the lane as deployed\n${report}`);
    assert.ok(mentionsVersions(report), `the lane failure must name both versions\n${report}`);
  });

  await scenario("an old pi-web first on PATH: the deploy and a hostile-shell restart both serve the new install", async () => {
    const base = join(scratch, "path-old");
    const old = oldPackage(join(base, "old"));
    const prefix = join(base, "new-prefix");
    const newPkgdir = join(prefix, "lib", "node_modules", PKG);
    pkgdirs.push(old.pkgdir, newPkgdir);
    const result = run(deployScript, [], baseEnv(sessions.pathOld, join(base, "old", "bin"), prefix));
    const report = output(result);
    assert.equal(result.status, 0, `a valid new install must deploy even when PATH resolves an old package first\n${report}`);
    assert.deepEqual(servedWarnings(result), [], `the deploy must not warn about serving a different package\n${report}`);
    assert.match(report, new RegExp(`^==> Served == installed: pi-web ${NEW.replaceAll(".", "\\.")} \\(.*\\)$`, "m"), report);

    const first = await waitForRuns(newPkgdir);
    assert.ok(first.length > 0, "fixture sanity: the newly installed loop must have started");
    assert.equal(first.at(-1).version, NEW, `the deploy must serve the installed package, not PATH's old one\n${report}`);

    const before = first.length;
    const respawn = spawnSync("tmux", ["respawn-window", "-k", "-t", `${sessions.pathOld}:piweb`], {
      encoding: "utf8",
      timeout: 10_000,
      env: hostileEnv(sessions.pathOld, join(base, "old", "bin")),
    });
    assert.equal(respawn.status, 0, `fixture sanity: tmux respawn-window must succeed\n${respawn.stdout}\n${respawn.stderr}`);
    const after = await waitForRuns(newPkgdir, before);
    assert.ok(after.length > before, "the hostile-shell restart must start the loop again");
    assert.equal(after.at(-1).version, NEW, "a restart from a shell whose PATH prefers the old package must still serve the installed one");
  });

  await scenario("aligned: PATH and the running loop resolve to the new install, no warning", async () => {
    const base = join(scratch, "aligned");
    const prefix = join(base, "prefix");
    const pkgdir = join(prefix, "lib", "node_modules", PKG);
    pkgdirs.push(pkgdir);
    // PATH holds the prefix's bin dir; npm creates its pi-web link on install.
    mkdirSync(join(prefix, "bin"), { recursive: true });
    const result = run(deployScript, [], baseEnv(sessions.aligned, join(prefix, "bin"), prefix));
    const report = output(result);
    assert.equal(result.status, 0, `an aligned deploy must succeed\n${report}`);
    assert.deepEqual(servedWarnings(result), [], `an aligned deploy must not warn\n${report}`);
    const aligned = report.match(/^==> Served == installed: pi-web (\S+) \((.*)\)$/m);
    assert.ok(aligned, `an aligned deploy prints one 'Served == installed' line\n${report}`);
    assert.equal(aligned[1], NEW, "the line names the installed version");
    const served = await waitForRuns(pkgdir);
    assert.ok(served.length > 0, "fixture sanity: the loop must be running the new package");
    assert.equal(served.at(-1).version, NEW, "fixture sanity: the loop serves the new version");
  });

  // the served == installed definition is the deploy's own
  // (bin/pi-web-served.js next to deploy-piweb.sh), so a pinned release whose
  // package predates that helper (a rollback) deploys and is confirmed too.
  await scenario("aligned, the installed package predates pi-web-served.js: still confirmed", async () => {
    const base = join(scratch, "no-helper");
    const prefix = join(base, "prefix");
    const pkgdir = join(prefix, "lib", "node_modules", PKG);
    pkgdirs.push(pkgdir);
    mkdirSync(join(prefix, "bin"), { recursive: true });
    const result = run(deployScript, [], baseEnv(sessions.noHelper, join(prefix, "bin"), prefix, { NPMSTUB_NO_SERVED_HELPER: "1" }));
    const report = output(result);
    assert.ok(!existsSync(join(pkgdir, "bin", "pi-web-served.js")), "fixture sanity: the installed package has no pi-web-served.js");
    assert.equal(result.status, 0, `a package without bin/pi-web-served.js must still deploy\n${report}`);
    assert.match(report, new RegExp(`^==> Served == installed: pi-web ${NEW.replaceAll(".", "\\.")} \\(.*\\)$`, "m"), report);
  });

  // served == installed is judged by the working directory of
  // the launcher's server (bin/pi-web-served.js, shared by deploy-piweb.sh and
  // tools/install.sh), so a pi-web that kept running while npm replaced its
  // package directory reads as stale, although its launcher's path resolves
  // to the new tree.
  await scenario("already stale: a pi-web whose package directory npm replaced under it is not served == installed (shared helper)", async () => {
    const base = join(scratch, "already-stale");
    const prefix = join(base, "prefix");
    const npmRoot = join(prefix, "lib", "node_modules");
    const pkgdir = join(npmRoot, PKG);
    mkdirSync(join(prefix, "bin"), { recursive: true });
    mkdirSync(staleTmuxDir, { recursive: true });
    const env = baseEnv(staleSession, join(prefix, "bin"), prefix, { TMUX_TMPDIR: staleTmuxDir });
    const npmInstall = (version) => {
      const result = spawnSync("npm", ["install", "-g", PKG], { encoding: "utf8", timeout: 30_000, env: { ...env, NPMSTUB_VERSION: version } });
      assert.equal(result.status, 0, `fixture sanity: npm install -g (${version})\n${output(result)}`);
    };
    const helper = (...args) => (spawnSync("node", [servedHelper, ...args], { encoding: "utf8", timeout: 30_000, env }).stdout ?? "").trim();

    npmInstall(OLD);
    // The deploy's loop shape, on the private tmux server.
    const loop = `while true; do PATH='${join(prefix, "bin")}':"$PATH" pi-web; sleep 1; done`;
    const started = spawnSync("tmux", ["new-session", "-d", "-s", staleSession, "-n", "piweb", loop], { encoding: "utf8", timeout: 10_000, env });
    assert.equal(started.status, 0, `fixture sanity: tmux new-session\n${output(started)}`);

    // Poll until the launcher's child (the entry journals its pid) is up and
    // the helper identifies the running pi-web.
    let before = "";
    let entries = [];
    for (const deadline = Date.now() + 20_000; Date.now() < deadline; await sleep(100)) {
      before = helper("served", staleSession);
      entries = runs(pkgdir);
      if (entries.length > 0 && before.includes("|")) break;
    }
    stalePids.push(...entries.map((entry) => entry.pid));
    assert.ok(entries.length > 0 && before.includes("|"), `fixture sanity: the loop's pi-web must come up and be identified (served ${JSON.stringify(before)}, entries ${entries.length})`);
    assert.equal(before.split("|")[0], OLD, `fixture sanity: before the replacement the running pi-web serves ${OLD}: ${before}`);
    const launcher = Number(before.split("|").at(-1));
    stalePids.push(launcher);

    // npm's replacement of the package directory: rename the old tree, write
    // the new one, delete the renamed tree. Nothing restarts the pi-web.
    const renamed = join(npmRoot, "@silgrid", ".pi-web-512");
    renameSync(pkgdir, renamed);
    npmInstall(NEW);
    rmSync(renamed, { recursive: true, force: true });
    const installed = helper("installed", npmRoot);
    assert.equal(installed.split("|")[0], NEW, `fixture sanity: npm installed ${NEW}: ${installed}`);

    // Poll (bounded) until the helper reports the still-running pi-web as
    // something other than the install.
    let after = "";
    for (const deadline = Date.now() + 10_000; Date.now() < deadline; await sleep(200)) {
      after = helper("served", staleSession);
      if (after.includes("|") && withoutPid(after) !== installed) break;
    }
    assert.ok(alive(launcher), `fixture sanity: the stale pi-web (launcher ${launcher}) must still run`);
    assert.ok(after.includes("|"), `the helper must still identify the running pi-web: ${JSON.stringify(after)}`);
    assert.equal(Number(after.split("|").at(-1)), launcher, `the served line names the running launcher's pid: ${after}`);
    assert.notEqual(withoutPid(after), installed, `an already-stale pi-web must not read as served == installed\nserved:    ${after}\ninstalled: ${installed}`);
    assert.notEqual(after.split("|")[0], NEW, `an already-stale pi-web must not read as serving the installed ${NEW}: ${after}`);
  });

  if (failures.length > 0) {
    console.error(`${failures.length} scenario(s) failed: ${failures.join("; ")}`);
    process.exitCode = 1;
  } else {
    console.log("PASS: all served-vs-installed scenarios held");
  }
} finally {
  // Never leak the scratch sessions, stub processes, or temp files.
  const tmuxEnv = { ...process.env };
  delete tmuxEnv.TMUX;
  delete tmuxEnv.TMUX_PANE;
  for (const session of Object.values(sessions)) {
    spawnSync("tmux", ["kill-session", "-t", session], { encoding: "utf8", env: tmuxEnv });
  }
  // The already-stale scenario's own processes, then its own session on its
  // private server (never kill-server).
  for (const pid of stalePids) spawnSync("kill", ["-9", String(pid)], { encoding: "utf8" });
  spawnSync("tmux", ["kill-session", "-t", staleSession], { encoding: "utf8", env: { ...tmuxEnv, TMUX_TMPDIR: staleTmuxDir } });
  for (const pkgdir of pkgdirs) {
    for (const item of runs(pkgdir)) spawnSync("kill", ["-9", String(item.pid)], { encoding: "utf8" });
  }
  rmSync(scratch, { recursive: true, force: true });
}