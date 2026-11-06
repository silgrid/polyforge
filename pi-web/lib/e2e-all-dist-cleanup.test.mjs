// once every suite has settled, the parallel e2e runner removes
// the per-suite Next dist dir (`.next/e2e/<stem>`) of each suite that passed and
// keeps the dir of each suite that failed, so a re-run of that suite starts warm.
// `.next/dev` (the real dev server's) and `.next` (a production build's) are not
// the runner's and survive; an interrupted run removes nothing.
//
// The test copies the real runner into a throwaway root (the
// lib/e2e-all-failures.test.mjs pattern) with fixture suites that write into the
// PI_WEB_DIST_DIR the runner hands them, as `next dev` does. On the pre-change
// runner every dist dir survives the run.
import "#test-env";
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const runnerSource = readFileSync(new URL("../e2e/all.mjs", import.meta.url), "utf8");
const jobsSource = readFileSync(new URL("../e2e/runner/jobs.mjs", import.meta.url), "utf8");

// A fixture suite that fills its dist dir like `next dev` would, then exits.
const suite = (exitCode) =>
  `import { mkdirSync, writeFileSync } from "node:fs";\n`
  + `mkdirSync(process.env.PI_WEB_DIST_DIR + "/server", { recursive: true });\n`
  + `writeFileSync(process.env.PI_WEB_DIST_DIR + "/server/chunk.js", "x");\n`
  + `console.log(${JSON.stringify(exitCode ? "FAIL: SC1" : "PASS: SC1")});\n`
  + `process.exit(${exitCode});\n`;

function fixtureRoot() {
  const work = mkdtempSync(join(tmpdir(), "e2e-all-dist-cleanup-"));
  mkdirSync(join(work, "e2e/runner"), { recursive: true });
  mkdirSync(join(work, "node_modules"));
  writeFileSync(join(work, "package.json"), JSON.stringify({ scripts: {} }));
  writeFileSync(join(work, "e2e/all.mjs"), runnerSource);
  writeFileSync(join(work, "e2e/runner/jobs.mjs"), jobsSource);
  // The paths that are not the runner's: the dev server's and a production build's.
  mkdirSync(join(work, ".next/dev"), { recursive: true });
  writeFileSync(join(work, ".next/dev/lock"), "");
  writeFileSync(join(work, ".next/BUILD_ID"), "prod");
  return work;
}

function assertProtected(work, output) {
  assert.ok(existsSync(join(work, ".next/dev/lock")), `.next/dev was touched:\n${output}`);
  assert.ok(existsSync(join(work, ".next/BUILD_ID")), `.next was touched:\n${output}`);
}

