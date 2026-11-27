"use client";

import { useEffect, useId, useRef, useState } from "react";
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
 *
 * The onboarding wizard's first-ticket step (polyforge#167 Q5) reuses this
 * one dialog without a source message: `analysis`/`message` are optional
 * (blank goal/content, the first mapped project) and `lane` names the
 * ticket's lane (`develop`). The lane rides a request header, so the
 * Messages path's body stays exactly `{goal, project, content}` and its one
 * flag create is unchanged.
 */
export function PolyforgeFileTicketDialog({
  analysis,
  message,
  projects,
  lane,
  embedded = false,
  onBusyChange,
  onClose,
  onCreated,
}: {
  analysis?: PolyforgeAnalysis | null;
  message?: Pick<PolyforgeMessage, "id" | "text"> | null;
  /** The `cli.toml`-mapped projects (the Messages project groups). */
  projects: string[];
  /** The ticket's lane (bundle name); absent on the Messages path. */
  lane?: string;
  /** Reuse the form in setup's content scroller and single footer. */
  embedded?: boolean;
  onBusyChange?: (busy: boolean) => void;
  onClose: () => void;
  onCreated: (slug: string) => void;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const formRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!embedded) return;
    const opener = document.activeElement as HTMLElement | null;
    formRef.current?.focus();
    return () => {
      if (opener?.isConnected) opener.focus();
      else document.querySelector<HTMLElement>("[data-onboarding-ticket-open]")?.focus();
    };
  }, [embedded]);
  const [defaults] = useState(() => fileTicketDialogDefaults(analysis, message, projects));
  const [goal, setGoal] = useState(defaults.goal);
  const [project, setProject] = useState(defaults.project);
  const [content, setContent] = useState(defaults.content);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // The onboarding wizard opens this dialog before its mapped-projects read
  // settles (polyforge#167 Q5): the select then starts on the first project
  // that arrives, so a confirm never sends a blank project.
  useEffect(() => {
    if (project === "" && projects.length > 0) setProject(projects[0]);
  }, [project, projects]);

  useEffect(() => {
    if (embedded) return;
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape" && !busy) onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [busy, embedded, onClose]);

  const confirm = async () => {
    if (busy) return;
    setBusy(true);
    onBusyChange?.(true);
    setError(null);
    try {
      const response = await fetch("/api/polyforge/messages/file-ticket", {
        method: "POST",
        headers: { "content-type": "application/json", ...(lane ? { "x-polyforge-lane": lane } : {}) },
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
      onBusyChange?.(false);
    }
  };

  const field = { background: "var(--bg)", color: "var(--text)", border: "1px solid var(--border)", borderRadius: 4, padding: "4px 6px", fontSize: 12, font: "inherit" } as const;
  const label = { display: "flex", flexDirection: "column", gap: 2, fontSize: 11, color: "var(--text-dim)" } as const;
  const fields = <>
    <label style={embedded ? undefined : label}>
      {t("polyforge.messages.ticketGoal")}
      <input value={goal} onChange={(event) => setGoal(event.target.value)} style={embedded ? undefined : field} />
    </label>
    <label style={embedded ? undefined : label}>
      {t("polyforge.messages.ticketProject")}
      <select value={project} onChange={(event) => setProject(event.target.value)} style={embedded ? undefined : field}>
        {projects.map((name) => <option key={name} value={name}>{name}</option>)}
      </select>
    </label>
    <label style={embedded ? undefined : label}>
      {t("polyforge.messages.ticketContent")}
      <textarea value={content} onChange={(event) => setContent(event.target.value)} rows={embedded ? 5 : 8} style={embedded ? undefined : { ...field, resize: "vertical" }} />
    </label>
    {error && <div role="alert" className={embedded ? "onboarding-error" : undefined} style={embedded ? undefined : { fontSize: 11, color: "#dc2626", overflowWrap: "anywhere" }}>{error}</div>}
  </>;
  if (embedded) return <div ref={formRef} tabIndex={-1} data-onboarding-ticket-form data-lane={lane ?? "none"} className="onboarding-ticket-embedded">
    <div className="onboarding-content">
      <header className="onboarding-step-heading"><h2>{t("onboarding.stepTicket")}</h2><p>{t("onboarding.ticketHint")}</p></header>
      <div className="onboarding-form">{fields}</div>
    </div>
    <footer className="onboarding-footer">
      <button type="button" className="onboarding-skip" data-onboarding-ticket-cancel onClick={onClose} disabled={busy}>{t("polyforge.messages.ticketCancel")}</button>
      <button type="button" className="onboarding-primary" onClick={() => void confirm()} disabled={busy} aria-busy={busy}>{t("polyforge.messages.ticketConfirm")}</button>
    </footer>
  </div>;
  return (
    <div
      onClick={(event) => { event.stopPropagation(); if (event.target === event.currentTarget && !busy) onClose(); }}
      style={{ position: "fixed", inset: 0, zIndex: 1000, background: "rgba(0, 0, 0, 0.35)", display: "flex", alignItems: "center", justifyContent: "center", padding: 16 }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        data-lane={lane ?? "none"}
        style={{ width: "min(520px, 100%)", maxHeight: "100%", overflowY: "auto", background: "var(--bg-panel)", color: "var(--text)", border: "1px solid var(--border)", borderRadius: 8, padding: 12, display: "flex", flexDirection: "column", gap: 8 }}
      >
        <div id={titleId} style={{ fontSize: 13, fontWeight: 600 }}>{t("polyforge.messages.ticketTitle")}</div>
        {fields}
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
