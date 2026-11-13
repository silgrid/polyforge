// the e2e page-load ceilings live in e2e/timeouts.mjs and can be
// overridden through E2E_COLD_MS / E2E_NAV_MS. A bad override must be refused,
// never replaced by a smaller ceiling. adds the action ceiling
// ACTION_MS (E2E_ACTION_MS) on the same terms.
import "#test-env";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

const moduleUrl = new URL("../e2e/timeouts.mjs", import.meta.url);
const { ACTION_MS, COLD_MS, NAV_MS, msFromEnv } = await import(moduleUrl);

function withEnv(name, value, fn) {
  const saved = process.env[name];
  if (value === undefined) delete process.env[name];
  else process.env[name] = value;
  try {
    return fn();
  } finally {
    if (saved === undefined) delete process.env[name];
    else process.env[name] = saved;
  }
}

test("defaults raise the old 240 s / 90 s / 60 s page-load ceilings", { skip: process.env.E2E_COLD_MS || process.env.E2E_NAV_MS ? "override set in the environment" : false }, () => {
  assert.equal(COLD_MS, 480_000);
  assert.equal(NAV_MS, 180_000);
});

test("the action ceiling defaults to 30 s, above the 8 s / 10 s that timed out under load", { skip: process.env.E2E_ACTION_MS ? "override set in the environment" : false }, () => {
  assert.equal(ACTION_MS, 30_000);
});

test("blank or unset keeps the default", () => {
  assert.equal(withEnv("E2E_TIMEOUTS_PROBE", undefined, () => msFromEnv("E2E_TIMEOUTS_PROBE", 7)), 7);
  assert.equal(withEnv("E2E_TIMEOUTS_PROBE", "", () => msFromEnv("E2E_TIMEOUTS_PROBE", 7)), 7);
  assert.equal(withEnv("E2E_TIMEOUTS_PROBE", "  ", () => msFromEnv("E2E_TIMEOUTS_PROBE", 7)), 7);
});

test("a positive number overrides the default", () => {
  assert.equal(withEnv("E2E_TIMEOUTS_PROBE", "333000", () => msFromEnv("E2E_TIMEOUTS_PROBE", 7)), 333_000);
});

test("zero, negative, non-numeric and infinite values are refused with the variable named", () => {
  for (const bad of ["0", "-1", "abc", "Infinity"]) {
    assert.throws(
      () => withEnv("E2E_TIMEOUTS_PROBE", bad, () => msFromEnv("E2E_TIMEOUTS_PROBE", 7)),
      (err) => err instanceof Error && err.message.includes("E2E_TIMEOUTS_PROBE") && err.message.includes(JSON.stringify(bad)),
      `value ${JSON.stringify(bad)} must be refused`,
    );
  }
});

const modulePath = fileURLToPath(moduleUrl);
const probe = `import(${JSON.stringify(modulePath)}).then((m) => console.log(JSON.stringify({ cold: m.COLD_MS, nav: m.NAV_MS, action: m.ACTION_MS })))`;

function runProbe(env) {
  const childEnv = { ...process.env };
  delete childEnv.E2E_COLD_MS;
  delete childEnv.E2E_NAV_MS;
  delete childEnv.E2E_ACTION_MS;
  return spawnSync(process.execPath, ["--input-type=module", "-e", probe], { env: { ...childEnv, ...env }, encoding: "utf8", timeout: 30_000 });
}

test("the real environment reaches the exported constants", () => {
  const run = runProbe({ E2E_NAV_MS: "123456", E2E_COLD_MS: "654321", E2E_ACTION_MS: "45000" });
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(JSON.parse(run.stdout), { cold: 654_321, nav: 123_456, action: 45_000 });
});

test("unset variables give every default to the suites", () => {
  const run = runProbe({});
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(JSON.parse(run.stdout), { cold: 480_000, nav: 180_000, action: 30_000 });
});

test("an invalid environment value stops the module from loading", () => {
  const run = runProbe({ E2E_NAV_MS: "0" });
  assert.notEqual(run.status, 0);
  assert.match(run.stderr, /E2E_NAV_MS must be a positive number of milliseconds, got "0"/);
});

test("an invalid action ceiling stops the module from loading", () => {
  const run = runProbe({ E2E_ACTION_MS: "abc" });
  assert.notEqual(run.status, 0);
  assert.match(run.stderr, /E2E_ACTION_MS must be a positive number of milliseconds, got "abc"/);
});
