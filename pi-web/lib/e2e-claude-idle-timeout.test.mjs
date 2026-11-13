// e2e/claude-composer-272.mjs started its server with
// PI_WEB_IDLE_TIMEOUT_MS=5000, below the page's 30 s lease renewal. A scenario
// that dwelled longer than 5 s (a loaded UI step) had its claude child reaped,
// the event stream reconnected after 1 s and the events route resumed the child
// with --resume; the suite's launch-count scenarios then read that resume as
// "the action restarted the conversation" (3 !== 1). The suite now uses the
// product default, so only an explicit kill ends a child mid-scenario.
import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const suite = readFileSync(new URL("../e2e/claude-composer-272.mjs", import.meta.url), "utf8");

test("claude-composer-272 keeps its child through a scenario's dwell", () => {
  const literal = suite.match(/PI_WEB_IDLE_TIMEOUT_MS:\s*"(\d+)"/)?.[1];
  assert.ok(literal, "e2e/claude-composer-272.mjs no longer sets PI_WEB_IDLE_TIMEOUT_MS explicitly");
  const idleMs = Number(literal);
  assert.ok(
    idleMs >= 600_000,
    `the suite's idle window is ${idleMs} ms, below the product default; a scenario dwell reaps and resumes its child`,
  );
});
