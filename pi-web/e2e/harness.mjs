// Shared scaffolding for the e2e suites: a helper, not a suite
// (no `// carries:` header; e2e/all.mjs and the verify-scope engine treat an
// imported, undeclared e2e file as a helper, so a change here selects every
// suite importing it). The suites' copies of these helpers differed only in
// parameters (poll interval, timeouts, failure-line shape, viewport, locale,
// error capture); each is defined once here and a suite states its
// parameters. A knob's default is the value most suites spelled.
// e2e/all.mjs tells a browser suite by the literal `chromium.launch` in its
// own source, so a suite spells its own launch, which runs only without the
// runner's shared browser: `await h.launchBrowser(() => chromium.launch())`.
import assert from "node:assert/strict";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { startNextServer } from "./next-server.mjs";
import { COLD_MS, NAV_MS } from "./timeouts.mjs";

/** The pi-web package root (no trailing slash). */
export const root = dirname(dirname(fileURLToPath(import.meta.url)));

/** Fails fast when a `next dev` holds the dist dir (default the runner's, else `.next`). */
export function assertNoDevServer(distDir = process.env.PI_WEB_DIST_DIR || ".next") {
  assert.ok(!existsSync(join(root, distDir, "dev/lock")), "Run in a checkout without an active dev server");
}

/** The suite's own dist dir: the runner's PI_WEB_DIST_DIR, else `.next/e2e/<name>`. */
export const suiteDist = (name) => process.env.PI_WEB_DIST_DIR || `.next/e2e/${name}`;

// Scenario bookkeeping

/** The first line of a thrown value's message. */
export const firstLine = (error) => String(error?.message ?? error).split("\n")[0];
/** Prints each titled scenario's PASS/FAIL (an unreached one fails) and marks the run failed on any. */
export function reportTitled(failures, ran) {
  for (const [title, list] of failures) {
    if (!ran.has(title)) list.push("scenario not reached");
    console.log(list.length ? `FAIL: ${title}\n  - ${list.join("\n  - ")}` : `PASS: ${title}`);
    if (list.length) process.exitCode = 1;
  }
}
/** A setup crash: logged, recorded as `setup: <message>`, the run marked failed. */
export function setupFailed(failures, error) {
  console.error(error);
  process.exitCode = 1;
  failures.push(`setup: ${error instanceof Error ? error.message : String(error)}`);
}
/** Whether two values are deeply equal (assert.deepStrictEqual's rule), as a boolean. */
export function same(actual, expected) {
  try {
    assert.deepStrictEqual(actual, expected);
    return true;
  } catch {
    return false;
  }
}
/** A soft assertion: `check(condition, message)` records `message` when the condition fails. */
export const softCheck = (failures) => (condition, message) => {
  if (!condition) failures.push(message);
};
/** A thrown value's message, whole. */
export const errorText = (error) => (error instanceof Error ? error.message : String(error));
/** The raw message, as `${error?.message ?? error}` prints it. */
export const rawMessage = (error) => `${error?.message ?? error}`;
/** The first `n` lines of a thrown value's message joined with " | ", at most `max` characters. */
export const joinedLines = (n, max = Infinity) => (error) => String(error?.message ?? error).split("\n").slice(0, n).join(" | ").slice(0, max);
/** The `FAIL: <name>` line with the message indented below it. */
export const blockFail = (names, text) => console.log(`FAIL: ${names[0]}\n  ${text.split("\n").join("\n  ")}`);
/** The `PASS: <name>` line. */
export const blockPass = (names) => console.log(`PASS: ${names[0]}`);

/**
 * `check(...names, fn)` runs `fn`, prints PASS/FAIL and records a failure;
 * never throws. Options: `failures` (array pushed `record(names, text,
 * error)`, default the first name; or a recorder function), `message` (the
 * recorded text, default the first line), `log` (the FAIL line's text),
 * `pass`/`fail` (print the lines; null: silent), `skip`, `before`, `after`,
 * `passed` hooks. `check.count` counts the scenarios run.
 */
