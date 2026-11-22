// Unit tests for the oracle runner's hot measurement (polyforge#140 r5 u13,
// review r4#1): the sample bound must be reachable when every hot call is
// FASTER than the probe interval — a fixed call cap made a compliant server
// fail AC1, faster calls collecting fewer samples than the bound requires.
// The probe target is a local throwaway HTTP server; the measured "calls" are
// fake ones of a fixed short duration, so no next build or real corpus is
// needed and the expected values come from the interval arithmetic alone:
// a call of D ms, issued back to back, accumulates roughly D/intervalMs
// in-window probes, whatever D is.
import "#test-env";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";

const { measureHotCalls } = await import("./session-list-verify-run.mjs");

test("fast hot calls still reach the sample bound", async (t) => {
  // The probe target: any GET answers 200 quickly.
  const probeServer = createServer((request, response) => {
    response.writeHead(200, { "Content-Type": "application/octet-stream" });
    response.end("ok");
  });
  await new Promise((resolveListen) => probeServer.listen(0, "127.0.0.1", resolveListen));
  t.after(() => new Promise((resolveClose) => probeServer.close(resolveClose)));
  const base = `http://127.0.0.1:${probeServer.address().port}`;

  // The ticket's shape: probes every 100 ms, 100 samples required — and every
  // measured call takes 20 ms, half a fast hot call budget.
  const details = {
    mode: "normal",
    staticPath: "/probe",
    intervalMs: 100,
    minSamples: 100,
    hotIterations: 200,
    hotDurationBudgetMs: 120_000,
  };
  const fakeServer = {
    base,
    call: async () => {
      await delay(20);
      return { status: 200, ms: 20 };
    },
  };

  const measured = await measureHotCalls(fakeServer, details);
  assert.ok(measured.calls.length > 100,
    `the calls run back to back until the bound, never a fixed count (${measured.calls.length} calls)`);
  assert.ok(measured.samples.length >= details.minSamples,
    `fast calls still collect the sample bound: ${measured.samples.length} of ${details.minSamples} (a fixed 200-call cap yields ~${Math.floor((200 * 20) / 100)})`);
});
