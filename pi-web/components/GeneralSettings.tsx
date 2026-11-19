"use client";

// General's 界面与对话 pane (polyforge#149, #85 85a1b): the former
// SettingsPanel GeneralSettings content, plus the claude hook-activity
// preference it shares with the legacy Claude page. Placeholder signature;
// the body arrives with its unit and replaces this stub.
export interface GeneralSettingsProps {
  sessionId: string | null;
  onSessionReloaded: () => void;
  quoteSelectionEnabled: boolean;
  onQuoteSelectionChange: (enabled: boolean) => void;
}

export function GeneralSettings(_props: GeneralSettingsProps): null {
  return null;
}
