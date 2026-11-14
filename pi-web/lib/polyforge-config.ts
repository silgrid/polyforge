import { spawn } from "node:child_process";
import { polyforgeNamedCell, type PolyforgeConfigScope } from "./polyforge-cells-pure";
import { pinPolyforgeScope } from "./polyforge-config-scope";
import { isPolyforgeVersionBelowMinimum } from "./polyforge-detect";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION, type PolyforgeConfigSchema } from "./polyforge-config-schema";
import {
  probePolyforgeVersion,
  type PolyforgeOpsStatus,
} from "./polyforge-status";

/**
 * Server-side reads for the Settings › Polyforge CONFIGURATION panel.
 * Every fact is a `pf` subprocess read — the panel never
 * parses cli.toml or `.polyforge.yaml` itself. The sanitizers are the only
 * passport off this module: an api_key value is never present on the wire,
 * and a class pf reports but the panel has never seen passes through
 * verbatim rather than being remapped.
 */

/** The configuration panel's own capability floor (plan decision 1): the
 *  next release after the v2.20.5 baseline. The dock's older floor is
 *  unchanged; this one gates only the new config verbs. */
export const POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION = "2.20.6";

/** The summarize/memory_policy floor (`2.20.21`), defined
 *  once in the browser-safe schema module and re-exported for the server
 *  read and write paths; the rest of the panel keeps the panel floor above. */
export { POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION };

const READ_TIMEOUT_MS = 15000;

export interface PolyforgeConfigDaemonValue {
  value: string | number | boolean | null;
  max_configured?: number;
}

export interface PolyforgeConfigItem {
  key: string;
  value?: unknown;
  set?: boolean;
  source?: string;
  class: string;
  schema?: PolyforgeConfigSchema;
  section?: boolean;
  daemon?: PolyforgeConfigDaemonValue;
  stale_reason?: string;
}

export interface PolyforgeConfigStep {
  project?: string;
  step: string;
  declared: boolean;
  models: string[] | null;
  /** `file` | `default`; absent from an older pf. */
  source?: string;
  /** pf's reload class of the step key (`live`); absent from an older pf. */
  class?: string;
}

export interface PolyforgeMemoryPolicyEntry {
  record: string[];
  skip: string[];
  declared: boolean;
}

/** `pf config list`'s memory_policy object (read-only):
 *  workspace path → entry (`default` or a project of that path) → its lists.
 *  `default` carries its effective lists (declared, else built-in); a
 *  project's are its own (appended to default's at a claim). */
export type PolyforgeMemoryPolicy = Record<string, Record<string, PolyforgeMemoryPolicyEntry>>;

export interface PolyforgeConfigReload {
  pending: string[];
  overridden: string[];
  maxConfigured?: number;
}

export interface PolyforgeConfigRead {
  pfPresent: boolean;
  pfVersion: string | null;
  capable: boolean;
  reason?: string;
  file?: { sha256: string | null; exists: boolean };
  items: PolyforgeConfigItem[];
  mapped: boolean;
  daemonReachable?: boolean;
  reload?: PolyforgeConfigReload;
  steps?: PolyforgeConfigStep[];
  steps_complete?: boolean;
  workspaceFile?: { sha256: string | null; exists: boolean };
  memoryPolicy?: PolyforgeMemoryPolicy;
  /** Why the summarize rows are hidden (pf below the summarize floor). */
  summarizeReason?: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function finite(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function runPf(argv: string[]): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn(polyforgePfBin(), argv, { shell: false, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      reject(new Error("pf did not answer in time"));
    }, READ_TIMEOUT_MS);
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => { stdout += chunk; });
    child.stderr.on("data", (chunk: string) => { stderr += chunk; });
    child.on("error", (error) => {
      clearTimeout(timer);
      reject(error instanceof Error ? error : new Error("polyforge is unavailable"));
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (code === 0) resolve(stdout);
      else reject(new Error(stderr.trim().slice(0, 512) || `pf exited ${code ?? 1}`));
    });
  });
}

async function runPfJson(argv: string[]): Promise<unknown> {
  return JSON.parse(await runPf(argv));
}

