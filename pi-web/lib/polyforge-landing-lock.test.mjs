// carries: lib/polyforge-landing-lock.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-landing-lock.ts");

const RAW = {
  state: "waiting", observed_at: "2026-11-25T00:00:00Z", as_of: "2026-11-25T00:00:01Z", age_ms: 1000,
  keys: [{ repo: "github.com/o/app", branch: "epic/245", position: 3, since: "2026-11-25T00:00:00Z" }],
  holder: "HOLDER_SENTINEL", token: "TOKEN_SENTINEL", machine_id: "MACHINE_SENTINEL", request_id: "REQ_SENTINEL",
};

test("landing lock passport: only the whitelisted envelope and keys", () => {
  const lock = lib.sanitizeLandingLock(RAW);
  assert.deepEqual(lock, {
    state: "waiting", observedAt: "2026-11-25T00:00:00Z", asOf: "2026-11-25T00:00:01Z", ageMs: 1000,
    keys: [{ repo: "github.com/o/app", branch: "epic/245", position: 3, since: "2026-11-25T00:00:00Z" }],
  });
  assert.equal(lib.landingLockPositions(lock), "#3");
  assert.equal(lib.landingLockKeysText(lock), "github.com/o/app (epic/245)");
  const serialized = JSON.stringify(lock);
  for (const dropped of ["HOLDER_SENTINEL", "TOKEN_SENTINEL", "MACHINE_SENTINEL", "REQ_SENTINEL", "holder", "token"]) {
    assert.ok(!serialized.includes(dropped), `the passport must not carry ${dropped}`);
  }
});

test("positions, states and malformed values degrade without inventing #0", () => {
  assert.equal(lib.sanitizeLandingLock({ ...RAW, state: "unknown" }), undefined);
  assert.equal(lib.sanitizeLandingLock({ ...RAW, keys: [] }), undefined, "waiting with no valid key");
  for (const bad of [0, -1, 1.5, "3", null]) {
    assert.equal(lib.sanitizeLandingLock({ ...RAW, keys: [{ repo: "r", branch: "b", position: bad }] }), undefined, `position ${bad}`);
  }
  const two = lib.sanitizeLandingLock({ ...RAW, keys: [
    { repo: "a", branch: "epic/245", position: 1 },
    { repo: "b", branch: "epic/245", position: 4 },
    { repo: "", branch: "x", position: 2 },
  ] });
  assert.equal(lib.landingLockPositions(two), "#1,#4", "invalid keys are dropped, order kept");
  const stale = lib.sanitizeLandingLock({ ...RAW, state: "stale", age_ms: 20000 });
  assert.equal(stale.state, "stale");
  assert.equal(lib.sanitizeLandingLock({ ...RAW, state: "unavailable", keys: [] }).state, "unavailable");
  const t = (key, values) => `${key}:${values?.positions ?? ""}`;
  assert.equal(lib.landingLockText(stale, t), "polyforge.landingLock.stale:#3");
  assert.equal(lib.landingLockText(lib.sanitizeLandingLock({ state: "unavailable", keys: [] }), t), "polyforge.landingLock.unavailable:");
});
