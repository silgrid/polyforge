// 一次成功写入的撤销记录（polyforge#259 T4 AC1）：开关与单选立即写，toast 里
// 「已保存 · 撤销」；撤销只在 toast 生命周期内、只针对同一字段的最后一次写入
// 写回它自己刚覆盖掉的旧值（工单计划审第 2 条）。纯函数，时钟由调用方给。

/** 一次可撤销的写入：哪个字段、写前的值、什么时候写的、还能撤销多久。 */
export interface SettingsWrite {
  field: string;
  /** 写前的值；撤销就是把这一项写回去。 */
  previous: unknown;
  /** 写入成功的时刻（毫秒）。 */
  at: number;
  /** toast 的生命周期，过了就不能撤销了。 */
  lifetimeMs: number;
}

export const UNDO_LIFETIME_MS = 6000;

/** 登记一次写入；失败的写入不登记（撤销不能把一次失败变成一次成功）。 */
export function recordWrite(_input: {
  field: string;
  previous: unknown;
  ok: boolean;
  at: number;
  lifetimeMs?: number;
}): SettingsWrite | null {
  return null;
}

/** 这条记录现在还能撤销吗。 */
export function undoAvailable(_record: SettingsWrite | null, _now: number): boolean {
  return false;
}

/** 撤销要写回的那一项（字段与旧值）；没有记录时为 null。 */
export function undoWrite(_record: SettingsWrite | null): { field: string; value: unknown } | null {
  return null;
}
