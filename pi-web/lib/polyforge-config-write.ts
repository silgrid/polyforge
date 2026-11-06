import { spawn } from "node:child_process";
import {
  parsePolyforgeScope,
  polyforgeDefaultScope,
  polyforgeNamedCell,
  polyforgeScopePrefix,
  type PolyforgeConfigScope,
  type PolyforgeScopeArgv,
} from "./polyforge-cells-pure";
import { pinPolyforgeScope } from "./polyforge-config-scope";
import { randomUUID } from "node:crypto";
import { AGENT_LANGUAGE_DEFAULT, isAgentLanguage, type AgentLanguage } from "./agent-language";
import {
  hasConfigListShape,
  POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION,
  POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION,
} from "./polyforge-config";
import { isPolyforgeVersionBelowMinimum } from "./polyforge-detect";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { probePolyforgeVersion } from "./polyforge-status";
import {
  CLI_WRITABLE_KEYS,
  IDENTITY_KEYS,
  NON_EMPTY_STRING_KEYS,
  isStepSkipKey,
  parseStepWriteKey,
  parseWorkspaceWriteKey,
  stepValueError,
  SUMMARIZE_KEYS,
  workspaceProjectsValue,
} from "./polyforge-config-schema";
import { POLYFORGE_WORKFLOW_CARD_MINIMUM_PF_VERSION } from "./polyforge-workflows";
import {
  appendPolyforgeConfigHistory,
  type PolyforgeConfigHistoryEntry,
} from "./polyforge-config-history";

/**
 * The panel's write path. It is the only module allowed to
 * mutate configuration, and it can only do so through `pf`: one no-shell
 * spawn helper, the exact writable-key allowlist, and api_key on stdin only.
 * No value from this module is ever logged or echoed in an error.
 * every config verb here — the capability list read, the
 * old-value read, the write, the rollback and the reload — is pinned to the
 * request's explicit scope (`pinPolyforgeScope`: `--profile default` for the
 * default Cell too, `--host` for the Host), never left to the Cell pf would
 * resolve from the server's PF_PROFILE or cwd. `pf --json ops status` stays a
 * Host command (PF_PROFILE and the cwd never move it).
 * Outside the panel it also writes pf's `[daemon] agent_language` of the
 * default Cell: the Agent language save (
 * `syncAgentLanguageToPolyforge`) and the backfill of an unset key from Pi
 * Web's stored language at the server's start only (
 * `reconcilePolyforgeAgentLanguage`). `GET /api/agent-language` only reads
 * the key (`readPolyforgeAgentLanguage`): one `config list`,
 * never a write.
 */

/** The fixed allowlist: every cli.toml key the panel edits (:
 *  the nine non-secret keys plus write-only api_key) and the cli.toml
 *  [summarize] keys the same constants the client form
 *  renders from. memory_policy is read-only in the panel and never listed. */
export const WRITABLE_KEYS = new Set([...CLI_WRITABLE_KEYS, ...SUMMARIZE_KEYS]);

/** The panel's allowlist: the fixed keys above, the workspace step keys
 *  `steps.<project>.<step-id>.models` and
 *  `steps.<project>.<step-id>.skip`, and a Cell's whole
 *  `[workspace."<abs path>"]` mapping sections. */
export function isPanelWritableKey(key: string): boolean {
  return WRITABLE_KEYS.has(key) || parseStepWriteKey(key) !== null || parseWorkspaceWriteKey(key) !== null;
}

/** A key written to the workspace `.polyforge.yaml` (needs a cwd): the
 *  step keys only — summarize.* is a flat cli.toml key. */
function isWorkspaceKey(key: string): boolean {
  return parseStepWriteKey(key) !== null;
}

const WRITE_TIMEOUT_MS = 30000;

export class ConfigWriteError extends Error {
  status: number;
  diff?: unknown;
  constructor(message: string, status = 400, diff?: unknown) {
    super(message);
    this.name = "ConfigWriteError";
    this.status = status;
    this.diff = diff;
  }
}

interface PfRun {
  code: number;
  stdout: string;
  stderr: string;
}

