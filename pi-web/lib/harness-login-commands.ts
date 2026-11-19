// The terminal login whitelist (polyforge#142, 85a2 AC3) — placeholder: the
// test-first commit carries only the signature so the suite's assertions
// run red; the implementation lands with the unit.

export interface ResolvedLoginCommand {
  argv: string[];
  env: Record<string, string>;
  cwd: string;
}

export async function resolveLoginCommand(
  _login: { harness: unknown; id: unknown; cell?: unknown },
  _options: { adapters?: unknown[]; doctorRead?: () => Promise<unknown> } = {},
): Promise<{ ok: true; command: ResolvedLoginCommand } | { ok: false; status: 400; reason: string }> {
  return { ok: false, status: 400, reason: "not implemented" };
}
