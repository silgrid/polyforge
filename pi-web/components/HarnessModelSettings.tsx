"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { buildModelRows, type ModelViewInput, type ModelViewRow } from "@/lib/harness/model-view";
import type { ModelPayload } from "@/lib/harness/concepts";
import {
  stepTierDisplayOrder,
  stepTierInvalidReferences,
  stepTierItems,
  stepTierRowView,
  stepTierState,
  stepTierWritePlan,
  type StepTierItem,
  type StepTierWrite,
} from "@/lib/step-tiers";
import { useEnabledModels, type EnabledModelsController } from "./EnabledModelsSection";
import { ModelsConfig } from "./ModelsConfig";
import { ClaudeCustomModels } from "./ClaudeCustomModels";
import { HarnessApplyTo, applyToTargets, type HarnessApplyToResult, type HarnessApplyToSelection } from "./HarnessApplyTo";
import { ConfigSidebar, ConfigSidebarList, ConfigSidebarItem, ConfigDetail } from "./SettingsUi";

type ModelIdentity = { harness: string; provider: string; id: string };

/** The tier column's own view of the Host's [step_tiers] catalog (85b2):
 *  the parsed tier items and why the column is read-only when it is. */
export interface StepTierColumnView {
  capable: boolean;
  reason: string | null;
  tiers: StepTierItem[];
}

/** One key write the tier column submits (the panel write route's face). */
export interface StepTierWriteOutcome {
  ok: boolean;
  error?: string;
}

/** The identity a row's harness addresses in the catalog: pi's
 *  provider/id, claude's model name (Q2's identity rule — the same one the
 *  write gate judges). */
function stepTierIdentity(row: Pick<ModelViewRow, "provider" | "id">, harness: string): { harness: "pi" | "claude"; model: string } | null {
  if (harness === "pi") return { harness: "pi", model: `${row.provider}/${row.id}` };
  if (harness === "claude") return { harness: "claude", model: row.id };
  return null;
}

/** One write applied to a local catalog copy (the cell recomputes every
 *  next write from the state the last one left — a second harness's plan
 *  never resurrects the first one's removed entries). */
function applyStepTierWrite(catalog: StepTierItem[], write: StepTierWrite): StepTierItem[] {
  const next = catalog.filter((item) => item.tier !== write.tier);
  if (write.action === "set") next.push({ tier: write.tier, candidates: write.candidates });
  return next;
}

/** One row's tier cell: the current state (全部 / 指定 with per-harness
 *  不一致 lines / 未设置, a multi-tier harness shown as 多档), a target
 *  select (未设置 or a tier, default first) and the「应用到」choice over
 *  the row's own harnesses; the submit writes one harness at a time —
 *  add-first, every write on the fresh catalog the last one left — and a
 *  failure stops there, reported per harness with the draft kept (the
 *  CAS conflict included). A multi-tier edit confirms before it replaces. */
