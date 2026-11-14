// carries: bin/pi-web.js, bin/pi-web-install-check.js, lib/polyforge-pf-bin*, bin/pi-web-served.js, e2e/lane-stubs.mjs
// AC6: the deploy reports which pf the service uses (absolute
// path + `pf --version`) and pins it into the restart loop.
//
// Reported gap: deploy-piweb.sh named only the pi-web package, so an operator
// could not tell which pf the service would spawn; the service's PF_BIN came
// from the tmux server's global environment (or nothing), and a hostile
// `tmux respawn-window` from a shell without PF_BIN could still lose pf.
//
// Covered criteria:
//   - a deploy with a valid PF_BIN prints one
//     `==> pf: <absolute path> (<pf --version>)` line and the running loop
//     inherits exactly that PF_BIN;
//   - a later hostile `tmux respawn-window` (PF_BIN unset, PATH without the
//     installed prefix and without pf) keeps the pinned value, so pf does not
//     depend on the tmux server's global environment;
//   - with no PF_BIN and no pf on the loop PATH, the deploy prints a clear
//     `==> pf: none found (...)` line and the loop carries no PF_BIN;
//   - a PF_BIN that is not executable prints a none-found line naming it and
//     still reaches the loop, so the service's own probe reports not installed.
//
// Fully offline: PATH stubs stand in for npm (it models a real global install:
// `install -g` copies an installed-package tree whose install scripts ran
// (e2e/lane-stubs.mjs writePiwebPackageTree, so the deploy's install check
// passes; its bin/pi-web.js the stub launcher that spawns the pi-web entry,
// bin/pi-web-entry, with the package directory as cwd, as the real launcher
// spawns Next) under a scratch prefix and writes its
// package.json, `root -g` reads it back) and pi-web; a stub pf answers `--version` and logs invocations. The
// node running this suite is linked next to the npm stub and into the prefix's
// bin dir (where a real npm prefix keeps it), so neither the deploy nor a
// hostile respawn depends on /usr/bin holding a node. HOME,
// TMPDIR and each scenario's TMUX_TMPDIR are scratch directories, and every
// child (deploy and harness tmux calls) runs without TMUX and TMUX_PANE, so the
// live service is never touched and each private tmux server starts without
// the operator's PF_BIN.
//
// Run: node e2e/deploy-pf-bin.mjs   (from the repository root)
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { linkNode, readLog, writePiwebLauncher, writePiwebPackageTree, writeStub } from "./lane-stubs.mjs";
import { blockFail, blockPass, createCheck } from "./harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const deployScript = join(root, "deploy-piweb.sh");

assert.ok(process.platform === "linux" || process.platform === "darwin", "deploy-piweb.sh targets Linux/macOS");
assert.equal(spawnSync("tmux", ["-V"], { encoding: "utf8" }).status, 0, "tmux must be installed for the deploy e2e");

const PKG = "@silgrid/pi-web";
const NEW = "0.1.7";
const PF_VERSION = "v2.20.13";
const scratch = mkdtempSync(join(tmpdir(), "piweb-pf-bin-"));
const stubbin = join(scratch, "bin");
mkdirSync(stubbin);
const pfLog = join(scratch, "pf.argv");
const pfPath = join(scratch, "pf-stub");
// The installed package's tree (install scripts ran, the stub launcher and
// the entry below in bin/); the npm stub's install copies it before writing
// the scenario's package.json.
const piwebTree = join(scratch, "piweb-tree");
writePiwebPackageTree(piwebTree);

// npm stub: a global install under NPMSTUB_PREFIX, as npm lays it out. `root
// -g` is what deploy-piweb.sh uses to find the installed package (its install
// check and the loop's pinned bin directory).
const npmStub = [
  "#!/bin/sh",
  "# Stub npm (deploy-pf-bin e2e): `install -g` writes the package under",
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
  '    mkdir -p "$pkgdir/bin" "$prefix/bin"',
  `    printf '{"name":"${PKG}","version":"%s","bin":{"pi-web":"bin/pi-web.js"}}\\n' "\${NPMSTUB_VERSION:?}" >"$pkgdir/package.json"`,
  `    ln -sf "../lib/node_modules/${PKG}/bin/pi-web.js" "$prefix/bin/pi-web"`,
  "    exit 0 ;;",
  "esac",
  "exit 0",
  "",
].join("\n");
writeStub(stubbin, "npm", npmStub);
// The deploy caller ran npm, so node is on its PATH next to it; the stubbed
// PATH below holds no other node dir (/usr/bin need not have one).
linkNode(stubbin);

