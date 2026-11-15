import { useEffect, useState } from "react";
import type { CSSProperties, Dispatch, ReactNode, SetStateAction } from "react";
import ReactMarkdown from "react-markdown";
import { ThinkingIcon } from "./ThinkingIcon";
import { isThinkingExpandedByDefault, THINKING_EXPANDED_EVENT } from "@/lib/thinking-expansion-preference";
import { getThinkingPreview } from "@/lib/message-display";

/**
 * The expansion state every thinking block shares (polyforge#53 lifted it
 * from the chat session's ThinkingBlock, its one implementation): collapsed
 * (or expanded) by the 「默认展开思考块」 preference, and updated in place by
 * the preference's broadcast — the settings panel's toggle reaches
 * already-mounted blocks through THINKING_EXPANDED_EVENT. A manual choice
 * lives in the same state, so the next broadcast overrides it, exactly as the
 * session's blocks always behaved.
 */
export function useThinkingExpansion(): [boolean, Dispatch<SetStateAction<boolean>>] {
  const [expanded, setExpanded] = useState(isThinkingExpandedByDefault);
  // Keep already-mounted blocks in sync when the preference changes.
  useEffect(() => {
    const onChange = () => setExpanded(isThinkingExpandedByDefault());
    window.addEventListener(THINKING_EXPANDED_EVENT, onChange);
    return () => window.removeEventListener(THINKING_EXPANDED_EVENT, onChange);
  }, []);
  return [expanded, setExpanded];
}

/**
 * The one collapsible thinking treatment, shared by the chat session's
 * thinking blocks and the polyforge progress's thinking lines: collapsed to
 * a single row (the thinking icon + the first-line preview, markdown
 * stripped), one click toggles, and expanded the button shrinks to its icon
 * beside the caller's content. The session's own box and button styles, byte
 * for byte (the session's markup tests pin them); `contentStyle` is the
 * expanded content wrapper's own treatment — the session's muted plain text,
 * the progress's dim italic Markdown — and `trailing` whatever rides the
 * box's right edge (the session's duration).
 */
export function ThinkingFold({ text, label, expanded, onToggle, contentStyle, children, trailing }: {
  /** The whole thinking text; the collapsed preview is its first line. */
  text: string;
  /** The localized block name — the button's title and aria-label prefix. */
  label: string;
  expanded: boolean;
  onToggle: (next: boolean) => void;
  contentStyle?: CSSProperties;
  children?: ReactNode;
  trailing?: ReactNode;
}) {
  const preview = getThinkingPreview(text);
  return (
    <div style={{
      display: "flex", alignItems: "flex-start", gap: 6, minWidth: 0,
      border: "1px solid var(--border)",
      borderRadius: 7,
      padding: "6px 10px",
      background: "var(--bg)",
      fontFamily: "var(--font-mono)",
      fontSize: "calc(11px + var(--chat-font-size-offset, 0px))",
      lineHeight: 1.5,
    }}>
      <button
        type="button"
        aria-expanded={expanded}
        aria-label={`${label}${preview ? `: ${preview}` : ""}`}
        title={label}
        onClick={() => onToggle(!expanded)}
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          width: expanded ? 14 : "100%",
          flexShrink: expanded ? 0 : 1,
          minWidth: 0,
          minHeight: "1.5em",
          padding: 0,
          background: "transparent",
          border: "none",
          color: "var(--text-muted)",
          cursor: "pointer",
          font: "inherit",
          textAlign: "left",
        }}
      >
        <ThinkingIcon active={expanded} />
        {!expanded && (
          <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {preview ? <ReactMarkdown allowedElements={[]} unwrapDisallowed skipHtml>{preview}</ReactMarkdown> : "..."}
          </span>
        )}
      </button>
      {expanded && (
        <div
          style={{
            flex: 1,
            minWidth: 0,
            ...contentStyle,
          }}
        >
          {children}
        </div>
      )}
      {trailing}
    </div>
  );
}
