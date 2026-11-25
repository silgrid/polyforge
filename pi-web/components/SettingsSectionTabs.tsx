"use client";

// The settings dialog's section tab bar (polyforge#259, T4 AC3): the nine
// sections' tabs and their scroll hint. Presentational like SettingsUi.tsx —
// the tabs, their words, the active one and the click all come from
// SettingsPanel. The bar keeps every label at every width (owner ruling
// 2026-11-23: no icon-only bar) and scrolls once nine tabs no longer fit;
// below 1000px the chevron marks the sections off its right edge, and the
// stylesheet, not JavaScript, decides where it shows.

import type { ReactNode } from "react";
import type { SettingsSection } from "@/lib/settings-navigation";

export interface SettingsSectionTab {
  id: SettingsSection;
  label: string;
  /** A section that needs a project, opened without one. */
  disabled: boolean;
  icon: ReactNode;
}

export interface SettingsSectionTabsProps {
  /** Names the navigation for assistive technology. */
  label: string;
  tabs: readonly SettingsSectionTab[];
  active: SettingsSection;
  /** Why a disabled tab is disabled; its tooltip when there is no label. */
  projectRequiredLabel: string;
  onSelect: (section: SettingsSection) => void;
}

export function SettingsSectionTabs({ label, tabs, active, projectRequiredLabel, onSelect }: SettingsSectionTabsProps) {
  return (
    <>
      <nav aria-label={label} className="settings-section-tabs">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            type="button"
            className="settings-section-tab"
            disabled={tab.disabled}
            title={tab.disabled ? projectRequiredLabel : tab.label}
            aria-current={tab.id === active ? "page" : undefined}
            onClick={() => onSelect(tab.id)}
          >
            {tab.icon}
            <span>{tab.label}</span>
          </button>
        ))}
      </nav>
      <span className="settings-section-tabs-more" data-settings-tabs-more="" aria-hidden="true">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="m9 18 6-6-6-6" />
        </svg>
      </span>
    </>
  );
}