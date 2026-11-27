"use client";

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useI18n } from "@/hooks/useI18n";
import { buildModelRows, type ModelViewInput, type ModelViewRow } from "@/lib/harness/model-view";
import type { ModelPayload } from "@/lib/harness/concepts";
import {
  stepTierInvalidReferences,
  stepTierItems,
  stepTierRowView,
  stepTierState,
  type StepTierItem,
  type StepTierWrite,
} from "@/lib/step-tiers";
import { EnabledModelsBanner, useEnabledModels, type EnabledModelsController } from "./EnabledModelsSection";
import {
  draftDirtyCount,
  draftWrites,
  emptyDraft,
  setDraftDefaultModel,
  setDraftEffort,
  settleWrite,
  type ModelPageDraft,
} from "@/lib/model-page-draft";
import { ModelsConfig, type ModelConnection, type ModelConnectionCatalog } from "./ModelsConfig";
import { AccountSettings, useAccountRead } from "./AccountSettings";
import { AddProviderPicker } from "./PiAccountLogin";
import { ConnectionOverview } from "./ConnectionOverview";
import { ProviderIcon } from "./ProviderIcon";
import { NativeModelTiers } from "./NativeModelTiers";
import { modelReferenceImpact, modelReferenceError } from "@/lib/model-reference-impact";
import { useSettingsDraft } from "./SettingsDrafts";
import { ClaudeCustomModels } from "./ClaudeCustomModels";
import { ModelStepTierDrawer, stepTierIdentity, type StepTierRead, type StepTierWriteFn } from "./ModelStepTierDrawer";
import { readView } from "@/lib/settings-read-states";
import {
  ConfigButton,
  ConfigEmptyState,
  ConfigErrorNotice,
  ConfigSidebar,
  ConfigSidebarItem,
  ConfigSidebarGroupLabel,
  ConfigSidebarList,
  ConfigDetail,
  ConfigSkeleton,
  ConfigSplitView,
  useConfigDetailNavigation,
} from "./SettingsUi";

type ModelIdentity = { harness: string; provider: string; id: string };

/** Main destinations plus object-specific detail pages. */
export type ModelSubpage = "connections" | "account-claude" | "default" | "list" | "providers" | "claude" | "tiers";

const MODEL_SUBPAGES: ReadonlyArray<{ id: ModelSubpage; labelKey: string }> = [
  { id: "default", labelKey: "models.defaultHeading" },
  { id: "list", labelKey: "models.listHeading" },
  { id: "tiers", labelKey: "models.tiersHeading" },
];

/** The main model destinations; object editors belong to connections. */
export function ModelPageNav({ page, onSelect, connections = [], onConnection, selectedConnection, onOpenTasks }: { onOpenTasks?: () => void; page: ModelSubpage; onSelect: (page: ModelSubpage) => void; connections?: ModelConnection[]; onConnection?: (connection: ModelConnection) => void; selectedConnection?: ModelConnection | null }) {
  const { t } = useI18n();
  return (
    <ConfigSidebar>
      <ConfigSidebarGroupLabel>{t("settings.connections")}</ConfigSidebarGroupLabel>
      <div className="settings-connection-nav">
        <ConfigSidebarItem data-model-subpage="connections" active={page === "connections" && !selectedConnection} onClick={() => onSelect("connections")}><span aria-hidden="true" className="settings-nav-symbol">↗</span><span className="config-sidebar-text">{t("settings.allConnections")}</span></ConfigSidebarItem>
        <ConfigSidebarItem data-model-subpage="account-claude" active={page === "account-claude"} onClick={() => onSelect("account-claude")}><ProviderIcon id="anthropic" size={15} />{t("settings.accountHarnessClaude")}</ConfigSidebarItem>
        {connections.map((connection) => <ConfigSidebarItem key={`${connection.kind}:${connection.id}`} data-connection-id={`${connection.kind}:${connection.id}`} active={selectedConnection?.id === connection.id && selectedConnection.kind === connection.kind} onClick={() => onConnection?.(connection)}><ProviderIcon id={connection.id} size={15} /><span className="config-sidebar-text">{connection.label}</span><small>{connection.kind === "oauth" ? "OAuth" : "API"}</small></ConfigSidebarItem>)}
      </div>
      <ConfigSidebarGroupLabel>{t("settings.modelSettings")}</ConfigSidebarGroupLabel>
      <ConfigSidebarList>
        {MODEL_SUBPAGES.map((subpage) => (
          <ConfigSidebarItem
            key={subpage.id}
            data-model-subpage={subpage.id}
            active={page === subpage.id && !selectedConnection}
            onClick={() => onSelect(subpage.id)}
          >
            <span className="settings-nav-symbol" aria-hidden="true">{subpage.id === "default" ? "☆" : subpage.id === "list" ? "☷" : subpage.id === "tiers" ? "◇" : "⚙"}</span><span className="config-sidebar-text">{t(subpage.labelKey)}</span>
          </ConfigSidebarItem>
        ))}
      </ConfigSidebarList>
      {onOpenTasks && <details className="settings-model-advanced"><summary>{t("models.advancedSettings")}</summary><ConfigSidebarItem data-model-task-entry="" onClick={onOpenTasks}>{t("polish.automaticTasks")}</ConfigSidebarItem></details>}
    </ConfigSidebar>
  );
}

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

