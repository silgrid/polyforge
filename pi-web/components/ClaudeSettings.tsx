"use client";

import { useEffect, useState, type FormEvent } from "react";
import { useI18n } from "@/hooks/useI18n";
import type { ClaudeSettingsResponse } from "@/lib/api-types";
import {
  CLAUDE_FEATURE_MIN_VERSION,
  CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT,
  CLAUDE_LAUNCH_PERMISSION_MODES,
  isClaudeModelName,
  type ClaudeFeature,
  type ClaudeModelOption,
  type ClaudeLaunchPermissionMode,
} from "@/lib/claude-shared";
import { ConfigButton, ConfigDetailGrid, ConfigDetailGridRow, ConfigNotice, ConfigSwitch } from "./SettingsUi";

/** What Settings › Claude reads of GET /api/claude: the claude on PATH as it reports itself. */
interface ClaudeReport {
  available?: boolean;
  models?: ClaudeModelOption[];
  version?: string | null;
  versionRecorded?: boolean | null;
  recordedVersions?: string[];
  capabilities?: string[];
  features?: ClaudeFeature[];
  probeError?: string;
}

type Translate = (key: string, params?: Record<string, string | number>) => string;

function isClaudeSettingsResponse(value: unknown): value is ClaudeSettingsResponse {
  if (typeof value !== "object" || value === null) return false;
  const settings = value as Partial<ClaudeSettingsResponse>;
  return typeof settings.launchPermissionMode === "string"
    && Array.isArray(settings.customModels)
    && typeof settings.includeHookEvents === "boolean";
}

/** The answer's `error`, else its status. */
async function responseError(response: Response): Promise<string> {
  const body = await response.json().catch(() => null) as { error?: unknown } | null;
  return typeof body?.error === "string" && body.error ? body.error : `HTTP ${response.status}`;
}

function permissionModeLabel(mode: ClaudeLaunchPermissionMode, t: Translate): string {
  return mode === CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT ? t("claude.permissionModeDefault") : mode;
}

function permissionModeDescription(mode: ClaudeLaunchPermissionMode, planAvailable: boolean, t: Translate): string | null {
  if (mode === "acceptEdits") return t("claude.permissionModeAcceptEditsDesc");
  if (mode === "plan") return t(planAvailable ? "claude.permissionModePlanDesc" : "claude.permissionModePlanUnavailable");
  return null;
}

function featureName(feature: ClaudeFeature, t: Translate): string {
  if (feature.id === "approvals") return t("settings.claudeFeatureApprovals");
  if (feature.id === "backgroundTasks") return t("settings.claudeFeatureBackgroundTasks");
  return t("settings.claudeFeatureSubagentText");
}

function featureStatus(feature: ClaudeFeature, t: Translate): string {
  if (feature.available === null) return t("settings.claudeFeatureUnknown");
  return feature.available ? t("settings.claudeFeatureAvailable") : t("settings.claudeFeatureUnavailable");
}

/** Why a feature is judged so (GET /api/claude's `basis` code), as text. */
function featureBasis(feature: ClaudeFeature, t: Translate): string {
  const min = feature.id === "approvals" ? "" : CLAUDE_FEATURE_MIN_VERSION[feature.id];
  switch (feature.basis) {
    case "no-host-prompts": return t("settings.claudeBasisNoHostPrompts");
    case "host-prompts": return t("settings.claudeBasisHostPrompts");
    case "recorded": return t("settings.claudeBasisRecorded", { min });
    case "below-recorded": return t("settings.claudeBasisBelowRecorded", { min });
    case "env-disabled": return t("settings.claudeBasisEnvDisabled");
    default: return t("settings.claudeBasisVersionUnknown");
  }
}

const cellStyle = { padding: "6px 8px", textAlign: "left", verticalAlign: "top", borderTop: "1px solid var(--border)" } as const;

/**
 * Settings › Claude: what a new claude session starts with — the default
 * permission mode, the custom model names offered beside claude's aliases, hook activity —
 * and, read-only, what the claude on PATH reports. Every change is one PUT of the whole
 * settings; the page shows a value only once the PUT succeeded, a failure as a visible line.
 * Open sessions keep the values they were created with.
 */
