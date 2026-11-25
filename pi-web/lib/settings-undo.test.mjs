// carries: lib/settings-undo.ts
// polyforge#259 (T4 AC1)：开关与单选立即写 + toast「已保存 · 撤销」，撤销写回
// 「它自己刚覆盖的旧值」（工单计划审第 2 条），并且只在 toast 生命周期内、只在
// 该字段仍是这次写入留下的值时（owner 4A：并发修改时放弃撤销，不覆盖别人）。
// 期望值来自这两条规则，不取自实现：旧值是写入前逐 harness 记下的真实状态，时钟
// 与当前值都由测试给定。
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

const { UNDO_LIFETIME_MS, recordWriteItems, undoPlan } = await import("./settings-undo.ts");

const AT = 1_700_000_000_000;
// skila: pi was enabled, claude held its own native word.
const PREVIOUS = [{ harness: "pi", state: "enabled" }, { harness: "claude", state: "user-invocable-only" }];
const WRITTEN = [{ harness: "pi", state: "disabled" }, { harness: "claude", state: "disabled" }];
const record = (patch = {}) => recordWriteItems({ field: "skill:a", previous: PREVIOUS, written: WRITTEN, ok: true, at: AT, ...patch });

test("undo writes back each harness's own previous value, never a value derived from the new one", () => {
  const plan = undoPlan(record(), WRITTEN, AT + 10);
  assert.deepEqual(plan, { ok: true, items: PREVIOUS });
  // A claude word that is not the mirror image of "enabled" survives.
  assert.equal(plan.items.find((item) => item.harness === "claude").state, "user-invocable-only");
});

test("undo is offered only inside the toast's lifetime", () => {
  assert.equal(undoPlan(record(), WRITTEN, AT + UNDO_LIFETIME_MS - 1).ok, true);
  assert.deepEqual(undoPlan(record(), WRITTEN, AT + UNDO_LIFETIME_MS), { ok: false, reason: "expired" });
  assert.deepEqual(undoPlan(null, WRITTEN, AT), { ok: false, reason: "expired" });
});

test("a field something else changed since the write is left alone (owner 4A)", () => {
  const changedElsewhere = [{ harness: "pi", state: "enabled" }, { harness: "claude", state: "disabled" }];
  assert.deepEqual(undoPlan(record(), changedElsewhere, AT + 10), { ok: false, reason: "changed" },
    "the undo overwrites a value another writer set");
});

test("a failed write records nothing, so a failure never becomes an undo", () => {
  assert.equal(recordWriteItems({ field: "skill:a", previous: PREVIOUS, written: WRITTEN, ok: false, at: AT }), null);
  assert.deepEqual(undoPlan(recordWriteItems({ field: "skill:a", previous: PREVIOUS, written: WRITTEN, ok: false, at: AT }), WRITTEN, AT), { ok: false, reason: "expired" });
});

test("the last write of a field wins", () => {
  const second = record({ previous: WRITTEN, written: [{ harness: "pi", state: "enabled" }, { harness: "claude", state: "enabled" }], at: AT + 100 });
  assert.deepEqual(undoPlan(second, [{ harness: "pi", state: "enabled" }, { harness: "claude", state: "enabled" }], AT + 110),
    { ok: true, items: WRITTEN });
});
