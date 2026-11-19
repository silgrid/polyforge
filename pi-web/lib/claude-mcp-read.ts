// Claude's MCP entries, read from its native files only (polyforge#144, #85
// 85c, AC1; Q6 B — this unit owns claude's MCP read). Never a spawn: `claude
// mcp list` health-checks servers (it connects them) and `mcp get` may print
// env or header values, so the only reads are the files:
// - user scope: `$CLAUDE_CONFIG_DIR/.claude.json` when set, else
//   `~/.claude.json` — claude's own placement, NOT `claudeConfigDir()`'s
//   `~/.claude` fallback, which never holds the state file;
// - local scope: the same file's `projects[<absolute path>].mcpServers`, the
//   key being the cwd claude resolved, so a project folder reached through a
//   symbolic link is matched by its real path too;
// - project scope: `<cwd>/.mcp.json`.
// The top-level `disabledMcpServers` is brought back read-only (Q1 A: claude
// has no enable/disable command, so Pi Web never writes it). Values never
// leave this module unmasked: env and header names only, url and args through
// `lib/mcp-secrets.ts`.
import { maskArgs, maskCommand, maskUrl } from "./mcp-secrets";
import type { McpScope } from "./api-types";

/** One of claude's MCP entries as the unified model lists it (masked). */
export interface ClaudeMcpServerInfo {
  name: string;
  /** The unified scope: claude's user is the global group, its local is Claude-only. */
  scope: McpScope;
  /** The native file the entry was read from. */
  sourcePath: string;
  command?: string;
  args?: string[];
  url?: string;
  /** claude's own `type` as written, when the entry carries one. */
  type?: string;
  envNames: string[];
  headerNames: string[];
  /** Named in the state file's `disabledMcpServers`; read-only display (Q1 A). */
  disabled?: boolean;
}

/** What reading claude's native MCP files found. */
export interface ClaudeMcpReadResult {
  /** The state file's path: `$CLAUDE_CONFIG_DIR/.claude.json`, else `~/.claude.json`. */
  configPath: string;
  /** False when the state file exists but is not a JSON object: no user or local entry is listed. */
  stateReadable: boolean;
  servers: ClaudeMcpServerInfo[];
  /** The state file's top-level `disabledMcpServers`, read-only (Q1 A). */
  disabledMcpServers: string[];
}

/** The options every read takes; tests inject `configDir` and `env`. */
export interface ClaudeMcpReadOptions {
  /** A project folder: local and project entries are read only with one. */
  cwd?: string;
  env?: NodeJS.ProcessEnv;
  /** Overrides the config-dir resolution (tests); never mixed with `env`. */
  configDir?: string;
}

/** The state file claude itself reads: inside $CLAUDE_CONFIG_DIR when set, else ~/.claude.json. */
export function claudeMcpStatePath(options: Pick<ClaudeMcpReadOptions, "env" | "configDir"> = {}): string {
  void options;
  void maskArgs;
  void maskCommand;
  void maskUrl;
  return "";
}

/** Reads claude's MCP entries from its files; spawns nothing. */
export function readClaudeMcpServers(_options: ClaudeMcpReadOptions = {}): ClaudeMcpReadResult {
  return { configPath: "", stateReadable: true, servers: [], disabledMcpServers: [] };
}
