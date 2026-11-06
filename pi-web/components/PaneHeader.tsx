"use client";

import { RunningSessionIndicator } from "@/components/RunningSessionIndicator";

// Embedded pane header (pi#25): each pane column carries its own header row
// instead of the old shared tab strip. The header keeps tab semantics
// (role="tab" + aria-selected, keyboard focus, running dot, completion badge,
// inline close ✕ with unchanged onClosePane wiring) while spanning the full
// pane width, so the pane content region below gains the strip's former height.

const PANE_HEADER_HEIGHT = 30;

interface PaneHeaderProps {
  id: string;
  label: string;
  running: boolean;
  hasBadge: boolean;
  focused: boolean;
  onClick: () => void;
  onClose: () => void;
  /** Drag-to-reorder (pi#70): spread from useTabDragReorder.getDragHandlers. */
  draggable?: boolean;
  isDragging?: boolean;
  /** Insertion-position indicator: which side of THIS header a drop would land on. */
  dropIndicator?: "before" | "after" | null;
  onDragStart?: (event: React.DragEvent<HTMLButtonElement>) => void;
  onDragOver?: (event: React.DragEvent<HTMLButtonElement>) => void;
  onDragLeave?: (event: React.DragEvent<HTMLButtonElement>) => void;
  onDrop?: (event: React.DragEvent<HTMLButtonElement>) => void;
  onDragEnd?: (event: React.DragEvent<HTMLButtonElement>) => void;
  /** Localized aria-roledescription announcing the tab is drag-reorderable. */
  reorderRoleDescription?: string;
  /** In-tab search opener (pi#80): raises the pane's floating search bar. */
  onSearch?: () => void;
  /** Localized aria-label/title for the search affordance. */
  searchLabel?: string;
}

export function PaneHeader({
  id,
  label,
  running,
  hasBadge,
  focused,
  onClick,
  onClose,
  draggable,
  isDragging,
  dropIndicator,
  onDragStart,
  onDragOver,
  onDragLeave,
  onDrop,
  onDragEnd,
  reorderRoleDescription,
  onSearch,
  searchLabel,
}: PaneHeaderProps) {
  const dropIndicatorShadow = dropIndicator === "before"
    ? "inset 3px 0 0 0 var(--accent)"
    : dropIndicator === "after"
      ? "inset -3px 0 0 0 var(--accent)"
      : undefined;
  return (
    <button
      type="button"
      id={id}
      role="tab"
      aria-selected={focused}
      aria-roledescription={reorderRoleDescription}
      onClick={onClick}
      title={label}
      draggable={draggable}
      onDragStart={onDragStart}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      onDragEnd={onDragEnd}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 6,
        width: "100%",
        height: PANE_HEADER_HEIGHT,
        padding: "0 10px 0 8px",
        flexShrink: 0,
        border: "none",
        borderTop: focused ? "2px solid var(--accent)" : "2px solid transparent",
        borderBottom: "1px solid var(--border)",
        background: focused ? "var(--bg-selected)" : "var(--bg-panel)",
        color: focused ? "var(--text)" : "var(--text-muted)",
        cursor: "pointer",
        fontSize: 11,
        fontFamily: "inherit",
        whiteSpace: "nowrap",
        overflow: "hidden",
        textOverflow: "ellipsis",
        opacity: isDragging ? 0.4 : 1,
        boxShadow: dropIndicatorShadow,
        transition: "background 0.1s, color 0.1s",
      }}
    >
      {running && <RunningSessionIndicator />}
      <span
        style={{
          minWidth: 0,
          flex: "1 1 auto",
          overflow: "hidden",
          textOverflow: "ellipsis",
          textAlign: "left",
        }}
      >
        {label}
      </span>
      {hasBadge && (
        <span
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            background: "#f59e0b",
            flexShrink: 0,
          }}
          aria-label="completed"
        />
      )}
      {onSearch && (
        <span
          role="button"
          tabIndex={0}
          aria-label={searchLabel ?? "Search in tab"}
          title={searchLabel ?? "Search in tab"}
          onClick={(e) => {
            e.stopPropagation();
            onSearch();
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.stopPropagation();
              e.preventDefault();
              onSearch();
            }
          }}
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            width: 18,
            height: 18,
            borderRadius: 4,
            lineHeight: 1,
            color: "var(--text-dim)",
            cursor: "pointer",
            flexShrink: 0,
            marginLeft: 2,
          }}
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden="true" style={{ display: "block" }}>
            <circle cx="11" cy="11" r="6.5" stroke="currentColor" strokeWidth="2" />
            <path d="M16 16 L21 21" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </svg>
        </span>
      )}
      <span
        role="button"
        tabIndex={0}
        aria-label="Close tab"
        onClick={(e) => {
          e.stopPropagation();
          onClose();
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.stopPropagation();
            e.preventDefault();
            onClose();
          }
        }}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          width: 18,
          height: 18,
          borderRadius: 4,
          fontSize: 11,
          lineHeight: 1,
          color: "var(--text-dim)",
          cursor: "pointer",
          flexShrink: 0,
          marginLeft: 2,
        }}
      >
        ×
      </span>
    </button>
  );
}
