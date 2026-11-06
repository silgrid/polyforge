/**
 * Client-safe rules of the machine-load section: the route's
 * five-field metrics type, the whitelist merge that keeps the last good value
 * on any failure, and the pinned, locale-invariant number formats. Nothing
 * here touches `node:os`, so the hook and the component import it freely.
 */

/** The route's whitelisted aggregate, as the host kernel sees it. */
export interface MachineLoadMetrics {
  /** Busy share of all logical CPUs since the previous fresh sample, 0..100; null until a baseline exists. */
  cpuPercent: number | null;
  coreCount: number;
  /** 1/5/15-minute load averages; null where the platform has none (Windows). */
  loadavg: [number, number, number] | null;
  memoryUsedBytes: number;
  memoryTotalBytes: number;
}

/** Shown where a value is not computable yet (CPU before a baseline, loadavg on Windows). */
export const MACHINE_LOAD_DASH = "—";

const GIB = 1024 ** 3;

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

/**
 * The next state after a response body: a valid whitelisted `metrics` object
 * replaces the state with a copy of just its five fields; anything else (no
 * body, non-JSON, `metrics: null`, a malformed object) keeps `previous`.
 */
export function nextMachineLoadMetrics(previous: MachineLoadMetrics | null, body: unknown): MachineLoadMetrics | null {
  if (!body || typeof body !== "object") return previous;
  const metrics = (body as { metrics?: unknown }).metrics;
  if (!metrics || typeof metrics !== "object") return previous;
  const { cpuPercent, coreCount, loadavg, memoryUsedBytes, memoryTotalBytes } = metrics as Record<string, unknown>;
  if (cpuPercent !== null && !(isFiniteNumber(cpuPercent) && cpuPercent >= 0 && cpuPercent <= 100)) return previous;
  if (!Number.isInteger(coreCount) || (coreCount as number) <= 0) return previous;
  if (loadavg !== null && !(Array.isArray(loadavg) && loadavg.length === 3 && loadavg.every(isFiniteNumber))) return previous;
  if (!isFiniteNumber(memoryUsedBytes) || !isFiniteNumber(memoryTotalBytes)) return previous;
  if (memoryUsedBytes < 0 || memoryTotalBytes <= 0 || memoryUsedBytes > memoryTotalBytes) return previous;
  return {
    cpuPercent,
    coreCount: coreCount as number,
    loadavg: loadavg === null ? null : [loadavg[0], loadavg[1], loadavg[2]],
    memoryUsedBytes,
    memoryTotalBytes,
  };
}

/** `NN.N%`, or the dash before a CPU baseline exists. */
export function formatCpuPercent(value: number | null): string {
  return value === null ? MACHINE_LOAD_DASH : `${value.toFixed(1)}%`;
}

/** `N.NN / N.NN / N.NN`, or the dash where the platform has no load average. */
export function formatLoadavg(value: [number, number, number] | null): string {
  return value === null ? MACHINE_LOAD_DASH : value.map((item) => item.toFixed(2)).join(" / ");
}

/** `N.N / N.N GiB`. */
export function formatMemory(usedBytes: number, totalBytes: number): string {
  return `${(usedBytes / GIB).toFixed(1)} / ${(totalBytes / GIB).toFixed(1)} GiB`;
}

export interface MachineLoadLabels {
  cpu: string;
  cores: string;
  loadavg: string;
  memory: string;
  unavailable: string;
}

export interface MachineLoadRow {
  key: "cpu" | "cores" | "loadavg" | "memory";
  label: string;
  value: string;
}

/** Exactly the four rows the section shows; with no good value yet every row is the unavailable placeholder. */
export function machineLoadRows(metrics: MachineLoadMetrics | null, labels: MachineLoadLabels): MachineLoadRow[] {
  return [
    { key: "cpu", label: labels.cpu, value: metrics ? formatCpuPercent(metrics.cpuPercent) : labels.unavailable },
    { key: "cores", label: labels.cores, value: metrics ? String(metrics.coreCount) : labels.unavailable },
    { key: "loadavg", label: labels.loadavg, value: metrics ? formatLoadavg(metrics.loadavg) : labels.unavailable },
    {
      key: "memory",
      label: labels.memory,
      value: metrics ? formatMemory(metrics.memoryUsedBytes, metrics.memoryTotalBytes) : labels.unavailable,
    },
  ];
}
