"use client";

import { useEffect, useMemo, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { buildModelRows, type ModelViewInput } from "@/lib/harness/model-view";
import { ModelsConfig } from "./ModelsConfig";

export function HarnessModelSettings({ cwd, onClose }: { cwd: string | null; onClose?: () => void }) {
  const { t } = useI18n();
  const [models, setModels] = useState<ModelViewInput[]>([]);
  const [defaultModel, setDefaultModel] = useState<{ harness: string; provider: string; id: string } | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    void Promise.all([
      fetch(`/api/models?scope=all${cwd ? `&cwd=${encodeURIComponent(cwd)}` : ""}`).then((response) => response.json()),
      fetch("/api/models/default").then((response) => response.json()),
    ]).then(([catalog, preference]) => {
      if (!active) return;
      const entries = Array.isArray(catalog.modelList) ? catalog.modelList : [];
      setModels(entries.map((entry: { provider?: string; id?: string; name?: string }) => ({
        harness: "pi", provider: entry.provider ?? "unknown", id: entry.id ?? "", name: entry.name ?? entry.id ?? "",
        chatAvailable: true,
      })).filter((entry: ModelViewInput) => entry.id));
      const selected = preference?.defaultModel;
      if (selected?.provider && selected?.modelId) setDefaultModel({ harness: selected.harness ?? "pi", provider: selected.provider, id: selected.modelId });
    }).catch(() => { if (active) setError(t("models.enabledUnavailable")); });
    return () => { active = false; };
  }, [cwd, t]);

  const rows = useMemo(() => buildModelRows(models, defaultModel), [models, defaultModel]);
  return (
    <div className="settings-section settings-models" data-settings-section="models">
      <h2>{t("common.models")}</h2>
      <p className="settings-description">模型默认值、provider 清单与 pi 专属编辑。</p>
      {error && <div className="settings-error">{error}</div>}
      <section aria-label="默认模型">
        <h3>默认模型</h3>
        <p>{rows.find((row) => row.isDefault)?.name ?? "未设置"}</p>
      </section>
      <section aria-label="模型清单">
        <h3>模型清单</h3>
        <div role="table" className="settings-model-table">
          <div role="row" className="settings-model-table-header"><span>可用于</span><span>聊天可用</span><span>{t("models.reasoning")}</span></div>
          {rows.map((row) => <div role="row" key={`${row.provider}/${row.id}`}><span>{row.provider} · {row.name}</span><span>{row.harnesses.join(", ")}</span><span>{row.harnesses.includes("claude") ? "不支持" : row.chatAvailable ? "是" : "否"}</span><span>{row.reasoning.join(", ") || t("models.notProvided")}</span></div>)}
        </div>
      </section>
      <section aria-label="pi">
        <h3>pi</h3>
        <ModelsConfig embedded cwd={cwd} onClose={onClose ?? (() => {})} />
      </section>
    </div>
  );
}
