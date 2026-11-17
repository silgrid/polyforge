"use client";

// PolyforgeStepControls.tsx — polyforge#25: the selected step's overlay
// controls of the Polyforge dock panel's job detail. The spawn face stays
// server-side (the route); this client component talks to it with plain
// fetch, exactly like the job composer.
//
// polyforge#42 (G11): the component keeps NO read of its own. The
// effective-model source line renders from the overlay view row the watch
// route composes into the panel's snapshot (`row`), the skip switch state
// and the pending mark from the snapshot's own `overrides` values — so
// selecting a step issues no request (a click-caused /api/polyforge/override
// read broke the graph suite's G11). The write still goes through the
// override route's POST, and the next watch poll (the snapshot's own 3 s
// cadence) carries the fresh row.
//
// polyforge#79: the params section renders for every selected step that
// has one — the composed final values and sources the watch route's
// `paramsView` carries (`params`), so a step that already ran shows its
// params read-only (AC1's 「已经跑过的 step 只读」); the write controls (the
// model chain, the skip switch and a set/clear per declared param) stay
// an UNSTARTED step's — the same posture rules `pf override` enforces
// server-side. A step with neither (a started step whose snapshot carries
// no params view) renders nothing at all: no empty shell above the
// progress region — a mounting shell would shrink the region under the
// reader's scroll and break the detail's opens-at-bottom follow.

import { useState, type CSSProperties } from "react";
import type { PolyforgeStepOverride, PolyforgeWatchStatus } from "@/lib/polyforge-status";
// type-only: the spawn face stays server-side (the route).
import type { PolyforgeOverrideAction, PolyforgeOverrideStepRow } from "@/lib/polyforge-overrides";
import type { PolyforgeParamsStepRow } from "@/lib/polyforge-params";
import { useI18n } from "@/hooks/useI18n";

export function PolyforgeStepControls({ wi, profile, step, skippable, unstarted, overlay, row, params, onApplied }: {
  wi: string | null;
  profile?: string;
  step: string;
  skippable: boolean;
  /** the step has no record or a pending one — the write controls' reach
   *  (a step with a live dispatch or one that already ran shows the params
   *  read-only only). */
  unstarted: boolean;
  overlay?: PolyforgeStepOverride;
  row?: PolyforgeOverrideStepRow;
  /** the composed params row of the watch route's `paramsView` (undefined
   *  when the snapshot carries none — an older pf, a degraded read). */
  params?: PolyforgeParamsStepRow;
  onApplied: () => void | Promise<void>;
}) {
  const { t } = useI18n();
  const [harness, setHarness] = useState<"pi" | "claude">("pi");
  const [model, setModel] = useState("");
  const [paramValues, setParamValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [applied, setApplied] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const write = (action: PolyforgeOverrideAction) => {
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
        setParamValues({});
        void Promise.resolve(onApplied()).catch(() => undefined);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };
  if (params === undefined && !unstarted) {
    return null; // no params to show, nothing to write: no shell at all
  }
  const effective = row?.effective ?? [];
  const source = row?.source ?? "";
  const pending = row?.pending || overlay?.pending;
  const inputStyle: CSSProperties = { flex: "1 1 120px", minWidth: 0, fontSize: 11, padding: "2px 6px", border: "1px solid var(--border)", borderRadius: 4, background: "var(--bg)", color: "var(--text)" };
  const buttonStyle: CSSProperties = { fontSize: 11, padding: "2px 8px", border: "1px solid var(--border)", borderRadius: 4, background: "var(--bg-panel)", color: "var(--text)" };
  return (
    <div data-pf-step-controls="" style={{ padding: "6px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0, display: "flex", flexDirection: "column", gap: 4, fontSize: 11, lineHeight: "16px", minWidth: 0 }}>
      {params && (
        <div data-pf-step-controls-params="" style={{ display: "flex", flexDirection: "column", gap: 2 }}>
          <span style={{ color: "var(--text-dim)" }}>{t("polyforge.stepControls.params")}</span>
          {params.params.length === 0 && <span style={{ color: "var(--text-dim)" }}>{t("polyforge.stepControls.paramsNone")}</span>}
          {params.params.map((p) => (
            <div key={p.name} style={{ display: "flex", gap: 4, alignItems: "center", flexWrap: "wrap" }}>
              <span data-pf-param={p.name} style={{ fontFamily: "var(--font-mono)", minWidth: 0, overflowWrap: "anywhere" }}>
                {p.name}={p.value ? p.value : "“”"}{p.source ? ` · ${p.source}` : ""}
              </span>
              {unstarted && (
                <>
                  <input
                    data-pf-param-input={p.name}
                    aria-label={t("polyforge.stepControls.paramValue")}
                    placeholder={t("polyforge.stepControls.paramValue")}
                    value={paramValues[p.name] ?? ""}
                    onChange={(event) => setParamValues((values) => ({ ...values, [p.name]: event.target.value }))}
                    style={{ ...inputStyle, flex: "0 1 120px" }}
                  />
                  <button
                    type="button"
                    data-pf-param-set={p.name}
                    disabled={busy}
                    onClick={() => write({ kind: "param", step, name: p.name, value: paramValues[p.name] ?? "" })}
                    style={{ ...buttonStyle, opacity: busy ? 0.5 : 1 }}
                  >
                    {t("polyforge.stepControls.apply")}
                  </button>
                  <button
                    type="button"
                    data-pf-param-clear={p.name}
                    disabled={busy}
                    onClick={() => write({ kind: "clearParam", step, name: p.name })}
                    style={{ ...buttonStyle, opacity: busy ? 0.5 : 1 }}
                  >
                    {t("polyforge.stepControls.paramClear")}
                  </button>
                </>
              )}
            </div>
          ))}
        </div>
      )}
      {unstarted && (
        <>
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
        </>
      )}
      {applied && <span data-pf-step-controls-applied="" style={{ color: "var(--accent)" }}>{t("polyforge.stepControls.applied")}</span>}
      {error && <span data-pf-step-controls-error="" style={{ color: "#dc2626" }}>{error}</span>}
    </div>
  );
}

// stepUnstarted reports whether the step of watch has no record or a pending
// one: the overlay write controls' reach (a step with a live dispatch or one
// that already ran never shows them).
export function stepUnstarted(watch: PolyforgeWatchStatus, step: string): boolean {
  const record = watch.steps.find((s) => s.name === step);
  return !record || record.status === "pending";
}
