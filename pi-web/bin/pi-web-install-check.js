"use strict";

// Install check: confirms that the effects of this package's install scripts
// are present in the installed package, whatever the host npm decided about
// running them. npm can skip install scripts and still exit 0 (e.g. npm 11
// under ignore-scripts or an explicit allowScripts denial; at default
// settings it runs unreviewed scripts with a notice), so an install can look
// successful with no patches applied and no node-pty native module built.
//
// Run from anywhere: `node "$(npm root -g)/@silgrid/pi-web/bin/pi-web-install-check.js"`.
// It works in its own package directory and, in order:
//   1. reads package.json;
//   2. runs each `&&` step of scripts.postinstall (patch-package, then
//      node bin/prepare-terminal.js) with the package's node_modules/.bin
//      first on PATH;
//   3. starts one pty through node-pty; when that fails, rebuilds node-pty
//      with its install scripts explicitly allowed and probes once more.
// Each step prints `==> Install check: <step> ok`. The first failure prints
// `ERROR: Pi Web install check failed at <step>: <reason>` and exits 1.
// Re-running it over a complete install is harmless: patch-package skips
// applied patches, prepare-terminal is idempotent and the probe only reads.

/* eslint-disable @typescript-eslint/no-require-imports */
const { spawnSync } = require("node:child_process");
const { readFileSync } = require("node:fs");
const path = require("node:path");

const pkgDir = path.dirname(__dirname);

function fail(step, reason) {
  process.stderr.write(`ERROR: Pi Web install check failed at ${step}: ${reason}\n`);
  process.exit(1);
}

function ok(step, note) {
  process.stdout.write(`==> Install check: ${step} ok${note ? ` (${note})` : ""}\n`);
}

// 1. package.json
const manifestPath = path.join(pkgDir, "package.json");
let manifest;
try {
  manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
} catch (error) {
  fail("package.json", `${manifestPath} could not be read: ${error.message}`);
}
ok("package.json");

// 2. The postinstall steps, as package.json defines them.
const postinstall = manifest && manifest.scripts && typeof manifest.scripts.postinstall === "string" ? manifest.scripts.postinstall : "";
const steps = postinstall
  .split("&&")
  .map((step) => step.trim())
  .filter(Boolean);
const stepEnv = {
  ...process.env,
  PATH: `${path.join(pkgDir, "node_modules", ".bin")}${path.delimiter}${process.env.PATH ?? ""}`,
};
for (const step of steps) {
  const result = spawnSync("sh", ["-c", step], { cwd: pkgDir, stdio: "inherit", env: stepEnv });
  if (result.error) fail(step, result.error.message);
  if (result.status !== 0) fail(step, result.status === null ? `killed by ${result.signal}` : `exit ${result.status}`);
  ok(step);
}

// 3. node-pty: load it and run one command in a pty.
const probeSource = `
const dir = ${JSON.stringify(pkgDir)};
let pty;
try {
  pty = require(require.resolve("node-pty", { paths: [dir] }));
} catch (error) {
  console.error("node-pty could not be loaded: " + (error && error.message));
  process.exit(1);
}
let term;
try {
  term = pty.spawn(process.execPath, ["-e", "process.stdout.write('pty-ok')"], { name: "xterm", cols: 80, rows: 24, cwd: dir, env: process.env });
} catch (error) {
  console.error("node-pty could not spawn a pty: " + (error && error.message));
  process.exit(1);
}
let output = "";
const finish = (code, message) => {
  if (message) console.error(message);
  try { term.kill(); } catch {}
  process.exit(code);
};
const deadline = setTimeout(() => finish(1, "the pty printed no 'pty-ok' within 10s (output: " + JSON.stringify(output.slice(-200)) + ")"), 10000);
term.onData((data) => {
  output += data;
  if (output.includes("pty-ok")) {
    clearTimeout(deadline);
    finish(0);
  }
});
term.onExit((event) => {
  setTimeout(() => {
    if (!output.includes("pty-ok")) finish(1, "the pty exited " + (event && event.exitCode) + " without printing 'pty-ok'");
  }, 1000);
});
`;

function probe() {
  const result = spawnSync(process.execPath, ["-e", probeSource], { cwd: pkgDir, encoding: "utf8", timeout: 20000 });
  if (result.status === 0) return { ok: true };
  const lines = `${result.stderr || ""}`.split("\n").map((line) => line.trim()).filter(Boolean);
  let reason = lines.at(-1);
  if (!reason && result.error) reason = result.error.message;
  if (!reason) reason = result.status === null ? `probe killed by ${result.signal}` : `probe exit ${result.status}`;
  return { ok: false, reason };
}

const first = probe();
if (first.ok) {
  ok("node-pty");
} else {
  process.stdout.write(`==> Install check: node-pty probe failed (${first.reason}); rebuilding node-pty with its install scripts allowed\n`);
  // --ignore-scripts=false overrides a configured ignore-scripts=true, and
  // --dangerously-allow-all-scripts bypasses npm 11's allowScripts policy, so
  // the host's npm configuration does not decide whether the build runs.
  const rebuild = spawnSync(
    "npm",
    ["rebuild", "node-pty", "--ignore-scripts=false", "--dangerously-allow-all-scripts", "--foreground-scripts"],
    { cwd: pkgDir, stdio: "inherit" },
  );
  const rebuildStatus = rebuild.error ? rebuild.error.message : rebuild.status === null ? `killed by ${rebuild.signal}` : `exit ${rebuild.status}`;
  const second = probe();
  if (!second.ok) fail("node-pty", `npm rebuild node-pty ended with ${rebuildStatus}; the pty probe still fails: ${second.reason}`);
  ok("node-pty", "rebuilt");
}

process.stdout.write("==> Install check: all steps ok\n");
