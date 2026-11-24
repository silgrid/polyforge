"use client";

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useI18n } from "@/hooks/useI18n";
import { useIsMobile } from "@/hooks/useIsMobile";
import {
  setLastSettingsSection,
  SETTINGS_TABS,
  settingsSectionRequiresProject,
  type SettingsSection,
} from "@/lib/settings-navigation";
import type { ProjectTrustStatus } from "@/lib/api-types";
import { focusModalPanel, listenForPanelEscape } from "@/lib/stacked-dialog";
import { ConfigDetailNavigationContext, type ConfigDetailNavigation } from "./SettingsUi";
import { HarnessGeneralSettings } from "./HarnessGeneralSettings";
import { HarnessPermissionSettings } from "./HarnessPermissionSettings";
import { HarnessModelSettings } from "./HarnessModelSettings";
import { SkillsConfig } from "./SkillsConfig";
import { AgentsConfig } from "./AgentsConfig";
import { PluginsConfig } from "./PluginsConfig";
import { PolyforgeConfig } from "./PolyforgeConfig";
import { McpConfig } from "./McpConfig";
import { AccountSettings } from "./AccountSettings";

interface Props {
  cwd: string | null;
  sessionId: string | null;
  initialSection: SettingsSection;
  /** A phone opens on the list of sections instead of `initialSection` (the plain Settings button). */
  openAtHome?: boolean;
  onClose: () => void;
  onSessionReloaded: () => void;
  quoteSelectionEnabled: boolean;
  onQuoteSelectionChange: (enabled: boolean) => void;
  /** Reopen the first-time onboarding wizard (polyforge#167); absent on a host. */
  onReopenOnboarding?: () => void;
  /** The page's trust status for `cwd`; Settings › MCP reloads when it changes, as after trusting. */
  projectTrust?: ProjectTrustStatus | null;
  /** Opens the page's trust dialog for `cwd`, above Settings; Settings › MCP's trust notice offers it. */
  onOpenTrustDialog?: () => void;
  /** Settings › MCP added a project server, which changed `cwd`'s trust (and may have trusted a fresh folder). */
  onProjectTrustChanged?: (cwd: string, status: ProjectTrustStatus) => void;
}

export function SettingsSectionIcon({ section, size = 16, strokeWidth = 1.8 }: { section: SettingsSection; size?: number; strokeWidth?: number }) {
  const common = {
    width: size,
    height: size,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    strokeWidth,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    "aria-hidden": true,
    className: "settings-section-icon",
  };

  if (section === "general") return <svg {...common}><path d="M20 7h-9M14 17H5" /><circle cx="7" cy="7" r="3" /><circle cx="17" cy="17" r="3" /></svg>;
  if (section === "account") return <svg {...common}><circle cx="12" cy="8" r="4" /><path d="M4 20c1.5-3.5 4.5-5.5 8-5.5s6.5 2 8 5.5" /></svg>;
  if (section === "models") return <svg {...common}><rect x="4" y="4" width="16" height="16" rx="2" /><rect x="9" y="9" width="6" height="6" /><path d="M9 1v3M15 1v3M9 20v3M15 20v3M20 9h3M20 15h3M1 9h3M1 15h3" /></svg>;
  if (section === "permission") return <svg {...common}><path d="M12 2l8 3v6c0 5-3.5 8.5-8 11-4.5-2.5-8-6-8-11V5l8-3Z" /><path d="m9 12 2 2 4-4" /></svg>;
  if (section === "skills") return <svg {...common}><path d="m12 2-10 5 10 5 10-5-10-5Z" /><path d="m2 12 10 5 10-5M2 17l10 5 10-5" /></svg>;
  if (section === "agents") return <svg {...common} className="settings-section-icon is-agent"><rect x="5" y="7" width="14" height="11" rx="2" /><path d="M9 11h.01M15 11h.01M9 15h6M12 7V4M10 4h4" /></svg>;
  if (section === "plugins") return <svg {...common}><path d="M9 7V2M15 7V2M6 13V8a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v5a6 6 0 0 1-12 0ZM12 19v3" /></svg>;
  if (section === "mcp") return <svg {...common}><rect x="3" y="3" width="18" height="7" rx="2" /><rect x="3" y="14" width="18" height="7" rx="2" /><path d="M7 6.5h.01M7 17.5h.01M11 6.5h6M11 17.5h6" /></svg>;
  return <svg {...common}><circle cx="6" cy="18" r="3" /><circle cx="18" cy="6" r="3" /><path d="M6 15V3M18 9a9 9 0 0 1-9 9" /></svg>;
}

