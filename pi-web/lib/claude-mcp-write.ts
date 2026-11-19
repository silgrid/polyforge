// Claude's MCP entries, written only through its official CLI (polyforge#144,
// #85 85c, AC2; ADR 0006 "Claude 一侧经官方 CLI"): Pi Web never writes
// `~/.claude.json` or `.mcp.json` itself — `claude mcp add-json` drops keys it
// does not know and `remove` is the only way out, so the file stays claude's
// own. Every write is `mcp add-json -s <scope> <name> <json>` or
// `mcp remove -s <scope> <name>`, always with `-s`; a modification is remove
// then add-json (add-json refuses an existing name), and when the add-json
// half fails the previous definition — staged server-side by the caller — is
// written back. The version is probed before any spawn: a version outside the
// recognized range (lib/harness/adapter.ts's CLAUDE_KNOWN_RANGE, claude
// 2.1.x) refuses without spawning. `mcp list` and `mcp get` are never called
// (list health-checks servers, get may print values). The child's output is
// never quoted in an error: the argv carries the entry's literal values, so
// an error says the exit code, never what the child printed.
import { spawn } from "node:child_process";
import { buildClaudeEnv } from "./claude-argv";
import { probeClaude } from "./claude-probe";
import { findClaudeBinary } from "./claude-session";
import { CLAUDE_KNOWN_RANGE, versionInKnownRange } from "./harness/adapter";
import type { McpScope } from "./api-types";

/** claude CLI's own `-s` values. */
export type ClaudeMcpWriteScope = "user" | "project" | "local";

/** What can go wrong; `error` is a fixed English sentence, never child output. */
export type ClaudeMcpWriteOutcome =
  | { ok: true }
  | { ok: false; reason: "invalid-request" | "claude-missing" | "version-unknown" | "version-unrecognized" | "remove-failed" | "add-failed" | "rollback-failed"; error: string; rolledBack?: boolean };

/** The context every write takes; tests inject `binary`, `version` and `env`. */
export interface ClaudeMcpWriteContext {
  /** The project folder: required for the project and local scopes (claude keys local by it). */
  cwd?: string;
  env?: NodeJS.ProcessEnv;
  /** The claude binary; default `findClaudeBinary()`. */
  binary?: string;
  /** A version already probed (tests): the gate checks it without spawning. */
  version?: string;
  /** How long the CLI may run; default 30 s. */
  timeoutMs?: number;
}

/** How long one claude MCP command may run. */
const WRITE_TIMEOUT_MS = 30_000;

/** The unified scope as claude's own `-s` spells it: the global group is claude's user. */
export function claudeMcpWriteScope(scope: McpScope): ClaudeMcpWriteScope | undefined {
  if (scope === "global") return "user";
  if (scope === "project" || scope === "local") return scope;
  return undefined;
}

/** One claude MCP command, run to its end: its exit code, or why it never ran. */
type RunOutcome =
  | { ran: true; code: number | null }
  | { ran: false; outcome: ClaudeMcpWriteOutcome };

function runClaude(argv: readonly string[], context: ClaudeMcpWriteContext): Promise<RunOutcome> {
  return new Promise((resolve) => {
    // The same environment an interactive claude session of Pi Web runs with
    // (#397): the config dir claude resolves its state file by is the one the
    // read resolved it by, so a write lands where the listing reads.
    const child = spawn(context.binary as string, argv, {
      cwd: context.cwd,
      env: buildClaudeEnv(context.env ?? process.env) as NodeJS.ProcessEnv,
      stdio: ["ignore", "ignore", "ignore"] as const,
    });
    let settled = false;
    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      child.kill("SIGKILL");
      resolve({ ran: true, code: null });
    }, context.timeoutMs ?? WRITE_TIMEOUT_MS);
    timer.unref?.();
    child.on("error", () => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve({ ran: false, outcome: { ok: false, reason: "claude-missing", error: `claude could not be started: ${context.binary}` } });
    });
    child.on("close", (code) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve({ ran: true, code });
    });
  });
}

/**
 * The version gate every write passes first (ADR 0006): a version claude's
 * own probe reported, inside the recognized range — or a version the caller
 * already probed — else the write refuses without spawning anything.
 */
async function gate(context: ClaudeMcpWriteContext): Promise<{ binary: string; version: string } | ClaudeMcpWriteOutcome> {
  const binary = context.binary ?? findClaudeBinary();
  if (!binary) {
    return { ok: false, reason: "claude-missing", error: "claude was not found on PATH" };
  }
  let version: string | null;
  if (context.version !== undefined) {
    version = context.version;
  } else {
    const probed = await probeClaude(binary);
    if ("error" in probed) return { ok: false, reason: "claude-missing", error: `claude's version could not be probed: ${probed.error}` };
    version = probed.version;
  }
  if (version === null || version === "unknown") {
    return { ok: false, reason: "version-unknown", error: "claude did not report a version, so Pi Web writes nothing" };
  }
  if (!versionInKnownRange(version, CLAUDE_KNOWN_RANGE)) {
    return { ok: false, reason: "version-unrecognized", error: `claude ${version} is outside the recognized range ${CLAUDE_KNOWN_RANGE.label}, so Pi Web writes nothing` };
  }
  return { binary, version };
}

