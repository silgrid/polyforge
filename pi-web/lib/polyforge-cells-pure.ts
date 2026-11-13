/**
 * Client-safe Cell (ProfileCell) types and sanitizers for Settings ›
 * Polyforge. One pf daemon is a Host serving one Cell per
 * AIHub profile; the Cell registry is read through `pf --json cell list`
 * (the daemon's GET /profiles) — never by parsing config files. Multi-Cell
 * writes are gated on the protocol capabilities `pf --json version`
 * reports, never on a bare version compare.
 */

/** The protocol capabilities a multi-Cell write needs (pf `--json version`). */
export const POLYFORGE_CELL_CAPABILITIES = ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] as const;

/** The upgrade hint shown when a multi-Cell capability is missing. */
export const POLYFORGE_CELLS_UPGRADE_HINT =
  "this pf does not speak the multi-Cell protocol (cells-v1, config-host-cell-v1, cell-lifecycle-v1) — upgrade pf to manage Cells";

/** The default Cell's reserved profile name. */
export const POLYFORGE_DEFAULT_PROFILE = "default";

const PROFILE_PATTERN = /^[a-z0-9][a-z0-9-]{0,31}$/;

/** A profile name a request may carry (the pf syntax; `default` included). */
export function isPolyforgeProfileName(value: unknown): value is string {
  return typeof value === "string" && PROFILE_PATTERN.test(value);
}

/** A name a new Cell may take (`default` is reserved). */
export function isNewCellName(value: unknown): value is string {
  return isPolyforgeProfileName(value) && value !== POLYFORGE_DEFAULT_PROFILE;
}

/** Whether every multi-Cell capability is present. */
export function hasPolyforgeCellCapabilities(capabilities: readonly string[] | null | undefined): boolean {
  if (!Array.isArray(capabilities)) return false;
  return POLYFORGE_CELL_CAPABILITIES.every((cap) => capabilities.includes(cap));
}

/** The config scope one read/write addresses: the Host (`--host`, cli.toml's
 *  Host-only keys) or one Cell (`--profile NAME`, `default` included).
 * there is no unscoped form — an unscoped pf call resolves
 *  its Cell from the calling process's PF_PROFILE and cwd, i.e. from pi-web's
 *  server process rather than the page, so every Settings › Polyforge pf call
 *  names its scope. */
export type PolyforgeConfigScope = { host: true } | { profile: string };

/** The default Cell's scope (its file is cli.toml). */
export function polyforgeDefaultScope(): PolyforgeConfigScope {
  return { profile: POLYFORGE_DEFAULT_PROFILE };
}

/** the file one Cell's keys live in — the default Cell's is
 *  cli.toml, a named Cell's cli-<profile>.toml (pf's cellcfg.CellFileName).
 *  Its name heads that Cell's file card and zones, never translated. */
export function polyforgeCellFileName(profile: string): string {
  return profile === POLYFORGE_DEFAULT_PROFILE ? "cli.toml" : `cli-${profile}.toml`;
}

/** Whether a scope is the default Cell's. */
export function isPolyforgeDefaultScope(scope: PolyforgeConfigScope): boolean {
  return "profile" in scope && scope.profile === POLYFORGE_DEFAULT_PROFILE;
}

/** A named Cell's profile (never `default`), or null for the Host or the default Cell. */
export function polyforgeNamedCell(scope: PolyforgeConfigScope): string | null {
  return "profile" in scope && scope.profile !== POLYFORGE_DEFAULT_PROFILE ? scope.profile : null;
}

/** argv placed before the verb (`--profile NAME`) for a Cell scope. */
export function polyforgeScopePrefix(scope: PolyforgeConfigScope): string[] {
  return "profile" in scope ? ["--profile", scope.profile] : [];
}

/** argv appended to `pf config …` (`--host`) for the Host scope. */
export function polyforgeScopeSuffix(scope: PolyforgeConfigScope): string[] {
  return "host" in scope ? ["--host"] : [];
}

/** Parse a request's scope fields ({host:true} | {profile}); a bad profile
 *  throws. a request with neither field addresses the default
 *  Cell — never the Cell pf would resolve from the server's PF_PROFILE or
 *  cwd. */
export function parsePolyforgeScope(body: { host?: unknown; profile?: unknown }): PolyforgeConfigScope {
  if (body.host === true) {
    if (body.profile !== undefined && body.profile !== null && body.profile !== "") {
      throw new Error("host and profile exclude each other");
    }
    return { host: true };
  }
  if (body.profile === undefined || body.profile === null || body.profile === "") return polyforgeDefaultScope();
  if (!isPolyforgeProfileName(body.profile)) throw new Error("profile must match [a-z0-9][a-z0-9-]{0,31}");
  return { profile: body.profile };
}

/** The argv that pins one pf call to a scope. */
export interface PolyforgeScopeArgv {
  /** Before the verb: `--profile NAME`. */
  prefix: string[];
  /** After `pf config <subcommand>`: `--host`. */
  suffix: string[];
}