// A phone shows Settings one level at a time: the sections, a section (its list), a row's detail.
type PhoneLevel = "home" | "section" | "detail";

const PHONE_PARENT_LEVEL: Record<PhoneLevel, PhoneLevel> = { home: "home", section: "home", detail: "section" };
const PHONE_LEVEL_DEPTH: Record<PhoneLevel, number> = { home: 0, section: 1, detail: 2 };
const PHONE_LEVEL_EASING = "cubic-bezier(0.2, 0.8, 0.2, 1)";

/**
 * A phone's level change slides the new level in: from the right going in,
 * from the left (a shorter way, fading up) going back, as a navigation stack
 * does. The header's Back and title fade in with it. Only the new level moves
 * (the old one is gone at once), so the main area clips it; nothing moves
 * under prefers-reduced-motion.
 */
function animatePhoneLevelChange(main: HTMLElement | null, header: HTMLElement | null, from: PhoneLevel, to: PhoneLevel) {
  if (!main || typeof main.animate !== "function") return;
  if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) return;
  const forward = PHONE_LEVEL_DEPTH[to] > PHONE_LEVEL_DEPTH[from];
  main.animate(forward
    ? [
      { transform: "translateX(100%)", boxShadow: "-12px 0 24px rgba(0, 0, 0, 0.12)" },
      { transform: "translateX(0)", boxShadow: "-12px 0 24px rgba(0, 0, 0, 0)" },
    ]
    : [{ transform: "translateX(-30%)", opacity: 0.4 }, { transform: "translateX(0)", opacity: 1 }], {
    duration: 280,
    easing: PHONE_LEVEL_EASING,
  });
  header?.querySelectorAll<HTMLElement>(".settings-phone-back, .settings-dialog-title").forEach((element) => {
    element.animate([
      { opacity: 0, transform: `translateX(${forward ? 16 : -16}px)` },
      { opacity: 1, transform: "translateX(0)" },
    ], { duration: 220, easing: PHONE_LEVEL_EASING });
  });
}

/**
 * The phone's level, with an entry in the browser history for each level
 * entered, so the system Back gesture goes up one level instead of leaving the
 * page. The entry copies the current history state (Next's router reads it)
 * and keeps the URL. Closing Settings drops the entries it added.
 */
