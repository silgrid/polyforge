"use client";

// A pi subagent run's progress panel (polyforge#255): the shared
// SubagentPanelFrame with this run's header, its child transcript read-only,
// and an input box that steers the run while it runs and is disabled with a
// reason once it ended.
import type { PiSubagentRunView } from "@/lib/pi-subagent-panel";

export function PiSubagentPanel(_props: {
  sessionId: string;
  run: PiSubagentRunView | null;
  phase: "loading" | "ready" | "missing" | "error";
  error: string | null;
  entries: readonly { id: string; message: import("@/lib/types").AgentMessage }[];
  hasEarlier: boolean;
  loadingEarlier: boolean;
  steerError: string | null;
  launchToolCallId: string | null;
  cwd: string | null;
  onLoadEarlier: () => void;
  onJump: () => void;
  onClose: () => void;
  onSteer: (text: string) => Promise<boolean>;
  now?: number;
}) {
  return null;
}
