"use client";

// One pi subagent run's progress panel (polyforge#255): the controller that
// reads the run and the child transcript, polls them while the panel is open
// (Q5), pages earlier history through the existing context route and sends a
// steer message through the one subagents route (Q2). The React hook owns one
// controller per open panel, so a session switch disposes it and its late
// answers never reach another child (Q4).
import { useEffect, useRef, useState } from "react";
import {
  mergePiSubagentEntries,
  piSubagentRunFromJson,
  piSubagentSnapshotReplaces,
  piSubagentSteerRequest,
  piSubagentTranscriptFromJson,
  type PiSubagentEntry,
  type PiSubagentPanelPhase,
  type PiSubagentRunView,
} from "@/lib/pi-subagent-panel";

/** How often an open, visible panel re-reads its snapshot. */
export const PI_SUBAGENT_PANEL_POLL_MS = 2_000;
/** How many 404s after the first one the panel tolerates before saying "missing". */
export const PI_SUBAGENT_PANEL_MISSING_RETRIES = 3;

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
  /** The chat the panel is open in: the family walk stops here. */
  parentSessionId: string;
  /** A card's own launch call id, which wins over the run's parent call (Q6). */
  preferredToolCallId?: string | null;
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

/** The one route that reads a run; a subagent's own, never a session route. */
function runUrl(sessionId: string): string {
  return `/api/subagents/${encodeURIComponent(sessionId)}`;
}

/**
 * Reads one run's panel. A read is single-flight: a 2 s tick that lands while
 * the previous read still runs is skipped, never started beside it, so a slow
 * answer still lands (Q5's at most one batch every 2 s). Only `dispose` (a
 * child switch) invalidates an in-flight answer.
 */
export class PiSubagentPanelController {
  private readonly options: PiSubagentPanelControllerOptions;
  private readonly fetchImpl: typeof fetch;
  private state: PiSubagentPanelSnapshot = emptyPiSubagentPanelSnapshot();
  private readSeq = 0;
  private earlierSeq = 0;
  private disposed = false;
  private missingAttempts = 0;
  private inFlight = false;
  private launchToolCallId: string | null = null;
  private launchResolved = false;

  constructor(options: PiSubagentPanelControllerOptions) {
    this.options = options;
    this.fetchImpl = options.fetchImpl ?? ((input, init) => fetch(input, init));
  }

  get snapshot(): PiSubagentPanelSnapshot {
    return this.state;
  }

  private apply(patch: Partial<PiSubagentPanelSnapshot>): void {
    if (this.disposed) return;
    this.state = { ...this.state, ...patch };
    this.options.onChange?.(this.state);
  }

  private current(seq: number): boolean {
    return !this.disposed && seq === this.readSeq;
  }

  private currentEarlier(seq: number): boolean {
    return !this.disposed && seq === this.earlierSeq;
  }

  async load(): Promise<void> {
    await this.read("replace");
  }

  async refresh(): Promise<void> {
    await this.read("append");
  }

