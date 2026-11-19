// The adapter account reads (polyforge#142, 85a2 AC1): the safe status
// commands the account page runs — claude's `auth status` for the
// interactive session card, and pi's `auth check --provider <p> --json
// --no-refresh` against a Cell's model-credential source dir for the step
// rows. The bounded runner never echoes raw output: only the parsed,
// closed-set fields leave this module, and the pi check always passes
// --no-refresh (a refresh would write auth.json and could race the
// daemon's step-agent snapshot) and never --credentials or a print-*
// subcommand (they would print a credential value).
import { spawn } from "node:child_process";
import { join } from "node:path";
import { buildClaudeEnv } from "../claude-argv";
import { findClaudeBinary } from "../claude-session";
import type { HarnessReadContext } from "./adapter";
import { readJsonRecord } from "./redact";

/** How long one status command may run before it is killed. */
export const AUTH_STATUS_DEADLINE_MS = 15_000;

/** claude's interactive-session status command, as the account page runs it. */
export const CLAUDE_AUTH_STATUS_ARGV: readonly string[] = ["auth", "status"];

/**
 * pi's step-status command shape: `auth check --provider <p> --json
 * --no-refresh` — the words after the binary, the provider spliced in. The
 * last word is pinned here so no caller can drop it.
 */
export const PI_AUTH_CHECK_ARGV: readonly string[] = ["auth", "check", "--provider", "--json", "--no-refresh"];

/** The interactive claude session's account status (never a value). */
export interface ClaudeSessionStatus {
  /** false when claude was not found or answered nothing parsable. */
  available: boolean;
  loggedIn: boolean | null;
  authMethod: string | null;
  configDirectory: string | null;
  /** Why the status is unavailable (claude missing, an unparsable answer); never raw output. */
  reason?: string;
}

/** One pi step-status read of a model-credential source dir (never a value). */
export interface PiStepStatus {
  status: "ready" | "not_ready" | "unknown";
  provider: string | null;
  authType: string | null;
  reason: string | null;
}

interface RunOutcome {
  ok: boolean;
  stdout: string;
  reason: string;
}

/** One bounded run: argv without a shell, a deadline that kills, the raw
 * stdout captured for parsing only — it never leaves this module. */
function runBounded(argv: string[], options: { env?: NodeJS.ProcessEnv; cwd?: string; timeoutMs?: number } = {}): Promise<RunOutcome> {
  const timeoutMs = options.timeoutMs ?? AUTH_STATUS_DEADLINE_MS;
  return new Promise<RunOutcome>((resolve) => {
    let stdout = "";
    let settled = false;
    const finish = (outcome: RunOutcome) => {
      if (settled) return;
      settled = true;
      clearTimeout(deadline);
      resolve(outcome);
    };
    let child;
    try {
      child = spawn(argv[0], argv.slice(1), {
        stdio: ["ignore", "pipe", "ignore"],
        env: options.env,
        cwd: options.cwd,
      });
    } catch (error) {
      finish({ ok: false, stdout: "", reason: error instanceof Error ? error.message : "the command could not be started" });
      return;
    }
    child.stdout?.setEncoding("utf8");
    child.stdout?.on("data", (chunk: string) => { stdout += chunk; });
    child.on("error", (error) => finish({ ok: false, stdout: "", reason: error instanceof Error ? error.message : "the command could not be started" }));
    child.on("close", () => finish({ ok: true, stdout, reason: "" }));
    const deadline = setTimeout(() => {
      child.kill("SIGKILL");
      finish({ ok: false, stdout: "", reason: "the command did not answer in time" });
    }, timeoutMs);
    deadline.unref();
  });
}

/** The environment a status command runs under: the caller's minus the
 * browser password. */
function statusEnv(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const out: NodeJS.ProcessEnv = {};
  for (const [key, value] of Object.entries(env)) {
    if (value !== undefined && key !== "PI_WEB_PASSWORD") out[key] = value;
  }
  return out;
}

/**
 * The interactive claude session's status through `claude auth status` —
 * the command the ticket names as the status source that never prints a
 * secret. Exit 1 (signed out) parses the same JSON as exit 0; a missing
 * binary or an unparsable answer degrades, never throws, and never echoes
 * the raw output.
 */
