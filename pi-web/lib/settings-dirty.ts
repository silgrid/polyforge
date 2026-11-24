// dirty 跟踪（polyforge#259 T4 AC1）：文本与多字段编辑不立即写，footer 主按钮
// 「保存（n 项改动）」一次性提交；切 tab 或关闭时若有未保存改动要提示。纯函数，
// 期望值来自需求：n 是「实际会写出去的字段数」，改回原值的不计。

/** 一份表单草稿：字段名到值。 */
export type SettingsDraft = Record<string, unknown>;

/** 草稿里与初始值不同的字段名（浅比较，按值相等）。 */
export function dirtyKeys(initial: SettingsDraft, draft: SettingsDraft): string[] {
  return [];
}

/** footer 的「保存（n 项改动）」里的 n。 */
export function dirtyCount(initial: SettingsDraft, draft: SettingsDraft): number {
  return 0;
}

/** 只含改动字段的补丁——保存写出去的正好这些。 */
export function dirtyPatch(initial: SettingsDraft, draft: SettingsDraft): SettingsDraft {
  return {};
}

/** 保存成功后落定：只把真的写成功的字段对齐到已保存值，其余仍是草稿。 */
export function settleDraft(draft: SettingsDraft, saved: SettingsDraft): SettingsDraft {
  return draft;
}
