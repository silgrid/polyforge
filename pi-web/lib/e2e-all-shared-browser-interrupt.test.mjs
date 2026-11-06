import "#test-env";
// review r1#1: e2e/all.mjs must start no suite after an
// interrupt (the guarantee, lib/e2e-all-interrupt.test.mjs) even
// while a browser suite still waits for the shared browser server to start:
// the signal handler has already SIGTERMed `running`, so a suite spawned once
// launchServer resolves would be orphaned with its detached `next dev` group.
// The runner owns the signals, so it starts the server with Playwright's own
// SIGINT/SIGTERM/SIGHUP handlers off (review r1#2): Playwright's SIGINT handler
// exits the process before the runner prints its summary.
//
// The test copies the real runner into a throwaway root with a stub
// `playwright` whose launchServer logs its options and then blocks until the
// runner process receives the signal: its own once-listener is registered after
// the runner's handler (installed before any suite runs), so it fires after
// that handler has run, and only then does the server start complete. The one
// fixture browser suite only leaves a marker that it was spawned. The marker
// must stay absent, the runner must exit non-zero and close the server it got.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const runnerSource = readFileSync(new URL("../e2e/all.mjs", import.meta.url), "utf8");
const jobsSource = readFileSync(new URL("../e2e/runner/jobs.mjs", import.meta.url), "utf8");

const playwrightStub = (work) => `import { spawn } from "node:child_process";
import { appendFileSync } from "node:fs";
const log = ${JSON.stringify(join(work, "browser.log"))};
export const chromium = {
  executablePath: () => process.execPath,
  async launchServer(options) {
    // Complete only once the runner's own signal handler has run (or after a bound).
    const signalled = new Promise((resolve) => {
      const timer = setTimeout(resolve, 30_000);
      for (const sig of ["SIGINT", "SIGTERM"]) process.once(sig, () => { clearTimeout(timer); resolve(); });
    });
    appendFileSync(log, "starting " + JSON.stringify(options ?? {}) + "\\n");
    await signalled;
    const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)", "fixture-shared-browser"], { stdio: "ignore" });
    await new Promise((resolve, reject) => { child.once("spawn", resolve); child.once("error", reject); });
    appendFileSync(log, "launch " + child.pid + "\\n");
    return {
      wsEndpoint: () => "ws://127.0.0.1:9/fixture-" + child.pid,
      process: () => child,
      async close() {
        if (child.exitCode === null && child.signalCode === null) {
          const gone = new Promise((resolve) => child.once("exit", resolve));
          child.kill("SIGTERM");
          await gone;
        }
        appendFileSync(log, "close " + child.pid + "\\n");
      },
    };
  },
};
`;

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const alive = (pid) => { try { process.kill(pid, 0); return true; } catch (e) { return e.code === "EPERM"; } };
const logLines = (work) => existsSync(join(work, "browser.log"))
  ? readFileSync(join(work, "browser.log"), "utf8").trim().split("\n").filter(Boolean)
  : [];

async function interruptWhileStarting(signal) {
  const work = mkdtempSync(join(tmpdir(), "e2e-all-shared-browser-interrupt-"));
  const suiteRan = join(work, "suite-ran");
  let child = null;
  try {
    mkdirSync(join(work, "e2e/runner"), { recursive: true });
    mkdirSync(join(work, "node_modules/playwright"), { recursive: true });
    writeFileSync(join(work, "package.json"), JSON.stringify({ scripts: {} }));
    writeFileSync(join(work, "e2e/all.mjs"), runnerSource);
    writeFileSync(join(work, "e2e/runner/jobs.mjs"), jobsSource);
    writeFileSync(join(work, "node_modules/playwright/package.json"), JSON.stringify({ name: "playwright", type: "module", main: "index.js" }));
    writeFileSync(join(work, "node_modules/playwright/index.js"), playwrightStub(work));
    // A browser suite (the runner's lane marker) that only records it was spawned.
    writeFileSync(
      join(work, "e2e/browser.mjs"),
      `// await h.launchBrowser(() => chromium${".launch"}())\n`
        + `import { writeFileSync } from "node:fs";\n`
        + `writeFileSync(${JSON.stringify(suiteRan)}, "1");\n`,
    );

    const env = { ...process.env, E2E_JOBS: "2" };
    delete env.E2E_SERVER_MODE;
    delete env.E2E_PREBUILT_DIST;
    delete env.E2E_BROWSER_WS;
    child = spawn(process.execPath, ["e2e/all.mjs"], { cwd: work, env, stdio: ["ignore", "pipe", "pipe"] });
    let output = "";
    child.stdout.on("data", (d) => { output += d; });
    child.stderr.on("data", (d) => { output += d; });
    const exited = new Promise((resolve) => child.once("close", (code, sig) => resolve({ code, signal: sig })));

    const startDeadline = Date.now() + 15_000;
    while (!logLines(work).some((l) => l.startsWith("starting ")) && Date.now() < startDeadline && child.exitCode === null) {
      await sleep(25);
    }
    assert.ok(logLines(work).some((l) => l.startsWith("starting ")), `launchServer was never called:\n${output}`);

    child.kill(signal);

    const exit = await Promise.race([exited, sleep(25_000).then(() => "timeout")]);
    assert.notEqual(exit, "timeout", `the runner did not exit after ${signal}:\n${output}`);
    assert.equal(existsSync(suiteRan), false, `a browser suite started after ${signal}:\n${output}`);
    assert.notEqual(exit.code, 0, `the interrupted run exited 0:\n${output}`);
    const options = JSON.parse(logLines(work).find((l) => l.startsWith("starting ")).slice("starting ".length));
    assert.deepEqual(
      { handleSIGINT: options.handleSIGINT, handleSIGTERM: options.handleSIGTERM, handleSIGHUP: options.handleSIGHUP },
      { handleSIGINT: false, handleSIGTERM: false, handleSIGHUP: false },
      "the runner keeps the signals: launchServer gets Playwright's own handlers off",
    );
    const launched = logLines(work).filter((l) => l.startsWith("launch ")).map((l) => Number(l.split(" ")[1]));
    for (const pid of launched) {
      assert.ok(logLines(work).includes(`close ${pid}`), `the shared browser ${pid} was not closed:\n${output}`);
    }
  } finally {
    if (child && child.exitCode === null && child.signalCode === null) {
      try { child.kill("SIGKILL"); } catch { /* already gone */ }
    }
    for (const l of logLines(work)) {
      const pid = l.startsWith("launch ") ? Number(l.split(" ")[1]) : NaN;
      if (Number.isInteger(pid) && alive(pid)) {
        try { process.kill(pid, "SIGKILL"); } catch { /* already gone */ }
      }
    }
    rmSync(work, { recursive: true, force: true });
  }
}

test("a browser suite waiting for the shared browser is not started after SIGTERM", { timeout: 60_000 }, () => interruptWhileStarting("SIGTERM"));

test("a browser suite waiting for the shared browser is not started after SIGINT", { timeout: 60_000 }, () => interruptWhileStarting("SIGINT"));
