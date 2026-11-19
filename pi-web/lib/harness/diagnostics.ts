// Native diagnostics execution (polyforge#149, #85 85a1b, AC6 — #141 已定
// Q5): the fixed safe diagnostic command each harness declares, run with a
// deadline, without a terminal, and through a filter that never echoes a
// credential or environment variable value. Only the capability table's fixed
// commands ever run — a command that could print a secret is never started,
// whatever a later filter could mask. Placeholder signatures; the bodies
// arrive with their unit and replace this stub.
import type { HarnessReadContext } from "./adapter";
import type { NativeDiagnosticsResult } from "./concepts";

/** The fixed safe diagnostic argv of the claude harness: `claude doctor`. */
export const CLAUDE_DIAGNOSTIC_ARGV: readonly string[] = ["doctor"];

/** How long a diagnostic command may run before it is signalled. */
export const DIAGNOSTIC_DEADLINE_MS = 15_000;

/** One output line with every credential- or env-value-looking token masked. */
export function maskDiagnosticLine(_line: string): string {
  return "";
}

export async function runDiagnosticCommand(
  _argv: readonly string[],
  _options: { timeoutMs?: number; env?: NodeJS.ProcessEnv } = {},
): Promise<{ lines: string[]; truncated: boolean; timedOut: boolean }> {
  return { lines: [], truncated: false, timedOut: false };
}

/** claude's native diagnostics through its fixed safe command. */
export async function claudeDiagnose(
  _ctx?: HarnessReadContext,
  _options: { timeoutMs?: number } = {},
): Promise<NativeDiagnosticsResult> {
  return { available: false, reason: "not implemented yet" };
}