export function StepTierCell({ row, tier, tierWrite, onTierSaved }: {
  row: ModelViewRow;
  tier: StepTierColumnView;
  tierWrite: (write: StepTierWrite) => Promise<StepTierWriteOutcome>;
  onTierSaved?: () => void;
}) {
  const { t } = useI18n();
  const identities = row.harnesses
    .map((harness) => stepTierIdentity(row, harness))
    .filter((identity): identity is { harness: "pi" | "claude"; model: string } => identity !== null);
  const perHarness: Record<string, string[]> = {};
  for (const identity of identities) perHarness[identity.harness] = stepTierState(tier.tiers, identity);
  const view = stepTierRowView(identities.map((identity) => identity.harness), perHarness);
  const [draft, setDraft] = useState<string | null>(null);
  const [selection, setSelection] = useState<HarnessApplyToSelection>({ mode: "all", selected: [] });
  const [results, setResults] = useState<HarnessApplyToResult[] | null>(null);
  const [busy, setBusy] = useState(false);
  if (!tier.capable) {
    return <span data-step-tier-cell="readonly" style={{ color: "var(--text-dim)" }}>—</span>;
  }
  const target = draft !== null ? draft : view.kind === "all" ? view.tier : "";
  const tierNameText = (tiers: string[]) => tiers.length === 0 ? t("models.stepTierUnset")
    : tiers.length === 1 ? tiers[0]
    : t("models.stepTierMulti", { tiers: tiers.join("、") });
  const submit = async () => {
    const targets = applyToTargets(selection, row.harnesses.map((harness) => ({ harness, label: harness })));
    if (targets.length === 0) return;
    // a multi-tier harness is replaced by the chosen single tier — confirm first
    if (targets.some((harness) => (perHarness[harness]?.length ?? 0) > 1)
      && !window.confirm(t("models.stepTierMultiConfirm"))) return;
    setBusy(true);
    setResults(null);
    const out: HarnessApplyToResult[] = [];
    let failed = false;
    let catalog = tier.tiers;
    for (const harness of targets) {
      if (failed) {
        out.push({ harness, ok: false, text: t("models.stepTierNotAttempted") });
        continue;
      }
      const identity = stepTierIdentity(row, harness);
      if (!identity) continue;
      let harnessError: string | null = null;
      for (let guard = 0; guard < 32; guard++) {
        const plan = stepTierWritePlan(catalog, identity, target === "" ? null : target);
        if (plan.length === 0) break;
        const answer = await tierWrite(plan[0]);
        if (!answer.ok) {
          harnessError = answer.error ?? `HTTP`;
          break;
        }
        catalog = applyStepTierWrite(catalog, plan[0]);
      }
      out.push({ harness, ok: !harnessError, text: harnessError ?? t("models.stepTierApplied") });
      if (harnessError) failed = true;
    }
    setResults(out);
    setBusy(false);
    if (!failed && onTierSaved) onTierSaved();
  };
  return (
    <span data-step-tier-cell="" style={{ display: "inline-flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
      <span data-step-tier-state="">
        {view.kind === "all" && t("models.stepTierAll", { tier: view.tier })}
        {view.kind === "unset" && t("models.stepTierUnset")}
        {view.kind === "specific" && <>
          <strong>{t("models.stepTierSpecific")}</strong>
          {view.per.map((entry) => <span key={entry.harness} data-step-tier-per={entry.harness}>
            {t("models.stepTierInconsistent", { harness: entry.harness, current: tierNameText(entry.tiers) })}
          </span>)}
        </>}
      </span>
      <select
        data-step-tier-select=""
        aria-label={t("models.stepTier")}
        disabled={busy}
        value={target}
        onChange={(event) => setDraft(event.target.value)}
      >
        <option value="">{t("models.stepTierUnset")}</option>
        {stepTierDisplayOrder([...new Set(tier.tiers.map((item) => item.tier))]).map((name) =>
          <option key={name} value={name}>{name}</option>)}
      </select>
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
    </span>
  );
}

export function HarnessModelTable({ rows, enabled, levels = {}, piSettings, onEffort, tier, tierWrite, onTierSaved }: {
  rows: ModelViewRow[];
  enabled?: EnabledModelsController;
  levels?: Record<string, string[]>;
  piSettings?: ModelPayload | null;
  onEffort?: (provider: string, id: string, level: string) => void;
  tier?: StepTierColumnView;
  tierWrite?: (write: StepTierWrite) => Promise<StepTierWriteOutcome>;
  onTierSaved?: () => void;
}) {
  const { t } = useI18n();
  return <table className="settings-model-table" style={{ width: "100%", textAlign: "left" }}>
    <thead><tr><th>{t("common.models")}</th><th>{t("models.availableFor")}</th><th>{t("models.chatAvailability")}</th><th>{t("models.reasoning")}</th>{tier && <th>{t("models.stepTier")}</th>}</tr></thead>
    <tbody>{rows.map((row) => {
      const key = `${row.provider}/${row.id}`;
      const supported = levels[`${row.provider}:${row.id}`] ?? [];
      return <tr key={key}>
        <td><strong>{row.name}</strong><br /><code>{key}</code></td>
        <td>{row.harnesses.join(", ")}</td>
        <td>
          {row.harnesses.includes("pi") && (enabled
            ? <label>pi <input type="checkbox" aria-label={t("models.enabledToggle", { model: row.name })}
                checked={row.chatAvailable} disabled={!enabled.view?.editable || enabled.pending !== null}
                onChange={(event) => enabled.setModels(key, [key], event.target.checked)} /></label>
            : <span>pi: {row.chatAvailable ? t("models.available") : t("models.unavailable")}</span>)}
          {row.harnesses.includes("claude") && <span> claude: {t("models.unsupported")}</span>}
        </td>
        <td>
          {onEffort && row.harnesses.includes("pi") && supported.length > 0 && <label>pi <select
            aria-label={`${t("models.reasoning")} — ${key}`}
            value={piSettings?.perModelReasoningEffort[key]?.raw ?? ""}
            onChange={(event) => onEffort(row.provider, row.id, event.target.value)}>
            <option value="">{t("i18n.default")}</option>
            {supported.map((level) => <option key={level} value={level}>{level}</option>)}
          </select></label>}
          <span>{row.reasoning.join(", ") || t("models.notProvided")}</span>
        </td>
        {tier && <td><StepTierCell row={row} tier={tier} tierWrite={tierWrite ?? (async () => ({ ok: false }))} onTierSaved={onTierSaved} /></td>}
      </tr>;
    })}</tbody>
  </table>;
}

export function HarnessModelSettings({ cwd, onClose, onOpenAccount }: { cwd: string | null; onClose?: () => void; onOpenAccount?: () => void }) {
  const { t } = useI18n();
  const enabled = useEnabledModels(cwd);
  const [models, setModels] = useState<ModelViewInput[]>([]);
  const [defaultModel, setDefaultModel] = useState<ModelIdentity | null>(null);
  const [piSettings, setPiSettings] = useState<ModelPayload | null>(null);
  const [levels, setLevels] = useState<Record<string, string[]>>({});
  const [error, setError] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);
  const [saving, setSaving] = useState(false);
  const [selectedProvider, setSelectedProvider] = useState<string | null>(null);
  const [tierColumn, setTierColumn] = useState<StepTierColumnView | null>(null);
  const query = cwd ? `?cwd=${encodeURIComponent(cwd)}` : "";

  useEffect(() => {
    const abort = new AbortController();
    const get = async (url: string) => {
      const response = await fetch(url, { signal: abort.signal });
      const data = await response.json();
      if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
      return data;
    };
    void Promise.allSettled([
      get(`/api/models?scope=all${cwd ? `&cwd=${encodeURIComponent(cwd)}` : ""}`),
      get("/api/models/default"),
      get("/api/claude"),
      get(`/api/harness/pi/model${query}`),
      get(`/api/harness/claude/model${query}`),
      get("/api/polyforge/config?host=1"),
    ]).then((answers) => {
      if (abort.signal.aborted) return;
      const value = (index: number) => answers[index].status === "fulfilled" ? answers[index].value : null;
      const catalog = value(0);
      const preference = value(1);
      const claude = value(2);
      const pi = value(3)?.data as ModelPayload | undefined;
      const claudeSettings = value(4)?.data as ModelPayload | undefined;
      // the Host's [step_tiers] catalog (85b2): the tier column's own read —
      // a failure or an incapable answer leaves the column read-only with
      // its reason, the other columns untouched
      const hostConfig = value(5);
      setTierColumn(hostConfig
        ? { capable: hostConfig.capable === true && !hostConfig.stepTiersReason, reason: hostConfig.stepTiersReason ?? hostConfig.reason ?? null, tiers: stepTierItems(hostConfig.items ?? []) }
        : { capable: false, reason: "the step-tier catalog read failed; polyforge may be unavailable", tiers: [] });
      const next: ModelViewInput[] = (catalog?.modelList ?? []).map((entry: { provider: string; id: string; name: string }) => ({
        harness: "pi", ...entry, chatAvailable: false,
        reasoning: pi?.perModelReasoningEffort[`${entry.provider}/${entry.id}`]?.raw ?? pi?.reasoningEffort?.raw ?? undefined,
      }));
      for (const model of claude?.models ?? []) {
        next.push({ harness: "claude", provider: "anthropic", id: model.value, name: model.displayName ?? model.value,
          chatAvailable: false, reasoning: claudeSettings?.perModelReasoningEffort[model.value]?.raw
            ?? claudeSettings?.reasoningEffort?.raw ?? undefined });
      }
      setModels(next);
      setPiSettings(pi ?? null);
      setLevels(catalog?.thinkingLevels ?? {});
      setDefaultModel(preference?.defaultHarness === "claude"
        ? { harness: "claude", provider: "anthropic", id: preference.claudeDefaultModel }
        : pi?.defaultProvider && pi.defaultModel
          ? { harness: "pi", provider: pi.defaultProvider, id: pi.defaultModel }
          : catalog?.defaultModel ? { harness: "pi", provider: catalog.defaultModel.provider, id: catalog.defaultModel.modelId } : null);
      const failed = answers.find((answer) => answer.status === "rejected");
      setError(failed?.status === "rejected" ? String(failed.reason?.message ?? failed.reason) : null);
    });
    return () => abort.abort();
  }, [cwd, query, revision]);

  const rows = useMemo(() => {
    const available = new Map(enabled.view?.providers.flatMap((provider) => provider.models.map((model) => [model.ref, model.enabled] as const)) ?? []);
    return buildModelRows(models.map((model) => ({ ...model,
      chatAvailable: model.harness === "pi" && available.get(`${model.provider}/${model.id}`) === true,
    })), defaultModel);
  }, [models, defaultModel, enabled.view]);

  const write = useCallback(async (url: string, body: unknown) => {
    setSaving(true);
    setError(null);
    try {
      const response = await fetch(url, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      const data = await response.json();
      if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
      setRevision((previous) => previous + 1);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally { setSaving(false); }
  }, []);

  // One tier-key write (85b2): a fresh Host read carries the CAS hash the
  // write expects — every step of a plan is its own read-then-write, so a
  // conflict (409) leaves the row's draft for the user to retry. The value
  // is the plan's whole-key list; the write gate judges the new candidates.
  const tierWrite = useCallback(async (writeStepTier: StepTierWrite): Promise<StepTierWriteOutcome> => {
    try {
      const read = await fetch("/api/polyforge/config?host=1").then((response) => response.json());
      const hash = typeof read?.file?.sha256 === "string" ? read.file.sha256 : null;
      const response = await fetch("/api/polyforge/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          action: writeStepTier.action,
          key: `step_tiers.${writeStepTier.tier}`,
          ...(writeStepTier.action === "set" ? { value: writeStepTier.candidates } : {}),
          host: true,
          ...(hash !== null ? { expect_state: { file_sha256: hash } } : {}),
        }),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok || data.error) return { ok: false, error: data.error ?? `HTTP ${response.status}` };
      return { ok: true };
    } catch (cause) {
      return { ok: false, error: cause instanceof Error ? cause.message : String(cause) };
    }
  }, []);
  const bumpTierRevision = useCallback(() => setRevision((previous) => previous + 1), []);

  const selected = defaultModel ? JSON.stringify(defaultModel) : "";
  return (
    <div className="settings-section settings-models" data-model-page="unified">
      <h2>{t("common.models")}</h2>
      {(error || enabled.failure) && <div role="alert">{error ?? enabled.failure?.message ?? (enabled.failure?.messageKey ? t(enabled.failure.messageKey) : "")}</div>}
      <div className="config-split-view">
      <ConfigSidebar><ConfigSidebarList>
        <ConfigSidebarItem active={selectedProvider === null} onClick={() => setSelectedProvider(null)}>{t("models.defaultHeading")}</ConfigSidebarItem>
        {[...new Set(rows.map((row) => row.provider))].map((provider) => <div key={provider}>
          <strong>{provider}</strong>
          {rows.filter((row) => row.provider === provider).map((row) => <ConfigSidebarItem
            key={row.id} active={selectedProvider === provider} onClick={() => setSelectedProvider(provider)}>
            {row.name} · {row.harnesses.join(", ")}
          </ConfigSidebarItem>)}
        </div>)}
      </ConfigSidebarList></ConfigSidebar>
      <ConfigDetail>
      <section aria-label={t("models.defaultHeading")}>
        <h3>{t("models.defaultHeading")}</h3>
        <select aria-label={t("models.defaultHeading")} disabled={saving} value={selected}
          onChange={(event) => {
            const choice = JSON.parse(event.target.value) as ModelIdentity;
            void write(`/api/models/default${query}`, choice.harness === "claude"
              ? { harness: "claude", model: choice.id, ...(cwd ? { cwd } : {}) }
              : { provider: choice.provider, modelId: choice.id, ...(cwd ? { cwd } : {}) });
          }}>
          <option value="" disabled>{t("models.notProvided")}</option>
          {defaultModel && !models.some((model) => model.harness === defaultModel.harness && model.provider === defaultModel.provider && model.id === defaultModel.id)
            && <option value={selected}>{defaultModel.harness} · {defaultModel.provider}/{defaultModel.id}</option>}
          {models.map((model) => <option key={`${model.harness}/${model.provider}/${model.id}`}
            value={JSON.stringify({ harness: model.harness, provider: model.provider, id: model.id })}>
            {model.harness} · {model.provider}/{model.id}
          </option>)}
        </select>
      </section>
      <section aria-label={t("models.listHeading")}>
        <h3>{t("models.listHeading")}</h3>
        {tierColumn && !tierColumn.capable && tierColumn.reason && (
          <p role="note" data-step-tier-reason="" style={{ color: "var(--text-dim)", margin: "0 0 4px" }}>{tierColumn.reason}</p>
        )}
        {tierColumn?.capable && (() => {
          // the catalog's references no listed row answers (a deleted model, a
          // migrated claude name) — noted once, never silently dropped
          const defined = { pi: rows.filter((row) => row.harnesses.includes("pi")).map((row) => `${row.provider}/${row.id}`),
            claude: rows.filter((row) => row.harnesses.includes("claude")).map((row) => row.id) };
          const invalid = stepTierInvalidReferences(tierColumn.tiers, defined);
          return invalid.length > 0 ? (
            <p role="note" data-step-tier-invalid="" style={{ color: "var(--text-dim)", margin: "0 0 4px" }}>
              {t("models.stepTierInvalidNote", { candidates: invalid.join("、") })}
            </p>
          ) : null;
        })()}
        <HarnessModelTable rows={selectedProvider ? rows.filter((row) => row.provider === selectedProvider) : rows} enabled={enabled} levels={levels} piSettings={piSettings}
          tier={tierColumn ?? undefined} tierWrite={tierWrite} onTierSaved={bumpTierRevision}
          onEffort={(provider, id, level) => { void write(`/api/harness/pi/model${query}`, { modelThinkingLevel: { key: `${provider}/${id}`, level: level || null } }); }} />
      </section>
      <section aria-label="pi">
        <h3>pi</h3>
        <ModelsConfig embedded cwd={cwd} onClose={onClose ?? (() => {})} onOpenAccount={onOpenAccount} enabledModels={enabled} />
      </section>
      <section aria-label="claude">
        <h3>claude</h3>
        <ClaudeCustomModels onRefresh={() => setRevision((previous) => previous + 1)} />
      </section>
      </ConfigDetail>
      </div>
    </div>
  );
}