function usePhoneSettingsLevel(isPhone: boolean, openAtHome: boolean) {
  const [level, setLevel] = useState<PhoneLevel>(openAtHome ? "home" : "section");
  // A desktop shows a section, so a window narrowed to a phone's width stays
  // on it instead of falling back to the level the panel opened at.
  if (!isPhone && level !== "section") setLevel("section");
  const levelRef = useRef(level);
  levelRef.current = level;
  const pushedRef = useRef(0);

  useEffect(() => {
    if (!isPhone) return;
    const onPopState = () => {
      if (pushedRef.current === 0) return;
      pushedRef.current -= 1;
      setLevel((current) => PHONE_PARENT_LEVEL[current]);
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [isPhone]);

  const enter = useCallback((next: PhoneLevel) => {
    if (!isPhone) return;
    window.history.pushState(window.history.state, "");
    pushedRef.current += 1;
    setLevel(next);
  }, [isPhone]);

  const goUp = useCallback(() => {
    // An entry this panel added: popstate goes up, as the system Back does.
    if (pushedRef.current > 0) window.history.back();
    else setLevel((current) => PHONE_PARENT_LEVEL[current]);
  }, []);

  const dropHistory = useCallback(() => {
    const pushed = pushedRef.current;
    pushedRef.current = 0;
    if (pushed > 0) window.history.go(-pushed);
  }, []);

  return { level, levelRef, enter, goUp, dropHistory };
}

export function SettingsPanel({
  cwd,
  sessionId,
  initialSection,
  openAtHome = false,
  onClose: closeSettings,
  onSessionReloaded,
  quoteSelectionEnabled,
  onQuoteSelectionChange,
  onReopenOnboarding,
  projectTrust,
  onOpenTrustDialog,
  onProjectTrustChanged,
}: Props) {
  const { t } = useI18n();
  const [section, setSection] = useState<SettingsSection>(initialSection);
  const [mountedSections, setMountedSections] = useState<ReadonlySet<SettingsSection>>(
    () => new Set([section]),
  );
  // The rail is one fact in lib/settings-navigation.ts (SETTINGS_TABS): the
  // nine sections, each with its translated label (polyforge#257 r2#9: the
  // retired legacy-marked pages left no consumer of a `legacy` flag).
  const sections: { id: SettingsSection; label: string; requiresProject: boolean }[] = SETTINGS_TABS
    .map((item) => ({
      id: item.id,
      label: t(item.labelKey),
      requiresProject: settingsSectionRequiresProject(item.id),
    }));
  const sectionRequiresProject = settingsSectionRequiresProject(section);
  const isPhone = useIsMobile();
  const phone = usePhoneSettingsLevel(isPhone, openAtHome);
  const { dropHistory, enter: enterPhoneLevel, goUp: goUpPhoneLevel, levelRef: phoneLevelRef } = phone;
  const headerRef = useRef<HTMLDivElement>(null);
  const mainRef = useRef<HTMLElement>(null);
  const shownPhoneLevelRef = useRef(phone.level);
  // Before paint, so the new level never shows in place first.
  useLayoutEffect(() => {
    const from = shownPhoneLevelRef.current;
    shownPhoneLevelRef.current = phone.level;
    if (isPhone && from !== phone.level) animatePhoneLevelChange(mainRef.current, headerRef.current, from, phone.level);
  }, [isPhone, phone.level]);
  const onClose = useCallback(() => {
    dropHistory();
    closeSettings();
  }, [closeSettings, dropHistory]);
  const sectionLabel = sections.find((item) => item.id === section)?.label ?? t("settings.title");
  // Named by the detail's active row (ConfigSidebarItem); none until one reports.
  const [detailTitle, setDetailTitle] = useState<string | null>(null);
  const detailNavigation = useMemo<ConfigDetailNavigation>(() => ({
    detailOpen: phone.level === "detail",
    openDetail: () => {
      if (phoneLevelRef.current !== "section") return;
      setDetailTitle(null);
      enterPhoneLevel("detail");
    },
    closeDetail: () => {
      if (phoneLevelRef.current === "detail") goUpPhoneLevel();
    },
    reportDetailTitle: setDetailTitle,
  }), [enterPhoneLevel, goUpPhoneLevel, phone.level, phoneLevelRef]);

  useEffect(() => setLastSettingsSection(initialSection), [initialSection]);

  // Bubble phase, unless something nearer handled the key. The trust dialog,
  // which opens above Settings, takes Escape in the capture phase and stops it,
  // so one Escape closes that dialog and leaves Settings open.
  useEffect(() => listenForPanelEscape(document, onClose), [onClose]);

  // Focus moves into Settings as it opens and back to what had it once it closes. Left
  // on the chat composer (a bare /mcp opens Settings from there), Escape would reach the
  // composer first and stop a running agent, which also kept Settings open. A layout
  // effect, so the opener is read before a section focuses its own search box.
  const dialogRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => focusModalPanel(document, dialogRef.current, {
    restoreTextEntry: !window.matchMedia?.("(pointer: coarse)").matches,
  }), []);

  useEffect(() => {
    if (cwd || !sectionRequiresProject) return;
    setSection("general");
    setMountedSections((current) => new Set(current).add("general"));
    setLastSettingsSection("general");
  }, [cwd, sectionRequiresProject]);

  const activateSection = (nextSection: SettingsSection) => {
    setMountedSections((current) => new Set(current).add(nextSection));
    setSection(nextSection);
    setLastSettingsSection(nextSection);
  };

  const openSectionFromHome = (nextSection: SettingsSection) => {
    activateSection(nextSection);
    enterPhoneLevel("section");
  };

  const sectionHost = (id: SettingsSection, content: ReactNode) => mountedSections.has(id) ? (
    <div
      key={id}
      hidden={section !== id || phone.level === "home"}
      data-phone-level={isPhone ? phone.level : undefined}
      data-settings-section={id}
      className="settings-section-host"
    >
      {content}
    </div>
  ) : null;

  return (
    <div
      ref={dialogRef}
      role="dialog"
      aria-modal="true"
      aria-label={t("settings.title")}
      tabIndex={-1}
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
      className="settings-dialog-backdrop"
    >
      <div className="settings-dialog-surface">
        <div ref={headerRef} className="settings-dialog-header">
          {isPhone && phone.level !== "home" && (() => {
            const backName = phone.level === "detail" ? sectionLabel : t("settings.title");
            return (
              <button
                type="button"
                className="settings-phone-back"
                onClick={goUpPhoneLevel}
                aria-label={t("settings.backTo", { name: backName })}
              >
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="m15 18-6-6 6-6" />
                </svg>
                <span>{backName}</span>
              </button>
            );
          })()}
          {/* A phone names the level it shows: Settings, the section, the detail's row. */}
          <strong className="settings-dialog-title">
            {!isPhone || phone.level === "home"
              ? t("settings.title")
              : phone.level === "section" ? sectionLabel : detailTitle}
          </strong>
          <nav aria-label={t("settings.title")} className="settings-section-tabs">
            {sections.map((item) => {
              const selected = section === item.id;
              const disabled = item.requiresProject && !cwd;
              return (
                <button
                  key={item.id}
                  type="button"
                  className="settings-section-tab"
                  disabled={disabled}
                  title={disabled ? t("settings.projectRequired") : item.label}
                  aria-current={selected ? "page" : undefined}
                  onClick={() => activateSection(item.id)}
                >
                  <SettingsSectionIcon section={item.id} />
                  <span>{item.label}</span>
                </button>
              );
            })}
          </nav>
          <button type="button" onClick={onClose} title={t("i18n.close")} aria-label={t("i18n.close")} className="config-close-button settings-dialog-close">×</button>
        </div>

        <main ref={mainRef} className="settings-dialog-main">
          {isPhone && phone.level === "home" && (
            <nav aria-label={t("settings.title")} className="settings-phone-home">
              {sections.map((item) => {
                const disabled = item.requiresProject && !cwd;
                return (
                  <button
                    key={item.id}
                    type="button"
                    className="settings-phone-home-item"
                    disabled={disabled}
                    aria-describedby={disabled ? `settings-phone-home-${item.id}-note` : undefined}
                    onClick={() => openSectionFromHome(item.id)}
                  >
                    <SettingsSectionIcon section={item.id} size={20} />
                    <span className="settings-phone-home-label">
                      <span>{item.label}</span>
                      {disabled && (
                        <span id={`settings-phone-home-${item.id}-note`} className="settings-phone-home-note">
                          {t("settings.projectRequired")}
                        </span>
                      )}
                    </span>
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className="settings-phone-home-chevron">
                      <path d="m9 18 6-6-6-6" />
                    </svg>
                  </button>
                );
              })}
            </nav>
          )}
          <ConfigDetailNavigationContext.Provider value={detailNavigation}>
          {sectionHost("general", <HarnessGeneralSettings cwd={cwd} sessionId={sessionId} onSessionReloaded={onSessionReloaded} quoteSelectionEnabled={quoteSelectionEnabled} onQuoteSelectionChange={onQuoteSelectionChange} onReopenOnboarding={onReopenOnboarding} />)}
          {/* 85a2's account tab replaced the 85a1b placeholder; the models
              placeholder is 85a3's; the permission page is 85a4's, mounted
              above (its Claude part is where a stored old `claude` navigation
              value lands). */}
          {sectionHost("account", <AccountSettings />)}
          {sectionHost("models", <HarnessModelSettings cwd={cwd} onClose={onClose} onOpenAccount={() => activateSection("account")} />)}
          {sectionHost("permission", <HarnessPermissionSettings cwd={cwd} sessionId={sessionId} />)}
          {/* Visited sections stay mounted, so the ones whose answer depends on trust take the page's
              status and load again in place when trusting from Settings › MCP changes it. */}
          {/* No project needed either (85d AC3): the user scope lists alone, a
              project adds its group. */}
          {sectionHost("skills", <SkillsConfig embedded key={cwd ?? ""} cwd={cwd} trust={projectTrust} onClose={onClose} />)}
          {cwd && sectionHost("agents", <AgentsConfig embedded key={cwd} cwd={cwd} sessionId={sessionId} trust={projectTrust} onClose={onClose} onReloaded={onSessionReloaded} />)}
          {cwd && sectionHost("plugins", <PluginsConfig embedded key={cwd} cwd={cwd} sessionId={sessionId} trust={projectTrust} onClose={onClose} onReloaded={onSessionReloaded} />)}
          {/* No project needed: the global mcp.json is listed alone, and a project adds its group. */}
          {sectionHost("mcp", <McpConfig embedded key={cwd ?? ""} cwd={cwd} trust={projectTrust} onTrustProject={onOpenTrustDialog} onProjectTrustChanged={onProjectTrustChanged} onClose={onClose} />)}
          {sectionHost("polyforge", <PolyforgeConfig cwd={cwd} active={section === "polyforge"} />)}
          </ConfigDetailNavigationContext.Provider>
        </main>
      </div>
    </div>
  );
}
