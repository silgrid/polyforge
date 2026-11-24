// carries: lib/settings-undo.ts
// polyforge#259 (T4 AC1): 开关与单选立即写 + toast「已保存 · 撤销」，撤销范围是
// 「当前 toast 生命周期内同一字段的最后一次写入」（工单计划审第 2 条）。期望值
// 来自这条规则与工单列的边界（请求失败、撤销过期）：时钟由测试给定，不取自实现。
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

const { UNDO_LIFETIME_MS, recordWrite, undoAvailable, undoWrite } = await import("./settings-undo.ts");

const AT = 1_700_000_000_000;

test("a successful write can be undone within the toast's lifetime, never after it", () => {
  const record = recordWrite({ field: "skill:a", previous: "disabled", ok: true, at: AT });
  assert.ok(record, "a successful write left no undo record");
  assert.equal(undoAvailable(record, AT + 10), true, "a fresh write cannot be undone");
  assert.equal(undoAvailable(record, AT + UNDO_LIFETIME_MS - 1), true, "the record expires early");
  assert.equal(undoAvailable(record, AT + UNDO_LIFETIME_MS), false, "an expired record is still undoable");
  assert.equal(undoAvailable(null, AT), false, "no record reads as undoable");
});

test("undo writes back the field's own previous value", () => {
  const record = recordWrite({ field: "skill:a", previous: "disabled", ok: true, at: AT });
  assert.deepEqual(undoWrite(record), { field: "skill:a", value: "disabled" });
  assert.equal(undoWrite(null), null);
});

test("a failed write records nothing, so a failure never becomes an undo", () => {
  assert.equal(recordWrite({ field: "skill:a", previous: "enabled", ok: false, at: AT }), null);
});

test("the last write of a field wins and another field never mixes in", () => {
  const first = recordWrite({ field: "skill:a", previous: "enabled", ok: true, at: AT });
  const second = recordWrite({ field: "skill:a", previous: "disabled", ok: true, at: AT + 100 });
  assert.deepEqual(undoWrite(second), { field: "skill:a", value: "disabled" }, "the second write's undo value is the first write's result");
  assert.notDeepEqual(undoWrite(second), undoWrite(first));
  const other = recordWrite({ field: "skill:b", previous: "enabled", ok: true, at: AT + 200 });
  assert.equal(undoWrite(other).field, "skill:b", "an undo of one field names another");
});
