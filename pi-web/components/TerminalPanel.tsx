"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { terminalFontSize, useChatAppearance } from "@/hooks/useChatAppearance";
import { useI18n } from "@/hooks/useI18n";
import { setTerminalTheme, useTerminalTheme } from "@/hooks/useTerminalTheme";
import { subscribeFontPreferences } from "@/hooks/useFontPreferences";
import { readFontWeight } from "@/lib/font-preferences";
import { createTerminalWriter, terminalRequest } from "@/lib/terminal-client";
import { createTerminalPanelInput, type TerminalPanelInput } from "./terminal-panel-input";
import { createTerminalPanelStream, type TerminalPanelStream } from "./terminal-panel-stream";
import { pageId, trackEventSource } from "@/lib/page-debug";
import {
  applyTerminalModifiers,
  NO_TERMINAL_MODIFIERS,
  terminalExtraKeySequence,
  type TerminalExtraKey,
  type TerminalModifier,
  type TerminalModifiers,
} from "@/lib/terminal-extra-keys";
import { TERMINAL_XTERM_THEMES } from "@/lib/terminal-theme";
import { TerminalExtraKeys, useTerminalExtraKeysVisible } from "./TerminalExtraKeys";
import type { TerminalTab } from "./terminal-tab-state";

interface Props {
  tab: TerminalTab;
  active: boolean;
  onRestart: () => void;
  onClosed: () => void;
  onCloseError: () => void;
}

