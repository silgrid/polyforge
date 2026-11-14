"use client";

// PolyforgeStepControls.tsx — polyforge#25: the selected UNSTARTED step's
// overlay controls of the Polyforge dock panel's job detail. The spawn face
// stays server-side (the route); this client component talks to it with
// plain fetch, exactly like the job composer.

import { useCallback, useEffect, useState, type CSSProperties } from "react";
import type { PolyforgeStepOverride, PolyforgeWatchStatus } from "@/lib/polyforge-status";
// type-only: the spawn face stays server-side (the route).
import type { PolyforgeOverrideView } from "@/lib/polyforge-overrides";
import { useI18n } from "@/hooks/useI18n";

export function PolyforgeStepControls({ wi, profile, step, skippable, overlay, onApplied }: {
  wi: string | null;
  profile?: string;
  step: string;
  skippable: boolean;
  overlay?: PolyforgeStepOverride;
  onApplied: () => void | Promise<void>;
}) {
  const { t } = useI18n();
  const [view, setView] = useState<PolyforgeOverrideView | null>(null);
  const [viewSeq, setViewSeq] = useState(0);
  const [harness, setHarness] = useState<"pi" | "claude">("pi");
  const [model, setModel] = useState("");
  const [busy, setBusy] = useState(false);
  const [applied, setApplied] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // one view read per (wi, step) selection, and again after each write: the
  // route runs `pf --json override` server-side; a failure keeps the last view
  const readView = useCallback(() => {
    if (!wi) return;
    let cancelled = false;
    setView(null);
    fetch(`/api/polyforge/override?wi=${encodeURIComponent(wi)}${profile && profile !== "default" ? `&profile=${encodeURIComponent(profile)}` : ""}`)
      .then((response) => response.json().catch(() => null))
      .then((body: { status?: PolyforgeOverrideView | null } | null) => { if (!cancelled && body?.status) setView(body.status); })
      .catch(() => undefined);
    return () => { cancelled = true; };
  }, [wi, profile]);
  useEffect(() => readView(), [readView, step, viewSeq]);
  const row = view?.steps.find((r) => r.id === step) ?? null;
  const write = (action: { kind: "models"; step: string; models: string[] } | { kind: "clearModels"; step: string } | { kind: "skip"; step: string; value: boolean }) => {
    if (!wi) return;
    setBusy(true);
    setError(null);
    setApplied(false);
    fetch("/api/polyforge/override", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ wi, ...(profile && profile !== "default" ? { profile } : {}), action }),
    })
      .then(async (response) => ({ status: response.status, body: await response.json().catch(() => ({})) as { ok?: boolean; error?: string } }))
      .then(({ status, body }) => {
        if (body.ok !== true) {
          setError(body.error || `HTTP ${status}`);
          return;
        }
        setApplied(true);
        setModel("");
        setViewSeq((n) => n + 1);
        void Promise.resolve(onApplied()).catch(() => undefined);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };
  const effective = row?.effective ?? [];
  const source = row?.source ?? "";
  const pending = row?.pending || overlay?.pending;
  const inputStyle: CSSProperties = { flex: "1 1 120px", minWidth: 0, fontSize: 11, padding: "2px 6px", border: "1px solid var(--border)", borderRadius: 4, background: "var(--bg)", color: "var(--text)" };
  const buttonStyle: CSSProperties = { fontSize: 11, padding: "2px 8px", border: "1px solid var(--border)", borderRadius: 4, background: "var(--bg-panel)", color: "var(--text)" };
  return (
    <div data-pf-step-controls="" style={{ padding: "6px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0, display: "flex", flexDirection: "column", gap: 4, fontSize: 11, lineHeight: "16px", minWidth: 0 }}>
      <span data-pf-step-controls-source="">
        {t("polyforge.stepControls.source")}:{" "}
        {effective.length > 0 ? `${effective.join(", ")} · ${source}` : t("polyforge.stepControls.sourceNone")}
        {pending ? ` ${t("polyforge.stepControls.pending")}` : ""}
      </span>
      <div style={{ display: "flex", gap: 4, alignItems: "center", flexWrap: "wrap" }}>
        <select
          data-pf-step-controls-harness=""
          aria-label={t("polyforge.stepControls.harness")}
          value={harness}
          onChange={(event) => setHarness(event.target.value === "claude" ? "claude" : "pi")}
          style={{ ...inputStyle, flex: "0 0 auto" }}
        >
          <option value="pi">pi</option>
          <option value="claude">claude</option>
        </select>
        <input
          data-pf-step-controls-model=""
          aria-label={t("polyforge.stepControls.model")}
          placeholder={t("polyforge.stepControls.model")}
          value={model}
          onChange={(event) => setModel(event.target.value)}
          style={inputStyle}
        />
        <button
          type="button"
          data-pf-step-controls-apply=""
          disabled={busy || !model.trim()}
          onClick={() => write({ kind: "models", step, models: [`${harness}:${model.trim()}`] })}
          style={{ ...buttonStyle, opacity: busy || !model.trim() ? 0.5 : 1 }}
        >
          {busy ? t("polyforge.stepControls.busy") : t("polyforge.stepControls.apply")}
        </button>
        {skippable ? (
          <button
            type="button"
            data-pf-step-controls-skip=""
            disabled={busy}
            onClick={() => write({ kind: "skip", step, value: !(overlay?.skip ?? false) })}
            style={{ ...buttonStyle, opacity: busy ? 0.5 : 1 }}
          >
            {overlay?.skip ? t("polyforge.stepControls.unskip") : t("polyforge.stepControls.skip")}
          </button>
        ) : (
          <span data-pf-step-controls-waive="" style={{ color: "var(--text-dim)" }}>{t("polyforge.stepControls.waiveHint")}</span>
        )}
      </div>
      {applied && <span data-pf-step-controls-applied="" style={{ color: "var(--accent)" }}>{t("polyforge.stepControls.applied")}</span>}
      {error && <span data-pf-step-controls-error="" style={{ color: "#dc2626" }}>{error}</span>}
    </div>
  );
}

// stepUnstarted reports whether the step of watch has no record or a pending
// one: the overlay controls' reach (a step with a live dispatch or one that
// already ran never shows them).
export function stepUnstarted(watch: PolyforgeWatchStatus, step: string): boolean {
  const record = watch.steps.find((s) => s.name === step);
  return !record || record.status === "pending";
}

