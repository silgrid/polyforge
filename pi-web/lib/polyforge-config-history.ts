import { chmodSync, existsSync, mkdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import lockfile from "proper-lockfile";
import { writePrivateFileAtomicSync } from "./atomic-file";

/**
 * The panel's bounded write history: one private JSON file
 * under pi's agent dir, newest first, at most 50 entries. api_key entries
 * carry set/unset booleans only — no value ever reaches this file.
 */

export const HISTORY_LIMIT = 50;

export interface PolyforgeConfigHistoryEntry {
  id: string;
  at: string;
  scope: "cli" | "workspace";
  cwd: string;
  key: string;
  old: unknown;
  new: unknown;
  secret: boolean;
  rollbackable: boolean;
  /** the file a scoped write addressed — the Host (`host`)
   *  or one Cell (`profile`). A rollback writes the same file back.
   * every write records one (`profile: "default"` for the
   *  default Cell); an entry without either is the default Cell's. */
  host?: true;
  profile?: string;
}

interface HistoryDocument {
  version: 1;
  entries: PolyforgeConfigHistoryEntry[];
}

export function historyPath(agentDir = getAgentDir()): string {
  return join(agentDir, "polyforge-config-history.json");
}

export function readPolyforgeConfigHistory(agentDir?: string): PolyforgeConfigHistoryEntry[] {
  const path = historyPath(agentDir);
  try {
    const parsed = JSON.parse(readFileSync(path, "utf8")) as Partial<HistoryDocument>;
    if (!Array.isArray(parsed.entries)) return [];
    return parsed.entries.filter((entry): entry is PolyforgeConfigHistoryEntry =>
      Boolean(entry && typeof entry === "object" && typeof entry.id === "string" && typeof entry.key === "string"))
      .slice(0, HISTORY_LIMIT);
  } catch {
    return [];
  }
}

/** Read-modify-write under the same lock the file itself takes. */
export async function appendPolyforgeConfigHistory(
  entry: PolyforgeConfigHistoryEntry,
  agentDir?: string,
): Promise<PolyforgeConfigHistoryEntry[]> {
  const path = historyPath(agentDir);
  mkdirSync(dirname(path), { recursive: true });
  // proper-lockfile locks an existing target; a fresh file starts empty.
  if (!existsSync(path)) writePrivateFileAtomicSync(path, JSON.stringify({ version: 1, entries: [] }));
  const release = await lockfile.lock(path, { realpath: false, retries: 10 });
  try {
    const current = readPolyforgeConfigHistory(agentDir);
    const entries = [entry, ...current].slice(0, HISTORY_LIMIT);
    writePrivateFileAtomicSync(path, JSON.stringify({ version: 1, entries }, null, 2));
    chmodSync(path, 0o600);
    return entries;
  } finally {
    await release();
  }
}
