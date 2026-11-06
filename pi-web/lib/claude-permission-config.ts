// The global claude permission settings: `<pi agent dir>/claude-permissions.json`,
// `{ "version": 1, "mode": "ask" | "deny", "push": boolean }`. pi-web's own file, apart from
// pi-web-settings.json (whose one writer is the agent-language route). `mode` is the
// default a claude session's child is launched with unless its index record overrides it;
// `push` says whether a pending permission prompt is pushed to subscribed devices.
import { mkdirSync, readFileSync } from "fs";
import { dirname, join } from "path";
import { writePrivateFileAtomicSync } from "./atomic-file";
import { defaultAgentDir } from "./http-dispatcher";
import { serializeByKey } from "./key-serializer";
import { CLAUDE_PERMISSION_MODE_DEFAULT, isClaudePermissionMode, type ClaudePermissionMode } from "./claude-shared";

export interface ClaudePermissionConfig {
  mode: ClaudePermissionMode;
  push: boolean;
}

const WRITE_CHAIN = Symbol.for("pi-web.claudePermissionConfigWrites");

export function claudePermissionConfigPath(agentDir: string = defaultAgentDir()): string {
  return join(agentDir, "claude-permissions.json");
}

function normalize(value: unknown): ClaudePermissionConfig {
  const raw = (typeof value === "object" && value !== null && !Array.isArray(value) ? value : {}) as Record<string, unknown>;
  return {
    mode: isClaudePermissionMode(raw.mode) ? raw.mode : CLAUDE_PERMISSION_MODE_DEFAULT,
    push: typeof raw.push === "boolean" ? raw.push : true,
  };
}

/** The stored settings; a missing, malformed or unknown value reads as its default (ask, push on). Never throws. */
export function readClaudePermissionConfig(agentDir?: string): ClaudePermissionConfig {
  try {
    return normalize(JSON.parse(readFileSync(claudePermissionConfigPath(agentDir), "utf8")));
  } catch {
    return normalize(undefined);
  }
}

/**
 * Stores the keys `patch` names, keeping the file's other keys; one write at a time per
 * file, replaced atomically with mode 0600. A file that exists but is not a JSON object is
 * refused (thrown), never overwritten. Resolves to the settings as stored.
 */
export function writeClaudePermissionConfig(patch: Partial<ClaudePermissionConfig>, agentDir?: string): Promise<ClaudePermissionConfig> {
  const path = claudePermissionConfigPath(agentDir);
  return serializeByKey(WRITE_CHAIN, path, async () => {
    let current: Record<string, unknown> = {};
    let text: string | undefined;
    try {
      text = readFileSync(path, "utf8");
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    if (text !== undefined) {
      let parsed: unknown;
      try {
        parsed = JSON.parse(text);
      } catch {
        throw new Error(`${path} is not valid JSON; refusing to overwrite it`);
      }
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
        throw new Error(`${path} is not a JSON object; refusing to overwrite it`);
      }
      current = parsed as Record<string, unknown>;
    }
    const next = normalize({
      ...normalize(current),
      ...(patch.mode !== undefined ? { mode: patch.mode } : {}),
      ...(patch.push !== undefined ? { push: patch.push } : {}),
    });
    mkdirSync(dirname(path), { recursive: true });
    writePrivateFileAtomicSync(path, `${JSON.stringify({ ...current, version: 1, mode: next.mode, push: next.push }, null, 2)}\n`);
    return next;
  });
}
