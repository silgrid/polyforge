// What the claude on PATH reports about itself: its version, its model
// aliases (with the full name each resolves to) and its own capabilities, from claude's
// answer to the host's `initialize` control request. Asked of one claude with no prompt —
// zero credit, no turn, no session persisted (lib/__fixtures__/claude-2.1.322/initialize/).
// And Pi Web's own judgment of the three claude features it relies on (claudePiWebFeatures()).
// Server only.
import { spawn } from "node:child_process";
import { realpathSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { buildClaudeEnv } from "./claude-argv";
import {
  CLAUDE_FEATURE_MIN_VERSION,
  compareClaudeVersions,
  isClaudeModelName,
  isClaudeRecordedVersion,
  type ClaudeFeature,
  type ClaudePermissionMode,
} from "./claude-shared";

/** One of claude's model aliases as its `initialize` answer lists it (claude's ModelInfo, the fields Pi Web reads). */
export interface ClaudeAliasModel {
  /** The name passed to `--model` (`default`, `opus`, …). */
  value: string;
  displayName?: string;
  /** The full name claude resolves the alias to (`claude-opus-5-5`). */
  resolvedModel?: string;
}

/** The parts of claude's `initialize` answer Pi Web reads. */
export interface ClaudeProbeInfo {
  version: string | null;
  models: ClaudeAliasModel[];
  /** claude's own capability names, verbatim (never mapped to Pi Web features). */
  capabilities: string[];
  /** Whether `version` is one of CLAUDE_RECORDED_VERSIONS (the fixtures cover it). */
  recorded: boolean;
}

export type ClaudeProbeResult = ClaudeProbeInfo | { error: string };

/** The probe's argv: no session, nothing persisted; stdin carries only `initialize`. */
export const CLAUDE_PROBE_ARGV: readonly string[] = [
  "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
  "--no-session-persistence", "--permission-prompts", "none",
];
/** How long a probed claude may take to leave after its stdin closes before it is signalled. */
export const CLAUDE_PROBE_EXIT_GRACE_MS = 2_000;
/** How long claude gets to answer `initialize`. */
export const CLAUDE_PROBE_DEADLINE_MS = 15_000;
const PROBE_REQUEST_ID = "pi-web-probe-initialize";
const STDERR_TAIL_BYTES = 2048;

const text = (value: unknown): string | undefined => (typeof value === "string" && value ? value : undefined);

/** The answer's fields Pi Web reads; anything malformed reads as missing. Never throws. */
export function claudeInitializeInfo(response: unknown): ClaudeProbeInfo {
  const answer = response && typeof response === "object" ? response as Record<string, unknown> : {};
  const version = text(answer.claude_code_version) ?? null;
  const models: ClaudeAliasModel[] = [];
  if (Array.isArray(answer.models)) {
    for (const entry of answer.models) {
      if (!entry || typeof entry !== "object") continue;
      const model = entry as Record<string, unknown>;
      if (!isClaudeModelName(model.value) || models.some((known) => known.value === model.value)) continue;
      const displayName = text(model.displayName);
      const resolvedModel = text(model.resolvedModel);
      models.push({ value: model.value, ...(displayName ? { displayName } : {}), ...(resolvedModel ? { resolvedModel } : {}) });
    }
  }
  const capabilities = Array.isArray(answer.capabilities) && answer.capabilities.every((name) => typeof name === "string")
    ? [...answer.capabilities as string[]]
    : [];
  return { version, models, capabilities, recorded: isClaudeRecordedVersion(version) };
}

interface ProbeCache {
  /** By binary realpath, mtime and size: a replaced or upgraded claude is asked again. */
  answers: Map<string, Promise<ClaudeProbeResult>>;
  /** The most recent successful answer: the aliases a session's model resolves through. */
  latest: ClaudeProbeInfo | null;
}
const globalCache = globalThis as typeof globalThis & { __piWebClaudeProbe?: ProbeCache };
const cache: ProbeCache = globalCache.__piWebClaudeProbe ??= { answers: new Map(), latest: null };

function binaryKey(binary: string): string | null {
  try {
    const real = realpathSync(binary);
    const stat = statSync(real);
    return `${real}\0${stat.mtimeMs}\0${stat.size}`;
  } catch {
    return null;
  }
}

/** Ask `binary` once: spawn, write `initialize`, read its answer, end the child. Never throws. */
function ask(binary: string, timeoutMs: number): Promise<ClaudeProbeResult> {
  return new Promise((resolve) => {
    let settled = false;
    let stdout = "";
    let stderr = "";
    const child = spawn(binary, [...CLAUDE_PROBE_ARGV], {
      cwd: homedir(),
      env: buildClaudeEnv(process.env) as NodeJS.ProcessEnv,
      stdio: ["pipe", "pipe", "pipe"],
    });
    const finish = (result: ClaudeProbeResult) => {
      if (settled) return;
      settled = true;
      clearTimeout(deadline);
      // End claude the way it ends itself: close its stdin. Only a child that outlives
      // the grace period is signalled (SIGTERM, then SIGKILL), never one that is leaving.
      child.stdin?.end();
      const alive = () => child.exitCode === null && child.signalCode === null;
      const term = setTimeout(() => {
        if (alive()) child.kill("SIGTERM");
        const kill = setTimeout(() => { if (alive()) child.kill("SIGKILL"); }, CLAUDE_PROBE_EXIT_GRACE_MS);
        kill.unref();
      }, CLAUDE_PROBE_EXIT_GRACE_MS);
      term.unref();
      child.once("exit", () => clearTimeout(term));
      resolve(result);
    };
    const deadline = setTimeout(() => finish({ error: `claude did not answer initialize within ${timeoutMs / 1000} s` }), timeoutMs);
    child.on("error", (error) => finish({ error: `claude could not be started: ${error.message}` }));
    child.stdin?.on("error", () => {});
    child.stderr?.setEncoding("utf8");
    child.stderr?.on("data", (chunk: string) => {
      stderr = (stderr + chunk).slice(-STDERR_TAIL_BYTES);
    });
    child.stdout?.setEncoding("utf8");
    child.stdout?.on("data", (chunk: string) => {
      stdout += chunk;
      let newline: number;
      while ((newline = stdout.indexOf("\n")) >= 0) {
        const line = stdout.slice(0, newline);
        stdout = stdout.slice(newline + 1);
        let frame: { type?: unknown; response?: { subtype?: unknown; request_id?: unknown; response?: unknown; error?: unknown } };
        try {
          frame = JSON.parse(line);
        } catch {
          continue;
        }
        if (frame?.type !== "control_response" || frame.response?.request_id !== PROBE_REQUEST_ID) continue;
        if (frame.response.subtype === "success") finish(claudeInitializeInfo(frame.response.response));
        else finish({ error: text(frame.response.error) ?? "claude refused initialize" });
      }
    });
    child.on("exit", (code, signal) => {
      const tail = stderr.trim();
      finish({ error: `claude exited (${signal ?? `code ${code}`}) before answering initialize${tail ? `: ${tail}` : ""}` });
    });
    child.stdin?.write(`${JSON.stringify({ type: "control_request", request_id: PROBE_REQUEST_ID, request: { subtype: "initialize" } })}\n`);
  });
}

/**
 * What `binary` reports: its `initialize` answer, else `{ error }` (it could not be
 * started, exited, refused or did not answer within the deadline). An answer is cached
 * per binary (realpath, mtime, size) for the server's life; an error is not, so the next
 * call asks again.
 */
export async function probeClaude(binary: string, options: { timeoutMs?: number } = {}): Promise<ClaudeProbeResult> {
  const key = binaryKey(binary);
  if (key === null) return { error: `claude could not be read: ${binary}` };
  const cached = cache.answers.get(key);
  if (cached) return cached;
  const pending = ask(binary, options.timeoutMs ?? CLAUDE_PROBE_DEADLINE_MS).then((result) => {
    if ("error" in result) cache.answers.delete(key);
    else cache.latest = result;
    return result;
  });
  cache.answers.set(key, pending);
  return pending;
}

/** The full name claude's latest answer resolves `model` to when it is one of its aliases; otherwise `model` itself. */
export function claudeResolvedModel(model: string): string {
  return cache.latest?.models.find((alias) => alias.value === model)?.resolvedModel ?? model;
}

/**
 * claude 2.1.322's reading of a boolean environment variable (its env module's
 * `H.bool()`, i.e. `Pe()`: `["1","true","yes","on"].includes(String(v).toLowerCase().trim())`).
 */
function claudeEnvTrue(value: string | undefined): boolean {
  return value !== undefined && ["1", "true", "yes", "on"].includes(value.toLowerCase().trim());
}

/**
 * Pi Web's three claude features (owner ruling Q1=A), from Pi Web's own
 * evidence: approvals from's approval mode `approvalMode` — the global
 * claude-permissions.json mode the caller reads, the one claude-argv's permissionPromptFlags()
 * turns into the child's flags: `ask` is `--permission-prompts host --permission-prompt-tool
 * stdio`, Pi Web answers claude's approval requests (`host-prompts`); `deny` is
 * `--permission-prompts none`, claude denies what would ask (`no-host-prompts`); never by
 * claude version, as #442 is not ( r3, plan D12); background tasks and the subagent's forwarded text from
 * the recorded threshold (CLAUDE_FEATURE_MIN_VERSION: unknown without a version, off
 * below it), background tasks also off when the child's environment `env` — the caller
 * passes `buildClaudeEnv(process.env)` — sets CLAUDE_CODE_DISABLE_BACKGROUND_TASKS (claude
 * then answers `background_tasks` "Background tasks are disabled in this session.").
 * A claude newer than the recordings is judged by the threshold. Pure.
 */
export function claudePiWebFeatures(version: string | null, env: Record<string, string | undefined>, approvalMode: ClaudePermissionMode): ClaudeFeature[] {
  const byVersion = (id: "backgroundTasks" | "subagentText"): ClaudeFeature => {
    const compared = compareClaudeVersions(version, CLAUDE_FEATURE_MIN_VERSION[id]);
    if (compared === null) return { id, available: null, basis: "version-unknown" };
    if (compared < 0) return { id, available: false, basis: "below-recorded" };
    if (id === "backgroundTasks" && claudeEnvTrue(env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS)) return { id, available: false, basis: "env-disabled" };
    return { id, available: true, basis: "recorded" };
  };
  const host = approvalMode === "ask";
  return [
    { id: "approvals", available: host, basis: host ? "host-prompts" : "no-host-prompts" },
    byVersion("backgroundTasks"),
    byVersion("subagentText"),
  ];
}
