import "#test-env";
// e2e/all.mjs starts one Chromium server (`chromium.launchServer()`)
// for every browser suite of a run and names it in E2E_BROWSER_WS; serial-lane
// suites never see the variable. The runner closes the server when every lane
// (the isolated suites included) has settled. When the server process dies
// while suites run on it, each of those suites' FAIL blocks carries a runner
// line that verify's units.py classifies as a browser crash (its BROWSER_CRASH,
// `\b(?:Target|Page) crashed\b`, scanned inside `===== FAIL <suite> =====`
// blocks), the runner names the crash before its summary, and a browser suite
// started afterwards gets a new server.
//
// The test copies the real runner into a throwaway root (the
// lib/e2e-all-*.test.mjs pattern) with a stub `playwright` whose launchServer
// spawns a real idle node process as the "browser server" and logs its launch
// and close; its ws endpoint names that pid. No Chromium is needed. Every
// server process the stub started is killed when the test ends.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const runnerSource = readFileSync(new URL("../e2e/all.mjs", import.meta.url), "utf8");
const jobsSource = readFileSync(new URL("../e2e/runner/jobs.mjs", import.meta.url), "utf8");

// units.py BROWSER_CRASH: what verify reads as a browser crash in a FAIL block.
const BROWSER_CRASH = /\b(?:Target|Page) crashed\b/;

const playwrightStub = (log) => `import { spawn } from "node:child_process";
import { appendFileSync } from "node:fs";
export const chromium = {
  executablePath: () => process.execPath,
  async launchServer() {
    const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)", "fixture-shared-browser"], { stdio: "ignore" });
    await new Promise((resolve, reject) => { child.once("spawn", resolve); child.once("error", reject); });
    appendFileSync(${JSON.stringify(log)}, "launch " + child.pid + "\\n");
    return {
      wsEndpoint: () => "ws://127.0.0.1:9/fixture-" + child.pid,
      process: () => child,
      async close() {
        if (child.exitCode === null && child.signalCode === null) {
          const gone = new Promise((resolve) => child.once("exit", resolve));
          child.kill("SIGTERM");
          await gone;
        }
        appendFileSync(${JSON.stringify(log)}, "close " + child.pid + "\\n");
      },
    };
  },
};
`;

// Shared by the fixture suites: the server pid named in E2E_BROWSER_WS, whether
// a pid is alive, a bounded poll, and a record of what the suite saw.
const prelude = (work, name) => `import { appendFileSync, existsSync, writeFileSync } from "node:fs";
const ws = process.env.E2E_BROWSER_WS ?? null;
const pid = Number(/fixture-(\\d+)$/.exec(ws ?? "")?.[1] ?? NaN);
const alive = (p) => { if (!Number.isInteger(p)) return false; try { process.kill(p, 0); return true; } catch (e) { return e.code === "EPERM"; } };
const until = async (what, ok) => {
  const deadline = Date.now() + 20_000;
  while (!ok()) {
    if (Date.now() > deadline) { console.log("FAIL: ${name}\\n  - timed out waiting for " + what); process.exit(1); }
    await new Promise((r) => setTimeout(r, 25));
  }
};
appendFileSync(${JSON.stringify(join(work, "seen.jsonl"))}, JSON.stringify({ suite: ${JSON.stringify(name)}, ws, serverAlive: alive(pid) }) + "\\n");
const mark = (what) => writeFileSync(${JSON.stringify(work)} + "/" + what, "");
const marked = (what) => existsSync(${JSON.stringify(work)} + "/" + what);
`;

// A browser suite as the migrated suites spell it: the runner's lane marker is
// the literal in `await h.launchBrowser(() => chromium.launch())`.
const browserSuite = (work, name, body, header = "") => `${header}// await h.launchBrowser(() => chromium${".launch"}())
${prelude(work, name)}${body}
`;