/** One list item, sanitized. api_key keeps set/source/class/schema only. */
export function sanitizeConfigItem(raw: unknown): PolyforgeConfigItem | null {
  if (!isRecord(raw)) return null;
  const key = str(raw.key);
  if (!key) return null;
  const item: PolyforgeConfigItem = { key, class: str(raw.class) };
  const source = str(raw.source);
  if (source) item.source = source;
  if (raw.section === true) item.section = true;
  if (isRecord(raw.schema) && typeof raw.schema.kind === "string") {
    item.schema = raw.schema as PolyforgeConfigSchema;
  }
  if (key === "api_key") {
    item.set = raw.set === true;
    return item;
  }
  if (raw.value !== undefined) item.value = raw.value;
  if (raw.set === true) item.set = true;
  return item;
}

/** The list document's shape gate: the CAS hash plus class/schema on every
 *  item. A newer version with the old shape is still not capable. */
export function hasConfigListShape(doc: unknown): doc is { file: { sha256: string; exists?: boolean }; items: Array<Record<string, unknown>> } {
  if (!isRecord(doc) || !isRecord(doc.file) || typeof doc.file.sha256 !== "string") return false;
  if (!Array.isArray(doc.items) || doc.items.length === 0) return false;
  return doc.items.every((item) => isRecord(item) && typeof item.key === "string" && typeof item.class === "string");
}

function sanitizeOps(raw: unknown): { ops: PolyforgeOpsStatus | null; reload?: PolyforgeConfigReload } {
  if (!isRecord(raw)) return { ops: null };
  const queue = isRecord(raw.queue) ? raw.queue : {};
  const reloadRaw = isRecord(raw.reload) ? raw.reload : {};
  const pending = Array.isArray(reloadRaw.pending) ? reloadRaw.pending.filter((v): v is string => typeof v === "string") : [];
  const overridden = Array.isArray(reloadRaw.overridden) ? reloadRaw.overridden.filter((v): v is string => typeof v === "string") : [];
  const maxConfigured = finite(queue.max_configured);
  const jobs: unknown[] = [];
  return {
    ops: {
      queue: { running: finite(queue.running) ?? 0, max: finite(queue.max_concurrent) ?? 0 },
      jobs: jobs as PolyforgeOpsStatus["jobs"],
    },
    reload: {
      pending,
      overridden,
      ...(maxConfigured !== undefined ? { maxConfigured } : {}),
    },
  };
}

/** Pure stale classification (plan decision 7): pending restart, an explicit
 *  daemon-flag override, a temporary `pf ops max` override, or not reloaded. */
export function classifyStaleReason(
  key: string,
  fileValue: unknown,
  daemonValue: unknown,
  reload: PolyforgeConfigReload | undefined,
  maxConfigured?: number,
): string | undefined {
  if (fileValue === undefined || daemonValue === undefined) return undefined;
  if (String(fileValue) === String(daemonValue)) return undefined;
  if (reload?.pending.includes(key)) return "pending restart";
  if (reload?.overridden.includes(key)) return "daemon flag override";
  if (key === "max_concurrent_jobs" && maxConfigured !== undefined && daemonValue !== undefined && Number(maxConfigured) !== Number(daemonValue)) {
    return "pf ops max override";
  }
  return "not reloaded";
}

function daemonValueFor(key: string, doc: Record<string, unknown>): { value: string | number | null; maxConfigured?: number } | undefined {
  const queue = isRecord(doc.queue) ? doc.queue : {};
  const supervision = isRecord(doc.supervision) ? doc.supervision : {};
  if (key === "max_concurrent_jobs") {
    const value = finite(queue.max_concurrent);
    if (value === undefined) return undefined;
    const maxConfigured = finite(queue.max_configured);
    return maxConfigured !== undefined ? { value, maxConfigured } : { value };
  }
  if (key === "step_timeout" && typeof supervision.step_timeout === "string") return { value: supervision.step_timeout };
  if (key === "stall_window" && typeof supervision.stall_window === "string") return { value: supervision.stall_window };
  return undefined;
}

function stringList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === "string") : [];
}

function sanitizePolicyEntry(raw: unknown): PolyforgeMemoryPolicyEntry {
  const entry = isRecord(raw) ? raw : {};
  return { record: stringList(entry.record), skip: stringList(entry.skip), declared: entry.declared === true };
}

/** `pf config list`'s memory_policy object → the read shape (path →
 *  entry → lists); null when pf reported none (a pf older than the
 *  summarize floor). */
