"use client";

// The permission page (polyforge#150 85a4, AC1/AC2/AC4): the left column's
// four items — 审批模式 / 沙箱 / 项目信任 / 规则 — each with the unified
// block above (one write, many reads: the compare's per-harness values, the
// 「统一成当前值」 button while harnesses differ, pi's own 不做审批 / 无沙箱
// note) and the harness-specific sections below (claude's extra approval
// values, denyInsteadOfPrompting, the Pi Web approval channel and push, the
// sandbox details and the rules editor; pi's trust section). One read per
// activation, never a poll; a save re-reads what it changed. A managed
// layer's value displays read-only (Q3 B).
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSettingsDraft } from "./SettingsDrafts";
import { useI18n } from "@/hooks/useI18n";
import { ClaudePermissionSettings } from "./ClaudePermissionSettings";
import { ClaudeCodeReport, type ClaudeReport } from "./ClaudeCodeReport";
import { UnifiedSetting } from "./UnifiedSetting";
import {
  ConfigButton,
  ConfigDetail,
  ConfigPanelShell,
  ConfigSidebar,
  ConfigSidebarItem,
  ConfigSidebarList,
  ConfigSplitView,
  ConfigSwitch,
} from "./SettingsUi";
import { CLAUDE_APPROVAL_MODES, type PermissionPayload } from "@/lib/harness/concepts";
import { DENY_INSTEAD_OF_PROMPTING, claudeApprovalModeName } from "@/lib/settings-display-names";
import {
  UNIFIED_APPROVAL_LABEL_KEYS,
  UNIFIED_APPROVAL_MODES,
  UNIFIED_TO_CLAUDE_MODE,
  compareFieldOf,
  rulesFromText,
  rulesToText,
  sourceIsReadOnly,
  trustUnifyTargets,
  unifiedApprovalOf,
  unifiedValues,
  type PermissionReads,
} from "@/lib/harness-permissions-view";

/** One compared field as the compare route answers it (the client's own shape: never the server module). */
export interface CompareFieldView {
  field: string;
  status: "consistent" | "inconsistent" | "unsupported";
  values: Record<string, { supported: boolean; neutral: string | null; raw: string | null }>;
}

/** The three rule lists' label keys (one fact, the tests read it). */
const RULES_LABEL_KEYS = {
  allow: "settings.permissionRulesAllow",
  ask: "settings.permissionRulesAsk",
  deny: "settings.permissionRulesDeny",
} as const;

/** The page's four panes, in the ticket's settled order. */
const PERMISSION_PANES = [
  { id: "approval", labelKey: "settings.permissionPaneApproval" },
  { id: "sandbox", labelKey: "settings.permissionPaneSandbox" },
  { id: "trust", labelKey: "settings.permissionPaneTrust" },
  { id: "rules", labelKey: "settings.permissionPaneRules" },
] as const;
type PermissionPaneId = (typeof PERMISSION_PANES)[number]["id"];

export interface HarnessPermissionSettingsProps {
  cwd: string | null;
  sessionId: string | null;
}

