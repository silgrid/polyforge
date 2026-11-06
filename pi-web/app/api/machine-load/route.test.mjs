import "#test-env";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import os from "node:os";
import { mock, test } from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const route = await jiti.import("./route.ts");

const METRIC_KEYS = ["coreCount", "cpuPercent", "loadavg", "memoryTotalBytes", "memoryUsedBytes"];

function get(headers = { host: "localhost" }) {
  return route.GET(new Request("http://localhost/api/machine-load", { headers }));
}

test("an allowed request answers 200 with exactly the five-field metrics whitelist", async () => {
  const response = get();
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.deepEqual(Object.keys(body), ["metrics"]);
  assert.deepEqual(Object.keys(body.metrics).sort(), METRIC_KEYS);
  const { cpuPercent, coreCount, loadavg, memoryUsedBytes, memoryTotalBytes } = body.metrics;
  assert.ok(cpuPercent === null || (cpuPercent >= 0 && cpuPercent <= 100));
  // The core count is the cores this process may run on, as nproc counts them.
  // OMP_NUM_THREADS / OMP_THREAD_LIMIT shadow nproc's count (GNU nproc honours
  // them), so the oracle reads it with both removed from a copied env.
  const env = { ...process.env };
  delete env.OMP_NUM_THREADS;
  delete env.OMP_THREAD_LIMIT;
  assert.equal(coreCount, process.platform === "linux" ? Number(execFileSync("nproc", { encoding: "utf8", env }).trim()) : os.cpus().length);
  assert.ok(loadavg === null || (loadavg.length === 3 && loadavg.every(Number.isFinite)));
  assert.ok(Number.isInteger(memoryUsedBytes) && memoryUsedBytes >= 0 && memoryUsedBytes <= memoryTotalBytes);
  assert.equal(memoryTotalBytes, os.totalmem());
});

test("a request the /api gate refuses answers 403 without metrics", async () => {
  const response = get({ host: "localhost", origin: "https://evil.example", "sec-fetch-site": "cross-site" });
  assert.equal(response.status, 403);
  const body = await response.json();
  assert.deepEqual(body, { error: "Forbidden" });
});

test("GET only and never cached", () => {
  assert.deepEqual(Object.keys(route).filter((key) => /^[A-Z]+$/.test(key)), ["GET"]);
  assert.equal(route.dynamic, "force-dynamic");
});

// the failure path is proven by making sampling fail, not by
// reading the source. Each case loads a fresh module instance (the shared
// jiti's moduleCache is off), so no earlier test's 2 s shared sample can mask
// the failure.
test("a failing host read degrades to 200 { metrics: null } and caches nothing", async (t) => {
  const cpus = t.mock.method(os, "cpus", () => { throw new Error("E2E-SAMPLER-FAILURE"); });
  const fresh = await jiti.import("./route.ts");
  const get = () => fresh.GET(new Request("http://localhost/api/machine-load", { headers: { host: "localhost" } }));
  const failed = get();
  assert.equal(failed.status, 200, "no 5xx");
  assert.deepEqual(await failed.json(), { metrics: null }, "exactly { metrics: null }, no error detail");
  cpus.mock.restore();
  const recovered = await get().json();
  assert.ok(recovered.metrics && Number.isInteger(recovered.metrics.coreCount), "the failed pass cached nothing: the next request samples afresh");
});

test("a sampler that throws degrades to 200 { metrics: null }", async () => {
  // The route's own guard: even a throwing sampler never produces a 5xx. One
  // cached jiti instance, so the route and this test hold the same sampler.
  const shared = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: true });
  const fresh = await shared.import("./route.ts");
  const { machineLoadSampler } = await shared.import("@/lib/machine-load");
  const sample = mock.method(machineLoadSampler, "sample", () => { throw new Error("E2E-SAMPLE-THROW"); });
  try {
    const response = fresh.GET(new Request("http://localhost/api/machine-load", { headers: { host: "localhost" } }));
    assert.equal(sample.mock.callCount(), 1, "the route asked the stubbed sampler");
    assert.equal(response.status, 200, "no 5xx, the answer stays 200");
    assert.deepEqual(await response.json(), { metrics: null }, "exactly { metrics: null }, no error detail");
  } finally {
    sample.mock.restore();
  }
});