/** One row's step-tier cell (polyforge#256 B7, T3): the current state as short
 *  badges — a single shared tier shows its name, differing harnesses show
 *  `pi: default` / `claude: fast`, an unset or incapable row shows 「—」. A
 *  capable cell is one small label button that opens the row's drawer
 *  (ModelStepTierDrawer); no editor lives inline. */
export function StepTierBadges({ row, tier, onOpen }: { row: ModelViewRow; tier: StepTierColumnView; onOpen?: (row: ModelViewRow) => void }) {
  const { t } = useI18n();
  if (!tier.capable) {
    return <span data-step-tier-cell="readonly" data-step-tier-state="readonly" style={{ color: "var(--text-dim)" }}>—</span>;
  }
  const identities = row.harnesses
    .map((harness) => stepTierIdentity(row, harness))
    .filter((identity): identity is { harness: "pi" | "claude"; model: string } => identity !== null);
  const perHarness: Record<string, string[]> = {};
  for (const identity of identities) perHarness[identity.harness] = stepTierState(tier.tiers, identity);
  const view = stepTierRowView(identities.map((identity) => identity.harness), perHarness);
  const content = view.kind === "unset"
    ? <span data-step-tier-state="unset" style={{ color: "var(--text-dim)" }}>—</span>
    : view.kind === "all"
      ? <span data-step-tier-state="all"><span data-step-tier-badge="all">{view.tier}</span></span>
      : <span data-step-tier-state="specific">
        {view.per.map((entry) => <span key={entry.harness} data-step-tier-badge={entry.harness}>
          {entry.harness}: {entry.tiers.length === 0 ? "—" : entry.tiers.join("、")}
        </span>)}
      </span>;
  return <button
    type="button"
    className="settings-step-tier-button"
    data-step-tier-cell="editable"
    data-step-tier-open=""
    aria-haspopup="dialog"
    title={t("models.stepTierEdit")}
    onClick={() => onOpen?.(row)}
  >{content}</button>;
}

export function HarnessModelTable({ rows, enabled, levels = {}, piSettings, effortValues = {}, onEffort, tier, onOpenTier, saving = false }: {
  rows: ModelViewRow[];
  enabled?: EnabledModelsController;
  levels?: Record<string, string[]>;
  piSettings?: ModelPayload | null;
  /** The unsaved per-model levels (T3's draft), keyed `provider/id`. */
  effortValues?: Record<string, string>;
  onEffort?: (provider: string, id: string, level: string) => void;
  tier?: StepTierColumnView;
  onOpenTier?: (row: ModelViewRow) => void;
  /** The explicit save is in flight: the draft must not accept a newer edit. */
  saving?: boolean;
}) {
  const { t } = useI18n();
  return <table className="settings-model-table" style={{ width: "100%", textAlign: "left" }}>
    <thead><tr><th>{t("common.models")}</th><th>{t("models.availableFor")}</th><th>{t("models.chatAvailability")}</th><th>{t("models.reasoning")}</th>{tier && <th>{t("models.stepTier")}</th>}</tr></thead>
    <tbody>{rows.map((row) => {
      const key = `${row.provider}/${row.id}`;
      const supported = levels[`${row.provider}:${row.id}`] ?? [];
      return <tr key={key}>
        <td><strong>{row.name}</strong><br /><code>{key}</code></td>
        <td data-label={t("models.availableFor")}>{row.harnesses.join(", ")}</td>
        <td data-label={t("models.chatAvailability")}>
          {row.harnesses.includes("pi") && (enabled
            ? <label>pi <input type="checkbox" aria-label={t("models.enabledToggle", { model: row.name })}
                checked={row.chatAvailable} disabled={!enabled.view?.editable || enabled.pending !== null}
                onChange={(event) => enabled.setModels(key, [key], event.target.checked)} /></label>
            : <span>pi: {row.chatAvailable ? t("models.available") : t("models.unavailable")}</span>)}
          {row.harnesses.includes("claude") && <span data-claude-chat="always" title={t("models.chatAlwaysOnReason")}>
            {" "}claude: {t("models.chatAlwaysOn")}<span className="settings-model-info" aria-hidden="true"> ⓘ</span>
          </span>}
        </td>
        <td data-label={t("models.reasoning")}>
          {onEffort && row.harnesses.includes("pi") && supported.length > 0
            ? <label>pi <select
              aria-label={`${t("models.reasoning")} — ${key}`}
              disabled={saving}
              value={key in effortValues ? effortValues[key] : piSettings?.perModelReasoningEffort[key]?.raw ?? ""}
              onChange={(event) => onEffort(row.provider, row.id, event.target.value)}>
              <option value="">{t("models.followGlobal", { level: piSettings?.reasoningEffort?.raw ?? t("models.notProvided") })}</option>
              {supported.map((level) => <option key={level} value={level}>{level}</option>)}
            </select></label>
            : <span data-reasoning="readonly">{row.reasoning.join(", ") || t("models.notProvided")}</span>}
        </td>
        {tier && <td data-label={t("models.stepTier")}><StepTierBadges row={row} tier={tier} onOpen={onOpenTier} /></td>}
      </tr>;
    })}</tbody>
  </table>;
}

