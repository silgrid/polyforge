import { readFileSync } from "node:fs";
import os from "node:os";
import type { MachineLoadMetrics } from "@/lib/machine-load-view";

/** One fresh sample serves every request inside this window; the first request at or after it samples again. */
export const MACHINE_LOAD_WINDOW_MS = 2000;

/** A CPU baseline older than this (five windows) is dropped: the next sample reports `cpuPercent: null` and rebaselines, never an average over a long idle gap. */
export const MACHINE_LOAD_BASELINE_MAX_AGE_MS = 10000;

type MachineOs = Pick<typeof os, "cpus" | "loadavg" | "totalmem" | "freemem">;

export interface MachineLoadSamplerOptions {
  os?: MachineOs;
  now?: () => number;
  platform?: NodeJS.Platform;
  /** Linux only: the text of `/proc/self/status` (injectable for tests). */
  readProcStatus?: () => string;
}

export interface MachineLoadSampler {
  /** The current window's sample, or a fresh one; null when sampling failed (never throws, never cached). */
  sample(): MachineLoadMetrics | null;
}

interface CpuTimes {
  idle: number;
  total: number;
}

function readCpuTimes(cpus: ReturnType<MachineOs["cpus"]>): CpuTimes | null {
  if (!Array.isArray(cpus) || cpus.length === 0) return null;
  let idle = 0;
  let total = 0;
  for (const cpu of cpus) {
    const times = cpu?.times;
    if (!times) return null;
    idle += times.idle;
    total += times.user + times.nice + times.sys + times.idle + times.irq;
  }
  return Number.isFinite(idle) && Number.isFinite(total) ? { idle, total } : null;
}

/** Count a Linux CPU list (`0-3,8,10-11`); null when it does not parse. Pure. */
export function countCpuList(list: string): number | null {
  let count = 0;
  for (const part of list.trim().split(",")) {
    const match = /^(\d+)(?:-(\d+))?$/.exec(part.trim());
    if (!match) return null;
    const low = Number(match[1]);
    const high = match[2] === undefined ? low : Number(match[2]);
    if (high < low) return null;
    count += high - low + 1;
  }
  return count;
}

/**
 * The cores this process may run on, as `nproc` counts them:
 * on Linux the `Cpus_allowed_list` of `/proc/self/status` (affinity/cpuset
 * aware, not cgroup-quota aware — `os.availableParallelism()` counts the quota
 * and disagrees with `nproc` on a quota-limited host). Anywhere else, or when
 * that file is unreadable or unparsable, `os.cpus().length`.
 */
function readCoreCount(cpuCount: number, platform: NodeJS.Platform, readProcStatus: () => string): number {
  if (platform !== "linux") return cpuCount;
  try {
    const list = /^Cpus_allowed_list:\s*(\S+)\s*$/m.exec(readProcStatus())?.[1];
    const count = list ? countCpuList(list) : null;
    return count && count > 0 ? count : cpuCount;
  } catch {
    return cpuCount;
  }
}

function readLoadavg(source: MachineOs, platform: NodeJS.Platform): [number, number, number] | null {
  // Windows has no load average: os.loadavg() answers [0, 0, 0], which is not a measurement.
  if (platform === "win32") return null;
  const values = source.loadavg();
  if (!Array.isArray(values) || values.length !== 3 || !values.every((value) => Number.isFinite(value) && value >= 0)) {
    return null;
  }
  return [values[0], values[1], values[2]];
}

/**
 * On-demand host sampler: it reads `os` only inside `sample()` (no timer, no
 * background work), shares one sample for MACHINE_LOAD_WINDOW_MS, and derives
 * CPU usage from the delta between two fresh samples — never the since-boot
 * average, and never across a baseline older than
 * MACHINE_LOAD_BASELINE_MAX_AGE_MS (that sample reports null and
 * rebaselines). A failed pass returns null and leaves the cache and baseline as
 * they were, so the next request simply tries again.
 */
export function createMachineLoadSampler(options: MachineLoadSamplerOptions = {}): MachineLoadSampler {
  const source = options.os ?? os;
  const now = options.now ?? Date.now;
  const platform = options.platform ?? process.platform;
  const readProcStatus = options.readProcStatus ?? (() => readFileSync("/proc/self/status", "utf8"));
  let cached: { at: number; metrics: MachineLoadMetrics } | null = null;
  let baseline: { times: CpuTimes; at: number } | null = null;

  return {
    sample() {
      try {
        const at = now();
        if (cached && at >= cached.at && at - cached.at < MACHINE_LOAD_WINDOW_MS) return cached.metrics;

        const cpus = source.cpus();
        const times = readCpuTimes(cpus);
        if (!times) return null;
        const memoryTotalBytes = source.totalmem();
        const freeBytes = source.freemem();
        if (!Number.isSafeInteger(memoryTotalBytes) || memoryTotalBytes <= 0 || !Number.isFinite(freeBytes)) return null;
        const loadavg = readLoadavg(source, platform);

        let cpuPercent: number | null = null;
        if (baseline && at - baseline.at <= MACHINE_LOAD_BASELINE_MAX_AGE_MS) {
          const deltaTotal = times.total - baseline.times.total;
          const deltaIdle = times.idle - baseline.times.idle;
          if (deltaTotal > 0) cpuPercent = Math.min(100, Math.max(0, ((deltaTotal - deltaIdle) / deltaTotal) * 100));
        }

        // Cores this process may run on (matches `nproc`); CPU above stays the
        // summed host view of every logical CPU.
        const coreCount = readCoreCount(cpus.length, platform, readProcStatus);

        const metrics: MachineLoadMetrics = {
          cpuPercent,
          coreCount,
          loadavg,
          memoryUsedBytes: Math.min(memoryTotalBytes, Math.max(0, Math.round(memoryTotalBytes - freeBytes))),
          memoryTotalBytes,
        };
        baseline = { times, at };
        cached = { at, metrics };
        return metrics;
      } catch {
        return null;
      }
    },
  };
}

/** The process-wide sampler the route uses: every client shares its window. */
export const machineLoadSampler = createMachineLoadSampler();
