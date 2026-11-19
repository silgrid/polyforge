// pi-web's own settings file, `<pi agent dir>/pi-web-settings.json`: the one
// server-side store of Pi Web's own preferences — the Agent language
// (`lib/agent-language-config.ts`, its reader stays there) and, from 85a4
// (polyforge#150), the claude preferences whose fields have no claude-native
// home (`lib/claude-preferences.ts`) plus the marker of the one-time
// migration of the retired self-stored files (`lib/claude-migration.ts`).
// One serialized writer per file, atomic 0600 replaces, unknown keys kept.
import { mkdirSync, readFileSync } from "fs";
import { dirname, join } from "path";
import { writePrivateFileAtomicSync } from "./atomic-file";
import { defaultAgentDir } from "./http-dispatcher";
import { serializeByKey } from "./key-serializer";

const WRITE_CHAIN = Symbol.for("pi-web.piWebSettingsWrites");

export function piWebSettingsPath(agentDir: string = defaultAgentDir()): string {
  return join(agentDir, "pi-web-settings.json");
}

/**
 * The file's record; a missing, malformed or non-object file reads as empty
 * (never throws). A caller about to write refuses a malformed file itself.
 */
export function readPiWebSettingsRecord(agentDir?: string): Record<string, unknown> {
  try {
    const parsed: unknown = JSON.parse(readFileSync(piWebSettingsPath(agentDir), "utf8"));
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed)
      ? parsed as Record<string, unknown>
      : {};
  } catch {
    return {};
  }
}

/**
 * One serialized read-merge-write on pi-web-settings.json: `merge` receives
 * the current record and answers the next one; every key it does not name
 * stays as it was. A file that exists but is not a JSON object is refused
 * (thrown), never overwritten. Resolves to the record as stored.
 */
export async function patchPiWebSettings(
  merge: (record: Record<string, unknown>) => Record<string, unknown>,
  agentDir?: string,
): Promise<Record<string, unknown>> {
  const path = piWebSettingsPath(agentDir);
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
    const next = { version: 1, ...merge(current) };
    mkdirSync(dirname(path), { recursive: true });
    writePrivateFileAtomicSync(path, `${JSON.stringify(next, null, 2)}\n`);
    return next;
  });
}
