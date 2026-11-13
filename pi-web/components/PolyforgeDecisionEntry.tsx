"use client";

import { useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import {
  decisionSourceShown,
  decisionState,
  isKnownDecisionClass,
  isOpenDecision,
  type PolyforgeDecision,
} from "@/lib/polyforge-decisions-pure";

/** A task summary longer than this folds behind 「展开」. */
const TEXT_FOLD_CHARS = 280;
const TEXT_FOLD_LINES = 4;

/** The failure family is red, other attention classes amber, the rest
 *  neutral. Display only. */
const RED_CLASSES = new Set(["failed", "step_dead", "carrier_push_failed", "submit_rejected", "submit_dropped", "capped", "interrupted"]);

function classColor(decision: PolyforgeDecision): string {
  if (RED_CLASSES.has(decision.class)) return "#dc2626";
  if (decision.attention) return "#d97706";
  return "var(--text)";
}

/**
 * One decision-feed entry, shown where its subject lives: a
 * background task's entries on its row in Work › Tasks (a work item's entries
 * are its feed-only attention card and its job card's composer).
 * It names its class (localized; an unknown class shows its
 * wire phrase), the source when it differs, the time and its state (resolved
 * and dismissed muted), the reason (the daemon's phrase), the question id /
 * repo when present, a task's summary (verbatim, folded when long) and, while
 * open, 「忽略」.
 */
export function PolyforgeDecisionEntry({
  decision,
  busy,
  onDismiss,
}: {
  decision: PolyforgeDecision;
  /** A dismiss is in flight (every entry's button waits). */
  busy: boolean;
  onDismiss: (id: number) => void;
}) {
  const { t, locale } = useI18n();
  const [textOpen, setTextOpen] = useState(false);
  const state = decisionState(decision);
  const open = isOpenDecision(decision);
  const source = decisionSourceShown(decision);
  const label = isKnownDecisionClass(decision.class) ? t(`polyforge.decisions.class.${decision.class}`) : (decision.phrase ?? decision.class);
  const reason = isKnownDecisionClass(decision.class) ? decision.phrase : undefined;
  const long = Boolean(decision.text && (decision.text.length > TEXT_FOLD_CHARS || decision.text.split("\n").length > TEXT_FOLD_LINES));
  const at = (() => {
    const date = new Date(decision.at);
    if (!decision.at || Number.isNaN(date.getTime())) return decision.at;
    try {
      return new Intl.DateTimeFormat(locale, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(date);
    } catch {
      return decision.at;
    }
  })();

  return (
    <div
      data-pf-decision={decision.id}
      data-pf-decision-class={decision.class}
      data-pf-decision-state={state}
      style={{ display: "flex", gap: 8, alignItems: "flex-start", padding: "6px 0", opacity: state === "open" ? 1 : 0.55 }}
    >
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 3 }}>
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "baseline", gap: 6, fontSize: 12 }}>
          <span data-pf-decision-label="" style={{ fontWeight: 600, color: classColor(decision) }}>{label}</span>
          {source && <span data-pf-decision-source="" style={{ fontSize: 11, color: "var(--text-dim)", fontFamily: "var(--font-mono, monospace)" }}>{source}</span>}
          <span data-pf-decision-at="" title={decision.at} style={{ fontSize: 11, color: "var(--text-dim)" }}>{at}</span>
          <span data-pf-decision-state-label="" style={{ fontSize: 11, color: open ? "#d97706" : "var(--text-dim)" }}>{t(`polyforge.decisions.state.${state}`)}</span>
        </div>
        {reason && <div data-pf-decision-reason="" style={{ fontSize: 12, color: "var(--text)", overflowWrap: "anywhere" }}>{reason}</div>}
        {(decision.question_id || decision.repo) && (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, fontSize: 11, color: "var(--text-dim)" }}>
            {decision.question_id && <span data-pf-decision-question="">{t("polyforge.decisions.question", { id: decision.question_id })}</span>}
            {decision.repo && <span data-pf-decision-repo="" style={{ overflowWrap: "anywhere" }}>{t("polyforge.decisions.repo", { repo: decision.repo })}</span>}
          </div>
        )}
        {decision.text && (
          <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
            <div
              data-pf-decision-text=""
              style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", fontSize: 12, color: "var(--text)",
                ...(long && !textOpen ? { maxHeight: "5.6em", overflow: "hidden" } : {}) }}
            >
              {decision.text}
            </div>
            {long && (
              <button
                type="button"
                data-pf-decision-text-toggle=""
                aria-expanded={textOpen}
                onClick={() => setTextOpen((value) => !value)}
                style={{ alignSelf: "flex-start", padding: 0, border: "none", background: "none", color: "var(--accent)", cursor: "pointer", fontSize: 11 }}
              >
                {textOpen ? t("polyforge.decisions.showLess") : t("polyforge.decisions.showMore")}
              </button>
            )}
          </div>
        )}
      </div>
      {open && (
        <button
          type="button"
          data-pf-decision-dismiss=""
          disabled={busy}
          onClick={() => onDismiss(decision.id)}
          style={{ flexShrink: 0, padding: "2px 8px", fontSize: 11, border: "1px solid var(--border)", borderRadius: 4, background: "none", color: "var(--text-muted)", cursor: busy ? "default" : "pointer" }}
        >
          {t("polyforge.decisions.dismiss")}
        </button>
      )}
    </div>
  );
}

/** A shared dismiss runner for a list of entries: one in flight at a time,
 *  the refusal's text kept for the list to show. */
export function useDecisionDismiss(onDismiss: (ids: number[]) => Promise<void>) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const dismiss = (id: number) => {
    if (busy) return;
    setBusy(true);
    setError(null);
    onDismiss([id])
      .catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)))
      .finally(() => setBusy(false));
  };
  return { busy, error, dismiss };
}
