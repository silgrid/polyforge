"use client";

// General's 生效值与来源 pane (polyforge#149, #85 85a1b, AC2/AC6): every
// harness's effective values, each marked 推算 (inferred) with its winning
// source — the values are computed from the config layers, never observed
// from a running process (#85 d-5) — plus one collapsed native-diagnostics
// block per harness, loaded on its first open. One read per activation of
// the pane and of a block, never a poll.
import { useEffect, useRef, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import type { EffectiveConfigPayload, NativeDiagnosticsResult } from "@/lib/harness/concepts";
import { effectiveRows, type HarnessEffectiveRow } from "@/lib/harness-settings-view";

/** One harness's rows as the pane shows them. */
export interface HarnessEffectiveGroupView {
  harness: string;
  rows: HarnessEffectiveRow[];
  error: string | null;
}

/** The values half: one group per harness, every value with its Inferred mark. */
export function HarnessEffectiveValues({ harness, rows }: { harness: string; rows: HarnessEffectiveRow[] }) {
  const { t } = useI18n();
  return (
    <div data-harness={harness} className="settings-harness-effective-group">
      <h4 className="settings-general-heading">{harness}</h4>
      <p className="settings-general-description">{t("settings.harnessEffectiveNote")}</p>
      {rows.length === 0 ? (
        <p className="settings-general-description">{t("settings.harnessEffectiveEmpty")}</p>
      ) : (
        <ul className="settings-harness-effective-rows">
          {rows.map((row) => (
            <li key={row.field} data-field={row.field} className="settings-harness-effective-row">
              <code className="settings-harness-effective-field">{row.field}</code>
              <span className="settings-harness-effective-value">{row.value}</span>
              <span className="settings-harness-inferred" title={t("settings.harnessEffectiveInferredTitle")}>{t("settings.harnessInferred")}</span>
              <span className="settings-harness-effective-source">
                <span className="settings-harness-effective-scope">{row.sourceScope}</span>
                <code>{row.sourcePath}</code>
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** One harness's diagnostics block state: loading, or the settled result. */
export interface HarnessDiagnosticsState {
  status: "loading" | "done";
  result: NativeDiagnosticsResult | null;
}

/**
 * The native-diagnostics block (AC6): a collapsed `<details>` per harness
 * whose first open loads the run once — the command is the harness's own
 * fixed safe one, its output filtered, and a timeout or an unavailable
 * reason is said as visible text, never hidden behind the fold.
 */
export function HarnessDiagnosticsBlock({
  harness,
  state,
  result,
  onOpen,
}: {
  harness: string;
  state: HarnessDiagnosticsState;
  result: NativeDiagnosticsResult | null;
  onOpen: (event: React.SyntheticEvent<HTMLDetailsElement>) => void;
}) {
  const { t } = useI18n();
  return (
    <details className="settings-harness-diagnostics" data-harness={harness} onToggle={onOpen}>
      <summary>{t("settings.harnessDiagnostics")}</summary>
      <div className="settings-harness-diagnostics-body">
        {state.status === "loading" && <p className="settings-general-description">{t("settings.harnessDiagnosticsRunning")}</p>}
        {state.status === "done" && result === null && (
          <p className="settings-general-description">{t("settings.harnessDiagnosticsRunning")}</p>
        )}
        {state.status === "done" && result?.available === false && (
          <p className="settings-general-description">{t("settings.harnessDiagnosticsUnavailable", { reason: result.reason })}</p>
        )}
        {state.status === "done" && result?.available === true && (
          <>
            <p className="settings-general-description"><code>{result.command.join(" ")}</code></p>
            {result.timedOut && <p role="status" className="settings-general-description">{t("settings.harnessDiagnosticsTimedOut")}</p>}
            <pre className="settings-harness-diagnostics-output">{result.lines.join("\n")}</pre>
            {result.truncated && <p className="settings-general-description">{t("settings.harnessDiagnosticsTruncated")}</p>}
          </>
        )}
      </div>
    </details>
  );
}

interface HarnessListEntry {
  id: string;
}

export function HarnessEffectiveSettings({ cwd }: { cwd: string | null }) {
  const { t } = useI18n();
  const [groups, setGroups] = useState<HarnessEffectiveGroupView[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [diagnostics, setDiagnostics] = useState<Record<string, HarnessDiagnosticsState>>({});
  const loadedRef = useRef<Set<string>>(new Set());

  // One read per mount (the pane unmounts on switch, so each activation
  // reads once — the settings pages' cadence, never a poll).
  useEffect(() => {
    let cancelled = false;
    const read = async () => {
      const next: HarnessEffectiveGroupView[] = [];
      try {
        const query = cwd ? `?cwd=${encodeURIComponent(cwd)}` : "";
        const listResponse = await fetch(`/api/harness${query}`);
        const list = await listResponse.json() as { harnesses?: HarnessListEntry[] } | null;
        const entries = listResponse.ok && Array.isArray(list?.harnesses) ? list!.harnesses! : [];
        for (const entry of entries) {
          try {
            const response = await fetch(`/api/harness/${encodeURIComponent(entry.id)}/effective-config${query}`);
            const body = await response.json() as { data?: unknown } | null;
            if (!response.ok || !body?.data) throw new Error(`HTTP ${response.status}`);
            const rows = effectiveRows(body.data as EffectiveConfigPayload | null);
            next.push({ harness: entry.id, rows, error: null });
          } catch (cause) {
            next.push({ harness: entry.id, rows: [], error: cause instanceof Error ? cause.message : String(cause) });
          }
        }
      } catch (cause) {
        // The list itself refused: its visible reason, no groups (r1#8).
        if (!cancelled) {
          setListError(cause instanceof Error ? cause.message : String(cause));
          setGroups([]);
          return;
        }
      }
      if (!cancelled) setGroups(next);
    };
    void read();
    return () => { cancelled = true; };
  }, [cwd]);

  /** A block's first open loads its run once; a later open re-reads nothing. */
  const openDiagnostics = (harness: string) => async (event: React.SyntheticEvent<HTMLDetailsElement>) => {
    if (!event.currentTarget.open || loadedRef.current.has(harness)) return;
    loadedRef.current.add(harness);
    setDiagnostics((current) => ({ ...current, [harness]: { status: "loading", result: null } }));
    try {
      const query = cwd ? `?cwd=${encodeURIComponent(cwd)}` : "";
      const response = await fetch(`/api/harness/${encodeURIComponent(harness)}/diagnose${query}`);
      const body = await response.json() as { diagnostics?: NativeDiagnosticsResult } | null;
      if (!response.ok || !body?.diagnostics) throw new Error(`HTTP ${response.status}`);
      setDiagnostics((current) => ({ ...current, [harness]: { status: "done", result: body.diagnostics! } }));
    } catch (cause) {
      setDiagnostics((current) => ({
        ...current,
        [harness]: { status: "done", result: { available: false, reason: cause instanceof Error ? cause.message : String(cause) } },
      }));
    }
  };

  return (
    <section className="settings-general-section">
      <h3 className="settings-general-heading">{t("settings.harnessEffectiveTitle")}</h3>
      <p className="settings-general-description">{t("settings.harnessEffectiveNote")}</p>
      {listError && <p role="alert" className="settings-general-error">{t("settings.harnessListFailed", { error: listError })}</p>}
      {groups === null && <p className="settings-general-description">{t("settings.harnessEnvReading")}</p>}
      {groups !== null && groups.length === 0 && (
        <p className="settings-general-description">{t("settings.harnessEffectiveNoHarnesses")}</p>
      )}
      {groups?.map((group) => (
        <div key={group.harness} className="settings-harness-effective">
          {group.error ? (
            <p role="alert" className="settings-general-error">{t("settings.harnessEnvFailed", { harness: group.harness, error: group.error })}</p>
          ) : (
            <HarnessEffectiveValues harness={group.harness} rows={group.rows} />
          )}
          <HarnessDiagnosticsBlock
            harness={group.harness}
            state={diagnostics[group.harness] ?? { status: "done", result: null }}
            result={diagnostics[group.harness]?.result ?? null}
            onOpen={openDiagnostics(group.harness)}
          />
        </div>
      ))}
    </section>
  );
}