test("a passing suite's dist dir is removed, a failing suite's is kept", { timeout: 60_000 }, () => {
  const work = fixtureRoot();
  try {
    writeFileSync(join(work, "e2e/green.mjs"), suite(0));
    writeFileSync(join(work, "e2e/red.mjs"), suite(1));
    // An isolated suite settles after both lanes; its dir follows the same rule.
    writeFileSync(join(work, "e2e/solo.mjs"), `// e2e/all: isolated\n${suite(0)}`);
    const run = spawnSync(process.execPath, ["e2e/all.mjs"], { cwd: work, encoding: "utf8", timeout: 50_000 });
    const output = `${run.stdout}${run.stderr}`;
    assert.notEqual(run.status, 0, `a failing suite must still fail the run:\n${output}`);
    assert.equal(existsSync(join(work, ".next/e2e/green")), false, `passing dist dir kept:\n${output}`);
    assert.equal(existsSync(join(work, ".next/e2e/solo")), false, `passing isolated dist dir kept:\n${output}`);
    assert.ok(existsSync(join(work, ".next/e2e/red/server/chunk.js")), `failing dist dir removed:\n${output}`);
    assertProtected(work, output);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

test("an all-green run removes every per-suite dist dir", { timeout: 60_000 }, () => {
  const work = fixtureRoot();
  try {
    writeFileSync(join(work, "e2e/one.mjs"), suite(0));
    writeFileSync(join(work, "e2e/two.mjs"), suite(0));
    const run = spawnSync(process.execPath, ["e2e/all.mjs"], { cwd: work, encoding: "utf8", timeout: 50_000 });
    const output = `${run.stdout}${run.stderr}`;
    assert.equal(run.status, 0, output);
    assert.equal(existsSync(join(work, ".next/e2e/one")), false, output);
    assert.equal(existsSync(join(work, ".next/e2e/two")), false, output);
    assertProtected(work, output);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

test("an interrupted run removes no dist dir", { timeout: 30_000 }, async () => {
  const work = fixtureRoot();
  const blockerStarted = join(work, "blocker-started");
  let child = null;
  try {
    // A suite that has already passed when the interrupt arrives ...
    writeFileSync(join(work, "e2e/done.mjs"), suite(0));
    // ... and one still running, blocked until the runner's SIGTERM.
    writeFileSync(
      join(work, "e2e/zz-blocker.mjs"),
      `import { mkdirSync, writeFileSync } from "node:fs";\n`
        + `mkdirSync(process.env.PI_WEB_DIST_DIR, { recursive: true });\n`
        + `writeFileSync(${JSON.stringify(blockerStarted)}, "1");\n`
        + `setTimeout(() => {}, 60_000);\n`,
    );
    // E2E_JOBS only sizes the browser lane; these non-browser fixtures share the
    // serial lane, so `done` (sorted first) settles before `zz-blocker` starts.
    child = spawn(process.execPath, ["e2e/all.mjs", "done.mjs", "zz-blocker.mjs"], {
      cwd: work,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let output = "";
    child.stdout.on("data", (d) => { output += d; });
    child.stderr.on("data", (d) => { output += d; });
    const deadline = Date.now() + 10_000;
    while (!existsSync(blockerStarted) && Date.now() < deadline && child.exitCode === null) await sleep(50);
    assert.ok(existsSync(blockerStarted), `the blocking suite never started:\n${output}`);
    assert.ok(existsSync(join(work, ".next/e2e/done/server/chunk.js")), `the passed suite left no dist dir:\n${output}`);

    child.kill("SIGINT");
    const exit = await new Promise((resolve) => {
      const timer = setTimeout(() => resolve("timeout"), 20_000);
      child.once("close", (code, signal) => {
        clearTimeout(timer);
        resolve({ code, signal });
      });
    });
    assert.notEqual(exit, "timeout", `the runner did not exit after SIGINT:\n${output}`);
    assert.ok(existsSync(join(work, ".next/e2e/done/server/chunk.js")), `interrupted run removed a dist dir:\n${output}`);
    assert.ok(existsSync(join(work, ".next/e2e/zz-blocker")), `interrupted run removed a dist dir:\n${output}`);
    assertProtected(work, output);
  } finally {
    if (child && child.exitCode === null) {
      try { child.kill("SIGKILL"); } catch { /* already gone */ }
    }
    rmSync(work, { recursive: true, force: true });
  }
});

// The runner removes `.next/e2e/<stem>` only. A suite that starts more than one
// server must nest each server's dist dir under the dir it was handed
// (join(PI_WEB_DIST_DIR, name)); a sibling `<stem>-<name>` is never removed, and
// the runner cannot sweep `<stem>-*` because one suite's stem prefixes others'.
test("no e2e suite derives a sibling dist dir from PI_WEB_DIST_DIR", () => {
  const e2eDir = new URL("../e2e/", import.meta.url);
  const sibling = /\$\{[^}]*(?:PI_WEB_DIST_DIR|distDir)[^}]*\}-|(?:PI_WEB_DIST_DIR|distDir)\s*\+\s*["'`]-/;
  const offenders = readdirSync(e2eDir).filter((f) => f.endsWith(".mjs"))
    .filter((f) => sibling.test(readFileSync(new URL(f, e2eDir), "utf8")));
  assert.deepEqual(offenders, []);
});
