/**
 * Browser-safe key-schema facts for the Settings › Polyforge configuration
 * panel.
 *
 * The panel's controls are built from pf's own key schema, and the form
 * enforces the same bounds before a write reaches the route. That guard must
 * run in the browser, so this module is deliberately import-free: the
 * server-side reads in `lib/polyforge-config.ts` pull `node:child_process`
 * and the pi SDK, which must never enter the client bundle. `polyforge-config.ts`
 * uses the `PolyforgeConfigSchema` type from here; the client form uses
 * `configValueError`. One definition, two consumers.
 */

/** The shape of pf's per-key validator facts, unchanged from the wire. */
export interface PolyforgeConfigSchema {
  kind: string;
  min?: number;
  max?: number;
  min_exclusive?: number;
  nonzero?: boolean;
  allow_negative?: boolean;
  enum?: unknown;
  [key: string]: unknown;
}

/** The cli.toml keys the panel writes, one fact for the
 *  client form (rows, folds, confirmation) and the write route's allowlist.
 *  Common keys are visible without expanding; advanced keys fold per file
 *  zone (the top-level zone and the `[daemon]` zone). pf stays the authority
 *  on every value; these lists only decide where a key renders and which
 *  keys the panel may hand to `pf config set`. */
export const CLI_COMMON_KEYS: readonly string[] = ["max_concurrent_jobs", "step_timeout", "stall_window", "claude_bin", "api_key"];
export const CLI_ADVANCED_TOP_LEVEL_KEYS: readonly string[] = ["server", "daemon_socket", "user_id"];
export const CLI_ADVANCED_DAEMON_KEYS: readonly string[] = [
  "harness_root", "operator_agent_dir",
  // polyforge#51: the Host-only classification pattern lists and the
  // slow-model latency bounds (Host region rows; a Cell's card excludes
  // them via HOST_ONLY_KEYS).
  "transient_error_patterns", "model_error_patterns", "slow_model_median_latency", "slow_model_p90_latency",
];
/** the Cell-only keys of a named Cell's `cli-<profile>.toml`
 *  (written with the Cell scope): the optional Cell ceiling and the
 *  model-credential declaration (`shared-default` or an absolute dir). */
export const CELL_KEYS: readonly string[] = ["cell_max_concurrent_jobs", "model.source"];
/** the keys that ARE a Cell's identity domain. They are never
 *  edited in place — a different AIHub server (or another user's key) is a
 *  new profile, added as a new Cell — so no panel surface writes them. */
export const IDENTITY_KEYS: readonly string[] = ["server"];
export const CLI_WRITABLE_KEYS: readonly string[] = [...CLI_COMMON_KEYS, ...CLI_ADVANCED_TOP_LEVEL_KEYS, ...CLI_ADVANCED_DAEMON_KEYS, ...CELL_KEYS]
  .filter((key) => !IDENTITY_KEYS.includes(key));
/** the Host-only keys the Host region edits one line each (polyforge#51:
 *  the two slow-model latency bounds join the scalar rows). */
export const HOST_SCALAR_KEYS: readonly string[] = ["max_concurrent_jobs", "daemon_socket", "harness_root", "operator_agent_dir", "slow_model_median_latency", "slow_model_p90_latency"];
/** the Host-only pattern-list keys (polyforge#51): the Host region edits
 *  each as one regex per line. */
export const HOST_LIST_KEYS: readonly string[] = ["transient_error_patterns", "model_error_patterns"];
/** the Host-only keys — a named Cell's file never carries them, the
 *  cli.toml card excludes them, the Host region owns them. */
export const HOST_ONLY_KEYS: readonly string[] = [...HOST_SCALAR_KEYS, ...HOST_LIST_KEYS];
/** The connection/credential keys: a save needs a second confirmation, since
 *  the panel's next pf call uses the new value while the running daemon keeps
 *  the old one until it restarts. narrowed to same-identity
 *  edits — `server` is an identity key (a change is a new profile) and never
 *  reaches this confirmation; an api_key of another user is refused by pf. */
export const CONNECTION_KEYS: readonly string[] = ["api_key", "daemon_socket", "user_id"];
/** The cli.toml [summarize] keys the panel writes (flat
 *  machine-level cli.toml keys, written without a workspace and read only
 *  from `pf config list`; the cli.toml card's `[summarize]` zone, all common
 *  rows) — never the workspace `.polyforge.yaml`. */
