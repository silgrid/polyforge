"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { buildModelRows, type ModelViewInput, type ModelViewRow } from "@/lib/harness/model-view";
import type { ModelPayload } from "@/lib/harness/concepts";
import { useEnabledModels, type EnabledModelsController } from "./EnabledModelsSection";
import { ModelsConfig } from "./ModelsConfig";
import { ConfigSidebar, ConfigSidebarList, ConfigSidebarItem, ConfigDetail } from "./SettingsUi";

type ModelIdentity = { harness: string; provider: string; id: string };

export function HarnessModelTable({ rows, enabled, levels = {}, piSettings, onEffort }: {
  rows: ModelViewRow[];
  enabled?: EnabledModelsController;
  levels?: Record<string, string[]>;
  piSettings?: ModelPayload | null;
  onEffort?: (provider: string, id: string, level: string) => void;
}) {
  const { t } = useI18n();
  return <table className="settings-model-table" style={{ width: "100%", textAlign: "left" }}>
    <thead><tr><th>{t("common.models")}</th><th>{t("models.availableFor")}</th><th>{t("models.chatAvailability")}</th><th>{t("models.reasoning")}</th></tr></thead>
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
    ]).then((answers) => {
      if (abort.signal.aborted) return;
      const value = (index: number) => answers[index].status === "fulfilled" ? answers[index].value : null;
      const catalog = value(0);
      const preference = value(1);
      const claude = value(2);
      const pi = value(3)?.data as ModelPayload | undefined;
      const claudeSettings = value(4)?.data as ModelPayload | undefined;
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
        <HarnessModelTable rows={selectedProvider ? rows.filter((row) => row.provider === selectedProvider) : rows} enabled={enabled} levels={levels} piSettings={piSettings}
          onEffort={(provider, id, level) => { void write(`/api/harness/pi/model${query}`, { modelThinkingLevel: { key: `${provider}/${id}`, level: level || null } }); }} />
      </section>
      <section aria-label="pi">
        <h3>pi</h3>
        <ModelsConfig embedded cwd={cwd} onClose={onClose ?? (() => {})} onOpenAccount={onOpenAccount} enabledModels={enabled} />
      </section>
      </ConfigDetail>
      </div>
    </div>
  );
}