function fixtureRoot(prefix) {
  const work = mkdtempSync(join(tmpdir(), prefix));
  mkdirSync(join(work, "e2e/runner"), { recursive: true });
  mkdirSync(join(work, "node_modules/playwright"), { recursive: true });
  writeFileSync(join(work, "package.json"), JSON.stringify({ scripts: {} }));
  writeFileSync(join(work, "e2e/all.mjs"), runnerSource);
  writeFileSync(join(work, "e2e/runner/jobs.mjs"), jobsSource);
  writeFileSync(join(work, "node_modules/playwright/package.json"), JSON.stringify({ name: "playwright", type: "module", main: "index.js" }));
  writeFileSync(join(work, "node_modules/playwright/index.js"), playwrightStub(join(work, "browser.log")));
  return work;
}

// The operator's own E2E_BROWSER_WS must not leak into any suite.
function runAll(work, jobs) {
  const env = { ...process.env, E2E_JOBS: String(jobs), E2E_BROWSER_WS: "ws://127.0.0.1:9/operator-browser" };
  delete env.E2E_SERVER_MODE;
  delete env.E2E_PREBUILT_DIST;
  const run = spawnSync(process.execPath, ["e2e/all.mjs"], { cwd: work, encoding: "utf8", timeout: 60_000, env });
  return { status: run.status, output: `${run.stdout}${run.stderr}` };
}

const browserLog = (work) => existsSync(join(work, "browser.log"))
  ? readFileSync(join(work, "browser.log"), "utf8").trim().split("\n").filter(Boolean).map((l) => {
    const [event, pid] = l.split(" ");
    return { event, pid: Number(pid) };
  })
  : [];
const seen = (work) => Object.fromEntries((existsSync(join(work, "seen.jsonl"))
  ? readFileSync(join(work, "seen.jsonl"), "utf8").trim().split("\n").filter(Boolean).map((l) => JSON.parse(l))
  : []).map((s) => [s.suite, s]));
const alive = (pid) => { try { process.kill(pid, 0); return true; } catch (e) { return e.code === "EPERM"; } };
const wsPid = (ws) => Number(/fixture-(\d+)$/.exec(ws ?? "")?.[1] ?? NaN);
// The runner's echoed block of one suite, up to the next `=====` line.
const block = (output, verdict, suite) =>
  new RegExp(`^===== ${verdict} ${suite.replace(/\./g, "\\.")} \\(\\d+s\\) =====$([\\s\\S]*?)^=====`, "m").exec(output)?.[1] ?? null;

function cleanup(work) {
  for (const { event, pid } of browserLog(work)) {
    if (event === "launch" && alive(pid)) {
      try { process.kill(pid, "SIGKILL"); } catch { /* already gone */ }
    }
  }
  rmSync(work, { recursive: true, force: true });
}

test("one shared browser serves every browser suite and is closed when the run ends", { timeout: 90_000 }, () => {
  const work = fixtureRoot("e2e-all-shared-browser-");
  try {
    const pass = (name) => `if (!alive(pid)) { console.log("FAIL: ${name}\\n  - no live shared browser in E2E_BROWSER_WS=" + ws); process.exit(1); }\nconsole.log("PASS: ${name}");`;
    for (const name of ["alpha", "beta", "gamma"]) writeFileSync(join(work, `e2e/${name}.mjs`), browserSuite(work, name, pass(name)));
    writeFileSync(join(work, "e2e/solo.mjs"), browserSuite(work, "solo", pass("solo"), "// e2e/all: isolated\n"));
    // A serial-lane suite (no browser marker).
    writeFileSync(join(work, "e2e/serial.mjs"), `${prelude(work, "serial")}console.log("PASS: serial");\n`);

    const { status, output } = runAll(work, 2);
    const log = browserLog(work);
    const launches = log.filter((e) => e.event === "launch");
    assert.equal(launches.length, 1, `one launchServer for the whole run:\n${JSON.stringify(log)}\n${output}`);
    const [{ pid }] = launches;
    const s = seen(work);
    for (const name of ["alpha", "beta", "gamma", "solo"]) {
      assert.ok(s[name], `${name} never ran:\n${output}`);
      assert.equal(wsPid(s[name].ws), pid, `${name} got E2E_BROWSER_WS=${s[name].ws}, not the run's shared browser`);
      assert.equal(s[name].serverAlive, true, `${name} ran while the shared browser was down`);
    }
    assert.equal(s.serial?.ws, null, `a serial-lane suite saw E2E_BROWSER_WS=${s.serial?.ws}`);
    assert.equal(status, 0, output);
    assert.deepEqual(log.map((e) => e.event), ["launch", "close"], `the runner closes its shared browser once at the end:\n${output}`);
    assert.equal(log[1].pid, pid);
    assert.equal(alive(pid), false, "the shared browser's process outlived the run");
  } finally {
    cleanup(work);
  }
});