export function createCheck({
  failures = [],
  record = (names) => names[0],
  message = firstLine,
  log = message,
  pass = (names) => console.log(`PASS ${names[0]}`),
  fail = (names, text) => console.log(`FAIL ${names.join(" ")}: ${text}`),
  skip, before, after, passed,
} = {}) {
  const check = async (...args) => {
    const fn = args.pop();
    if (skip?.(args)) return;
    check.count += 1;
    await before?.(args);
    try {
      await fn();
      passed?.(args);
      pass?.(args);
    } catch (error) {
      const text = message(error);
      if (typeof failures === "function") failures(args, text, error);
      else failures.push(record(args, text, error));
      fail?.(args, log(error), error);
    } finally {
      await after?.(args);
    }
  };
  check.count = 0;
  check.failures = failures;
  return check;
}

// Bounded polls

/**
 * `waitFor(fn, label, ms)`: polls every `every` ms until `done(answer)`, then
 * answers it; a throw is remembered as the last answer (`rethrow: true` ends
 * the wait instead). At the deadline it throws `describe(label, last)`.
 */
export function createWaitFor({ ms = 12_000, every = 200, done = Boolean, describe = (label, last) => `${label} (last: ${last})`, rethrow = false } = {}) {
  return async function waitFor(fn, label, timeout = ms) {
    const deadline = Date.now() + timeout;
    let last;
    while (Date.now() < deadline) {
      if (rethrow) last = await fn();
      else {
        try { last = await fn(); } catch (error) { last = error.message; await delay(every); continue; }
      }
      if (done(last)) return last;
      await delay(every);
    }
    throw new Error(describe(label, last));
  };
}

/**
 * `waitFor(predicate, timeoutMs, message, stepMs)`: polls until truthy and
 * answers it (a throw is "not yet"); at the deadline fails an assertion with
 * `message` (a function, sync or async, is read then), `withLast` appending
 * the last answer.
 */
export function createWaitUntil({ every = 250, withLast = false } = {}) {
  return async function waitFor(predicate, timeoutMs, message, stepMs = every) {
    const deadline = Date.now() + timeoutMs;
    let last;
    while (Date.now() < deadline) {
      try {
        const value = await predicate();
        if (value) return value;
        last = value;
      } catch (error) {
        last = error;
      }
      await delay(stepMs);
    }
    const text = typeof message === "function" ? await message() : message;
    if (!withLast) assert.ok(false, text);
    assert.ok(false, `${text}${last === undefined ? "" : ` (last: ${last instanceof Error ? last.message : JSON.stringify(last)})`}`);
  };
}

/** A bounded poll answering true once `predicate` holds, else its final answer. */
export async function eventually(predicate, timeoutMs = 10_000, stepMs = 100) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await predicate()) return true;
    await delay(stepMs);
  }
  return predicate();
}

// Fixtures

/** The host session a suite opens: header + one user message `m1`; answers the file. */
export function writeHostSession(agentDir, { id, cwd, text, timestamp, dir = "e2e" }) {
  const file = join(agentDir, "sessions", dir, `${id}.jsonl`);
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, [
    { type: "session", version: 3, id, timestamp, cwd },
    { type: "message", id: "m1", parentId: null, timestamp, message: { role: "user", content: text } },
  ].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
  return file;
}

// Temp file + rename: a stub polling its state file never reads it half-written.
export const writeFileAtomic = (file, data) => { const tmp = `${file}.${process.pid}.tmp`; writeFileSync(tmp, data); renameSync(tmp, file); };

/** The JSON records of a JSON-lines file ([] when it does not exist; unparsable lines skipped). */
export function readJsonLines(file) {
  if (!existsSync(file)) return [];
  return readFileSync(file, "utf8").split("\n").filter(Boolean).flatMap((line) => {
    try {
      return [JSON.parse(line)];
    } catch {
      return [];
    }
  });
}

/** Whether a process is alive (EPERM: alive, someone else's). */
export function pidAlive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return error.code === "EPERM";
  }
}

// Claude-session suites

/** The claude-session suites' scratch tree, every dir created, the e2e claude stub in `stubDir` unless `stub: false`. */
export function claudeScratch(prefix, { stub = true } = {}) {
  const artifacts = join(root, "test-results/e2e");
  mkdirSync(artifacts, { recursive: true });
  const scratch = mkdtempSync(join(tmpdir(), prefix));
  const home = join(scratch, "home");
  const kit = { artifacts, scratch, home, agentDir: join(home, ".pi", "agent"), project: join(scratch, "project"),
    stubDir: join(scratch, "bin"), stubLog: join(scratch, "claude-stub.jsonl"), configDir: join(scratch, "claude-config") };
  for (const dir of [home, kit.agentDir, kit.project, kit.stubDir, kit.configDir]) mkdirSync(dir, { recursive: true });
  if (stub) {
    copyFileSync(join(root, "e2e/claude-stub/claude.mjs"), join(kit.stubDir, "claude"));
    chmodSync(join(kit.stubDir, "claude"), 0o755);
  }
  return kit;
}