  private async read(mode: "replace" | "append"): Promise<void> {
    if (this.inFlight) return;
    this.inFlight = true;
    const seq = this.readSeq;
    try {
      let response: Response;
      try {
        response = await this.fetchImpl(runUrl(this.options.childSessionId));
      } catch (error) {
        if (!this.current(seq)) return;
        this.apply({ phase: "error", error: message(error) });
        return;
      }
      if (!this.current(seq)) return;
      if (response.status === 404) {
        this.missingAttempts += 1;
        if (this.missingAttempts > (this.options.missingRetries ?? PI_SUBAGENT_PANEL_MISSING_RETRIES)) {
          this.apply({ phase: "missing", error: null });
        }
        return;
      }
      this.missingAttempts = 0;
      if (!response.ok) {
        this.apply({ phase: "error", error: `HTTP ${response.status}` });
        return;
      }
      const run = piSubagentRunFromJson(await response.json().catch(() => null));
      if (!this.current(seq)) return;
      if (!run) {
        this.apply({ phase: "error", error: "unreadable run" });
        return;
      }
      // The run's own read decides the header; the child transcript is read on
      // the same snapshot, and a settled run gets one more read of it (the
      // terminal status and the final entry do not land in one answer, Q5).
      const transcript = await this.readTranscript(seq);
      if (!this.current(seq)) return;
      if (!this.launchResolved) {
        this.launchToolCallId = this.options.preferredToolCallId
          ?? await this.resolveLaunch(run, seq);
        if (!this.current(seq)) return;
        this.launchResolved = true;
      }
      if (transcript.ok) {
        // A replacement (first load, or a snapshot with no overlap after a
        // pause longer than its tail) adopts the snapshot's own hasMore: the
        // missing middle must stay reachable through 「加载更早」 (r3#2).
        const replaced = piSubagentSnapshotReplaces(this.state.entries, transcript.value.entries);
        this.apply({
          run,
          phase: "ready",
          error: null,
          entries: mergePiSubagentEntries(this.state.entries, transcript.value.entries, mode),
          hasEarlier: mode === "replace" || replaced ? transcript.value.hasEarlier : this.state.hasEarlier,
          cwd: transcript.value.cwd ?? this.state.cwd,
          launchToolCallId: this.launchToolCallId,
        });
      } else if (transcript.error === null) {
        // A queued or starting run may have no session file yet: an empty
        // body, not an error — the header still explains the wait.
        this.apply({
          run,
          phase: "ready",
          error: null,
          ...(mode === "replace" ? { hasEarlier: false } : {}),
          launchToolCallId: this.launchToolCallId,
        });
      } else {
        // The last snapshot stays visible, but the panel says it is stale and
        // disables sending until a read succeeds again (Q5).
        this.apply({ run, phase: "error", error: transcript.error, launchToolCallId: this.launchToolCallId });
      }
    } finally {
      this.inFlight = false;
    }
  }

  private async readTranscript(seq: number): Promise<{ ok: true; value: NonNullable<ReturnType<typeof piSubagentTranscriptFromJson>> } | { ok: false; error: string | null }> {
    let response: Response;
    try {
      response = await this.fetchImpl(`/api/sessions/${encodeURIComponent(this.options.childSessionId)}`);
    } catch (error) {
      return { ok: false, error: message(error) };
    }
    if (!this.current(seq)) return { ok: false, error: null };
    if (response.status === 404) return { ok: false, error: null };
    if (!response.ok) return { ok: false, error: `HTTP ${response.status}` };
    const value = piSubagentTranscriptFromJson(await response.json().catch(() => null));
    return value ? { ok: true, value } : { ok: false, error: "unreadable transcript" };
  }

  /**
   * The Agent call in THIS chat that leads to the child. A direct child is its
   * own launch; a deeper descendant walks its ancestors' runs until one names
   * this chat. A chain that cannot be read (404, a network failure, a cycle)
   * yields null — never another chat's call id (Q6).
   */
  private async resolveLaunch(run: PiSubagentRunView, seq: number): Promise<string | null> {
    let current: PiSubagentRunView | null = run;
    const seen = new Set<string>();
    for (let depth = 0; current && depth < 20; depth += 1) {
      if (current.parentSessionId === this.options.parentSessionId) return current.parentToolCallId ?? null;
      if (seen.has(current.parentSessionId)) return null;
      seen.add(current.parentSessionId);
      let response: Response;
      try {
        response = await this.fetchImpl(runUrl(current.parentSessionId));
      } catch {
        return null;
      }
      if (!this.current(seq)) return this.launchToolCallId;
      if (!response.ok) return null;
      current = piSubagentRunFromJson(await response.json().catch(() => null));
    }
    return null;
  }

