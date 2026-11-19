// The unified merge of two harnesses' config entries (polyforge#144, #85 85c,
// AC1/AC3; Q2 A one row per (unified scope, name), Q8 A a generic core 85d
// reuses): the core knows nothing of MCP — it merges entries by (scope, name)
// over comparable fields, both sides' shared fields only, so a field one side
// alone has (pi's exposure, claude's type) never reads as a difference (the
// analysis's risk 8: "同名同作用域" is not "同一个服务器" until the shared
// fields agree). The MCP application maps pi's global/project and claude's
// user/project/local onto the unified scopes and formats the field values as
// the panel shows them, masked as each side's reader already masked them.
import type { ClaudeMcpServerInfo, McpServerInfo, McpUnifiedRow } from "./api-types";

/** One harness the merge knows: its id and the unified scopes it can hold. */
export interface UnifiedMergeHarness<H extends string> {
  harness: H;
  /** The unified scopes this harness holds entries in. */
  scopes: readonly string[];
}

/** One entry as the merge sees it: harness, (scope, name) and comparable fields. */
export interface UnifiedMergeEntry<H extends string> {
  harness: H;
  scope: string;
  name: string;
  /** The comparable fields; a field only one side defines is never a difference. */
  fields: Record<string, unknown>;
}

/** One shared field whose values differ between the harnesses that hold the row. */
export interface UnifiedFieldDiff<H extends string> {
  field: string;
  /** Each present harness's value, by harness id. */
  values: Partial<Record<H, unknown>>;
}

/** One merged row: the (scope, name) every harness's entry of it collapses into. */
export interface UnifiedMergeRow<H extends string> {
  scope: string;
  name: string;
  /** Each harness's entry, by harness id; absent when it holds none. */
  entries: Partial<Record<H, UnifiedMergeEntry<H>>>;
  /** Harnesses that support this scope but hold no entry of this name. */
  missing: H[];
  /** Shared fields whose values differ. */
  inconsistent: UnifiedFieldDiff<H>[];
  /** Other scopes holding the same name (「另在 X 组」). */
  otherScopes: string[];
}

/** The generic core (Q8 A): merges `entries` by (scope, name) over `harnesses`. */
export function mergeUnifiedEntries<H extends string>(
  _harnesses: readonly UnifiedMergeHarness<H>[],
  _entries: readonly UnifiedMergeEntry<H>[],
): UnifiedMergeRow<H>[] {
  return [];
}

/** pi's entry as comparable fields: command, args, url, env names (masked already). */
export function unifiedMcpPiFields(_server: McpServerInfo): Record<string, unknown> {
  return {};
}

/** claude's entry as comparable fields: command, args, url, env names (masked already). */
export function unifiedMcpClaudeFields(_server: ClaudeMcpServerInfo): Record<string, unknown> {
  return {};
}

/** The unified rows of the MCP list: pi's and claude's entries merged (Q2 A). */
export function unifiedMcpRows(
  _piServers: readonly McpServerInfo[],
  _claudeServers: readonly ClaudeMcpServerInfo[],
): McpUnifiedRow[] {
  return [];
}