/** The one explicit save action of the model subpages (T3): nothing is
 *  written on change; the count names the fields still in the draft. */
export function ModelSaveBar({ count, saving, onSave }: { count: number; saving: boolean; onSave: () => void }) {
  const { t } = useI18n();
  return <div className="settings-model-savebar">
    <ConfigButton data-model-save="" variant="primary" disabled={saving || count === 0} onClick={onSave}>
      {count > 0 ? t("models.saveChanges", { count }) : t("i18n.save")}
    </ConfigButton>
  </div>;
}

/** The model list's provider-wide enable/disable entry (T3): chat
 *  availability has one home, so the provider switch that used to sit in the
 *  Provider editor lives here as a bulk action instead. */
export function ModelEnabledBulk({ enabled }: { enabled: EnabledModelsController }) {
  const { t } = useI18n();
  const [provider, setProvider] = useState("");
  const providers = enabled.view?.providers ?? [];
  if (providers.length === 0) return null;
  const editable = enabled.view?.editable === true;
  const busy = enabled.pending !== null;
  return <div className="settings-model-bulk" data-model-bulk="">
    <label className="settings-model-bulk-field">
      <span>{t("models.providerLabel")}</span>
      <select aria-label={t("models.providerLabel")} value={provider} onChange={(event) => setProvider(event.target.value)} disabled={busy}>
        <option value="">{t("models.providerLabel")}</option>
        {providers.map((entry) => <option key={entry.id} value={entry.id}>{entry.name}</option>)}
      </select>
    </label>
    <ConfigButton size="small" disabled={!editable || !provider || busy}
      onClick={() => { if (provider) enabled.setProvider(provider, true); }}>{t("models.enableAll")}</ConfigButton>
    <ConfigButton size="small" disabled={!editable || !provider || busy}
      onClick={() => { if (provider) enabled.setProvider(provider, false); }}>{t("models.disableAll")}</ConfigButton>
  </div>;
}

/** 模型页读取失败时的页顶状态（polyforge#271 AC1/A3）：alert + 重试，纯展示。 */
export function ModelPageError({ summary, onRetry, t }: { summary: ReactNode; onRetry: () => void; t: (key: string) => string }) {
  return (
    <ConfigErrorNotice
      summary={summary}
      action={<ConfigButton size="small" data-model-retry="" onClick={onRetry}>{t("i18n.retry")}</ConfigButton>}
    />
  );
}

/** 模型集合成功读回但为空时的空状态（polyforge#271 AC1/A3）：点明缺什么，
 *  并给出两个真实动作——去账号连接、去 Providers 配置。 */
export function ModelEmptyState({ onOpenAccount, onOpenProviders }: { onOpenAccount?: () => void; onOpenProviders?: () => void }) {
  const { t } = useI18n();
  return (
    <ConfigEmptyState action={(
      <>
        {onOpenAccount && <ConfigButton data-model-action="open-account" onClick={onOpenAccount}>{t("models.openAccount")}</ConfigButton>}
        {onOpenProviders && <ConfigButton data-model-action="open-providers" onClick={onOpenProviders}>{t("models.openProviders")}</ConfigButton>}
      </>
    )}>
      {t("models.noModels")} {t("models.noModelsHint")}
    </ConfigEmptyState>
  );
}