// pf stub: records every invocation and answers `--version`.
writeStub(
  scratch,
  "pf-stub",
  [
    "#!/bin/sh",
    'printf \'%s\\n\' "$*" >>"${PFSTUB_LOG:?PFSTUB_LOG must point at the invocation log}"',
    'if [ "$1" = "--version" ]; then printf \'%s\\n\' "' + PF_VERSION + '"; exit 0; fi',
    "exit 0",
    "",
  ].join("\n"),
);

// The pi-web package entry (bin/pi-web-entry, the stub launcher's child):
// records the environment the loop handed it (PF_BIN and PATH included), then
// stays alive.
const piwebBin = [
  "#!/usr/bin/env node",
  'const { appendFileSync, readFileSync } = require("node:fs");',
  'const { join } = require("node:path");',
  'const pkg = join(__dirname, "..");',
  'const { version } = JSON.parse(readFileSync(join(pkg, "package.json"), "utf8"));',
  'appendFileSync(join(pkg, "run.jsonl"), `${JSON.stringify({ pid: process.pid, version, pfBin: process.env.PF_BIN ?? null, path: process.env.PATH ?? "" })}\\n`);',
  "setInterval(() => {}, 60_000);",
  "",
].join("\n");
const piwebBinTemplate = join(scratch, "pi-web.js.template");
writeFileSync(piwebBinTemplate, piwebBin);
writePiwebLauncher(piwebTree, piwebBinTemplate);

// A private tmux server per scenario: its global environment starts from the
// deploy caller, so a caller without PF_BIN really has no server-side PF_BIN.
const tmuxDirFor = (session) => {
  const dir = join(scratch, "tmux", session);
  mkdirSync(dir, { recursive: true });
  return dir;
};

const baseEnv = (session, tmuxDir, prefix, overrides = {}) => {
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (key.startsWith("PI_WEB_") || key.startsWith("PIWEB_") || key.startsWith("GHSTUB_") || key.startsWith("NPMSTUB_") || key.startsWith("PFSTUB_")) {
      delete env[key];
    }
  }
  delete env.PF_BIN;
  delete env.TMUX;
  delete env.TMUX_PANE;
  // The global prefix holds node, as a real npm prefix does.
  linkNode(join(prefix, "bin"));
  return Object.assign(env, {
    PATH: `${stubbin}:/usr/bin:/bin`,
    HOME: scratch,
    TMPDIR: scratch,
    TMUX_TMPDIR: tmuxDir,
    PIWEB_TMUX_SESSION: session,
    PIWEB_HOST: "127.0.0.1",
    NPMSTUB_PREFIX: prefix,
    NPMSTUB_VERSION: NEW,
    PFSTUB_LOG: pfLog,
    ...overrides,
  });
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

const run = (script, args, env) => spawnSync("bash", [script, ...args], { cwd: root, encoding: "utf8", timeout: 120_000, env });
const output = (result) => `${result.stdout}\n${result.stderr}`;

const sessions = [];
const pkgdirs = [];
const failures = [];
const scenario = createCheck({ failures, message: (error) => String(error.message), pass: blockPass, fail: blockFail });

