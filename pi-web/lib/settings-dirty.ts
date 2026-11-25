// dirty 跟踪（polyforge#259 T4 AC1）：文本与多字段编辑不立即写，footer 主按钮
// 「保存（n 项改动）」一次性提交；切 tab 或关闭时若有未保存改动要提示。纯函数，
// 期望值来自需求：n 是「实际会写出去的字段数」，改回原值的不计。

/** 一份表单草稿：字段名到值。 */
export type SettingsDraft = Record<string, unknown>;

/** 两个值是否相等：标量按 ===，数组与对象按 JSON 形状（草稿里只有这两类）。 */
function sameValue(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (typeof a !== "object") return false;
  return JSON.stringify(a) === JSON.stringify(b);
}

/** 草稿里与初始值不同的字段名（按值相等，改回原值的不算）。 */
export function dirtyKeys(initial: SettingsDraft, draft: SettingsDraft): string[] {
  const keys = new Set([...Object.keys(initial), ...Object.keys(draft)]);
  return [...keys].filter((key) => !sameValue(initial[key], draft[key])).sort();
}

/** footer 的「保存（n 项改动）」里的 n。 */
export function dirtyCount(initial: SettingsDraft, draft: SettingsDraft): number {
  return dirtyKeys(initial, draft).length;
}

/** 只含改动字段的补丁——保存写出去的正好这些。 */
export function dirtyPatch(initial: SettingsDraft, draft: SettingsDraft): SettingsDraft {
  const patch: SettingsDraft = {};
  for (const key of dirtyKeys(initial, draft)) patch[key] = draft[key];
  return patch;
}

/** 保存成功后落定：只把真的写成功的字段对齐到已保存值，其余仍是草稿。 */
export function settleDraft(draft: SettingsDraft, saved: SettingsDraft): SettingsDraft {
  const next = { ...draft };
  for (const key of Object.keys(saved)) next[key] = saved[key];
  return next;
}
