"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { modelReferenceImpact } from "@/lib/model-reference-impact";
import { useSettingsDraft } from "./SettingsDrafts";
import { useI18n } from "@/hooks/useI18n";
import { ConfigButton, ConfigDetailHeader, ConfigDetailHeaderInfo, ConfigDetailTitle, ConfigDetailActions, ConfigEmptyState, ConfigErrorNotice, ConfigSkeleton } from "./SettingsUi";

/**
 * The models page's Claude section (85a3-3): the editor of claude's own
 * native modelPicker custom names, and the home of the retired
 * self-stored customModels' one-time migration. Entering reads only the
 * current list. An explicit check/migrate action fires the protected POST:
 * an empty native target migrates at once, a non-empty
 * one answers the merge preview below (the native rows stay, only the
 * missing names append, nothing written until「合并」), and illegal data or an
 * unknown claude version shows its reason with nothing written. The editor
 * lists the current names and writes every add or remove as the full next
 * list through the adapter's PUT (the shared version gate and the 2.1.242
 * key floor refuse with a visible reason); a managed modelPicker that
 * shadows the user target shows「已保存、未生效」(Q6), never a write to the
 * higher layer.
 */
type MigrationPreview = {
  native: { model: string; label?: string }[];
  missing: string[];
  digest: string;
  shadowed: boolean;
};

/** The section's read states (polyforge#271 A4): skeleton, then either the
 *  list, the empty state with its add action, or an alert with a retry.
 *  Hook-free so the states render on their own. */
export function ClaudeCustomReadStates({ loading, readError, t, onRetry, onAdd }: {
  loading: boolean;
  readError: string | null;
  t: (key: string) => string;
  onRetry: () => void;
  onAdd?: () => void;
}) {
  if (loading) {
    return <div data-claude-models-loading=""><ConfigSkeleton label={t("models.claudeCustomLoading")} lines={3} /></div>;
  }
  if (readError) {
    return (
      <div data-claude-models-retry="">
        <ConfigErrorNotice
          summary={t("models.claudeCustomReadFailed")}
          details={readError}
          detailsLabel={t("i18n.detailsLabel")}
          action={<ConfigButton size="small" onClick={onRetry}>{t("i18n.retry")}</ConfigButton>}
        />
      </div>
    );
  }
  if (onAdd) {
    return (
      <ConfigEmptyState action={<ConfigButton data-claude-models-add="" onClick={onAdd}>{t("models.claudeCustomEmptyAdd")}</ConfigButton>}>
        {t("models.claudeCustomEmpty")}
      </ConfigEmptyState>
    );
  }
  return null;
}

