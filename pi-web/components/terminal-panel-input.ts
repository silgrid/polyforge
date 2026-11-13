/**
 * Input bridge for the xterm terminal panel (pi#84).
 *
 * The panel renders xterm with `screenReaderMode: true`, and that mode makes
 * xterm drop the bare `input`-event insertion path (upstream `_inputEvent`
 * guard). Keystrokes that reach the page only as `input` events — on-screen
 * keyboards that fire no keydown (iOS/WKWebView), emoji pickers, some IME
 * commits — would be lost, so this bridge forwards them.
 *
 * Everything the panel does to terminal input lives here so the policy is
 * testable in one place:
 *  - keystrokes that arrive before the server-side shell exists are buffered
 *    and flushed in order once `markShellReady` runs;
 *  - suspending stdin (page hide, offline, tab close) gates this bridge's own
 *    forwarding too, not just xterm's stdin option: input that arrives while
 *    suspended is retained and flushes in order when the output stream
 *    recovers; a closing tab's buffer is dropped at dispose. Nothing is ever
 *    sent while suspended;
 *  - insertions already delivered by xterm's own keydown/keypress paths are
 *    never double-sent, with the suppression scoped to the actual event
 *    sequence: keyup and blur end it, so a stale mark cannot swallow later
 *    soft-keyboard input;
 *  - compositions are coordinated with xterm's own delivery, never with
 *    candidate-string equality: xterm's CompositionHelper delivers the commit
 *    itself from the textarea's final value on a deferred timeout (a final
 *    value that can differ from every composition candidate — see the
 *    installed @xterm/xterm CompositionHelper for why), so the bridge forwards
 *    NOTHING while that delivery is pending — not just a single commit echo,
 *    but every insertion up to xterm's own onData, because xterm reads the
 *    textarea's value at delivery time and a keystroke typed in that window
 *    is already included in what xterm is about to send (swallowing only the
 *    first such event would double-send the rest, e.g. "yXy" instead of "Xy"
 *    for a commit "X" plus a keystroke "y" typed before the deferred read);
 *    that suppression expires on the same deferred turn as xterm's own
 *    delivery (xterm sends nothing at all for an empty or cancelled
 *    composition, so an armed guard must not outlive that turn) and also ends
 *    at the keyup/blur/new-composition boundaries, so a finished IME commit
 *    cannot swallow later input;
 *  - the output stream (SSE) is independent of the input path: a recoverable
 *    output-stream interruption leaves stdin alone, while exit, failed input
 *    delivery, page hide and terminal close keep disabling it;
 *  - a failed shell start or a failed input writer stops input outright:
 *    stdin is disabled and the retained buffer is dropped, so keystrokes
 *    typed around a dead start can never be silently replayed into the fresh
 *    shell a later Reconnect spawns.
 */

/**
 * Extracts shell input from a bare `input` event, or null when the event is
 * not a direct text insertion. `insertLineBreak` (some on-screen keyboards'
 * Enter) maps to the CR the pty expects. Composition text, paste and
 * deletions are handled by xterm's own paths and are not insertions here.
 */
export function insertedTerminalData(event: Pick<InputEvent, "inputType" | "data">): string | null {
  if (event.inputType === "insertLineBreak") return "\r";
  if (event.inputType === "insertText" && typeof event.data === "string" && event.data) return event.data;
  return null;
}

/** The xterm surface this bridge drives (satisfied by `Terminal`). */
export interface TerminalInputTarget {
  readonly textarea: HTMLTextAreaElement | null | undefined;
  onData(listener: (data: string) => void): { dispose(): void };
  readonly options: { disableStdin?: boolean | undefined };
}

