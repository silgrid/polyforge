import { spawn } from "node:child_process";

/**
 * Process-layer supervisor for pi-web's built-in background tasks
 * (polyforge#172, unit bg-runtime).
 *
 * Every task runs in its own process group behind a small wrapper process
 * (Q12 A): pi-web spawns `node -e <wrapper source>` with `detached: true`, so
 * the wrapper becomes the process group leader; the wrapper starts
 * `/bin/sh -c <command>` inside its own group, streams the child's output to
 * the task log under a byte cap, watches the pi-web host pid it was told
 * (capturing `process.ppid` at its own start is too late: the host may die
 * before the wrapper finishes booting, and a reparented wrapper would then
 * record 1 as "normal"), and kills the whole group when that host is gone.
 * No dependency, no resident watchdog process (Q10: nothing new is installed).
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
  /**
   * The pid of the host whose death must take the group with it (AC4);
   * defaults to the current process, pi-web's server.
   */
  hostPid?: number;
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
  /** Resolves once the wrapper has installed its signal handlers (it prints `R` first). */
  ready: Promise<void>;
  /** Resolves once the task's process group is gone (with the wrapper's report when it could write one). */
  done: Promise<BgSupervisorResult>;
  /** Stops the task's whole process group; idempotent (a gone group is a success). */
  stop(): Promise<void>;
}

/**
 * The wrapper process. It runs as its own group leader and, in order:
 *  - prints `R` to stdout (fd 1, synchronous) as soon as its SIGTERM handler is
 *    installed — a stop that lands earlier would hit node's default SIGTERM
 *    disposition and lose the `stopped` report;
 *  - spawns `/bin/sh -c <command>` (argv: command, cwd, logPath, logMaxBytes, hostPid);
 *  - streams the child's stdout and stderr to the log, counting bytes and never
 *    writing past the byte cap (a chunk is cut to the remaining room, Q7 A);
 *  - stops the whole group past the cap, reporting `log-limit`;
 *  - answers a SIGTERM addressed to itself (bg_stop's first step) by reporting
 *    `stopped` and killing the group;
 *  - polls its parent pid against the host pid it was told and kills the group
 *    as soon as they differ (the pi-web server was SIGKILLed; AC4);
 *  - prints one JSON result line to stdout before dying.
 */
const WRAPPER_SOURCE = String.raw`
const fs = require("node:fs");
const { spawn } = require("node:child_process");
const command = process.argv[1];
const cwd = process.argv[2];
const logPath = process.argv[3];
const maxBytesArg = Number(process.argv[4]);
const maxBytes = Number.isFinite(maxBytesArg) && maxBytesArg > 0 ? maxBytesArg : 64 * 1024 * 1024;
const hostPid = Number(process.argv[5]);
const logFd = fs.openSync(logPath, "a");
let bytes = 0;
let settled = false;
function writeResult(result) {
  if (settled) return;
  settled = true;
  try { fs.writeSync(1, JSON.stringify(result) + "\n"); } catch {}
}
function killGroup() {
  try { process.kill(-process.pid, "SIGKILL"); } catch {}
}
process.on("SIGTERM", () => {
  writeResult({ exitCode: null, signal: "SIGTERM", bytesWritten: bytes, reason: "stopped" });
  killGroup();
  process.exit(0);
});
try { fs.writeSync(1, "R\n"); } catch {}
const child = spawn("/bin/sh", ["-c", command], { cwd, stdio: ["ignore", "pipe", "pipe"] });
function append(chunk) {
  const room = maxBytes - bytes;
  if (room <= 0) return;
  const write = room < chunk.length ? chunk.subarray(0, room) : chunk;
  bytes += write.length;
  try { fs.writeSync(logFd, write); } catch {}
  if (bytes >= maxBytes) {
    writeResult({ exitCode: null, signal: null, bytesWritten: bytes, reason: "log-limit" });
    killGroup();
    process.exit(0);
  }
}
child.stdout.on("data", append);
child.stderr.on("data", append);
child.on("exit", (code, signal) => {
  // The child's stdio may still hold the command's last bytes when the shell
  // exits, and a straggler may still be writing through the inherited pipe:
  // drain both pipes (bounded — a straggler that never ends cannot hold the
  // task) before reporting, so the log keeps the whole output.
  let pendingPipes = 2;
  let finished = false;
  const finish = () => {
    if (finished) return;
    finished = true;
    // Report first: the group kill below includes this wrapper.
    writeResult({ exitCode: code, signal: signal == null ? null : String(signal), bytesWritten: bytes, reason: null });
    killGroup();
    process.exit(0);
  };
  const settle = () => {
    pendingPipes -= 1;
    if (pendingPipes <= 0) finish();
  };
  const drain = (stream) => {
    // The end event may already have fired before this handler registered.
    if (stream.readableEnded) settle();
    else stream.once("end", settle);
  };
  drain(child.stdout);
  drain(child.stderr);
  // Ref'd: the report must land even when nothing else holds the loop.
  setTimeout(finish, 2000);
});
child.on("error", (error) => {
  writeResult({ exitCode: null, signal: null, bytesWritten: bytes, reason: null, error: String((error && error.message) || error) });
  process.exit(0);
});
const hostWatch = setInterval(() => {
  if (!Number.isFinite(hostPid) || process.ppid !== hostPid) {
    killGroup();
    process.exit(0);
  }
}, 250);
hostWatch.unref();
`;

