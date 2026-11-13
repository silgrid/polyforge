import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, moduleCache: false });
const { countCpuList, createMachineLoadSampler, MACHINE_LOAD_BASELINE_MAX_AGE_MS, MACHINE_LOAD_WINDOW_MS } = await jiti.import("./machine-load.ts");
const source = await readFile(new URL("./machine-load.ts", import.meta.url), "utf8");

const GiB = 1024 ** 3;

/** A mocked `os` whose CPU times advance by `step` per pass and that counts every call. */
function mockOs({ cores = 4, busyPerPass = 30, idlePerPass = 70, loadavg = [0.5, 0.25, 0.125], total = 16 * GiB, free = 6 * GiB } = {}) {
  const calls = { cpus: 0, loadavg: 0, totalmem: 0, freemem: 0 };
  let pass = 0;
  return {
    calls,
    os: {
      cpus() {
        calls.cpus += 1;
        pass += 1;
        return Array.from({ length: cores }, () => ({
          model: "mock",
          speed: 1,
          times: { user: 1000 + busyPerPass * pass, nice: 0, sys: 500, idle: 5000 + idlePerPass * pass, irq: 0 },
        }));
      },
      loadavg() { calls.loadavg += 1; return loadavg; },
      totalmem() { calls.totalmem += 1; return total; },
      freemem() { calls.freemem += 1; return free; },
    },
  };
}

function clock(start = 1_000_000) {
  const state = { now: start };
  return { state, now: () => state.now };
}

test("the window is the fixed 2 s", () => {
  assert.equal(MACHINE_LOAD_WINDOW_MS, 2000);
});

test("the first sample has no CPU baseline but still reports cores, loadavg and memory", () => {
  const mock = mockOs();
  const { now } = clock();
  const sampler = createMachineLoadSampler({ os: mock.os, now, platform: "linux", readProcStatus: () => "Cpus_allowed_list:\t0-3\n" });
  assert.deepEqual(sampler.sample(), {
    cpuPercent: null,
    coreCount: 4,
    loadavg: [0.5, 0.25, 0.125],
    memoryUsedBytes: 10 * GiB,
    memoryTotalBytes: 16 * GiB,
  });
});

test("coreCount counts Linux Cpus_allowed_list like nproc, else os.cpus().length", () => {
  const status = (list) => () => `Name:\tnode\nCpus_allowed:\tff\nCpus_allowed_list:\t${list}\nMems_allowed_list:\t0\n`;
  const cores = (opts) => createMachineLoadSampler({ os: mockOs({ cores: 8 }).os, now: clock().now, ...opts }).sample().coreCount;
  assert.equal(cores({ platform: "linux", readProcStatus: status("0-1,4") }), 3, "a cpuset-restricted host reports the cores nproc reports, not every host CPU");
  assert.equal(cores({ platform: "linux", readProcStatus: () => { throw new Error("no /proc"); } }), 8, "an unreadable /proc falls back to os.cpus().length");
  assert.equal(cores({ platform: "linux", readProcStatus: status("garbage") }), 8, "an unparsable list falls back to os.cpus().length");
  assert.equal(cores({ platform: "darwin", readProcStatus: status("0") }), 8, "other platforms use os.cpus().length");
  assert.deepEqual(["0", "0-31", "0-3,8,10-11", "3-1", "", "a"].map(countCpuList), [1, 32, 7, null, null, null]);
});

test("calls inside one window share one sample and one os pass", () => {
  const mock = mockOs();
  const time = clock();
  const sampler = createMachineLoadSampler({ os: mock.os, now: time.now, platform: "linux" });
  const first = sampler.sample();
  time.state.now += 1999;
  const second = sampler.sample();
  assert.equal(second, first, "both callers receive the identical sample");
  assert.equal(mock.calls.cpus, 1);
  assert.equal(mock.calls.totalmem, 1);
});

test("a call at T + 2 s takes a fresh sample whose CPU is the delta since the previous one", () => {
  const mock = mockOs({ busyPerPass: 30, idlePerPass: 70 });
  const time = clock();
  const sampler = createMachineLoadSampler({ os: mock.os, now: time.now, platform: "linux" });
  sampler.sample();
  time.state.now += 2000;
  const next = sampler.sample();
  assert.equal(mock.calls.cpus, 2, "the throttle boundary samples again");
  // 30 busy + 70 idle ticks per core since the previous pass: 30 %, not the
  // since-boot share (which would be ~ (1060+500)/(1060+500+5140) ≈ 23 %).
  assert.equal(next.cpuPercent, 30);
});