export function ClaudeCustomModels({ onRefresh, cwd = null, onReveal }: { onReveal?: () => void; onRefresh?: () => void; cwd?: string | null }) {
  const { t } = useI18n();
  const [names, setNames] = useState<string[] | null>(null);
  const [readError, setReadError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [preview, setPreview] = useState<MigrationPreview | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [shadowed, setShadowed] = useState(false);
  const [input, setInput] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // The parent's refresh callback may change identity every render (the
  // models page passes an inline bump); the mount effect must not refire on
  // that, so it goes through a ref.
  const refreshRef = useRef(onRefresh);
  refreshRef.current = onRefresh;

  const load = useCallback(async () => {
    setReadError(null);
    try {
      const response = await fetch("/api/harness/claude/model");
      const data = await response.json();
      if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
      // r1#1: the editor's list is the user layer's own names — the merged
      // customModels view is display-only, so a managed picker's names never
      // enter this list nor a PUT body.
      setNames(Array.isArray(data.data?.userCustomModels) ? data.data.userCustomModels : []);
    } catch (cause) {
      // polyforge#271 A4: a failed read ends loading as an alert with a retry —
      // it is never the empty list, so `names` stays null.
      setReadError(cause instanceof Error ? cause.message : String(cause));
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const checkMigration = useCallback(async () => {
    setBusy(true);
    setNotice(null);
    try {
      const response = await fetch("/api/claude-models-migration", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      const data = await response.json();
      if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
      setShadowed(data.shadowed === true);
      setPreview(data.status === "preview"
        ? { native: data.native, missing: data.missing, digest: data.digest, shadowed: data.shadowed === true }
        : null);
      if (data.status === "blocked" || data.status === "deferred") {
        setNotice(typeof data.reason === "string" ? data.reason : String(data.status));
      } else if (data.status === "done" && data.changed === true) {
        await load();
        refreshRef.current?.();
      }
    } catch (cause) {
      setNotice(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }, [load]);

  const confirmMerge = useCallback(async () => {
    if (preview === null) return;
    setBusy(true);
    setError(null);
    try {
      const response = await fetch("/api/claude-models-migration", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ confirm: preview.digest }),
      });
      const data = await response.json();
      if (response.status === 409 && data.status === "conflict") {
        // The source or target changed while the user looked: the route
        // answers 409 with the fresh preview riding along (never a silent
        // merge) — it is not an error; the banner shows the new state and
        // the next「合并」carries the new digest (r2#1: this check runs before
        // the !response.ok throw below, which used to eat it).
        setPreview({ native: data.native, missing: data.missing, digest: data.digest, shadowed: data.shadowed === true });
        setShadowed(data.shadowed === true);
        return;
      }
      if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
      setPreview(null);
      setShadowed(data.shadowed === true);
      await load();
      onRefresh?.();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }, [preview, load, onRefresh]);

  const writeNames = useCallback(async (next: string[]) => {
    setBusy(true);
    setError(null);
    try {
      const removed = (names ?? []).filter((name) => !next.includes(name));
      if (removed.length) {
        const references = await modelReferenceImpact(removed, cwd, undefined, "claude");
        if (references.length) throw new Error(t("models.referenceBlocked", { references: references.join("; ") }));
      }
      const response = await fetch("/api/harness/claude/model", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ customModels: next }),
      });
      const data = await response.json();
      if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
      setNames(next);
      onRefresh?.();
      return true;
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      return false;
    } finally {
      setBusy(false);
    }
  }, [onRefresh, names, cwd, t]);

  const add = useCallback(async () => {
    const name = input.trim();
    if (name === "" || names === null || names.includes(name)) return;
    if (await writeNames([...names, name])) setInput("");
  }, [input, names, writeNames]);
  const managed = useSettingsDraft({ label: t("models.claudeCustomHeading"), reveal: onReveal, count: input.trim() ? 1 : 0, busy,
    canSave: names !== null && Boolean(input.trim()) && !names.includes(input.trim()), save: () => void add(), discard: () => setInput("") });

  return (
    <div data-claude-custom-models className="settings-claude-models">
      <ConfigDetailHeader>
        <ConfigDetailHeaderInfo><ConfigDetailTitle>{t("models.claudeCustomHeading")}</ConfigDetailTitle></ConfigDetailHeaderInfo>
        <ConfigDetailActions><ConfigButton disabled={busy} onClick={() => void checkMigration()}>{t("models.claudeCustomCheckMigration")}</ConfigButton></ConfigDetailActions>
      </ConfigDetailHeader>
      {notice !== null && (
        <p role="note" className="settings-general-description">
          {t("models.claudeCustomBlocked")}
          <code>{notice}</code>
        </p>
      )}
      {preview !== null && (
        <div data-claude-migration-preview className="settings-claude-preview">
          <p className="settings-general-description">{t("models.claudeCustomPreviewTitle")}</p>
          <p className="settings-general-description">{t("models.claudeCustomNativeKept")}</p>
          <ul data-claude-preview-native className="settings-claude-names">
            {preview.native.map((row) => (
              <li key={row.model}>{row.label !== undefined ? `${row.model} (${row.label})` : row.model}</li>
            ))}
          </ul>
          <p className="settings-general-description">{t("models.claudeCustomMissing")}</p>
          <ul data-claude-preview-missing className="settings-claude-names">
            {preview.missing.map((model) => <li key={model}>{model}</li>)}
          </ul>
          <div className="settings-claude-actions">
            <ConfigButton onClick={() => void confirmMerge()} disabled={busy}>{t("models.claudeCustomMerge")}</ConfigButton>
            <ConfigButton onClick={() => setPreview(null)}>{t("common.cancel")}</ConfigButton>
          </div>
        </div>
      )}
      {(readError !== null || names === null || names.length === 0) ? (
        <ClaudeCustomReadStates
          loading={names === null && readError === null}
          readError={readError}
          t={t}
          onRetry={() => void load()}
          onAdd={names !== null && names.length === 0 ? () => inputRef.current?.focus() : undefined}
        />
      ) : (
        <ul data-claude-custom-list className="settings-claude-names">
          {names.map((model) => (
            <li key={model}>
              <code>{model}</code>
              <ConfigButton
                size="small"
                variant="ghost"
                aria-label={t("models.claudeCustomRemove", { model })}
                disabled={busy}
                onClick={() => void writeNames(names.filter((entry) => entry !== model))}
              >
                ✕
              </ConfigButton>
            </li>
          ))}
        </ul>
      )}
      {shadowed && <p role="note" className="settings-general-description">{t("models.claudeCustomShadowed")}</p>}
      <div className="settings-claude-add">
      <input
        ref={inputRef}
        className="settings-claude-model-input"
        value={input}
        placeholder={t("models.claudeCustomPlaceholder")}
        aria-label={t("models.claudeCustomPlaceholder")}
        disabled={busy || names === null}
        onChange={(event) => setInput(event.target.value)}
      />
      {!managed && <ConfigButton onClick={add} disabled={busy || input.trim() === "" || names === null}>
        {t("models.claudeCustomAdd")}
      </ConfigButton>}
      </div>
      {error !== null && <p role="alert" className="config-add-source-error">{error}</p>}
    </div>
  );
}