/** The gate plus the scope/cwd rule, shared by every write entry point. */
async function prepare(scope: McpScope, context: ClaudeMcpWriteContext): Promise<{ binary: string; claudeScope: ClaudeMcpWriteScope } | ClaudeMcpWriteOutcome> {
  const claudeScope = claudeMcpWriteScope(scope);
  if (claudeScope === undefined) {
    return { ok: false, reason: "invalid-request", error: `"${scope}" is not a scope claude writes` };
  }
  if (claudeScope !== "user" && context.cwd === undefined) {
    return { ok: false, reason: "invalid-request", error: `claude's ${claudeScope} scope needs the project folder it lives in` };
  }
  const gated = await gate(context);
  if ("ok" in gated) return gated;
  return { binary: gated.binary, claudeScope };
}

/**
 * The gate every claude write passes, run without writing anything: the
 * scope/cwd rule and the version probe (r2#3 — a caller writing other
 * harnesses first asks here, so an add to both sides meets the gate before
 * any file is touched).
 */
export async function claudeMcpWriteGate(scope: McpScope, context: ClaudeMcpWriteContext = {}): Promise<ClaudeMcpWriteOutcome> {
  const ready = await prepare(scope, context);
  // prepare's refusal carries `ok: false`; its success shape does not.
  if ("ok" in ready) return ready;
  return { ok: true };
}

/** Adds `name` as `claude mcp add-json -s <scope> <name> <json>` would. */
export async function claudeMcpAddJson(scope: McpScope, name: string, entry: Record<string, unknown>, context: ClaudeMcpWriteContext = {}): Promise<ClaudeMcpWriteOutcome> {
  const ready = await prepare(scope, context);
  if ("ok" in ready) return ready;
  const json = JSON.stringify(entry);
  const run = await runClaude(["mcp", "add-json", "-s", ready.claudeScope, name, json], { ...context, binary: ready.binary });
  if (!run.ran) return run.outcome;
  if (run.code !== 0) {
    return { ok: false, reason: "add-failed", error: `claude mcp add-json exited with code ${run.code ?? "<killed>"}` };
  }
  return { ok: true };
}

/** Removes `name` as `claude mcp remove -s <scope> <name>` would. */
export async function claudeMcpRemove(scope: McpScope, name: string, context: ClaudeMcpWriteContext = {}): Promise<ClaudeMcpWriteOutcome> {
  const ready = await prepare(scope, context);
  if ("ok" in ready) return ready;
  const run = await runClaude(["mcp", "remove", "-s", ready.claudeScope, name], { ...context, binary: ready.binary });
  if (!run.ran) return run.outcome;
  if (run.code !== 0) {
    return { ok: false, reason: "remove-failed", error: `claude mcp remove exited with code ${run.code ?? "<killed>"}` };
  }
  return { ok: true };
}

/**
 * Writes `name` as a modification: remove, then add-json; when the add-json
 * half fails, `previous` (the caller's staged copy of the old definition) is
 * written back, and the outcome says whether that rollback landed.
 */
export async function claudeMcpWriteEntry(
  scope: McpScope,
  name: string,
  entry: Record<string, unknown>,
  context: ClaudeMcpWriteContext & { previous?: Record<string, unknown> } = {},
): Promise<ClaudeMcpWriteOutcome> {
  const ready = await prepare(scope, context);
  if ("ok" in ready) return ready;
  const removal = await runClaude(["mcp", "remove", "-s", ready.claudeScope, name], { ...context, binary: ready.binary });
  if (!removal.ran) return removal.outcome;
  if (removal.code !== 0) {
    return { ok: false, reason: "remove-failed", error: `claude mcp remove exited with code ${removal.code ?? "<killed>"}` };
  }
  const adding = await claudeMcpAddJson(scope, name, entry, { ...context, binary: ready.binary });
  if (adding.ok) return adding;
  if (context.previous === undefined) {
    return { ...adding, reason: "add-failed" };
  }
  const rollback = await claudeMcpAddJson(scope, name, context.previous, { ...context, binary: ready.binary });
  if (rollback.ok) {
    return { ok: false, reason: "add-failed", error: `${adding.error}; the previous definition was written back`, rolledBack: true };
  }
  return { ok: false, reason: "rollback-failed", error: `${adding.error}; writing the previous definition back failed too: ${rollback.error}` };
}