export function HarnessModelSettings({ cwd, onClose, onOpenAccount: externalOpenAccount, onOpenPolyforge, initialSubpage = "connections", addProviderRequest = 0, onUnsavedChange }: {
  cwd: string | null;
  onClose?: () => void;
  onOpenAccount?: () => void;
  onOpenPolyforge?: () => void;
  /** A component test's entry point; the page opens on the default subpage. */
  initialSubpage?: ModelSubpage;
  addProviderRequest?: number;
  /** The page reports its unsaved draft so Settings can guard a close (owner plan review item 1 A). */
  onUnsavedChange?: (dirty: boolean) => void;
}) {
  const { t } = useI18n();
  const enabled = useEnabledModels(cwd);
  const { openDetail, reportDetailTitle } = useConfigDetailNavigation();
  const [page, setPage] = useState<ModelSubpage>(initialSubpage);
  // The Provider editor owns a models.json draft. Once visited it stays
  // mounted (hidden), so switching subpages never throws that draft away; it
  // is not fetched before the first visit.
  const [connections, setConnections] = useState<ModelConnection[]>([]);
  const [connectionCatalog, setConnectionCatalog] = useState<ModelConnectionCatalog>({ oauthProviders: [], apiKeyProviders: [], loading: true, error: null });
  const account = useAccountRead();
  const [connectionPickerOpen, setConnectionPickerOpen] = useState(false);
  const [accountsVisited, setAccountsVisited] = useState(initialSubpage === "account-claude");
  const [taskAuthOpen, setTaskAuthOpen] = useState(false);
  const [taskAuthVisited, setTaskAuthVisited] = useState(false);
  const [selectedConnection, setSelectedConnection] = useState<ModelConnection | null>(null);
  const modelRootRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    // A newly chosen native connection is not in the connected sidebar yet.
    if (selectedConnection && !modelRootRef.current?.closest("[hidden]")) reportDetailTitle(selectedConnection.label);
  }, [selectedConnection, reportDetailTitle]);
  useLayoutEffect(() => {
    modelRootRef.current?.querySelector<HTMLElement>(":scope > .config-split-view > .config-detail")?.scrollTo?.({ top: 0 });
  }, [page, selectedConnection?.kind, selectedConnection?.id]);
  const [customRequest, setCustomRequest] = useState(0);
  const [visited, setVisited] = useState<Set<ModelSubpage>>(() => new Set([initialSubpage]));
  const openConnection = (connection: ModelConnection) => {
    openDetail();
    setTaskAuthOpen(false);
    setSelectedConnection(connection);
    setPage(connection.kind === "provider" ? "providers" : "connections");
    if (connection.kind !== "provider") setAccountsVisited(true);
  };
  const openClaude = () => { setPage("account-claude"); setSelectedConnection(null); setAccountsVisited(true); setTaskAuthOpen(false); };
  const openTaskAuth = () => { setPage("account-claude"); setTaskAuthVisited(true); setTaskAuthOpen(true); };
  const onOpenAccount = () => { setTaskAuthOpen(false); setPage("connections"); setSelectedConnection(null); externalOpenAccount?.(); };
  const openCustom = () => { setPage("providers"); setSelectedConnection(null); setCustomRequest((value) => value + 1); };
  useEffect(() => {
    if (!addProviderRequest) return;
    // Keep every mounted page's draft; navigation is not a save or discard.
    setPage("providers");
    setSelectedConnection(null);
  }, [addProviderRequest]);
  const [draft, setDraft] = useState<ModelPageDraft>(emptyDraft);
  const [drawerRow, setDrawerRow] = useState<ModelViewRow | null>(null);
  // The drawer keeps the catalog it opened with, so a background re-read never
  // unmounts it mid-edit (the CAS re-read on save is what guards the write).
  const [drawerTier, setDrawerTier] = useState<StepTierColumnView | null>(null);
  const [models, setModels] = useState<ModelViewInput[]>([]);
  const [claudeModelCount, setClaudeModelCount] = useState<number | null>(null);
  const [defaultModel, setDefaultModel] = useState<ModelIdentity | null>(null);
  const [piSettings, setPiSettings] = useState<ModelPayload | null>(null);
  const [levels, setLevels] = useState<Record<string, string[]>>({});
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [revision, setRevision] = useState(0);
  const [saving, setSaving] = useState(false);
  // A save failure is not a read failure: a reload after a landed write must
  // not clear the message that belongs to the failed draft entry.
  const [saveError, setSaveError] = useState<string | null>(null);
  const [hostTierConfig, setHostTierConfig] = useState<HostTierConfig | null>(null);
  const query = cwd ? `?cwd=${encodeURIComponent(cwd)}` : "";
  const refreshEnabled = enabled.refresh;
  useEffect(() => {
    const refresh = () => { setRevision((previous) => previous + 1); refreshEnabled(); };
    window.addEventListener("pi-web:connections-changed", refresh);
    return () => window.removeEventListener("pi-web:connections-changed", refresh);
  }, [refreshEnabled]);

  useEffect(() => {
    const abort = new AbortController();
    setLoading(true);
    setError(null);
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
      setClaudeModelCount(Array.isArray(claude?.models) ? claude.models.length : null);
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
          : null);
      const failed = answers.find((answer) => answer.status === "rejected");
      setError(failed?.status === "rejected" ? String(failed.reason?.message ?? failed.reason) : null);
      setLoading(false);
    });
    return () => abort.abort();
  }, [cwd, query, revision]);

  const rows = useMemo(() => {
    const available = new Map(enabled.view?.providers.flatMap((provider) => provider.models.map((model) => [model.ref, model.enabled] as const)) ?? []);
    return buildModelRows(models.map((model) => ({ ...model,
      chatAvailable: model.harness === "pi" && available.get(`${model.provider}/${model.id}`) === true,
    })), defaultModel);
  }, [models, defaultModel, enabled.view]);

  const [search, setSearch] = useState("");
  const [impactBusy, setImpactBusy] = useState(false);
  const guardedEnabled: EnabledModelsController = { ...enabled,
    pending: impactBusy ? "reference-check" : enabled.pending,
    setModels: (key, refs, value) => {
      if (value) { enabled.setModels(key, refs, value); return; }
      setImpactBusy(true);
      void modelReferenceImpact(refs, cwd).then((references) => {
        if (references.some((reference) => reference.startsWith("default ·"))) {
          setSaveError(t("models.referenceBlocked", { references: references.join("; ") })); return;
        }
        if (!references.length || window.confirm(t("models.visibilityImpact", { references: references.join("; ") }))) enabled.setModels(key, refs, false);
      }).catch((cause) => setSaveError(modelReferenceError(cause, t))).finally(() => setImpactBusy(false));
    },
    setProvider: (provider, value) => {
      if (value) { enabled.setProvider(provider, true); return; }
      const refs = enabled.view?.providers.find((entry) => entry.id === provider)?.models.map((model) => model.ref) ?? [];
      guardedEnabled.setModels(`provider:${provider}`, refs, false);
    },
  };
  const dirty = draftDirtyCount(draft);
  const saveDraft = useCallback(async () => {
    if (saving) return;
    const writes = draftWrites(draft, cwd);
    if (writes.length === 0) return;
    setSaving(true);
    setError(null);
    setSaveError(null);
    let next = draft;
    for (const request of writes) {
      try {
        const response = await fetch(request.url, {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(request.body),
        });
        const data = await response.json();
        if (!response.ok || data.error) throw new Error(data.message ?? data.error ?? `HTTP ${response.status}`);
        // A landed write is the saved value from now on: the UI shows it even
        // when a later write of the same round fails, and the draft keeps only
        // the failed entries for a retry. The controls are disabled while
        // saving, so this settlement never races a newer edit.
        if (request.kind === "default" && next.defaultModel) setDefaultModel(next.defaultModel);
        if (request.kind === "effort" && request.key) {
          const level = next.efforts[request.key] ?? "";
          setPiSettings((previous) => previous ? {
            ...previous,
            perModelReasoningEffort: { ...previous.perModelReasoningEffort, [request.key as string]: { neutral: null, raw: level } },
          } : previous);
        }
        next = settleWrite(next, request);
        setDraft(next);
        setRevision((previous) => previous + 1);
      } catch (cause) {
        setSaveError(cause instanceof Error ? cause.message : String(cause));
        break;
      }
    }
    setSaving(false);
  }, [cwd, draft, saving]);

  // One Host catalog read: the items the plan derives from AND the file hash
  // the write expects, from the SAME GET — the CAS guards exactly that state.
  const tierRead: StepTierRead = useCallback(async () => {
    try {
      const response = await fetch("/api/polyforge/config?host=1");
      const data = await response.json().catch(() => null);
      if (!response.ok || data?.error) return { ok: false, error: data?.error ?? `HTTP ${response.status}` };
      return {
        ok: true,
        items: Array.isArray(data?.items) ? data.items.map((item: { key?: unknown; value?: unknown }) => ({ key: typeof item.key === "string" ? item.key : "", value: item.value })) : [],
        hash: typeof data?.file?.sha256 === "string" ? data.file.sha256 : null,
      };
    } catch (cause) {
      return { ok: false, error: cause instanceof Error ? cause.message : String(cause) };
    }
  }, []);

  // One tier-key write; the CAS hash comes from the same read that planned it.
  const tierWrite: StepTierWriteFn = useCallback(async (write: StepTierWrite, hash: string | null) => {
    try {
      const response = await fetch("/api/polyforge/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          action: write.action,
          key: `step_tiers.${write.tier}`,
          ...(write.action === "set" ? { value: write.candidates } : {}),
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

  // The unsaved draft is reported to Settings and guarded on the page itself:
  // a subpage switch asks first, a close asks before anything is lost.
  useEffect(() => { onUnsavedChange?.(dirty > 0); }, [dirty, onUnsavedChange]);
  useEffect(() => {
    if (dirty === 0) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  const managed = useSettingsDraft({ label: t("models.defaultAndReasoning"), reveal: () => setPage(draft.defaultModel ? "default" : "list"), count: dirty, busy: saving, save: () => void saveDraft(), discard: () => { setDraft(emptyDraft()); setSaveError(null); } });
  const changePage = (next: ModelSubpage) => {
    setTaskAuthOpen(false);
    setSelectedConnection(null);
    if (next === "account-claude") { setAccountsVisited(true); setTaskAuthOpen(false); }
    setVisited((previous) => new Set(previous).add(next));
    setPage(next);
  };

  // The tier column is read-only (polyforge#256 B7): one Host read at page
  // load, localized in render.
  const tierColumn = useMemo<StepTierColumnView | null>(() => hostTierConfig === null ? null : {
    capable: hostTierConfig.capable && !hostTierConfig.stepTiersReason,
    reason: hostTierConfig.capable && !hostTierConfig.stepTiersReason ? null : stepTierReasonText(hostTierConfig, t),
    tiers: stepTierItems(hostTierConfig.items),
  }, [hostTierConfig, t]);

  const shownDefault = draft.defaultModel ?? defaultModel;
  const selected = shownDefault ? JSON.stringify(shownDefault) : "";
  const defined = {
    pi: rows.filter((row) => row.harnesses.includes("pi")).map((row) => `${row.provider}/${row.id}`),
    claude: rows.filter((row) => row.harnesses.includes("claude")).map((row) => row.id),
  };
  const invalidTiers = tierColumn?.capable ? stepTierInvalidReferences(tierColumn.tiers, defined) : [];
  const view = readView({ loading, error: Boolean(error || saveError), count: models.length });
  // A kept draft (or the last good default) is data: a reload keeps the select
  // on screen instead of replacing it with a skeleton (Q5 A).
  const defaultView = readView({ loading: loading && !shownDefault, error: Boolean(error || saveError), count: models.length + (shownDefault ? 1 : 0) });
  return (
    <div ref={modelRootRef} className="settings-section settings-models" data-model-page="unified">
      {(error || saveError || enabled.failure) && (
        error ? (
          <ModelPageError
            summary={saveError ?? error}
            onRetry={() => setRevision((previous) => previous + 1)}
            t={t}
          />
        ) : (
          <ConfigErrorNotice summary={saveError ?? enabled.failure?.message ?? (enabled.failure?.messageKey ? t(enabled.failure.messageKey) : "")} />
        )
      )}
      <ConfigSplitView>
        <ModelPageNav page={page} onSelect={changePage} connections={connections} selectedConnection={selectedConnection} onConnection={openConnection} onOpenTasks={onOpenPolyforge} />
        <ConfigDetail>
          {page === "connections" && !selectedConnection && <ConnectionOverview
            connections={connections.map((connection) => ({ ...connection, modelCount: connection.modelCount ?? enabled.view?.providers.find((provider) => provider.id === connection.id)?.models.length ?? null }))}
            catalog={connectionCatalog} account={account} claudeModelCount={claudeModelCount}
            onAdd={() => setConnectionPickerOpen(true)} onSelect={openConnection} onClaude={openClaude} onModels={() => changePage("list")} />}
          {accountsVisited && <div hidden={taskAuthOpen || (page !== "account-claude" && (page !== "connections" || !selectedConnection))} className="settings-connection-detail" data-connection-detail="">
            <AccountSettings onRevealConnection={openConnection} accountState={account} onManageModels={page === "account-claude" ? () => changePage("claude") : selectedConnection ? () => { setPage("providers"); openDetail(); } : undefined} onTaskAuthentication={openTaskAuth} connectionLabel={selectedConnection?.label} onOpenConnections={onOpenAccount} cwd={cwd} onRevealCredentials={openClaude} selectedHarness={page === "account-claude" ? "claude" : "pi"} selectedConnection={selectedConnection && selectedConnection.kind !== "provider" ? { kind: selectedConnection.kind, id: selectedConnection.id } : null} onOpenModels={openCustom} />
          </div>}
          {taskAuthVisited && <div hidden={!taskAuthOpen || page !== "account-claude"} className="settings-connection-detail" data-task-auth-flow=""><AccountSettings purpose="tasks" selectedHarness="claude" cwd={cwd} onOpenConnections={openClaude} onRevealCredentials={openTaskAuth} /></div>}
          {(page === "tiers" || visited.has("tiers")) && <div hidden={page !== "tiers"}><NativeModelTiers defined={defined} onReveal={() => setPage("tiers")} onSaved={() => setRevision((previous) => previous + 1)} /></div>}
          {page === "default" && defaultView.kind === "loading" && (
            <section className="settings-model-section" data-model-page-pane="default" aria-label={t("models.defaultHeading")}>
              <div data-model-skeleton=""><ConfigSkeleton label={t("i18n.loading")} lines={3} /></div>
              {!managed && <ModelSaveBar count={0} saving={saving} onSave={() => void saveDraft()} />}
            </section>
          )}
          {page === "default" && defaultView.kind === "empty" && (
            <section className="settings-model-section" data-model-page-pane="default" aria-label={t("models.defaultHeading")}>
              <ModelEmptyState onOpenAccount={onOpenAccount} onOpenProviders={() => setConnectionPickerOpen(true)} />
            </section>
          )}
          {page === "default" && defaultView.kind === "ready" && (
            <section className="settings-model-section" data-model-page-pane="default" aria-label={t("models.defaultHeading")}>
              <header className="settings-content-heading"><div><h2>{t("models.defaultHeading")}</h2><p>{t("models.defaultBoundary")}</p></div></header>
              <div className="settings-data-panel settings-default-card"><div className="settings-default-choice"><div><h3>{t("models.defaultScopeTitle")}</h3><p>{t("models.defaultUseHint")}</p></div>
              <select className="settings-model-default-select" aria-label={t("models.defaultHeading")} disabled={saving} value={selected}
                onChange={(event) => {
                  const choice = JSON.parse(event.target.value) as ModelIdentity;
                  setDraft((previous) => setDraftDefaultModel(previous, JSON.stringify(choice) === JSON.stringify(defaultModel) ? null : choice));
                }}>
                <option value="" disabled>{t("models.notProvided")}</option>
                {shownDefault && !models.some((model) => model.harness === shownDefault.harness && model.provider === shownDefault.provider && model.id === shownDefault.id)
                  && <option value={selected}>{shownDefault.harness} · {shownDefault.provider}/{shownDefault.id}</option>}
                {models.map((model) => <option key={`${model.harness}/${model.provider}/${model.id}`}
                  value={JSON.stringify({ harness: model.harness, provider: model.provider, id: model.id })}>
                  {model.name} · {model.harness} / {model.provider}
                </option>)}
              </select></div>
              <dl className="settings-model-facts"><div><dt>Harness</dt><dd>{shownDefault?.harness ?? "—"}</dd></div><div><dt>{t("models.providerLabel")}</dt><dd>{shownDefault?.provider ?? "—"}</dd></div><div><dt>{t("models.modelIdentity")}</dt><dd>{shownDefault?.id ?? t("models.notProvided")}</dd></div></dl>
              </div>
              <details className="settings-model-advanced"><summary>{t("polish.automaticTasks")}</summary><p>{t("models.defaultAutomationHint")}</p><ConfigButton onClick={onOpenPolyforge}>{t("models.openActualChains")}</ConfigButton></details>
              {!managed && <ModelSaveBar count={dirty} saving={saving} onSave={() => void saveDraft()} />}
            </section>
          )}
          {page === "list" && view.kind === "loading" && (
            <section className="settings-model-section" data-model-page-pane="list" aria-label={t("models.listHeading")}>
              <div data-model-skeleton=""><ConfigSkeleton label={t("i18n.loading")} lines={4} /></div>
            </section>
          )}
          {page === "list" && view.kind === "empty" && (
            <section className="settings-model-section" data-model-page-pane="list" aria-label={t("models.listHeading")}>
              <ModelEmptyState onOpenAccount={onOpenAccount} onOpenProviders={() => setConnectionPickerOpen(true)} />
            </section>
          )}
          {page === "list" && view.kind === "ready" && (
            <section className="settings-model-section" data-model-page-pane="list" aria-label={t("models.listHeading")}>
              <header className="settings-content-heading"><div><h2>{t("models.listHeading")}</h2><p>{t("models.listIntro")}</p></div><span className="settings-count-badge">{rows.length}</span></header>
              <EnabledModelsBanner controller={enabled} />
              {tierColumn && !tierColumn.capable && tierColumn.reason && (
                <p role="note" data-step-tier-reason="" style={{ color: "var(--text-dim)", margin: "0 0 4px" }}>{tierColumn.reason}</p>
              )}
              {invalidTiers.length > 0 && (
                <p role="note" data-step-tier-invalid="" style={{ color: "var(--text-dim)", margin: "0 0 4px" }}>
                  {t("models.stepTierInvalidNote", { candidates: invalidTiers.join("、") })}
                </p>
              )}
              <div className="settings-model-toolbar"><input className="settings-model-search" aria-label={t("models.searchModels")} placeholder={t("models.searchModels")} value={search} onChange={(event) => setSearch(event.target.value)} />
              <ModelEnabledBulk enabled={guardedEnabled} /></div>
              <div className="settings-data-panel settings-model-inventory"><HarnessModelTable rows={rows.filter((row) => `${row.name} ${row.provider}/${row.id}`.toLowerCase().includes(search.toLowerCase()))} enabled={guardedEnabled} levels={levels} piSettings={piSettings}
                saving={saving}
                effortValues={draft.efforts}
                tier={tierColumn ?? undefined}
                onOpenTier={(row) => { setDrawerTier(tierColumn); setDrawerRow(row); }}
                onEffort={(provider, id, level) => setDraft((previous) => {
                  const key = `${provider}/${id}`;
                  const next = setDraftEffort(previous, key, level);
                  if (level === (piSettings?.perModelReasoningEffort[key]?.raw ?? "")) delete next.efforts[key];
                  return next;
                })} /></div>
              {!managed && <ModelSaveBar count={dirty} saving={saving} onSave={() => void saveDraft()} />}
            </section>
          )}
          {(
            <section hidden={page !== "providers"} className="settings-model-section is-provider-page" data-model-page-pane="providers" aria-label={t("models.providersHeading")}>
              <ModelsConfig embedded cwd={cwd} onOpenConnection={openConnection} onBack={selectedConnection && selectedConnection.kind !== "provider" ? () => openConnection(selectedConnection) : onOpenAccount} onReveal={() => setPage("providers")} addProviderRequest={addProviderRequest + customRequest} selectedManaged={selectedConnection && selectedConnection.kind !== "provider" ? { kind: selectedConnection.kind, id: selectedConnection.id, label: selectedConnection.label } : null} selectedProvider={selectedConnection?.kind === "provider" ? selectedConnection.id : null} onConnections={setConnections} onCatalogChange={setConnectionCatalog} onClose={onClose ?? (() => {})} onOpenAccount={onOpenAccount} enabledModels={enabled} />
            </section>
          )}
          {(page === "claude" || visited.has("claude")) && (
            <section hidden={page !== "claude"} className="settings-model-section settings-connection-detail" data-model-page-pane="claude" aria-label={t("models.claudeCustomHeading")}>
              <ConfigButton variant="ghost" data-claude-models-back="" onClick={openClaude}>← Claude Code</ConfigButton>
              <ClaudeCustomModels cwd={cwd} onReveal={() => setPage("claude")} onRefresh={() => setRevision((previous) => previous + 1)} />
            </section>
          )}
        </ConfigDetail>
      </ConfigSplitView>
      {connectionPickerOpen && <AddProviderPicker
        oauthProviders={connectionCatalog.oauthProviders} apiKeyProviders={connectionCatalog.apiKeyProviders}
        onSelectOAuth={(id) => openConnection({ id, kind: "oauth", label: connectionCatalog.oauthProviders.find((entry) => entry.id === id)?.name ?? id })}
        onSelectApiKey={(id) => openConnection({ id, kind: "apikey", label: connectionCatalog.apiKeyProviders.find((entry) => entry.id === id)?.displayName ?? id })}
        onOpenModels={openCustom} onClose={() => setConnectionPickerOpen(false)} />}
      {drawerRow && drawerTier?.capable && (
        <ModelStepTierDrawer
          row={drawerRow}
          tier={drawerTier}
          tierRead={tierRead}
          tierWrite={tierWrite}
          onClose={() => { setDrawerRow(null); setDrawerTier(null); }}
          onSaved={() => setRevision((previous) => previous + 1)}
        />
      )}
    </div>
  );
}