/** One PUT to a harness's permission write route; the answer is the written summary or the refusal's reason. */
async function putPermission(harness: "pi" | "claude", body: Record<string, unknown>, cwd: string | null): Promise<void> {
  const query = cwd ? `?cwd=${encodeURIComponent(cwd)}` : "";
  const response = await fetch(`/api/harness/${harness}/permission${query}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    const data = await response.json().catch(() => null) as { message?: unknown } | null;
    throw new Error(typeof data?.message === "string" && data.message ? data.message : `HTTP ${response.status}`);
  }
}

export function HarnessPermissionSettings({ cwd, sessionId }: HarnessPermissionSettingsProps) {
  const { t } = useI18n();
  const [pane, setPane] = useState<PermissionPaneId>("approval");
  const [compare, setCompare] = useState<CompareFieldView[] | null>(null);
  const [reads, setReads] = useState<PermissionReads>({ pi: null, claude: null });
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [savedNote, setSavedNote] = useState<string | null>(null);
  const rulesDirtyRef = useRef(false);
  const [rulesBase, setRulesBase] = useState<{ allow: string; ask: string; deny: string } | null>(null);
  const [rulesDraft, setRulesDraft] = useState<{ allow: string; ask: string; deny: string } | null>(null);
  // The retired Claude page's read-only report (owner ruling: taken over):
  // one fetch of GET /api/claude on the page's mount, shown on the approval pane.
  const [claudeReport, setClaudeReport] = useState<ClaudeReport | null>(null);
  const [claudeReportError, setClaudeReportError] = useState<string | null>(null);
  useEffect(() => {
    let cancelled = false;
    void fetch("/api/claude")
      .then(async (response) => {
        // ok first (r2#8): a non-JSON error body shows the status, not a parse error.
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const data = await response.json() as ClaudeReport;
        if (!cancelled) setClaudeReport(data);
      })
      .catch((cause) => {
        if (!cancelled) setClaudeReportError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => { cancelled = true; };
  }, []);

  const query = cwd ? `?cwd=${encodeURIComponent(cwd)}` : "";
  const readAll = useCallback(async () => {
    try {
      const [compareResponse, piResponse, claudeResponse] = await Promise.all([
        fetch(`/api/harness/compare${query}`),
        fetch(`/api/harness/pi/permission${query}`),
        fetch(`/api/harness/claude/permission${query}`),
      ]);
      if (!compareResponse.ok || !piResponse.ok || !claudeResponse.ok) throw new Error(`HTTP ${compareResponse.status || piResponse.status || claudeResponse.status}`);
      const compareBody = await compareResponse.json() as { results?: CompareFieldView[] };
      const piBody = await piResponse.json() as { data?: PermissionPayload };
      const claudeBody = await claudeResponse.json() as { data?: PermissionPayload };
      if (!Array.isArray(compareBody.results) || !piBody.data || !claudeBody.data) throw new Error("unexpected answer");
      setCompare(compareBody.results);
      setReads({ pi: piBody.data, claude: claudeBody.data });
      const nextRules = {
        allow: rulesToText(claudeBody.data.rules?.allow ?? []),
        ask: rulesToText(claudeBody.data.rules?.ask ?? []),
        deny: rulesToText(claudeBody.data.rules?.deny ?? []),
      };
      if (!rulesDirtyRef.current) { setRulesDraft(nextRules); setRulesBase(nextRules); }
      setLoadError(null);
    } catch (cause) {
      setLoadError(cause instanceof Error ? cause.message : String(cause));
    }
  }, [query]);

  useEffect(() => { void readAll(); }, [readAll]);

  const run = useCallback(async (save: () => Promise<void>) => {
    setSaving(true);
    setSaveError(null);
    setSavedNote(null);
    try {
      await save();
      await readAll();
      setSavedNote(t("settings.permissionSaved"));
    } catch (cause) {
      setSaveError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setSaving(false);
    }
  }, [readAll, t]);

  const saveClaude = useCallback((body: Record<string, unknown>) => run(async () => {
    await putPermission("claude", body, cwd);
    if (body.rules) rulesDirtyRef.current = false;
  }), [cwd, run]);
  const rulesDirty = rulesDraft && rulesBase ? (["allow", "ask", "deny"] as const).filter((key) => rulesDraft[key] !== rulesBase[key]).length : 0;
  useEffect(() => { rulesDirtyRef.current = rulesDirty > 0; }, [rulesDirty]);
  useSettingsDraft({ label: t("settings.permissionPaneRules"), reveal: () => setPane("rules"), count: rulesDirty, busy: saving,
    discard: () => setRulesDraft(rulesBase), save: () => {
      if (!rulesDraft || sourceIsReadOnly(reads.claude?.sources.rules ?? null)) return;
      void saveClaude({ rules: { allow: rulesFromText(rulesDraft.allow), ask: rulesFromText(rulesDraft.ask), deny: rulesFromText(rulesDraft.deny) } });
    } });

  const claudePayload = reads.claude;
  const piPayload = reads.pi;
  const approvalCompare = useMemo(() => compareFieldOf(compare ?? [], "approval-mode"), [compare]);
  const sandboxCompare = useMemo(() => compareFieldOf(compare ?? [], "sandbox"), [compare]);
  const trustCompare = useMemo(() => compareFieldOf(compare ?? [], "project-trust"), [compare]);
  const trustTargets = useMemo(() => trustUnifyTargets(reads), [reads]);

  const unifiedTexts = {
    currentValue: t("settings.permissionUnifiedCurrent"),
    unify: t("settings.permissionUnify"),
    consistent: t("settings.permissionUnifiedConsistent"),
    unsupported: t("settings.permissionUnifiedUnsupported"),
    saving: t("settings.permissionUnifiedSaving"),
  };

  const unifiedApproval = unifiedApprovalOf(claudePayload?.approvalMode.neutral ?? null);

  return (
    <ConfigPanelShell embedded title={t("settings.tabPermission")} onClose={() => {}}>
      <ConfigSplitView>
        <ConfigSidebar>
          <ConfigSidebarList>
            {PERMISSION_PANES.map((item) => (
              <ConfigSidebarItem key={item.id} active={pane === item.id} onClick={() => setPane(item.id)}>
                <span className="config-sidebar-text">{t(item.labelKey)}</span>
              </ConfigSidebarItem>
            ))}
          </ConfigSidebarList>
        </ConfigSidebar>
        <ConfigDetail>
          <header className="settings-content-heading"><div><h2>{t(PERMISSION_PANES.find((item) => item.id === pane)!.labelKey)}</h2><p>{t("settings.permissionIntro")}</p></div></header>
          <div data-permission-pane={pane} className="settings-general-pane-host">
            {loadError && <p role="alert" className="settings-general-error">{t("settings.permissionLoadError", { error: loadError })}</p>}
            {saveError && <p role="alert" className="settings-general-error">{t("settings.permissionSaveError", { error: saveError })}</p>}
            {savedNote && <p role="status" className="settings-general-description">{savedNote}</p>}

            {pane === "approval" && (
              <>
                <UnifiedSetting
                  texts={{ ...unifiedTexts, title: t("settings.permissionUnifiedApproval") }}
                  current={unifiedApproval === null ? (claudePayload?.approvalMode.raw ?? null) : UNIFIED_TO_CLAUDE_MODE[unifiedApproval]}
                  values={unifiedValues("approval-mode", approvalCompare, {
                    pi: t("settings.permissionPiNoApproval"),
                    claude: "",
                  })}
                />
                <section className="settings-general-section" data-permission-zone="unified-approval">
                  <h3 className="settings-general-heading">{t("settings.permissionChooseApproval")}</h3>
                  <div role="radiogroup" aria-label={t("settings.permissionChooseApproval")} className="settings-agent-language-options">
                    {UNIFIED_APPROVAL_MODES.map((mode) => (
                      <button
                        key={mode}
                        type="button"
                        role="radio"
                        aria-checked={unifiedApproval === mode}
                        disabled={saving || sourceIsReadOnly(claudePayload?.sources.defaultMode ?? null)}
                        onClick={() => void saveClaude({ approvalMode: UNIFIED_TO_CLAUDE_MODE[mode] })}
                        className="settings-language-option"
                      >
                        <span className="settings-language-radio">
                          {unifiedApproval === mode && <span className="settings-language-radio-dot" />}
                        </span>
                        <span className="settings-language-label">{t(UNIFIED_APPROVAL_LABEL_KEYS[mode])}</span>
                      </button>
                    ))}
                  </div>
                  {claudePayload?.sources.defaultMode && claudePayload.sources.defaultMode.scope !== "user" && (
                    <p role="note" className="settings-general-description">
                      {t("settings.permissionSourceNote", { scope: claudePayload.sources.defaultMode.scope, path: claudePayload.sources.defaultMode.path })}
                    </p>
                  )}
                </section>
                <details className="settings-general-section settings-advanced-section" data-permission-zone="claude">
                  <summary>{t("settings.claudeAdvanced")}</summary>
                  <p className="settings-general-description">{t("settings.permissionClaudeExtraModesDescription")}</p>
                  <div role="radiogroup" aria-label={t("settings.permissionClaudeExtraModes")} className="settings-agent-language-options">
                    {CLAUDE_APPROVAL_MODES.map((mode) => {
                      const modeName = claudeApprovalModeName(mode);
                      return (
                      <button
                        key={mode}
                        type="button"
                        role="radio"
                        aria-checked={claudePayload?.approvalMode.raw === mode}
                        disabled={saving || sourceIsReadOnly(claudePayload?.sources.defaultMode ?? null)}
                        onClick={() => void saveClaude({ approvalMode: mode })}
                        className="settings-language-option"
                      >
                        <span className="settings-language-radio">
                          {claudePayload?.approvalMode.raw === mode && <span className="settings-language-radio-dot" />}
                        </span>
                        {/* Display name first; the raw mode stays as the second line (AC2). */}
                        <span className="settings-language-label">
                          <span className="settings-name-stack">
                            <span>{modeName.labelKey ? t(modeName.labelKey) : modeName.id}</span>
                            <code className="settings-secondary-id">{modeName.id}</code>
                          </span>
                        </span>
                      </button>
                      );
                    })}
                  </div>
                  <div className="settings-shell-option">
                    <span className="settings-name-stack">
                      <span>{t(DENY_INSTEAD_OF_PROMPTING.labelKey!)}</span>
                      <code className="settings-secondary-id">{DENY_INSTEAD_OF_PROMPTING.id}</code>
                    </span>
                    <ConfigSwitch
                      checked={claudePayload?.denyInsteadOfPrompting === true}
                      loading={saving}
                      label={t(DENY_INSTEAD_OF_PROMPTING.labelKey!)}
                      disabled={sourceIsReadOnly(claudePayload?.sources.denyInsteadOfPrompting ?? null)}
                      onChange={(enabled) => void saveClaude({ denyInsteadOfPrompting: enabled })}
                    />
                  </div>
                  <p className="settings-general-description">{t("settings.permissionDenyInsteadDescription")}</p>
                </details>
                <section className="settings-general-section" data-permission-zone="pi-web-channel">
                  <h3 className="settings-general-heading">{t("settings.permissionPiWebChannel")}</h3>
                  <p className="settings-general-description">{t("settings.permissionPiWebChannelDescription")}</p>
                  <ClaudePermissionSettings sessionId={sessionId} />
                </section>
                <details className="settings-general-section settings-advanced-section"><summary>{t("settings.claudeRuntimeReport")}</summary><ClaudeCodeReport report={claudeReport} error={claudeReportError} /></details>
              </>
            )}

            {pane === "sandbox" && (
              <>
                <UnifiedSetting
                  texts={{ ...unifiedTexts, title: t("settings.permissionUnifiedSandbox") }}
                  current={claudePayload?.sandbox.enabled === null || claudePayload?.sandbox.enabled === undefined ? null : String(claudePayload.sandbox.enabled)}
                  values={unifiedValues("sandbox", sandboxCompare, {
                    pi: t("settings.permissionPiNoSandbox"),
                    claude: "",
                    unset: t("settings.permissionSandboxUnset"),
                  })}
                />
                <section className="settings-general-section" data-permission-zone="claude">
                  <h3 className="settings-general-heading">Claude</h3>
                  <div className="settings-shell-option">
                    <span>{t("settings.permissionSandboxEnabled")}</span>
                    <ConfigSwitch
                      checked={claudePayload?.sandbox.enabled === true}
                      loading={saving}
                      label={t("settings.permissionSandboxEnabled")}
                      disabled={sourceIsReadOnly(claudePayload?.sources.sandbox ?? null)}
                      onChange={(enabled) => void saveClaude({ sandboxEnabled: enabled })}
                    />
                  </div>
                  <div className="settings-shell-option">
                    <span>{t("settings.permissionSandboxAutoAllow")}</span>
                    <ConfigSwitch
                      checked={claudePayload?.sandbox.autoAllowBashIfSandboxed === true}
                      loading={saving}
                      label={t("settings.permissionSandboxAutoAllow")}
                      disabled={sourceIsReadOnly(claudePayload?.sources.sandbox ?? null)}
                      onChange={(enabled) => void saveClaude({ autoAllowBashIfSandboxed: enabled })}
                    />
                  </div>
                  {(claudePayload?.sandbox.otherKeys.length ?? 0) > 0 && (
                    <p className="settings-general-description">{t("settings.permissionSandboxOtherKeys", { keys: claudePayload?.sandbox.otherKeys.join(", ") ?? "" })}</p>
                  )}
                </section>
              </>
            )}

            {pane === "trust" && (
              <>
                <UnifiedSetting
                  texts={{ ...unifiedTexts, title: t("settings.permissionUnifiedTrust") }}
                  current={piPayload?.projectTrust?.trusted === undefined ? null : String(piPayload?.projectTrust?.trusted)}
                  values={unifiedValues("project-trust", trustCompare, {
                    pi: cwd ? "" : t("settings.permissionNoProject"),
                    claude: cwd ? "" : t("settings.permissionNoProject"),
                  })}
                  busy={saving}
                  onUnify={trustTargets === null ? undefined : () => void run(async () => {
                    for (const harness of trustTargets.harnesses) {
                      await putPermission(harness, { projectTrust: { trusted: trustTargets.trusted } }, cwd);
                    }
                  })}
                />
                {!cwd && <p className="settings-general-description">{t("settings.permissionNoProject")}</p>}
                <section className="settings-general-section" data-permission-zone="pi">
                  <h3 className="settings-general-heading">pi</h3>
                  <p className="settings-general-description">{t("settings.permissionTrustPiNote")}</p>
                </section>
                <section className="settings-general-section" data-permission-zone="claude">
                  <h3 className="settings-general-heading">Claude</h3>
                  <p className="settings-general-description">{t("settings.permissionTrustClaudeNote")}</p>
                </section>
              </>
            )}

            {pane === "rules" && (
              <section className="settings-general-section" data-permission-zone="claude">
                <h3 className="settings-general-heading">Claude</h3>
                <p className="settings-general-description">{t("settings.permissionRulesDescription")}</p>
                {(["allow", "ask", "deny"] as const).map((list) => (
                  <div key={list} className="settings-shell-option" style={{ alignItems: "flex-start" }}>
                    <label htmlFor={`permission-rules-${list}`} className="settings-general-heading">
                      {t(RULES_LABEL_KEYS[list])}
                    </label>
                    <textarea
                      id={`permission-rules-${list}`}
                      value={rulesDraft?.[list] ?? ""}
                      disabled={saving || sourceIsReadOnly(claudePayload?.sources.rules ?? null)}
                      onChange={(event) => setRulesDraft((draft) => draft === null ? draft : { ...draft, [list]: event.target.value })}
                      className="settings-age-filter-input"
                      style={{ width: "100%", minHeight: 64, textAlign: "left", fontFamily: "var(--font-mono, monospace)" }}
                      spellCheck={false}
                    />
                  </div>
                ))}
                <ConfigButton
                  size="small"
                  disabled={saving || rulesDraft === null}
                  onClick={() => {
                    if (rulesDraft === null) return;
                    void saveClaude({
                      rules: {
                        allow: rulesFromText(rulesDraft.allow),
                        ask: rulesFromText(rulesDraft.ask),
                        deny: rulesFromText(rulesDraft.deny),
                      },
                    });
                  }}
                >
                  {t("settings.permissionSave")}
                </ConfigButton>
              </section>
            )}
          </div>
        </ConfigDetail>
      </ConfigSplitView>
    </ConfigPanelShell>
  );
}
