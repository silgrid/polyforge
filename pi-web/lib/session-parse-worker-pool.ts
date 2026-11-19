// A bounded pool of `worker_threads` workers running
// SESSION_SCAN_WORKER_SOURCE (lib/session-scan-worker-source.ts): the session
// list's transcript parsing — a 20 MiB single JSON line included — happens off
// the server's main thread, so a scan never blocks other requests (polyforge#140).
//
// The pool holds at most a few workers (PI_WEB_SESSION_WORKERS, else
// min(4, cpus-1)); tasks beyond that wait in one queue, so the scanner's own
// concurrency limit stays the outer bound. State lives on globalThis so a Next
// dev reload never strands a worker with the old code.
//
// Tests read `globalThis.__piWebScanTaskLog` (when they set it to an array):
// one entry per stream read a completed task opened, exactly the facts the old
// `fs.createReadStream` interception recorded — `{ path, start }` for scans,
// and `{ path, start: 0, kind: "claude-entries" }` for transcript parses.
import { Worker } from "node:worker_threads";
import { availableParallelism } from "node:os";
import { SESSION_SCAN_WORKER_SOURCE } from "./session-scan-worker-source";

export interface SessionScanTaskResult {
  /** The scanned row, or null when the file is not a readable session. */
  info: {
    path: string;
    id: string;
    cwd: string;
    name?: string;
    created: Date;
    modified: Date;
    messageCount: number;
    firstMessage: string;
    parentSessionPath?: string;
  } | null;
  /** The file's resume point, when this scan kept one. */
  resume?: {
    ino: number;
    offset: number;
    headerEnd: number;
    check: string;
    hasFirstMessage: boolean;
    lastActivityTime?: number;
  };
  /** Byte offsets this task's stream reads began at, oldest first. */
  readStarts: number[];
}

interface PoolWorker {
  worker: Worker;
  /** The task whose answer this worker owes, when one is assigned. */
  pending?: { id: number; resolve: (value: unknown) => void; reject: (error: Error) => void };
}

interface PoolState {
  workers: PoolWorker[];
  /** Tasks waiting for a worker; the pool pumps this on every completion. */
  queue: Array<{ task: Record<string, unknown>; resolve: (value: unknown) => void; reject: (error: Error) => void }>;
  nextId: number;
}

function workerLimit(): number {
  const parsed = Number(process.env.PI_WEB_SESSION_WORKERS);
  if (Number.isInteger(parsed) && parsed > 0) return parsed;
  return Math.max(1, Math.min(4, availableParallelism() - 1));
}

function getPoolState(): PoolState {
  if (!globalThis.__piWebSessionScanPool) {
    globalThis.__piWebSessionScanPool = { workers: [], queue: [], nextId: 1 };
  }
  return globalThis.__piWebSessionScanPool;
}

function spawnWorker(state: PoolState): PoolWorker {
  const worker = new Worker(SESSION_SCAN_WORKER_SOURCE, { eval: true });
  // A pool never holds the process open: an unanswered task keeps it alive,
  // because the caller's promise is still pending.
  worker.unref();
  const entry: PoolWorker = { worker };
  worker.on("message", (message: { id: number; ok: boolean; result?: unknown; error?: string }) => {
    // A busy round-trip re-refs the worker; re-unref so an idle worker never
    // holds the process (a pending task still does, through its promise).
    worker.unref();
    const owed = entry.pending;
    entry.pending = undefined;
    if (owed && message.id === owed.id) {
      if (message.ok) owed.resolve(message.result);
      else owed.reject(new Error(message.error ?? "session scan worker failed"));
    }
    pump(state);
  });
  worker.on("error", (error: Error) => {
    const owed = entry.pending;
    entry.pending = undefined;
    owed?.reject(error);
    pump(state);
  });
  // A worker that dies without an error event (a hard crash) must reject its
  // task and leave the pool, never leave a caller waiting forever.
  worker.on("exit", () => {
    const index = state.workers.indexOf(entry);
    if (index !== -1) state.workers.splice(index, 1);
    const owed = entry.pending;
    entry.pending = undefined;
    owed?.reject(new Error("session scan worker exited before answering"));
    pump(state);
  });
  state.workers.push(entry);
  return entry;
}

function pump(state: PoolState): void {
  while (state.queue.length > 0) {
    let entry: PoolWorker | undefined = state.workers.find((candidate) => !candidate.pending);
    if (!entry) {
      if (state.workers.length >= workerLimit()) return;
      entry = spawnWorker(state);
    }
    const next = state.queue.shift();
    if (!next) return;
    const id = state.nextId++;
    entry.pending = { id, resolve: next.resolve, reject: next.reject };
    entry.worker.postMessage({ id, ...next.task });
  }
}

function logTaskReads(task: Record<string, unknown>, result: unknown): void {
  const log = globalThis.__piWebScanTaskLog;
  if (!Array.isArray(log)) return;
  const path = task.path;
  if (typeof path !== "string") return;
  if (task.kind === "claude-entries") {
    log.push({ path, start: 0, kind: "claude-entries" });
    return;
  }
  const starts = (result as SessionScanTaskResult | undefined | null)?.readStarts;
  if (Array.isArray(starts)) {
    for (const start of starts) log.push({ path, start });
  }
}

/**
 * Run one task on the pool and return its result. A worker that fails rejects;
 * the scanner treats that as it treated a thrown read: the file's row is null.
 */
export function runSessionScanTask(task: Record<string, unknown>): Promise<unknown> {
  const state = getPoolState();
  return new Promise((resolve, reject) => {
    state.queue.push({
      task,
      resolve: (result) => {
        logTaskReads(task, result);
        resolve(result);
      },
      reject,
    });
    pump(state);
  });
}

/** Test seam: terminate every worker and drop the queue. */
export function disposeSessionScanWorkerPoolForTests(): void {
  const state = globalThis.__piWebSessionScanPool;
  if (!state) return;
  globalThis.__piWebSessionScanPool = undefined;
  for (const entry of state.workers) {
    entry.pending?.reject(new Error("session scan worker pool disposed"));
    entry.worker.terminate().catch(() => undefined);
  }
  for (const queued of state.queue) queued.reject(new Error("session scan worker pool disposed"));
}

declare global {
  var __piWebSessionScanPool: PoolState | undefined;
  /** Set by a test to record the pool's completed reads. */
  var __piWebScanTaskLog: Array<Record<string, unknown>> | undefined;
}
