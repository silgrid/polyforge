// The adapter account reads (polyforge#142, 85a2 AC1) — placeholder: the
// test-first commit carries only the signatures so the suite's assertions
// (not a missing symbol) run red; the implementation lands with the unit.
import type { HarnessReadContext } from "./adapter";

export const CLAUDE_AUTH_STATUS_ARGV: readonly string[] = [];
export const PI_AUTH_CHECK_ARGV: readonly string[] = [];

export interface ClaudeSessionStatus {
  available: boolean;
  loggedIn: boolean | null;
  authMethod: string | null;
  configDirectory: string | null;
  reason?: string;
}

export interface PiStepStatus {
  status: "ready" | "not_ready" | "unknown";
  provider: string | null;
  authType: string | null;
  reason: string | null;
}

export async function claudeSessionStatus(_ctx?: HarnessReadContext, _options: { timeoutMs?: number } = {}): Promise<ClaudeSessionStatus> {
  return { available: false, loggedIn: null, authMethod: null, configDirectory: null };
}

export function piStepStatusProvider(_sourceDir: string): { provider: string | null; reason: string | null } {
  return { provider: null, reason: null };
}

export async function piStepStatus(_sourceDir: string, _options: { binary?: string; env?: NodeJS.ProcessEnv; timeoutMs?: number } = {}): Promise<PiStepStatus> {
  return { status: "unknown", provider: null, authType: null, reason: null };
}