export async function claudeSessionStatus(
  ctx?: HarnessReadContext,
  options: { timeoutMs?: number } = {},
): Promise<ClaudeSessionStatus> {
  const binary = ctx?.claudeBinary ?? findClaudeBinary();
  if (!binary) return { available: false, loggedIn: null, authMethod: null, configDirectory: null, reason: "claude was not found on PATH" };
  const run = await runBounded([binary, ...CLAUDE_AUTH_STATUS_ARGV], {
    env: statusEnv(buildClaudeEnv(ctx?.env ?? process.env) as NodeJS.ProcessEnv),
    timeoutMs: options.timeoutMs,
  });
  if (!run.ok) {
    return { available: false, loggedIn: null, authMethod: null, configDirectory: null, reason: `claude auth status ${run.reason}` };
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(run.stdout);
  } catch {
    return { available: false, loggedIn: null, authMethod: null, configDirectory: null, reason: "claude auth status answered no parsable status" };
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return { available: false, loggedIn: null, authMethod: null, configDirectory: null, reason: "claude auth status answered no parsable status" };
  }
  const answer = parsed as Record<string, unknown>;
  if (typeof answer.loggedIn !== "boolean") {
    return { available: false, loggedIn: null, authMethod: null, configDirectory: null, reason: "claude auth status answered no parsable status" };
  }
  return {
    available: true,
    loggedIn: answer.loggedIn,
    authMethod: typeof answer.authMethod === "string" ? answer.authMethod : null,
    configDirectory: typeof answer.configDirectory === "string" ? answer.configDirectory : null,
  };
}

/**
 * The provider a source dir's step status is checked for: its settings.json
 * defaultProvider, else the first credential key of auth.json (names only —
 * the file is never echoed). None declared: null with the reason.
 */
export function piStepStatusProvider(sourceDir: string): { provider: string | null; reason: string | null } {
  const settings = readJsonRecord(join(sourceDir, "settings.json"));
  if (settings && typeof settings.defaultProvider === "string" && settings.defaultProvider !== "") {
    return { provider: settings.defaultProvider, reason: null };
  }
  const auth = readJsonRecord(join(sourceDir, "auth.json"));
  if (auth) {
    const first = Object.keys(auth)[0];
    if (first) return { provider: first, reason: null };
  }
  return { provider: null, reason: "the source directory declares no provider and holds no credential" };
}

/**
 * One Cell's pi step status: `pi auth check --provider <p> --json
 * --no-refresh` with PI_CODING_AGENT_DIR pointing at the Cell's
 * model-credential source dir. The check never refreshes (so it never
 * writes auth.json) and never prints a credential; only the parsed
 * status/provider/authType/reason fields leave.
 */
export async function piStepStatus(
  sourceDir: string,
  options: { binary?: string; env?: NodeJS.ProcessEnv; timeoutMs?: number } = {},
): Promise<PiStepStatus> {
  const { provider, reason } = piStepStatusProvider(sourceDir);
  if (provider === null) {
    return { status: "unknown", provider: null, authType: null, reason };
  }
  const env = statusEnv(options.env ?? process.env);
  env.PI_CODING_AGENT_DIR = sourceDir;
  const run = await runBounded([options.binary ?? "pi", "auth", "check", "--provider", provider, "--json", "--no-refresh"], {
    env,
    cwd: sourceDir,
    timeoutMs: options.timeoutMs,
  });
  if (!run.ok) {
    return { status: "unknown", provider, authType: null, reason: `pi auth check ${run.reason}` };
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(run.stdout);
  } catch {
    return { status: "unknown", provider, authType: null, reason: "pi auth check answered no parsable status" };
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return { status: "unknown", provider, authType: null, reason: "pi auth check answered no parsable status" };
  }
  const answer = parsed as Record<string, unknown>;
  if (answer.status === "ready") {
    return { status: "ready", provider, authType: typeof answer.authType === "string" ? answer.authType : null, reason: null };
  }
  if (answer.status === "not_ready") {
    return { status: "not_ready", provider, authType: null, reason: typeof answer.reason === "string" ? answer.reason : "not ready" };
  }
  return { status: "unknown", provider, authType: null, reason: "pi auth check answered no parsable status" };
}