try {
  await scenario("AC6: a valid PF_BIN is printed with its version and pinned across a hostile respawn", async () => {
    const session = `piweb-pf-bin-${process.pid}`;
    sessions.push(session);
    const tmuxDir = tmuxDirFor(session);
    const prefix = join(scratch, "pf-pinned-prefix");
    const pkgdir = join(prefix, "lib", "node_modules", PKG);
    pkgdirs.push(pkgdir);

    const result = run(deployScript, [], baseEnv(session, tmuxDir, prefix, { PF_BIN: pfPath }));
    const report = output(result);
    assert.equal(result.status, 0, `a deploy with a valid PF_BIN must succeed\n${report}`);
    const line = report.match(/^==> pf: (.+) \((.*)\)$/m);
    assert.ok(line, `the deploy must print one '==> pf: <path> (<version>)' line\n${report}`);
    assert.equal(line[1], pfPath, `the printed pf must be PF_BIN's absolute path: ${line[1]}`);
    assert.equal(line[2], PF_VERSION, `the printed pf line names pf --version: ${line[2]}`);
    assert.ok(readLog(pfLog).includes("--version"), `the deploy must run the resolved pf --version; observed ${JSON.stringify(readLog(pfLog))}`);

    const first = await waitForRuns(pkgdir);
    assert.ok(first.length > 0, "fixture sanity: the newly installed loop must have started");
    assert.equal(first.at(-1).pfBin, pfPath, `the loop must inherit the pinned PF_BIN; observed ${JSON.stringify(first.at(-1))}`);

    const before = first.length;
    const respawn = spawnSync("tmux", ["respawn-window", "-k", "-t", `${session}:piweb`], {
      encoding: "utf8",
      timeout: 10_000,
      env: baseEnv(session, tmuxDir, prefix, { PATH: "/usr/bin:/bin" }),
    });
    assert.equal(respawn.status, 0, `fixture sanity: tmux respawn-window must succeed\n${respawn.stdout}\n${respawn.stderr}`);
    const after = await waitForRuns(pkgdir, before);
    assert.ok(after.length > before, "the hostile-shell restart must start the loop again");
    assert.equal(after.at(-1).pfBin, pfPath, `a respawn from a shell without PF_BIN must keep the pinned pf; observed ${JSON.stringify(after.at(-1))}`);
  });

  await scenario("AC6: with no PF_BIN and no pf on the loop PATH the deploy says so", async () => {
    const session = `piweb-pf-none-${process.pid}`;
    sessions.push(session);
    const tmuxDir = tmuxDirFor(session);
    const prefix = join(scratch, "pf-none-prefix");
    const pkgdir = join(prefix, "lib", "node_modules", PKG);
    pkgdirs.push(pkgdir);

    const result = run(deployScript, [], baseEnv(session, tmuxDir, prefix));
    const report = output(result);
    assert.equal(result.status, 0, `a deploy without pf must still succeed\n${report}`);
    assert.match(report, /^==> pf: none found \(.*\)$/m, `the deploy must say no pf was found\n${report}`);
    assert.doesNotMatch(report, /^==> pf: \//m, `the none-found line must not name a path\n${report}`);

    const first = await waitForRuns(pkgdir);
    assert.ok(first.length > 0, "fixture sanity: the newly installed loop must have started");
    assert.equal(first.at(-1).pfBin, null, `no pf means the loop carries no PF_BIN; observed ${JSON.stringify(first.at(-1))}`);
  });

  await scenario("AC6: a configured PF_BIN that is not executable reads as none found", async () => {
    const session = `piweb-pf-missing-${process.pid}`;
    sessions.push(session);
    const tmuxDir = tmuxDirFor(session);
    const prefix = join(scratch, "pf-missing-prefix");
    const pkgdir = join(prefix, "lib", "node_modules", PKG);
    pkgdirs.push(pkgdir);
    const missing = join(scratch, "does-not-exist-pf");

    const result = run(deployScript, [], baseEnv(session, tmuxDir, prefix, { PF_BIN: missing }));
    const report = output(result);
    assert.equal(result.status, 0, `a deploy with a missing PF_BIN must still succeed\n${report}`);
    assert.match(report, /^==> pf: none found \(PF_BIN=.* is not an executable.*\)$/m, `the deploy must explain the missing PF_BIN\n${report}`);

    const first = await waitForRuns(pkgdir);
    assert.ok(first.length > 0, "fixture sanity: the newly installed loop must have started");
    assert.equal(first.at(-1).pfBin, missing, `the configured PF_BIN reaches the service so it can report not-installed; observed ${JSON.stringify(first.at(-1))}`);
  });

  if (failures.length > 0) {
    console.error(`${failures.length} scenario(s) failed: ${failures.join("; ")}`);
    process.exitCode = 1;
  } else {
    console.log("PASS: all pf reporting scenarios held");
  }
} finally {
  // Never leak the scratch sessions, stub processes, or temp files.
  for (const session of sessions) {
    const env = { ...process.env, TMUX_TMPDIR: tmuxDirFor(session) };
    delete env.TMUX;
    delete env.TMUX_PANE;
    spawnSync("tmux", ["kill-session", "-t", session], { encoding: "utf8", env });
  }
  for (const pkgdir of pkgdirs) {
    for (const item of runs(pkgdir)) spawnSync("kill", ["-9", String(item.pid)], { encoding: "utf8" });
  }
  rmSync(scratch, { recursive: true, force: true });
}