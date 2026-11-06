import { execFile } from "child_process";
import { promisify } from "util";

/**
 * The one promisified `execFile` for the whole app (audit P5). Previously
 * every child-process call site declared its own `promisify(execFile)` and
 * two modules kept private, slightly different `git()` wrappers. Import this
 * instead of promisifying again — the spawn options (locale pinning,
 * timeouts, buffers) belong in ONE place so they cannot drift.
 */
export const execFileAsync = promisify(execFile);

export interface RunGitOptions {
  /** Defaults to 10s, matching the previous per-module wrappers. */
  timeoutMs?: number;
  /** Defaults to Node's own 1MB when omitted. */
  maxBuffer?: number;
  /** Trim trailing whitespace from stdout. Off by default: callers that
   *  parse position-sensitive output (porcelain status) need it raw. */
  trim?: boolean;
}

/**
 * `git -C <cwd> <args…>` with the message locale pinned to C so error-text
 * matching (dirty-worktree detection, status parsing) is language-independent.
 * Never uses a shell: arguments are passed as argv only.
 */
export async function runGit(cwd: string, args: string[], options: RunGitOptions = {}): Promise<string> {
  const { stdout } = await execFileAsync("git", ["-C", cwd, ...args], {
    timeout: options.timeoutMs ?? 10_000,
    ...(options.maxBuffer !== undefined ? { maxBuffer: options.maxBuffer } : {}),
    env: { ...process.env, LC_ALL: "C" },
  });
  return options.trim ? stdout.trim() : stdout;
}
