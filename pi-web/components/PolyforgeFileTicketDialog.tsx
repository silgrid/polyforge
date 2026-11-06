"use client";

import { useEffect, useId, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import {
  fileTicketDialogDefaults,
  type PolyforgeAnalysis,
  type PolyforgeMessage,
} from "@/lib/polyforge-messages-pure";

type FileTicketResponse = { ok?: boolean; slug?: string; error?: string };

/**
 * The 【建票】 confirmation dialog. It opens prefilled — the
 * goal from the analysis's suggested next line, the project select over the
 * mapped projects defaulting to the analysis's project, the content with the
 * source message reference and the summary — and every field stays
 * editable. Cancel, the close control, Escape and the backdrop write
 * nothing. Confirm runs exactly one `POST /api/polyforge/messages/file-ticket`
 * (one `pf file create` without `--bundle`: a registration ticket); success
 * hands the created slug up (the panel closes the dialog and names it), a
 * refusal keeps the dialog open with pf's text and is never retried.
 */
export function PolyforgeFileTicketDialog({
  analysis,
  message,
  projects,
  onClose,
  onCreated,
}: {
  analysis: PolyforgeAnalysis;
  message: Pick<PolyforgeMessage, "id" | "text">;
  /** The `cli.toml`-mapped projects (the Messages project groups). */
  projects: string[];
  onClose: () => void;
  onCreated: (slug: string) => void;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const [defaults] = useState(() => fileTicketDialogDefaults(analysis, message, projects));
  const [goal, setGoal] = useState(defaults.goal);
  const [project, setProject] = useState(defaults.project);
  const [content, setContent] = useState(defaults.content);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape" && !busy) onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [busy, onClose]);

  const confirm = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const response = await fetch("/api/polyforge/messages/file-ticket", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ goal, project, content }),
      });
      const body = await response.json() as FileTicketResponse;
      if (!response.ok || body.error || body.ok !== true || !body.slug) {
        setError(body.error ?? t("polyforge.messages.ticketFailed"));
        return;
      }
      onCreated(body.slug);
    } catch {
      setError(t("polyforge.messages.ticketFailed"));
    } finally {
      setBusy(false);
    }
  };

  const field = { background: "var(--bg)", color: "var(--text)", border: "1px solid var(--border)", borderRadius: 4, padding: "4px 6px", fontSize: 12, font: "inherit" } as const;
  const label = { display: "flex", flexDirection: "column", gap: 2, fontSize: 11, color: "var(--text-dim)" } as const;
  return (
    <div
      onClick={(event) => { event.stopPropagation(); if (event.target === event.currentTarget && !busy) onClose(); }}
      style={{ position: "fixed", inset: 0, zIndex: 1000, background: "rgba(0, 0, 0, 0.35)", display: "flex", alignItems: "center", justifyContent: "center", padding: 16 }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        style={{ width: "min(520px, 100%)", maxHeight: "100%", overflowY: "auto", background: "var(--bg-panel)", color: "var(--text)", border: "1px solid var(--border)", borderRadius: 8, padding: 12, display: "flex", flexDirection: "column", gap: 8 }}
      >
        <div id={titleId} style={{ fontSize: 13, fontWeight: 600 }}>{t("polyforge.messages.ticketTitle")}</div>
        <label style={label}>
          {t("polyforge.messages.ticketGoal")}
          <input value={goal} onChange={(event) => setGoal(event.target.value)} style={field} />
        </label>
        <label style={label}>
          {t("polyforge.messages.ticketProject")}
          <select value={project} onChange={(event) => setProject(event.target.value)} style={field}>
            {projects.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </label>
        <label style={label}>
          {t("polyforge.messages.ticketContent")}
          <textarea value={content} onChange={(event) => setContent(event.target.value)} rows={8} style={{ ...field, resize: "vertical" }} />
        </label>
        {error && <div role="alert" style={{ fontSize: 11, color: "#dc2626", overflowWrap: "anywhere" }}>{error}</div>}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <button type="button" onClick={onClose} disabled={busy}
            style={{ background: "none", color: "var(--text)", border: "1px solid var(--border)", borderRadius: 4, padding: "4px 12px", fontSize: 12, cursor: busy ? "default" : "pointer" }}>
            {t("polyforge.messages.ticketCancel")}
          </button>
          <button type="button" onClick={() => void confirm()} disabled={busy} aria-busy={busy}
            style={{ background: "var(--accent)", color: "var(--bg-panel)", border: "none", borderRadius: 4, padding: "4px 12px", fontSize: 12, cursor: busy ? "default" : "pointer" }}>
            {t("polyforge.messages.ticketConfirm")}
          </button>
        </div>
      </div>
    </div>
  );
}