export function sanitizeMemoryPolicy(raw: unknown): PolyforgeMemoryPolicy | null {
  if (!isRecord(raw)) return null;
  const out: PolyforgeMemoryPolicy = {};
  for (const [path, block] of Object.entries(raw)) {
    if (!path || !isRecord(block)) continue;
    const entries: Record<string, PolyforgeMemoryPolicyEntry> = {};
    for (const [entry, value] of Object.entries(block)) {
      if (isRecord(value)) entries[entry] = sanitizePolicyEntry(value);
    }
    out[path] = entries;
  }
  return out;
}

/** One `steps.<project>.<id>` entry of pf's workspace read, sanitized. */
export function sanitizeStepEntry(project: string | undefined, step: string, entry: Record<string, unknown>): PolyforgeConfigStep {
  const out: PolyforgeConfigStep = {
    ...(project !== undefined ? { project } : {}),
    step,
    declared: entry.declared === true,
    models: Array.isArray(entry.models) ? stringList(entry.models) : null,
  };
  const source = str(entry.source);
  if (source) out.source = source;
  const cls = str(entry.class);
  if (cls) out.class = cls;
  return out;
}

/** pf's workspace read → the step facts. summarize and memory_policy are
 *  cli.toml facts read from `pf config list` only: a
 *  drifted workspace document carrying them is ignored. */
export function sanitizeWorkspace(raw: unknown): {
  mapped: boolean;
  steps?: PolyforgeConfigStep[];
  steps_complete?: boolean;
  workspaceFile?: { sha256: string | null; exists: boolean };
} {
  if (!isRecord(raw)) return { mapped: false };
  if (raw.mapped !== true) return { mapped: false };
  const yaml = isRecord(raw.yaml)
    ? { sha256: typeof raw.yaml.sha256 === "string" ? raw.yaml.sha256 : null, exists: raw.yaml.exists !== false }
    : undefined;
  const steps: PolyforgeConfigStep[] = [];
  if (isRecord(raw.steps)) {
    for (const [outer, value] of Object.entries(raw.steps)) {
      if (!isRecord(value)) continue;
      if (typeof value.declared === "boolean") {
        steps.push(sanitizeStepEntry(undefined, outer, value));
        continue;
      }
      // Real pf groups candidates per project; every mapped project carries
      // a `default` and a `task` entry besides the step ids.
      for (const [step, entry] of Object.entries(value)) {
        if (!isRecord(entry) || typeof entry.declared !== "boolean") continue;
        steps.push(sanitizeStepEntry(outer, step, entry));
      }
    }
  }
  return {
    mapped: true,
    steps,
    steps_complete: raw.steps_complete !== false,
    ...(yaml ? { workspaceFile: yaml } : {}),
  };
}

/** The summarize rows' one source: the `summarize.*` items
 *  of `pf config list`, at or above the summarize floor. Below it — a null
 *  or unparseable version counts as below — and for a list that carries no
 *  `summarize.*` item at all, every `summarize.*` item is dropped and the
 *  reason names the floor and the found version; no `config workspace` read
 *  stands in for them. Pure; the read assembly below calls it. */
export function withListSummarize(
  items: PolyforgeConfigItem[],
  pfVersion: string | null,
): { items: PolyforgeConfigItem[]; reason?: string } {
  const below = isPolyforgeVersionBelowMinimum(pfVersion, POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION, true);
  if (!below && items.some((item) => item.key.startsWith("summarize."))) return { items };
  return {
    items: items.filter((item) => !item.key.startsWith("summarize.")),
    reason: `the summarize settings need pf ${POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION} or newer (found ${pfVersion ?? "an unparseable version"}) — upgrade pf to read and edit them`,
  };
}

/** The one read assembly the config route calls. Every degradation is a
 *  field, never an exception: pf absent / below the floor / an old list
 *  shape answers capable:false with visible reason text and issues no new
 *  workspace/write spawn.
 *
 * `scope` is always explicit — the Host, the default Cell or
 *  a named Cell — and every Cell read is pinned to it (`pinPolyforgeScope`),
 *  never left to the Cell pf would resolve from the server's PF_PROFILE or
 *  cwd. A scope this pf cannot address answers capable:false with the
 *  upgrade hint and spawns no config verb. The live daemon facts come from
 *  the daemon side that applies the file read: the Host aggregate
 *  (`pf --json ops status`, a Host command — PF_PROFILE and the cwd never
 *  move it) for the Host and the default Cell (cli.toml, whose aggregate
 *  starts from the default Cell's own facts), that Cell's own status
 *  (`--profile NAME`) for a named Cell. The workspace read is a Cell's
 *  mapping, so the Host scope has none. */
