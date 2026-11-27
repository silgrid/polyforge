"use client";

import { useCallback, useEffect, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { stepTierItems, stepTierInvalidReferences, parseStepTierCandidate, type StepTierItem } from "@/lib/step-tiers";
import { ConfigButton, ConfigErrorNotice, ConfigSkeleton } from "./SettingsUi";
import { useSettingsDraft } from "./SettingsDrafts";

type Catalog = { capable?: boolean; stepTiersReason?: string | null; items?: { key: string; value?: unknown }[]; file?: { sha256?: string | null }; error?: string };
const lines = (text: string) => text.split("\n").map((line) => line.trim()).filter(Boolean);

/** Ordered Host candidates. Visibility in an interactive picker is not daemon eligibility. */
export function NativeModelTiers({ defined, onSaved, onReveal }: { defined: Record<"pi" | "claude", string[]>; onSaved: () => void; onReveal?: () => void }) {
  const { t } = useI18n();
  const [catalog, setCatalog] = useState<Catalog | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [baseline, setBaseline] = useState<Record<string, string[]>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("");
  const [outcomes, setOutcomes] = useState<Record<string, string>>({});
  const read = useCallback(async () => {
    const response = await fetch("/api/polyforge/config?host=1");
    const answer = await response.json() as Catalog;
    if (!response.ok || answer.error) throw new Error(answer.error ?? `HTTP ${response.status}`);
    return answer;
  }, []);
  useEffect(() => {
    let active = true;
    void read().then((answer) => { if (active) setCatalog(answer); }).catch((cause) => { if (active) setError(String(cause)); });
    return () => { active = false; };
  }, [read]);
  const tiers = stepTierItems(catalog?.items ?? []);
  const change = (tier: string, text: string) => {
    const original = tiers.find((item) => item.tier === tier)?.candidates ?? [];
    setBaseline((previous) => tier in previous ? previous : { ...previous, [tier]: original });
    setDrafts((previous) => {
      const next = { ...previous };
      if (JSON.stringify(lines(text)) === JSON.stringify(baseline[tier] ?? original)) delete next[tier];
      else next[tier] = text;
      return next;
    });
  };
  const save = async () => {
    if (busy) return;
    setBusy(true); setError(null); setOutcomes({});
    const remaining = { ...drafts };
    try {
      for (const [tier, text] of Object.entries(drafts)) {
        const fresh = await read();
        const actual = stepTierItems(fresh.items ?? []).find((item) => item.tier === tier)?.candidates ?? [];
        if (JSON.stringify(actual) !== JSON.stringify(baseline[tier] ?? [])) throw new Error(t("settings.draftConflict", { name: tier }));
        if (!fresh.capable || fresh.stepTiersReason || !fresh.file || !("sha256" in fresh.file)) throw new Error(t("models.stepTierReasonUnavailable"));
        const candidates = lines(text);
        const response = await fetch("/api/polyforge/config", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({
          host: true, key: `step_tiers.${tier}`, action: candidates.length ? "set" : "unset", ...(candidates.length ? { value: candidates } : {}), expect_state: { file_sha256: fresh.file.sha256 },
        }) });
        const answer = await response.json();
        if (!response.ok || answer.error) throw new Error(answer.error ?? `HTTP ${response.status}`);
        delete remaining[tier]; setDrafts({ ...remaining });
        setBaseline((previous) => { const next = { ...previous }; delete next[tier]; return next; });
        setOutcomes((previous) => ({ ...previous, [tier]: t("i18n.saved") }));
        // Keep the saved projection even if the follow-up read fails.
        setCatalog({ ...fresh, items: [...(fresh.items ?? []).filter((item) => item.key !== `step_tiers.${tier}`), ...(candidates.length ? [{ key: `step_tiers.${tier}`, value: candidates }] : [])] });
        onSaved();
      }
    } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
    finally { setBusy(false); }
  };
  useSettingsDraft({ label: t("models.tiersHeading"), reveal: onReveal, count: Object.keys(drafts).length, busy, save: () => void save(), discard: () => { setDrafts({}); setBaseline({}); setError(null); } });
  const all: StepTierItem[] = [...tiers, ...Object.keys(drafts).filter((tier) => !tiers.some((item) => item.tier === tier)).map((tier) => ({ tier, candidates: [] }))];
  return <section className="settings-model-section" data-native-tiers="">
    <header className="settings-content-heading"><div><h2>{t("models.tiersHeading")}</h2><p>{t("models.tierIntro")}</p></div></header>
    <details className="settings-advanced-note"><summary>{t("i18n.detailsLabel")}</summary><p>{t("models.tierBoundary")}</p></details>
    {error && <ConfigErrorNotice summary={error} />}
    {!catalog && !error && <ConfigSkeleton label={t("i18n.loading")} />}
    {catalog && (!catalog.capable || catalog.stepTiersReason) && <p>{t("models.stepTierReasonUnavailable")}</p>}
    <fieldset disabled={busy || !catalog?.capable || Boolean(catalog?.stepTiersReason)} className="settings-tier-fields">
      <div className="settings-model-bulk"><input aria-label={t("models.newTier")} placeholder={t("models.newTier")} value={name} onChange={(event) => setName(event.target.value)} /><ConfigButton disabled={!/^[a-z][a-z0-9-]{0,39}$/.test(name) || all.some((item) => item.tier === name)} onClick={() => { setDrafts((previous) => ({ ...previous, [name]: "" })); setBaseline((previous) => ({ ...previous, [name]: [] })); setName(""); }}>{t("models.newTier")}</ConfigButton></div>
      {all.map((item) => {
        const candidates = item.tier in drafts ? lines(drafts[item.tier]) : item.candidates;
        const invalid = stepTierInvalidReferences([{ ...item, candidates }], defined);
        return <div className="settings-tier-card" key={item.tier}>
          <div className="settings-panel-heading"><h3>{item.tier}</h3><span className="settings-count-badge">{candidates.length}</span></div>
          <ol className="settings-candidate-list">{candidates.map((candidate, index) => {
            const parsed = parseStepTierCandidate(candidate);
            return <li key={`${index}:${candidate}`}><span className="settings-candidate-order">{index + 1}</span><div><strong>{parsed?.model ?? candidate}</strong><small>{parsed ? `${parsed.harness}${parsed.effort ? ` · ${parsed.effort}` : ""}` : t("models.connectionUnknown")}</small></div></li>;
          })}</ol>
          <details className="settings-tier-editor" open={item.tier in drafts || candidates.length === 0 || undefined}><summary>{t("models.editCandidates")}</summary>
          <label>{t("models.orderedCandidates")}<textarea aria-label={`${item.tier} ${t("models.orderedCandidates")}`} value={drafts[item.tier] ?? item.candidates.join("\n")} onChange={(event) => change(item.tier, event.target.value)} /></label></details>
          {invalid.length > 0 && <p role="status">{t("models.stepTierInvalidNote", { candidates: invalid.join("、") })}</p>}
          {candidates.length === 0 && <p>{t("models.tierUnset")}</p>}
          {outcomes[item.tier] && <p role="status">{outcomes[item.tier]}</p>}
        </div>;
      })}
    </fieldset>
  </section>;
}