/** A claude-session server's environment over `claudeScratch`'s tree (the stub first on PATH), then `env`. */
export const claudeEnv = (kit, env = {}) => ({
  HOME: kit.home, PI_CODING_AGENT_DIR: kit.agentDir, CLAUDECODE: "1", CLAUDE_CONFIG_DIR: kit.configDir,
  PI_WEB_ALLOWED_ROOT_PREFIXES: kit.scratch, PATH: `${kit.stubDir}:${process.env.PATH ?? ""}`, PI_E2E_CLAUDE_STUB_LOG: kit.stubLog, ...env,
});

/** apiFetch (a body is JSON; `init.timeout`), getJson (asserts 200) and postJson ({status, body}) against `baseOf()`. */
export function createApi(baseOf, { timeoutMs = 120_000 } = {}) {
  const apiFetch = (pathname, init = {}) => fetch(`${baseOf()}${pathname}`, {
    ...init,
    headers: { ...(init.body ? { "Content-Type": "application/json" } : {}), ...(init.headers ?? {}) },
    signal: AbortSignal.timeout(init.timeout ?? timeoutMs),
  });
  const getJson = async (pathname) => {
    const response = await apiFetch(pathname);
    assert.equal(response.status, 200, `GET ${pathname} -> ${response.status}`);
    return response.json();
  };
  const postJson = async (pathname, body) => {
    const response = await apiFetch(pathname, { method: "POST", body: JSON.stringify(body) });
    return { status: response.status, body: await response.json().catch(() => ({})) };
  };
  return { apiFetch, getJson, postJson };
}

/** The transcript claude wrote for session `id` under `configDir`, or null. */
export function findTranscript(configDir, id) {
  let dirs = [];
  try {
    dirs = readdirSync(join(configDir, "projects"));
  } catch {
    return null;
  }
  for (const dir of dirs) {
    const file = join(configDir, "projects", dir, `${id}.jsonl`);
    if (existsSync(file)) return file;
  }
  return null;
}

// Route mocks

/** Seeds a context's localStorage before any page script; default: the classic layout, no session age filter. */
export const seedStorage = (context, entries = { "pi-web:split-view-enabled": "false", "pi-web:session-age-filter-days": "0" }) => context.addInitScript((pairs) => {
  try { for (const [key, value] of pairs) localStorage.setItem(key, value); } catch { /* storage unavailable */ }
}, Object.entries(entries));

