import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, moduleCache: false });
const view = await jiti.import("./machine-load-view.ts");
const source = await readFile(new URL("./machine-load-view.ts", import.meta.url), "utf8");

const PAYLOAD = { cpuPercent: 12.3, coreCount: 8, loadavg: [0.42, 0.51, 0.63], memoryUsedBytes: 5583457485, memoryTotalBytes: 17179869184 };
const LABELS = { cpu: "CPU", cores: "Cores", loadavg: "Load", memory: "Memory", unavailable: "不可用" };

test("the pinned formats render a known payload exactly", () => {
  assert.deepEqual(view.machineLoadRows(PAYLOAD, LABELS), [
    { key: "cpu", label: "CPU", value: "12.3%" },
    { key: "cores", label: "Cores", value: "8" },
    { key: "loadavg", label: "Load", value: "0.42 / 0.51 / 0.63" },
    { key: "memory", label: "Memory", value: "5.2 / 16.0 GiB" },
  ]);
  assert.equal(view.formatCpuPercent(0), "0.0%");
  assert.equal(view.formatCpuPercent(100), "100.0%");
  assert.equal(view.formatLoadavg([0, 0, 0]), "0.00 / 0.00 / 0.00", "Linux zeros are real values");
  assert.doesNotMatch(source, /toLocaleString|Intl\./, "numbers stay locale-invariant");
});

test("a null CPU or loadavg renders the em dash, never a fabricated zero", () => {
  const rows = view.machineLoadRows({ ...PAYLOAD, cpuPercent: null, loadavg: null }, LABELS);
  assert.equal(rows[0].value, "—");
  assert.equal(rows[2].value, "—");
  assert.equal(rows[1].value, "8");
  assert.equal(rows[3].value, "5.2 / 16.0 GiB");
});

test("no good value renders the unavailable placeholder in every row", () => {
  const rows = view.machineLoadRows(null, LABELS);
  assert.equal(rows.length, 4);
  assert.deepEqual(rows.map((row) => row.value), ["不可用", "不可用", "不可用", "不可用"]);
});

test("a valid body replaces the state with a whitelist copy", () => {
  const next = view.nextMachineLoadMetrics(null, { metrics: { ...PAYLOAD, hostname: "leak", extra: 1 }, debug: "x" });
  assert.deepEqual(next, PAYLOAD);
  assert.deepEqual(Object.keys(next).sort(), ["coreCount", "cpuPercent", "loadavg", "memoryTotalBytes", "memoryUsedBytes"]);
  assert.equal(view.nextMachineLoadMetrics(PAYLOAD, { metrics: { ...PAYLOAD, cpuPercent: null } }).cpuPercent, null,
    "a null CPU is a valid sample");
});

test("failures and malformed bodies keep the previous state", () => {
  const previous = { ...PAYLOAD };
  for (const body of [
    null,
    undefined,
    "not json",
    {},
    { metrics: null },
    { metrics: { ...PAYLOAD, cpuPercent: 101 } },
    { metrics: { ...PAYLOAD, cpuPercent: "12" } },
    { metrics: { ...PAYLOAD, coreCount: 0 } },
    { metrics: { ...PAYLOAD, loadavg: [1, 2] } },
    { metrics: { ...PAYLOAD, loadavg: [1, 2, Number.NaN] } },
    { metrics: { ...PAYLOAD, memoryUsedBytes: PAYLOAD.memoryTotalBytes + 1 } },
    { metrics: { ...PAYLOAD, memoryTotalBytes: undefined } },
  ]) {
    assert.equal(view.nextMachineLoadMetrics(previous, body), previous, JSON.stringify(body));
  }
  assert.equal(view.nextMachineLoadMetrics(null, { metrics: null }), null, "no previous state stays unavailable");
});

test("the view module stays client-safe", () => {
  assert.doesNotMatch(source, /from "node:|require\(/);
});