function groupAlive(pgid: number): boolean {
  try {
    process.kill(-pgid, 0);
    return true;
  } catch (error) {
    return (error as NodeJS.ErrnoException).code === "EPERM";
  }
}

const sleep = (ms: number): Promise<void> => new Promise((resolve) => { setTimeout(resolve, ms); });

const READY_TIMEOUT_MS = 10_000;
const STOP_TERM_GRACE_MS = 2_000;
const STOP_POLL_MS = 50;

/** Spawns the task behind its wrapper process; throws synchronously when the spawn itself fails. */
export function spawnBgSupervisedTask(options: BgSupervisorSpawnOptions): BgSupervisorTask {
  const logMaxBytes = options.logMaxBytes ?? BG_LOG_MAX_BYTES_DEFAULT;
  const hostPid = options.hostPid ?? process.pid;
  const child = spawn(
    process.execPath,
    ["-e", WRAPPER_SOURCE, "--", options.command, options.cwd, options.logPath, String(logMaxBytes), String(hostPid)],
    { detached: true, cwd: options.cwd, env: options.env, stdio: ["ignore", "pipe", "pipe"] },
  );
  const pgid = child.pid;
  if (!pgid) {
    throw new Error(`could not spawn the background task wrapper (cwd: ${options.cwd})`);
  }
  let resultSettled = false;
  let readySettled = false;
  let stdoutBuffer = "";
  const resolveReady = (): void => {
    if (readySettled) return;
    readySettled = true;
    resolveReadyRef();
  };
  let resolveReadyRef: () => void = () => {};
  const ready = new Promise<void>((resolve) => {
    resolveReadyRef = resolve;
    // A wrapper that never reports in (crashed, host gone) must not block a stop.
    const timer = setTimeout(resolve, READY_TIMEOUT_MS);
    timer.unref?.();
  });
  const done = new Promise<BgSupervisorResult>((resolve) => {
    const settle = (result: BgSupervisorResult): void => {
      if (resultSettled) return;
      resultSettled = true;
      resolve(result);
      resolveReady();
    };
    child.stdout.on("data", (chunk: Buffer) => {
      stdoutBuffer += chunk.toString("utf8");
      for (;;) {
        const lineEnd = stdoutBuffer.indexOf("\n");
        if (lineEnd === -1) return;
        const line = stdoutBuffer.slice(0, lineEnd);
        stdoutBuffer = stdoutBuffer.slice(lineEnd + 1);
        if (line === "R") {
          resolveReady();
          continue;
        }
        try {
          const parsed = JSON.parse(line) as Partial<BgSupervisorResult>;
          settle({
            exitCode: typeof parsed.exitCode === "number" ? parsed.exitCode : null,
            signal: typeof parsed.signal === "string" ? parsed.signal : null,
            bytesWritten: typeof parsed.bytesWritten === "number" ? parsed.bytesWritten : null,
            reason: parsed.reason === "log-limit" || parsed.reason === "stopped" ? parsed.reason : null,
          });
        } catch {
          // A malformed line falls through to the close event below.
        }
      }
    });
    // Drained, never surfaced: the task's own output lives in its log.
    child.stderr.on("data", () => {});
    child.on("close", (code, signal) => {
      settle({ exitCode: code, signal: signal ?? null, bytesWritten: null, reason: null });
    });
    child.on("error", () => {
      settle({ exitCode: null, signal: null, bytesWritten: null, reason: null });
    });
  });
  const stop = async (): Promise<void> => {
    await ready;
    // SIGTERM to the leader first, so the wrapper itself reports `stopped`;
    // a leader that is gone gets the group-wide signal instead.
    try {
      process.kill(pgid, "SIGTERM");
    } catch {
      try { process.kill(-pgid, "SIGTERM"); } catch { /* fall through to the poll */ }
    }
    const deadline = Date.now() + STOP_TERM_GRACE_MS;
    while (groupAlive(pgid) && Date.now() < deadline) {
      await sleep(STOP_POLL_MS);
    }
    if (!groupAlive(pgid)) return;
    try {
      process.kill(-pgid, "SIGKILL");
    } catch {
      return;
    }
    const killDeadline = Date.now() + 500;
    while (groupAlive(pgid) && Date.now() < killDeadline) {
      await sleep(STOP_POLL_MS);
    }
  };
  return { pgid, logPath: options.logPath, ready, done, stop };
}

/**
 * Stops a task's whole process group by pgid alone, without a task object (a
 * cleanup path); idempotent. Unlike {@link BgSupervisorTask.stop} it cannot
 * wait for the wrapper's readiness, so a wrapper still booting loses its
 * `stopped` report to node's default SIGTERM disposition.
 */
export async function stopBgTaskGroup(pgid: number): Promise<void> {
  if (!Number.isInteger(pgid) || pgid <= 0) return;
  try {
    process.kill(pgid, "SIGTERM");
  } catch {
    try { process.kill(-pgid, "SIGTERM"); } catch { /* fall through to the poll */ }
  }
  const deadline = Date.now() + STOP_TERM_GRACE_MS;
  while (groupAlive(pgid) && Date.now() < deadline) {
    await sleep(STOP_POLL_MS);
  }
  if (!groupAlive(pgid)) return;
  try {
    process.kill(-pgid, "SIGKILL");
  } catch {
    return;
  }
  const killDeadline = Date.now() + 500;
  while (groupAlive(pgid) && Date.now() < killDeadline) {
    await sleep(STOP_POLL_MS);
  }
}