test("a CPU baseline older than 10 s is dropped: null, then a fresh delta", () => {
  assert.equal(MACHINE_LOAD_BASELINE_MAX_AGE_MS, 10000);
  const mock = mockOs({ busyPerPass: 30, idlePerPass: 70 });
  const time = clock();
  const sampler = createMachineLoadSampler({ os: mock.os, now: time.now, platform: "linux", readProcStatus: () => "Cpus_allowed_list:\t0-3\n" });
  sampler.sample();
  time.state.now += 10_000;
  assert.equal(sampler.sample().cpuPercent, 30, "a baseline exactly at the max age still yields the delta");
  time.state.now += 10_001;
  const stale = sampler.sample();
  assert.equal(stale.cpuPercent, null, "a sample after a > 10 s gap never averages the gap");
  assert.equal(stale.coreCount, 4, "the other fields stay real");
  time.state.now += 2000;
  assert.equal(sampler.sample().cpuPercent, 30, "the next 2 s sample after the rebaseline reports a delta");
  assert.equal(mock.calls.cpus, 4);
});

test("no sample() call means no os call (no timer, no background sampling)", () => {
  const mock = mockOs();
  const time = clock();
  createMachineLoadSampler({ os: mock.os, now: time.now, platform: "linux" });
  time.state.now += 60_000;
  assert.deepEqual(mock.calls, { cpus: 0, loadavg: 0, totalmem: 0, freemem: 0 });
  assert.doesNotMatch(source, /setInterval|setTimeout|setImmediate/, "the sampler owns no timer");
});

test("CPU is clamped to 0..100 and a zero delta yields no value", () => {
  const time = clock();
  let reading = 0;
  const readings = [
    { user: 100, nice: 0, sys: 0, idle: 100, irq: 0 },
    { user: 50, nice: 0, sys: 0, idle: 250, irq: 0 }, // busy went backwards: clamp at 0
    { user: 50, nice: 0, sys: 0, idle: 250, irq: 0 }, // no elapsed ticks: null
  ];
  const os = {
    cpus: () => [{ model: "m", speed: 1, times: readings[reading++] }],
    loadavg: () => [0, 0, 0],
    totalmem: () => 100,
    freemem: () => 40,
  };
  const sampler = createMachineLoadSampler({ os, now: time.now, platform: "linux" });
  assert.equal(sampler.sample().cpuPercent, null);
  time.state.now += 2000;
  assert.equal(sampler.sample().cpuPercent, 0);
  time.state.now += 2000;
  assert.equal(sampler.sample().cpuPercent, null);
});

test("Linux zeros stay real values; Windows and invalid arrays have no loadavg", () => {
  const zeros = createMachineLoadSampler({ os: mockOs({ loadavg: [0, 0, 0] }).os, now: clock().now, platform: "linux" });
  assert.deepEqual(zeros.sample().loadavg, [0, 0, 0]);
  const windows = createMachineLoadSampler({ os: mockOs({ loadavg: [0, 0, 0] }).os, now: clock().now, platform: "win32" });
  assert.equal(windows.sample().loadavg, null);
  const invalid = createMachineLoadSampler({ os: mockOs({ loadavg: [1, Number.NaN, 2] }).os, now: clock().now, platform: "darwin" });
  assert.equal(invalid.sample().loadavg, null);
});

test("memory used is total − free, never negative and never above total", () => {
  const over = createMachineLoadSampler({ os: mockOs({ total: 100, free: 140 }).os, now: clock().now, platform: "linux" });
  assert.equal(over.sample().memoryUsedBytes, 0);
  const negativeFree = createMachineLoadSampler({ os: mockOs({ total: 100, free: -5 }).os, now: clock().now, platform: "linux" });
  assert.equal(negativeFree.sample().memoryUsedBytes, 100);
});

test("a failed pass returns null without throwing and is not cached", () => {
  const time = clock();
  let fail = true;
  const healthy = mockOs();
  const os = {
    ...healthy.os,
    cpus() {
      if (fail) throw new Error("mock os failure");
      return healthy.os.cpus();
    },
  };
  const sampler = createMachineLoadSampler({ os, now: time.now, platform: "linux" });
  assert.equal(sampler.sample(), null);
  fail = false;
  const recovered = sampler.sample();
  assert.ok(recovered, "the next call inside the same window samples again");
  assert.equal(recovered.cpuPercent, null, "a failed pass leaves no baseline behind");

  const partial = createMachineLoadSampler({
    os: { ...mockOs().os, totalmem() { throw new Error("partial failure"); } },
    now: clock().now,
    platform: "linux",
  });
  assert.equal(partial.sample(), null);
  assert.equal(createMachineLoadSampler({ os: { ...mockOs().os, cpus: () => [] }, now: clock().now }).sample(), null);
});
