// carries: bin/pi-web-install-check.js, e2e/lane-stubs.mjs
// deploy-piweb.sh confirms the effects of the package's install
// scripts itself, whatever the host npm's allowScripts policy decides.
//
// Reported failure: npm 11 only warns about install scripts its allowScripts
// policy does not cover ("packages have install scripts not yet covered by
// allowScripts") and skips them, yet `npm install -g` exits 0. The package's
// postinstall (patch-package, then node bin/prepare-terminal.js) and
// node-pty's native build never ran, the deploy reported success, and the
// terminal could not start a pty.
//
// Covered criteria (each scenario installs with every script skipped):
//   (a) everything buildable: the deploy runs the postinstall steps and gets
//       node-pty built itself, prints one `==> Install check: <step> ok` line
//       per step (`node-pty ok (rebuilt)` for the rebuilt module) and
//       `==> Install check: all steps ok`, the patch marker and the native
//       module exist in the installed package, and the service comes up
//       (`==> Served == installed`);
//   (b) node-pty cannot be built: the deploy exits non-zero naming node-pty,
//       before it recycles anything (the old loop window's pane is unchanged);
//   (c) patch-package fails: non-zero naming patch-package, no service window;
//   (d) npm reports no global root: non-zero naming `npm root -g`;
//   (e) the installed package has no install checker: non-zero naming it.
//
// Fully offline: PATH = a stub dir (npm, plus the node running this suite) and
// /usr/bin:/bin. The npm stub models npm 11 with the scripts not allowed:
// `install -g` lays the package from a template prefix (e2e/lane-stubs.mjs
// writePiwebPrefix, scripts skipped), prints npm's allowScripts warning and
// runs no script; `rebuild node-pty` runs node-pty's install script only when
// its command line both re-enables scripts (`--ignore-scripts=false`) and
// bypasses the allowScripts policy (`--dangerously-allow-all-scripts`), and
// otherwise warns and runs nothing, exit 0. HOME and TMPDIR are scratch; every
// tmux call (the deploy's and this harness's) runs on a private server
// (scratch TMUX_TMPDIR) with TMUX and TMUX_PANE removed, and cleanup only
// kills this suite's own sessions there, never a server.
//
// Run: node e2e/deploy-install-scripts.mjs   (from pi-web/)
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { ghStubBody, linkNode, readLog, releaseStubEnv, writePiwebPrefix, writeStub } from "./lane-stubs.mjs";
import { blockFail, blockPass, createCheck, eventually } from "./harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const deployScript = join(root, "deploy-piweb.sh");

assert.ok(process.platform === "linux" || process.platform === "darwin", "deploy-piweb.sh targets Linux/macOS");

const PKG = "@silgrid/pi-web";
const VERSION = "0.2.6";
const scratch = mkdtempSync(join(tmpdir(), "piweb-install-scripts-"));
const stubbin = join(scratch, "stubbin");
const tmuxDir = join(scratch, "tmux");
const npmLog = join(scratch, "npm.argv");
mkdirSync(stubbin);
mkdirSync(tmuxDir);

// Every child (deploy and harness tmux calls): no inherited tmux selector, a
// private tmux socket directory.
const childEnv = (overrides = {}) => {
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (key.startsWith("PI_WEB_") || key.startsWith("PIWEB_") || key.startsWith("NPMSTUB_")) delete env[key];
  }
  delete env.TMUX;
  delete env.TMUX_PANE;
  delete env.PF_BIN;
  return Object.assign(env, { HOME: scratch, TMPDIR: scratch, TMUX_TMPDIR: tmuxDir }, overrides);
};
const tmux = (args) => spawnSync("tmux", args, { encoding: "utf8", timeout: 10_000, env: childEnv() });
assert.equal(tmux(["-V"]).status, 0, "tmux must be installed for the deploy e2e");

const warnLine =
  `npm warn install-scripts 3 packages have install scripts not yet covered by allowScripts: ${PKG}@${VERSION} ` +
  "(postinstall: patch-package && node bin/prepare-terminal.js), node-pty@0.0.0-fixture (install: node scripts/install.js)";
