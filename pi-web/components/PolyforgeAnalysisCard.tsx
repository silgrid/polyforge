"use client";

import { useI18n } from "@/hooks/useI18n";
import { primarySuggestedAction, type PolyforgeAnalysis } from "@/lib/polyforge-messages-pure";

/**
 * One message's structured analysis card, rendered under the
 * message it analyzes in place of the retired one-line gray text. The
 * header — the localized type badge (never the raw enum), `model · duration`
 * and the expand/collapse control — and the action row are always shown; the
 * body (the summary and the link that opens the task in Work › Tasks) only
 * while expanded. The action row always carries 【建票】 and 【回复】: the
 * suggested one is the primary button, the other secondary, and both are
 * secondary for `note_only` or no suggestion. Its data is the hook's
 * page-memory analysis; the collapsed state is the panel's component memory.
 * It writes nothing: every action is handed up to the panel.
 */

const PRIMARY = { background: "var(--accent)", color: "var(--bg-panel)", border: "1px solid var(--accent)", fontWeight: 600 } as const;
const SECONDARY = { background: "none", color: "var(--text)", border: "1px solid var(--border)", fontWeight: 400 } as const;
export function PolyforgeAnalysisCard({
  analysis,
  expanded,
  onToggle,
  onFileTicket,
  onReply,
  onOpenTask,
}: {
  analysis: PolyforgeAnalysis;
  expanded: boolean;
  onToggle: () => void;
  onFileTicket: () => void;
  onReply: () => void;
  onOpenTask: (taskId: string) => void;
}) {
  const { t } = useI18n();
  const primary = primarySuggestedAction(analysis.suggested_actions);
  const action = (kind: "file_ticket" | "reply", label: string, onClick: () => void) => (
    <button
      type="button"
      data-pf-analysis-action={kind}
      onClick={(event) => { event.stopPropagation(); onClick(); }}
      style={{ ...(primary === kind ? PRIMARY : SECONDARY), borderRadius: 4, padding: "2px 10px", fontSize: 11, cursor: "pointer" }}
    >
      {label}
    </button>
  );
  const facts = [analysis.model, analysis.duration].filter((fact) => fact !== "").join(" · ");
  return (
    <div
      data-pf-analysis={analysis.message_id}
      style={{
        display: "flex", flexDirection: "column", gap: 4, marginTop: 4, padding: "6px 8px",
        border: "1px solid var(--border)", borderRadius: 6, background: "var(--bg-panel)", cursor: "default",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
        <span style={{
          flexShrink: 0, padding: "0 6px", borderRadius: 8, fontSize: 10, fontWeight: 600, lineHeight: "16px",
          border: "1px solid var(--border)", color: "var(--text)", background: "var(--bg-hover)",
        }}>
          {t(`polyforge.messages.analysisType.${analysis.type}`)}
        </span>
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 10, color: "var(--text-dim)" }}>
          {facts}
        </span>
        <button
          type="button"
          aria-expanded={expanded}
          aria-label={expanded ? t("polyforge.messages.analysisCollapse") : t("polyforge.messages.analysisExpand")}
          title={expanded ? t("polyforge.messages.analysisCollapse") : t("polyforge.messages.analysisExpand")}
          onClick={(event) => { event.stopPropagation(); onToggle(); }}
          style={{ flexShrink: 0, background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", padding: 0, display: "inline-flex" }}
        >
          <svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d={expanded ? "m18 15-6-6-6 6" : "m6 9 6 6 6-6"} />
          </svg>
        </button>
      </div>
      <div hidden={!expanded} style={{ fontSize: 12, color: "var(--text)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
        {analysis.summary}
      </div>
      <button
        type="button"
        hidden={!expanded}
        onClick={(event) => { event.stopPropagation(); onOpenTask(analysis.task_id); }}
        style={{ alignSelf: "flex-start", background: "none", border: "none", padding: 0, color: "var(--accent)", cursor: "pointer", font: "inherit", fontSize: 11, textDecoration: "underline" }}
      >
        {t("polyforge.messages.analysisTask", { id: analysis.task_id })}
      </button>
      <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
        {action("file_ticket", t("polyforge.messages.actionFileTicket"), onFileTicket)}
        {action("reply", t("polyforge.messages.reply"), onReply)}
      </div>
    </div>
  );
}
