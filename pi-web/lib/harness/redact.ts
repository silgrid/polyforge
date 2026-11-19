// Redaction helpers every harness adapter's per-concept read builds its
// payloads through (polyforge#85 85a1): a payload is constructed by an
// allow-list, never by passing a parsed native file through. Credentials
// appear only as existence and path; an env block appears only as its key
// names; an MCP entry is masked through lib/mcp-secrets.ts (maskUrl,
// maskArgs, isSecretName) — the one masking implementation, reused.
import { existsSync } from "node:fs";
import type { CredentialRef, MaskedMcpServer } from "./concepts";
import { maskUrl, maskArgs } from "../mcp-secrets";

/** A credential file's existence and path, never its contents. */
export function credentialRef(path: string): CredentialRef {
  // stub (85a1 u2): zero-value body, the red run's target
  return { exists: false, path: "" };
}

/** Which of `names` are set in `env` — names only, never values. */
export function automationEnvStatus(env: NodeJS.ProcessEnv, names: readonly string[]): Record<string, boolean> {
  // stub (85a1 u2): zero-value body, the red run's target
  return {};
}

/** A plain JSON object read (BOM tolerated), null when absent or not an object — never pi's SettingsManager, which locks and busy-waits. */
export function readJsonRecord(path: string): Record<string, unknown> | null {
  // stub (85a1 u2): zero-value body, the red run's target
  return null;
}

/** One MCP server as the mcp concept lists it: masked url and args, header/env names without values. */
export function maskedMcpServer(
  scope: MaskedMcpServer["scope"],
  info: {
    name: string;
    enabled?: boolean;
    url?: string;
    command?: string;
    args?: string[];
    headerNames?: string[];
    envNames?: string[];
  },
): MaskedMcpServer {
  // stub (85a1 u2): zero-value body, the red run's target
  return { name: "", scope };
}