const npmStub = [
  "#!/bin/sh",
  "# Stub npm (deploy-install-scripts e2e): npm 11 with the install scripts not",
  "# covered by allowScripts. Logs argv to NPMSTUB_LOG.",
  'printf \'%s\\n\' "$*" >>"${NPMSTUB_LOG:?NPMSTUB_LOG must point at the invocation log}"',
  'prefix="${NPMSTUB_PREFIX:?NPMSTUB_PREFIX must name the global prefix}"',
  'case "$1" in',
  "  root)",
  '    [ "${NPMSTUB_ROOT:-}" = empty ] && exit 0',
  '    printf \'%s\\n\' "$prefix/lib/node_modules"; exit 0 ;;',
  "  ls) exit 1 ;;",
  "  install)",
  '    rm -rf "$prefix" && mkdir -p "$prefix" && cp -R "${NPMSTUB_TEMPLATE:?NPMSTUB_TEMPLATE must name the prefix template}/." "$prefix/" || exit 1',
  `    printf '%s\\n' '${warnLine}' >&2`,
  "    exit 0 ;;",
  "  rebuild)",
  '    ign=0; allow=0; pty=0',
  '    for a in "$@"; do',
  '      case "$a" in --ignore-scripts=false) ign=1 ;; --dangerously-allow-all-scripts) allow=1 ;; node-pty) pty=1 ;; esac',
  "    done",
  '    [ "$pty" = 1 ] || exit 1',
  '    [ -d "$PWD/node_modules/node-pty" ] || { echo "npm error: node-pty is not installed in $PWD" >&2; exit 1; }',
  '    if [ "$ign" = 1 ] && [ "$allow" = 1 ]; then',
  '      cd "$PWD/node_modules/node-pty" && node scripts/install.js',
  "      exit $?",
  "    fi",
  "    printf '%s\\n' 'npm warn install-scripts 1 package has install scripts not yet covered by allowScripts: node-pty@0.0.0-fixture (install: node scripts/install.js)' >&2",
  "    exit 0 ;;",
  "esac",
  "exit 0",
  "",
].join("\n");
writeStub(stubbin, "npm", npmStub);
writeStub(stubbin, "gh", ghStubBody);
linkNode(stubbin);

// The installed pi-web entry: records its pid, stays alive so the loop's
// process keeps its script path.
const entry = join(scratch, "pi-web.js.template");
writeFileSync(
  entry,
  [
    "#!/usr/bin/env node",
    'const { appendFileSync } = require("node:fs");',
    'const { join } = require("node:path");',
    'appendFileSync(join(__dirname, "..", "run.jsonl"), `${JSON.stringify({ pid: process.pid })}\\n`);',
    "setInterval(() => {}, 60_000);",
    "",
  ].join("\n"),
);

const template = (name, treeOpts = {}) => {
  const dir = join(scratch, "templates", name);
  writePiwebPrefix(dir, { version: VERSION, entry, postinstall: true, scripts: "skipped", ...treeOpts });
  return dir;
};
const templates = {
  buildable: template("buildable"),
  ptyFail: template("pty-fail", { nodePty: "fail" }),
  patchFail: template("patch-fail", { patchPackage: "fail" }),
  noChecker: template("no-checker", { checker: false }),
};

const sessions = [];
const prefixes = [];
const runPids = () =>
  prefixes.flatMap((prefix) => {
    try {
      return readFileSync(join(prefix, "lib", "node_modules", PKG, "run.jsonl"), "utf8")
        .split("\n")
        .filter(Boolean)
        .map((line) => JSON.parse(line).pid);
    } catch {
      return [];
    }
  });

const deploy = (name, templateDir, overrides = {}) => {
  const session = `piweb-install-scripts-${name}-${process.pid}`;
  const prefix = join(scratch, "prefix", name);
  sessions.push(session);
  prefixes.push(prefix);
  rmSync(npmLog, { force: true });
  return {
    session,
    prefix,
    pkgdir: join(prefix, "lib", "node_modules", PKG),
    run: () => {
      const result = spawnSync("bash", [deployScript], {
        cwd: root,
        encoding: "utf8",
        timeout: 180_000,
        env: childEnv({
          ...releaseStubEnv(join(scratch, "gh.argv")),
          PATH: `${stubbin}:/usr/bin:/bin`,
          PIWEB_TMUX_SESSION: session,
          PIWEB_HOST: "127.0.0.1",
          NPMSTUB_LOG: npmLog,
          NPMSTUB_PREFIX: prefix,
          NPMSTUB_TEMPLATE: templateDir,
          ...overrides,
        }),
      });
      return { ...result, report: `${result.stdout}\n${result.stderr}` };
    },
  };
};

const windows = (session) => {
  const listed = tmux(["list-windows", "-t", session, "-F", "#{window_name}|#{pane_pid}"]);
  if (listed.status !== 0) return [];
  return listed.stdout
    .split("\n")
    .filter(Boolean)
    .map((line) => {
      const [name, pid] = line.split("|");
      return { name, pid };
    });
};

