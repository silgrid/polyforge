// Redaction helpers every harness adapter's per-concept read builds its
// payloads through (polyforge#85 85a1): a payload is constructed by an
// allow-list, never by passing a parsed native file through. Credentials
// appear only as existence and path; an env block appears only as its key
// names; an MCP entry is masked through lib/mcp-secrets.ts (maskUrl,
// maskArgs) — the one masking implementation, reused.
import { existsSync, readFileSync } from "node:fs";
import type { CredentialRef, MaskedMcpServer } from "./concepts";
import { maskUrl, maskArgs } from "../mcp-secrets";

/** A credential file's existence and path, never its contents. */
export function credentialRef(path: string): CredentialRef {
  return { exists: existsSync(path), path };
}

/** Which of `names` are set in `env` — names only, never values. */
export function automationEnvStatus(env: NodeJS.ProcessEnv, names: readonly string[]): Record<string, boolean> {
  const status: Record<string, boolean> = {};
  for (const name of names) {
    status[name] = typeof env[name] === "string" && (env[name] as string) !== "";
  }
  return status;
}

/**
 * A plain JSON object read (BOM tolerated): null when absent, unreadable or
 * not an object. Never pi's SettingsManager — it locks each file and
 * busy-waits (lib/transcript-notices-server.ts), which a read must not pay.
 */
export function readJsonRecord(path: string): Record<string, unknown> | null {
  try {
    const parsed: unknown = JSON.parse(readFileSync(path, "utf8").replace(/^\uFEFF/, ""));
    return parsed !== null && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as Record<string, unknown>) : null;
  } catch {
    return null;
  }
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
  const server: MaskedMcpServer = { name: info.name, scope };
  if (info.enabled !== undefined) server.disabled = !info.enabled;
  if (info.url !== undefined) {
    const masked = maskUrl(info.url);
    server.url = { masked: masked.value, hadSecret: masked.masked };
  }
  if (info.command !== undefined) server.command = info.command;
  if (info.args !== undefined) {
    const masked = maskArgs(info.args);
    server.args = { args: masked.args, masked: masked.masked };
  }
  if (info.headerNames !== undefined) server.headerNames = [...info.headerNames];
  if (info.envNames !== undefined) server.envNames = [...info.envNames];
  return server;
}
