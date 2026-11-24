"use client";

// The step-tier drawer (polyforge#258 T3, AC2): the model table's cell is a
// small label; opening it shows the tier choice and 「应用到」 in one right-side
// drawer. Every harness write re-reads the Host catalog together with the file
// hash its write expects (the CAS of the same state the plan derives from), and
// a failure stops there with the draft kept — the #151 rules, restored after
// #256 removed the inline editor.

import { useLayoutEffect, useRef, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import type { ModelViewRow } from "@/lib/harness/model-view";
import {
  stepTierDisplayOrder,
  stepTierItems,
  stepTierRowView,
  stepTierState,
  stepTierWritePlan,
  type StepTierItem,
  type StepTierWrite,
} from "@/lib/step-tiers";
import { HarnessApplyTo, applyToTargets, type HarnessApplyToResult, type HarnessApplyToSelection } from "./HarnessApplyTo";
import { ConfigButton } from "./SettingsUi";
import { openStackedDialog } from "@/lib/stacked-dialog";

export interface StepTierColumn {
  capable: boolean;
  reason: string | null;
  tiers: StepTierItem[];
}

/** One Host catalog read: the items the plan derives from and the file hash the
 *  write's CAS expects, from the same GET. */
export type StepTierRead = () => Promise<
  { ok: true; items: Array<{ key: string; value?: unknown }>; hash: string | null }
  | { ok: false; error: string }
>;

export type StepTierWriteFn = (write: StepTierWrite, hash: string | null) => Promise<{ ok: true } | { ok: false; error?: string }>;

/** The identity a row's harness addresses in the catalog (pi's provider/id,
 *  claude's model name — the write gate's own rule). */
export function stepTierIdentity(row: Pick<ModelViewRow, "provider" | "id">, harness: string): { harness: "pi" | "claude"; model: string } | null {
  if (harness === "pi") return { harness: "pi", model: `${row.provider}/${row.id}` };
  if (harness === "claude") return { harness: "claude", model: row.id };
  return null;
}

export function ModelStepTierDrawer({ row, tier, tierRead, tierWrite, onClose, onSaved }: {
  row: ModelViewRow;
  tier: StepTierColumn;
  tierRead: StepTierRead;
  tierWrite: StepTierWriteFn;
  onClose: () => void;
  onSaved?: () => void;
}) {
  const { t } = useI18n();
  const identities = row.harnesses
    .map((harness) => stepTierIdentity(row, harness))
    .filter((identity): identity is { harness: "pi" | "claude"; model: string } => identity !== null);
  const perHarness: Record<string, string[]> = {};
  for (const identity of identities) perHarness[identity.harness] = stepTierState(tier.tiers, identity);
  const view = stepTierRowView(identities.map((identity) => identity.harness), perHarness);
  const [draft, setDraft] = useState<string | null>(null);
  // The submit reads the ref, so a target chosen in the same turn as the click
  // is never lost to a not-yet-flushed render (the e2e's finding, u5#1).
  const draftRef = useRef<string | null>(null);
  const [selection, setSelection] = useState<HarnessApplyToSelection>({ mode: "all", selected: [] });
  const [results, setResults] = useState<HarnessApplyToResult[] | null>(null);
  const [busy, setBusy] = useState(false);
  const dialogRef = useRef<HTMLElement | null>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  // A dialog above Settings: Escape is taken in the capture phase, so one press
  // closes only this drawer, and focus returns to the label that opened it.
  useLayoutEffect(() => openStackedDialog(document, dialogRef.current, () => onCloseRef.current()), []);
  if (!tier.capable) return null;
  const target = draft !== null ? draft : view.kind === "all" ? view.tier : "";

  const submit = async () => {
    const chosen = draftRef.current !== null ? draftRef.current : view.kind === "all" ? view.tier : "";
    const targets = applyToTargets(selection, row.harnesses.map((harness) => ({ harness, label: harness })));
    if (targets.length === 0) return;
    // a harness in several tiers is replaced by the chosen single tier — confirm first
    if (targets.some((harness) => (perHarness[harness]?.length ?? 0) > 1)
      && !window.confirm(t("models.stepTierMultiConfirm"))) return;
    setBusy(true);
    setResults(null);
    const out: HarnessApplyToResult[] = [];
    let failed = false;
    for (const harness of targets) {
      if (failed) {
        out.push({ harness, ok: false, text: t("models.stepTierNotAttempted") });
        continue;
      }
      const identity = stepTierIdentity(row, harness);
      if (!identity) continue;
      let harnessError: string | null = null;
      for (let guard = 0; guard < 32; guard++) {
        // one read carries both the catalog the plan derives from and the
        // hash the write expects — the CAS then guards exactly that state
        const read = await tierRead();
        if (!read.ok) { harnessError = read.error; break; }
        const plan = stepTierWritePlan(stepTierItems(read.items), identity, chosen === "" ? null : chosen);
        if (plan.length === 0) break;
        const answer = await tierWrite(plan[0], read.hash);
        if (!answer.ok) {
          harnessError = answer.error ?? "HTTP";
          break;
        }
      }
      out.push({ harness, ok: !harnessError, text: harnessError ?? t("models.stepTierApplied") });
      if (harnessError) failed = true;
    }
    setResults(out);
    setBusy(false);
    if (!failed && onSaved) onSaved();
  };

  return (
    <div className="model-tier-drawer-backdrop" onClick={onClose}>
      <aside
        ref={(node) => { dialogRef.current = node; }}
        role="dialog"
        aria-modal="true"
        aria-label={`${t("models.stepTier")} — ${row.name}`}
        tabIndex={-1}
        data-step-tier-drawer=""
        className="model-tier-drawer"
        onClick={(event) => event.stopPropagation()}
      >
        <header className="model-tier-drawer-header">
          <span className="model-tier-drawer-title">
            <strong>{row.name}</strong>
            <code>{row.provider}/{row.id}</code>
          </span>
          <button type="button" className="config-close-button" aria-label={t("i18n.close")} title={t("i18n.close")} onClick={onClose}>×</button>
        </header>
        <div className="model-tier-drawer-body">
          <div className="model-tier-drawer-current">
            {identities.map((identity) => {
              const tiers = perHarness[identity.harness] ?? [];
              return <span key={identity.harness} data-step-tier-current={identity.harness}>
                {identity.harness}: {tiers.length === 0 ? t("models.stepTierUnset") : tiers.join("、")}
              </span>;
            })}
          </div>
          <label className="model-tier-drawer-field">
            <span>{t("models.stepTier")}</span>
            <select
              data-step-tier-select=""
              aria-label={t("models.stepTier")}
              disabled={busy}
              value={target}
              onChange={(event) => { draftRef.current = event.target.value; setDraft(event.target.value); }}
            >
              <option value="">{t("models.stepTierUnset")}</option>
              {stepTierDisplayOrder([...new Set(tier.tiers.map((item) => item.tier))]).map((name) =>
                <option key={name} value={name}>{name}</option>)}
            </select>
          </label>
          <HarnessApplyTo
            texts={{
              title: t("models.stepTierApplyTo.title"),
              all: t("models.stepTierApplyTo.all"),
              specific: t("models.stepTierApplyTo.specific"),
              submit: t("models.stepTierApplyTo.submit"),
            }}
            targets={row.harnesses.map((harness) => ({ harness, label: harness }))}
            value={selection}
            busy={busy}
            results={results}
            onChange={setSelection}
            onSubmit={() => void submit()}
          />
        </div>
        <footer className="model-tier-drawer-footer">
          <ConfigButton onClick={onClose}>{t("i18n.cancel")}</ConfigButton>
        </footer>
      </aside>
    </div>
  );
}