export function ClaudeSettings() {
  const { t } = useI18n();
  const [settings, setSettings] = useState<ClaudeSettingsResponse | null>(null);
  // GET's planAvailable ( r3):'s global claude tool permissions are ask.
  const [planAvailable, setPlanAvailable] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [report, setReport] = useState<ClaudeReport | null>(null);
  const [reportError, setReportError] = useState<string | null>(null);
  const [modelInput, setModelInput] = useState("");
  const [modelError, setModelError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void fetch("/api/claude-settings")
      .then(async (response) => {
        if (!response.ok) throw new Error(await responseError(response));
        const data: unknown = await response.json();
        if (!isClaudeSettingsResponse(data)) throw new Error("unexpected answer");
        if (cancelled) return;
        setPlanAvailable((data as { planAvailable?: unknown }).planAvailable === true);
        setSettings(data);
      })
      .catch((cause) => {
        if (!cancelled) setLoadError(cause instanceof Error ? cause.message : String(cause));
      });
    void fetch("/api/claude")
      .then(async (response) => {
        if (!response.ok) throw new Error(await responseError(response));
        const data = await response.json() as ClaudeReport;
        if (!cancelled) setReport(data);
      })
      .catch((cause) => {
        if (!cancelled) setReportError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => { cancelled = true; };
  }, []);

  const save = async (next: ClaudeSettingsResponse): Promise<boolean> => {
    setSaving(true);
    setSaveError(null);
    try {
      const response = await fetch("/api/claude-settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          launchPermissionMode: next.launchPermissionMode,
          customModels: next.customModels,
          includeHookEvents: next.includeHookEvents,
        }),
      });
      if (!response.ok) throw new Error(await responseError(response));
      const data: unknown = await response.json();
      if (!isClaudeSettingsResponse(data)) throw new Error("unexpected answer");
      setSettings(data);
      setLoadError(null);
      return true;
    } catch (cause) {
      setSaveError(cause instanceof Error ? cause.message : String(cause));
      return false;
    } finally {
      setSaving(false);
    }
  };

  const aliases = (report?.models ?? []).filter((model) => model.source === "alias");

  const addModel = async (event: FormEvent) => {
    event.preventDefault();
    if (!settings || saving) return;
    const name = modelInput.trim();
    if (!name) return;
    if (!isClaudeModelName(name)) {
      setModelError(t("settings.claudeInvalidModel", { name: JSON.stringify(name) }));
      return;
    }
    if (settings.customModels.includes(name) || aliases.some((alias) => alias.value === name)) {
      setModelError(t("settings.claudeDuplicateModel", { name }));
      return;
    }
    setModelError(null);
    if (await save({ ...settings, customModels: [...settings.customModels, name] })) setModelInput("");
  };

  const removeModel = (name: string) => {
    if (!settings || saving) return;
    setModelError(null);
    void save({ ...settings, customModels: settings.customModels.filter((entry) => entry !== name) });
  };

  const setMode = (mode: ClaudeLaunchPermissionMode) => {
    if (!settings || saving || mode === settings.launchPermissionMode) return;
    void save({ ...settings, launchPermissionMode: mode });
  };

  return (
    <div className="settings-general settings-claude-page">
      <section className="settings-general-section">
        <h2 className="settings-general-heading" style={{ fontSize: 16 }}>{t("settings.claude")}</h2>
        <p className="settings-general-description">{t("settings.claudeIntro")}</p>
        {loadError && <p role="alert" className="settings-general-error">{t("settings.claudeLoadFailed", { error: loadError })}</p>}
        {saveError && <p role="alert" className="settings-general-error">{t("settings.claudeSaveError", { error: saveError })}</p>}
      </section>

      {settings && (
        <>
          <section className="settings-general-section">
            <h3 className="settings-general-heading">{t("settings.claudeLaunchPermissionMode")}</h3>
            <p className="settings-general-description">{t("settings.claudeLaunchPermissionModeDescription")}</p>
            <div role="radiogroup" aria-label={t("settings.claudeLaunchPermissionMode")} className="settings-agent-language-options">
              {CLAUDE_LAUNCH_PERMISSION_MODES.map((mode) => {
                const selected = settings.launchPermissionMode === mode;
                // plan needs's claude tool permissions on ask: else offered, disabled, its reason as text.
                const unavailable = mode === "plan" && !planAvailable;
                const description = permissionModeDescription(mode, planAvailable, t);
                return (
                  <button
                    key={mode}
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    disabled={saving || unavailable}
                    onClick={() => setMode(mode)}
                    className="settings-language-option"
                    style={{ height: "auto", minHeight: 44, padding: "6px 10px", textAlign: "left", opacity: unavailable ? 0.65 : undefined, cursor: unavailable ? "not-allowed" : undefined }}
                  >
                    <span className="settings-language-radio">
                      {selected && <span className="settings-language-radio-dot" />}
                    </span>
                    <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
                      <span className="settings-language-label">{permissionModeLabel(mode, t)}</span>
                      {description && <span className="settings-general-description" style={{ margin: 0 }}>{description}</span>}
                    </span>
                  </button>
                );
              })}
            </div>
          </section>

          <section className="settings-general-section">
            <h3 className="settings-general-heading">{t("settings.claudeModels")}</h3>
            <p className="settings-general-description">{t("settings.claudeModelsDescription")}</p>
            <ConfigDetailGrid>
              <ConfigDetailGridRow label={t("settings.claudeAliases")} tone="plain">
                {report === null && !reportError
                  ? <span className="settings-general-description" style={{ margin: 0 }}>{t("settings.claudeReading")}</span>
                  : (
                    <ul aria-label={t("settings.claudeAliases")} style={{ margin: 0, padding: 0, listStyle: "none", display: "flex", flexDirection: "column", gap: 2 }}>
                      {aliases.map((alias) => (
                        <li key={alias.value}>
                          <code>{alias.value}</code>
                          {alias.resolvedModel && alias.resolvedModel !== alias.value && (
                            <span className="settings-general-description" style={{ margin: 0 }}> → {alias.resolvedModel}</span>
                          )}
                        </li>
                      ))}
                    </ul>
                  )}
              </ConfigDetailGridRow>
              <ConfigDetailGridRow label={t("settings.claudeCustomModels")} tone="plain">
                {settings.customModels.length === 0
                  ? <span className="settings-general-description" style={{ margin: 0 }}>{t("settings.claudeCustomModelsEmpty")}</span>
                  : (
                    <ul aria-label={t("settings.claudeCustomModels")} style={{ margin: 0, padding: 0, listStyle: "none", display: "flex", flexDirection: "column", gap: 4 }}>
                      {settings.customModels.map((name) => (
                        <li key={name} style={{ display: "flex", alignItems: "center", gap: 8 }}>
                          <code style={{ overflowWrap: "anywhere" }}>{name}</code>
                          <ConfigButton
                            size="small"
                            variant="ghost"
                            disabled={saving}
                            aria-label={t("settings.claudeRemoveModel", { name })}
                            onClick={() => removeModel(name)}
                          >
                            {t("settings.claudeRemove")}
                          </ConfigButton>
                        </li>
                      ))}
                    </ul>
                  )}
              </ConfigDetailGridRow>
            </ConfigDetailGrid>
            <form onSubmit={(event) => void addModel(event)} className="settings-shell-option" style={{ gap: 8 }}>
              <input
                type="text"
                value={modelInput}
                onChange={(event) => { setModelInput(event.target.value); setModelError(null); }}
                aria-label={t("settings.claudeCustomModelName")}
                placeholder={t("settings.claudeCustomModelPlaceholder")}
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
                className="settings-age-filter-input"
                style={{ flex: 1, minWidth: 0, width: "auto", textAlign: "left" }}
              />
              <ConfigButton type="submit" size="small" disabled={saving || !modelInput.trim()}>{t("settings.claudeAddModel")}</ConfigButton>
            </form>
            {modelError && <p role="alert" className="settings-general-error">{modelError}</p>}
          </section>

          <section className="settings-general-section">
            <h3 className="settings-general-heading">{t("settings.claudeHookEvents")}</h3>
            <div className="settings-shell-option">
              <span>{t("settings.claudeHookEvents")}</span>
              <ConfigSwitch
                checked={settings.includeHookEvents}
                loading={saving}
                label={t("settings.claudeHookEvents")}
                describedBy="settings-claude-hook-events-description"
                onChange={(enabled) => void save({ ...settings, includeHookEvents: enabled })}
              />
            </div>
            <p id="settings-claude-hook-events-description" className="settings-general-description">{t("settings.claudeHookEventsDescription")}</p>
          </section>
        </>
      )}

      <ClaudeCodeReport report={report} error={reportError} t={t} />
    </div>
  );
}

