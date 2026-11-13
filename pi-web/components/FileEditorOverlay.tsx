"use client";

import { useEffect, useMemo, useState } from "react";
import { PrismAsyncLight } from "react-syntax-highlighter";
import { vs } from "react-syntax-highlighter/dist/cjs/styles/prism";
import { vscDarkPlus } from "react-syntax-highlighter/dist/cjs/styles/prism";
import { resolveHighlightLanguage } from "@/lib/prism-language";

/**
 * The edit-mode surface (pi#81): a transparent textarea precisely overlaid
 * on a syntax-highlighted layer of the same text — same font metrics, same
 * padding, so the caret and selection sit exactly on the highlighted
 * glyphs. The highlight layer re-renders debounced (typing stays instant);
 * beyond SOURCE_HIGHLIGHT_MAX_LINES it degrades to a plain <pre>, exactly
 * like the read-only preview does.
 */

const HIGHLIGHT_MAX_LINES = 1_000;
const HIGHLIGHT_DEBOUNCE_MS = 150;
const LINE_HEIGHT = 20;

export interface FileEditorOverlayProps {
  value: string;
  onChange: (value: string) => void;
  language: string;
  isDark: boolean;
  wrapLines: boolean;
  /** Raw keydown from the textarea (Cmd+S save, Esc exit — owned by FileViewer). */
  onKeyDown?: (event: React.KeyboardEvent<HTMLTextAreaElement>) => void;
}

const MONO_METRICS = {
  fontFamily: "var(--font-mono)",
  fontSize: 12,
  lineHeight: `${LINE_HEIGHT}px`,
  tabSize: 4,
} as const;

export function FileEditorOverlay({
  value,
  onChange,
  language,
  isDark,
  wrapLines,
  onKeyDown,
}: FileEditorOverlayProps) {
  const [highlightInput, setHighlightInput] = useState(value);
  useEffect(() => {
    const handle = setTimeout(() => setHighlightInput(value), HIGHLIGHT_DEBOUNCE_MS);
    return () => clearTimeout(handle);
  }, [value]);

  const lines = useMemo(() => value.split("\n"), [value]);
  const highlightLines = useMemo(() => highlightInput.split("\n").length, [highlightInput]);
  const usePrism = language !== "text" && highlightLines <= HIGHLIGHT_MAX_LINES;
  const gutterWidth = `${String(lines.length).length + 1}ch`;
  const whiteSpace = wrapLines ? "pre-wrap" : "pre";

  return (
    <div className="file-editor" style={{ display: "flex", minHeight: "100%" }} data-testid="file-editor">
      {/* Line-number gutter, same metrics as the layers to its right. */}
      <div
        aria-hidden="true"
        style={{
          ...MONO_METRICS,
          width: gutterWidth,
          flexShrink: 0,
          padding: "12px 8px 24px 4px",
          textAlign: "right",
          color: "var(--text-dim)",
          userSelect: "none",
          whiteSpace: "pre",
        }}
      >
        {lines.map((_, index) => (
          <div key={index} style={{ height: LINE_HEIGHT }}>{index + 1}</div>
        ))}
      </div>
      <div style={{ position: "relative", flex: 1, minWidth: 0 }}>
        {usePrism ? (
          <PrismAsyncLight
            language={resolveHighlightLanguage(language)}
            style={isDark ? vscDarkPlus : vs}
            customStyle={{
              margin: 0,
              padding: "12px 0 24px 0",
              background: "transparent",
              overflow: "visible",
              whiteSpace,
              ...MONO_METRICS,
            }}
            codeTagProps={{ style: { ...MONO_METRICS } }}
          >
            {highlightInput}
          </PrismAsyncLight>
        ) : (
          <pre
            style={{
              ...MONO_METRICS,
              margin: 0,
              padding: "12px 0 24px 0",
              background: "transparent",
              whiteSpace,
              color: "var(--text)",
            }}
          >
            {highlightInput}
          </pre>
        )}
        {/* The editing layer: transparent text, visible caret. */}
        <textarea
          value={value}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Tab") {
              // Insert a soft tab instead of leaving the editor.
              event.preventDefault();
              const target = event.currentTarget;
              const { selectionStart, selectionEnd } = target;
              const next = `${value.slice(0, selectionStart)}  ${value.slice(selectionEnd)}`;
              onChange(next);
              requestAnimationFrame(() => {
                target.selectionStart = target.selectionEnd = selectionStart + 2;
              });
            }
            onKeyDown?.(event);
          }}
          wrap={wrapLines ? "soft" : "off"}
          spellCheck={false}
          autoComplete="off"
          aria-label="File editor"
          data-testid="file-editor-textarea"
          style={{
            position: "absolute",
            inset: 0,
            width: "100%",
            height: "100%",
            margin: 0,
            padding: "12px 0 24px 0",
            border: "none",
            outline: "none",
            resize: "none",
            background: "transparent",
            color: "transparent",
            caretColor: "var(--text)",
            overflow: "hidden",
            whiteSpace,
            overflowWrap: wrapLines ? "break-word" : "normal",
            ...MONO_METRICS,
          }}
        />
      </div>
    </div>
  );
}
