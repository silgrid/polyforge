"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { buildModelRows, type ModelViewInput, type ModelViewRow } from "@/lib/harness/model-view";
import type { ModelPayload } from "@/lib/harness/concepts";
import {
  stepTierInvalidReferences,
  stepTierItems,
  stepTierRowView,
  stepTierState,
  type StepTierItem,
} from "@/lib/step-tiers";
import { useEnabledModels, type EnabledModelsController } from "./EnabledModelsSection";
import { ModelsConfig } from "./ModelsConfig";
import { ClaudeCustomModels } from "./ClaudeCustomModels";
import { ConfigSidebar, ConfigSidebarList, ConfigSidebarItem, ConfigDetail } from "./SettingsUi";

type ModelIdentity = { harness: string; provider: string; id: string };

/** The tier column's own view of the Host's [step_tiers] catalog (85b2):
 *  the parsed tier items and why the column is read-only when it is. */
export interface StepTierColumnView {
  capable: boolean;
  reason: string | null;
  tiers: StepTierItem[];
}

/** The raw Host config answer the tier column reads (the panel's own shape):
 *  the version facts localize the read-only reason, the items are the catalog
 *  (polyforge#256 B1: the reason is rendered from i18n, never the server's
 *  English sentence). */
export interface HostTierConfig {
  pfPresent: boolean | null;
  pfVersion: string | null;
  capable: boolean;
  stepTiersReason: string | null;
  items: Array<{ key: string; value?: unknown }>;
}

/** Why the tier column is read-only, localized from the read's facts: not
 *  installed, a version below the family's `0.2.4-0` floor (its found version
 *  named), or an unreadable catalog. */
export function stepTierReasonText(host: HostTierConfig, t: (key: string, params?: Record<string, string | number>) => string): string {
  if (host.pfPresent === false) return t("models.stepTierReasonNotInstalled");
  if (host.stepTiersReason) return t("models.stepTierReasonVersion", { version: host.pfVersion ?? t("models.stepTierReasonUnknownVersion") });
  return t("models.stepTierReasonUnavailable");
}

/** The identity a row's harness addresses in the catalog: pi's
 *  provider/id, claude's model name (Q2's identity rule — the same one the
 *  write gate judges). */
function stepTierIdentity(row: Pick<ModelViewRow, "provider" | "id">, harness: string): { harness: "pi" | "claude"; model: string } | null {
  if (harness === "pi") return { harness: "pi", model: `${row.provider}/${row.id}` };
  if (harness === "claude") return { harness: "claude", model: row.id };
  return null;
}

/** One row's step-tier cell, read-only (polyforge#256 B7): the current state
 *  as short badges — a single shared tier shows its name, differing harnesses
 *  show `pi: default` / `claude: fast`, an unset or incapable row shows 「—」.
 *  The editing entry point is gone (T3 owns the next editor): no select, no
 *  apply-to control, no write callback, so opening the page can never write a
 *  default tier. `pf config set step_tiers… --host` and the write route keep
 *  their gates (lib/polyforge-config-write.ts). */
export function StepTierBadges({ row, tier }: { row: ModelViewRow; tier: StepTierColumnView }) {
  if (!tier.capable) {
    return <span data-step-tier-cell="readonly" data-step-tier-state="readonly" style={{ color: "var(--text-dim)" }}>—</span>;
  }
  const identities = row.harnesses
    .map((harness) => stepTierIdentity(row, harness))
    .filter((identity): identity is { harness: "pi" | "claude"; model: string } => identity !== null);
  const perHarness: Record<string, string[]> = {};
  for (const identity of identities) perHarness[identity.harness] = stepTierState(tier.tiers, identity);
  const view = stepTierRowView(identities.map((identity) => identity.harness), perHarness);
  if (view.kind === "unset") {
    return <span data-step-tier-cell="readonly" data-step-tier-state="unset" style={{ color: "var(--text-dim)" }}>—</span>;
  }
  if (view.kind === "all") {
    return <span data-step-tier-cell="readonly" data-step-tier-state="all">
      <span data-step-tier-badge="all">{view.tier}</span>
    </span>;
  }
  return <span data-step-tier-cell="readonly" data-step-tier-state="specific">
    {view.per.map((entry) => <span key={entry.harness} data-step-tier-badge={entry.harness}>
      {entry.harness}: {entry.tiers.length === 0 ? "—" : entry.tiers.join("、")}
    </span>)}
  </span>;
}

export function HarnessModelTable({ rows, enabled, levels = {}, piSettings, onEffort, tier }: {
  rows: ModelViewRow[];
  enabled?: EnabledModelsController;
  levels?: Record<string, string[]>;
  piSettings?: ModelPayload | null;
  onEffort?: (provider: string, id: string, level: string) => void;
  tier?: StepTierColumnView;
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
        {tier && <td><StepTierBadges row={row} tier={tier} /></td>}
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
  const [hostTierConfig, setHostTierConfig] = useState<HostTierConfig | null>(null);
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
      const hostConfig = value(5) as Partial<HostTierConfig> | undefined;
      setHostTierConfig(hostConfig
        ? {
          pfPresent: hostConfig.pfPresent === false ? false : hostConfig.pfPresent === true ? true : null,
          pfVersion: typeof hostConfig.pfVersion === "string" ? hostConfig.pfVersion : null,
          capable: hostConfig.capable === true,
          stepTiersReason: typeof hostConfig.stepTiersReason === "string" ? hostConfig.stepTiersReason : null,
          items: Array.isArray(hostConfig.items) ? hostConfig.items : [],
        }
        : { pfPresent: null, pfVersion: null, capable: false, stepTiersReason: null, items: [] });
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

  // The tier column is read-only (polyforge#256 B7): one Host read at page
  // load, localized in render, and no write path at all.
  const tierColumn = useMemo<StepTierColumnView | null>(() => hostTierConfig === null ? null : {
    capable: hostTierConfig.capable && !hostTierConfig.stepTiersReason,
    reason: hostTierConfig.capable && !hostTierConfig.stepTiersReason ? null : stepTierReasonText(hostTierConfig, t),
    tiers: stepTierItems(hostTierConfig.items),
  }, [hostTierConfig, t]);

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
          tier={tierColumn ?? undefined}
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