export async function readPolyforgeConfig(cwd: string, scope: PolyforgeConfigScope): Promise<PolyforgeConfigRead> {
  // A capability read is route-initiated: probe fresh so an installed pf
  // upgrade/downgrade is reflected on the next panel read.
  const presence = await probePolyforgeVersion(true);
  if (!presence.present) {
    return {
      pfPresent: false,
      pfVersion: null,
      capable: false,
      reason: "polyforge is not installed on this machine",
      items: [],
      mapped: false,
    };
  }
  if (isPolyforgeVersionBelowMinimum(presence.version, POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION)) {
    return {
      pfPresent: true,
      pfVersion: presence.version,
      capable: false,
      reason: `the configuration panel needs pf ${POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION} or newer (found ${presence.version ?? "an unparseable version"})`,
      items: [],
      mapped: false,
    };
  }
  const pin = await pinPolyforgeScope(scope);
  if (!pin.ok) {
    return {
      pfPresent: true,
      pfVersion: presence.version,
      capable: false,
      reason: pin.reason,
      items: [],
      mapped: false,
    };
  }
  let list: unknown;
  try {
    ///#367: the Host (`--host`) or one Cell's (`--profile`) file
    list = await runPfJson([...pin.prefix, "--json", "config", "list", ...pin.suffix]);
  } catch (error) {
    return {
      pfPresent: true,
      pfVersion: presence.version,
      capable: false,
      reason: error instanceof Error ? error.message : "pf config list is unavailable",
      items: [],
      mapped: false,
    };
  }
  if (!hasConfigListShape(list)) {
    return {
      pfPresent: true,
      pfVersion: presence.version,
      capable: false,
      reason: "this pf reports the configuration list without the class/schema shape the panel needs",
      items: [],
      mapped: false,
    };
  }
  const items = list.items
    .map(sanitizeConfigItem)
    .filter((item): item is PolyforgeConfigItem => item !== null);

  let opsRaw: unknown = null;
  try {
    opsRaw = await runPfJson(polyforgeNamedCell(scope) ? [...pin.prefix, "--json", "ops", "status"] : ["--json", "ops", "status"]);
  } catch {
    opsRaw = null;
  }
  const { reload } = sanitizeOps(opsRaw);
  if (isRecord(opsRaw)) {
    for (const item of items) {
      const daemon = daemonValueFor(item.key, opsRaw);
      if (!daemon) continue;
      item.daemon = daemon;
      const reason = classifyStaleReason(item.key, item.value, daemon.value, reload, daemon.maxConfigured);
      if (reason) item.stale_reason = reason;
    }
  }

  const summarize = withListSummarize(items, presence.version);
  const memoryPolicy = summarize.reason ? null : sanitizeMemoryPolicy((list as Record<string, unknown>).memory_policy);

  let mapped = false;
  let steps: PolyforgeConfigStep[] | undefined;
  let stepsComplete: boolean | undefined;
  let workspaceFile: PolyforgeConfigRead["workspaceFile"];
  if (cwd && "profile" in scope) {
    let ws: ReturnType<typeof sanitizeWorkspace> | null = null;
    try {
      ws = sanitizeWorkspace(await runPfJson([...pin.prefix, "--json", "config", "workspace", "--cwd", cwd]));
    } catch {
      ws = null;
    }
    mapped = ws?.mapped === true;
    steps = ws?.steps;
    stepsComplete = ws?.steps_complete;
    workspaceFile = ws?.workspaceFile;
  }

  return {
    pfPresent: true,
    pfVersion: presence.version,
    capable: true,
    file: { sha256: list.file.sha256, exists: list.file.exists !== false },
    items: summarize.items,
    mapped,
    daemonReachable: isRecord(opsRaw),
    ...(reload ? { reload } : {}),
    ...(steps ? { steps } : {}),
    ...(stepsComplete !== undefined ? { steps_complete: stepsComplete } : {}),
    ...(workspaceFile ? { workspaceFile } : {}),
    ...(memoryPolicy ? { memoryPolicy } : {}),
    ...(summarize.reason ? { summarizeReason: summarize.reason } : {}),
  };
}
