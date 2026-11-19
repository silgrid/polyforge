"use client";

// General's 界面与对话 pane (polyforge#149, #85 85a1b): the former
// SettingsPanel GeneralSettings — appearance, fonts, chat, the session filter,
// the sidebar layout, the shell tool, push, the UI language, the Agent
// language, the claude tool permissions (kept in place per owner ruling Q7 A
// until 85a4) and the logout — moved whole into its own file so the left-column
// shell (components/HarnessGeneralSettings.tsx) can host it beside the
// environment and effective-values panes.

import { useEffect, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { useTheme } from "@/hooks/useTheme";
import { THEME_OPTIONS } from "@/lib/theme";
import {
  CHAT_CONTENT_WIDTH_DEFAULT,
  CHAT_CONTENT_WIDTH_MAX,
  CHAT_CONTENT_WIDTH_MIN,
  CHAT_CONTENT_FONT_SIZE_DEFAULT,
  CHAT_CONTENT_FONT_SIZE_MAX,
  CHAT_CONTENT_FONT_SIZE_MIN,
  useChatAppearance,
} from "@/hooks/useChatAppearance";
import { useEnterSendMode, setEnterSendMode } from "@/hooks/useEnterSendMode";
import { sendAgentCommand } from "@/lib/agent-client";
import type { AgentLanguageResponse, ToolSettingsResponse } from "@/lib/api-types";
import { AGENT_LANGUAGE_DEFAULT, isAgentLanguage, type AgentLanguage } from "@/lib/agent-language";
import { AgentLanguagePfStatus, requestAgentLanguageSync } from "@/components/AgentLanguagePfStatus";
import { setupPushSubscription } from "@/lib/push-client";
import {
  setFilesPlacement,
  useFilesPlacement,
} from "@/lib/files-placement";
import {
  DEFAULT_SESSION_FILTER_PATTERNS,
  loadSessionFilterPatterns,
  loadShowFilteredSessions,
  setSessionFilterPatterns,
  setShowFilteredSessions,
  sessionFilterStorage,
  getSessionFilterState,
  DEFAULT_SESSION_AGE_FILTER_DAYS,
  setSessionAgeFilterDays,
} from "@/lib/session-filter";
import {
  isThinkingExpandedByDefault,
  setThinkingExpandedByDefault,
} from "@/lib/thinking-expansion-preference";
import { ConfigButton, ConfigSwitch } from "./SettingsUi";
import { FontSettings } from "./FontSettings";
import { ClaudePermissionSettings } from "./ClaudePermissionSettings";

/** The props the pane takes from the settings panel. */
export interface GeneralSettingsProps {
  sessionId: string | null;
  onSessionReloaded: () => void;
  quoteSelectionEnabled: boolean;
  onQuoteSelectionChange: (enabled: boolean) => void;
}

/**
 * Settings › General's Agent language: the server-side language new pi and
 * claude sessions capture, shared by every device. Independent of the UI language above
 * (browser-local `pi-locale`): it has its own state and its own API. Shown once the GET
 * settles; a value is shown as saved only after the PUT succeeds. Below it, pf's own language
 * (`polyforge` of the GET), a mismatch hint and the one-click sync.
 */
function AgentLanguageSettings() {
  const { t } = useI18n();
  const [language, setLanguage] = useState<AgentLanguage | null>(null);
  const [polyforge, setPolyforge] = useState<AgentLanguageResponse["polyforge"]>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveFailed, setSaveFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void fetch("/api/agent-language")
      .then(async (response) => {
        const data = await response.json() as Partial<AgentLanguageResponse>;
        if (!response.ok || !isAgentLanguage(data.language)) throw new Error(`HTTP ${response.status}`);
        if (cancelled) return;
        setLanguage(data.language);
        setPolyforge(data.polyforge ?? null);
      })
      .catch(() => {
        if (cancelled) return;
        setLoadFailed(true);
        setLanguage(AGENT_LANGUAGE_DEFAULT);
      });
    return () => { cancelled = true; };
  }, []);

  const save = async (next: AgentLanguage) => {
    if (next === language) return;
    setSaving(true);
    setSaveFailed(false);
    try {
      const response = await fetch("/api/agent-language", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ language: next }),
      });
      const data = await response.json() as Partial<AgentLanguageResponse>;
      if (!response.ok || !isAgentLanguage(data.language)) throw new Error(`HTTP ${response.status}`);
      setLanguage(data.language);
      setLoadFailed(false);
      await refreshPolyforge();
    } catch {
      setSaveFailed(true);
    } finally {
      setSaving(false);
    }
  };

  /** pf's language after a save; a failed read keeps the line as it was. */
  const refreshPolyforge = async () => {
    try {
      const response = await fetch("/api/agent-language");
      const data = await response.json() as Partial<AgentLanguageResponse>;
      if (response.ok && isAgentLanguage(data.language)) setPolyforge(data.polyforge ?? null);
    } catch {
      // the save itself succeeded; the pf line keeps its last known state
    }
  };

  /** The sync writes Pi Web's current value again (PUT, then GET), past save()'s unchanged-value return. */
  const sync = async () => {
    if (language === null) return;
    setSaving(true);
    setSaveFailed(false);
    try {
      const data = await requestAgentLanguageSync(language);
      setLanguage(data.language);
      setPolyforge(data.polyforge ?? null);
      setLoadFailed(false);
    } catch {
      setSaveFailed(true);
    } finally {
      setSaving(false);
    }
  };

  if (language === null) return null;
  const options: { id: AgentLanguage; label: string }[] = [
    { id: "en", label: t("settings.agentLanguageEnglish") },
    { id: "zh-CN", label: t("settings.agentLanguageChinese") },
  ];
  return (
    <section className="settings-general-section">
      <h3 className="settings-general-heading">{t("settings.agentLanguage")}</h3>
      <p className="settings-general-description">{t("settings.agentLanguageDescription")}</p>
      <div role="radiogroup" aria-label={t("settings.agentLanguage")} className="settings-agent-language-options">
        {options.map((option) => {
          const selected = language === option.id;
          return (
            <button
              key={option.id}
              type="button"
              role="radio"
              aria-checked={selected}
              disabled={saving}
              onClick={() => void save(option.id)}
              className="settings-language-option"
            >
              <span className="settings-language-radio">
                {selected && <span className="settings-language-radio-dot" />}
              </span>
              <span className="settings-language-label">{option.label}</span>
              <span className="settings-language-code">{option.id}</span>
            </button>
          );
        })}
      </div>
      <AgentLanguagePfStatus
        language={language}
        polyforge={polyforge}
        languageLabels={{ en: t("settings.agentLanguageEnglish"), "zh-CN": t("settings.agentLanguageChinese") }}
        statusText={(languageLabel) => t("settings.agentLanguagePolyforge", { language: languageLabel })}
        mismatchText={t("settings.agentLanguagePolyforgeMismatch")}
        unsetMismatchText={t("settings.agentLanguagePolyforgeUnset")}
        syncLabel={t("settings.agentLanguagePolyforgeSync")}
        syncing={saving}
        onSync={() => void sync()}
      />
      {loadFailed && <p role="alert" className="settings-general-error">{t("settings.agentLanguageLoadFailed")}</p>}
      {saveFailed && <p role="alert" className="settings-general-error">{t("settings.agentLanguageSaveError")}</p>}
    </section>
  );
}

