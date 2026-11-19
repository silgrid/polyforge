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

/**
 * The generic core (Q8 A): merges `entries` by (scope, name) over
 * `harnesses`. A harness that supports a scope but holds no entry of a name
 * the other side holds is named in that row's `missing`; a field both sides
 * define whose values differ is the row's one inconsistency (risk 8: fields
 * one side alone defines are never differences); the same name in another
 * scope stays its own row, hinted in `otherScopes` (Q2 A).
 */
export function mergeUnifiedEntries<H extends string>(
  harnesses: readonly UnifiedMergeHarness<H>[],
  entries: readonly UnifiedMergeEntry<H>[],
): UnifiedMergeRow<H>[] {
  const rows = new Map<string, UnifiedMergeRow<H>>();
  const rowKey = (scope: string, name: string) => `${scope}\0${name}`;
  for (const entry of entries) {
    const key = rowKey(entry.scope, entry.name);
    let row = rows.get(key);
    if (!row) {
      row = { scope: entry.scope, name: entry.name, entries: {}, missing: [], inconsistent: [], otherScopes: [] };
      rows.set(key, row);
    }
    row.entries[entry.harness] = entry;
  }
  for (const row of rows.values()) {
    for (const harness of harnesses) {
      if (row.entries[harness.harness] === undefined && harness.scopes.includes(row.scope)) {
        row.missing.push(harness.harness);
      }
    }
    const present = harnesses.filter((harness) => row.entries[harness.harness] !== undefined);
    const fieldNames = [...new Set(present.flatMap((harness) => Object.keys(row.entries[harness.harness]!.fields)))];
    for (const field of fieldNames) {
      if (present.some((harness) => !(field in row.entries[harness.harness]!.fields))) continue;
      const values: Partial<Record<H, unknown>> = {};
      for (const harness of present) values[harness.harness] = row.entries[harness.harness]!.fields[field];
      const distinct = [...present.map((harness) => JSON.stringify(values[harness.harness]))];
      if (new Set(distinct).size > 1) row.inconsistent.push({ field, values });
    }
  }
  for (const row of rows.values()) {
    for (const other of rows.values()) {
      if (other !== row && other.name === row.name && !row.otherScopes.includes(other.scope)) {
        row.otherScopes.push(other.scope);
      }
    }
  }
  return [...rows.values()];
}

/** pi's entry as comparable fields: command, args, env names — masked already by its reader. */
export function unifiedMcpPiFields(server: McpServerInfo): Record<string, unknown> {
  const fields: Record<string, unknown> = {};
  if (server.command !== undefined) fields.command = server.command;
  if (server.args !== undefined) fields.args = server.args;
  if (server.url !== undefined) fields.url = server.url;
  if (server.envNames.length > 0) fields.envNames = server.envNames;
  return fields;
}

/** claude's entry as comparable fields: command, args, env names — masked already by its reader. */
export function unifiedMcpClaudeFields(server: ClaudeMcpServerInfo): Record<string, unknown> {
  const fields: Record<string, unknown> = {};
  if (server.command !== undefined) fields.command = server.command;
  if (server.args !== undefined) fields.args = server.args;
  if (server.url !== undefined) fields.url = server.url;
  if (server.envNames.length > 0) fields.envNames = server.envNames;
  return fields;
}

/** A field's value as the row's inconsistency note shows it. */
function fieldText(field: string, value: unknown): string {
  if (Array.isArray(value)) {
    return value.every((item) => typeof item === "string")
      ? (value as string[]).join(field === "args" ? " " : ", ")
      : JSON.stringify(value);
  }
  return typeof value === "string" ? value : JSON.stringify(value);
}

/** The harnesses of the unified MCP list: pi holds no local scope (Q2/Q3). */
const MCP_HARNESSES = [
  { harness: "pi" as const, scopes: ["global", "project"] as const },
  { harness: "claude" as const, scopes: ["global", "project", "local"] as const },
];

/**
 * The unified rows of the MCP list: pi's and claude's entries merged by
 * (scope, name) (Q2 A), each row's `missing` naming the harness a change
 * would newly reach, its `inconsistent` the shared fields that differ (the
 * row the panel marks and offers to 统一成当前值), and `otherScopes` the
 * same name's other groups (「另在 X 组」).
 */
export function unifiedMcpRows(
  piServers: readonly McpServerInfo[],
  claudeServers: readonly ClaudeMcpServerInfo[],
): McpUnifiedRow[] {
  const entries = [
    ...piServers.map((server) => ({
      harness: "pi" as const,
      scope: server.scope,
      name: server.name,
      fields: unifiedMcpPiFields(server),
    })),
    ...claudeServers.map((server) => ({
      harness: "claude" as const,
      scope: server.scope,
      name: server.name,
      fields: unifiedMcpClaudeFields(server),
    })),
  ];
  return mergeUnifiedEntries(MCP_HARNESSES, entries).map((row) => ({
    scope: row.scope as McpUnifiedRow["scope"],
    name: row.name,
    pi: row.entries.pi !== undefined,
    claude: row.entries.claude !== undefined,
    missing: row.missing,
    inconsistent: row.inconsistent.map((diff) => ({
      field: diff.field,
      values: Object.fromEntries(Object.entries(diff.values).map(([harness, value]) => [harness, fieldText(diff.field, value)])),
    })),
    otherScopes: row.otherScopes as McpUnifiedRow["otherScopes"],
  }));
}