/** the argv one pf call needs to address `scope` on a pf that
 *  reports `capabilities` (`pf --json version`), or null when that pf cannot
 *  address it. A multi-Cell pf always gets the explicit scope — the default
 *  Cell `--profile default` too — so its profile resolver (flag, PF_PROFILE,
 *  cwd, default) never sees an unscoped call. A pf without the multi-Cell
 *  protocol has one config file (cli.toml, the default Cell's) and no
 *  resolver: the default scope needs no flag there (the flag does not exist
 *  on it), and a Host or named-Cell scope cannot be addressed at all. That
 *  pre-Cell branch is deleted once the configuration panel's floor is a pf
 *  release that reports the multi-Cell capabilities. Pure. */
export function polyforgeScopeArgv(scope: PolyforgeConfigScope, capabilities: readonly string[]): PolyforgeScopeArgv | null {
  if (isPolyforgeDefaultScope(scope)) return polyforgeDefaultScopeArgv(capabilities);
  return hasPolyforgeCellCapabilities(capabilities) ? { prefix: polyforgeScopePrefix(scope), suffix: polyforgeScopeSuffix(scope) } : null;
}

/** The default Cell's argv, addressable on every pf (`polyforgeScopeArgv`):
 *  `--profile default` on a multi-Cell pf, no flag on a pre-Cell one. */
export function polyforgeDefaultScopeArgv(capabilities: readonly string[]): PolyforgeScopeArgv {
  return { prefix: hasPolyforgeCellCapabilities(capabilities) ? polyforgeScopePrefix(polyforgeDefaultScope()) : [], suffix: [] };
}

/** One Cell row, sanitized (no credential is ever carried by pf either). */
export interface PolyforgeCell {
  profile: string;
  state: string;
  valid: boolean;
  reason?: string;
  server?: string;
  workspaces: string[];
  dataDir?: string;
  machineId?: string;
  heartbeat?: string;
  executionDisabled?: string;
  cellMaxConcurrentJobs?: number;
  maxConcurrent?: number;
  running: number;
  pending: number;
  draining: boolean;
  pendingChange?: string;
}

function str(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}

function num(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

/** Sanitize `pf --json cell list` (GET /profiles) to the panel's rows. */
export function sanitizePolyforgeCells(machine: unknown): PolyforgeCell[] | null {
  if (typeof machine !== "object" || machine === null) return null;
  const profiles = (machine as { profiles?: unknown }).profiles;
  if (!Array.isArray(profiles)) return null;
  const out: PolyforgeCell[] = [];
  for (const raw of profiles) {
    if (typeof raw !== "object" || raw === null) continue;
    const p = raw as Record<string, unknown>;
    if (!isPolyforgeProfileName(p.profile)) continue;
    const cell: PolyforgeCell = {
      profile: p.profile,
      state: str(p.state) ?? "unknown",
      valid: p.valid === true,
      workspaces: Array.isArray(p.workspaces) ? p.workspaces.filter((w): w is string => typeof w === "string") : [],
      running: num(p.running) ?? 0,
      pending: num(p.pending) ?? 0,
      draining: p.draining === true,
    };
    const optional: Array<[keyof PolyforgeCell, unknown]> = [
      ["reason", str(p.reason)], ["server", str(p.server)], ["dataDir", str(p.data_dir)],
      ["machineId", str(p.machine_id)], ["heartbeat", str(p.heartbeat)],
      ["executionDisabled", str(p.execution_disabled)], ["pendingChange", str(p.pending_change)],
      ["cellMaxConcurrentJobs", num(p.cell_max_concurrent_jobs)], ["maxConcurrent", num(p.max_concurrent)],
    ];
    for (const [key, value] of optional) {
      if (value !== undefined) (cell as unknown as Record<string, unknown>)[key] = value;
    }
    out.push(cell);
  }
  return out;
}

/** Sanitize `pf --json version`'s capabilities (absent on an older pf → []). */
export function sanitizePolyforgeCapabilities(machine: unknown): string[] {
  if (typeof machine !== "object" || machine === null) return [];
  const caps = (machine as { capabilities?: unknown }).capabilities;
  return Array.isArray(caps) ? caps.filter((c): c is string => typeof c === "string") : [];
}

/** the one state a Cell's list entry shows, in pf's own
 *  facts: an invalid or non-serving Cell is degraded (a removed one says so),
 *  a draining Cell waits for its pending connection change to apply, a Cell
 *  pf closed for execution (no workspace mapped, …) is execution-disabled,
 *  otherwise it serves. The first match wins, so the worst state shows. */
export type PolyforgeCellListState = "serving" | "pending-restart" | "degraded" | "execution-disabled" | "removed";

export function polyforgeCellListState(cell: PolyforgeCell): PolyforgeCellListState {
  if (!cell.valid) return cell.state === "removed" ? "removed" : "degraded";
  if (cell.state !== "serving") return "degraded";
  if (cell.draining) return "pending-restart";
  if (cell.executionDisabled) return "execution-disabled";
  return "serving";
}

/** The Cell whose execution-disabled reason / draining state should be shown. */
export function polyforgeCellNotice(cell: PolyforgeCell): string | null {
  if (!cell.valid) return cell.reason ?? cell.state;
  if (cell.draining) return cell.pendingChange ?? "draining for a connection change";
  if (cell.executionDisabled) return `execution disabled: ${cell.executionDisabled}`;
  if (cell.state === "degraded") return cell.reason ?? "degraded";
  return null;
}
