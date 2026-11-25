// carries: lib/step-tier-write.ts, lib/step-tiers.ts
// polyforge#258 r3 #2: the drawer's per-harness tier plan is run bounded. A
// catalog with more keys than the write limit must never be reported as
// applied: the runner stops at the limit and leaves the remaining catalog for a
// retry. Expected catalogs are the fixture's own hand-built tiers, not the
// component's output.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const m = await jiti.import("./step-tier-write.ts");

function harness(initialTiers, { failWrite = 0, failRead = false } = {}) {
  const tiers = new Map(Object.entries(initialTiers).map(([tier, candidates]) => [tier, [...candidates]]));
  const writes = [];
  let hash = 0;
  const read = async () => {
    if (failRead) return { ok: false, error: "fixture read failed" };
    return { ok: true, items: [...tiers].map(([tier, candidates]) => ({ key: `step_tiers.${tier}`, value: candidates })), hash: String(++hash) };
  };
  const write = async (plan, planHash) => {
    writes.push({ plan, planHash });
    if (writes.length <= failWrite) return { ok: false, error: "fixture write refused" };
    if (plan.action === "set") tiers.set(plan.tier, [...plan.candidates]);
    else tiers.delete(plan.tier);
    return { ok: true };
  };
  return { tiers, writes, read, write };
}

const TIER = (suffix) => ({ [`tier-${suffix}`]: ["pi:a/one"] });

test("a plan larger than the write limit stops without claiming success", async () => {
  const initial = {};
  for (let i = 0; i < 33; i++) initial[`tier-${i}`] = ["pi:a/one"];
  const h = harness(initial);
  const result = await m.runStepTierWritePlan(h.read, h.write, { harness: "pi", model: "a/one" }, null);
  assert.deepEqual(result, { ok: false, reason: "limit" });
  assert.equal(h.writes.length, 32, "the runner stops at the documented limit");
  assert.equal(h.tiers.size, 1, "the un-run key stays in the catalog, so a retry can finish it");
  assert.deepEqual([...h.tiers.values()][0], ["pi:a/one"]);
});

test("a plan that empties the catalog in time reports success", async () => {
  const h = harness({ one: ["pi:a/one"], two: ["pi:a/one"] });
  const result = await m.runStepTierWritePlan(h.read, h.write, { harness: "pi", model: "a/one" }, null);
  assert.deepEqual(result, { ok: true });
  assert.equal(h.writes.length, 2);
  assert.equal(h.tiers.size, 0);
});

test("a refused write and an unreadable catalog stop the run with their own reason", async () => {
  const refused = harness({ one: ["pi:a/one"] }, { failWrite: 1 });
  assert.deepEqual(await m.runStepTierWritePlan(refused.read, refused.write, { harness: "pi", model: "a/one" }, null),
    { ok: false, reason: "write", error: "fixture write refused" });
  assert.equal(refused.writes.length, 1);

  const unreadable = harness({ one: ["pi:a/one"] }, { failRead: true });
  assert.deepEqual(await m.runStepTierWritePlan(unreadable.read, unreadable.write, { harness: "pi", model: "a/one" }, null),
    { ok: false, reason: "read", error: "fixture read failed" });
  assert.equal(unreadable.writes.length, 0);
});

test("a caller's smaller limit is the bound it obeys", async () => {
  const initial = {};
  for (let i = 0; i < 6; i++) initial[`tier-${i}`] = ["pi:a/one"];
  const h = harness(initial);
  const result = await m.runStepTierWritePlan(h.read, h.write, { harness: "pi", model: "a/one" }, null, 5);
  assert.deepEqual(result, { ok: false, reason: "limit" });
  assert.equal(h.writes.length, 5);
  assert.equal(h.tiers.size, 1);
});

test("a plan that finishes on its last allowed write reports success", async () => {
  const initial = {};
  for (let i = 0; i < 32; i++) initial[`tier-${i}`] = ["pi:a/one"];
  const h = harness(initial);
  const result = await m.runStepTierWritePlan(h.read, h.write, { harness: "pi", model: "a/one" }, null);
  assert.deepEqual(result, { ok: true }, "the final read after the 32nd write finds an empty plan");
  assert.equal(h.writes.length, 32, "the limit bounds writes, not the confirming read");
  assert.equal(h.tiers.size, 0);
});

test("a one-write plan succeeds with a limit of one", async () => {
  const h = harness({ only: ["pi:a/one"] });
  const result = await m.runStepTierWritePlan(h.read, h.write, { harness: "pi", model: "a/one" }, null, 1);
  assert.deepEqual(result, { ok: true });
  assert.equal(h.writes.length, 1);
  assert.equal(h.tiers.size, 0);
});

test("a plan exactly filling a smaller limit also succeeds", async () => {
  const initial = {};
  for (let i = 0; i < 5; i++) initial[`tier-${i}`] = ["pi:a/one"];
  const h = harness(initial);
  assert.deepEqual(await m.runStepTierWritePlan(h.read, h.write, { harness: "pi", model: "a/one" }, null, 5), { ok: true });
  assert.equal(h.writes.length, 5);
  assert.equal(h.tiers.size, 0);
});