export interface TerminalPanelInput {
  /** The server-side shell exists (create or attach resolved): flush held input in order. */
  markShellReady(): void;
  /**
   * The output stream (SSE) broke or is reconnecting. This is a recoverable
   * *output* interruption: the input writer posts independently of the SSE
   * stream, so stdin deliberately stays enabled and buffered/forwarded input
   * keeps flowing. A genuinely dead shell surfaces through the input path
   * itself (`markInputFailed`) on the next keystroke. Must not touch
   * disableStdin — that is what froze the panel's input on every SSE blip.
   */
  outputStreamInterrupted(): void;
  /**
   * The output stream reconnected: lift a page-hide/offline suspension,
   * re-enable stdin (unless input already failed) and flush retained input
   * in order.
   */
  outputStreamRecovered(): void;
  /** The shell process exited: input stops and stdin is disabled. */
  markExited(): void;
  /**
   * Input delivery failed ambiguously (writer stopped, or the shell never
   * came up): input stops, stdin is disabled and the retained buffer is
   * dropped — input typed around the failure must never replay into whatever
   * shell a later Reconnect spawns.
   */
  markInputFailed(): void;
  /**
   * The page is hidden or the browser went offline, or the tab is closing:
   * stdin is disabled and the bridge itself stops sending — input arriving
   * while suspended is retained and flushes on recovery; a closing tab's
   * buffer is dropped at dispose.
   */
  suspendStdin(): void;
  dispose(): void;
}

