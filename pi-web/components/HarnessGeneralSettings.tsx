"use client";

// General's left-column skeleton (polyforge#149, #85 85a1b, AC1): the three
// switchable panes of the settled list — 界面与对话 / 环境 / 生效值与来源 —
// hosted in the shared split view, so a phone walks them as list → detail →
// back through the settings panel's own detail navigation (owner ruling Q7 A:
// no navigation system of its own). The panes stay unmounted until chosen, so
// each reads once per activation like the other settings pages.
import { useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import {
  ConfigDetail,
  ConfigPanelShell,
  ConfigSidebar,
  ConfigSidebarItem,
  ConfigSidebarList,
  ConfigSplitView,
} from "./SettingsUi";
import { GeneralSettings } from "./GeneralSettings";
import { HarnessEnvironmentSettings } from "./HarnessEnvironmentSettings";
import { HarnessEffectiveSettings } from "./HarnessEffectiveSettings";

export interface HarnessGeneralSettingsProps {
  cwd: string | null;
  sessionId: string | null;
  onSessionReloaded: () => void;
  quoteSelectionEnabled: boolean;
  onQuoteSelectionChange: (enabled: boolean) => void;
  /** Reopen the first-time onboarding wizard (polyforge#167); absent on a host. */
  onReopenOnboarding?: () => void;
}

/** The three panes, in their settled order; the label keys are one fact here. */
const GENERAL_PANES = [
  { id: "conversation", labelKey: "settings.generalPaneConversation" },
  { id: "environment", labelKey: "settings.generalPaneEnvironment" },
  { id: "effective", labelKey: "settings.generalPaneEffective" },
] as const;

type GeneralPaneId = (typeof GENERAL_PANES)[number]["id"];

export function HarnessGeneralSettings({
  cwd,
  sessionId,
  onSessionReloaded,
  quoteSelectionEnabled,
  onQuoteSelectionChange,
  onReopenOnboarding,
}: HarnessGeneralSettingsProps) {
  const { t } = useI18n();
  const [pane, setPane] = useState<GeneralPaneId>("conversation");
  return (
    <ConfigPanelShell embedded title={t("settings.general")} onClose={() => {}}>
      <ConfigSplitView>
        <ConfigSidebar>
          <ConfigSidebarList>
            {GENERAL_PANES.map((item) => (
              <ConfigSidebarItem key={item.id} active={pane === item.id} onClick={() => setPane(item.id)}>
                <span className="config-sidebar-text">{t(item.labelKey)}</span>
              </ConfigSidebarItem>
            ))}
          </ConfigSidebarList>
        </ConfigSidebar>
        <ConfigDetail>
          <header className="settings-content-heading"><div><h2>{t(GENERAL_PANES.find((item) => item.id === pane)!.labelKey)}</h2><p>{t("settings.generalIntro")}</p></div></header>
          {pane === "conversation" && (
            <div data-general-pane="conversation" className="settings-general-pane-host">
              <GeneralSettings
                sessionId={sessionId}
                onSessionReloaded={onSessionReloaded}
                quoteSelectionEnabled={quoteSelectionEnabled}
                onQuoteSelectionChange={onQuoteSelectionChange}
                onReopenOnboarding={onReopenOnboarding}
              />
            </div>
          )}
          {pane === "environment" && (
            <div data-general-pane="environment" className="settings-general-pane-host">
              <HarnessEnvironmentSettings cwd={cwd} />
            </div>
          )}
          {pane === "effective" && (
            <div data-general-pane="effective" className="settings-general-pane-host">
              <HarnessEffectiveSettings cwd={cwd} />
            </div>
          )}
        </ConfigDetail>
      </ConfigSplitView>
    </ConfigPanelShell>
  );
}