export function GeneralSettings({ sessionId, onSessionReloaded, quoteSelectionEnabled, onQuoteSelectionChange }: GeneralSettingsProps) {
  const { locale, setLocale, supportedLocales, t } = useI18n();
  const { preference, setThemePreference } = useTheme();
  const { width: chatContentWidth, setWidth: setChatContentWidth, fontSize, setFontSize } = useChatAppearance();
  const enterSendMode = useEnterSendMode();
  const filesPlacement = useFilesPlacement();
  const [shellSettings, setShellSettings] = useState<ToolSettingsResponse | null>(null);
  const [shellSaving, setShellSaving] = useState(false);
  const [shellError, setShellError] = useState<string | null>(null);
  const [thinkingExpanded, setThinkingExpanded] = useState(false);
  const [cacheMissNotices, setCacheMissNotices] = useState<boolean | null>(null);
  const [cacheMissNoticesSaving, setCacheMissNoticesSaving] = useState(false);
  const [cacheMissNoticesError, setCacheMissNoticesError] = useState<string | null>(null);
  const [pushRegistering, setPushRegistering] = useState(false);
  const [pushStatus, setPushStatus] = useState<{ kind: "ok" | "error"; message: string } | null>(null);
  const [webAuthEnabled, setWebAuthEnabled] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const [logoutError, setLogoutError] = useState("");
  // Session filter (wi pi#49 R1): the editor is line-per-rule. The initial
  // state is the default list (SSR-safe, no storage read) and the real
  // persisted value hydrates after mount — the same pattern as the other
  // client-persisted toggles. Saving happens on every change; the FIRST
  // render never writes the key, so a fresh install keeps the default as a
  // display value only.
  const [sessionFilterRulesText, setSessionFilterRulesText] = useState(
    () => DEFAULT_SESSION_FILTER_PATTERNS.join("\n"),
  );
  const [showFilteredSessions, setShowFilteredSessionsState] = useState(false);
  // Session age window (pi#82): hydrated after mount like the other
  // client-persisted values; every change persists through the shared store
  // so the sidebar (and this field) update without a reload.
  const [ageFilterDays, setAgeFilterDaysState] = useState(DEFAULT_SESSION_AGE_FILTER_DAYS);

  useEffect(() => {
    setSessionFilterRulesText(loadSessionFilterPatterns(sessionFilterStorage()).join("\n"));
    setShowFilteredSessionsState(loadShowFilteredSessions(sessionFilterStorage()));
    setAgeFilterDaysState(getSessionFilterState().ageFilterDays);
  }, []);

  useEffect(() => {
    setThinkingExpanded(isThinkingExpandedByDefault());
    void fetch("/api/web-auth")
      .then((response) => response.ok ? response.json() : null)
      .then((data: { enabled?: boolean } | null) => setWebAuthEnabled(data?.enabled === true))
      .catch(() => {});
  }, []);

  const logOut = async () => {
    setLoggingOut(true);
    setLogoutError("");
    try {
      const response = await fetch("/api/web-auth", { method: "DELETE" });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      window.location.replace("/login");
    } catch {
      setLogoutError(t("auth.logoutFailed"));
    } finally {
      setLoggingOut(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    void fetch("/api/tools/settings")
      .then(async (response) => {
        const data = await response.json() as ToolSettingsResponse & { error?: string };
        if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
        if (!cancelled) setShellSettings(data);
      })
      .catch((cause) => {
        if (!cancelled) setShellError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    let cancelled = false;
    void fetch("/api/cache-miss-notices")
      .then(async (response) => {
        const data = await response.json() as { enabled?: boolean; error?: string };
        if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
        if (!cancelled) setCacheMissNotices(data.enabled === true);
      })
      .catch((cause) => {
        if (!cancelled) setCacheMissNoticesError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => { cancelled = true; };
  }, []);

  // pi's showCacheMissNotices (#1265); the open chat is read again to show or drop them.
  const toggleCacheMissNotices = async (enabled: boolean) => {
    setCacheMissNoticesSaving(true);
    setCacheMissNoticesError(null);
    try {
      const response = await fetch("/api/cache-miss-notices", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      });
      const data = await response.json() as { enabled?: boolean; error?: string };
      if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
      setCacheMissNotices(data.enabled === true);
      if (sessionId) onSessionReloaded();
    } catch (cause) {
      setCacheMissNoticesError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setCacheMissNoticesSaving(false);
    }
  };

  const togglePowerShell = async (enabled: boolean) => {
    setShellSaving(true);
    setShellError(null);
    try {
      const response = await fetch("/api/tools/settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      });
      const data = await response.json() as ToolSettingsResponse & { error?: string };
      if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
      setShellSettings(data);
      if (sessionId) {
        await sendAgentCommand(sessionId, { type: "reload" });
        onSessionReloaded();
      }
    } catch (cause) {
      setShellError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setShellSaving(false);
    }
  };

  // Session filter persistence: every edit rewrites the persisted array
  // (splitting on newlines — one rule per line); the reveal toggle persists
  // under its own key. Both are best-effort against unavailable storage.
  const handleSessionFilterRulesChange = (value: string) => {
    setSessionFilterRulesText(value);
    // Live store: the sidebar subscribes and re-filters immediately.
    setSessionFilterPatterns(value.split("\n"));
  };

  const handleShowFilteredSessionsChange = (enabled: boolean) => {
    setShowFilteredSessions(enabled);
    setShowFilteredSessionsState(enabled);
  };

  const registerPush = async () => {
    if (pushRegistering) return;
    setPushRegistering(true);
    setPushStatus(null);
    try {
      if (typeof window === "undefined" || !("Notification" in window)) {
        throw new Error("unsupported or not permitted");
      }
      const permission = Notification.permission === "default"
        ? await Notification.requestPermission()
        : Notification.permission;
      if (permission !== "granted") throw new Error("unsupported or not permitted");
      const ok = await setupPushSubscription(locale);
      if (!ok) throw new Error("unsupported or not permitted");
      setPushStatus({ kind: "ok", message: t("settings.pushRegistered") });
    } catch (cause) {
      setPushStatus({ kind: "error", message: `${t("settings.pushRegisterFailed")} ${cause instanceof Error ? cause.message : String(cause)}` });
    } finally {
      setPushRegistering(false);
    }
  };

  return (
    <div className="settings-general is-pane">
      <section className="settings-general-section">
        <h3 className="settings-general-heading">{t("settings.appearance")}</h3>
        <div role="radiogroup" aria-label={t("settings.appearance")} className="settings-theme-options">
          {THEME_OPTIONS.map((option) => {
            const selected = preference === option.id;
            return (
              <label
                key={option.id}
                className="settings-theme-option"
                data-system={option.id === "auto" || undefined}
                data-wide={option.id === "contrast-light" || option.id === "contrast-dark" || option.id === "zenburn" || undefined}
              >
                <input
                  type="radio"
                  name="theme"
                  value={option.id}
                  checked={selected}
                  onChange={() => setThemePreference(option.id)}
                  className="sr-only"
                />
                {/* A data-theme span paints with that theme's own variables. System is a switch, not a
                    palette: a neutral tile with a light and a dark chip beside its name. */}
                {option.id === "auto" ? (
                  <span className="settings-theme-swatch settings-theme-system" aria-hidden="true">
                    <span data-theme="light" className="settings-theme-chip" />
                    <span data-theme="dark" className="settings-theme-chip" />
                    <span className="settings-theme-system-name">{t(option.label)}</span>
                  </span>
                ) : (
                  <span className="settings-theme-swatch" aria-hidden="true">
                    <span data-theme={option.id} className="settings-theme-fill">
                      <span className="settings-theme-name">{t(option.label)}</span>
                    </span>
                  </span>
                )}
                <span className="sr-only">{t(option.label)}</span>
              </label>
            );
          })}
        </div>
      </section>

      <section className="settings-general-section">
        <h3 className="settings-general-heading">{t("settings.typography")}</h3>
        <p id="settings-typography-description" className="settings-general-description">{t("settings.fontDescription")}</p>
        <div className="settings-chat-options">
          <FontSettings />
          <div className="settings-chat-option settings-chat-range-option">
            <div className="settings-chat-range-header">
              <label htmlFor="settings-chat-content-font-size">{t("settings.chatContentFontSize")}</label>
              <output htmlFor="settings-chat-content-font-size">{fontSize}px</output>
              <ConfigButton
                variant="ghost"
                size="small"
                className="settings-chat-reset"
                title={t("settings.resetChatContentFontSize")}
                aria-label={t("settings.resetChatContentFontSize")}
                disabled={fontSize === CHAT_CONTENT_FONT_SIZE_DEFAULT}
                onClick={() => setFontSize(CHAT_CONTENT_FONT_SIZE_DEFAULT)}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8M3 3v5h5" />
                </svg>
              </ConfigButton>
            </div>
            <input
              id="settings-chat-content-font-size"
              type="range"
              min={CHAT_CONTENT_FONT_SIZE_MIN}
              max={CHAT_CONTENT_FONT_SIZE_MAX}
              step={1}
              value={fontSize}
              onChange={(event) => setFontSize(Number(event.target.value))}
            />
          </div>
          <div className="settings-chat-option settings-chat-range-option">
            <div className="settings-chat-range-header">
              <label htmlFor="settings-chat-content-width">{t("settings.chatContentWidth")}</label>
              <output htmlFor="settings-chat-content-width">{chatContentWidth}px</output>
              <ConfigButton
                variant="ghost"
                size="small"
                className="settings-chat-reset"
                title={t("settings.resetChatContentWidth")}
                aria-label={t("settings.resetChatContentWidth")}
                disabled={chatContentWidth === CHAT_CONTENT_WIDTH_DEFAULT}
                onClick={() => setChatContentWidth(CHAT_CONTENT_WIDTH_DEFAULT)}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8M3 3v5h5" />
                </svg>
              </ConfigButton>
            </div>
            <input
              id="settings-chat-content-width"
              type="range"
              min={CHAT_CONTENT_WIDTH_MIN}
              max={CHAT_CONTENT_WIDTH_MAX}
              step={10}
              value={chatContentWidth}
              onChange={(event) => setChatContentWidth(Number(event.target.value))}
            />
          </div>
        </div>
      </section>

      <section className="settings-general-section">
        <h3 className="settings-general-heading">{t("settings.chat")}</h3>
        <div className="settings-chat-options">
          <div className="settings-chat-option settings-chat-switch-option">
            <span>{t("settings.thinkingExpandedDefault")}</span>
            <ConfigSwitch
              checked={thinkingExpanded}
              label={t("settings.thinkingExpandedDefault")}
              onChange={(enabled) => {
                setThinkingExpandedByDefault(enabled);
                setThinkingExpanded(enabled);
              }}
            />
          </div>
          <div className="settings-chat-option settings-chat-switch-option">
            <span>{t("settings.quoteSelection")}</span>
            <ConfigSwitch
              checked={quoteSelectionEnabled}
              label={t("settings.quoteSelection")}
              onChange={onQuoteSelectionChange}
            />
          </div>
          <div className="settings-chat-option settings-chat-switch-option" role="radiogroup" aria-label={t("settings.enterSendMode")}>
            <span>{t("settings.enterSendMode")}</span>
            <div className="settings-send-mode-options">
              <label className="settings-send-mode-option">
                <input
                  type="radio"
                  name="enter-send-mode"
                  value="enter"
                  checked={enterSendMode === "enter"}
                  onChange={() => setEnterSendMode("enter")}
                  className="sr-only"
                />
                <span className="settings-send-mode-label">{t("settings.enterSendModeEnter")}</span>
              </label>
              <label className="settings-send-mode-option">
                <input
                  type="radio"
                  name="enter-send-mode"
                  value="ctrlEnter"
                  checked={enterSendMode === "ctrlEnter"}
                  onChange={() => setEnterSendMode("ctrlEnter")}
                  className="sr-only"
                />
                <span className="settings-send-mode-label">{t("settings.enterSendModeCtrlEnter")}</span>
              </label>
            </div>
          </div>
          <div className="settings-chat-option settings-chat-switch-option">
            <span>{t("settings.cacheMissNotices")}</span>
            <ConfigSwitch
              checked={cacheMissNotices === true}
              loading={(cacheMissNotices === null && !cacheMissNoticesError) || cacheMissNoticesSaving}
              label={t("settings.cacheMissNotices")}
              describedBy="settings-cache-miss-notices-description"
              onChange={(enabled) => void toggleCacheMissNotices(enabled)}
            />
          </div>
        </div>
        <p id="settings-cache-miss-notices-description" className="settings-general-description">{t("settings.cacheMissNoticesDescription")}</p>
        {cacheMissNoticesError && <p role="alert" className="settings-general-error">{cacheMissNoticesError}</p>}
      </section>

      <section className="settings-general-section">
        <h3 className="settings-general-heading">{t("settings.sessionFilter")}</h3>
        <p className="settings-general-description">{t("settings.sessionFilterDescription")}</p>
        <textarea
          className="settings-session-filter-rules"
          aria-label={t("settings.sessionFilterPatternsLabel")}
          title={t("settings.sessionFilterPatternsLabel")}
          spellCheck={false}
          rows={4}
          value={sessionFilterRulesText}
          onChange={(event) => handleSessionFilterRulesChange(event.target.value)}
        />
        <div className="settings-shell-option">
          <span>{t("settings.showFilteredSessions")}</span>
          <ConfigSwitch
            checked={showFilteredSessions}
            label={t("settings.showFilteredSessions")}
            onChange={handleShowFilteredSessionsChange}
          />
        </div>
        <div className="settings-shell-option">
          <span>{t("settings.sessionAgeDays")}</span>
          <input
            type="number"
            min={0}
            max={365}
            step={1}
            value={ageFilterDays}
            onChange={(event) => {
              const days = Math.round(Number(event.target.value));
              setAgeFilterDaysState(Number.isFinite(days) ? Math.max(0, Math.min(365, days)) : 0);
              setSessionAgeFilterDays(days);
            }}
            aria-label={t("settings.sessionAgeDays")}
            title={t("settings.sessionAgeDaysDescription")}
            className="settings-age-filter-input"
            data-testid="session-age-filter-input"
          />
        </div>
        <p className="settings-general-description">{t("settings.sessionAgeDaysDescription")}</p>
      </section>

      <section className="settings-general-section">
        <h3 className="settings-general-heading">{t("settings.sidebarLayout")}</h3>
        <p className="settings-general-description">{t("settings.sidebarLayoutDescription")}</p>
        {/* Files placement (wi B3): a two-option radio row in General (the
            WI's stated location — no sidebar shortcut). The live store
            setter persists AND broadcasts, so the sidebar swaps its layout
            immediately; the hook value re-renders this row in sync. Stacked
            is the default and today's behavior; this commit only stores and
            reads the value (the sidebar layout change lands separately). */}
        <div className="settings-chat-option settings-chat-switch-option" role="radiogroup" aria-label={t("settings.sidebarLayout")}>
          <span>{t("settings.sidebarLayout")}</span>
          <div className="settings-send-mode-options">
            <label className="settings-send-mode-option">
              <input
                type="radio"
                name="files-placement"
                value="stacked"
                checked={filesPlacement === "stacked"}
                onChange={() => setFilesPlacement("stacked")}
                className="sr-only"
              />
              <span className="settings-send-mode-label">{t("settings.filesPlacementStacked")}</span>
            </label>
            <label className="settings-send-mode-option">
              <input
                type="radio"
                name="files-placement"
                value="tabs"
                checked={filesPlacement === "tabs"}
                onChange={() => setFilesPlacement("tabs")}
                className="sr-only"
              />
              <span className="settings-send-mode-label">{t("settings.filesPlacementTabs")}</span>
            </label>
          </div>
        </div>
      </section>

      {shellSettings?.isWindows && (
        <section className="settings-general-section">
          <h3 className="settings-general-heading">{t("settings.shellTool")}</h3>
          <p className="settings-general-description">{t("settings.shellToolDescription")}</p>
          <div className="settings-shell-option">
            <span>{t("settings.usePowerShell")}</span>
            <ConfigSwitch
              checked={shellSettings.powerShellEnabled}
              loading={shellSaving}
              label={t("settings.usePowerShell")}
              onChange={(enabled) => void togglePowerShell(enabled)}
            />
          </div>
          {shellError && <p role="alert" className="settings-general-error">{shellError}</p>}
        </section>
      )}

      <section className="settings-general-section">
        <h3 className="settings-general-heading">{t("settings.pushPermission")}</h3>
        <p className="settings-general-description">{t("settings.pushPermissionDescription")}</p>
        <div className="settings-shell-option">
          <span>{t("settings.pushPermission")}</span>
          <button
            type="button"
            className="config-button config-button-small config-button-secondary"
            disabled={pushRegistering}
            onClick={() => void registerPush()}
          >
            {pushRegistering ? t("settings.pushRegisterLoading") : t("settings.pushRegister")}
          </button>
        </div>
        {pushStatus && (
          <p
            role="status"
            className="settings-general-error"
            style={pushStatus.kind === "ok" ? { color: "var(--accent)" } : undefined}
          >
            {pushStatus.message}
          </p>
        )}
      </section>

      <section className="settings-general-section">
        <h3 className="settings-general-heading">{t("common.language")}</h3>
        <div role="radiogroup" aria-label={t("common.language")} className="settings-language-options">
          {supportedLocales.map((plugin) => {
            const selected = locale === plugin.id;
            return (
              <button
                key={plugin.id}
                type="button"
                role="radio"
                aria-checked={selected}
                onClick={() => setLocale(plugin.id as typeof locale)}
                className="settings-language-option"
              >
                <span className="settings-language-radio">
                  {selected && <span className="settings-language-radio-dot" />}
                </span>
                <span className="settings-language-label">{plugin.label}</span>
                <span className="settings-language-code">{plugin.id}</span>
              </button>
            );
          })}
        </div>
      </section>

      <AgentLanguageSettings />

      <ClaudePermissionSettings sessionId={sessionId} />

      {webAuthEnabled && (
        <section className="settings-general-section">
          <ConfigButton variant="secondary" disabled={loggingOut} onClick={() => void logOut()}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M10 17l5-5-5-5M15 12H3M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4" />
            </svg>
            {loggingOut ? t("auth.loggingOut") : t("auth.logOut")}
          </ConfigButton>
          {logoutError && <p role="alert" className="settings-general-error">{logoutError}</p>}
        </section>
      )}
    </div>
  );
}

