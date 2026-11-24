// carries: lib/settings-dirty.ts
// polyforge#259 (T4 AC1): 文本 / 多字段编辑走 dirty 跟踪，footer「保存（n 项
// 改动）」；工单列的边界是 n=0 不显示保存按钮、dirty 计数必须与实际写入字段一致。
// 期望值来自这两句，不取自实现：改两个字段是 2，改回原值是 0，patch 只含改动项。
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

const { dirtyCount, dirtyKeys, dirtyPatch, settleDraft } = await import("./settings-dirty.ts");

const BASE = { name: "reviewer", model: "pi:a", maxTurns: 8, tags: ["x"], enabled: true };

test("a fresh draft is clean and two changed fields count two", () => {
  assert.equal(dirtyCount(BASE, { ...BASE }), 0, "an untouched draft is dirty");
  assert.equal(dirtyCount(BASE, { ...BASE, name: "critic" }), 1);
  assert.equal(dirtyCount(BASE, { ...BASE, name: "critic", maxTurns: 12 }), 2);
  assert.equal(dirtyCount({ ...BASE, tags: ["x"] }, { ...BASE, tags: ["x"] }), 0, "an equal array counts as changed");
});

test("a value edited back to what it was is not a change", () => {
  const edited = { ...BASE, name: "critic" };
  assert.equal(dirtyCount(BASE, { ...edited, name: BASE.name }), 0, "an edit reverted by hand still counts");
  assert.deepEqual(dirtyKeys(BASE, { ...edited, name: BASE.name }), []);
});

test("the patch carries exactly the changed fields the save would write", () => {
  const draft = { ...BASE, name: "critic", maxTurns: 12 };
  assert.deepEqual(dirtyPatch(BASE, draft), { name: "critic", maxTurns: 12 });
  assert.deepEqual(dirtyPatch(BASE, { ...BASE }), {}, "a clean draft still sends fields");
  assert.deepEqual(dirtyKeys(BASE, draft), ["maxTurns", "name"], "the counted keys are the patched keys");
});

test("a partial save settles only what it wrote, so the rest stays dirty", () => {
  const draft = { ...BASE, name: "critic", maxTurns: 12 };
  // Only `name` landed; `maxTurns` was refused and keeps the draft.
  const settled = settleDraft(draft, { name: "critic" });
  assert.equal(settled.maxTurns, 12, "a failed field was dropped to the saved value");
  assert.equal(dirtyCount(BASE, settled), 1, "the failed field no longer reads as unsaved");
});
