import type { ReactNode } from "react";
import { isAgentLanguage, type AgentLanguage } from "@/lib/agent-language";
import type { AgentLanguageResponse } from "@/lib/api-types";
import type { PolyforgeAgentLanguage } from "@/lib/polyforge-config-write";
import { ConfigButton } from "@/components/SettingsUi";

/**
 * Settings › General's Agent language, below its radio group: the language
 * Polyforge's step agents use (pf's default Cell `[daemon] agent_language`, as
 * `GET /api/agent-language` reports it), a hint when it differs from Pi Web's, and the one-click
 * sync. Presentational like `SettingsUi.tsx`: no hook, no state, every word a prop.
 * the hint follows pf's `source` — `file` (pf's config sets the key, never
 * overwritten automatically) or `default` (pf's config does not set it: the start's backfill did
 * not land, e.g. refused because pf's config changed at the same moment).
 */
export interface AgentLanguagePfStatusProps {
  /** Pi Web's stored Agent language. */
  language: AgentLanguage;
  /** pf's language as GET /api/agent-language reported it; null or absent: no pf line. */
  polyforge: PolyforgeAgentLanguage | null | undefined;
  /** The visible name of each Agent language. */
  languageLabels: Record<AgentLanguage, string>;
  /** The pf line's text for a language's visible name. */
  statusText: (languageLabel: string) => string;
  /** The mismatch hint when pf's config sets the key (`source` file). */
  mismatchText: string;
  /** The mismatch hint when pf's config does not set the key (`source` default). */
  unsetMismatchText: string;
  syncLabel: string;
  syncing: boolean;
  onSync: () => void;
}

export function AgentLanguagePfStatus({
  language,
  polyforge,
  languageLabels,
  statusText,
  mismatchText,
  unsetMismatchText,
  syncLabel,
  syncing,
  onSync,
}: AgentLanguagePfStatusProps): ReactNode {
  if (!polyforge) return null;
  const mismatch = polyforge.language !== language;
  return (
    <div data-agent-language-pf={polyforge.language}>
      <p className="settings-general-description">{statusText(languageLabels[polyforge.language])}</p>
      {mismatch && (
        <>
          <p role="status" className="settings-general-description">{polyforge.source === "default" ? unsetMismatchText : mismatchText}</p>
          <ConfigButton size="small" disabled={syncing} onClick={onSync} data-agent-language-pf-sync="">
            {syncLabel}
          </ConfigButton>
        </>
      )}
    </div>
  );
}

/**
 * The sync: `PUT /api/agent-language` with Pi Web's own language (the route writes pf first,
 *), then `GET /api/agent-language` for both sides afresh. Throws on any non-2xx
 * answer or a reply without a language; a refused PUT sends no GET.
 */
export async function requestAgentLanguageSync(language: AgentLanguage, fetchImpl: typeof fetch = fetch): Promise<AgentLanguageResponse> {
  const put = await fetchImpl("/api/agent-language", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ language }),
  });
  if (!put.ok) throw new Error(`HTTP ${put.status}`);
  const get = await fetchImpl("/api/agent-language");
  const data = await get.json() as Partial<AgentLanguageResponse>;
  if (!get.ok || !isAgentLanguage(data.language)) throw new Error(`HTTP ${get.status}`);
  return { language: data.language, polyforge: data.polyforge ?? null };
}