  /** One earlier page through the existing context route, prepended once (Q5). */
  async loadEarlier(): Promise<void> {
    const oldest = this.state.entries[0]?.id;
    if (!oldest || this.state.loadingEarlier || this.state.phase !== "ready") return;
    // Paging has its own sequence: a 2 s snapshot poll running beside it must
    // not drop the page (and leave the button stuck loading), and the page
    // must not drop a poll either.
    const seq = ++this.earlierSeq;
    this.apply({ loadingEarlier: true });
    const query = `leafId=${encodeURIComponent(oldest)}&before=${encodeURIComponent(oldest)}&tail=50`;
    let response: Response;
    try {
      response = await this.fetchImpl(`/api/sessions/${encodeURIComponent(this.options.childSessionId)}/context?${query}`);
    } catch (error) {
      if (!this.currentEarlier(seq)) return;
      this.apply({ loadingEarlier: false, error: message(error) });
      return;
    }
    if (!this.currentEarlier(seq)) return;
    const page = response.ok ? piSubagentTranscriptFromJson(await response.json().catch(() => null)) : null;
    if (!page) {
      this.apply({ loadingEarlier: false, error: response.ok ? "unreadable transcript" : `HTTP ${response.status}` });
      return;
    }
    this.apply({
      entries: mergePiSubagentEntries(this.state.entries, page.entries, "prepend"),
      hasEarlier: page.hasEarlier,
      cwd: page.cwd ?? this.state.cwd,
      loadingEarlier: false,
      error: null,
    });
  }

  /**
   * Send `text` through the run's own steer action. A refusal (409 while the
   * run settles, 500, a network failure) reports false so the panel keeps the
   * draft, shows the server's message and re-reads the run; it never retries
   * by itself or falls back to a prompt.
   */
  async steer(text: string): Promise<boolean> {
    if (this.disposed) return false;
    const request = piSubagentSteerRequest(this.options.childSessionId, text);
    try {
      const response = await this.fetchImpl(request.url, request.init);
      if (!response.ok) {
        const body = await response.json().catch(() => null) as { error?: unknown } | null;
        this.apply({ steerError: typeof body?.error === "string" ? body.error : `HTTP ${response.status}` });
        await this.refresh();
        return false;
      }
      this.apply({ steerError: null });
      await this.refresh();
      return true;
    } catch (error) {
      this.apply({ steerError: message(error) });
      await this.refresh();
      return false;
    }
  }

  dispose(): void {
    this.disposed = true;
    this.readSeq += 1;
    this.earlierSeq += 1;
  }
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * The panel's controller for one open child: reads on open, polls every 2 s
 * while the page is visible, and re-reads at once when it becomes visible
 * again. Closing or switching the child disposes the controller (its late
 * answers are dropped).
 */
export function usePiSubagentPanel(options: {
  childSessionId: string | null;
  parentSessionId: string | null;
  /** The Agent card's own call id, when the panel was opened from one. */
  toolCallId?: string | null;
  open: boolean;
}): PiSubagentPanelSnapshot & {
  loadEarlier: () => void;
  steer: (text: string) => Promise<boolean>;
} {
  const { childSessionId, parentSessionId, toolCallId, open } = options;
  const [snapshot, setSnapshot] = useState<PiSubagentPanelSnapshot>(emptyPiSubagentPanelSnapshot);
  const controllerRef = useRef<PiSubagentPanelController | null>(null);
  useEffect(() => {
    controllerRef.current?.dispose();
    controllerRef.current = null;
    if (!open || !childSessionId || !parentSessionId) {
      setSnapshot(emptyPiSubagentPanelSnapshot());
      return undefined;
    }
    setSnapshot(emptyPiSubagentPanelSnapshot());
    const controller = new PiSubagentPanelController({
      childSessionId,
      parentSessionId,
      preferredToolCallId: toolCallId,
      onChange: setSnapshot,
    });
    controllerRef.current = controller;
    void controller.load();
    const tick = () => {
      if (typeof document !== "undefined" && document.hidden) return;
      void controller.refresh();
    };
    const timer = setInterval(tick, PI_SUBAGENT_PANEL_POLL_MS);
    const onVisible = () => {
      if (typeof document !== "undefined" && !document.hidden) void controller.refresh();
    };
    if (typeof document !== "undefined") document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(timer);
      if (typeof document !== "undefined") document.removeEventListener("visibilitychange", onVisible);
      controller.dispose();
      if (controllerRef.current === controller) controllerRef.current = null;
    };
  }, [childSessionId, parentSessionId, toolCallId, open]);
  return {
    ...snapshot,
    loadEarlier: () => {
      void controllerRef.current?.loadEarlier();
    },
    steer: (text: string) => controllerRef.current?.steer(text) ?? Promise.resolve(false),
  };
}
