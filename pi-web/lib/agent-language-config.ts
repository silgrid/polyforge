// The server-side Agent language: `<pi agent dir>/pi-web-settings.json`,
// `{ "version": 1, "agentLanguage": "en" | "zh-CN" }`. pi-web's own file, never pi's
// settings.json, cli.toml or browser storage. `app/api/agent-language` is its one writer.
import { mkdirSync, readFileSync } from "fs";
import { dirname, join } from "path";
import { writePrivateFileAtomicSync } from "./atomic-file";
import { defaultAgentDir } from "./http-dispatcher";
import { serializeByKey } from "./key-serializer";
import { normalizeAgentLanguage, type AgentLanguage } from "./agent-language";

const WRITE_CHAIN = Symbol.for("pi-web.agentLanguageWrites");

export function agentLanguageConfigPath(agentDir: string = defaultAgentDir()): string {
  return join(agentDir, "pi-web-settings.json");
}

/** The stored language; a missing, malformed or unknown value reads as English (never throws). */
export function readStoredAgentLanguage(agentDir?: string): AgentLanguage {
  try {
    const parsed: unknown = JSON.parse(readFileSync(agentLanguageConfigPath(agentDir), "utf8"));
    return normalizeAgentLanguage((parsed as { agentLanguage?: unknown } | null)?.agentLanguage);
  } catch {
    return normalizeAgentLanguage(undefined);
  }
}

/**
 * Stores `language`, keeping the file's other keys; one write at a time per file,
 * replaced atomically with mode 0600. A file that exists but is not a JSON object is
 * refused (thrown), never overwritten.
 */
export function writeStoredAgentLanguage(language: AgentLanguage, agentDir?: string): Promise<AgentLanguage> {
  const path = agentLanguageConfigPath(agentDir);
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
    mkdirSync(dirname(path), { recursive: true });
    writePrivateFileAtomicSync(path, `${JSON.stringify({ ...current, version: 1, agentLanguage: language }, null, 2)}\n`);
    return language;
  });
}
