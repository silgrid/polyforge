import type { SessionManager } from "@earendil-works/pi-coding-agent";
import { isAgentLanguage, type AgentLanguage } from "./agent-language";
import type { SessionEntry } from "./types";

/**
 * A pi session's Agent language, fixed when the session is created:
 * a versioned custom entry, like `pi-web:tool-selection`. Custom entries never reach
 * the model, and compaction keeps them in the file.
 */
export const SESSION_AGENT_LANGUAGE_TYPE = "pi-web:agent-language";

export interface SessionAgentLanguageData {
  version: 1;
  language: AgentLanguage;
}

/** The newest valid entry's language; a session with none (pre-feature) or only invalid ones is English. */
export function readSessionAgentLanguage(entries: readonly SessionEntry[]): AgentLanguage {
  for (let index = entries.length - 1; index >= 0; index -= 1) {
    const entry = entries[index];
    if (entry.type !== "custom" || entry.customType !== SESSION_AGENT_LANGUAGE_TYPE) continue;
    const data = entry.data as { version?: unknown; language?: unknown } | null | undefined;
    if (typeof data === "object" && data !== null && data.version === 1 && isAgentLanguage(data.language)) {
      return data.language;
    }
  }
  return "en";
}

export function appendSessionAgentLanguage(sessionManager: SessionManager, language: AgentLanguage): void {
  sessionManager.appendCustomEntry(SESSION_AGENT_LANGUAGE_TYPE, {
    version: 1,
    language,
  } satisfies SessionAgentLanguageData);
}
