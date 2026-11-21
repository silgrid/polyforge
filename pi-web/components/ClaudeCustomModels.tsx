"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useI18n } from "@/hooks/useI18n";

/**
 * The models page's Claude section (85a3-3): the editor of claude's own
 * native modelPicker custom names, and the home of the retired
 * self-stored customModels' one-time migration. Entering the section fires
 * the protected migration POST once (Q2: the page's reads stay
 * side-effect-free) — an empty native target migrates at once, a non-empty
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

export function ClaudeCustomModels({ onRefresh }: { onRefresh?: () => void }) {
  const { t } = useI18n();
  const [names, setNames] = useState<string[] | null>(null);
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
    try {
      const response = await fetch("/api/harness/claude/model");
      const data = await response.json();
      if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
      setNames(Array.isArray(data.data?.customModels) ? data.data.customModels : []);
    } catch (cause) {
      // A failed read must not leave the section loading forever (the page's
      // other scenarios read its text): the editor offers the empty state
      // with the reason beside it.
      setNames([]);
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const response = await fetch("/api/claude-models-migration", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: "{}",
        });
        const data = await response.json();
        if (cancelled) return;
        if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
        setShadowed(data.shadowed === true);
        if (data.status === "preview") {
          setPreview({ native: data.native, missing: data.missing, digest: data.digest, shadowed: data.shadowed === true });
        } else if (data.status === "blocked" || data.status === "deferred") {
          setNotice(typeof data.reason === "string" ? data.reason : String(data.status));
        } else if (data.status === "done" && data.changed === true) {
          // An automatic migration on entry changed the picker: the page's own
          // claude rows (fetched beside this POST) must re-read (AC2's 列表刷新).
          refreshRef.current?.();
        }
        await load();
      } catch (cause) {
        if (!cancelled) setNotice(cause instanceof Error ? cause.message : String(cause));
      }
    })();
    return () => { cancelled = true; };
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
      if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
      if (data.status === "conflict") {
        // The source or target changed while the user looked: the fresh
        // preview rides along, never a silent merge.
        setPreview({ native: data.native, missing: data.missing, digest: data.digest, shadowed: data.shadowed === true });
        return;
      }
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
      const response = await fetch("/api/harness/claude/model", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ customModels: next }),
      });
      const data = await response.json();
      if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
      setNames(next);
      onRefresh?.();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }, [onRefresh]);

  const add = useCallback(() => {
    const name = input.trim();
    if (name === "" || names === null || names.includes(name)) return;
    void writeNames([...names, name]);
    setInput("");
  }, [input, names, writeNames]);

  return (
    <div data-claude-custom-models>
      <h4>{t("models.claudeCustomHeading")}</h4>
      {notice !== null && (
        <p role="note">
          {t("models.claudeCustomBlocked")}
          <code>{notice}</code>
        </p>
      )}
      {preview !== null && (
        <div data-claude-migration-preview>
          <p>{t("models.claudeCustomPreviewTitle")}</p>
          <p>{t("models.claudeCustomNativeKept")}</p>
          <ul data-claude-preview-native>
            {preview.native.map((row) => (
              <li key={row.model}>{row.label !== undefined ? `${row.model} (${row.label})` : row.model}</li>
            ))}
          </ul>
          <p>{t("models.claudeCustomMissing")}</p>
          <ul data-claude-preview-missing>
            {preview.missing.map((model) => <li key={model}>{model}</li>)}
          </ul>
          {preview.shadowed && <p role="note">{t("models.claudeCustomShadowed")}</p>}
          <button type="button" onClick={() => void confirmMerge()} disabled={busy}>{t("models.claudeCustomMerge")}</button>
          <button type="button" onClick={() => setPreview(null)}>{t("common.cancel")}</button>
        </div>
      )}
      {names === null ? (
        <p>{t("models.claudeCustomLoading")}</p>
      ) : names.length === 0 ? (
        <p>{t("models.claudeCustomEmpty")}</p>
      ) : (
        <ul data-claude-custom-list>
          {names.map((model) => (
            <li key={model}>
              <code>{model}</code>
              <button
                type="button"
                aria-label={t("models.claudeCustomRemove", { model })}
                disabled={busy}
                onClick={() => void writeNames(names.filter((entry) => entry !== model))}
              >
                ✕
              </button>
            </li>
          ))}
        </ul>
      )}
      {shadowed && <p role="note">{t("models.claudeCustomShadowed")}</p>}
      <input
        value={input}
        placeholder={t("models.claudeCustomPlaceholder")}
        aria-label={t("models.claudeCustomPlaceholder")}
        disabled={busy || names === null}
        onChange={(event) => setInput(event.target.value)}
      />
      <button type="button" onClick={add} disabled={busy || input.trim() === "" || names === null}>
        {t("models.claudeCustomAdd")}
      </button>
      {error !== null && <p role="alert" style={{ color: "#dc2626" }}>{error}</p>}
    </div>
  );
}
