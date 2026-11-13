// review: the isolated phase must not start a suite after an
// interrupt.
//
// e2e/all.mjs runs suites marked `e2e/all: isolated` after both regular lanes
// drain. The signal handler SIGTERMs the running children and arms an 11s
// process.exit(130); the isolated loop must stop before spawning anything then,
// or the exit orphans that suite's detached `next dev` + Chromium group.
//
// The test copies the real runner into a throwaway root with two fixture
// suites: a regular one that blocks and an isolated one that leaves a marker.
// It sends SIGINT while the regular suite is running: the runner must exit and
// the isolated marker must stay absent. On the pre-fix loop the marker appears.
import "#test-env";
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawn } from "node:child_process";
import test from "node:test";

const runnerSource = readFileSync(new URL("../e2e/all.mjs", import.meta.url), "utf8");
const jobsSource = readFileSync(new URL("../e2e/runner/jobs.mjs", import.meta.url), "utf8");

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

test("the isolated phase starts no suite after SIGINT", { timeout: 30_000 }, async () => {
  const work = mkdtempSync(join(tmpdir(), "e2e-all-interrupt-"));
  const regularStartedAt = join(work, "regular-started");
  const isolatedRanAt = join(work, "isolated-ran");
  let child = null;
  try {
    mkdirSync(join(work, "e2e/runner"), { recursive: true });
    mkdirSync(join(work, "node_modules"));
    writeFileSync(join(work, "package.json"), JSON.stringify({ scripts: {} }));
    writeFileSync(join(work, "e2e/all.mjs"), runnerSource);
    writeFileSync(join(work, "e2e/runner/jobs.mjs"), jobsSource);
    // The regular suite announces that it is running, then blocks until the
    // runner's SIGTERM reaches its process group.
    writeFileSync(
      join(work, "e2e/regular.mjs"),
      `import { writeFileSync } from "node:fs";\n`
        + `writeFileSync(${JSON.stringify(regularStartedAt)}, "1");\n`
        + `setTimeout(() => {}, 60_000);\n`,
    );
    // The isolated suite only records that it was spawned at all.
    writeFileSync(
      join(work, "e2e/isolated.mjs"),
      `// e2e/all: isolated\n`
        + `import { writeFileSync } from "node:fs";\n`
        + `writeFileSync(${JSON.stringify(isolatedRanAt)}, "1");\n`,
    );

    child = spawn(process.execPath, ["e2e/all.mjs", "regular.mjs", "isolated.mjs"], {
      cwd: work,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let output = "";
    child.stdout.on("data", (d) => { output += d; });
    child.stderr.on("data", (d) => { output += d; });

    const startDeadline = Date.now() + 10_000;
    while (!existsSync(regularStartedAt) && Date.now() < startDeadline && child.exitCode === null) {
      await sleep(50);
    }
    assert.ok(existsSync(regularStartedAt), `the regular suite never started:\n${output}`);

    child.kill("SIGINT");
    const exit = await new Promise((resolve) => {
      const timer = setTimeout(() => resolve("timeout"), 20_000);
      child.once("close", (code, signal) => {
        clearTimeout(timer);
        resolve({ code, signal });
      });
    });
    assert.notEqual(exit, "timeout", `the runner did not exit after SIGINT:\n${output}`);
    assert.equal(existsSync(isolatedRanAt), false, `an isolated suite started after SIGINT:\n${output}`);
  } finally {
    if (child && child.exitCode === null) {
      try { child.kill("SIGKILL"); } catch { /* already gone */ }
    }
    rmSync(work, { recursive: true, force: true });
  }
});