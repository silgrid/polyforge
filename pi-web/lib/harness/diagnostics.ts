// Native diagnostics execution (polyforge#149, #85 85a1b, AC6 — #141 已定
// Q5): the fixed safe diagnostic command a harness declares, run with a
// deadline, without a terminal, and through a filter that never echoes a
// credential or environment variable value. Only the capability table's
// fixed commands ever run — a command that could print a secret is never
// started, whatever the filter below could mask; the filter is the second
// layer, not the safety.
import { spawn } from "node:child_process";
import { buildClaudeEnv } from "../claude-argv";
import { findClaudeBinary } from "../claude-session";
import { SECRET_MASK, isSecretName, looksLikeSecretValue } from "../mcp-secrets";
import type { HarnessReadContext } from "./adapter";
import type { NativeDiagnosticsResult } from "./concepts";

/** The fixed safe diagnostic argv of the claude harness: `claude doctor`. */
export const CLAUDE_DIAGNOSTIC_ARGV: readonly string[] = ["doctor"];

/** How long a diagnostic command may run before it is signalled. */
export const DIAGNOSTIC_DEADLINE_MS = 15_000;
/** How long a signalled command may take to leave before it is killed. */
const DIAGNOSTIC_EXIT_GRACE_MS = 2_000;
/** The output caps: a bounded tail of lines, a bounded total of bytes. */
const MAX_LINES = 400;
const MAX_BYTES = 64 * 1024;

/** An environment-variable-shaped name: the pair rule masks its value whatever the name says (r1#2). */
const ENV_SHAPED_NAME = /^[A-Z][A-Z0-9_]*$/;
const isCredentialName = (name: string): boolean => isSecretName(name) || ENV_SHAPED_NAME.test(name);

/** A URL's userinfo (scheme://user:pass@host) — a credential wherever it sits. */
const URL_USERINFO = /([A-Za-z][A-Za-z0-9+.-]*:\/\/)([^\s/@]+)@/g;

/**
 * One output line with every credential or environment variable value masked:
 * `NAME=value` / `NAME: value` pairs under a credential name or an
 * environment-variable-shaped name lose their value whatever it looks like,
 * a URL's `user:pass@` is masked wherever it sits, any remaining bare token
 * that looks like a key or token is masked, and a bare occurrence of a set
 * secret-named variable's value (when the run's env is given) is masked too.
 * Ordinary words, paths and versions pass through.
 */
export function maskDiagnosticLine(line: string, env?: NodeJS.ProcessEnv): string {
  const pairsMasked = line.replace(
    /([A-Za-z0-9_.\-]+)(\s*[:=]\s*)("[^"]*"|\S+)/g,
    (whole: string, name: string, separator: string) => (
      isCredentialName(name) ? `${name}${separator}${SECRET_MASK}` : whole
    ),
  );
  const userinfoMasked = pairsMasked.replace(URL_USERINFO, (_whole: string, scheme: string) => `${scheme}${SECRET_MASK}@`);
  const tokensMasked = userinfoMasked.replace(/\S+/g, (token) => (looksLikeSecretValue(token) ? SECRET_MASK : token));
  if (!env) return tokensMasked;
  // A value the caller's environment holds under a credential-named variable
  // never echoes, even bare: mask each occurrence. Short or whitespace-bearing
  // values are left alone — masking them would eat ordinary output.
  let masked = tokensMasked;
  for (const [name, value] of Object.entries(env)) {
    if (!isSecretName(name) || typeof value !== "string" || value.length < 8 || /\s/.test(value)) continue;
    masked = masked.split(value).join(SECRET_MASK);
  }
  return masked;
}

/** The bounded run's outcome; `ok:false` means the command could not be started. */
export type DiagnosticRun =
  | { ok: true; lines: string[]; truncated: boolean; timedOut: boolean }
  | { ok: false; reason: string };