function runPf(argv: string[], stdin?: string): Promise<PfRun> {
  return new Promise((resolve, reject) => {
    const child = spawn(polyforgePfBin(), argv, { shell: false, stdio: ["pipe", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      reject(new ConfigWriteError("pf did not answer in time", 504));
    }, WRITE_TIMEOUT_MS);
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => { stdout += chunk; });
    child.stderr.on("data", (chunk: string) => { stderr += chunk; });
    child.on("error", (error) => {
      clearTimeout(timer);
      reject(new ConfigWriteError(error instanceof Error ? error.message : "polyforge is unavailable", 503));
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      resolve({ code: code ?? 1, stdout, stderr });
    });
    if (stdin !== undefined) child.stdin.write(stdin);
    child.stdin.end();
  });
}

/** pf's typed error envelope, both the CLI's own api.Error and the wrapped
 *  `{error: …}` shape a stub/older build may use. pf keeps api_key out of the
 *  detail and diff by contract (the CLI reports a changed/unchanged bit with
 *  the value redacted); this function returns `detail`/`message` and `extra`
 *  verbatim and does no redaction of its own. */
export function parsePfError(stderr: string): { status: number; message: string; diff?: unknown } {
  const text = stderr.trim();
  const jsonStart = text.indexOf("{");
  if (jsonStart >= 0) {
    try {
      const parsed = JSON.parse(text.slice(jsonStart)) as Record<string, unknown>;
      const envelope = (parsed.error && typeof parsed.error === "object" ? parsed.error : parsed) as Record<string, unknown>;
      const code = typeof envelope.code === "string" ? envelope.code : "";
      const detail = typeof envelope.detail === "string" ? envelope.detail
        : typeof envelope.message === "string" ? envelope.message : "pf refused the write";
      const extra = envelope.extra;
      const status = /-(409)$/.test(code) ? 409 : 400;
      return { status, message: detail, diff: extra };
    } catch {
      // fall through to pf's plain stderr
    }
  }
  return { status: 400, message: text.slice(0, 512) || "pf refused the write" };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function assertShape(key: string, value: unknown): void {
  if (parseStepWriteKey(key)) {
    const invalid = stepValueError(key, value);
    if (invalid) throw new ConfigWriteError(`${key}: ${invalid}`);
    return;
  }
  if (key === "api_key") {
    if (typeof value !== "string" || value === "") throw new ConfigWriteError("api_key must be a non-empty string");
    return;
  }
  if (parseWorkspaceWriteKey(key)) {
    if (!workspaceProjectsValue(value)) throw new ConfigWriteError(`${key}: projects must be one or more project names (letters, digits, . _ -)`);
    return;
  }
  if (key === "summarize.models") {
    if (!Array.isArray(value) || value.some((item) => typeof item !== "string")) {
      throw new ConfigWriteError("summarize.models must be an ordered list of model candidates");
    }
    return;
  }
  if (key === "summarize.language") {
    if (typeof value !== "string" || value.trim() === "") throw new ConfigWriteError("summarize.language must be a non-empty string");
    return;
  }
  if (key === "summarize.interval_sec" || key === "max_concurrent_jobs" || key === "cell_max_concurrent_jobs") {
    const okNumber = typeof value === "number" && Number.isInteger(value);
    const okString = typeof value === "string" && /^-?\d+$/.test(value.trim());
    if (!okNumber && !okString) throw new ConfigWriteError(`${key} must be an integer`);
    return;
  }
  if (key === "model.source") {
    if (typeof value !== "string" || (value !== "shared-default" && !value.startsWith("/"))) {
      throw new ConfigWriteError("model.source must be shared-default or an absolute directory");
    }
    return;
  }
  if (key === "step_timeout" || key === "stall_window") {
    if (typeof value !== "string" || value.trim() === "") throw new ConfigWriteError(`${key} must be a duration string`);
    return;
  }
  if (NON_EMPTY_STRING_KEYS.includes(key)) {
    if (typeof value !== "string" || value.trim() === "") throw new ConfigWriteError(`${key} must be a non-empty string`);
    return;
  }
}

function valueArg(key: string, value: unknown): string {
  // a mapping section is pf's `projects=a,b`
  if (parseWorkspaceWriteKey(key)) return `projects=${(workspaceProjectsValue(value) ?? []).join(",")}`;
  if (key === "summarize.models" || (parseStepWriteKey(key) && Array.isArray(value))) return JSON.stringify(value);
  return String(value);
}

/** Whether the Host answers (`pf --json ops status`, the Host aggregate). */
async function opsReachable(): Promise<boolean> {
  const run = await runPf(["--json", "ops", "status"]);
  return run.code === 0;
}

/** Pin a scope for this pf; a scope it cannot address (a
 *  Host or named Cell on a pf without the multi-Cell protocol) is refused
 *  400 with the upgrade hint before any scoped spawn. */
async function pinOrRefuse(scope: PolyforgeConfigScope): Promise<PolyforgeScopeArgv> {
  const pin = await pinPolyforgeScope(scope);
  if (!pin.ok) throw new ConfigWriteError(pin.reason, 400);
  return { prefix: pin.prefix, suffix: pin.suffix };
}

async function readConfigList(pin: PolyforgeScopeArgv): Promise<Record<string, unknown>> {
  const run = await runPf([...pin.prefix, "--json", "config", "list", ...pin.suffix]);
  if (run.code !== 0) {
    const failure = parsePfError(run.stderr);
    throw new ConfigWriteError(failure.message, failure.status, failure.diff);
  }
  return JSON.parse(run.stdout) as Record<string, unknown>;
}


export interface ConfigWriteRequest {
  action: "set" | "unset";
  key: string;
  value?: unknown;
  cwd?: string;
  expect_state?: unknown;
  /** the Cell (`profile`) or the Host (`host: true`) the write
   *  addresses; absent = the default Cell, pinned
   *  `--profile default` — never the Cell pf resolves from the server. */
  profile?: string;
  host?: boolean;
}

export interface ConfigWriteResult {
  ok: true;
  key: string;
  outcome: "applied" | "saved";
  message: string;
  entry: PolyforgeConfigHistoryEntry | null;
  daemonReachable: boolean;
}

/** The write surface stays unavailable below the configuration panel's
 *  capability floor. A read only: it never spawns a write/workspace verb. */
async function assertPanelFloor(): Promise<void> {
  const presence = await probePolyforgeVersion(true);
  if (!presence.present) throw new ConfigWriteError("polyforge is not installed on this machine", 400);
  if (isPolyforgeVersionBelowMinimum(presence.version, POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION)) {
    throw new ConfigWriteError(`the configuration panel needs pf ${POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION} or newer (found ${presence.version ?? "an unparseable version"})`, 400);
  }
}

/** The write's scope pinned for this pf, and the list of the file it
 *  addresses carrying the class/schema shape the panel needs (an older shape
 *  is refused). Reads only: it never spawns a write/workspace verb. */
async function assertWritableScope(scope: PolyforgeConfigScope): Promise<PolyforgeScopeArgv> {
  const pin = await pinOrRefuse(scope);
  const list = await readConfigList(pin);
  if (!hasConfigListShape(list)) {
    throw new ConfigWriteError("this pf reports the configuration list without the class/schema shape the panel needs", 400);
  }
  return pin;
}

/** A summarize write needs the pf that reads and writes [summarize] in
 *  cli.toml; an older pf would write the retired yaml key.
 *  A null/unparseable version is below the floor: 400, nothing spawned. */
async function assertSummarizeWriteCapability(): Promise<void> {
  const presence = await probePolyforgeVersion(true);
  if (isPolyforgeVersionBelowMinimum(presence.version, POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION, true)) {
    throw new ConfigWriteError(`the summarize settings need pf ${POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION} or newer (found ${presence.version ?? "an unparseable version"})`, 400);
  }
}

/** A step-key write also needs the workflow card's floor and the running
 *  daemon's own fact (`write_capable` of `pf --json workflows`: it answers
 *  with its workflows dir). pf refuses the same way (CORE-501) before
 *  touching the file; this gate names the reason without a write spawn. */
async function assertStepWriteCapability(cwd: string, pin: PolyforgeScopeArgv): Promise<void> {
  const presence = await probePolyforgeVersion(true);
  if (isPolyforgeVersionBelowMinimum(presence.version, POLYFORGE_WORKFLOW_CARD_MINIMUM_PF_VERSION)) {
    throw new ConfigWriteError(`step chain writes need pf ${POLYFORGE_WORKFLOW_CARD_MINIMUM_PF_VERSION} or newer (found ${presence.version ?? "an unparseable version"})`, 400);
  }
  const run = await runPf([...pin.prefix, "--json", "workflows", "--cwd", cwd]);
  if (run.code !== 0) {
    const failure = parsePfError(run.stderr);
    throw new ConfigWriteError(failure.message, failure.status, failure.diff);
  }
  let doc: Record<string, unknown> = {};
  try { doc = JSON.parse(run.stdout) as Record<string, unknown>; } catch { doc = {}; }
  if (doc.write_capable !== true) {
    const reason = typeof doc.write_reason === "string" && doc.write_reason ? doc.write_reason : "the running daemon cannot check the step keys";
    throw new ConfigWriteError(reason, 400);
  }
}

interface CurrentValue {
  class: string;
  hash: string | null;
  exists: boolean;
  value: unknown;
  set: boolean;
  workspace?: string;
}

/** The value a key has in the file right now, read through pf. The write path
 *  records `old` from here (never from the previous history entry), and takes
 *  the CAS hash of the file the key actually lives in (cli.toml for the cli
 *  and summarize keys, the workspace yaml for the step keys). */
async function readCurrentValue(key: string, cwd: string, pin: PolyforgeScopeArgv): Promise<CurrentValue> {
  const stepKey = parseStepWriteKey(key);
  const skipKey = isStepSkipKey(key);
  if (stepKey) {
    const run = await runPf([...pin.prefix, "--json", "config", "workspace", "--cwd", cwd]);
    if (run.code !== 0) {
      const failure = parsePfError(run.stderr);
      throw new ConfigWriteError(failure.message, failure.status, failure.diff);
    }
    const doc = JSON.parse(run.stdout) as Record<string, unknown>;
    const yaml = isRecord(doc.yaml) ? doc.yaml : {};
    const steps = isRecord(doc.steps) && isRecord(doc.steps[stepKey.project]) ? doc.steps[stepKey.project] as Record<string, unknown> : {};
    const entry = isRecord(steps[stepKey.step]) ? steps[stepKey.step] as Record<string, unknown> : {};
    if (skipKey) {
      // the skip switch is fixed when a work item is claimed
      // (pf's class of a .skip key); its old value is the entry's `skip`
      // when pf reports one.
      const set = typeof entry.skip === "boolean";
      return {
        class: "next-claim",
        hash: typeof yaml.sha256 === "string" ? yaml.sha256 : null,
        exists: yaml.exists !== false,
        value: set ? entry.skip : null,
        set,
        workspace: typeof doc.workspace === "string" ? doc.workspace : cwd,
      };
    }
    const declared = entry.declared === true;
    return {
      class: typeof entry.class === "string" ? entry.class : "",
      hash: typeof yaml.sha256 === "string" ? yaml.sha256 : null,
      exists: yaml.exists !== false,
      value: declared ? entry.models ?? null : null,
      set: declared,
      workspace: typeof doc.workspace === "string" ? doc.workspace : cwd,
    };
  }
  const list = await readConfigList(pin);
  const file = isRecord(list.file) ? list.file : {};
  const items = Array.isArray(list.items) ? list.items : [];
  const item = items.find((it) => isRecord(it) && it.key === key);
  const cls = item && typeof item.class === "string" ? item.class : "";
  const hash = typeof file.sha256 === "string" ? file.sha256 : null;
  const exists = file.exists !== false;
  if (key === "api_key") {
    const set = Boolean(item && item.set === true);
    return { class: cls, hash, exists, value: { set }, set };
  }
  const source = item && typeof item.source === "string" ? item.source : "default";
  const fromFile = source === "file";
  return { class: cls, hash, exists, value: fromFile ? item?.value ?? null : null, set: fromFile };
}

/** pf's reload class → the result text. A restart-only key never claims to be
 *  applied by the running daemon; a down daemon always says the next start. */
function writeMessage(cls: string, reachable: boolean): { outcome: "applied" | "saved"; text: string } {
  if (!reachable) return { outcome: "saved", text: "saved; the value applies at the next daemon start" };
  switch (cls) {
    case "restart-only":
      return { outcome: "saved", text: "saved; the value applies after a daemon restart" };
    case "next-claim":
      return { outcome: "saved", text: "saved; applies at the next claim" };
    case "cli-only":
      return { outcome: "saved", text: "saved (cli-only); applies to the next pf invocation" };
    default:
      return { outcome: "applied", text: "applied" };
  }
}

async function executeWrite(body: ConfigWriteRequest): Promise<ConfigWriteResult> {
  await assertPanelFloor();
  const key = typeof body.key === "string" ? body.key : "";
  if (IDENTITY_KEYS.includes(key)) {
    throw new ConfigWriteError(`${key} is a Cell's identity: a different AIHub server or user is a new profile — add a Cell (pf cell add) instead of editing this one`, 400);
  }
  if (!isPanelWritableKey(key)) throw new ConfigWriteError(`key ${JSON.stringify(key)} is not writable from the panel`, 400);
  const action = body.action;
  if (action !== "set" && action !== "unset") throw new ConfigWriteError("action must be set or unset");
  const cwd = typeof body.cwd === "string" ? body.cwd : "";
  const scope: "cli" | "workspace" = isWorkspaceKey(key) ? "workspace" : "cli";
  if (scope === "workspace" && !cwd) throw new ConfigWriteError("a workspace write needs cwd");
  if (action === "set") assertShape(key, body.value);
  let cellScope: PolyforgeConfigScope;
  try { cellScope = parsePolyforgeScope(body); } catch (error) {
    throw new ConfigWriteError(error instanceof Error ? error.message : "bad scope", 400);
  }
  // A workspace (.polyforge.yaml) key is a Cell's workflow card's: pf
  // matches its cwd in that Cell's own mapping (:
  // every Cell pane has the card). The Host has no workspace.
  if (scope === "workspace" && "host" in cellScope) {
    throw new ConfigWriteError("a workspace (.polyforge.yaml) write belongs to a Cell: it takes no Host scope", 400);
  }
  const pin = await assertWritableScope(cellScope);
  if (parseStepWriteKey(key)) await assertStepWriteCapability(cwd, pin);
  if (key.startsWith("summarize.")) await assertSummarizeWriteCapability();

  // A fresh read gives the file value before the write (the history's `old`)
  // and the file the key lives in (the CAS hash).
  const current = await readCurrentValue(key, scope === "workspace" ? cwd : "", pin);

  const argv = [...pin.prefix, "--json", "config", action, key, ...pin.suffix];
  let stdin: string | undefined;
  if (action === "set") {
    if (key === "api_key") {
      stdin = `${String(body.value)}\n`;
    } else {
      argv.push(valueArg(key, body.value));
    }
  }
  if (scope === "workspace") argv.push("--cwd", cwd);
  if (isRecord(body.expect_state)) argv.push("--expect-state", JSON.stringify(body.expect_state));

  const run = await runPf(argv, stdin);
  if (run.code !== 0) {
    const failure = parsePfError(run.stderr);
    throw new ConfigWriteError(failure.message, failure.status, failure.diff);
  }

  const reachable = await opsReachable();
  const secret = key === "api_key";
  const old = secret ? { set: current.set } : current.set ? current.value : null;
  const entry: PolyforgeConfigHistoryEntry = {
    id: randomUUID(),
    at: new Date().toISOString(),
    scope,
    // Workspace entries key by the resolved workspace, never the raw cwd.
    cwd: scope === "workspace" ? current.workspace ?? cwd : "",
    key,
    old,
    new: secret ? { set: action === "set" } : action === "set" ? (parseWorkspaceWriteKey(key) ? workspaceProjectsValue(body.value) : body.value) : null,
    secret,
    rollbackable: !secret,
    ...cellScope,
  };
  await appendPolyforgeConfigHistory(entry);
  const message = writeMessage(current.class, reachable);
  return { ok: true, key, outcome: message.outcome, message: message.text, entry, daemonReachable: reachable };
}

export async function writePolyforgeConfig(body: ConfigWriteRequest): Promise<ConfigWriteResult> {
  return executeWrite(body);
}

export interface ConfigRollbackRequest {
  id?: string;
  key?: string;
  old?: unknown;
  cwd?: string;
  entry?: PolyforgeConfigHistoryEntry;
}

/** Rollback is a fresh read followed by the SAME CAS write path; it appends
 *  a new entry and never touches api_key (non-rollbackable). The entry's own
 *  workspace is used, never the panel's current cwd, and an entry with no old
 *  value rolls back to `unset` as the spec says. */
export async function rollbackPolyforgeConfig(body: ConfigRollbackRequest): Promise<ConfigWriteResult> {
  const entry = body.entry;
  const key = typeof body.key === "string" ? body.key : entry?.key ?? "";
  if (!isPanelWritableKey(key)) throw new ConfigWriteError(`key ${JSON.stringify(key)} is not writable from the panel`, 400);
  if (key === "api_key") throw new ConfigWriteError("api_key entries are not rollbackable", 400);
  const scope: "cli" | "workspace" = isWorkspaceKey(key) ? "workspace" : "cli";
  const workspace = scope === "workspace" ? entry?.cwd ?? "" : "";
  if (scope === "workspace" && !workspace) throw new ConfigWriteError("this workspace entry has no workspace to roll back to", 400);
  // a scoped entry rolls back in the file it was written to;
  // an entry without a scope is the default Cell's.
  let cellScope: PolyforgeConfigScope;
  try { cellScope = parsePolyforgeScope({ host: entry?.host, profile: entry?.profile }); } catch (error) {
    throw new ConfigWriteError(error instanceof Error ? error.message : "bad scope", 400);
  }
  const current = await readCurrentValue(key, workspace, await pinOrRefuse(cellScope));
  const expectState: Record<string, unknown> = { file_sha256: current.hash };
  const target = body.old !== undefined ? body.old : entry?.old;
  if (target === null || target === undefined) {
    return executeWrite({ action: "unset", key, cwd: workspace, expect_state: expectState, ...cellScope });
  }
  return executeWrite({ action: "set", key, value: target, cwd: workspace, expect_state: expectState, ...cellScope });
}

/** `pf config reload` of one scope (the default Cell pinned
 *  `--profile default`), then the daemon's own facts read independently —
 *  the Host aggregate for the Host and the default Cell, a named Cell's own
 *  status for it. */
export async function reloadPolyforgeConfig(cellScope: PolyforgeConfigScope): Promise<{ reloaded: unknown; daemon: unknown }> {
  const pin = await pinOrRefuse(cellScope);
  const run = await runPf([...pin.prefix, "--json", "config", "reload", ...pin.suffix]);
  if (run.code !== 0) {
    const failure = parsePfError(run.stderr);
    throw new ConfigWriteError(failure.message, failure.status, failure.diff);
  }
  let reloaded: unknown = null;
  try { reloaded = JSON.parse(run.stdout); } catch { reloaded = null; }
  const status = await runPf(polyforgeNamedCell(cellScope) ? [...pin.prefix, "--json", "ops", "status"] : ["--json", "ops", "status"]);
  let daemon: unknown = null;
  if (status.code === 0) {
    try { daemon = JSON.parse(status.stdout); } catch { daemon = null; }
  }
  return { reloaded, daemon };
}

/** the Agent language save (`PUT /api/agent-language`) also
 *  sets pf's `[daemon] agent_language` of the default Cell (the language of
 *  the daemon's step agents), before Pi Web writes its own file. Written only
 *  when pf is installed and its default Cell's `config list` carries the key
 *  (capability by fact); no pf, a pf without the key, or a `config list` that
 *  fails or does not print JSON is a no-op (Pi Web stays usable on its own). A pf refusal throws (ConfigWriteError), so the
 *  caller writes nothing local. Not a panel write: no history entry, and the
 *  key stays out of WRITABLE_KEYS. This is the user's explicit save and may
 *  overwrite a key pf's file sets; the automatic backfill
 *  (`reconcilePolyforgeAgentLanguage` below,) never does. */
export async function syncAgentLanguageToPolyforge(language: string): Promise<{ synced: boolean }> {
  const presence = await probePolyforgeVersion(true);
  if (!presence.present) return { synced: false };
  const pin = await pinOrRefuse(polyforgeDefaultScope());
  let list: unknown;
  try {
    list = await readConfigList(pin);
  } catch {
    return { synced: false };
  }
  const items = isRecord(list) && Array.isArray(list.items) ? list.items : [];
  if (!items.some((item) => isRecord(item) && item.key === "agent_language")) return { synced: false };
  const run = await runPf([...pin.prefix, "--json", "config", "set", "agent_language", language, ...pin.suffix]);
  if (run.code !== 0) {
    const failure = parsePfError(run.stderr);
    throw new ConfigWriteError(failure.message, failure.status, failure.diff);
  }
  return { synced: true };
}

/** pf's `[daemon] agent_language` of the default Cell: the
 *  language its step agents use and whether pf's file sets it (`file`) or pf
 *  falls back to its default English (`default`). */
export interface PolyforgeAgentLanguage {
  language: AgentLanguage;
  source: "default" | "file";
}

/** the automatic backfill, run at Pi Web's start only (the GET
 *  reads without it, `readPolyforgeAgentLanguage`). Reads the default
 *  Cell's `agent_language` from `pf --profile default --json config list`
 *  (the same capability fact as `syncAgentLanguageToPolyforge`) and, only when
 *  pf's file does not set it (`source` default) and `stored` differs from pf's
 *  language (its default English), runs `pf config set agent_language
 *  <stored>` with `--expect-state` on the hash that list read; a key pf's
 *  file sets is never overwritten, nor one set between the list and the set
 *  (pf refuses the stale hash). Resolves to pf's state after the backfill (a
 *  refused or failed set: one fresh list's state, else the state before it),
 *  or null for no pf, a pf
 *  without the key (older), a scope it cannot pin or a list that fails or is
 *  not JSON; never spawns a set then and never throws. Not a panel write: no
 *  history entry, and the key stays out of WRITABLE_KEYS. */
export async function reconcilePolyforgeAgentLanguage(stored: AgentLanguage): Promise<PolyforgeAgentLanguage | null> {
  try {
    const presence = await probePolyforgeVersion(true);
    if (!presence.present) return null;
    const pin = await pinPolyforgeScope(polyforgeDefaultScope());
    if (!pin.ok) return null;
    const read = await readAgentLanguageItem(pin);
    if (!read) return null;
    const { current, hash } = read;
    if (current.source === "file" || current.language === stored) return current;
    // r1#1: the set is a CAS on the file hash this same list
    // read (`--expect-state`, as executeWrite), so a write landing between the
    // read and the set — the user's save, a manual `pf config set` — makes pf
    // refuse it (CORE-409) instead of being overwritten. Any refused set is not
    // retried: one fresh read answers pf's state then (a failed read, the state
    // before the backfill).
    const expectState = JSON.stringify({ file_sha256: hash });
    const run = await runPf([...pin.prefix, "--json", "config", "set", "agent_language", stored, ...pin.suffix, "--expect-state", expectState]).catch(() => null);
    if (run?.code === 0) return { language: stored, source: "file" };
    const reread = await readAgentLanguageItem(pin).catch(() => null);
    return reread ? reread.current : current;
  } catch {
    return null;
  }
}

/** pf's `[daemon] agent_language` of the default Cell as it is
 *  now, for `GET /api/agent-language`: exactly one spawn, `pf --profile default
 *  --json config list` — no version probe, no capability read, never a write.
 *  The key is newer than the multi-Cell protocol, so a pf that
 *  has it accepts `--profile`; an older pf answers no key either way. Resolves
 *  to null for no pf, a pf without the key, or a list that fails or is not
 *  JSON; never throws. */
export async function readPolyforgeAgentLanguage(): Promise<PolyforgeAgentLanguage | null> {
  try {
    const read = await readAgentLanguageItem({ prefix: polyforgeScopePrefix(polyforgeDefaultScope()), suffix: [] });
    return read ? read.current : null;
  } catch {
    return null;
  }
}

/** The default Cell's `agent_language` row of one `pf config list` and the
 *  sha256 of the file that list read (null when pf reports the file absent:
 *  the CAS then expects it absent); null for a pf without the key. Throws when
 *  the list fails or is not JSON. */
async function readAgentLanguageItem(pin: PolyforgeScopeArgv): Promise<{ current: PolyforgeAgentLanguage; hash: string | null } | null> {
  const list = await readConfigList(pin);
  const items = isRecord(list) && Array.isArray(list.items) ? list.items : [];
  const item = items.find((entry): entry is Record<string, unknown> => isRecord(entry) && entry.key === "agent_language");
  if (!item) return null;
  const file = isRecord(list.file) ? list.file : {};
  const hash = file.exists !== false && typeof file.sha256 === "string" ? file.sha256 : null;
  const current: PolyforgeAgentLanguage = item.source === "file"
    ? { language: isAgentLanguage(item.value) ? item.value : AGENT_LANGUAGE_DEFAULT, source: "file" }
    : { language: AGENT_LANGUAGE_DEFAULT, source: "default" };
  return { current, hash };
}