export function createTerminalPanelInput(terminal: TerminalInputTarget, send: (data: string) => void): TerminalPanelInput {
  let disposed = false;
  let shellReady = false;
  let exited = false;
  let inputFailed = false;
  let suspended = false;
  let earlyInput = "";
  // A keydown or an active composition means xterm delivers the next insertion
  // through its own keydown/keypress paths; forwarding it again would
  // double-send. The mark is scoped to the actual sequence: keyup and blur
  // clear it, so an unrelated later bare insertion (soft-keyboard Enter after
  // a finished IME commit, text after an arrow key) is still forwarded
  // instead of being swallowed by a stale mark.
  let insertionDeliveredByXterm = false;
  // Set at compositionend. xterm's CompositionHelper is about to deliver the
  // commit itself — from the textarea's *final* value, on a deferred timeout
  // — and some browsers echo that commit with one more `input` insertion
  // whose data can differ from every composition candidate. The first
  // insertion after compositionend is therefore swallowed regardless of
  // content, exactly once. The guard ends at the boundaries that prove the
  // commit sequence is over: xterm's own delivery (observed on onData),
  // keyup, blur, a new composition — or the deferred turn itself (see
  // onCompositionEnd), because a cancelled or empty composition makes
  // xterm deliver nothing at all and an armed guard must not then swallow
  // the next genuine insertion.
  let pendingCompositionEcho = false;
  let pendingCompositionEchoTimer: ReturnType<typeof setTimeout> | null = null;
  const clearCompositionEchoGuard = () => {
    pendingCompositionEcho = false;
    if (pendingCompositionEchoTimer !== null) {
      clearTimeout(pendingCompositionEchoTimer);
      pendingCompositionEchoTimer = null;
    }
  };

  const active = () => !exited && !inputFailed;
  const setStdinEnabled = (enabled: boolean) => {
    terminal.options.disableStdin = !enabled;
  };
  const forward = (data: string) => {
    if (!active()) return;
    // Suspension gates this bridge's own forwarding, not just xterm's stdin
    // option, and pre-shell keystrokes are held: both retain input in order
    // instead of sending (or dropping) it.
    if (shellReady && !suspended) send(data);
    else earlyInput += data;
  };
  const flushRetained = () => {
    if (!earlyInput) return;
    const buffered = earlyInput;
    earlyInput = "";
    forward(buffered);
  };

  const onData = terminal.onData((data) => {
    // Anything xterm delivers through its own key/keypress/composition paths
    // proves the composition commit sequence, if any, has run its course:
    // after the deferred compositionend delivery no echo is still pending.
    clearCompositionEchoGuard();
    forward(data);
  });
  const textarea = terminal.textarea;

  const markDeliveredByXterm = () => {
    insertionDeliveredByXterm = true;
    clearCompositionEchoGuard();
  };
  const clearDeliveredByXterm = () => {
    insertionDeliveredByXterm = false;
    clearCompositionEchoGuard();
  };
  const onCompositionStart = () => {
    // Composition insertions are delivered by xterm itself at compositionend
    // (from the textarea's final value), so nothing the browser reports
    // during the composition may be forwarded — neither the candidate updates
    // nor a commit echo fired before compositionend.
    insertionDeliveredByXterm = true;
    clearCompositionEchoGuard();
  };
  const onCompositionEnd = () => {
    insertionDeliveredByXterm = false;
    pendingCompositionEcho = true;
    // Expire the echo guard on the same deferred turn as xterm's own
    // composition delivery. The CompositionHelper sends the commit from a
    // setTimeout(0) scheduled at compositionend — and sends nothing at all
    // when the textarea's final value holds no composition text (a cancelled
    // or empty composition; see its `input.length > 0` guard). Without this
    // expiry such a composition would leave the guard armed until the next
    // insertion and unconditionally swallow that first genuine character or
    // Enter. A real commit echo, when the browser fires one, is dispatched
    // in the same event cascade as compositionend — before this timeout — so
    // it is still swallowed exactly once.
    if (pendingCompositionEchoTimer !== null) clearTimeout(pendingCompositionEchoTimer);
    pendingCompositionEchoTimer = setTimeout(() => {
      pendingCompositionEchoTimer = null;
      pendingCompositionEcho = false;
    }, 0);
  };
  const onTextareaInput = (event: Event) => {
    const input = event as InputEvent;
    if (input.inputType === "insertCompositionText") return;
    const insertion = insertedTerminalData(input);
    if (insertion === null) return;
    if (pendingCompositionEcho) {
      // Anything that arrives while xterm's own deferred delivery is still
      // pending reads back into (or precedes) that same delivery: xterm's
      // CompositionHelper reads the textarea's *current* value at delivery
      // time, so a genuine keystroke typed here is already included in what
      // xterm is about to send and must not also be forwarded here — only
      // the first such event (a commit echo) is swallowed, the rest would be
      // double-sent. The guard is cleared only by xterm's own onData (the
      // delivery happened, observed above) or by the deferred-turn timer
      // (delivery was empty/cancelled) — never by consuming one event here —
      // so an echo followed by a genuine keystroke before xterm's deferred
      // read cannot produce e.g. "yXy" for a commit "X" plus typed "y".
      return;
    }
    if (!insertionDeliveredByXterm) forward(insertion);
    insertionDeliveredByXterm = false;
  };

  textarea?.addEventListener("keydown", markDeliveredByXterm);
  textarea?.addEventListener("keyup", clearDeliveredByXterm);
  textarea?.addEventListener("blur", clearDeliveredByXterm);
  textarea?.addEventListener("compositionstart", onCompositionStart);
  textarea?.addEventListener("compositionend", onCompositionEnd);
  textarea?.addEventListener("input", onTextareaInput);

  return {
    markShellReady() {
      shellReady = true;
      // A page hidden (or offline) while the shell was starting keeps holding
      // the buffered input: it flushes when the stream recovers.
      if (!suspended) flushRetained();
    },
    outputStreamInterrupted() {
      // Deliberately leaves stdin enabled: see the interface doc.
    },
    outputStreamRecovered() {
      suspended = false;
      if (!active()) return;
      setStdinEnabled(true);
      flushRetained();
    },
    markExited() {
      exited = true;
      setStdinEnabled(false);
    },
    markInputFailed() {
      inputFailed = true;
      // The shell this input was meant for is gone (failed start or failed
      // delivery): drop the retained buffer with it. Input typed around the
      // failure must never be silently replayed into the fresh shell a later
      // Reconnect spawns (pi#84 review).
      earlyInput = "";
      setStdinEnabled(false);
    },
    suspendStdin() {
      suspended = true;
      setStdinEnabled(false);
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      onData.dispose();
      clearCompositionEchoGuard();
      textarea?.removeEventListener("keydown", markDeliveredByXterm);
      textarea?.removeEventListener("keyup", clearDeliveredByXterm);
      textarea?.removeEventListener("blur", clearDeliveredByXterm);
      textarea?.removeEventListener("compositionstart", onCompositionStart);
      textarea?.removeEventListener("compositionend", onCompositionEnd);
      textarea?.removeEventListener("input", onTextareaInput);
    },
  };
}
