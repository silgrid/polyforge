"use client";

// One pi subagent run's progress panel (polyforge#255): the controller that
// reads the run and the child transcript, polls while the panel is open, pages
// earlier history and sends a steer message, and the React hook that owns one.
import type { PiSubagentEntry, PiSubagentPanelPhase, PiSubagentRunView } from "@/lib/pi-subagent-panel";

export interface PiSubagentPanelSnapshot {
  run: PiSubagentRunView | null;
  entries: PiSubagentEntry[];
  hasEarlier: boolean;
  cwd: string | null;
  phase: PiSubagentPanelPhase;
  error: string | null;
  launchToolCallId: string | null;
  loadingEarlier: boolean;
  steerError: string | null;
}

export interface PiSubagentPanelControllerOptions {
  childSessionId: string;
  parentSessionId: string;
  fetchImpl?: typeof fetch;
  onChange?: (snapshot: PiSubagentPanelSnapshot) => void;
  missingRetries?: number;
}

export function emptyPiSubagentPanelSnapshot(): PiSubagentPanelSnapshot {
  return {
    run: null,
    entries: [],
    hasEarlier: false,
    cwd: null,
    phase: "loading",
    error: null,
    launchToolCallId: null,
    loadingEarlier: false,
    steerError: null,
  };
}

export class PiSubagentPanelController {
  constructor(_options: PiSubagentPanelControllerOptions) {}

  get snapshot(): PiSubagentPanelSnapshot {
    return emptyPiSubagentPanelSnapshot();
  }

  async load(): Promise<void> {}

  async refresh(): Promise<void> {}

  async loadEarlier(): Promise<void> {}

  async steer(_text: string): Promise<boolean> {
    return false;
  }

  dispose(): void {}
}

export function usePiSubagentPanel(_options: {
  childSessionId: string | null;
  parentSessionId: string | null;
  open: boolean;
}): PiSubagentPanelSnapshot & {
  loadEarlier: () => void;
  steer: (text: string) => Promise<boolean>;
} {
  return {
    ...emptyPiSubagentPanelSnapshot(),
    loadEarlier: () => {},
    steer: async () => false,
  };
}