/** The read-only block: claude's version and capabilities as it reports them, Pi Web's three features. */
function ClaudeCodeReport({ report, error, t }: { report: ClaudeReport | null; error: string | null; t: Translate }) {
  const version = report?.version ?? null;
  return (
    <section className="settings-general-section">
      <h3 className="settings-general-heading">{t("settings.claudeCode")}</h3>
      <p className="settings-general-description">{t("settings.claudeCodeDescription")}</p>
      {error && <p role="alert" className="settings-general-error">{t("settings.claudeReportFailed", { error })}</p>}
      {report === null && !error && <p className="settings-general-description">{t("settings.claudeReading")}</p>}
      {report && (
        <>
          {report.available === false && <p className="settings-general-error">{t("claude.notOnPath")}</p>}
          {report.probeError && <p className="settings-general-error">{t("settings.claudeProbeError", { error: report.probeError })}</p>}
          {version && report.versionRecorded === false && (
            <ConfigNotice>
              {t("settings.claudeVersionNotRecorded", { version, versions: (report.recordedVersions ?? []).join(", ") })}
            </ConfigNotice>
          )}
          <ConfigDetailGrid>
            <ConfigDetailGridRow label={t("settings.claudeVersion")} tone="plain" mono>
              {version ?? t("settings.claudeVersionUnknown")}
            </ConfigDetailGridRow>
            <ConfigDetailGridRow label={t("settings.claudeCapabilities")} tone="plain">
              {(report.capabilities ?? []).length === 0
                ? <span className="settings-general-description" style={{ margin: 0 }}>{t("settings.claudeCapabilitiesNone")}</span>
                : (
                  <ul aria-label={t("settings.claudeCapabilities")} style={{ margin: 0, padding: 0, listStyle: "none", display: "flex", flexWrap: "wrap", gap: "2px 10px" }}>
                    {(report.capabilities ?? []).map((capability) => <li key={capability}><code>{capability}</code></li>)}
                  </ul>
                )}
            </ConfigDetailGridRow>
          </ConfigDetailGrid>
          <table aria-label={t("settings.claudeFeatures")} style={{ width: "100%", borderCollapse: "collapse", fontSize: 13, marginTop: 8 }}>
            <thead>
              <tr>
                <th scope="col" style={{ ...cellStyle, borderTop: "none" }}>{t("settings.claudeFeature")}</th>
                <th scope="col" style={{ ...cellStyle, borderTop: "none" }}>{t("settings.claudeFeatureStatus")}</th>
                <th scope="col" style={{ ...cellStyle, borderTop: "none" }}>{t("settings.claudeFeatureBasis")}</th>
              </tr>
            </thead>
            <tbody>
              {(report.features ?? []).map((feature) => (
                <tr key={feature.id}>
                  <td style={cellStyle}>{featureName(feature, t)}</td>
                  <td style={{ ...cellStyle, whiteSpace: "nowrap", color: feature.available ? "var(--accent)" : "var(--text-muted)" }}>{featureStatus(feature, t)}</td>
                  <td style={{ ...cellStyle, color: "var(--text-muted)" }}>{featureBasis(feature, t)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}