/**
 * Runs `argv` without a terminal — stdin is closed at once, so an interactive
 * prompt reads EOF immediately and cannot block — with a deadline that
 * SIGTERM (then SIGKILL) enforces, capturing a masked, capped tail of the
 * output. Never throws, never leaves a child behind the grace period.
 */
export async function runDiagnosticCommand(
  argv: readonly string[],
  options: { timeoutMs?: number; env?: NodeJS.ProcessEnv } = {},
): Promise<DiagnosticRun> {
  return new Promise<DiagnosticRun>((resolve) => {
    let settled = false;
    let timedOut = false;
    let truncated = false;
    let bytes = 0;
    const lines: string[] = [];
    let pending = "";

    const child = spawn(argv[0], argv.slice(1), {
      stdio: ["ignore", "pipe", "pipe"],
      env: (options.env ?? process.env) as NodeJS.ProcessEnv,
    });

    const take = (chunk: string) => {
      pending += chunk;
      let newline: number;
      while ((newline = pending.indexOf("\n")) >= 0) {
        const line = pending.slice(0, newline).replace(/\r$/, "");
        pending = pending.slice(newline + 1);
        addLine(line);
      }
    };
    const addLine = (line: string) => {
      if (lines.length >= MAX_LINES) {
        truncated = true;
        return;
      }
      const masked = maskDiagnosticLine(line, options.env);
      if (masked.length > MAX_BYTES || bytes + masked.length > MAX_BYTES) {
        truncated = true;
        return;
      }
      bytes += masked.length;
      lines.push(masked);
    };
    const finish = (result: DiagnosticRun) => {
      if (settled) return;
      settled = true;
      clearTimeout(deadline);
      resolve(result);
    };

    child.on("error", (error) => finish({ ok: false, reason: `could not be started: ${error.message}` }));
    child.stdout?.setEncoding("utf8");
    child.stdout?.on("data", take);
    child.stderr?.setEncoding("utf8");
    child.stderr?.on("data", take);
    child.on("close", () => {
      if (pending) addLine(pending.replace(/\r$/, ""));
      finish({ ok: true, lines, truncated, timedOut });
    });

    const deadline = setTimeout(() => {
      timedOut = true;
      child.kill("SIGTERM");
      const kill = setTimeout(() => { child.kill("SIGKILL"); }, DIAGNOSTIC_EXIT_GRACE_MS);
      kill.unref();
      // The deadline is the answer: what was captured by now is the result,
      // the same bound the probe holds itself to.
      if (pending) addLine(pending.replace(/\r$/, ""));
      finish({ ok: true, lines, truncated: true, timedOut });
    }, options.timeoutMs ?? DIAGNOSTIC_DEADLINE_MS);
    deadline.unref();
  });
}

/**
 * claude's native diagnostics through its fixed safe command, `claude doctor`.
 * The run inherits claude's own environment (pi-web's minus its password), is
 * bounded by the deadline, and its output is masked before it is answered.
 */
export async function claudeDiagnose(
  ctx?: HarnessReadContext,
  options: { timeoutMs?: number } = {},
): Promise<NativeDiagnosticsResult> {
  const binary = ctx?.claudeBinary ?? findClaudeBinary();
  if (!binary) return { available: false, reason: "claude was not found on PATH" };
  const run = await runDiagnosticCommand([binary, ...CLAUDE_DIAGNOSTIC_ARGV], {
    env: buildClaudeEnv(ctx?.env ?? process.env) as NodeJS.ProcessEnv,
    timeoutMs: options.timeoutMs ?? DIAGNOSTIC_DEADLINE_MS,
  });
  if (!run.ok) return { available: false, reason: `claude doctor ${run.reason}` };
  return {
    available: true,
    command: ["claude", ...CLAUDE_DIAGNOSTIC_ARGV],
    lines: run.lines,
    truncated: run.truncated,
    timedOut: run.timedOut,
  };
}