export function TerminalPanel({ tab, active, onRestart, onClosed, onCloseError }: Props) {
  const { t } = useI18n();
  const { id, cwd, restored } = tab;
  const containerRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const startRef = useRef<Promise<void>>(Promise.resolve());
  const writerRef = useRef<ReturnType<typeof createTerminalWriter> | null>(null);
  const callbacksRef = useRef({ onClosed, onCloseError });
  callbacksRef.current = { onClosed, onCloseError };
  const [status, setStatus] = useState<"connecting" | "ready" | "exited" | "error">("connecting");
  const [error, setError] = useState<string | null>(null);
  const [exitCode, setExitCode] = useState<number | null>(null);
  const [reconnectKey, setReconnectKey] = useState(0);
  const inputRef = useRef<TerminalPanelInput | null>(null);
  const [modifiers, setModifiers] = useState<TerminalModifiers>(NO_TERMINAL_MODIFIERS);
  const modifiersRef = useRef(modifiers);
  const showExtraKeys = useTerminalExtraKeysVisible();
  const theme = useTerminalTheme();
  const themeRef = useRef(theme);
  themeRef.current = theme;
  const fontSize = terminalFontSize(useChatAppearance().fontSize);
  const fontSizeRef = useRef(fontSize);
  fontSizeRef.current = fontSize;
  const refitRef = useRef<() => void>(() => {});

  const consumeModifiers = useCallback(() => {
    const current = modifiersRef.current;
    if (current.ctrl || current.alt) {
      modifiersRef.current = NO_TERMINAL_MODIFIERS;
      setModifiers(NO_TERMINAL_MODIFIERS);
    }
    return current;
  }, []);
  const sendExtraKey = (key: TerminalExtraKey) => {
    const terminal = terminalRef.current;
    if (!terminal) return;
    const sequence = terminalExtraKeySequence(key, consumeModifiers(), terminal.modes.applicationCursorKeysMode);
    terminal.input(sequence, true);
  };
  const toggleModifier = (modifier: TerminalModifier) => {
    const next = { ...modifiersRef.current, [modifier]: !modifiersRef.current[modifier] };
    modifiersRef.current = next;
    setModifiers(next);
  };

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let disposed = false;
    setStatus("connecting");
    setError(null);
    setExitCode(null);

    const readTerminalFont = () => {
      const style = getComputedStyle(container);
      return {
        fontFamily: style.getPropertyValue("--font-mono").trim() || "monospace",
        // Base weights stop at 600, so xterm's default bold (700) stays heavier.
        fontWeight: readFontWeight(style.getPropertyValue("--font-mono-weight")),
      };
    };
    const terminal = new Terminal({
      cursorBlink: true,
      ...readTerminalFont(),
      fontSize: fontSizeRef.current,
      lineHeight: 1.25,
      scrollback: 8000,
      screenReaderMode: true,
      // Input stays enabled from the start so keystrokes typed while the
      // panel settles are captured (and flushed once the shell exists)
      // instead of being silently swallowed; error/exit/offline paths below
      // still disable it.
      disableStdin: false,
      theme: TERMINAL_XTERM_THEMES[themeRef.current],
    });
    terminalRef.current = terminal;
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(container);
    terminal.attachCustomKeyEventHandler((event) => {
      if (event.type !== "keydown") return true;
      const key = event.key.toLowerCase();
      if ((event.ctrlKey || event.metaKey) && key === "v") return false;
      if ((event.ctrlKey || event.metaKey) && key === "c" && terminal.hasSelection()) return false;
      return true;
    });

    const fitAndResize = () => {
      if (!container.offsetWidth || !container.offsetHeight) return;
      fit.fit();
    };

    let stream: TerminalPanelStream | null = null;
    const writer = createTerminalWriter(id, (reason) => {
      if (disposed) return;
      stream?.markInputFailed(reason.message);
    });
    writerRef.current = writer;
    // All terminal-input handling (early buffering, bare-input forwarding with
    // lifecycle-scoped dedup, and the disableStdin policy) lives in the
    // bridge; the SSE output stream, page-lifecycle suspension and the
    // failed-start input policy live in the stream controller — both so the
    // races between them are testable (pi#84).
    // A Ctrl/Alt armed on the extra-keys row applies to the next input, typed or tapped.
    const inputBridge = createTerminalPanelInput(terminal, (data) => writer.write(applyTerminalModifiers(data, consumeModifiers())));
    inputRef.current = inputBridge;
    const onResize = terminal.onResize(({ cols, rows }) => {
      if (stream?.canResize()) writer.resize(cols, rows);
    });
    refitRef.current = fitAndResize;
    const resizeObserver = new ResizeObserver(fitAndResize);
    resizeObserver.observe(container);
    const updateFont = () => {
      if (disposed) return;
      const font = readTerminalFont();
      terminal.options.fontFamily = font.fontFamily;
      terminal.options.fontWeight = font.fontWeight;
      fitAndResize();
    };
    const unsubscribeFonts = subscribeFontPreferences(updateFont);

    const createServerTerminal = () => terminalRequest("/api/terminal", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, cwd, cols: terminal.cols, rows: terminal.rows }),
    });
    stream = createTerminalPanelStream({
      start: async () => {
        fitAndResize();
        if (reconnectKey > 0) {
          try {
            await terminalRequest(`/api/terminal/${encodeURIComponent(id)}`);
          } catch (reason) {
            // An explicit Reconnect on a tab whose server terminal is gone
            // (server restart, lease expiry) revives the tab in place: same
            // id, fresh shell, so the panel is never a dead end.
            if (!navigator.onLine) throw reason;
            await createServerTerminal();
          }
        } else if (restored) {
          // Restoring a tab must never silently launch a replacement shell.
          await terminalRequest(`/api/terminal/${encodeURIComponent(id)}`);
        } else {
          await createServerTerminal();
        }
      },
      openStream: (after) => {
        // `holder`: the page, for the Debug panel's connected pages.
        const query = new URLSearchParams({ holder: pageId() });
        if (after !== undefined) query.set("after", String(after));
        return trackEventSource("terminal", new EventSource(`/api/terminal/${encodeURIComponent(id)}/events?${query}`), cwd);
      },
      input: inputBridge,
      isOnline: () => navigator.onLine,
      addWindowListener: (type, handler) => window.addEventListener(type, handler),
      removeWindowListener: (type, handler) => window.removeEventListener(type, handler),
      onOutput: (data) => terminal.write(data),
      onReset: () => terminal.reset(),
      onStreamReady: () => {
        fitAndResize();
        writer.resize(Math.max(2, terminal.cols), Math.max(2, terminal.rows));
        if (container.offsetWidth && container.offsetHeight) terminal.focus();
      },
      onStatus: setStatus,
      onError: setError,
      onExited: (exitCode) => {
        setExitCode(exitCode);
        setStatus("exited");
      },
    });
    startRef.current = stream.started();

    return () => {
      disposed = true;
      stream?.dispose();
      void writer.stop();
      inputRef.current = null;
      resizeObserver.disconnect();
      unsubscribeFonts();
      inputBridge.dispose();
      onResize.dispose();
      terminal.dispose();
      terminalRef.current = null;
      refitRef.current = () => {};
    };
  }, [id, cwd, restored, reconnectKey, consumeModifiers]);

  useEffect(() => {
    if (terminalRef.current) terminalRef.current.options.theme = TERMINAL_XTERM_THEMES[theme];
  }, [theme]);

  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal || terminal.options.fontSize === fontSize) return;
    // A refit sends the new cols/rows to the shell through onResize, as a font change does.
    terminal.options.fontSize = fontSize;
    refitRef.current();
  }, [fontSize]);

  useEffect(() => {
    if (active) terminalRef.current?.focus();
  }, [active]);

  useEffect(() => {
    if (!tab.closing) return;
    let cancelled = false;
    inputRef.current?.suspendStdin();
    void (async () => {
      await startRef.current;
      await writerRef.current?.stop();
      await terminalRequest(`/api/terminal/${encodeURIComponent(id)}`, { method: "DELETE", keepalive: true });
      if (!cancelled) callbacksRef.current.onClosed();
    })().catch((reason: Error) => {
      if (cancelled) return;
      setError(reason.message);
      setStatus("error");
      callbacksRef.current.onCloseError();
    });
    return () => { cancelled = true; };
  }, [id, tab.closing]);

  const nextTheme = theme === "dark" ? "light" : "dark";
  const themeLabel = nextTheme === "light" ? "terminal.lightTheme" : "terminal.darkTheme";

  return (
    <section className="terminal-panel" data-terminal-theme={theme} aria-label={t("terminal.title")}>
      <header className="terminal-panel-header">
        <div className="terminal-panel-path">
          <span className={`terminal-status-dot is-${status}`} title={t(`terminal.${status}`)} />
          <span title={cwd}>{cwd}</span>
        </div>
        <div className="terminal-panel-actions">
          {status === "error" && (
            <button type="button" onClick={() => setReconnectKey((key) => key + 1)} disabled={Boolean(tab.closing)} title={t("terminal.reconnect")} aria-label={t("terminal.reconnect")}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-2 2M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l2-2" />
              </svg>
            </button>
          )}
          <button type="button" onClick={() => setTerminalTheme(nextTheme)} title={t(themeLabel)} aria-label={t(themeLabel)}>
            {nextTheme === "light" ? (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41" />
              </svg>
            ) : (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
              </svg>
            )}
          </button>
          <button type="button" onClick={onRestart} disabled={Boolean(tab.closing)} title={t("terminal.restart")} aria-label={t("terminal.restart")}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M20 11a8 8 0 1 0-2.34 5.66" /><polyline points="20 4 20 11 13 11" />
            </svg>
          </button>
        </div>
      </header>
      <div>
        {error && <div className="terminal-panel-error" role="alert">{error}</div>}
        {status === "exited" && <div className="terminal-panel-exit" role="status">{exitCode === null ? t("terminal.exited") : t("terminal.exitCode", { code: exitCode })}</div>}
      </div>
      <div className="terminal-xterm"><div ref={containerRef} className="terminal-xterm-host" /></div>
      {showExtraKeys && <TerminalExtraKeys modifiers={modifiers} onKey={sendExtraKey} onToggleModifier={toggleModifier} />}
    </section>
  );
}
