import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

import { trackBrowserErrors } from "../e2e/console-errors.mjs";

// The owner decision of 2026-11-19 (polyforge#156, "已定"): a console error
// is tolerated only when it is paired one-to-one with a
// requestfailed(errorText=net::ERR_NETWORK_CHANGED) event, at most 3 per
// flow, each logged `INFO tolerated network-change: <url>`. Every other
// error — an unpaired console error with the same text, "Failed to fetch",
// ERR_CONNECTION_REFUSED, pageerror, a 5xx, the tolerated pair past the
// cap — stays red. Chromium prints `Failed to load resource: net::<errorText>`
// for a failed resource (probed on the real browser: the requestfailed event
// arrives first and its errorText token is embedded verbatim), so the console
// text is matched by that token, never by the prefix wording, which #128 did
// not retain.

const NETWORK_CHANGE = "net::ERR_NETWORK_CHANGED";
const CONSOLE_TEXT = `Failed to load resource: ${NETWORK_CHANGE}`;
const BASE = "http://127.0.0.1:30141";

// A fake Playwright page: handlers registered by the collector, emitted by
// the test with the same event payloads the real page passes (a request has
// url()/failure(), a console event type()/text(), a response url()/status()).
function fakePage() {
  const listeners = new Map();
  return {
    on(event, handler) {
      if (!listeners.has(event)) listeners.set(event, []);
      listeners.get(event).push(handler);
    },
    emit(event, payload) {
      for (const handler of listeners.get(event) ?? []) handler(payload);
    },
  };
}

const requestFailed = (url, errorText = NETWORK_CHANGE) => ({
  url: () => url,
  failure: () => ({ errorText }),
});
const consoleError = (text) => ({ type: () => "error", text: () => text });
const response = (url, status) => ({ url: () => url, status: () => status });

function collector(page) {
  const lines = [];
  const errors = trackBrowserErrors(page, { base: BASE, log: (line) => lines.push(line) });
  return { errors, lines };
}

test("paired network-change console errors are tolerated and logged", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  const url = `${BASE}/api/sessions/e2e-long-session/state`;
  page.emit("requestfailed", requestFailed(url));
  page.emit("console", consoleError(CONSOLE_TEXT));
  assert.deepEqual(errors.settle(), []);
  assert.deepEqual(lines, [`INFO tolerated network-change: ${url}`]);
});

test("an unpaired network-change console error stays red", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  page.emit("console", consoleError(CONSOLE_TEXT));
  assert.deepEqual(errors.settle(), [CONSOLE_TEXT]);
  assert.deepEqual(lines, []);
});

test("a Failed to fetch console error stays red even beside a requestfailed pair", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  page.emit("requestfailed", requestFailed(`${BASE}/api/agent/e2e-x`));
  page.emit("console", consoleError("Failed to fetch"));
  assert.deepEqual(errors.settle(), ["Failed to fetch"]);
  assert.deepEqual(lines, []);
});

test("an ERR_CONNECTION_REFUSED failure stays red", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  page.emit("requestfailed", requestFailed(`${BASE}/api/agent/e2e-x`, "net::ERR_CONNECTION_REFUSED"));
  page.emit("console", consoleError("Failed to load resource: net::ERR_CONNECTION_REFUSED"));
  assert.deepEqual(errors.settle(), ["Failed to load resource: net::ERR_CONNECTION_REFUSED"]);
  assert.deepEqual(lines, []);
});

test("a pageerror is never tolerated, even with a requestfailed pair available", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  page.emit("requestfailed", requestFailed(`${BASE}/api/agent/e2e-x`));
  page.emit("pageerror", { message: `Uncaught TypeError: fetch failed ${NETWORK_CHANGE}` });
  assert.deepEqual(errors.settle(), [`Uncaught TypeError: fetch failed ${NETWORK_CHANGE}`]);
  assert.deepEqual(lines, []);
});

test("a 5xx response stays red and other responses and console types are not collected", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  page.emit("response", response(`${BASE}/api/sessions`, 500));
  page.emit("response", response(`${BASE}/api/sessions`, 200));
  page.emit("response", response("https://models.dev/x", 500));
  page.emit("console", consoleError("Failed to load resource: net::ERR_CONNECTION_REFUSED"));
  page.emit("console", { type: () => "warning", text: () => CONSOLE_TEXT });
  page.emit("console", { type: () => "log", text: () => CONSOLE_TEXT });
  assert.deepEqual(errors.settle(), [`500 ${BASE}/api/sessions`, "Failed to load resource: net::ERR_CONNECTION_REFUSED"]);
  assert.deepEqual(lines, []);
});

test("pairing is one-to-one: a second console error with no second requestfailed stays red", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  page.emit("requestfailed", requestFailed(`${BASE}/api/sessions/a`));
  page.emit("console", consoleError(CONSOLE_TEXT));
  page.emit("console", consoleError(CONSOLE_TEXT));
  assert.deepEqual(errors.settle(), [CONSOLE_TEXT]);
  assert.deepEqual(lines, [`INFO tolerated network-change: ${BASE}/api/sessions/a`]);
});

test("at most three network changes are tolerated per flow, the fourth pair stays red", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  for (const suffix of ["a", "b", "c", "d"]) {
    page.emit("requestfailed", requestFailed(`${BASE}/api/sessions/${suffix}`));
    page.emit("console", consoleError(CONSOLE_TEXT));
  }
  assert.deepEqual(errors.settle(), [CONSOLE_TEXT]);
  assert.deepEqual(lines, [
    `INFO tolerated network-change: ${BASE}/api/sessions/a`,
    `INFO tolerated network-change: ${BASE}/api/sessions/b`,
    `INFO tolerated network-change: ${BASE}/api/sessions/c`,
  ]);
});

test("each collector is one flow: a second page gets its own cap", () => {
  const first = fakePage();
  const second = fakePage();
  const firstCollector = collector(first);
  const secondCollector = collector(second);
  for (let i = 0; i < 3; i++) {
    first.emit("requestfailed", requestFailed(`${BASE}/api/sessions/first-${i}`));
    first.emit("console", consoleError(CONSOLE_TEXT));
  }
  for (let i = 0; i < 3; i++) {
    second.emit("requestfailed", requestFailed(`${BASE}/api/sessions/second-${i}`));
    second.emit("console", consoleError(CONSOLE_TEXT));
  }
  assert.deepEqual(firstCollector.errors.settle(), []);
  assert.deepEqual(secondCollector.errors.settle(), []);
  assert.equal(secondCollector.lines.length, 3);
});