const failures = [];
const scenario = createCheck({ failures, message: (error) => String(error.message), pass: blockPass, fail: blockFail });

const hasLine = (report, line) => report.split("\n").some((l) => l.trim() === line);

try {
  await scenario("(a) scripts skipped, all buildable: the deploy runs them itself and serves the package", async () => {
    const d = deploy("buildable", templates.buildable);
    const result = d.run();
    const { report } = result;
    assert.equal(result.status, 0, `a deploy whose skipped scripts can be run must succeed\n${report}`);
    for (const line of [
      "==> Install check: patch-package ok",
      "==> Install check: node bin/prepare-terminal.js ok",
      "==> Install check: node-pty ok (rebuilt)",
      "==> Install check: all steps ok",
    ]) {
      assert.ok(hasLine(report, line), `the deploy must print '${line}'\n${report}`);
    }
    assert.ok(existsSync(join(d.pkgdir, "node_modules", ".fixture", "patched")), `the installed package must carry the applied patches\n${report}`);
    assert.ok(
      existsSync(join(d.pkgdir, "node_modules", "node-pty", "build", "Release", "pty.node")),
      `the installed package's node-pty must be built\nnpm argv: ${JSON.stringify(readLog(npmLog))}\n${report}`,
    );
    assert.ok(
      await eventually(() => windows(d.session).some((w) => w.name === "piweb")),
      `the service window 'piweb' must be up; windows: ${JSON.stringify(windows(d.session))}\n${report}`,
    );
    assert.match(report, /^==> Served == installed: pi-web /m, `the deploy must confirm the served package\n${report}`);
  });

  await scenario("(b) node-pty cannot be built: non-zero, named, the old service window untouched", async () => {
    const d = deploy("pty-fail", templates.ptyFail);
    const started = tmux(["new-session", "-d", "-s", d.session, "-n", "piweb", "sleep 600"]);
    assert.equal(started.status, 0, `fixture sanity: the old service window must start\n${started.stderr}`);
    const before = windows(d.session);
    assert.equal(before.length, 1, `fixture sanity: one old window; observed ${JSON.stringify(before)}`);
    const result = d.run();
    const { report } = result;
    assert.notEqual(result.status, 0, `a deploy whose node-pty cannot be built must fail\n${report}`);
    assert.match(report, /^ERROR: Pi Web install check failed at node-pty: /m, `the failure must name node-pty\n${report}`);
    assert.deepEqual(windows(d.session), before, `the old service window must be left as it was (same pane, no new window)\n${report}`);
  });

  await scenario("(c) patch-package fails: non-zero, named, no service window", async () => {
    const d = deploy("patch-fail", templates.patchFail);
    const result = d.run();
    const { report } = result;
    assert.notEqual(result.status, 0, `a deploy whose patch-package step fails must fail\n${report}`);
    assert.match(report, /^ERROR: Pi Web install check failed at patch-package: /m, `the failure must name patch-package\n${report}`);
    assert.ok(!windows(d.session).some((w) => w.name === "piweb"), `no service window may be created\n${report}`);
  });

  await scenario("(d) npm reports no global root: non-zero, named", async () => {
    const d = deploy("no-root", templates.buildable, { NPMSTUB_ROOT: "empty" });
    const result = d.run();
    const { report } = result;
    assert.notEqual(result.status, 0, `a deploy that cannot find the installed package must fail\n${report}`);
    assert.match(report, /^ERROR: Pi Web install check failed at npm root -g: /m, `the failure must name npm root -g\n${report}`);
  });

  await scenario("(e) a package without the install checker: non-zero, named", async () => {
    const d = deploy("no-checker", templates.noChecker);
    const result = d.run();
    const { report } = result;
    assert.notEqual(result.status, 0, `a deploy of a package without the install checker must fail\n${report}`);
    assert.match(report, /^ERROR: Pi Web install check failed at pi-web-install-check\.js: /m, `the failure must name the missing checker\n${report}`);
  });

  if (failures.length > 0) {
    console.error(`${failures.length} scenario(s) failed: ${failures.join("; ")}`);
    process.exitCode = 1;
  } else {
    console.log("PASS: all install-script scenarios held");
  }
} finally {
  // Only this suite's own sessions on the private socket; never a server.
  for (const session of sessions) tmux(["kill-session", "-t", session]);
  for (const pid of runPids()) spawnSync("kill", ["-9", String(pid)], { encoding: "utf8" });
  rmSync(scratch, { recursive: true, force: true });
}