export const SUMMARIZE_KEYS: readonly string[] = ["summarize.models", "summarize.language", "summarize.interval_sec"];
/** The summarize/memory_policy floor: the pf release that
 *  first carries summarize.* and the per-path memory_policy on
 *  `pf config list` (`docs/upgrade.md` v2.20.21). Below it —
 *  a null or unparseable version included — both groups are hidden with one
 *  upgrade hint and summarize writes answer 400; nothing is read for them
 *  from `config workspace`. Defined here (browser-safe) so the server read,
 *  the write gate and the client hint share one value; a release tagged
 *  differently changes only this value and its fixtures. */
export const POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION = "2.20.21";
/** String/path keys that must not be saved empty (the only guard the panel
 *  adds besides pf's own schema kinds). */
export const NON_EMPTY_STRING_KEYS: readonly string[] = ["server", "daemon_socket", "user_id", "claude_bin", "harness_root", "operator_agent_dir"];

/** Parse a Go duration (the `supervision` keys' syntax) to milliseconds, or
 *  null when the text is not one. Pure and exported so the form can enforce
 *  pf's duration schema client-side. */
export function parseGoDurationMs(text: string): number | null {
  const trimmed = text.trim();
  if (trimmed === "0" || trimmed === "-0" || trimmed === "+0") return 0;
  if (!/^-?(?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+$/.test(trimmed)) return null;
  const factors: Record<string, number> = { ns: 1e-6, us: 1e-3, "µs": 1e-3, ms: 1, s: 1000, m: 60000, h: 3600000 };
  let total = 0;
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  for (let m = re.exec(trimmed); m !== null; m = re.exec(trimmed)) {
    total += Number(m[1]) * factors[m[2]];
  }
  return trimmed.startsWith("-") ? -total : total;
}

/** The form's client-side enforcement of pf's key schema (spec: "Unreachable
 *  in the form"): an int's min/max and a duration's min_exclusive/nonzero are
 *  checked here so the form cannot submit 0 or 9. It is a guard, not a second
 *  validator — the route still hands every value to pf. */
export function configValueError(key: string, value: unknown, schema: PolyforgeConfigSchema | undefined): string | null {
  const kind = schema?.kind;
  if (kind === "model-list" || key === "summarize.models") {
    if (!Array.isArray(value) || value.length === 0) return "at least one model candidate is required";
    if (value.some((item) => typeof item !== "string" || item.trim() === "")) return "model candidates must be non-empty strings";
    return null;
  }
  if (kind === "regex-list") {
    if (!Array.isArray(value)) return "a list of regex strings is required";
    if (value.some((item) => typeof item !== "string" || item.trim() === "")) return "patterns must be non-empty strings";
    return null; // the regex dialect is pf's to judge (RE2 — a JS-only syntax passes the form and pf refuses it)
  }
  if (kind === "int") {
    const n = typeof value === "number" ? value : Number(String(value).trim());
    if (!Number.isInteger(n)) return "an integer is required";
    if (typeof schema?.min === "number" && n < schema.min) return `must be at least ${schema.min}`;
    if (typeof schema?.max === "number" && n > schema.max) return `must be at most ${schema.max}`;
    return null;
  }
  if (kind === "duration") {
    const ms = typeof value === "string" ? parseGoDurationMs(value) : null;
    if (ms === null) return "a Go duration such as 45m is required";
    if (typeof schema?.min_exclusive === "number" && ms <= schema.min_exclusive * 1000) return "must be greater than zero";
    if (schema?.nonzero === true && ms === 0) return "must be non-zero";
    return null;
  }
  if (typeof value !== "string" || value.trim() === "") return "a value is required";
  return null;
}

/** The workspace step keys the panel writes:
 *  `steps.<project>.<step-id>.models` (a candidate list) and,
 *  `steps.<project>.<step-id>.skip` (a boolean: the project's switch of a
 *  step its lane declares skippable — pf refuses any other step). Shared by
 *  the client form and the write route, so the allowlist and the guard are
 *  one fact. */
export type StepWriteKey = { project: string; step: string };

const STEP_SEGMENT = /^[A-Za-z0-9_-]+$/;

export function parseStepWriteKey(key: string): StepWriteKey | null {
  const parts = key.split(".");
  if (parts[0] !== "steps" || !parts.slice(1).every((part) => STEP_SEGMENT.test(part))) return null;
  if (parts.length === 4 && (parts[3] === "models" || parts[3] === "skip")) return { project: parts[1], step: parts[2] };
  return null;
}

/** Whether a step key is the skip switch (`steps.<p>.<s>.skip`). */
export function isStepSkipKey(key: string): boolean {
  return parseStepWriteKey(key) !== null && key.endsWith(".skip");
}

export function stepWriteKey(project: string, step: string): string {
  return `steps.${project}.${step}.models`;
}

/** The skip switch's key of one project's step. */
export function skipWriteKey(project: string, step: string): string {
  return `steps.${project}.${step}.skip`;
}

/** The form-side guard of a step-key value: a models list of non-empty
 *  candidate strings (pf alone judges the candidate format), or a boolean
 *  for the skip switch. A guard, not a validator — pf stays the authority. */
export function stepValueError(key: string, value: unknown): string | null {
  if (!parseStepWriteKey(key)) return "not a writable step key";
  if (isStepSkipKey(key)) return typeof value === "boolean" ? null : "skip must be true or false";
  if (!Array.isArray(value) || value.length === 0) return "at least one model candidate is required";
  if (value.some((item) => typeof item !== "string" || item.trim() === "")) return "model candidates must be non-empty strings";
  return null;
}

/** one Cell's 工作区 mapping row, written as a whole
 *  `[workspace."<abs path>"]` section of that Cell's file (`pf [--profile P]
 *  config set '[workspace."<path>"]' projects=a,b` / `config unset`). The
 *  path must be absolute and carry nothing the section header could misread
 *  (a quote, a backslash, a control character). Pure. */
export function workspaceWriteKey(path: string): string {
  return `[workspace."${path}"]`;
}

/** The workspace path a mapping key addresses, or null when the key is not
 *  a mapping key the panel may write. Shared by the form and the route. */
export function parseWorkspaceWriteKey(key: string): string | null {
  const match = /^\[workspace\."(.*)"\]$/.exec(key);
  if (!match) return null;
  const path = match[1];
  if (!path.startsWith("/") || /["\\\u0000-\u001f\u007f]/.test(path)) return null;
  return path;
}

// polyforge#151 (85b2): the [step_tiers] key family — pf's Host-only
// step-tier catalog, one `step_tiers.<tier>` key per tier. The tier-name
// grammar is pf's own (internal/polyforge/cli/step_tiers.go:
// ^[a-z][a-z0-9-]{0,39}$, the lane tier grammar), so a bad tier name is
// refused before any spawn, and the family is Host-only beside the fixed
// HOST_ONLY_KEYS list — a prefix family, never a fixed array entry (a
// named Cell's list would otherwise leak the catalog as read-only chips).
/** The pf release that first carries the [step_tiers] catalog — the
 *  family's own floor, independent of the panel's 2.20.x line (the
 *  summarize precedent in lib/polyforge-memory.ts: a v0.2.x pf ranks
 *  above every 2.20.x floor, an unparseable version is never capable). */
export const POLYFORGE_CONFIG_STEP_TIERS_MINIMUM_PF_VERSION = "0.2.4";

/** The tier name a `step_tiers.<tier>` key addresses, or null when the key
 *  is not a catalog key with a well-formed tier name (the panel's and the
 *  write route's shared refusal — a bad name never reaches pf). */
export function parseStepTierKey(key: string): string | null {
  const tier = key.startsWith("step_tiers.") ? key.slice("step_tiers.".length) : "";
  return /^[a-z][a-z0-9-]{0,39}$/.test(tier) ? tier : null;
}

/** Whether a key is Host-only: one of the fixed HOST_ONLY_KEYS, or a
 *  well-formed `step_tiers.<tier>` catalog key (the cli.toml card excludes
 *  both; the Host region and the models page's tier column own them). */
export function isHostOnlyKey(key: string): boolean {
  return HOST_ONLY_KEYS.includes(key) || parseStepTierKey(key) !== null;
}

const PROJECT_NAME = /^[A-Za-z0-9._-]+$/;

/** A mapping row's projects as pf's ordered list: an array of names or the
 *  comma string `pf config list` reports (and a history entry's old value
 *  keeps). Null when empty or when a name is not a plain project name. */
export function workspaceProjectsValue(value: unknown): string[] | null {
  const list = Array.isArray(value)
    ? value.map((entry) => (typeof entry === "string" ? entry.trim() : ""))
    : typeof value === "string" ? value.split(",").map((entry) => entry.trim()).filter((entry) => entry !== "") : null;
  if (!list || list.length === 0 || list.some((name) => !PROJECT_NAME.test(name))) return null;
  return [...new Set(list)];
}
