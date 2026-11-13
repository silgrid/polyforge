/**
 * Stream and page-lifecycle controller for the terminal panel (pi#84).
 *
 * Everything the panel does around its output stream (SSE) — connecting,
 * offset-based replay, exit handling, page hide/offline suspension — lives
 * here instead of the component effect, so the lifecycle races flagged in
 * review are one testable policy rather than closure state:
 *
 *  - pagehide/offline suspend the panel: a shell start that resolves while
 *    the page is hidden, `connect`, and a stream whose `open` fires after
 *    the hide can never lift that suspension or flush retained input while
 *    the page is invisible — bfcache pagehide keeps `navigator.onLine` true,
 *    so an online check alone cannot gate this. The matching
 *    pageshow/online resumes and reconnects, and only then flushes;
 *  - a failed shell start (or a failed input writer) marks the input bridge
 *    failed: stdin is disabled and the startup buffer is dropped
 *    (see terminal-panel-input.ts), so input typed around a dead start can
 *    never be silently replayed into the fresh shell a later Reconnect
 *    spawns.
 */

import type { TerminalEvent } from "@/lib/terminal-manager";
import type { TerminalPanelInput } from "./terminal-panel-input";

/** The EventSource surface this controller drives (satisfied by EventSource). */
export interface TerminalEventSource {
  close(): void;
  readonly readyState: number;
  // The handlers carry Event parameters because the DOM EventSource types do;
  // this controller always assigns its own (parameter-less) handlers.
  onopen: ((event: Event) => void) | null;
  onmessage: ((message: MessageEvent) => void) | null;
  onerror: ((event: Event) => void) | null;
}

// EventSource.CLOSED, spelled out: this module also loads where the global
// EventSource constructor is not available.
const STREAM_CLOSED = 2;

export interface TerminalStreamPanel {
  /** Create (or verify/revive) the server-side shell for this panel. Rejects on failure. */
  start(): Promise<void>;
  /** Open the output stream; `after` resumes from the recorded UTF-16 offset. */
  openStream(after: number | undefined): TerminalEventSource;
  input: TerminalPanelInput;
  isOnline(): boolean;
  addWindowListener(type: string, handler: EventListener): void;
  removeWindowListener(type: string, handler: EventListener): void;
  onOutput(data: string): void;
  onReset(): void;
  /** The stream (re)connected with the page visible and input alive. */
  onStreamReady(): void;
  onStatus(status: "connecting" | "ready" | "error"): void;
  onError(reason: string): void;
  onExited(exitCode: number | null): void;
}

export interface TerminalPanelStream {
  /** Resolves once the shell-start attempt has settled (success or failure). */
  started(): Promise<void>;
  /** The input writer failed: stop input, disable stdin and surface the error. */
  markInputFailed(reason: string): void;
  /** True while a live stream is attached to a healthy shell (resize is safe). */
  canResize(): boolean;
  dispose(): void;
}

export function createTerminalPanelStream(panel: TerminalStreamPanel): TerminalPanelStream {
  let disposed = false;
  let events: TerminalEventSource | null = null;
  let offset: number | undefined;
  let connected = false;
  let exited = false;
  let inputFailed = false;
  // Set by pagehide/offline and cleared only by the matching pageshow/online:
  // no stream may open, recover or flush while the page is suspended.
  let pageSuspended = false;

  const markPageHidden = () => {
    pageSuspended = true;
    connected = false;
    panel.input.suspendStdin();
    events?.close();
    if (!exited && !inputFailed) panel.onStatus("connecting");
  };
  const resume = () => {
    pageSuspended = false;
    connect();
  };
  const pageShown = (event: Event) => {
    if ((event as PageTransitionEvent).persisted) resume();
  };

  const connect = () => {
    if (disposed || exited || pageSuspended || !panel.isOnline()) return;
    events?.close();
    const stream = panel.openStream(offset);
    events = stream;
    stream.onmessage = (message) => {
      const event = JSON.parse(String(message.data)) as TerminalEvent;
      if (event.type === "output") {
        if (event.reset) panel.onReset();
        else if (offset !== undefined && event.offset <= offset) return;
        panel.onOutput(event.data);
        offset = event.offset;
      } else {
        exited = true;
        connected = false;
        panel.input.markExited();
        events?.close();
        panel.onExited(event.type === "exit" ? event.exitCode : null);
      }
    };
    stream.onopen = () => {
      connected = true;
      // A stream opening after the page hid (shell start racing pagehide, or
      // a stale socket) must not lift the suspension and flush retained input
      // while the page is invisible, and must not re-enable the stdin of a
      // failed writer. Recovery happens on pageshow/online instead.
      if (disposed || inputFailed || pageSuspended) return;
      panel.input.outputStreamRecovered();
      panel.onStatus("ready");
      panel.onStreamReady();
    };
    stream.onerror = () => {
      if (disposed || exited) return;
      connected = false;
      // A broken output stream must not freeze input (pi#84): keystrokes
      // keep flowing through the writer, and a genuinely dead shell
      // surfaces through markInputFailed on the next keystroke instead.
      panel.input.outputStreamInterrupted();
      panel.onStatus(stream.readyState === STREAM_CLOSED ? "error" : "connecting");
    };
  };

  const markInputFailed = (reason: string) => {
    if (disposed) return;
    // The shell never came up (failed start) or input delivery failed: stop
    // input outright. The bridge drops its retained buffer with this mark,
    // so keystrokes typed around the failure can never be silently replayed
    // into the fresh shell a later Reconnect spawns.
    inputFailed = true;
    panel.input.markInputFailed();
    panel.onError(reason);
    panel.onStatus("error");
  };

  const started = (async () => {
    try {
      await panel.start();
      if (disposed) return;
      // Safe while suspended: the bridge holds the startup flush until the
      // output stream recovers with the page visible again.
      panel.input.markShellReady();
      connect();
    } catch (reason) {
      markInputFailed(reason instanceof Error ? reason.message : String(reason));
    }
  })();

  panel.addWindowListener("pagehide", markPageHidden);
  panel.addWindowListener("offline", markPageHidden);
  panel.addWindowListener("pageshow", pageShown);
  panel.addWindowListener("online", resume);

  return {
    started: () => started,
    markInputFailed,
    canResize: () => connected && !exited && !inputFailed,
    dispose() {
      if (disposed) return;
      disposed = true;
      events?.close();
      panel.removeWindowListener("pagehide", markPageHidden);
      panel.removeWindowListener("offline", markPageHidden);
      panel.removeWindowListener("pageshow", pageShown);
      panel.removeWindowListener("online", resume);
    },
  };
}
