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
const FORBIDDEN_TEXT = "Failed to load resource: the server responded with a status of 403 (Forbidden)";

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
// A console event carries the resource's address in location().url
// (polyforge#180): Chromium's resource-load text has no URL in it. An empty
// url keeps the raw text with an explicit `[url unavailable]` marker —
// nothing is guessed (owner decision Q2; review r1 #1).
const consoleError = (text, url = "") => ({ type: () => "error", text: () => text, location: () => ({ url }) });
const response = (url, status) => ({ url: () => url, status: () => status });

function collector(page, options = {}) {
  const lines = [];
  const errors = trackBrowserErrors(page, { base: BASE, log: (line) => lines.push(line), ...options });
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
  assert.deepEqual(errors.settle(), [`${CONSOLE_TEXT} [url unavailable]`]);
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
  assert.deepEqual(errors.settle(), ["Failed to load resource: net::ERR_CONNECTION_REFUSED [url unavailable]"]);
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
  assert.deepEqual(errors.settle(), [`500 ${BASE}/api/sessions`, "Failed to load resource: net::ERR_CONNECTION_REFUSED [url unavailable]"]);
  assert.deepEqual(lines, []);
});

test("pairing is one-to-one: a second console error with no second requestfailed stays red", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  page.emit("requestfailed", requestFailed(`${BASE}/api/sessions/a`));
  page.emit("console", consoleError(CONSOLE_TEXT));
  page.emit("console", consoleError(CONSOLE_TEXT));
  assert.deepEqual(errors.settle(), [`${CONSOLE_TEXT} [url unavailable]`]);
  assert.deepEqual(lines, [`INFO tolerated network-change: ${BASE}/api/sessions/a`]);
});

test("at most three network changes are tolerated per flow, the fourth pair stays red", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  for (const suffix of ["a", "b", "c", "d"]) {
    page.emit("requestfailed", requestFailed(`${BASE}/api/sessions/${suffix}`));
    page.emit("console", consoleError(CONSOLE_TEXT));
  }
  assert.deepEqual(errors.settle(), [`${CONSOLE_TEXT} [url unavailable]`]);
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

// polyforge#180: a resource-load console error ("Failed to load resource: …")
// carries no resource address in its text — #179's full-8 red was a bare 403
// nobody could attribute. The console event's location().url names the
// resource; the red text appends it in brackets after Chromium's text, which
// already carries the status for HTTP failures. Without a location URL the
// raw text gets an explicit `[url unavailable]` marker — the owner decision
// Q2 “保留原文并标明不可用”: a reader can tell the collector could not get the
// URL apart from an older collector that never tried. The pairing token check
// reads only the raw console text, so a token that appears merely in the
// appended URL never pairs (owner decision Q2).
test("a 4xx resource-load console error's red text carries the resource url", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  const url = `${BASE}/static/avatar.png`;
  page.emit("console", consoleError(FORBIDDEN_TEXT, url));
  assert.deepEqual(errors.settle(), [`${FORBIDDEN_TEXT} [${url}]`]);
  assert.deepEqual(lines, []);
});

test("a resource-load console error without a location url keeps its text and marks the url unavailable", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  page.emit("console", consoleError(FORBIDDEN_TEXT, ""));
  assert.deepEqual(errors.settle(), [`${FORBIDDEN_TEXT} [url unavailable]`]);
  assert.deepEqual(lines, []);
});

test("a 4xx response alone collects nothing", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  page.emit("response", response(`${BASE}/api/forbidden`, 403));
  page.emit("response", response(`${BASE}/api/missing`, 404));
  assert.deepEqual(errors.settle(), []);
  assert.deepEqual(lines, []);
});

test("a network-change token that appears only in the appended url does not pair", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  const text = "Failed to load resource: net::ERR_CONNECTION_REFUSED";
  const url = `${BASE}/static/net::ERR_NETWORK_CHANGED.png`;
  page.emit("requestfailed", requestFailed(`${BASE}/api/agent/e2e-x`));
  page.emit("console", consoleError(text, url));
  assert.deepEqual(errors.settle(), [`${text} [${url}]`]);
  assert.deepEqual(lines, []);
});

test("a console error emitted before its requestfailed still pairs", () => {
  const page = fakePage();
  const { errors, lines } = collector(page);
  const url = `${BASE}/api/sessions/order`;
  page.emit("console", consoleError(CONSOLE_TEXT, `${BASE}/static/app.png`));
  page.emit("requestfailed", requestFailed(url));
  assert.deepEqual(errors.settle(), []);
  assert.deepEqual(lines, [`INFO tolerated network-change: ${url}`]);
});

test("a custom maxToleratedNetworkChanges cap holds", () => {
  const page = fakePage();
  const { errors, lines } = collector(page, { maxToleratedNetworkChanges: 1 });
  for (const suffix of ["a", "b"]) {
    page.emit("requestfailed", requestFailed(`${BASE}/api/sessions/${suffix}`));
    page.emit("console", consoleError(CONSOLE_TEXT));
  }
  assert.deepEqual(errors.settle(), [`${CONSOLE_TEXT} [url unavailable]`]);
  assert.deepEqual(lines, [`INFO tolerated network-change: ${BASE}/api/sessions/a`]);
});
