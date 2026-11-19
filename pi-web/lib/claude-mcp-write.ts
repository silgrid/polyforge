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

/** The unified scope as claude's own `-s` spells it: the global group is claude's user. */
export function claudeMcpWriteScope(scope: McpScope): ClaudeMcpWriteScope | undefined {
  void scope;
  return undefined;
}

/** Adds `name` as `claude mcp add-json -s <scope> <name> <json>` would. */
export async function claudeMcpAddJson(_scope: McpScope, _name: string, _entry: Record<string, unknown>, _context: ClaudeMcpWriteContext = {}): Promise<ClaudeMcpWriteOutcome> {
  return { ok: false, reason: "invalid-request", error: "not implemented" };
}

/** Removes `name` as `claude mcp remove -s <scope> <name>` would. */
export async function claudeMcpRemove(_scope: McpScope, _name: string, _context: ClaudeMcpWriteContext = {}): Promise<ClaudeMcpWriteOutcome> {
  return { ok: false, reason: "invalid-request", error: "not implemented" };
}

/**
 * Writes `name` as a modification: remove, then add-json; when the add-json
 * half fails, `previous` (the caller's staged copy of the old definition) is
 * written back, and the outcome says whether that rollback landed.
 */
export async function claudeMcpWriteEntry(_scope: McpScope, _name: string, _entry: Record<string, unknown>, _context: ClaudeMcpWriteContext & { previous?: Record<string, unknown> } = {}): Promise<ClaudeMcpWriteOutcome> {
  return { ok: false, reason: "invalid-request", error: "not implemented" };
}

// Referenced so the skeleton keeps the imports the implementation needs.
void buildClaudeEnv;
void probeClaude;
void findClaudeBinary;
void CLAUDE_KNOWN_RANGE;
void versionInKnownRange;
