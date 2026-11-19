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
import { existsSync, readFileSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { McpScope } from "./api-types";
import { maskArgs, maskCommand, maskUrl } from "./mcp-secrets";

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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** The state file claude itself reads: inside $CLAUDE_CONFIG_DIR when set, else ~/.claude.json. */
export function claudeMcpStatePath(options: Pick<ClaudeMcpReadOptions, "env" | "configDir"> = {}): string {
  if (options.configDir !== undefined) return join(options.configDir, ".claude.json");
  const inherited = options.env?.CLAUDE_CONFIG_DIR?.trim();
  return inherited ? join(inherited, ".claude.json") : join(homedir(), ".claude.json");
}

/** A JSON object read whole, or null when the file is missing, unreadable or not an object. */
function readJsonObject(path: string): Record<string, unknown> | null {
  if (!existsSync(path)) return null;
  try {
    const parsed: unknown = JSON.parse(readFileSync(path, "utf8"));
    return isRecord(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

/** The key names of a headers/env block, values never read. */
function keyNames(value: unknown): string[] {
  return isRecord(value) ? Object.keys(value).sort() : [];
}

/** One entry as the unified model lists it: own property only, masked, `undefined` fields absent. */
function entryInfo(
  name: string,
  scope: McpScope,
  sourcePath: string,
  entry: unknown,
  disabled: boolean,
): ClaudeMcpServerInfo {
  const info: ClaudeMcpServerInfo = { name, scope, sourcePath, envNames: [], headerNames: [] };
  if (disabled) info.disabled = true;
  if (!isRecord(entry)) return info;
  if (typeof entry.command === "string") info.command = maskCommand(entry.command).value;
  if (Array.isArray(entry.args) && entry.args.every((arg) => typeof arg === "string")) {
    info.args = maskArgs(entry.args as string[]).args;
  }
  if (typeof entry.url === "string") info.url = maskUrl(entry.url).value;
  if (typeof entry.type === "string") info.type = entry.type;
  info.envNames = keyNames(entry.env);
  info.headerNames = keyNames(entry.headers);
  return info;
}

/** Every own `mcpServers` entry of `record`, in file order, as the model lists it. */
function serverEntries(
  servers: Record<string, unknown> | undefined,
  scope: McpScope,
  sourcePath: string,
  disabledNames: ReadonlySet<string>,
): ClaudeMcpServerInfo[] {
  if (!servers) return [];
  return Object.keys(servers)
    .filter((name) => Object.hasOwn(servers, name))
    .map((name) => entryInfo(name, scope, sourcePath, servers[name], disabledNames.has(name)));
}

/** Reads claude's MCP entries from its files; spawns nothing. */
export function readClaudeMcpServers(options: ClaudeMcpReadOptions = {}): ClaudeMcpReadResult {
  const configPath = claudeMcpStatePath(options);
  const disabledNames = new Set<string>();
  const result: ClaudeMcpReadResult = { configPath, stateReadable: true, servers: [], disabledMcpServers: [] };
  const state = readJsonObject(configPath);
  if (state === null) {
    // A missing file is no error; one that exists but is not a JSON object lists none of it.
    result.stateReadable = !existsSync(configPath);
  } else {
    if (Array.isArray(state.disabledMcpServers)) {
      result.disabledMcpServers = state.disabledMcpServers.filter((name): name is string => typeof name === "string");
      for (const name of result.disabledMcpServers) disabledNames.add(name);
    }
    if (isRecord(state.mcpServers)) {
      result.servers.push(...serverEntries(state.mcpServers, "global", configPath, disabledNames));
    }
    const projects = isRecord(state.projects) ? state.projects : undefined;
    if (projects && options.cwd !== undefined) {
      // claude keys the local scope by the absolute path it resolved: a folder
      // reached through a symbolic link is keyed by its real path, so both the
      // given cwd and its real path are matched.
      const keys = new Set([options.cwd]);
      try {
        const real = realpathSync(options.cwd);
        if (real !== options.cwd) keys.add(real);
      } catch {
        // A cwd that resolves to nothing has no local entries.
      }
      for (const key of keys) {
        const project = projects[key];
        if (!isRecord(project) || !isRecord(project.mcpServers)) continue;
        result.servers.push(...serverEntries(project.mcpServers, "local", configPath, disabledNames));
      }
    }
  }
  if (options.cwd !== undefined) {
    const dotMcpPath = join(options.cwd, ".mcp.json");
    const dotMcp = readJsonObject(dotMcpPath);
    if (dotMcp && isRecord(dotMcp.mcpServers)) {
      result.servers.push(...serverEntries(dotMcp.mcpServers, "project", dotMcpPath, disabledNames));
    }
  }
  return result;
}
