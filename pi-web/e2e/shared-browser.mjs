// carries: e2e/harness.mjs
// the browser suites share one Chromium server (e2e/all.mjs
// starts it and names it in E2E_BROWSER_WS); a suite connects through
// e2e/harness.mjs's launchBrowser. This suite drives the real Playwright
// server, no next server (pages are `setContent`):
//
// SB1  two suites' connections each open a context; closing one suite
//      (`h.close()`) leaves the server process running and the other suite's
//      page answering, and that suite is not reported as failed.
// SB2  a suite (a child process importing the harness, with E2E_BROWSER_WS)
//      connected to the server with a page open; the server process is
//      SIGKILLed: the suite exits non-zero and says why, with the
//      `FAIL: shared-browser` scenario marker the runner's failures section lists.
//
// It starts its own servers with chromium.launchServer (the literal
// `chromium.launch` there also makes e2e/all.mjs run it in the browser lane).
//
//   node e2e/shared-browser.mjs
//   npm run test:e2e:all -- shared-browser.mjs
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { chromium } from "playwright";
import { createCheck, createHarness, createWaitUntil, launchBrowser, pidAlive, root } from "./harness.mjs";

const MARKER = "FAIL: shared-browser";
const SETTLE_MS = 30_000;
const neverLaunch = () => { throw new Error("launch() ran although E2E_BROWSER_WS was set"); };
const check = createCheck();
const waitFor = createWaitUntil({ every: 100, withLast: true });

/** Runs `fn(server)` against a fresh Chromium server, closed (or reaped) afterwards. */
async function withServer(fn) {
  const server = await chromium.launchServer();
  try {
    return await fn(server);
  } finally {
    await server.close().catch(() => {});
  }
}

/** A page with `text` in a new context of `browser`. */
async function pageWith(browser, text) {
  const page = await (await browser.newContext()).newPage();
  await page.setContent(`<p id="t">${text}</p>`);
  return page;
}

await check("SB1", () => withServer(async (server) => {
  const env = { E2E_BROWSER_WS: server.wsEndpoint() };
  const printed = [];
  const write = process.stdout.write;
  process.stdout.write = function (chunk, ...rest) {
    printed.push(String(chunk));
    return write.call(this, chunk, ...rest);
  };
  const exitCode = process.exitCode;
  try {
    const first = createHarness();
    await first.launchBrowser(neverLaunch, { env });
    const other = await launchBrowser(neverLaunch, { env });
    try {
      await pageWith(first.browser, "first");
      const page = await pageWith(other, "other");
      await first.close(); // resolves once the browser's "disconnected" has been emitted
      assert.equal(first.browser.isConnected(), false, "the closed suite's connection is gone");
      assert.equal(server.process().exitCode, null, "the server process still runs after one suite closed");
      assert.ok(pidAlive(server.process().pid), "the server pid is alive");
      assert.equal(other.isConnected(), true, "the other suite stays connected");
      assert.equal(await page.locator("#t").textContent(), "other", "the other suite's page still answers");
      assert.ok(!printed.join("").includes(MARKER), `a suite that closed itself printed ${MARKER}`);
      assert.equal(process.exitCode, exitCode, "closing its own connection does not fail the suite");
    } finally {
      await other.close().catch(() => {});
    }
  } finally {
    process.stdout.write = write;
  }
}));

await check("SB2", () => withServer(async (server) => {
  const dir = mkdtempSync(join(tmpdir(), "pi-web-e2e-shared-browser-"));
  const script = join(dir, "suite.mjs");
  // A suite as the harness runs one: its page fails once the browser is gone,
  // it reports that scenario and closes its harness as it would on any failure.
  writeFileSync(script, `
import { createHarness } from ${JSON.stringify(pathToFileURL(join(root, "e2e/harness.mjs")).href)};
import { setTimeout as delay } from "node:timers/promises";
const h = createHarness();
await h.launchBrowser(() => { throw new Error("launch() ran although E2E_BROWSER_WS was set"); });
const page = await (await h.browser.newContext()).newPage();
await page.setContent("<p>ready</p>");
console.log("READY");
const deadline = Date.now() + ${SETTLE_MS};
try {
  while (Date.now() < deadline) { await page.evaluate(() => 1); await delay(50); }
  console.log("FAIL child: the page kept answering");
} catch (error) {
  console.log("FAIL child: " + String(error?.message ?? error).split("\\n")[0]);
}
process.exitCode = 1;
await h.close();
`);
  let output = "";
  let exit;
  const child = spawn(process.execPath, [script], { cwd: root, env: { ...process.env, E2E_BROWSER_WS: server.wsEndpoint() }, stdio: ["ignore", "pipe", "pipe"] });
  child.stdout.on("data", (chunk) => { output += chunk; });
  child.stderr.on("data", (chunk) => { output += chunk; });
  child.on("exit", (code, signal) => { exit = { code, signal }; });
  try {
    await waitFor(() => output.includes("READY") || exit, SETTLE_MS, () => `the suite connected and opened a page (output: ${output})`);
    assert.equal(exit, undefined, `the suite ended before the browser died: ${output}`);
    server.process().kill("SIGKILL");
    await waitFor(() => exit, SETTLE_MS, () => `the suite ended after the shared browser died (output: ${output})`);
    assert.notEqual(exit.code, 0, `the suite exits non-zero (output: ${output})`);
    assert.ok(output.includes(MARKER), `the suite's output names the cause with ${MARKER}: ${output}`);
  } finally {
    if (!exit) {
      child.kill("SIGKILL");
      await waitFor(() => exit, SETTLE_MS, "the killed suite exited");
    }
    rmSync(dir, { force: true, recursive: true });
  }
}));

if (check.failures.length) {
  process.exitCode = 1;
  console.log(`FAIL shared-browser: ${check.failures.length} scenario(s) failed`);
}
