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
export function recordWrite(input: {
  field: string;
  previous: unknown;
  ok: boolean;
  at: number;
  lifetimeMs?: number;
}): SettingsWrite | null {
  if (!input.ok) return null;
  return {
    field: input.field,
    previous: input.previous,
    at: input.at,
    lifetimeMs: input.lifetimeMs ?? UNDO_LIFETIME_MS,
  };
}

/** 这条记录现在还能撤销吗。 */
export function undoAvailable(record: SettingsWrite | null, now: number): boolean {
  if (record === null) return false;
  return now < record.at + record.lifetimeMs;
}

/** 撤销要写回的那一项（字段与旧值）；没有记录时为 null。 */
export function undoWrite(record: SettingsWrite | null): { field: string; value: unknown } | null {
  if (record === null) return null;
  return { field: record.field, value: record.previous };
}

/** 一个字段（一个 harness 的那一份）在某一刻的值。 */
export interface SettingsWriteItem {
  harness: string;
  /** 该 harness 自己的状态词（pi 的统一态、claude 的原生词）。 */
  state: string;
}

/** 撤销计划：还能撤销时要写回的逐 harness 旧值，否则是放弃的原因。 */
export type UndoPlan =
  | { ok: true; items: SettingsWriteItem[] }
  | { ok: false; reason: "expired" | "changed" };

/** 登记一次多 harness 写入；失败不登记。 */
export function recordWriteItems(input: {
  field: string;
  previous: SettingsWriteItem[];
  written: SettingsWriteItem[];
  ok: boolean;
  at: number;
  lifetimeMs?: number;
}): SettingsWriteItems | null {
  if (!input.ok) return null;
  return {
    field: input.field,
    previous: input.previous.map((item) => ({ ...item })),
    written: input.written.map((item) => ({ ...item })),
    at: input.at,
    lifetimeMs: input.lifetimeMs ?? UNDO_LIFETIME_MS,
  };
}

/**
 * 撤销计划（owner 4A）：过期就放弃；字段里有一个已经不是这次写入留下的值，说明
 * 别处改过它，也放弃——撤销永远不覆盖别人的写入。
 */
export function undoPlan(
  record: SettingsWriteItems | null,
  current: SettingsWriteItem[],
  now: number,
): UndoPlan {
  if (record === null || now >= record.at + record.lifetimeMs) return { ok: false, reason: "expired" };
  const byHarness = new Map(current.map((item) => [item.harness, item.state]));
  for (const written of record.written) {
    if (!byHarness.has(written.harness) || byHarness.get(written.harness) !== written.state) {
      return { ok: false, reason: "changed" };
    }
  }
  return { ok: true, items: record.previous.map((item) => ({ ...item })) };
}

/** 一次多 harness 写入的撤销记录。 */
export interface SettingsWriteItems {
  field: string;
  /** 写之前每个 harness 真实的值。 */
  previous: SettingsWriteItem[];
  /** 这次写入实际设置的值（并发检查拿当前值比它）。 */
  written: SettingsWriteItem[];
  at: number;
  lifetimeMs: number;
}
