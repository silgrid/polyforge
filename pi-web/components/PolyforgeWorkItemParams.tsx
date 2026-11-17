"use client";

// PolyforgeWorkItemParams.tsx — polyforge#79: the 工单详情's params
// section. ONE /api/polyforge/params read per open (a gesture — the detail
// opens; no timer, no polling), every step's declared params rendered with
// their final value and source (the composed face `pf params` answers).
// A QUEUED work item's rows carry the set/clear write controls — the
// surface's one write (owner ruling 2026-11-17: the 配置入口 lives here,
// 「放开它原来只读的约定」), through the same /api/polyforge/override POST
// the panel's controls use; a claimed work item is read-only (its write
// face is the running job's panel). A write's success re-reads the view
// exactly once; a refused read or write degrades to the error line, never
// a throw. Nothing is persisted, nothing else is fetched.

import { useEffect, useRef, useState, type CSSProperties } from "react";
import { useI18n } from "@/hooks/useI18n";
import type { PolyforgeParamsView } from "@/lib/polyforge-params";

const inputStyle: CSSProperties = { flex: "0 1 120px", minWidth: 0, fontSize: 11, padding: "2px 6px", border: "1px solid var(--border)", borderRadius: 4, background: "var(--bg)", color: "var(--text)" };
const buttonStyle: CSSProperties = { fontSize: 11, padding: "2px 8px", border: "1px solid var(--border)", borderRadius: 4, background: "var(--bg-panel)", color: "var(--text)" };

export function PolyforgeWorkItemParams({ wi }: { wi: string }) {
  const { t } = useI18n();
  const [view, setView] = useState<PolyforgeParamsView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [writeError, setWriteError] = useState<string | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const readSeqRef = useRef(0);

  const read = () => {
    const seq = ++readSeqRef.current;
    setLoading(true);
    fetch(`/api/polyforge/params?wi=${encodeURIComponent(wi)}`)
      .then(async (response) => ({ status: response.status, body: await response.json().catch(() => ({})) as { params?: PolyforgeParamsView | null; error?: string } }))
      .then(({ body }) => {
        if (seq !== readSeqRef.current) return;
        setLoading(false);
        setView(body.params ?? null);
        setError(body.params ? null : (body.error ?? (body.params === null ? null : t("polyforge.workItems.paramsUnavailable"))));
        if (body.params === null && !body.error) setError(t("polyforge.workItems.paramsUnavailable"));
      })
      .catch(() => {
        if (seq !== readSeqRef.current) return;
        setLoading(false);
        setError(t("polyforge.workItems.paramsUnavailable"));
      });
  };

  // the one read per open: the section mounts with the detail, and a
  // re-used instance reads again for a CHANGED wi (the parent keys the
  // mount by the ticket, so this is the belt, not the mechanism)
  const readWiRef = useRef<string | null>(null);
  useEffect(() => {
    if (readWiRef.current === wi) return;
    readWiRef.current = wi;
    read();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- one read per opening, none per re-render
  }, [wi]);

  const write = (step: string, name: string, kind: "param" | "clearParam") => {
    setBusy(true);
    setWriteError(null);
    fetch("/api/polyforge/override", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ wi, action: kind === "param" ? { kind, step, name, value: values[`${step}/${name}`] ?? "" } : { kind, step, name } }),
    })
      .then(async (response) => ({ status: response.status, body: await response.json().catch(() => ({})) as { ok?: boolean; error?: string } }))
      .then(({ status, body }) => {
        if (body.ok !== true) {
          setWriteError(body.error || `HTTP ${status}`);
          return;
        }
        setValues((current) => {
          const next = { ...current };
          delete next[`${step}/${name}`];
          return next;
        });
        read(); // the write's success re-reads the view exactly once
      })
      .catch((e: unknown) => setWriteError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  const queued = view?.posture === "queued";
  return (
    <div data-pf-wi-params="" style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 11, lineHeight: "16px", padding: "6px 0" }}>
      <span style={{ color: "var(--text-dim)" }}>{t("polyforge.workItems.params")}</span>
      {loading && <span style={{ color: "var(--text-dim)" }}>{t("polyforge.workItems.loading")}</span>}
      {error && <span data-pf-wi-params-error="" style={{ color: "var(--text-dim)" }}>{error}</span>}
      {view && view.steps.map((step) => (
        <div key={step.id} data-pf-wi-params-step={step.id} style={{ display: "flex", flexDirection: "column", gap: 2 }}>
          <span style={{ color: "var(--text-muted)" }}>{step.id} · {step.status}</span>
          {step.params.length === 0 && <span style={{ color: "var(--text-dim)" }}>{t("polyforge.stepControls.paramsNone")}</span>}
          {step.params.map((p) => (
            <div key={p.name} style={{ display: "flex", gap: 4, alignItems: "center", flexWrap: "wrap" }}>
              <span data-pf-wi-param={p.name} style={{ fontFamily: "var(--font-mono)", minWidth: 0, overflowWrap: "anywhere" }}>
                {p.name}={p.value ? p.value : "“”"}{p.source ? ` · ${p.source}` : ""}
              </span>
              {queued && (
                <>
                  <input
                    data-pf-wi-param-input={p.name}
                    data-pf-wi-param-step={step.id}
                    aria-label={t("polyforge.stepControls.paramValue")}
                    placeholder={t("polyforge.stepControls.paramValue")}
                    value={values[`${step.id}/${p.name}`] ?? ""}
                    onChange={(event) => setValues((current) => ({ ...current, [`${step.id}/${p.name}`]: event.target.value }))}
                    style={inputStyle}
                  />
                  <button
                    type="button"
                    data-pf-wi-param-set={p.name}
                    data-pf-wi-param-step={step.id}
                    disabled={busy}
                    onClick={() => write(step.id, p.name, "param")}
                    style={{ ...buttonStyle, opacity: busy ? 0.5 : 1 }}
                  >
                    {t("polyforge.stepControls.apply")}
                  </button>
                  <button
                    type="button"
                    data-pf-wi-param-clear={p.name}
                    data-pf-wi-param-step={step.id}
                    disabled={busy}
                    onClick={() => write(step.id, p.name, "clearParam")}
                    style={{ ...buttonStyle, opacity: busy ? 0.5 : 1 }}
                  >
                    {t("polyforge.stepControls.paramClear")}
                  </button>
                </>
              )}
            </div>
          ))}
        </div>
      ))}
      {writeError && <span data-pf-wi-params-error="" style={{ color: "#dc2626" }}>{writeError}</span>}
    </div>
  );
}