test("a shared browser that dies fails the suites running on it as a browser crash and later suites get a new one", { timeout: 90_000 }, () => {
  const work = fixtureRoot("e2e-all-shared-browser-crash-");
  try {
    // a-killer waits until both victims run on the shared browser, then SIGKILLs it.
    writeFileSync(join(work, "e2e/a-killer.mjs"), browserSuite(work, "a-killer", `await until("both victims", () => marked("started-b-victim") && marked("started-c-victim"));
process.kill(pid, "SIGKILL");
await until("the shared browser to die", () => !alive(pid));
console.log("PASS: a-killer");`));
    // The victims fail as a suite on a dead shared browser does (harness.mjs launchBrowser).
    for (const name of ["b-victim", "c-victim"]) {
      writeFileSync(join(work, `e2e/${name}.mjs`), browserSuite(work, name, `mark("started-${name}");
await until("the shared browser to die", () => !alive(pid));
console.log("FAIL: shared-browser\\n  - the shared browser (E2E_BROWSER_WS=" + ws + ") disconnected while this suite ran: the runner's Chromium died");
process.exit(1);`));
    }
    // d-later starts only once a pool slot frees, after the crash.
    writeFileSync(join(work, "e2e/d-later.mjs"), browserSuite(work, "d-later", `if (!alive(pid)) { console.log("FAIL: d-later\\n  - no live shared browser in E2E_BROWSER_WS=" + ws); process.exit(1); }
console.log("PASS: d-later");`));

    const { status, output } = runAll(work, 3);
    const log = browserLog(work);
    const launches = log.filter((e) => e.event === "launch").map((e) => e.pid);
    assert.equal(launches.length, 2, `a crashed shared browser is replaced once:\n${JSON.stringify(log)}\n${output}`);
    const [first, second] = launches;
    const s = seen(work);
    for (const name of ["a-killer", "b-victim", "c-victim"]) assert.equal(wsPid(s[name]?.ws), first, `${name}:\n${output}`);
    assert.equal(wsPid(s["d-later"]?.ws), second, `the suite after the crash did not get the new shared browser:\n${output}`);
    assert.equal(status, 1, output);

    for (const name of ["b-victim.mjs", "c-victim.mjs"]) {
      const lines = block(output, "FAIL", name);
      assert.ok(lines !== null, `no FAIL block for ${name}:\n${output}`);
      const crash = lines.split("\n").filter((l) => l.startsWith("[all.mjs] ") && BROWSER_CRASH.test(l));
      assert.equal(crash.length, 1, `${name}'s FAIL block has no runner line verify reads as a browser crash:\n${lines}`);
      assert.match(crash[0], /SIGKILL/, "the crash line names how the server process exited");
    }
    assert.ok(block(output, "PASS", "d-later.mjs") !== null, `the suite after the crash did not pass:\n${output}`);
    assert.match(output, /^e2e\/all: the shared browser crashed 1 time\(s\)/m, "the runner reports the crash");
    assert.ok(output.indexOf("the shared browser crashed 1 time(s)") < output.indexOf("===== summary ====="), "the crash report precedes the summary");

    assert.deepEqual(log.map((e) => `${e.event} ${e.pid}`), [`launch ${first}`, `launch ${second}`, `close ${second}`], output);
    assert.equal(alive(second), false, "the replacement shared browser outlived the run");
  } finally {
    cleanup(work);
  }
});
