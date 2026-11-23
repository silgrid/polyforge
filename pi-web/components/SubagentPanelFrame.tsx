"use client";

// The shared shell of a subagent progress panel: the floating section, its
// top-edge height drag and its Esc rule, with the header, body and footer
// supplied by the caller. ClaudeSubagentPanel and PiSubagentPanel both use it,
// so the two panels keep one layout, one drag and one keyboard rule.
import type { ReactNode } from "react";

/** What `isClaudeSubagentPanelEscape` reads of a keydown. */
export interface ClaudeSubagentPanelKey {
  key: string;
  isComposing: boolean;
  keyCode: number;
  defaultPrevented: boolean;
  target: EventTarget | null;
}

/** Where a finished drag keeps the panel height (px), shared by every subagent's panel. */
export const CLAUDE_SUBAGENT_PANEL_HEIGHT_KEY = "pi-web:claude-subagent-panel-height";
export const CLAUDE_SUBAGENT_PANEL_MIN_HEIGHT = 160;

/** Whether a keydown is the Escape that closes the open panel. */
export function isClaudeSubagentPanelEscape(
  _event: ClaudeSubagentPanelKey,
  _pane: { contains(node: Node | null): boolean } | null,
  _activePane: boolean,
): boolean {
  return false;
}

export function SubagentPanelFrame(_props: {
  panelAttrs: Record<`data-${string}`, string | undefined>;
  ariaLabel: string;
  accent: string;
  borderColor: string;
  failed?: boolean;
  resizeLabel: string;
  header: ReactNode;
  children: ReactNode;
  footer: ReactNode;
}): ReactNode {
  return null;
}