/** Fulfils a route with a JSON body. */
export const reply = (route, body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

/** The empty answers dock suites give the reads their scenario does not drive. */
export const NO_MESSAGES = Object.freeze({ pfPresent: true, pfVersion: "v2.21.0", capable: true, conversations: [], contacts: [] });
export const NO_TASKS = Object.freeze({ pfPresent: true, capable: true, tasks: [] });
export const NO_PROGRESS = Object.freeze({ available: true, omitted: 0, lines: [] });

/**
 * Mocks /api/polyforge/*: `handlers` keyed by the path below it (`status`,
 * `cards`, `watch`, `messages`, …; `x/*` a prefix) answer a JSON body, or a
 * function `(url, route)` answering one, or undefined when it settled the
 * route itself. Anything else gets `otherwise` (default `{}`).
 */
export async function mockPolyforge(page, handlers, { otherwise = {} } = {}) {
  const prefixes = Object.keys(handlers).filter((key) => key.endsWith("/*")).map((key) => [key.slice(0, -1), handlers[key]]);
  await page.route((url) => url.pathname.startsWith("/api/polyforge/"), async (route) => {
    const url = new URL(route.request().url());
    const name = url.pathname.slice("/api/polyforge/".length);
    let handler = Object.hasOwn(handlers, name) ? handlers[name] : prefixes.find(([prefix]) => name.startsWith(prefix))?.[1];
    if (handler === undefined) handler = otherwise;
    const body = typeof handler === "function" ? await handler(url, route) : handler;
    if (body !== undefined) await reply(route, body);
  });
}

/**
 * Pins the context reading for `session`: the mount's state read and the live
 * GET /api/agent/<id> read (the newer wins) both answer `usage` (the live one
 * with `running: true`, #236, #247). Other calls pass through.
 */
export async function mockContextUsage(page, session, usage) {
  await page.route("**/api/sessions/*/state", (route) => reply(route, { running: false, state: { contextUsage: usage } }));
  await page.route((url) => url.pathname === `/api/agent/${session}`, (route) => (
    route.request().method() === "GET" ? reply(route, { running: true, state: { contextUsage: usage } }) : route.fallback()
  ));
}

// Browser + server

/** How long a suite waits to connect to the runner's shared browser before it fails. */
const CONNECT_MS = 30_000;

/**
 * The suite's browser. e2e/all.mjs starts one Chromium server
 * for every browser suite and names it in `E2E_BROWSER_WS`: with it set, this
 * connects to that server (`connect(ws)`, default Playwright's
 * `chromium.connect` with a bounded timeout) and `launch` never runs; unset or
 * blank, the suite's own `launch()` runs, so a suite run alone launches its own
 * Chromium as before. Closing a connected browser drops only this suite's
 * contexts and connection, never the shared server. A connection that drops
 * without the suite closing it means the runner's Chromium died: the suite is
 * failed with the `FAIL: shared-browser` scenario marker naming that cause
 * (the pages' own errors only say "Target page, context or browser has been
 * closed").
 */
export async function launchBrowser(launch, { env = process.env, connect } = {}) {
  const ws = env.E2E_BROWSER_WS;
  if (!ws) return launch();
  const open = connect ?? (async (endpoint) => (await import("playwright")).chromium.connect(endpoint, { timeout: CONNECT_MS }));
  const browser = await open(ws);
  let closing = false;
  browser.on?.("disconnected", () => {
    if (closing) return;
    process.exitCode = 1;
    console.log(`FAIL: shared-browser\n  - the shared browser (E2E_BROWSER_WS=${ws}) disconnected while this suite ran: the runner's Chromium died`);
  });
  const close = browser.close.bind(browser);
  browser.close = async (...args) => {
    // Playwright rejects a page's pending calls as soon as the connection
    // drops but emits "disconnected" one timer turn later, so a suite that
    // closes on that rejection would otherwise pass for its own close; after
    // that turn a dropped connection reads as not connected. (playwright-core
    // 1.63 client/connect.ts: on close it runs the pages' `_onClose()`, then
    // `setTimeout(() => browser._didClose(), 0)`; equal 0 ms timers run in
    // order, so this turn is an ordering, not a wait.)
    await delay(0);
    closing = browser.isConnected?.() ?? true;
    return close(...args);
  };
  return browser;
}

/**
 * One suite's browser and server. `openPage(options)` opens a fresh context
 * and page; `options` override the suite's `defaults` per call (`storage`
 * merged key by key, then `shape(options)` maps suite-specific options):
 * viewport (1280x800), locale (context, "en-US"), context (extra newContext
 * options), permissions (+ permissionOrigin), storage (localStorage entries,
 * null skipped), init (extra init scripts: fn or [fn, arg]), timeout
 * (setDefaultTimeout, 10_000), errors (pushed to `h.errors`: "pageerror",
 * "console", "5xx"; default the first two), setup(page, o) before navigation,
 * session/path (default `/?session=<session>`), ready (text to attach,
 * `(page, o, timeout)`, or null: no navigation), gotoTimeout/readyTimeout
 * (NAV_MS; `coldFirst`: COLD_MS for the first open after each start), after(page, o)
 * (a non-undefined answer replaces the page). `withPage(options, fn)` (either
 * order) closes the context whatever happens. A suite with several servers
 * declares `server(name, ...args)` → startServer options and
 * `started(started, name, ...args)`, and calls `startServer(name, ...args)`.
 */
export function createHarness(defaults = {}) {
  const interrupt = () => {
    process.exitCode = 1;
    h.server?.kill("SIGTERM");
    void h.browser?.close().catch(() => {});
  };
  const h = {
    browser: undefined,
    base: undefined,
    server: undefined,
    stop: async () => {},
    errors: [],
    cold: false,

    /** startNextServer with process.env (or `from`), no password, no telemetry, then `env`; `distDir` is cleared and passed. */
    async startServer(spec, ...args) {
      const { name, logFile, env = {}, distDir, from = process.env } = typeof spec === "string" ? await defaults.server(spec, ...args) : spec;
      if (distDir) rmSync(join(root, distDir), { force: true, recursive: true });
      const started = await startNextServer({ name, logFile, env: {
        ...from, PI_WEB_PASSWORD: "", NEXT_TELEMETRY_DISABLED: "1", ...(distDir ? { PI_WEB_DIST_DIR: distDir } : {}), ...env,
      } });
      h.base = started.base;
      h.server = started.server;
      h.stop = started.stop;
      h.cold = true;
      if (typeof spec === "string") await defaults.started?.(started, spec, ...args);
      return started;
    },

    async openPage(options = {}) {
      const merged = { ...defaults, ...options, storage: { ...defaults.storage, ...options.storage } };
      const o = defaults.shape ? defaults.shape(merged) : merged;
      const context = await h.browser.newContext({ viewport: o.viewport ?? { width: 1280, height: 800 }, locale: o.locale ?? "en-US", ...o.context });
      if (o.permissions) await context.grantPermissions(o.permissions, o.permissionOrigin ? { origin: h.base } : undefined);
      const entries = Object.fromEntries(Object.entries(o.storage).filter(([, value]) => value !== undefined && value !== null));
      if (Object.keys(entries).length) await seedStorage(context, entries);
      for (const script of o.init ?? []) {
        if (Array.isArray(script)) await context.addInitScript(script[0], script[1]);
        else await context.addInitScript(script);
      }
      const page = await context.newPage();
      page.setDefaultTimeout(o.timeout ?? 10_000);
      const errors = o.errors ?? ["pageerror", "console"];
      if (errors.includes("pageerror")) page.on("pageerror", (error) => h.errors.push(error.message));
      if (errors.includes("console")) page.on("console", (event) => { if (event.type() === "error") h.errors.push(event.text()); });
      if (errors.includes("5xx")) page.on("response", (r) => { if (r.url().startsWith(h.base) && r.status() >= 500) h.errors.push(`${r.status()} ${r.url()}`); });
      await o.setup?.(page, o);
      if (o.ready !== null) {
        const cold = o.coldFirst && h.cold;
        h.cold = false;
        const path = typeof o.path === "function" ? o.path(o) : o.path ?? `/?session=${o.session}`;
        const gotoTimeout = typeof o.gotoTimeout === "function" ? o.gotoTimeout(o) : o.gotoTimeout;
        await page.goto(`${h.base}${path}`, { waitUntil: "domcontentloaded", timeout: cold ? COLD_MS : gotoTimeout ?? NAV_MS });
        const readyTimeout = cold ? COLD_MS : o.readyTimeout ?? NAV_MS;
        if (typeof o.ready === "function") await o.ready(page, o, readyTimeout);
        else await page.getByText(o.ready).first().waitFor({ state: "attached", timeout: readyTimeout });
      }
      const result = await o.after?.(page, o);
      return result === undefined ? page : result;
    },

    /** `launchBrowser(launch, options)`, kept as `h.browser` (which `close()` closes) and returned. */
    async launchBrowser(launch, options) {
      h.browser = await launchBrowser(launch, options);
      return h.browser;
    },

    async withPage(a, b) {
      const [options, fn] = typeof a === "function" ? [b, a] : [a, b];
      const opened = await h.openPage(options);
      try {
        return await fn(opened);
      } finally {
        const context = typeof opened.context === "function" ? opened.context() : opened.context;
        await context.close();
      }
    },

    /** On SIGINT/SIGTERM: fail the run, SIGTERM the server, close the browser (`close()` removes it). */
    trapInterrupt() {
      process.once("SIGINT", interrupt);
      process.once("SIGTERM", interrupt);
    },

    /** Closes the browser (if any), stops the server, removes the interrupt handlers. */
    async close() {
      try { await h.browser?.close(); } catch { /* already closed */ }
      await h.stop();
      process.off("SIGINT", interrupt);
      process.off("SIGTERM", interrupt);
    },
  };
  h.openPage = h.openPage.bind(h);
  h.withPage = h.withPage.bind(h);
  h.startServer = h.startServer.bind(h);
  return h;
}
