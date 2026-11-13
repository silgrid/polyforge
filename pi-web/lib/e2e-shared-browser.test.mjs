import "#test-env";
// the browser suites share one Chromium server that e2e/all.mjs
// starts and names in E2E_BROWSER_WS. A suite gets its browser through
// e2e/harness.mjs's launchBrowser / h.launchBrowser: with the variable set it
// connects to that server, without it the suite's own launch runs (a suite run
// alone behaves as before). Closing a connected browser drops only that
// suite's connection, never the shared server. Fake chromium only: the unit
// bar (npm test, CI) has no Chromium installed; e2e/shared-browser.mjs drives
// the real one.
import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { createHarness, launchBrowser } from "../e2e/harness.mjs";

const WS = "ws://127.0.0.1:41234/0123456789abcdef";

/** A fake browser server: each connect() hands out its own connection, whose close() is counted. */
function fakeServer() {
  const server = { connections: [] };
  server.connect = async (ws) => {
    const connection = { ws, closed: false, closes: 0 };
    connection.close = async () => { connection.closes += 1; connection.closed = true; };
    server.connections.push(connection);
    return connection;
  };
  return server;
}

const neverLaunch = () => { throw new Error("launch() must not run when E2E_BROWSER_WS is set"); };
const neverConnect = (ws) => { throw new Error(`connect(${ws}) must not run without E2E_BROWSER_WS`); };

test("launchBrowser runs the suite's own launch when E2E_BROWSER_WS is unset", async () => {
  for (const env of [{}, { E2E_BROWSER_WS: "" }]) {
    const own = { own: true };
    let launches = 0;
    const browser = await launchBrowser(async () => { launches += 1; return own; }, { env, connect: neverConnect });
    assert.equal(browser, own, `env ${JSON.stringify(env)}: the suite's own browser`);
    assert.equal(launches, 1, `env ${JSON.stringify(env)}: launched once`);
  }
});

test("launchBrowser connects to E2E_BROWSER_WS and never launches", async () => {
  const server = fakeServer();
  const browser = await launchBrowser(neverLaunch, { env: { E2E_BROWSER_WS: WS }, connect: server.connect });
  assert.equal(server.connections.length, 1, "one connection to the shared server");
  assert.equal(server.connections[0].ws, WS, "connected to the runner's address");
  assert.equal(browser, server.connections[0], "the suite's browser is that connection");
});

test("h.launchBrowser keeps the browser and h.close() closes only that connection", async () => {
  const server = fakeServer();
  const options = { env: { E2E_BROWSER_WS: WS }, connect: server.connect };
  const first = createHarness();
  const second = createHarness();
  const firstBrowser = await first.launchBrowser(neverLaunch, options);
  const secondBrowser = await second.launchBrowser(neverLaunch, options);
  assert.equal(server.connections.length, 2, "each suite connects on its own");
  assert.equal(firstBrowser, server.connections[0], "h.launchBrowser returns the connection");
  assert.equal(first.browser, firstBrowser, "h.launchBrowser keeps it as h.browser");
  assert.equal(second.browser, secondBrowser);

  await first.close();
  assert.equal(server.connections[0].closes, 1, "h.close() closes its own connection once");
  assert.equal(server.connections[1].closed, false, "the other suite's connection stays open");

  const own = { own: true, close: async () => {} };
  const alone = createHarness();
  assert.equal(await alone.launchBrowser(async () => own, { env: {}, connect: neverConnect }), own);
  assert.equal(alone.browser, own, "without the variable h.browser is the suite's own browser");
  await second.close();
  await alone.close();
});

// The suites e2e/all.mjs runs, discovered as it does (its top-level script
// cannot be imported): every e2e/*.mjs except files other e2e files import
// (helpers), the conf's `exclude=` globs, and all.mjs itself; kept when the
// source has the runner's browser marker, the literal `chromium.launch`.
const e2eRoot = fileURLToPath(new URL("..", import.meta.url));
function runnerBrowserSuites() {
  const dir = join(e2eRoot, "e2e");
  const files = readdirSync(dir).filter((f) => f.endsWith(".mjs")).sort();
  const source = new Map(files.map((f) => [f, readFileSync(join(dir, f), "utf8")]));
  const imported = new Set();
  for (const text of source.values()) {
    for (const m of text.matchAll(/from\s+["']\.\/([\w.-]+\.mjs)["']/g)) imported.add(m[1]);
  }
  const confPath = join(e2eRoot, ".polyforge/verify-scope.conf");
  const excludes = existsSync(confPath)
    ? readFileSync(confPath, "utf8").split("\n").filter((l) => l.startsWith("exclude=")).map((l) => l.slice(8).trim())
    : [];
  const globToRe = (g) => new RegExp("^" + g.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, ".*") + "$");
  const excluded = (f) => excludes.some((g) => globToRe(g).test(f) || globToRe(g).test(`e2e/${f}`));
  const marker = "chromium" + ".launch";
  return new Map([...source].filter(([f, text]) =>
    f !== "all.mjs" && !imported.has(f) && !excluded(f) && text.includes(marker)));
}

const importsCreateHarness = (text) =>
  /import\s*\{[^}]*\bcreateHarness\b[^}]*\}\s*from\s*["']\.\/harness\.mjs["']/.test(text);

// A suite's own launch is allowed only as the launchBrowser fallback closure.
const launchesOutsideHelper = (text) =>
  text.split("launchBrowser(() => chromium.launch())").join("").includes("chromium.launch(");

test("every createHarness browser suite launches through h.launchBrowser", () => {
  const suites = [...runnerBrowserSuites()].filter(([, text]) => importsCreateHarness(text));
  assert.ok(suites.length >= 48, `the runner's createHarness browser suites (found ${suites.length})`);
  const offenders = suites.filter(([, text]) => launchesOutsideHelper(text)).map(([f]) => `e2e/${f}`);
  assert.deepEqual(offenders, [], "createHarness suites calling chromium.launch( outside h.launchBrowser(() => chromium.launch())");
});

test("every other browser suite but manual-fetch and pdf-page-fragment launches through launchBrowser", () => {
  // manual-fetch.mjs keeps its own Chromium ( AC1);
  // pdf-page-fragment.mjs needs full Chromium's PDF viewer (channel "chromium"),
  // which the shared headless server does not have.
  const allowed = new Set(["manual-fetch.mjs", "pdf-page-fragment.mjs"]);
  const suites = [...runnerBrowserSuites()].filter(([, text]) => !importsCreateHarness(text));
  const names = suites.map(([f]) => f);
  for (const f of allowed) assert.ok(names.includes(f), `${f} is still a runner browser suite`);
  const offenders = suites.filter(([f, text]) => !allowed.has(f) && launchesOutsideHelper(text)).map(([f]) => `e2e/${f}`);
  assert.deepEqual(offenders, [], "browser suites calling chromium.launch( outside launchBrowser(() => chromium.launch())");
});

test("polyforge-context-usage.mjs launches its browser through h.launchBrowser", () => {
  const text = runnerBrowserSuites().get("polyforge-context-usage.mjs");
  assert.ok(text, "polyforge-context-usage.mjs is still a runner browser suite");
  assert.ok(importsCreateHarness(text), "polyforge-context-usage.mjs is a createHarness suite");
  assert.ok(text.includes("h.launchBrowser(() => chromium.launch())"), "polyforge-context-usage.mjs launches through h.launchBrowser(() => chromium.launch())");
  assert.equal(launchesOutsideHelper(text), false, "polyforge-context-usage.mjs calls chromium.launch( nowhere else");
});
