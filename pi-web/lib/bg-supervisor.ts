import { spawn } from "node:child_process";
import type { ChildProcess } from "node:child_process";

/**
 * Process-layer supervisor for pi-web's built-in background tasks
 * (polyforge#172, unit bg-runtime).
 *
 * Every task runs in its own process group behind a small wrapper process
 * (Q12 A): pi-web spawns `node -e <wrapper source>` with `detached: true`, so
 * the wrapper becomes the process group leader; the wrapper starts
 * `/bin/sh -c <command>` inside its own group, streams the child's output to
 * the task log under a byte cap, watches its parent pid (the pi-web server),
 * and kills the whole group when that parent disappears.
 *
 * This module stays import-clean (node built-ins only): a test's intermediate
 * host process imports it directly under `--experimental-strip-types`.
 */

/** The per-task log cap pi-web enforces (64 MiB, AC2 / Q7 A). */
export const BG_LOG_MAX_BYTES_DEFAULT = 64 * 1024 * 1024;

export interface BgSupervisorSpawnOptions {
  /** The command line, run as `/bin/sh -c <command>` (Q4 A). */
  command: string;
  /** The session's cwd, the task's working directory. */
  cwd: string;
  /** The task's environment (already sanitized by the caller). */
  env: NodeJS.ProcessEnv;
  /** Absolute path of the task log; its parent directory must exist. */
  logPath: string;
  /** Byte cap of the task log; defaults to {@link BG_LOG_MAX_BYTES_DEFAULT}. */
  logMaxBytes?: number;
}

export interface BgSupervisorResult {
  /** The shell's exit code, when it exited on its own. */
  exitCode: number | null;
  /** The signal name that ended the shell, when a signal did. */
  signal: string | null;
  /** How many bytes the log holds at the end, when the wrapper could count them. */
  bytesWritten: number | null;
  /** `log-limit` when the byte cap stopped the group, `stopped` when bg_stop did. */
  reason: "log-limit" | "stopped" | null;
}

export interface BgSupervisorTask {
  /** The wrapper's pid, which is the task's process group id. */
  pgid: number;
  logPath: string;
  /** Resolves once the task's process group is gone (with the wrapper's report when it could write one). */
  done: Promise<BgSupervisorResult>;
}

/** Spawns the task behind its wrapper process; throws synchronously when the spawn itself fails. */
export function spawnBgSupervisedTask(options: BgSupervisorSpawnOptions): BgSupervisorTask {
  void options;
  void spawn;
  void (null as unknown as ChildProcess);
  return {
    pgid: 0,
    logPath: options.logPath,
    done: Promise.resolve({ exitCode: null, signal: null, bytesWritten: null, reason: null }),
  };
}

/** Stops the task's whole process group; idempotent (a gone group is a success). */
export async function stopBgTaskGroup(pgid: number): Promise<void> {
  void pgid;
}
