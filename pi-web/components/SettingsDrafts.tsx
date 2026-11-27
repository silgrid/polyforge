"use client";

import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { useI18n } from "@/hooks/useI18n";

type DraftAction = { save: () => void; discard: () => void; reveal: () => void };
type DraftEntry = { id: string; section: string; label: string; count: number; busy: boolean; canSave: boolean; canDiscard: boolean; saveLabel?: string; actions: DraftAction };
type DraftRegistry = { update: (entry: DraftEntry) => void; remove: (id: string) => void };
const DraftContext = createContext<DraftRegistry | null>(null);
export const SettingsDraftScope = createContext({ section: "", label: "" });

/** Composite editors register each row separately and share the same footer. */
export function useSettingsDraftManaged() { return useContext(DraftContext) !== null; }

/** Editors retain ownership of their draft and native save boundary. No secrets enter this registry. */
export function useSettingsDraft({ label, count, busy = false, canSave = true, canDiscard = true, saveLabel, reveal, save, discard }: {
  label?: string; count: number; busy?: boolean; canSave?: boolean; canDiscard?: boolean; saveLabel?: string; reveal?: () => void; save: () => void; discard: () => void;
}) {
  const registry = useContext(DraftContext);
  const scope = useContext(SettingsDraftScope);
  const id = useId();
  const current = useRef({ save, discard, reveal });
  useEffect(() => { current.current = { save, discard, reveal }; });
  const actions = useMemo(() => ({ save: () => current.current.save(), discard: () => current.current.discard(), reveal: () => current.current.reveal?.() }), []);
  const name = label ?? scope.label;
  useEffect(() => {
    if (count > 0) registry?.update({ id, section: scope.section, label: name, count, busy, canSave, canDiscard, saveLabel, actions });
    else registry?.remove(id);
  }, [registry, id, scope.section, name, count, busy, canSave, canDiscard, saveLabel, actions]);
  useEffect(() => () => registry?.remove(id), [registry, id]);
  return registry !== null;
}

export function SettingsDraftRegistration(props: Parameters<typeof useSettingsDraft>[0]) {
  useSettingsDraft(props);
  return null;
}

export function SettingsDraftProvider({ children, onDirtyChange, onNavigate }: {
  children: ReactNode; onDirtyChange: (dirty: boolean) => void; onNavigate: (section: string) => void;
}) {
  const { t } = useI18n();
  const [entries, setEntries] = useState<Record<string, DraftEntry>>({});
  const update = useCallback((entry: DraftEntry) => setEntries((previous) => ({ ...previous, [entry.id]: entry })), []);
  const remove = useCallback((id: string) => setEntries((previous) => {
    if (!(id in previous)) return previous;
    const next = { ...previous }; delete next[id]; return next;
  }), []);
  const registry = useMemo(() => ({ update, remove }), [update, remove]);
  const drafts = Object.values(entries);
  const count = drafts.reduce((sum, entry) => sum + entry.count, 0);
  useEffect(() => { onDirtyChange(count > 0); }, [count, onDirtyChange]);
  useEffect(() => {
    if (!count) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [count]);
  return <DraftContext.Provider value={registry}>
    {children}
    {count > 0 && <aside className="settings-draft-summary" aria-label={t("settings.draftSummary")} data-settings-drafts="">
      <details open>
        <summary>{t("settings.draftCount", { count })}</summary>
        <p>{t("settings.draftBoundaries")}</p>
        <div className="settings-draft-items">{drafts.map((entry) => <div key={entry.id} className="settings-draft-item">
          <button type="button" className="settings-draft-link" onClick={() => { onNavigate(entry.section); entry.actions.reveal(); }}>{entry.label} · {entry.count}</button>
          <button type="button" className="config-button config-button-secondary config-button-small" disabled={entry.busy || !entry.canDiscard} onClick={entry.actions.discard}>{t("settings.discard")}</button>
          <button type="button" className="config-button config-button-primary config-button-small" disabled={entry.busy || !entry.canSave} onClick={() => { onNavigate(entry.section); entry.actions.reveal(); entry.actions.save(); }}>{entry.busy ? t("i18n.saving") : entry.saveLabel ?? t("i18n.save")}</button>
        </div>)}</div>
      </details>
    </aside>}
  </DraftContext.Provider>;
}
