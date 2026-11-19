"use client";

// General's 环境 pane (polyforge#149, #85 85a1b, AC2): the per-harness
// environment variable names, read through the 85a1 adapter routes — names
// only, never a value (the adapters' allow-list already answers names only;
// the pane's own copy says so too). One read per activation of the pane, no
// polling. The presentational half takes its rows as props, so the unit test
// pins the loaded look; the container is the only piece that fetches.
import { useEffect, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { envGroup, type HarnessEnvGroup } from "@/lib/harness-settings-view";

/** One harness's group as the pane shows it: its keys, or why they did not read. */
export interface HarnessEnvGroupView extends HarnessEnvGroup {
  error: string | null;
}

/** The pane's loaded look: one group per harness, names only. */
export function HarnessEnvironmentGroups({ groups, listError }: { groups: HarnessEnvGroupView[]; listError?: string | null }) {
  const { t } = useI18n();
  return (
    <section className="settings-general-section">
      <h3 className="settings-general-heading">{t("settings.harnessEnvTitle")}</h3>
      <p className="settings-general-description">{t("settings.harnessEnvNote")}</p>
      {listError && <p role="alert" className="settings-general-error">{t("settings.harnessListFailed", { error: listError })}</p>}
      <div className="settings-harness-env-groups">
        {groups.map((group) => (
          <div key={group.harness} data-harness={group.harness} className="settings-harness-env-group">
            <h4 className="settings-general-heading">{group.harness}</h4>
            {group.error ? (
              <p role="alert" className="settings-general-error">{t("settings.harnessEnvFailed", { harness: group.harness, error: group.error })}</p>
            ) : group.keys.length === 0 ? (
              <p className="settings-general-description">{t("settings.harnessEnvEmpty")}</p>
            ) : (
              <ul className="settings-harness-env-keys">
                {group.keys.map((name) => <li key={name}><code>{name}</code></li>)}
              </ul>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

/** The harness list entry the /api/harness route answers. */
interface HarnessListEntry {
  id: string;
}

export function HarnessEnvironmentSettings({ cwd }: { cwd: string | null }) {
  const { t } = useI18n();
  const [groups, setGroups] = useState<HarnessEnvGroupView[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);

  // One read per mount (the pane unmounts on switch, so each activation
  // reads once — the settings pages' cadence, never a poll).
  useEffect(() => {
    let cancelled = false;
    const read = async () => {
      const next: HarnessEnvGroupView[] = [];
      setListError(null);
      try {
        const query = cwd ? `?cwd=${encodeURIComponent(cwd)}` : "";
        const listResponse = await fetch(`/api/harness${query}`);
        const list = await listResponse.json() as { harnesses?: HarnessListEntry[] } | null;
        const entries = listResponse.ok && Array.isArray(list?.harnesses) ? list!.harnesses! : [];
        for (const entry of entries) {
          try {
            const response = await fetch(`/api/harness/${encodeURIComponent(entry.id)}/env${query}`);
            const body = await response.json() as { data?: unknown } | null;
            if (!response.ok || !body?.data) throw new Error(`HTTP ${response.status}`);
            const group = envGroup(entry.id, body.data as { target: string; setKeys: string[] } | null);
            if (group) next.push({ ...group, error: null });
          } catch (cause) {
            next.push({ harness: entry.id, target: entry.id, keys: [], error: cause instanceof Error ? cause.message : String(cause) });
          }
        }
      } catch (cause) {
        // The list itself refused: its visible reason, no groups (r1#8).
        if (!cancelled) {
          setListError(cause instanceof Error ? cause.message : String(cause));
          setGroups([]);
          return;
        }
      }
      if (!cancelled) setGroups(next);
    };
    void read();
    return () => { cancelled = true; };
  }, [cwd]);

  if (groups === null) {
    return (
      <section className="settings-general-section">
        <h3 className="settings-general-heading">{t("settings.harnessEnvTitle")}</h3>
        <p className="settings-general-description">{t("settings.harnessEnvReading")}</p>
      </section>
    );
  }
  return <HarnessEnvironmentGroups groups={groups} listError={listError} />;
}
