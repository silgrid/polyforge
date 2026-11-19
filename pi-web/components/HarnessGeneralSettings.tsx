"use client";

// General's left-column skeleton (polyforge#149, #85 85a1b): three switchable
// panes — 界面与对话 / 环境 / 生效值与来源. This placeholder keeps the file's
// signature so a test that needs the symbol compiles; the body arrives with
// its unit and replaces this stub.
import type { ReactNode } from "react";

export interface HarnessGeneralSettingsProps {
  cwd: string | null;
  sessionId: string | null;
  onSessionReloaded: () => void;
  quoteSelectionEnabled: boolean;
  onQuoteSelectionChange: (enabled: boolean) => void;
  children?: ReactNode;
}

export function HarnessGeneralSettings(_props: HarnessGeneralSettingsProps): null {
  return null;
}
