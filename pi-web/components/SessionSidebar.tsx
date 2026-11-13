"use client";

import { useEffect, useLayoutEffect, useState, useCallback, useMemo, useRef, useSyncExternalStore, type CSSProperties, type DragEvent, type ReactNode } from "react";
import type { SessionInfo } from "@/lib/types";
import { listSessionFamilies, getSessionFamily, isHiddenRelation, type SessionFamily } from "@/lib/session-family";
import { countFaviconUnread, syncFaviconBadge } from "@/lib/favicon-badge";
import { loadExplorerOpen, saveExplorerOpen } from "@/lib/file-explorer-state";
import { dispatchSessionRowContextMenu } from "@/lib/session-row-context-menu";
import { isPlainActivation, sessionDeepLink } from "@/lib/session-links";
import { skillExpansionToCommand } from "@/lib/slash-display";
import { getRecentProjects, isPathInsideDirectory, sessionsForDirectory, sessionsForProject } from "@/lib/project-groups";
import { prefetchSessionView } from "@/lib/session-prefetch";
import {
  addCustomDirectory,
  customDirectoryIdentity,
  isCustomDirectoryListed,
  listCustomDirectories,
  removeCustomDirectory,
  renameCustomDirectory,
  renameCustomDirectoryPath,
} from "@/lib/custom-directories";
import {
  discardExpandedGroupKey,
  readExpandedGroupKeys,
  writeExpandedGroupKeys,
} from "@/lib/pinned-expansion";
import {
  applyGroupOrder,
  listGroupOrder,
  setGroupOrder,
} from "@/lib/pinned-group-order";
import {
  createRowDeleteHandler,
  createRowPathRenameHandler,
} from "@/lib/custom-directory-manage";
import { buildExplorerRoots } from "@/lib/explorer-roots";
import {
  computeFilteredSessionIds,
  countFilteredSessions,
  getServerSessionFilterState,
  getSessionFilterState,
  isFamilyRowFiltered,
  filterSessionsByAge,
  isSessionFiltered,
  setShowFilteredSessions,
  subscribeSessionFilter,
} from "@/lib/session-filter";
import {
  buildSidebarRows,
  getWindowedRows,
  scrollTargetForSession,
  sidebarRowsHeight,
  SESSION_LIST_ITEM_HEIGHT,
  type GroupEmptyRow,
  type GroupMoreRow,
  type SessionRow,
  type SidebarRow,
  type SidebarProject,
} from "@/lib/sidebar-rows";
import { workspaceKeyOf } from "@/lib/workspace-memory";
import { formatRelativeTime } from "@/lib/i18n/format";
import { useI18n } from "@/hooks/useI18n";
import { useIsMobile } from "@/hooks/useIsMobile";
import { useFilesPlacement } from "@/lib/files-placement";
import { RunningSessionIndicator } from "@/components/RunningSessionIndicator";
import { useResizablePanel } from "@/hooks/useResizablePanel";
import { useScrollbarVisibility } from "@/hooks/useScrollbarVisibility";
import { DirectoryPicker } from "./DirectoryPicker";
import { createDirectoryPinFlow } from "@/lib/custom-directory-pin";
import { MultiRootFileExplorer, type MultiRootFileExplorerHandle } from "./MultiRootFileExplorer";
import { DismissButton } from "./DismissButton";
import { SessionSearch } from "./SessionSearch";
import {
  ARCHIVED_SESSIONS_STORAGE_KEY,
  groupArchivedSessionsByProject,
  loadArchivedSessionIds,
  pruneArchivedSessionIds,
  saveArchivedSessionIds,
  sessionArchiveStorage,
} from "@/lib/session-archive";

/** Client-side temporary session id — pi spawns lazily on first message. */
function newTempSessionId(): string {
  return typeof crypto.randomUUID === "function"
    ? crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
}

interface FileManagerAvailability {
  supported: boolean;
  reason: string | null;
  platform: string;
}

// Server error codes with a translation; any other code is shown verbatim.
const FILE_MANAGER_ERROR_KEYS: Record<string, string> = {
  remote: "sidebar.openInExplorerRemoteOnly",
  "unsupported-platform": "sidebar.openInExplorerUnsupported",
};

declare global {
  interface Window {
    piDesktop?: {
      selectDirectory: () => Promise<string | null>;
    };
  }
}

function ToolbarIconButton({
  onClick,
  title,
  disabled,
  skipHover,
  color,
  background = "none",
  marginRight,
  ariaPressed,
  children,
}: {
  onClick: () => void;
  title: string;
  disabled?: boolean;
  skipHover?: boolean;
  color: string;
  background?: string;
  marginRight?: number;
  ariaPressed?: boolean;
  children: ReactNode;
}) {
  const enter = (e: React.MouseEvent<HTMLButtonElement>) => {
    if (disabled || skipHover) return;
    e.currentTarget.style.color = "var(--text-muted)";
    e.currentTarget.style.background = "var(--bg-hover)";
  };
  const leave = (e: React.MouseEvent<HTMLButtonElement>) => {
    if (disabled || skipHover) return;
    e.currentTarget.style.color = color;
    e.currentTarget.style.background = background;
  };
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-label={title}
      aria-pressed={ariaPressed}
      style={{
        position: "relative",
        display: "flex", alignItems: "center", justifyContent: "center",
        width: 26, height: 26, padding: 0, marginRight,
        background,
        border: "none",
        color,
        cursor: disabled ? "default" : "pointer",
        borderRadius: 5,
        flexShrink: 0,
        opacity: disabled ? 0.6 : 1,
        transition: "color 0.3s, background 0.3s",
      }}
      onMouseEnter={enter}
      onMouseLeave={leave}
    >
      {children}
    </button>
  );
}

function sessionListUrl(summary: boolean, force: boolean): string {
  if (summary) return "/api/sessions?summary=1";
  if (force) return "/api/sessions?force=1";
  return "/api/sessions";
}

interface Props {
  selectedSessionId: string | null;
  /** Split-view follow: focus-derived session id the sidebar highlights.
   *  Falls back to selectedSessionId when the prop is absent, so classic
   *  call sites keep byte-for-byte the old behavior. Never the new-session
   *  sentinel — AppShell derives it via resolveSidebarSessionId. */
  highlightSessionId?: string | null;
  /** Gate for the highlight's scroll/expand follow effect. AppShell passes
   *  `splitPaneEnabled && !isMobile` so the classic and mobile layouts keep
   *  zero new scroll/expansion side effects. */
  followHighlightIntoView?: boolean;
  onSelectSession: (session: SessionInfo, isRestore?: boolean, entryId?: string, blockIndex?: number) => void;
  onNewSession?: (sessionId: string, cwd: string) => void;
  initialSessionId?: string | null;
  skipInitialProjectSelection?: boolean;
  onInitialRestoreDone?: () => void;
  refreshKey?: number;
  onSessionDeleted?: (sessionId: string) => void;
  selectedCwd?: string | null;
  onCwdChange?: (
    cwd: string | null,
    projectRoot?: string | null,
    projectKey?: string | null,
  ) => void;
  onOpenFile?: (filePath: string, fileName: string, options?: { sourceSessionId?: string | null; modeHint?: "diff" }) => void;
  onOpenTerminal?: (cwd: string) => void;
  /** Full path of the file currently shown in the editor/preview pane (the
   *  real active file-tab identity from AppShell), for the explorer's
   *  "currently open" row highlight. Not a local echo — this is the same
   *  value AppShell derives from its file-tab state, so it tracks the
   *  TabBar, tab-close fallback, and any other path that opens a file. */
  activeFilePath?: string | null;
  explorerRefreshKey?: number;
  onExplorerRefresh?: () => void;
  onAtMention?: (relativePath: string, isDir: boolean) => void;
  onAtMentions?: (relativePaths: string[]) => void;
  /** Fired when a session that is not currently selected finishes running.
   *  Lets the app play a cross-workspace completion tone. */
  onBackgroundTaskDone?: () => void;
  /**
   * Sessions that finished while the tab was hidden (#1484): usually the
   * selected one, which `unreadSessionIds` excludes. Counted by the favicon
   * until the tab is opened again; never persisted, never dotted.
   */
  hiddenFinishedSessionIds?: Set<string>;
  onRunningSessionIdsChange?: (ids: Set<string>) => void;
  onSessionsChange?: (sessions: SessionInfo[]) => void;
  /** Fired when the selected session was written externally (another pi
   *  process) so the open chat view can reload it from disk. */
  onExternalSessionChange?: (sessionId: string) => void;
}

interface ProjectSelection {
  root: string;
  key: string;
}

interface ValidatedProject {
  cwd: string;
  root: string;
  key: string;
}

const UNREAD_SESSIONS_STORAGE_KEY = "pi-web:unread-session-ids";
const LAST_CUSTOM_CWD_STORAGE_KEY = "pi-web:last-custom-cwd";
const RUNNING_SESSIONS_POLL_MS = 2500;
const SESSION_DETAILS_HYDRATION_DELAY_MS = 750;
const SESSION_PANE_DEFAULT_HEIGHT = 320;
const SESSION_PANE_MIN_HEIGHT = 80;
const EXPLORER_PANE_MIN_HEIGHT = 120;
const SESSION_PANE_MAX_HEIGHT = 1600;
/** Pinned-group pagination (wi pi#101): rows a group shows before its
 *  "show more" footer (upstream v0.12.0 value). */
const PINNED_VISIBLE_LIMIT = 8;
/** How many more rows each pinned-group "show more" click reveals. */
const SHOW_MORE_STEP = 20;
/** Stable empty set for the server-filter match fallback (a mismatched key
 *  must read as "no server matches", never as a new Set per render). */
const EMPTY_SERVER_FILTER_MATCH: ReadonlySet<string> = new Set<string>();
/** Stable empty list for the archived-slice memo (pi#103): the no-archive
 *  case must not allocate a fresh array per render. */
const EMPTY_ARCHIVED_SESSIONS: readonly SessionInfo[] = [];

function loadLastCustomCwd(): string {
  if (typeof window === "undefined") return "";
  try {
    return window.localStorage.getItem(LAST_CUSTOM_CWD_STORAGE_KEY) ?? "";
  } catch {
    return "";
  }
}

function saveLastCustomCwd(cwd: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(LAST_CUSTOM_CWD_STORAGE_KEY, cwd);
  } catch {
    // Persistence is best-effort.
  }
}

function loadUnreadSessionIds(): Set<string> {
  if (typeof window === "undefined") return new Set();
  try {
    const raw = window.localStorage.getItem(UNREAD_SESSIONS_STORAGE_KEY);
    if (!raw) return new Set();
    const parsed = JSON.parse(raw) as unknown;
    if (Array.isArray(parsed)) return new Set(parsed.filter((id): id is string => typeof id === "string"));
    return new Set();
  } catch {
    return new Set();
  }
}

function saveUnreadSessionIds(ids: Set<string>): void {
  if (typeof window === "undefined") return;
  try {
    if (ids.size === 0) window.localStorage.removeItem(UNREAD_SESSIONS_STORAGE_KEY);
    else window.localStorage.setItem(UNREAD_SESSIONS_STORAGE_KEY, JSON.stringify([...ids]));
  } catch {
    // ignore storage quota / privacy-mode errors
  }
}

/** Substitute the home dir prefix with ~ (no path truncation — see PathLabel) */
function displayCwd(cwd: string, homeDir?: string): string {
  return (homeDir && cwd.startsWith(homeDir)) ? "~" + cwd.slice(homeDir.length) : cwd;
}

// Hide-toggle for unmergeable worktree pseudo-project rows was retired with
// the worktree switcher (2026-09-23 user decision): rows render with no
// worktree-specific hiding. (readHidePseudoProjects and the
// pi-web:hide-pseudo-projects key are gone.)

// Pinned-group expansion state now lives in lib/pinned-expansion.ts (wi
// pi#52): ONE storage-injectable implementation — read, persist, and the
// new discardExpandedGroupKey used by the manage-mode delete path so a
// deleted group leaves no stale expanded key behind.

/**
 * Path label that ellipsizes on the LEFT, keeping the (most relevant) trailing
 * segments visible: "…orkspace/pi-web". Shows as much of the path as fits
 * instead of a fixed number of segments. The rtl container moves the ellipsis
 * to the left edge; the inner plaintext bidi isolation keeps the path itself
 * rendered strictly left-to-right (no punctuation reordering).
 */
function PathLabel({ text, style }: { text: string; style?: CSSProperties }) {
  return (
    <span
      style={{
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
        display: "block",
        minWidth: 0,
        lineHeight: 1.35,
        direction: "rtl",
        textAlign: "left",
        ...style,
      }}
    >
      <span style={{ unicodeBidi: "plaintext" }}>{text}</span>
    </span>
  );
}

/** Pushpin glyph for the pin affordance; filled when pinned. */
function PinIcon({ pinned }: { pinned: boolean }) {
  return (
    <svg width="10" height="10" viewBox="0 0 10 10" fill={pinned ? "currentColor" : "none"} stroke="currentColor" strokeWidth="1.1" strokeLinecap="round" strokeLinejoin="round" style={{ display: "block" }}>
      <path d="M3.6 1h2.8l-.4 2.6 1.5 1.4v.8H2.5v-.8L4 3.6z" />
      <line x1="5" y1="5.8" x2="5" y2="9" />
    </svg>
  );
}

/** Stable DOM id of a pinned group's expandable content container. */
function pinnedGroupContentId(key: string): string {
  return `pinned-group-${encodeURIComponent(key)}`;
}

/**
 * Header row of one listed-directory group in the session list: chevron +
 * label (the expand control, a real button with aria-expanded /
 * aria-controls), the per-project activity badge, the rename affordance
 * (edits the entry's displayName; an empty value clears it back to the
 * path-derived label), the remove-from-list toggle and the group's own
 * new-session [+] (relocated from the workspace dropdown). A stale root
 * renders the whole header greyed with the missing-directory hint and
 * disables [+].
 */
/**
 * Drag-to-reorder wiring handed to a pinned group header (wi B2).
 * Only non-running groups are draggable (the guard is the running count
 * from the groupActivity map); the drop TARGET may be any group, running
 * included — only the dragged item must be idle. Reordering is an
 * adjacent-swap move modeled on upstream adjacentProjectMove: the dragged
 * group is removed and reinserted at the drop target's index, and the FULL
 * key sequence is persisted through lib/pinned-group-order. Keyboard
 * (menu / hotkey) reordering is deliberately deferred to the backlog per
 * the work item.
 */
interface PinnedGroupReorderProps {
  /** False for groups with running sessions: not draggable. */
  draggable: boolean;
  /** True while this header is the active drop target (highlight). */
  isDropTarget: boolean;
  onDragStart: (event: DragEvent<HTMLDivElement>) => void;
  onDragOver: (event: DragEvent<HTMLDivElement>) => void;
  onDragLeave: (event: DragEvent<HTMLDivElement>) => void;
  onDrop: (event: DragEvent<HTMLDivElement>) => void;
  onDragEnd: () => void;
}

function PinnedGroupHeader({
  project,
  label,
  expanded,
  stale,
  activity,
  homeDir,
  t,
  reorder,
  onToggle,
  onUnpin,
  onRename,
  onNewSession,
}: {
  project: SidebarProject;
  /** User-set display name; absent falls back to the path label. */
  label?: string;
  expanded: boolean;
  /** Root no longer exists on disk: rendered greyed, [+] disabled. */
  stale: boolean;
  activity?: { running: number; unread: number };
  homeDir: string;
  t: (key: string, params?: Record<string, string | number>) => string;
  /** Drag-to-reorder wiring; absent disables the interaction. */
  reorder?: PinnedGroupReorderProps;
  onToggle: () => void;
  onUnpin: () => void;
  /** Commits a rename; an empty value clears the display name. */
  onRename: (name: string) => void;
  onNewSession: () => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const newSessionLabel = t("sidebar.newSessionTitle", { path: project.root });
  const displayLabel = label ?? displayCwd(project.root, homeDir);

  const startRename = () => {
    setRenameValue(label ?? "");
    setRenaming(true);
  };
  const commitRename = () => {
    setRenaming(false);
    const name = renameValue.trim();
    if (name === (label ?? "")) return;
    onRename(name);
  };

  return (
    <div
      // Drag-to-reorder (wi B2): the whole header row is the drag source
      // and the drop target. Draggable only for idle groups; a running
      // group can still be dropped ONTO (target ordering allowed). Dragging
      // is suspended while renaming so the input keeps normal focus /
      // selection behavior inside a draggable container.
      draggable={reorder != null && reorder.draggable && !renaming}
      onDragStart={reorder?.onDragStart}
      onDragOver={reorder?.onDragOver}
      onDragLeave={reorder?.onDragLeave}
      onDrop={reorder?.onDrop}
      onDragEnd={reorder?.onDragEnd}
      style={{
        display: "flex",
        alignItems: "center",
        height: "100%",
        paddingRight: 6,
        borderBottom: "1px solid var(--border)",
        background: reorder?.isDropTarget ? "var(--bg-selected)" : "var(--bg)",
        ...(reorder?.isDropTarget
          ? { outline: "2px dashed var(--accent)", outlineOffset: -2 }
          : null),
      }}
    >
      {renaming ? (
        <>
          <input
            value={renameValue}
            autoFocus
            placeholder={t("sidebar.renameDirectory")}
            onChange={(event) => setRenameValue(event.target.value)}
            onBlur={commitRename}
            onKeyDown={(event) => {
              if (event.key === "Enter") commitRename();
              if (event.key === "Escape") setRenaming(false);
            }}
            style={{
              flex: 1,
              minWidth: 0,
              height: 26,
              margin: "0 0 0 10px",
              padding: "0 8px",
              border: "1px solid var(--accent)",
              borderRadius: 5,
              outline: "none",
              background: "var(--bg)",
              color: "var(--text)",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
            }}
          />
          <button
            onClick={commitRename}
            title={t("sidebar.renameDirectory")}
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              height: 22,
              padding: "0 9px",
              marginLeft: 4,
              flexShrink: 0,
              background: "var(--accent)",
              border: "none",
              borderRadius: 5,
              color: "var(--accent-contrast)",
              fontSize: 11,
              fontWeight: 600,
              cursor: "pointer",
            }}
          >
            {t("sidebar.renameDirectory")}
          </button>
        </>
      ) : (
        <>
      <button
        onClick={onToggle}
        aria-expanded={expanded}
        aria-controls={pinnedGroupContentId(project.key)}
        title={stale
          ? `${project.root} — ${t("sidebar.pinnedProjectMissing")}`
          : t(expanded ? "sidebar.pinnedGroupCollapse" : "sidebar.pinnedGroupExpand", { path: project.root })}
        style={{
          flex: 1,
          minWidth: 0,
          display: "flex",
          alignItems: "center",
          gap: 6,
          height: "100%",
          padding: "0 4px 0 10px",
          background: "none",
          border: "none",
          color: stale ? "var(--text-dim)" : "var(--text)",
          cursor: "pointer",
          textAlign: "left",
        }}
      >
        <svg
          width="9"
          height="9"
          viewBox="0 0 10 10"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinecap="round"
          strokeLinejoin="round"
          style={{ flexShrink: 0, transform: expanded ? "none" : "rotate(-90deg)", transition: "transform 0.15s" }}
        >
          <polyline points="3 2 7 5 3 8" />
        </svg>
        <PathLabel
          text={displayLabel}
          style={{ flex: 1, fontFamily: "var(--font-mono)", fontSize: 11, lineHeight: 1.35 }}
        />
      </button>
      {showProjectActivity(activity, t)}
      <span
        role="button"
        tabIndex={0}
        title={t("sidebar.renameDirectory")}
        aria-label={t("sidebar.renameDirectory")}
        onClick={(e) => {
          e.stopPropagation();
          startRename();
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.stopPropagation();
            e.preventDefault();
            startRename();
          }
        }}
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: 16,
          height: 16,
          flexShrink: 0,
          marginLeft: 4,
          borderRadius: 3,
          color: "var(--text-dim)",
          cursor: "pointer",
        }}
      >
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ display: "block" }}>
          <path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z" />
        </svg>
      </span>
      <span
        role="button"
        tabIndex={0}
        title={t("sidebar.unpinProject")}
        aria-label={t("sidebar.unpinProject")}
        onClick={(e) => {
          e.stopPropagation();
          onUnpin();
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.stopPropagation();
            e.preventDefault();
            onUnpin();
          }
        }}
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: 16,
          height: 16,
          flexShrink: 0,
          marginLeft: 4,
          borderRadius: 3,
          color: "var(--accent)",
          cursor: "pointer",
        }}
      >
        <PinIcon pinned />
      </span>
      <button
        onClick={onNewSession}
        disabled={stale}
        title={newSessionLabel}
        aria-label={newSessionLabel}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          width: 22,
          height: 22,
          padding: 0,
          marginLeft: 2,
          flexShrink: 0,
          background: "none",
          border: "1px solid var(--border)",
          borderRadius: 5,
          color: stale ? "var(--text-dim)" : "var(--text-muted)",
          cursor: stale ? "default" : "pointer",
          opacity: stale ? 0.6 : 1,
        }}
      >
        <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
          <line x1="6" y1="1" x2="6" y2="11" />
          <line x1="1" y1="6" x2="11" y2="6" />
        </svg>
      </button>
        </>
      )}
    </div>
  );
}



const SCRAMBLE_CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*";

function useScramble(target: string, running: boolean): string {
  const [display, setDisplay] = useState(target);
  const frameRef = useRef<number | null>(null);
  const iterRef = useRef(0);

  useEffect(() => {
    if (!running) {
      setDisplay(target);
      return;
    }
    iterRef.current = 0;
    const totalFrames = target.length * 4;

    const step = () => {
      iterRef.current += 1;
      const progress = iterRef.current / totalFrames;
      const resolved = Math.floor(progress * target.length);

      setDisplay(
        target
          .split("")
          .map((char, i) => {
            if (char === " ") return " ";
            if (i < resolved) return char;
            return SCRAMBLE_CHARS[Math.floor(Math.random() * SCRAMBLE_CHARS.length)];
          })
          .join("")
      );

      if (iterRef.current < totalFrames) {
        frameRef.current = requestAnimationFrame(step);
      } else {
        setDisplay(target);
      }
    };

    frameRef.current = requestAnimationFrame(step);
    return () => { if (frameRef.current) cancelAnimationFrame(frameRef.current); };
  }, [target, running]);

  return display;
}

function PiWebTitle() {
  const [showVersion, setShowVersion] = useState(false);
  const [scrambling, setScrambling] = useState(false);
  const revertTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const target = showVersion ? `${process.env.NEXT_PUBLIC_APP_VERSION ?? "0.0.0"}p${process.env.NEXT_PUBLIC_PI_VERSION ?? "0.0.0"}` : "Pi Web";
  const display = useScramble(target, scrambling);

  const triggerScramble = useCallback((toVersion: boolean) => {
    setShowVersion(toVersion);
    setScrambling(true);
    setTimeout(() => setScrambling(false), (toVersion ? 6 : 8) * 4 * (1000 / 60) + 100);
  }, []);

  const handleClick = useCallback(() => {
    if (revertTimerRef.current) clearTimeout(revertTimerRef.current);

    const next = !showVersion;
    triggerScramble(next);

    if (next) {
      revertTimerRef.current = setTimeout(() => triggerScramble(false), 3000);
    }
  }, [showVersion, triggerScramble]);

  useEffect(() => () => { if (revertTimerRef.current) clearTimeout(revertTimerRef.current); }, []);

  return (
    <button
      onClick={handleClick}
      style={{
        background: "none", border: "none", padding: 0, cursor: "default",
        fontWeight: 700, fontSize: 15, letterSpacing: "-0.01em",
        color: showVersion ? "var(--accent)" : "var(--text)",
        fontFamily: "var(--font-mono)",
        minWidth: "6ch",
      }}
    >
      {display}
    </button>
  );
}

export function SessionSidebar({ selectedSessionId, highlightSessionId, followHighlightIntoView, onSelectSession, onNewSession, initialSessionId, skipInitialProjectSelection, onInitialRestoreDone, refreshKey, onSessionDeleted, selectedCwd: selectedCwdProp, onCwdChange, onOpenFile, onOpenTerminal, activeFilePath, explorerRefreshKey, onExplorerRefresh, onAtMention, onAtMentions, onBackgroundTaskDone, hiddenFinishedSessionIds, onRunningSessionIdsChange, onSessionsChange, onExternalSessionChange }: Props) {
  const { t } = useI18n();
  // Split-view follow: the row highlight reads the focus-derived id when the
  // shell passes one and falls back to the classic selection otherwise.
  // selectedSessionId itself (toast suppression, unread clearing, search)
  // keeps its exact classic semantics — only the row highlight follows focus.
  const effectiveHighlightSessionId = highlightSessionId === undefined
    ? selectedSessionId
    : highlightSessionId;
  const [allSessions, setAllSessions] = useState<SessionInfo[]>([]);
  // Tracked in a ref only: the version is compared against the polled value to
  // decide whether the list needs reloading, and no render reads it.
  const sessionListVersionRef = useRef<number | null>(null);
  const sessionLoadIdRef = useRef(0);
  // Pane-tab restore (this wi): flips once the FIRST session-list load settles
  // (success or failure). Until then `allSessions` is just the initial empty
  // state, and reporting it to the shell would make "loaded but empty"
  // indistinguishable from "not yet loaded" for the shell's restore gating.
  const [sessionsLoadSettled, setSessionsLoadSettled] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // Manual-refresh button feedback (pi#69): the forced rescan passes
  // showLoading=false so the list-replacing `loading` state above stays out
  // of it — a spinner on the button icon is the only visual signal a click
  // landed, especially when the disk content turns out unchanged. The ref is
  // the double-click guard (checked synchronously in the click handler); the
  // state drives the spin animation and is always cleared in `finally`.
  const [sidebarRefreshing, setSidebarRefreshing] = useState(false);
  const sidebarRefreshInFlightRef = useRef(false);
  // Pull-to-refresh tracking: fires the force scan when the list is pulled
  // down past PULL_TO_REFRESH_THRESHOLD_PX while already scrolled to the top.
  const pullStartYRef = useRef<number | null>(null);
  const pullFiredRef = useRef(false);
  const [selectedCwd, setSelectedCwd] = useState<string | null>(null);
  // pi#14: the file explorer's trailing section. Written ONLY on explicit
  // workspace-selector actions (dropdown row select, custom-path commit,
  // default-directory, and the one-shot initial auto-select/URL restore) —
  // never on session clicks, pane-focus prop sync, worktree switches or
  // pinned-group [+], so the explorer stays decoupled from the session-driven
  // selectedCwd.
  const [explorerSelection, setExplorerSelection] = useState<ProjectSelection | null>(null);
  const [homeDir, setHomeDir] = useState<string>("");
  // Worker-session filter (wi pi#49 R1): patterns + reveal toggle, read once
  // per mount (the Settings dialog owns edits; a reload picks them up).
  // The lazy initializers are hydration-safe: the rendered session list is
  // empty on both server render and first client render (it fills only
  // after the first /api/sessions response lands client-side), so a stored
  // preference can never cause a hydration mismatch.
  // Session filter (code review): a LIVE subscription to the shared
  // session-filter store — editing rules or the reveal toggle in Settings
  // updates the rendered sidebar immediately, without a remount.
  // getServerSnapshot: the app server-renders client components — without a
  // stable server snapshot React 19 SSR throws "Missing getServerSnapshot".
  // The server never reads storage: defaults only (hydration-safe, and the
  // client snapshot replaces it after hydration).
  const sessionFilter = useSyncExternalStore(subscribeSessionFilter, getSessionFilterState, getServerSessionFilterState);
  const sessionFilterPatterns = sessionFilter.patterns;
  const showFilteredSessions = sessionFilter.showFiltered;
  const sessionAgeFilterDays = sessionFilter.ageFilterDays;
  // Server-side full-text filter matching (review r1 on the list payload
  // slim-down): the LIST payload carries only a firstMessage preview, so a
  // pattern whose only match sits beyond the preview boundary is matched
  // server-side against the reader's cached FULL text. The result is keyed by
  // the pattern list it was computed for, so an in-flight response from older
  // patterns is never applied to newer ones (it just reads as empty). Local
  // matching still runs first — names and short previews never need the
  // round-trip, and the server set only ever ADDS hidden rows.
  const sessionFilterPatternsKey = sessionFilterPatterns.join("\n");
  const [serverFilterMatch, setServerFilterMatch] = useState<{ key: string; ids: ReadonlySet<string> }>(() => ({
    key: "",
    ids: new Set<string>(),
  }));
  // review r2: re-run when the catalogue itself changes, not only when the
  // pattern list changes — a newly-arrived session whose only match sits
  // beyond the preview boundary must not stay visible until the user edits
  // the filter or reloads. `allSessions.length` is a cheap, stable proxy for
  // "the catalogue changed" (a session added or removed); it intentionally
  // ignores in-place field updates on existing rows, which cannot introduce a
  // NEW server-only match for an already-scanned session.
  const allSessionsCount = allSessions.length;
  useEffect(() => {
    if (showFilteredSessions || sessionFilterPatterns.length === 0) return;
    const controller = new AbortController();
    // Debounced so typing a pattern in Settings does not fetch per keystroke.
    const timer = setTimeout(() => {
      fetch(`/api/sessions/filter-match?patterns=${encodeURIComponent(JSON.stringify(sessionFilterPatterns))}`, {
        signal: controller.signal,
      })
        .then((response) => (response.ok ? response.json() : { matchedIds: [] }))
        .then((payload: { matchedIds?: unknown }) => {
          const matchedIds = Array.isArray(payload?.matchedIds)
            ? payload.matchedIds.filter((id): id is string => typeof id === "string")
            : [];
          setServerFilterMatch({ key: sessionFilterPatternsKey, ids: new Set(matchedIds) });
        })
        .catch(() => {
          // Offline/failed fetch: local matching still applies; the next
          // pattern change or sidebar reload retries.
        });
    }, 150);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [sessionFilterPatterns, sessionFilterPatternsKey, showFilteredSessions, allSessionsCount]);
  const serverFilterMatchedIds = serverFilterMatch.key === sessionFilterPatternsKey
    ? serverFilterMatch.ids
    : EMPTY_SERVER_FILTER_MATCH;
  // Pinned projects: the store re-reads localStorage on every call, so a
  // revision counter is all the React state we need — bump it after each
  // pin/unpin and rows move immediately without a reload.
  const [pinnedRevision, setPinnedRevision] = useState(0);
  // Pinned roots confirmed missing on disk (greyed rows). Checked at most
  // once per root per sidebar mount; entries are never auto-unpinned.
  const [stalePinnedRoots, setStalePinnedRoots] = useState<ReadonlySet<string>>(() => new Set());
  const checkedPinnedRootsRef = useRef<Set<string>>(new Set());
  // Which pinned groups are expanded. Starts empty so the server prerender
  // and the first client render agree (hydration), then restores the persisted
  // state after mount — the same pattern as the explorerOpen preference.
  const [expandedGroupKeys, setExpandedGroupKeys] = useState<ReadonlySet<string>>(() => new Set());
  // Pinned-group pagination (wi pi#101): per-group rows shown past
  // PINNED_VISIBLE_LIMIT. Component state only — the pin order and the
  // expansion state already persist (lib/pinned-expansion.ts), and this
  // transient view state deliberately does not. An absent key = the limit.
  const [pinnedShownExtra, setPinnedShownExtra] = useState<Record<string, number>>({});
  // Drag-to-reorder (wi B2): the dragged group's key and the header currently
  // under it. Component state only — the ORDER itself persists through
  // lib/pinned-group-order; these two just drive the live drag affordance.
  const [dragGroupKey, setDragGroupKey] = useState<string | null>(null);
  const [dropTargetGroupKey, setDropTargetGroupKey] = useState<string | null>(null);
  // "Show more" for a group: reveal SHOW_MORE_STEP more rows (uncapped —
  // the row model slices to what exists; overshoot is what flips the footer
  // to "show less").
  const handlePinnedGroupShowMore = useCallback((groupKey: string, current: number) => {
    setPinnedShownExtra((prev) => ({ ...prev, [groupKey]: current + SHOW_MORE_STEP }));
  }, []);
  // "Show less": back to the base limit (delete the entry).
  const handlePinnedGroupShowLess = useCallback((groupKey: string) => {
    setPinnedShownExtra((prev) => {
      if (!Object.hasOwn(prev, groupKey)) return prev;
      const next = { ...prev };
      delete next[groupKey];
      return next;
    });
  }, []);
  // Pinned groups (and their expansion state) come from localStorage, which is
  // unavailable during server rendering: the sidebar starts in its pre-feature
  // layout and hydrates the groups after mount so users with pins do not hit a
  // React hydration mismatch on a hard reload.
  const [sidebarHydrated, setSidebarHydrated] = useState(false);
  useEffect(() => {
    setSidebarHydrated(true);
    setExpandedGroupKeys(readExpandedGroupKeys());
  }, []);
  // Accordion: expanding a pinned group makes it the ONLY expanded group —
  // every other pinned group collapses. The single-key set is persisted
  // as-is, so legacy multi-key storage written by the pre-accordion version
  // simply collapses on the first expand action (no migration path).
  const expandPinnedGroup = useCallback((key: string) => {
    const next = new Set([key]);
    setExpandedGroupKeys(next);
    writeExpandedGroupKeys(next);
  }, []);
  // Listed-directory entries (the user-managed custom list), most-recently-
  // added first. The store persists each entry's path (and optional display
  // name), so a listed directory renders (and stays selectable) even when no
  // currently-loaded session resolves to it — and the store's one-time
  // migration seeds it from the legacy pinned-projects payload.
  // pinnedRevision is a deliberate refresh trigger: every store mutation
  // bumps it so the localStorage re-read runs even though the callback body
  // does not read it. Until sidebarHydrated the list stays empty (SSR
  // agreement). Declared early because selection handlers below need the
  // listed key set.
  const pinnedEntries = useMemo(
    () => (sidebarHydrated ? listCustomDirectories() : []),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [pinnedRevision, sidebarHydrated],
  );
  const pinnedProjects = useMemo(
    () => applyGroupOrder(
      pinnedEntries.map((entry) => ({ key: customDirectoryIdentity(entry.path), root: entry.path, displayName: entry.displayName })),
      listGroupOrder(),
    ),
    // Re-computed whenever the entries refresh (pin/unpin/rename bumps
    // pinnedRevision); listGroupOrder re-reads the persisted overlay then.
    [pinnedEntries],
  );
  // Optional per-entry display names, keyed by the same normalized identity.
  const pinnedLabelsByKey = useMemo(
    () => new Map(pinnedEntries.map((entry) => [customDirectoryIdentity(entry.path), entry.displayName])),
    [pinnedEntries],
  );
  // Drag-to-reorder (wi B2): adjacent-swap move modeled on upstream
  // adjacentProjectMove — the dragged group is removed and reinserted at
  // the drop target's index (for neighbors that is a plain swap), and the
  // full key sequence is persisted so a reload restores the order exactly.
  // The pinnedRevision bump re-runs the entries memo, which re-applies the
  // stored overlay, so the rows move immediately.
  const movePinnedGroup = useCallback((draggedKey: string, targetKey: string) => {
    if (draggedKey === targetKey) return;
    const keys = pinnedProjects.map((project) => project.key);
    const draggedIndex = keys.indexOf(draggedKey);
    const targetIndex = keys.indexOf(targetKey);
    if (draggedIndex < 0 || targetIndex < 0) return;
    keys.splice(draggedIndex, 1);
    keys.splice(targetIndex, 0, draggedKey);
    setGroupOrder(keys);
    setPinnedRevision((revision) => revision + 1);
  }, [pinnedProjects]);
  // A unpinned group leaves no stale pagination entry behind (same cleanup
  // discipline as the expansion keys).
  const pinnedGroupKeysSignature = pinnedProjects.map((project) => project.key).join("\n");
  useEffect(() => {
    setPinnedShownExtra((prev) => {
      const keys = new Set(pinnedProjects.map((project) => project.key));
      const kept = Object.entries(prev).filter(([key]) => keys.has(key));
      return kept.length === Object.keys(prev).length ? prev : Object.fromEntries(kept);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pinnedGroupKeysSignature]);
  const [customPathOpen, setCustomPathOpen] = useState(false);
  const [customPathValue, setCustomPathValue] = useState(loadLastCustomCwd);
  const [customPathError, setCustomPathError] = useState<string | null>(null);
  const [customPathValidating, setCustomPathValidating] = useState(false);
  const [validatedProject, setValidatedProject] = useState<ValidatedProject | null>(null);
  // The add-directory picker (manage mode): adds a directory to the custom
  // list, registers it as an allowed file root, and can rename/remove list
  // entries and create folders from the dialog.
  const [addDirectoryOpen, setAddDirectoryOpen] = useState(false);
  const [explorerOpen, setExplorerOpen] = useState(true);
  // Sessions | Files layout (wi B3): "stacked" is the classic split (the
  // default and today's behavior — sessions pane, drag handle, explorer
  // section); "tabs" replaces it with a two-tab switcher. Mobile viewports
  // ALWAYS use tabs (upstream's rule: stacked = desktop + stacked
  // preference). The active tab is component state, deliberately not
  // persisted (a reload returns to the sessions tab).
  //
  // State preservation: both layouts render the SAME element sequence from
  // the same JSX slots — the tab bar is one conditional slot, the sessions
  // pane and the explorer section ALWAYS render and only toggle
  // display:none, and the resize handle keeps its slot gated on stacked.
  // Nothing remounts on a layout switch (or a tab switch), so the
  // MultiRootFileExplorer instance — expanded roots, selected file, scroll
  // position — survives both. Do not wrap either pane in differently-keyed
  // or differently-positioned subtrees.
  const filesPlacement = useFilesPlacement();
  const isMobile = useIsMobile();
  const stackedSidebarLayout = !isMobile && filesPlacement === "stacked";
  // Verification rewind (mobile e2e regression, plan step-4): the repo's
  // mobile contract keeps the multi-root explorer reachable without a tab
  // click — the mobile e2e loop resolves [data-explorer-section] right after
  // opening the sidebar. So when the viewport forces tabs, the default tab
  // is "files" (Sessions stays one tap away); desktop tabs mode (the user's
  // placement choice) defaults to "sessions". An explicit tab click always
  // wins over the default, and the choice survives layout switches like any
  // other sidebar state (no remount).
  const [sidebarTabChoice, setSidebarTab] = useState<"sessions" | "files" | null>(null);
  const sidebarTab = sidebarTabChoice ?? (isMobile ? "files" : "sessions");
  const sessionsPaneHidden = !stackedSidebarLayout && sidebarTab !== "sessions";
  const filesPaneHidden = !stackedSidebarLayout && sidebarTab !== "files";
  const [explorerKey, setExplorerKey] = useState(0);
  const [explorerUploadBusy, setExplorerUploadBusy] = useState(false);
  const [fileSearchOpen, setFileSearchOpen] = useState(false);
  const [sessionSearchOpen, setSessionSearchOpen] = useState(false);
  const [sessionSearchQuery, setSessionSearchQuery] = useState("");
  const sessionSearchActive = sessionSearchOpen && Boolean(sessionSearchQuery.trim());
  const [changesCount, setChangesCount] = useState(0);
  const [changesCollapsed, setChangesCollapsed] = useState(true);
  const [explorerRefreshDone, setExplorerRefreshDone] = useState(false);
  const [fileManager, setFileManager] = useState<FileManagerAvailability | null>(null);
  const [fileManagerError, setFileManagerError] = useState<string | null>(null);
  const [runningSessionIds, setRunningSessionIds] = useState<Set<string>>(() => new Set());
  const [awaitingSessionIds, setAwaitingSessionIds] = useState<Set<string>>(() => new Set());
  const [unreadSessionIds, setUnreadSessionIds] = useState<Set<string>>(() => loadUnreadSessionIds());
  // Archived sessions (wi pi#103, sidebar A1): pure client-side state. The
  // set persists in localStorage (the unread-ids precedent) and is never
  // sent to the server — archiving/unarchiving mutates this set only, so
  // no chat tab, selection, or delete path is involved. Lazily initialized
  // SSR-safely like unread: null storage on the server reads as empty.
  const [archivedSessionIds, setArchivedSessionIds] = useState<ReadonlySet<string>>(
    () => new Set(loadArchivedSessionIds(sessionArchiveStorage())),
  );
  // claude rows keep their archive state in pi-web's index: the set mirrors
  // it, so it holds the browser's archived pi rows plus the index's archived claude rows.
  useEffect(() => {
    setArchivedSessionIds((prev) => withClaudeArchiveState(prev, allSessions));
  }, [allSessions]);
  const previousRunningSessionIdsRef = useRef<Set<string>>(new Set());
  const currentSuppressedCompletionSessionIdsRef = useRef<Set<string>>(new Set());
  const previousSuppressedCompletionSessionIdsRef = useRef<Set<string>>(new Set());
  // Once polling has delivered a snapshot it is the source of truth for
  // running state; late /api/sessions responses must not overwrite it.
  const runningPollAuthoritativeRef = useRef(false);
  // Latest selected-session external-write generation seen from the poll; a
  // rising generation means another pi process appended to the open session.
  const selectedWriteGenerationRef = useRef<{ sessionId: string; generation: number } | null>(null);
  const selectedSessionIdRef = useRef(selectedSessionId);
  selectedSessionIdRef.current = selectedSessionId;
  const onExternalSessionChangeRef = useRef(onExternalSessionChange);
  onExternalSessionChangeRef.current = onExternalSessionChange;
  const detailsHydrationTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const explorerRefreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const multiRootExplorerRef = useRef<MultiRootFileExplorerHandle>(null);

  // Virtualized session list: only the visible window of rows is mounted.
  const listScrollRef = useRef<HTMLDivElement>(null);
  const explorerScrollRef = useRef<HTMLDivElement>(null);
  useScrollbarVisibility(listScrollRef);
  useScrollbarVisibility(explorerScrollRef, explorerOpen && Boolean(selectedCwdProp || selectedCwd));
  const sessionPaneRef = useRef<HTMLDivElement>(null);
  const explorerSectionRef = useRef<HTMLDivElement>(null);
  const sessionPaneHeightRef = useRef(SESSION_PANE_DEFAULT_HEIGHT);
  const getDefaultSessionPaneHeight = useCallback(() => {
    if (!explorerOpen) return SESSION_PANE_DEFAULT_HEIGHT;
    const paneHeight = sessionPaneRef.current?.getBoundingClientRect().height;
    const explorerHeight = explorerSectionRef.current?.getBoundingClientRect().height;
    return paneHeight && explorerHeight
      ? Math.round((paneHeight + explorerHeight) / 2)
      : SESSION_PANE_DEFAULT_HEIGHT;
  }, [explorerOpen]);
  const getMaxSessionPaneHeight = useCallback(() => {
    if (!explorerOpen || !(selectedCwdProp || selectedCwd)) return SESSION_PANE_MAX_HEIGHT;
    const paneHeight = sessionPaneRef.current?.getBoundingClientRect().height ?? SESSION_PANE_DEFAULT_HEIGHT;
    const explorerHeight = explorerSectionRef.current?.getBoundingClientRect().height ?? EXPLORER_PANE_MIN_HEIGHT;
    return Math.max(
      SESSION_PANE_MIN_HEIGHT,
      paneHeight + explorerHeight - EXPLORER_PANE_MIN_HEIGHT,
    );
  }, [explorerOpen, selectedCwd, selectedCwdProp]);
  const sessionPaneResizer = useResizablePanel({
    ariaLabel: t("layout.resizeSidebarSections"),
    axis: "vertical",
    cssVariable: "--sidebar-session-pane-height",
    defaultWidth: SESSION_PANE_DEFAULT_HEIGHT,
    getDefaultWidth: getDefaultSessionPaneHeight,
    getMaxWidth: getMaxSessionPaneHeight,
    growthDirection: "down",
    maxWidth: SESSION_PANE_MAX_HEIGHT,
    minWidth: SESSION_PANE_MIN_HEIGHT,
    storageKey: "pi-web:sidebar-session-pane-height",
    widthRef: sessionPaneHeightRef,
  });
  const [listViewportH, setListViewportH] = useState(0);
  const [listScrollTop, setListScrollTop] = useState(0);
  const [focusedSessionId, setFocusedSessionId] = useState<string | null>(null);
  const listScrollRafRef = useRef<number | null>(null);
  const listScrollTopRef = useRef(0);
  const renderedListScrollTopRef = useRef(0);
  const handleListScroll = useCallback((e: React.UIEvent<HTMLDivElement>) => {
    listScrollTopRef.current = e.currentTarget.scrollTop;
    if (listScrollRafRef.current != null) return;
    listScrollRafRef.current = requestAnimationFrame(() => {
      listScrollRafRef.current = null;
      const nextTop = Math.floor(listScrollTopRef.current / SESSION_LIST_ITEM_HEIGHT) * SESSION_LIST_ITEM_HEIGHT;
      if (renderedListScrollTopRef.current === nextTop) return;
      renderedListScrollTopRef.current = nextTop;
      setListScrollTop(nextTop);
    });
  }, []);
  useLayoutEffect(() => {
    const el = listScrollRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) setListViewportH(entry.contentRect.height);
    });
    ro.observe(el);
    setListViewportH(el.clientHeight);
    listScrollTopRef.current = el.scrollTop;
    renderedListScrollTopRef.current = Math.floor(el.scrollTop / SESSION_LIST_ITEM_HEIGHT) * SESSION_LIST_ITEM_HEIGHT;
    setListScrollTop(renderedListScrollTopRef.current);
    return () => ro.disconnect();
  }, [sessionSearchActive]);

  const loadSessions = useCallback(async (showLoading = false, force = false, summary = false) => {
    const loadId = ++sessionLoadIdRef.current;
    try {
      if (showLoading) setLoading(true);
      const res = await fetch(sessionListUrl(summary, force), {
        cache: "no-store",
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json() as {
        sessions: SessionInfo[];
        sessionListVersion: number;
        runningSessionIds?: string[];
        awaitingSessionIds?: string[];
        completionNotificationSuppressedSessionIds?: string[];
      };
      if (loadId !== sessionLoadIdRef.current) return;
      sessionListVersionRef.current = data.sessionListVersion;
      setAllSessions(data.sessions);
      // Treat the fetched running set as an initial fallback only. Once the
      // lightweight poll is live, a slow session-list fetch cannot overwrite it.
      if (!runningPollAuthoritativeRef.current) {
        currentSuppressedCompletionSessionIdsRef.current = new Set(
          data.completionNotificationSuppressedSessionIds ?? [],
        );
        setRunningSessionIds(new Set(data.runningSessionIds ?? []));
        setAwaitingSessionIds(new Set(data.awaitingSessionIds ?? []));
      }
      // Drop markers for deleted sessions and for hidden family members
      // (subagents, side chats), whose completion is intentionally silent
      // even if an older client marked them unread.
      const unreadEligibleIds = new Set(
        data.sessions
          .filter((session) => !isHiddenRelation(session.relation))
          .map((session) => session.id),
      );
      setUnreadSessionIds((prev) => {
        if (prev.size === 0) return prev;
        const next = new Set([...prev].filter((id) => unreadEligibleIds.has(id)));
        return next.size === prev.size ? prev : next;
      });
      // Prune the archive set against the loaded sessions (spec: deleting an
      // archived session — from the CLI, another tab, or anywhere outside
      // this sidebar — removes it from the archive set so N sheds ghosts).
      // Same shape as the unread prune above: a no-op keeps the reference.
      const loadedSessionIds = new Set(data.sessions.map((session) => session.id));
      setArchivedSessionIds((prev) =>
        pruneArchivedSessionIds(
          prev,
          [...prev].filter((id) => !loadedSessionIds.has(id)),
        ));
      setError(null);
    } catch (e) {
      if (loadId === sessionLoadIdRef.current) setError(String(e));
    } finally {
      if (loadId === sessionLoadIdRef.current) {
        setLoading(false);
        setSessionsLoadSettled(true);
      }
    }
  }, []);

  const initialLoadDone = useRef(false);
  useEffect(() => {
    const isFirst = !initialLoadDone.current;
    initialLoadDone.current = true;
    let active = true;

    if (isFirst) {
      // Header/stat metadata is enough to select the URL session and paint the
      // sidebar. Hydrate exact counts, names, and first messages once the
      // selected chat has had a chance to start loading.
      void loadSessions(true, false, true).then(() => {
        if (!active) return;
        detailsHydrationTimerRef.current = setTimeout(() => {
          detailsHydrationTimerRef.current = null;
          if (active) void loadSessions(false, true);
        }, SESSION_DETAILS_HYDRATION_DELAY_MS);
      });
    } else {
      // Every refreshKey bump follows a change the server already invalidated
      // the list for (a run's agent_end, a fork, a delete, a name, a new
      // session). Forcing would rescan again and bump the version, so every
      // other open tab would reload the list once more.
      void loadSessions();
    }

    return () => {
      active = false;
      if (detailsHydrationTimerRef.current) {
        clearTimeout(detailsHydrationTimerRef.current);
        detailsHydrationTimerRef.current = null;
      }
    };
  }, [loadSessions, refreshKey]);

  // Browser storage is unavailable during server rendering. Restore the panel
  // preference after hydration so a collapsed explorer stays collapsed on reload.
  useEffect(() => {
    setExplorerOpen(loadExplorerOpen());
  }, []);

  // Only the server can raise a file-manager window, and only when the browser
  // runs on that same machine. Ask it once so the button can pick the right
  // label (Explorer / Finder / generic) and disable itself when unavailable.
  useEffect(() => {
    let cancelled = false;
    fetch("/api/open-in-explorer")
      .then((res) => res.ok ? res.json() as Promise<FileManagerAvailability> : null)
      .then((data) => { if (!cancelled && data) setFileManager(data); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, []);

  // A failure belongs to the project it happened on.
  useEffect(() => {
    setFileManagerError(null);
  }, [selectedCwd, selectedCwdProp]);

  const openInFileManager = useCallback(async () => {
    const dir = selectedCwd ?? selectedCwdProp;
    if (!dir) return;
    try {
      const res = await fetch("/api/open-in-explorer", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ cwd: dir }),
      });
      if (res.ok) {
        setFileManagerError(null);
        return;
      }
      const data = await res.json().catch(() => ({})) as { error?: string };
      setFileManagerError(data.error ?? `HTTP ${res.status}`);
    } catch (error) {
      setFileManagerError(error instanceof Error ? error.message : String(error));
    }
  }, [selectedCwd, selectedCwdProp]);

  const fileManagerLabel = t(
    fileManager?.platform === "darwin"
      ? "sidebar.openInFinder"
      : fileManager?.platform === "win32"
        ? "sidebar.openInExplorer"
        : "sidebar.openInFileManager",
  );
  const fileManagerUnavailable = fileManager?.supported === false;
  const fileManagerErrorMessage = fileManagerError
    ? t(FILE_MANAGER_ERROR_KEYS[fileManagerError] ?? fileManagerError)
    : null;

  // Persist unread markers so they survive a browser refresh before the user
  // has actually opened the completed session.
  useEffect(() => {
    saveUnreadSessionIds(unreadSessionIds);
  }, [unreadSessionIds]);

  // ── Archived sessions (wi pi#103, sidebar A1) ──
  // Archiving/unarchiving are local set mutations ONLY: no fetch, no tab or
  // selection change, no delete path. Every rendered-list exclusion and the
  // archive view are pure functions of this set, so unarchiving restores the
  // prior behavior exactly (the row re-enters every list it left).
  const allSessionsRef = useRef(allSessions);
  allSessionsRef.current = allSessions;
  // A claude row's archive state is written to pi-web's index instead; the row follows at once.
  const setClaudeArchived = useCallback((id: string, archived: boolean) => {
    setAllSessions((prev) => prev.map((session) => session.id === id ? { ...session, archived: archived || undefined } : session));
    void patchClaudeArchived(id, archived).catch((error) => console.error("Failed to archive the claude session:", error));
  }, []);
  const archiveSession = useCallback((id: string) => {
    if (allSessionsRef.current.some((session) => session.id === id && session.harness === "claude")) {
      setClaudeArchived(id, true);
      return;
    }
    setArchivedSessionIds((prev) => {
      if (prev.has(id)) return prev;
      const next = new Set(prev);
      next.add(id);
      return next;
    });
  }, [setClaudeArchived]);
  const unarchiveSession = useCallback((id: string) => {
    if (allSessionsRef.current.some((session) => session.id === id && session.harness === "claude")) {
      setClaudeArchived(id, false);
      return;
    }
    setArchivedSessionIds((prev) => pruneArchivedSessionIds(prev, [id]));
  }, [setClaudeArchived]);

  // Persist the archive set so it survives a browser refresh (same shape and
  // empty-key-removal rule as the unread markers above).
  useEffect(() => {
    saveArchivedSessionIds(archivedSessionIds, sessionArchiveStorage());
  }, [archivedSessionIds]);

  // Running activity forces an unarchive (spec: a session that starts
  // running again must leave the archive — capsule sync, a new prompt).
  // Keyed on the running set so it covers both newly-running sessions and
  // ones already running at snapshot time. Passive unread never triggers
  // this: only running ids are removed here.
  useEffect(() => {
    if (runningSessionIds.size === 0) return;
    setArchivedSessionIds((prev) => pruneArchivedSessionIds(prev, runningSessionIds));
  }, [runningSessionIds]);

  // Cross-tab sync (the useFontPreferences storage-event precedent): a
  // foreign tab's write reloads the set into this tab's state. The reload is
  // diff-guarded so identical values never bounce a write back (the persist
  // effect above would otherwise re-emit the same value in a loop).
  useEffect(() => {
    if (typeof window === "undefined") return;
    const onArchiveStorage = (event: StorageEvent) => {
      if (event.key !== ARCHIVED_SESSIONS_STORAGE_KEY) return;
      setArchivedSessionIds((prev) => {
        const next = new Set(loadArchivedSessionIds(sessionArchiveStorage()));
        if (next.size === prev.size && [...next].every((id) => prev.has(id))) return prev;
        return next;
      });
    };
    window.addEventListener("storage", onArchiveStorage);
    return () => window.removeEventListener("storage", onArchiveStorage);
  }, []);

  // The archived slice of the FULL universe — the archive view's rows and the
  // global 「已归档 N」 count both derive from it. Deliberately global (never
  // scoped to selectedProject): N counts every archived session across all
  // projects and stays stable across project switches.
  const archivedSessions = useMemo(
    () => archivedSessionIds.size === 0
      ? EMPTY_ARCHIVED_SESSIONS
      : allSessions.filter((session) => archivedSessionIds.has(session.id)),
    [allSessions, archivedSessionIds],
  );
  // The view's grouping: one group per project root, freshest project first
  // (lib/session-archive.ts — pure, so the grouping is unit-testable without
  // React). Labels reuse the pinned-group convention (displayCwd, full path
  // in title) so each project's directory name is visible.
  const archivedGroups = useMemo(
    () => groupArchivedSessionsByProject(archivedSessions),
    [archivedSessions],
  );

  // Archive view (wi pi#103): open state of the replacement view. Purely
  // presentational local state — the view's contents are a pure function of
  // the archive set, so opening/closing never touches persistence.
  const [archiveViewOpen, setArchiveViewOpen] = useState(false);
  // Auto-return: when the last archived session leaves the set while the
  // view is open (unarchived one by one, pruned externally, force-unarchived
  // by running activity), the view has nothing left to show — it closes and
  // the normal sessions list restores exactly.
  useEffect(() => {
    if (archiveViewOpen && archivedSessions.length === 0) setArchiveViewOpen(false);
  }, [archiveViewOpen, archivedSessions.length]);

  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let controller: AbortController | null = null;

    const clearTimer = () => {
      if (timer) clearTimeout(timer);
      timer = null;
    };

    const schedule = () => {
      clearTimer();
      if (stopped || document.visibilityState !== "visible") return;
      timer = setTimeout(() => void poll(), RUNNING_SESSIONS_POLL_MS);
    };

    const poll = async () => {
      if (stopped || document.visibilityState !== "visible") return;
      const current = new AbortController();
      controller?.abort();
      controller = current;
      try {
        const res = await fetch("/api/agent/running", {
          cache: "no-store",
          signal: current.signal,
        });
        if (!res.ok) return;
        const data = await res.json() as {
          sessionListVersion: number;
          runningSessionIds?: string[];
          awaitingSessionIds?: string[];
          completionNotificationSuppressedSessionIds?: string[];
          recentSessionWrites?: { sessionId?: string; path: string; generation: number }[];
        };
        if (stopped || controller !== current) return;
        runningPollAuthoritativeRef.current = true;
        currentSuppressedCompletionSessionIdsRef.current = new Set(
          data.completionNotificationSuppressedSessionIds ?? [],
        );
        setRunningSessionIds(new Set(data.runningSessionIds ?? []));
        setAwaitingSessionIds(new Set(data.awaitingSessionIds ?? []));
        // Detect that the currently selected session was written by another
        // pi process (TUI / another pi-web window). A session already in the
        // ring when it is selected is baselined at that generation, so merely
        // selecting it reads nothing. A session absent from the ring at
        // selection is baselined at 0, so its first write is a rise and is
        // notified: without that baseline the first sighting swallowed it.
        const selectedId = selectedSessionIdRef.current;
        const selectedWrite = (data.recentSessionWrites ?? []).find(
          (write) => write.sessionId !== undefined && write.sessionId === selectedId,
        );
        if (selectedId && selectedWrite) {
          const previous = selectedWriteGenerationRef.current;
          if (
            previous
            && previous.sessionId === selectedId
            && selectedWrite.generation > previous.generation
          ) {
            onExternalSessionChangeRef.current?.(selectedId);
          }
          selectedWriteGenerationRef.current = {
            sessionId: selectedId,
            generation: selectedWrite.generation,
          };
        } else if (selectedId && selectedWriteGenerationRef.current?.sessionId !== selectedId) {
          selectedWriteGenerationRef.current = { sessionId: selectedId, generation: 0 };
        }
        if (data.sessionListVersion !== sessionListVersionRef.current) {
          // Reuse the invalidated cache; forcing a scan would change the version again.
          await loadSessions();
        }
      } catch {
        // Keep the last known state; the next visible-tab poll retries.
      } finally {
        if (controller === current) controller = null;
        schedule();
      }
    };

    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        void poll();
        return;
      }
      clearTimer();
      controller?.abort();
      controller = null;
    };

    void poll();
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      stopped = true;
      clearTimer();
      controller?.abort();
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [loadSessions]);

  useEffect(() => {
    onRunningSessionIdsChange?.(runningSessionIds);
  }, [onRunningSessionIdsChange, runningSessionIds]);

  useEffect(() => {
    // Report only settled lists: the pre-load mount emission of the initial
    // empty state is a no-op over the shell's own initial state, and the
    // shell's pane-tab restore gates on "the live catalog has settled".
    if (!sessionsLoadSettled) return;
    onSessionsChange?.(allSessions);
  }, [allSessions, onSessionsChange, sessionsLoadSettled]);

  // The tab strip's favicon mirrors what the rows show (#1483): a persistent
  // badge after the completion sound fades, cleared the same way the blue dot is.
  // Sessions that finished while the tab was hidden count too (#1484): the rows
  // exclude the selected session ("already looking"), which is false while hidden.
  useEffect(() => {
    syncFaviconBadge({
      awaiting: awaitingSessionIds.size,
      unread: countFaviconUnread(unreadSessionIds, hiddenFinishedSessionIds),
    });
  }, [awaitingSessionIds, unreadSessionIds, hiddenFinishedSessionIds]);

  useEffect(() => {
    const previous = previousRunningSessionIdsRef.current;
    const completedInBackground = [...previous].filter((id) => !runningSessionIds.has(id) && id !== selectedSessionId);
    const knownSubagentIds = new Set(
      allSessions
        .filter((session) => isHiddenRelation(session.relation))
        .map((session) => session.id),
    );
    const completedWithNotifications = completedInBackground.filter(
      (id) => !previousSuppressedCompletionSessionIdsRef.current.has(id) && !knownSubagentIds.has(id),
    );
    const newlyRunning = [...runningSessionIds].filter((id) => !previous.has(id));

    if (completedWithNotifications.length > 0 || newlyRunning.length > 0) {
      setUnreadSessionIds((prev) => {
        const next = new Set(prev);
        runningSessionIds.forEach((id) => next.delete(id));
        completedWithNotifications.forEach((id) => next.add(id));
        return next;
      });
    }
    const hasUnlistedRunningSession = newlyRunning.some(
      (id) => !allSessions.some((session) => session.id === id),
    );
    // Not forced: a run's agent_end has already invalidated the server's list,
    // and a forced rescan here, in every tab not showing that session, bumped
    // the version again and made all the others reload once more.
    if (completedInBackground.length > 0 || hasUnlistedRunningSession) {
      void loadSessions();
    }
    if (completedWithNotifications.length > 0) {
      onBackgroundTaskDone?.();
    }

    previousRunningSessionIdsRef.current = runningSessionIds;
    previousSuppressedCompletionSessionIdsRef.current = new Set(
      [...runningSessionIds].filter(
        (id) => currentSuppressedCompletionSessionIdsRef.current.has(id) || knownSubagentIds.has(id),
      ),
    );
  }, [runningSessionIds, selectedSessionId, allSessions, loadSessions, onBackgroundTaskDone]);

  useEffect(() => {
    if (!selectedSessionId) return;
    setUnreadSessionIds((prev) => {
      if (!prev.has(selectedSessionId)) return prev;
      const next = new Set(prev);
      next.delete(selectedSessionId);
      return next;
    });
  }, [selectedSessionId]);

  useEffect(() => {
    if (explorerRefreshKey !== undefined) setExplorerKey((k) => k + 1);
  }, [explorerRefreshKey]);

  useEffect(() => {
    fetch("/api/home").then((r) => r.json()).then((d: { home?: string }) => {
      if (d.home) setHomeDir(d.home);
    }).catch(() => {});
  }, []);

  const restoredRef = useRef(false);

  const projectSelection = useCallback((root: string, key: string): ProjectSelection => ({
    root,
    key,
  }), []);

  /** Resolve both display root and stable identity from server-provided data. */
  const projectFor = useCallback((cwd: string | null): ProjectSelection | null => {
    if (!cwd) return null;
    // /api/cwd/validate resolves identity before a custom path becomes active,
    // preventing one render with a raw path key from looking like a switch.
    if (validatedProject?.cwd === cwd) {
      return projectSelection(validatedProject.root, validatedProject.key);
    }
    const match = allSessions.find((session) => (
      session.cwd === cwd || (session.projectRoot ?? session.cwd) === cwd
    ));
    return match
      ? projectSelection(match.projectRoot ?? match.cwd, workspaceKeyOf(match))
      : projectSelection(cwd, cwd);
  }, [validatedProject, allSessions, projectSelection]);

  // Accordion selection switch: when a selection moves the effective cwd
  // into a listed directory, that directory's group becomes the expanded one
  // — the previously expanded group collapses. Selecting within the currently
  // expanded group re-applies the same single-key set (no visible change);
  // unlisted targets are left alone. A worktree cwd resolves through the
  // session's server-provided projectRoot, because linked worktrees live
  // OUTSIDE their repository directory.
  const listedEntryForPath = useCallback((path: string | null | undefined): SidebarProject | null => {
    if (!path) return null;
    let best: SidebarProject | null = null;
    for (const project of pinnedProjects) {
      if (isPathInsideDirectory(project.root, path)
        && (!best || project.root.length > best.root.length)) {
        best = project;
      }
    }
    return best;
  }, [pinnedProjects]);
  const expandPinnedGroupForCwd = useCallback((cwd: string | null, projectRoot?: string | null) => {
    const entry = listedEntryForPath(cwd) ?? listedEntryForPath(projectRoot ?? null);
    if (!entry) return;
    expandPinnedGroup(entry.key);
  }, [listedEntryForPath, expandPinnedGroup]);

  // A worktree/session refresh can hydrate the stable key without changing
  // cwd, so notify when either changes. The parent treats same-cwd key changes
  // as identity hydration rather than a workspace switch.
  const lastNotifiedProjectRef = useRef<{ cwd: string | null; key: string | null } | null>(null);
  useEffect(() => {
    const project = projectFor(selectedCwd);
    const previous = lastNotifiedProjectRef.current;
    if (previous?.cwd === selectedCwd && previous.key === (project?.key ?? null)) return;
    lastNotifiedProjectRef.current = { cwd: selectedCwd, key: project?.key ?? null };
    onCwdChange?.(
      selectedCwd,
      project?.root ?? null,
      project?.key ?? null,
    );
  }, [selectedCwd, onCwdChange, projectFor]);

  // Sync the effective cwd to the focused session's cwd (prop). Sessions of
  // all worktrees in a project share one list, so clicking a session from
  // another worktree moves the effective cwd there. Only fires when the prop
  // value changes.
  const lastSyncedCwdPropRef = useRef<string | null>(null);
  useEffect(() => {
    if (selectedCwdProp && selectedCwdProp !== lastSyncedCwdPropRef.current) {
      lastSyncedCwdPropRef.current = selectedCwdProp;
      setSelectedCwd(selectedCwdProp);
    }
  }, [selectedCwdProp]);

  // Auto-select cwd and restore session from URL on first load
  useEffect(() => {
    if (allSessions.length === 0 || skipInitialProjectSelection) return;

    if (selectedCwd === null) {
      // If restoring a session, set cwd to match that session
      if (initialSessionId && !restoredRef.current) {
        restoredRef.current = true;
        const target = allSessions.find((s) => s.id === initialSessionId);
        if (target) {
          setSelectedCwd(target.cwd);
          // One-shot initial selection (URL restore): the restored session's
          // project becomes the explorer's trailing section — resolved to the
          // project root, never a worktree cwd.
          setExplorerSelection({ root: target.projectRoot ?? target.cwd, key: workspaceKeyOf(target) });
          onSelectSession(target, true);
          return;
        }
        // Session not found — notify parent so it can show the placeholder
        onInitialRestoreDone?.();
      }
      const projects = getRecentProjects(allSessions);
      if (projects.length > 0) {
        setSelectedCwd(projects[0].root);
        // One-shot initial auto-select: the most recent project becomes the
        // explorer's trailing section on load.
        setExplorerSelection(projects[0]);
      }
    }
  }, [allSessions, selectedCwd, initialSessionId, skipInitialProjectSelection, onSelectSession, onInitialRestoreDone]);

  const commitCustomPath = useCallback(async (candidate?: string) => {
    const path = (candidate ?? customPathValue).trim();
    if (!path || customPathValidating) return;

    setCustomPathValidating(true);
    setCustomPathError(null);
    try {
      const res = await fetch("/api/cwd/validate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ cwd: path }),
      });
      const data = await res.json().catch(() => ({})) as {
        cwd?: string;
        projectRoot?: string;
        projectKey?: string;
        error?: string;
      };
      if (!res.ok || data.error || !data.cwd || !data.projectRoot || !data.projectKey) {
        setCustomPathError(data.error ?? `HTTP ${res.status}`);
        return;
      }
      setValidatedProject({
        cwd: data.cwd,
        root: data.projectRoot,
        key: data.projectKey,
      });
      saveLastCustomCwd(data.cwd);
      setCustomPathValue(data.cwd);
      setSelectedCwd(data.cwd);
      // Custom-path commit is an explicit workspace-selector action: the
      // validated project identity becomes the explorer's trailing section.
      setExplorerSelection({ root: data.projectRoot, key: data.projectKey });
      setCustomPathOpen(false);
    } catch (e) {
      setCustomPathError(e instanceof Error ? e.message : String(e));
    } finally {
      setCustomPathValidating(false);
    }
  }, [customPathValue, customPathValidating]);

  // Clicking a session moves the effective cwd to that session's worktree.
  // Done on the click path (not via the selectedCwd prop sync) so it also
  // works when the prop value won't change — e.g. re-clicking the already
  // open session after manually switching worktrees.
  const handleSelectSessionFromList = useCallback((s: SessionInfo, entryId?: string, blockIndex?: number) => {
    setAllSessions((current) => current.some((session) => session.id === s.id) ? current : [s, ...current]);
    if (s.cwd) setSelectedCwd(s.cwd);
    // Accordion: selecting a session that resolves into a listed directory
    // other than the expanded one switches the expanded group to it.
    expandPinnedGroupForCwd(s.cwd, s.projectRoot ?? null);
    onSelectSession(s, false, entryId, blockIndex);
  }, [onSelectSession, expandPinnedGroupForCwd]);

  // Toggle one pinned group's expansion; selection and cwd stay untouched.
  // Accordion: expanding makes the group the only expanded one (every other
  // pinned group collapses); collapsing is independent — it removes just
  // the toggled key and never expands another group. The new state is
  // persisted so a reload restores it.
  const handleToggleGroup = useCallback((key: string) => {
    if (expandedGroupKeys.has(key)) {
      const next = new Set(expandedGroupKeys);
      next.delete(key);
      setExpandedGroupKeys(next);
      writeExpandedGroupKeys(next);
      return;
    }
    expandPinnedGroup(key);
  }, [expandedGroupKeys, expandPinnedGroup]);

  // Group [+] starts a session rooted at the group's display root and moves
  // the effective cwd there (mirroring a dropdown row select) so subsequent
  // sidebar actions target that root. The group also expands so the new
  // session's row is visible (spec R2) — accordion-style: expanding it
  // collapses every other pinned group, and when it is already the expanded
  // group this is a no-op. Disabled for stale roots.
  const handleNewSessionInProject = useCallback((project: SidebarProject) => {
    if (stalePinnedRoots.has(project.root)) return;
    setSelectedCwd(project.root);
    expandPinnedGroup(project.key);
    onNewSession?.(newTempSessionId(), project.root);
  }, [stalePinnedRoots, onNewSession, expandPinnedGroup]);

  // pi#14 explorer section roots: every listed directory (list order) plus
  // the workspace selector's current selection as the trailing section. A
  // selection that resolves into a listed directory reuses that entry's
  // normalized key so identity dedupe keeps one section per directory (the
  // old key-identity dedupe now happens at this call site, because the
  // selection's stable workspace key and the entry's normalized path key
  // are different identities). Store mutations bump pinnedRevision, so a
  // removed entry's section disappears immediately with the other sections'
  // expansion state intact.
  const explorerRoots = useMemo(
    () => {
      const owning = explorerSelection ? listedEntryForPath(explorerSelection.root) : null;
      return buildExplorerRoots(pinnedProjects, owning ?? explorerSelection);
    },
    [pinnedProjects, explorerSelection, listedEntryForPath],
  );
  // Pin/unpin now operate on the custom directory store, leaving exactly
  // one user-managed list: pin adds the project root at the head, unpin
  // removes the entry (a list operation only — the disk is untouched).
  const togglePin = useCallback((root: string) => {
    if (isCustomDirectoryListed(root)) removeCustomDirectory(root);
    else addCustomDirectory(root);
    setPinnedRevision((revision) => revision + 1);
  }, []);

  // Adding a directory from the picker: append to the custom store and
  // register it as an allowed file root through the same /api/cwd/validate
  // integration the custom-path commit uses, so its files are browsable
  // immediately. Idempotent for an already-listed directory (the store moves
  // it to the head, no duplicate entry). Cancelling the dialog changes
  // nothing — only an explicit select lands here.
  const handleAddDirectory = useCallback(async (path: string) => {
    setAddDirectoryOpen(false);
    addCustomDirectory(path);
    setPinnedRevision((revision) => revision + 1);
    // A newly added directory expands immediately AND scrolls into view:
    // the list renders most-recently-added first (offset 0), so a scrolled
    // viewport would window the new group's header out — scroll to top so
    // the "New session in …" affordance is reachable without a second
    // interaction.
    expandPinnedGroup(customDirectoryIdentity(path));
    listScrollRef.current?.scrollTo({ top: 0 });
    try {
      await fetch("/api/cwd/validate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ cwd: path }),
      });
    } catch {
      // Best-effort: an offline failure defers root registration to the
      // next validate/commit that touches this directory.
    }
  }, [expandPinnedGroup]);

  // Per-row pin flow for the add-directory picker (wi pi#47): validate
  // BEFORE add, so a failed /api/cwd/validate surfaces a typed error in the
  // picker and mutates NOTHING. A successful pin registers the directory as
  // an allowed file root, adds it to the custom store (idempotent
  // head-of-list), bumps the revision notification and expands/scrolls the
  // new group. Passed ONLY to the addDirectoryOpen manage picker — the
  // plain customPath picker stays select-and-close with no pin.
  const pinDirectory = useMemo(
    () => createDirectoryPinFlow({
      // The sidebar OWNS the store: the mutation is injected explicitly, the
      // flow helper has no production default write (review blocker, pi#47).
      add: (path: string) => addCustomDirectory(path),
      onAdded: (path: string) => {
        setPinnedRevision((revision) => revision + 1);
        expandPinnedGroup(customDirectoryIdentity(path));
        listScrollRef.current?.scrollTo({ top: 0 });
      },
    }),
    [expandPinnedGroup],
  );

  // Manage-mode row seams (wi pi#52): the picker's per-row delete and inline
  // path rename run through the SAME injectable-callback factories the
  // behavioral tests drive. The sidebar OWNS the store (mutations injected,
  // no production default writes in the seams). Delete carries the
  // last-entry guard and discards the removed group's expansion key (both
  // the persisted set and the in-memory accordion state, so no stale
  // reference survives); rename delegates to the store's path-edit
  // primitive. The refusal reason is read from the returned outcome —
  // onError exists for consumers that prefer push notification.
  const rowDeleteHandler = useMemo(
    () => createRowDeleteHandler({
      list: () => listCustomDirectories(),
      remove: (path: string) => removeCustomDirectory(path),
      discardExpandedKey: (key: string) => {
        discardExpandedGroupKey(key);
        setExpandedGroupKeys((previous) => {
          if (!previous.has(key)) return previous;
          const next = new Set(previous);
          next.delete(key);
          return next;
        });
      },
      onError: () => {
        // The typed reason travels on the returned outcome; nothing else
        // to notify in the sidebar itself.
      },
    }),
    [],
  );
  const rowPathRenameHandler = useMemo(
    () => createRowPathRenameHandler({
      renamePath: (currentPath: string, nextPath: string) =>
        renameCustomDirectoryPath(currentPath, nextPath),
      onError: () => {
        // Same as above: the returned outcome carries the typed reason.
      },
    }),
    [],
  );

  // Stale pinned roots: on sidebar mount (and whenever the pinned set
  // changes), ask the server whether each pinned display root still exists
  // on disk. Roots that are gone render greyed (and are never
  // auto-unpinned); a failed check (offline, server hiccup) leaves the group
  // alone rather than greying it on a guess. The roots-key dependency keeps
  // this from re-running per render.
  const pinnedRootsKey = pinnedProjects.map((project) => project.root).join("\n");
  useEffect(() => {
    if (!pinnedRootsKey) return;
    const pending = pinnedRootsKey
      .split("\n")
      .filter((root) => root && !checkedPinnedRootsRef.current.has(root));
    if (pending.length === 0) return;
    for (const root of pending) checkedPinnedRootsRef.current.add(root);
    let cancelled = false;
    void (async () => {
      const results = await Promise.all(pending.map(async (root) => {
        try {
          const response = await fetch("/api/cwd/validate", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ cwd: root }),
          });
          return { root, exists: response.ok };
        } catch {
          return { root, exists: true };
        }
      }));
      if (cancelled) return;
      setStalePinnedRoots((previous) => {
        let changed = false;
        const next = new Set(previous);
        for (const { root, exists } of results) {
          if (!exists && !next.has(root)) { next.add(root); changed = true; }
          if (exists && next.has(root)) { next.delete(root); changed = true; }
        }
        return changed ? next : previous;
      });
      // Roots that are currently missing stay eligible for re-checking, so a
      // directory that comes back un-greys on the next check.
      for (const { root, exists } of results) {
        if (!exists) checkedPinnedRootsRef.current.delete(root);
      }
    })();
    return () => { cancelled = true; };
  }, [pinnedRootsKey]);

  // Sessions of every worktree in the selected project are shown together
  const selectedProject = useMemo(() => projectFor(selectedCwd), [projectFor, selectedCwd]);

  // On load, the selected directory's group starts expanded; other groups
  // start collapsed unless its persisted state says otherwise. Fires once,
  // as soon as the selection resolves into a listed directory.
  const autoExpandedGroupRef = useRef(false);
  useEffect(() => {
    if (autoExpandedGroupRef.current) return;
    const entry = listedEntryForPath(selectedCwd)
      ?? listedEntryForPath(selectedProject?.root ?? null);
    if (!entry) return;
    autoExpandedGroupRef.current = true;
    // Accordion: the auto-expand replaces the persisted set, so a persisted
    // different group (or legacy multi-key storage) collapses to this one.
    expandPinnedGroup(entry.key);
    // expandedGroupKeys is deliberately excluded: the ref guard makes this a
    // one-shot effect and reading it here would restart on every toggle.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedCwd, selectedProject, listedEntryForPath]);

  // Worker-session filter (wi pi#49 R1): sessions matching any pattern (a
  // case-insensitive substring hit on name OR firstMessage) are hidden from
  // every RENDERED list before grouping — the main list and the pinned
  // groups both derive from visibleSessions. Local matching covers the
  // stored name and the first-message PREVIEW the list payload carries;
  // matches beyond the preview come from the server-side full-text match
  // (serverFilterMatchedIds), keeping the filter's original full-text
  // semantics without restoring the oversized payload. Non-render concerns
  // (background-completion notifications, unread/running bookkeeping,
  // initial-project restore, recent-project selection) keep operating on
  // allSessions. The reveal toggle disables the filtering entirely, so a
  // re-revealed session renders with no visual difference.
  // pi#82: the age window composes AFTER the worker filter. day-granularity
  // means a stale `now` inside a cached memo never moves a boundary visibly;
  // the memo recomputes whenever the list or the setting changes.
  // pi#103: the archived-exclusion composes FIRST — ahead of the worker
  // filter/reveal toggle and the age window — so an archived id never
  // renders regardless of the reveal toggle: a re-revealed worker session
  // renders, an archived one never does. allSessions itself stays the full
  // universe (the archive view, the search badge, the prune, and
  // unread/running bookkeeping all need it).
  const unarchivedSessions = useMemo(
    () => archivedSessionIds.size === 0
      ? allSessions
      : allSessions.filter((session) => !archivedSessionIds.has(session.id)),
    [allSessions, archivedSessionIds],
  );
  const visibleSessions = useMemo(
    () => filterSessionsByAge(
      showFilteredSessions || sessionFilterPatterns.length === 0
        ? unarchivedSessions
        : unarchivedSessions.filter((session) => (
          !isSessionFiltered(session, sessionFilterPatterns)
          && !serverFilterMatchedIds.has(session.id)
        )),
      sessionAgeFilterDays,
    ),
    [unarchivedSessions, showFilteredSessions, sessionFilterPatterns, serverFilterMatchedIds, sessionAgeFilterDays],
  );
  // Hidden-count affordance (wi pi#65): the exact number of sessions the
  // active patterns hide, computed over allSessions MINUS archived ids
  // (pi#103) and only inside the age window (a session the
  // day filter drops is not revealed by the badge, so it is not counted;
  // 0 days counts every session) — independent of the reveal toggle so the
  // badge stays live in both states (clicking it flips the toggle through
  // the shared store, so Settings updates too). Zero patterns (hiding
  // disabled) reads as 0 and renders no badge. Archived sessions never
  // count as hidden.
  const hiddenSessionCount = useMemo(
    () => sessionFilterPatterns.length > 0
      ? countFilteredSessions(unarchivedSessions, sessionFilterPatterns, sessionAgeFilterDays)
      : 0,
    [unarchivedSessions, sessionFilterPatterns, sessionAgeFilterDays],
  );
  // Reveal-mode marking (wi pi#65): ids of exactly the sessions the
  // patterns hide. Computed only when the reveal toggle is ON and patterns
  // exist; null otherwise so the marking stays off and rows render exactly
  // as before. A family row is marked only when its ROOT is in the set —
  // the rendered title, click target and identity are the root's, so a
  // subagent-only match must not label a non-hidden root row "filtered"
  // (review r1 P2: family-wide matching falsely marked non-matching roots).
  const filteredSessionIds = useMemo(
    () => showFilteredSessions && sessionFilterPatterns.length > 0
      ? computeFilteredSessionIds(allSessions, sessionFilterPatterns)
      : null,
    [allSessions, showFilteredSessions, sessionFilterPatterns],
  );
  // The main list below the listed groups keeps its existing filtering
  // rules, minus the sessions grouped under a listed directory — those
  // render only inside their group, so a listed directory's sessions never
  // appear twice. Recomputed per render, exactly like the pre-group session
  // list was. NOTE (pi#56 merge): filteredSessions keeps OUR visibleSessions
  // (session-filter) source, not upstream's allSessions — the upstream view
  // cache feeds allSessions upstream of the filter.
  const filteredSessions = selectedProject
    ? sessionsForProject(visibleSessions, selectedProject.key)
    : visibleSessions;
  // Session families per listed directory. sessionsForDirectory groups by
  // path containment (plus the server-resolved project root), so sessions
  // in any git worktree of a listed repository and sessions in non-git
  // directories both land under their listed entry — no workspace-key,
  // pseudo-project or worktree-specific filtering.
  const familiesByProject = new Map<string, SessionFamily[]>();
  const groupedSessionIds = new Set<string>();
  for (const project of pinnedProjects) {
    const families = listSessionFamilies(sessionsForDirectory(visibleSessions, project.root));
    familiesByProject.set(project.key, families);
    for (const family of families) {
      groupedSessionIds.add(family.root.id);
      for (const subagent of family.subagents) groupedSessionIds.add(subagent.id);
    }
  }
  const mainFamilies = listSessionFamilies(
    filteredSessions.filter((session) => !groupedSessionIds.has(session.id)),
  );
  // Per-group activity counts (running / unread), aggregated over the
  // directory's grouped sessions and keyed by the entry's normalized path
  // identity — the group keys, not the workspace keys the dropdown uses.
  const groupActivity = new Map<string, { running: number; unread: number }>();
  for (const project of pinnedProjects) {
    let running = 0;
    let unread = 0;
    for (const family of familiesByProject.get(project.key) ?? []) {
      for (const session of [family.root, ...family.subagents]) {
        if (runningSessionIds.has(session.id)) running += 1;
        if (unreadSessionIds.has(session.id)) unread += 1;
      }
    }
    groupActivity.set(project.key, { running, unread });
  }
  // Drag-to-reorder wiring for one pinned group header (wi B2): only idle
  // groups drag (running guard from groupActivity); any group — running
  // included — may be the drop target. One dragged group at a time; the
  // drop applies the adjacent-swap move and persists the full order.
  // Keyboard reorder is deferred to the backlog per the work item.
  const pinnedGroupDragProps = (project: SidebarProject): PinnedGroupReorderProps => {
    const running = (groupActivity.get(project.key)?.running ?? 0) > 0;
    const isDropTarget = dragGroupKey != null && dropTargetGroupKey === project.key && dragGroupKey !== project.key;
    return {
      draggable: !running,
      isDropTarget,
      onDragStart: (event) => {
        if (running) {
          event.preventDefault();
          return;
        }
        event.dataTransfer.setData("text/plain", project.key);
        event.dataTransfer.effectAllowed = "move";
        setDragGroupKey(project.key);
      },
      onDragOver: (event) => {
        if (dragGroupKey == null || dragGroupKey === project.key) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        if (dropTargetGroupKey !== project.key) setDropTargetGroupKey(project.key);
      },
      onDragLeave: () => {
        if (dropTargetGroupKey === project.key) setDropTargetGroupKey(null);
      },
      onDrop: (event) => {
        if (dragGroupKey == null || dragGroupKey === project.key) return;
        event.preventDefault();
        const draggedKey = dragGroupKey;
        setDragGroupKey(null);
        setDropTargetGroupKey(null);
        movePinnedGroup(draggedKey, project.key);
      },
      onDragEnd: () => {
        setDragGroupKey(null);
        setDropTargetGroupKey(null);
      },
    };
  };
  // One flat row array with cumulative offsets for the whole scroll area.
  // Pinned-group pagination (wi pi#101): groups with more families than the
  // limit get their shown count (limit + revealed extra); smaller groups and
  // absent entries render every row, so the default layout is unchanged.
  const groupVisibleCounts = new Map<string, number>();
  for (const project of pinnedProjects) {
    if ((familiesByProject.get(project.key) ?? []).length <= PINNED_VISIBLE_LIMIT) continue;
    const extra = pinnedShownExtra[project.key];
    groupVisibleCounts.set(
      project.key,
      PINNED_VISIBLE_LIMIT + (typeof extra === "number" && extra > 0 ? Math.floor(extra) : 0),
    );
  }
  const sidebarRows = buildSidebarRows({
    pinnedProjects,
    familiesByProject,
    expandedKeys: expandedGroupKeys,
    mainFamilies,
    groupVisibleCounts,
  });
  // Group rows by owning project, for the per-group content containers.
  // Headers never enter (their groupKey slot is null), so the value type
  // excludes GroupHeaderRow and the container render can narrow to
  // session / empty hint / pagination footer alone.
  const groupRowsByKey = new Map<string, (SessionRow | GroupEmptyRow | GroupMoreRow)[]>();
  for (const row of sidebarRows) {
    if (row.kind === "groupHeader") continue;
    const groupKey = row.groupKey;
    if (!groupKey) continue;
    const list = groupRowsByKey.get(groupKey);
    if (list) list.push(row);
    else groupRowsByKey.set(groupKey, [row]);
  }
  const visibleSidebarRows = getWindowedRows(sidebarRows, listScrollTop, listViewportH, focusedSessionId);

  // --- Split-view highlight follow (this wi) ---
  // When the focus-derived highlight changes while the follow gate is on,
  // reveal that session's row: expand its collapsed pinned group (accordion,
  // same single-key helper as click-driven expansion) and scroll the row
  // into view through the offset-based row model — no DOM node needed. The
  // effect fires ONLY on a highlight change: never on mount, never on
  // session-list refreshes that leave the highlight alone, so it never
  // fights the user's manual scrolling. With the gate off (classic layout,
  // mobile) there are zero new scroll/expansion side effects.
  const applyFollowScroll = useCallback((rows: readonly SidebarRow[], sessionId: string) => {
    const el = listScrollRef.current;
    if (!el) return;
    const target = scrollTargetForSession(rows, sessionId, el.clientHeight, el.scrollTop);
    if (target != null) el.scrollTop = target;
    // The programmatic write rides the existing onScroll → rAF →
    // setListScrollTop path, remounting the correct windowed slice.
  }, []);
  const lastSeenHighlightIdRef = useRef<string | null | undefined>(undefined);
  const pendingFollowScrollSessionIdRef = useRef<string | null>(null);
  useEffect(() => {
    if (lastSeenHighlightIdRef.current === undefined) {
      // First observation (mount): baseline the highlight without scrolling.
      lastSeenHighlightIdRef.current = effectiveHighlightSessionId;
      return;
    }
    const changed = lastSeenHighlightIdRef.current !== effectiveHighlightSessionId;
    lastSeenHighlightIdRef.current = effectiveHighlightSessionId;
    if (!followHighlightIntoView || !changed || effectiveHighlightSessionId == null) return;
    // Pane ids are family roots in practice; a subagent id resolves to its
    // root's row so the scroll math addresses a real row.
    const family = getSessionFamily(allSessions, effectiveHighlightSessionId);
    if (!family) return;
    const rootId = family.root.id;
    // A stale queued scroll must never win over a newer highlight.
    pendingFollowScrollSessionIdRef.current = null;
    const owningProject = pinnedProjects.find((project) =>
      (familiesByProject.get(project.key) ?? []).some((f) => f.root.id === rootId));
    if (owningProject && !expandedGroupKeys.has(owningProject.key)) {
      // The row hides inside a collapsed pinned group. Expand it now, but
      // queue the scroll: the DOM height still describes the pre-expansion
      // rows, so a synchronous scrollTop write would clamp short. The
      // pending effect below applies it once the rebuilt row model lands.
      expandPinnedGroup(owningProject.key);
      pendingFollowScrollSessionIdRef.current = rootId;
      return;
    }
    applyFollowScroll(sidebarRows, rootId);
    // Rows, families and the sessions catalog are render products of the
    // dependency inputs; listing them would re-scroll on list churn.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [effectiveHighlightSessionId, followHighlightIntoView]);
  // Applies a queued expansion scroll once the row model reflects it.
  useEffect(() => {
    const pendingId = pendingFollowScrollSessionIdRef.current;
    if (!pendingId) return;
    if (!sidebarRows.some((row) => row.kind === "session" && row.family.root.id === pendingId)) {
      return; // the expand re-render has not rebuilt the rows yet
    }
    pendingFollowScrollSessionIdRef.current = null;
    applyFollowScroll(sidebarRows, pendingId);
  });
  // One session row, shared by the main list and pinned groups: identical
  // selection behavior — the effective cwd moves to the session's worktree.
  const renderSessionRow = (family: SessionFamily) => {
    const familySessions = [family.root, ...family.subagents];
    // Marking is keyed on the ROOT id only (review r1 P2): the row's title,
    // selection target and click target are all the root's, so marking it
    // "filtered" must mean the root itself matches the active patterns —
    // never a subagent buried under a non-matching root.
    const familyIsFiltered = isFamilyRowFiltered(family.root.id, filteredSessionIds);
    const displaySession = family.latestModified === family.root.modified
      ? family.root
      : { ...family.root, modified: family.latestModified };
    return (
      <SessionItem
        session={displaySession}
        isSelected={familySessions.some((session) => session.id === effectiveHighlightSessionId)}
        isRunning={familySessions.some((session) => runningSessionIds.has(session.id))}
        isAwaiting={familySessions.some((session) => awaitingSessionIds.has(session.id))}
        isUnread={familySessions.some((session) => unreadSessionIds.has(session.id))}
        isFiltered={familyIsFiltered}
        onClick={() => handleSelectSessionFromList(family.root)}
        onPointerEnter={() => void prefetchSessionView(family.root.id)}
        onFocus={() => void prefetchSessionView(family.root.id)}
        onRenamed={loadSessions}
        onDeleted={(id) => {
          onSessionDeleted?.(id);
          loadSessions();
        }}
        onArchived={archiveSession}
      />
    );
  };

  return (
    <div
      ref={sessionPaneResizer.panelRef}
      style={{
        display: "flex",
        flexDirection: "column",
        height: "100%",
        overflow: "hidden",
        "--sidebar-session-pane-height": `${sessionPaneResizer.width}px`,
      } as CSSProperties}
    >
      {/* Managed entries with per-row rename/delete in the session-cwd
          picker too (wi pi#57): the SAME registered list and guarded seams
          as the manage picker, so the owner can rename/delete a registered
          directory right here. onSelect stays pure session-cwd selection
          (commitCustomPath) — picking a directory never registers it.
          Browse rows carry no manage actions; the buttons render only on
          the registered-entries rows because manage mode is
          `entries !== undefined`. */}
      {customPathOpen && (
        <DirectoryPicker
          initialPath={customPathValue}
          busy={customPathValidating}
          error={customPathError}
          entries={pinnedEntries.map((entry) => ({ path: entry.path, displayName: entry.displayName }))}
          onRenameEntryPath={(path, nextPath) => {
            const outcome = rowPathRenameHandler(path, nextPath);
            if (outcome.ok) {
              // Same expansion preservation as the manage picker: when the
              // renamed entry's group was the expanded one, keep it
              // expanded under the NEW identity.
              const oldKey = customDirectoryIdentity(path);
              if (expandedGroupKeys.has(oldKey)) {
                expandPinnedGroup(customDirectoryIdentity(nextPath.trim()));
              }
              setPinnedRevision((revision) => revision + 1);
              return { ok: true };
            }
            return {
              ok: false,
              error: t(outcome.reason === "empty"
                ? "directoryPicker.renamePathRequired"
                : "directoryPicker.renamePathDuplicate"),
            };
          }}
          onRemoveEntry={(path) => {
            const outcome = rowDeleteHandler(path);
            if (outcome.ok) {
              setPinnedRevision((revision) => revision + 1);
              return { ok: true };
            }
            return { ok: false, error: t("directoryPicker.cannotRemoveLastEntry") };
          }}
          onSelect={(path) => void commitCustomPath(path)}
          onCancel={() => {
            setCustomPathOpen(false);
            setCustomPathError(null);
          }}
        />
      )}
      {/* Add-directory picker (manage mode): select adds the directory to
          the custom list and registers it as an allowed file root; the
          dialog's manage callbacks run the guarded seams above (wi pi#52),
          so delete/rename from the picker behave exactly like the other
          surfaces and report typed refusals back into the dialog. */}
      {addDirectoryOpen && (
        <DirectoryPicker
          initialPath={customPathValue || homeDir || undefined}
          entries={pinnedEntries.map((entry) => ({ path: entry.path, displayName: entry.displayName }))}
          onRenameEntryPath={(path, nextPath) => {
            const outcome = rowPathRenameHandler(path, nextPath);
            if (outcome.ok) {
              // When the renamed entry's group was the expanded one, keep it
              // expanded under the NEW identity so the group does not
              // visually collapse on rename (the old key is gone).
              const oldKey = customDirectoryIdentity(path);
              if (expandedGroupKeys.has(oldKey)) {
                expandPinnedGroup(customDirectoryIdentity(nextPath.trim()));
              }
              setPinnedRevision((revision) => revision + 1);
              return { ok: true };
            }
            return {
              ok: false,
              error: t(outcome.reason === "empty"
                ? "directoryPicker.renamePathRequired"
                : "directoryPicker.renamePathDuplicate"),
            };
          }}
          onRemoveEntry={(path) => {
            const outcome = rowDeleteHandler(path);
            if (outcome.ok) {
              setPinnedRevision((revision) => revision + 1);
              return { ok: true };
            }
            return { ok: false, error: t("directoryPicker.cannotRemoveLastEntry") };
          }}
          onSelect={(path) => void handleAddDirectory(path)}
          onPinDirectory={pinDirectory}
          onCancel={() => setAddDirectoryOpen(false)}
        />
      )}
      {/* Header */}
      <div
        style={{
          padding: "12px 10px 10px",
          borderBottom: "1px solid var(--border)",
          flexShrink: 0,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 10 }}>
          <PiWebTitle />
          <div style={{ display: "flex", gap: 6 }}>
            <button
              type="button"
              onClick={() => {
                if (sidebarRefreshInFlightRef.current) return;
                sidebarRefreshInFlightRef.current = true;
                setSidebarRefreshing(true);
                void loadSessions(false, true).finally(() => {
                  sidebarRefreshInFlightRef.current = false;
                  setSidebarRefreshing(false);
                });
              }}
              disabled={sidebarRefreshing}
              aria-busy={sidebarRefreshing}
              title={sidebarRefreshing ? t("sidebar.refreshing") : t("sidebar.refresh")}
              aria-label={sidebarRefreshing ? t("sidebar.refreshing") : t("sidebar.refresh")}
              className="flex h-[32px] w-[32px] shrink-0 cursor-pointer items-center justify-center rounded-[7px] border border-border bg-bg-hover text-text-muted hover:bg-bg-selected focus-visible:outline-2 focus-visible:outline-accent disabled:cursor-default"
              onMouseEnter={(e) => { e.currentTarget.style.background = "var(--bg-selected)"; e.currentTarget.style.color = "var(--text)"; }}
              onMouseLeave={(e) => { e.currentTarget.style.background = "var(--bg-hover)"; e.currentTarget.style.color = "var(--text-muted)"; }}
            >
              <svg className={sidebarRefreshing ? "animate-spin" : undefined} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M21 12a9 9 0 1 1-2.64-6.36L21 8" />
                <path d="M21 3v5h-5" />
              </svg>
            </button>
            <button
              type="button"
              onClick={() => {
                setSessionSearchOpen((open) => !open);
              }}
              title={t("sidebar.toggleSessionSearch")}
              aria-label={t("sidebar.toggleSessionSearch")}
              aria-expanded={sessionSearchOpen}
              aria-controls="session-search-input"
              className={`flex h-[32px] w-[32px] shrink-0 cursor-pointer items-center justify-center rounded-[7px] border border-border hover:bg-bg-selected focus-visible:outline-2 focus-visible:outline-accent ${sessionSearchOpen ? "bg-bg-selected text-accent" : "bg-bg-hover text-text-muted"}`}
            >
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <circle cx="11" cy="11" r="7" /><path d="m20 20-4-4" />
              </svg>
            </button>
            {/* R2b (pi#49 → wi pi#52): the Add button moved INTO the toolbar
                row, level with refresh and search — the standalone
                full-width button below is gone. Same 32px icon-button
                styling, same label, same behavior: opens the directory
                picker in manage mode. */}
            <button
              type="button"
              onClick={() => setAddDirectoryOpen(true)}
              title={t("sidebar.addNew")}
              aria-label={t("sidebar.addNew")}
              className="flex h-[32px] w-[32px] shrink-0 cursor-pointer items-center justify-center rounded-[7px] border border-border bg-bg-hover text-text-muted hover:bg-bg-selected focus-visible:outline-2 focus-visible:outline-accent"
              onMouseEnter={(e) => { e.currentTarget.style.background = "var(--bg-selected)"; e.currentTarget.style.color = "var(--text)"; }}
              onMouseLeave={(e) => { e.currentTarget.style.background = "var(--bg-hover)"; e.currentTarget.style.color = "var(--text-muted)"; }}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                <line x1="12" y1="5" x2="12" y2="19" />
                <line x1="5" y1="12" x2="19" y2="12" />
              </svg>
            </button>
          </div>
        </div>

        {sessionSearchOpen && (
          <input
            id="session-search-input"
            type="search"
            autoFocus
            value={sessionSearchQuery}
            maxLength={200}
            aria-label={t("sidebar.searchSessions")}
            placeholder={t("sidebar.searchSessions")}
            onChange={(event) => setSessionSearchQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                event.stopPropagation();
                setSessionSearchQuery("");
              }
            }}
            className="mt-[6px] block h-[29px] w-full min-w-0 rounded-[7px] border border-border bg-bg px-[10px] text-xs text-text focus:outline-2 focus:outline-accent"
          />
        )}

      </div>

      {/* Sessions | Files tabs (wi B3): render ONLY in tabs mode (stacked
          keeps the classic header + split). One conditional JSX slot between
          the header and the sessions pane — the panes below keep their
          positions, so the explorer never remounts when the mode flips. */}
      {!stackedSidebarLayout && (
        <div
          role="tablist"
          aria-label={t("sidebar.tabsLabel")}
          style={{
            display: "flex",
            alignItems: "stretch",
            flexShrink: 0,
            borderBottom: "1px solid var(--border)",
          }}
        >
          <button
            type="button"
            role="tab"
            id="session-sidebar-tab-sessions"
            aria-selected={sidebarTab === "sessions"}
            aria-controls="session-sidebar-panel-sessions"
            tabIndex={sidebarTab === "sessions" ? 0 : -1}
            onClick={() => setSidebarTab("sessions")}
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              gap: 6,
              flex: 1,
              height: 30,
              background: "none",
              border: "none",
              borderBottom: sidebarTab === "sessions" ? "2px solid var(--accent)" : "2px solid transparent",
              color: sidebarTab === "sessions" ? "var(--text)" : "var(--text-muted)",
              fontSize: 11,
              fontWeight: 600,
              letterSpacing: "0.05em",
              textTransform: "uppercase",
              cursor: "pointer",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }} aria-hidden="true">
              <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
            </svg>
            {t("sidebar.tabSessions")}
          </button>
          <button
            type="button"
            role="tab"
            id="session-sidebar-tab-files"
            aria-selected={sidebarTab === "files"}
            aria-controls="session-sidebar-panel-files"
            tabIndex={sidebarTab === "files" ? 0 : -1}
            onClick={() => setSidebarTab("files")}
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              gap: 6,
              flex: 1,
              height: 30,
              background: "none",
              border: "none",
              borderBottom: sidebarTab === "files" ? "2px solid var(--accent)" : "2px solid transparent",
              color: sidebarTab === "files" ? "var(--text)" : "var(--text-muted)",
              fontSize: 11,
              fontWeight: 600,
              letterSpacing: "0.05em",
              textTransform: "uppercase",
              cursor: "pointer",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }} aria-hidden="true">
              <path d="M3 8a2 2 0 0 1 2-2h3.4l1.9 1.9H19a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
            </svg>
            {t("sidebar.tabFiles")}
          </button>
        </div>
      )}

      {/* Session list — upstream #825 resizable panes: the list pane adopts the
          computed height whenever the explorer pane is open; pi#14 keeps the
          explorer mounted with or without a selected cwd, so no cwd condition. */}
      <div
        ref={sessionPaneRef}
        id="session-sidebar-panel-sessions"
        role={stackedSidebarLayout ? undefined : "tabpanel"}
        aria-labelledby={stackedSidebarLayout ? undefined : "session-sidebar-tab-sessions"}
        style={{
          // Tabs mode: the pane is either the visible tab (fills the column)
          // or display:none — NEVER unmounted, so its state survives tab and
          // layout switches. Stacked mode: today's flex behavior unchanged.
          display: sessionsPaneHidden ? "none" : "flex",
          flexDirection: "column",
          flex: stackedSidebarLayout
            ? explorerOpen
              ? "0 1 var(--sidebar-session-pane-height, 320px)"
              : "1 1 auto"
            : "1 1 auto",
          minHeight: SESSION_PANE_MIN_HEIGHT,
          overflow: "hidden",
        }}
      >
        <SessionSearch open={sessionSearchOpen} query={sessionSearchQuery} selectedSessionId={selectedSessionId} archivedSessionIds={archivedSessionIds} onSelectSession={handleSelectSessionFromList}>
        {archiveViewOpen ? (
          /* Archive view (wi pi#103): temporarily REPLACES the sessions pane
             list region — the normal rows stop rendering while the view is
             open (replacement, not an inline block). A prominent back button
             restores the list; the body groups the archived sessions by
             project. Row operations are limited to open (view-only) and
             unarchive — no delete, rename, open-in-new-tab or context menu,
             and no fold/motion animation (spec non-goal). */
          <div style={{ flex: "1 1 auto", minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>
            <button
              type="button"
              onClick={() => setArchiveViewOpen(false)}
              title={t("sidebar.archiveBack")}
              aria-label={t("sidebar.archiveBack")}
              style={{
                display: "flex", alignItems: "center", gap: 6,
                flexShrink: 0, width: "100%",
                padding: "8px 14px",
                background: "var(--bg-hover)", border: "none",
                borderBottom: "1px solid var(--border)",
                color: "var(--text)", cursor: "pointer",
                fontSize: 12, fontWeight: 600, textAlign: "left",
              }}
            >
              {/* ← glyph — the e2e locator contract accepts Back/返回/←. */}
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
                <line x1="19" y1="12" x2="5" y2="12" />
                <polyline points="12 19 5 12 12 5" />
              </svg>
              {t("sidebar.archiveBack")}
            </button>
            <div className="scrollbar-subtle" style={{ flex: "1 1 auto", minHeight: 0, overflowY: "auto" }}>
              {archivedGroups.map((group) => (
                <div key={group.root}>
                  {/* Group label: the pinned-group path-derived convention —
                      display text carries the directory (basename visible),
                      the title attr carries the full path. */}
                  <div
                    title={group.root}
                    style={{
                      padding: "8px 14px 3px",
                      fontSize: 10,
                      fontWeight: 600,
                      letterSpacing: "0.05em",
                      textTransform: "uppercase",
                      color: "var(--text-dim)",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {displayCwd(group.root, homeDir)}
                  </div>
                  {group.sessions.map((session) => (
                    <ArchiveSessionRow
                      key={session.id}
                      session={session}
                      isSelected={session.id === selectedSessionId}
                      isUnread={unreadSessionIds.has(session.id)}
                      onOpen={handleSelectSessionFromList}
                      onUnarchived={unarchiveSession}
                    />
                  ))}
                </div>
              ))}
            </div>
          </div>
        ) : (
        <div
          ref={listScrollRef}
          onScroll={handleListScroll}
          className="scrollbar-subtle"
          onTouchStart={(event) => {
            const el = listScrollRef.current;
            if (!el || el.scrollTop > 0) return;
            pullStartYRef.current = event.touches[0]?.clientY ?? null;
            pullFiredRef.current = false;
          }}
          onTouchMove={(event) => {
            const startY = pullStartYRef.current;
            if (startY == null || pullFiredRef.current) return;
            const deltaY = (event.touches[0]?.clientY ?? startY) - startY;
            if (deltaY > 64) {
              pullFiredRef.current = true;
              void loadSessions(false, true);
            }
          }}

          style={{
            flex: "1 1 auto",
            minHeight: 0,
            overflowY: "auto",
            padding: "0",
          }}
        >
        {loading && (
          <div style={{ padding: "16px 14px", color: "var(--text-muted)", fontSize: 12 }}>
            {t("sidebar.loading")}
          </div>
        )}
        {error && (
          <div style={{ padding: "12px 14px", color: "#f87171", fontSize: 12 }}>
            {error}
          </div>
        )}
        {!loading && !error && sidebarRows.length === 0 && (
          <div style={{ padding: "16px 14px", color: "var(--text-muted)", fontSize: 12 }}>
            {t("sidebar.noSessions")}
          </div>
        )}
        {sidebarRows.length > 0 && (
          <div
            style={{
              position: "relative",
              height: sidebarRowsHeight(sidebarRows),
            }}
          >
            {/* Pinned-group content containers — one per expanded group, so
                each header's aria-controls points at its own region. Only
                the windowed rows inside are mounted. */}
            {pinnedProjects.map((project) => {
              if (!expandedGroupKeys.has(project.key)) return null;
              const groupRows = groupRowsByKey.get(project.key) ?? [];
              if (groupRows.length === 0) return null;
              const contentTop = groupRows[0].offset;
              const last = groupRows[groupRows.length - 1];
              const contentHeight = last.offset + last.height - contentTop;
              return (
                <div
                  key={`group-content:${project.key}`}
                  id={pinnedGroupContentId(project.key)}
                  style={{ position: "absolute", top: contentTop, left: 0, right: 0, height: contentHeight }}
                >
                  {groupRows
                    .filter((row) => visibleSidebarRows.includes(row))
                    .map((row) => (
                      row.kind === "groupEmpty" ? (
                        <div
                          key={row.key}
                          style={{
                            position: "absolute",
                            top: row.offset - contentTop,
                            left: 0,
                            right: 0,
                            height: row.height,
                            display: "flex",
                            alignItems: "center",
                            paddingLeft: 28,
                            fontSize: 11,
                            color: "var(--text-dim)",
                          }}
                        >
                          {t("sidebar.pinnedGroupNoSessions")}
                        </div>
                      ) : row.kind === "groupMore" ? (
                        <button
                          key={row.key}
                          // Rows remain hidden ("show more · N", revealing
                          // SHOW_MORE_STEP more) or the click already revealed
                          // everything ("show less", back to the limit).
                          onClick={() => row.hiddenCount > 0
                            ? handlePinnedGroupShowMore(
                                row.groupKey,
                                typeof pinnedShownExtra[row.groupKey] === "number" && pinnedShownExtra[row.groupKey] > 0
                                  ? Math.floor(pinnedShownExtra[row.groupKey])
                                  : 0,
                              )
                            : handlePinnedGroupShowLess(row.groupKey)}
                          title={row.hiddenCount > 0
                            ? t("sidebar.showMore", { count: row.hiddenCount })
                            : t("sidebar.showLess")}
                          aria-label={row.hiddenCount > 0
                            ? t("sidebar.showMore", { count: row.hiddenCount })
                            : t("sidebar.showLess")}
                          style={{
                            position: "absolute",
                            top: row.offset - contentTop,
                            left: 0,
                            right: 0,
                            height: row.height,
                            display: "flex",
                            alignItems: "center",
                            justifyContent: "flex-start",
                            paddingLeft: 28,
                            fontSize: 11,
                            color: "var(--accent)",
                            background: "none",
                            border: "none",
                            cursor: "pointer",
                            padding: 0,
                          }}
                        >
                          {row.hiddenCount > 0
                            ? t("sidebar.showMore", { count: row.hiddenCount })
                            : t("sidebar.showLess")}
                        </button>
                      ) : (
                        <div
                          key={row.key}
                          onFocus={() => setFocusedSessionId(row.family.root.id)}
                          onBlur={() => setFocusedSessionId(null)}
                          style={{ position: "absolute", top: row.offset - contentTop, left: 0, right: 0 }}
                        >
                          {renderSessionRow(row.family)}
                        </div>
                      )
                    ))}
                </div>
              );
            })}
            {visibleSidebarRows.map((row) => {
              if (row.kind === "groupHeader") {
                return (
                  <div
                    key={row.key}
                    style={{ position: "absolute", top: row.offset, left: 0, right: 0, height: row.height }}
                  >
                    <PinnedGroupHeader
                      project={row.project}
                      label={pinnedLabelsByKey.get(row.project.key)}
                      expanded={expandedGroupKeys.has(row.project.key)}
                      stale={stalePinnedRoots.has(row.project.root)}
                      activity={groupActivity.get(row.project.key)}
                      homeDir={homeDir}
                      t={t}
                      reorder={pinnedGroupDragProps(row.project)}
                      onToggle={() => handleToggleGroup(row.project.key)}
                      onUnpin={() => togglePin(row.project.root)}
                      onRename={(name) => {
                        // Rename edits the list entry's displayName; an empty
                        // value clears it, falling back to the path-derived
                        // label. List operation only — the disk is untouched.
                        renameCustomDirectory(row.project.root, name);
                        setPinnedRevision((revision) => revision + 1);
                      }}
                      onNewSession={() => handleNewSessionInProject(row.project)}
                    />
                  </div>
                );
              }
              // Group session rows render inside their group container above.
              if (row.kind !== "session" || row.groupKey !== null) return null;
              const family = row.family;
              // Bubble blur after the input's save handler before unpinning the row.
              return (
                <div
                  key={family.root.id}
                  onFocus={() => setFocusedSessionId(family.root.id)}
                  onBlur={() => setFocusedSessionId(null)}
                  style={{ position: "absolute", top: row.offset, left: 0, right: 0 }}
                >
                  {renderSessionRow(family)}
                </div>
              );
            })}
          </div>
        )}
        </div>
        )}
        </SessionSearch>
        {hiddenSessionCount > 0 && (
          /* Hidden-sessions toggle (wi pi#73, refines pi#65): relocated to the
             bottom of the list and restyled after the explorer's build-outputs
             checkbox. Checking it reveals the filtered rows through the shared
             live store (setShowFilteredSessions), keeping Settings in sync. */
          <label
            title={t("sidebar.showHiddenSessions", { count: hiddenSessionCount })}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 5,
              width: "100%",
              padding: "4px 14px",
              flexShrink: 0,
              fontSize: 10,
              color: "var(--text-dim)",
              cursor: "pointer",
              userSelect: "none",
              borderTop: "1px solid var(--border)",
            }}
          >
            <input
              type="checkbox"
              checked={showFilteredSessions}
              onChange={() => setShowFilteredSessions(!showFilteredSessions)}
              aria-label={t("sidebar.showHiddenSessions", { count: hiddenSessionCount })}
              style={{ margin: 0 }}
            />
            {t("sidebar.showHiddenSessions", { count: hiddenSessionCount })}
          </label>
        )}
        {archivedSessions.length > 0 && (
          /* 「已归档 N」 entry (wi pi#103): the GLOBAL archive count — never
             scoped to the selected project — so it is stable across project
             switches. Entirely hidden at N=0 (the count includes the label,
             aria-label and button). Activating it opens the replacement
             archive view above. */
          <button
            type="button"
            onClick={() => setArchiveViewOpen(true)}
            title={t("sidebar.archivedSessions", { count: archivedSessions.length })}
            aria-label={t("sidebar.archivedSessions", { count: archivedSessions.length })}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 5,
              width: "100%",
              padding: "4px 14px",
              flexShrink: 0,
              fontSize: 10,
              color: "var(--text-dim)",
              background: "none",
              border: "none",
              borderTop: "1px solid var(--border)",
              cursor: "pointer",
              textAlign: "left",
              userSelect: "none",
            }}
          >
            {/* An archive box, matching the row action's glyph family. */}
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
              <rect x="3" y="3" width="18" height="4" rx="1" />
              <path d="M5 7v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7" />
              <path d="M10 12h4" />
            </svg>
            {t("sidebar.archivedSessions", { count: archivedSessions.length })}
          </button>
        )}
      </div>

      {/* Section resize handle (stacked only, wi B3): tabs mode has nothing
          to resize — the handle keeps its JSX slot but does not render, and
          the panes' positions are unaffected. */}
      {stackedSidebarLayout && explorerOpen && (
        <div
          className={`sidebar-section-resize-handle${sessionPaneResizer.isResizing ? " is-resizing" : ""}`}
          data-resize-handle="sidebar-sections"
          title={`${t("layout.resizeSidebarSections")}: ${t("layout.resizeHeightHint")}`}
          style={{
            position: "relative",
            zIndex: 20,
            width: "100%",
            height: 12,
            margin: "-6px 0",
            flex: "0 0 12px",
            cursor: "row-resize",
            touchAction: "none",
          }}
          {...sessionPaneResizer.separatorProps}
        />
      )}

      {/* File Explorer section — pi#14: always mounted (upstream gates on a
          selected cwd; the multi-root explorer takes roots from pinned projects
          too). The section ref feeds the #825 pane-height computation. */}
      <div
        ref={explorerSectionRef}
        id="session-sidebar-panel-files"
        role={stackedSidebarLayout ? undefined : "tabpanel"}
        aria-labelledby={stackedSidebarLayout ? undefined : "session-sidebar-tab-files"}
          style={{
            // Tabs mode: the pane is the visible tab or display:none — NEVER
            // unmounted, so the explorer's expansion/selection/scroll state
            // survives every tab and layout switch. Stacked mode: today's
            // flex behavior unchanged.
            display: filesPaneHidden ? "none" : "flex",
            borderTop: "1px solid var(--border)",
            flexDirection: "column",
            flex: explorerOpen ? "1 1 0" : "0 0 auto",
            minHeight: explorerOpen ? EXPLORER_PANE_MIN_HEIGHT : 0,
            overflow: "hidden",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", flexShrink: 0 }}>
            <button
              onClick={() => setExplorerOpen((open) => {
                const next = !open;
                saveExplorerOpen(next);
                return next;
              })}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 6,
                flex: 1,
                padding: "6px 10px",
                background: "none",
                border: "none",
                color: "var(--text-muted)",
                cursor: "pointer",
                fontSize: 11,
                fontWeight: 600,
                letterSpacing: "0.05em",
                textTransform: "uppercase",
                textAlign: "left",
              }}
            >
              <svg
                width="9" height="9" viewBox="0 0 10 10" fill="none"
                stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"
                style={{ transform: explorerOpen ? "rotate(90deg)" : "none", transition: "transform 0.15s", flexShrink: 0 }}
              >
                <polyline points="3 2 7 5 3 8" />
              </svg>
              {t("files.explorer")}
            </button>
            <ToolbarIconButton
              onClick={() => { void openInFileManager(); }}
              disabled={fileManagerUnavailable}
              title={fileManagerUnavailable
                ? t(fileManager?.reason === "remote" ? "sidebar.openInExplorerRemoteOnly" : "sidebar.openInExplorerUnsupported")
                : fileManagerLabel}
              color="var(--text-dim)"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M3 8a2 2 0 0 1 2-2h3.4l1.9 1.9H19a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
              </svg>
            </ToolbarIconButton>
            {onOpenTerminal && (
              <ToolbarIconButton
                onClick={() => onOpenTerminal(selectedCwd ?? selectedCwdProp!)}
                title={t("terminal.open")}
                color="var(--text-dim)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <polyline points="4 17 10 11 4 5" /><line x1="12" y1="19" x2="20" y2="19" />
                </svg>
              </ToolbarIconButton>
            )}
            {explorerOpen && changesCount > 0 && (
              <ToolbarIconButton
                onClick={() => setChangesCollapsed((v) => !v)}
                title={t("sidebar.changedFiles", { count: changesCount })}
                ariaPressed={!changesCollapsed}
                color={changesCollapsed ? "var(--text-dim)" : "var(--accent)"}
                background={changesCollapsed ? "none" : "var(--bg-selected)"}
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <circle cx="12" cy="12" r="3" />
                  <path d="M3 12h6" />
                  <path d="M15 12h6" />
                </svg>
              </ToolbarIconButton>
            )}
            {explorerOpen && (
              <ToolbarIconButton
                onClick={() => {
                  if (fileSearchOpen) {
                    setFileSearchOpen(false);
                    return;
                  }
                  // Opening routes through the container handle: it expands
                  // the deterministic target section first, so the search
                  // input appears even from the all-collapsed default state
                  // (review-FAIL blocker 2 — the handle was dead code before).
                  multiRootExplorerRef.current?.openFileSearch();
                }}
                disabled={explorerRoots.length === 0}
                title={t("sidebar.searchFiles")}
                ariaPressed={fileSearchOpen}
                color={fileSearchOpen ? "var(--accent)" : "var(--text-dim)"}
                background={fileSearchOpen ? "var(--bg-selected)" : "none"}
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <circle cx="11" cy="11" r="7" /><path d="m20 20-4-4" />
                </svg>
              </ToolbarIconButton>
            )}
            {explorerOpen && (
              <ToolbarIconButton
                onClick={() => multiRootExplorerRef.current?.openUploadPicker()}
                disabled={explorerUploadBusy || explorerRoots.length === 0}
                title={t("sidebar.uploadFilesTitle")}
                color="var(--text-dim)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                  <path d="m17 8-5-5-5 5" />
                  <path d="M12 3v12" />
                </svg>
              </ToolbarIconButton>
            )}
            <ToolbarIconButton
              onClick={() => {
                if (onExplorerRefresh) onExplorerRefresh();
                else setExplorerKey((k) => k + 1);
                setExplorerRefreshDone(true);
                if (explorerRefreshTimerRef.current) clearTimeout(explorerRefreshTimerRef.current);
                explorerRefreshTimerRef.current = setTimeout(() => setExplorerRefreshDone(false), 2000);
              }}
              title={t("sidebar.refreshExplorer")}
              skipHover={explorerRefreshDone}
              color={explorerRefreshDone ? "#4ade80" : "var(--text-dim)"}
              background={explorerRefreshDone ? "rgba(74,222,128,0.18)" : "none"}
              marginRight={6}
            >
              {explorerRefreshDone ? (
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#4ade80" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              ) : (
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
                  <path d="M3 3v5h5" />
                </svg>
              )}
            </ToolbarIconButton>
          </div>
          {fileManagerErrorMessage && (
            <div role="alert" style={{ display: "flex", alignItems: "flex-start", gap: 6, padding: "0 10px 6px", fontSize: 10, lineHeight: 1.35, color: "#f87171" }}>
              <span style={{ minWidth: 0, flex: 1, overflowWrap: "anywhere" }}>{fileManagerErrorMessage}</span>
              <DismissButton onClick={() => setFileManagerError(null)} title={t("files.dismissError")} />
            </div>
          )}
          {explorerOpen && (
            <div ref={explorerScrollRef} className="scrollbar-subtle" style={{ flex: 1, overflowY: "auto", overflowX: "hidden" }}>
              <MultiRootFileExplorer
                ref={multiRootExplorerRef}
                roots={explorerRoots}
                staleRoots={stalePinnedRoots}
                homeDir={homeDir}
                refreshKey={explorerKey}
                onOpenFile={onOpenFile ?? (() => {})}
                onAtMention={onAtMention}
                onAtMentions={onAtMentions}
                activeFilePath={activeFilePath ?? null}
                onUploadBusyChange={setExplorerUploadBusy}
                changesCollapsed={changesCollapsed}
                onChangesCountChange={setChangesCount}
                fileSearchOpen={fileSearchOpen}
                onFileSearchOpenChange={setFileSearchOpen}
              />
            </div>
          )}
        </div>
    </div>
  );
}


/**
 * The inline "awaiting input" marker (wi pi#101): a running session parked
 * on an extension dialog or human question. Static by contrast with the
 * running spinner — the run is not moving — and amber by contrast with the
 * accent-blue spinner and cyan unread dot, so all three row states stay
 * distinguishable at a glance.
 */
/**
 * The visible harness marker of a claude session's row: just "claude", pushed to
 * the right end of its title line. The model is never shown (owner intentional change,
 *): a claude session's model can change in-session, so a listed value would go
 * stale. pi rows show none.
 */
export function HarnessMarker({ session }: { session: Pick<SessionInfo, "harness"> }) {
  if (session.harness !== "claude") return null;
  return (
    <span
      data-harness="claude"
      style={{
        flexShrink: 0,
        marginLeft: "auto",
        fontSize: 10,
        lineHeight: 1.4,
        padding: "0 5px",
        color: "var(--accent)",
        background: "var(--bg-hover)",
        border: "1px solid var(--border)",
        borderRadius: 4,
        whiteSpace: "nowrap",
      }}
    >
      claude
    </span>
  );
}

/**
 * The archive set with every claude row's state from pi-web's index: an
 * archived claude row is in it, any other claude row is not; pi rows keep the browser's
 * state. The same set back when nothing changed.
 */
export function withClaudeArchiveState(current: ReadonlySet<string>, sessions: readonly SessionInfo[]): ReadonlySet<string> {
  let next: Set<string> | null = null;
  for (const session of sessions) {
    if (session.harness !== "claude") continue;
    const archived = session.archived === true;
    if (archived === current.has(session.id)) continue;
    next ??= new Set(current);
    if (archived) next.add(session.id);
    else next.delete(session.id);
  }
  return next ?? current;
}

/** Writes a claude row's archive state to pi-web's index. */
async function patchClaudeArchived(id: string, archived: boolean): Promise<void> {
  const response = await fetch(`/api/sessions/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ archived }),
  });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
}

/** The awaiting marker; a claude row waits only on claude's permission prompts. */
function AwaitingInputIndicator({ permission }: { permission: boolean }) {
  const { t } = useI18n();
  const label = permission ? t("sidebar.awaitingPermission") : t("sidebar.awaitingInput");
  return (
    <span
      title={label}
      aria-label={label}
      style={{
        width: 14,
        height: 14,
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        flexShrink: 0,
        color: "#d97706",
      }}
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true" style={{ display: "block" }}>
        <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2" />
        <path d="M9.4 9.1a2.6 2.6 0 1 1 3.7 2.4c-.7.3-1.1.8-1.1 1.5v.2" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        <circle cx="12" cy="16.6" r="1.1" fill="currentColor" />
      </svg>
    </span>
  );
}

function UnreadSessionIndicator() {
  const { t } = useI18n();
  return (
    <span
      title={t("sidebar.newActivity")}
      aria-label={t("sidebar.newSessionActivity")}
      style={{
        width: 14,
        height: 14,
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        flexShrink: 0,
        color: "#0891b2",
      }}
    >
      <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true" style={{ display: "block" }}>
        <circle cx="7" cy="7" r="2.5" fill="currentColor" />
        <circle cx="7" cy="7" r="3" stroke="currentColor" strokeWidth="1.4" opacity="0.32">
          <animate attributeName="r" values="3;6;3" dur="1.6s" repeatCount="indefinite" />
          <animate attributeName="opacity" values="0.32;0;0.32" dur="1.6s" repeatCount="indefinite" />
        </circle>
      </svg>
    </span>
  );
}

/** Archive view row (wi pi#103): mirrors SessionItem's normal-view row
 *  markup — same fixed height (SESSION_LIST_ITEM_HEIGHT), deep-link anchor
 *  title, unread indicator, relative time — but offers ONLY view-only open
 *  and unarchive. No delete, rename, open-in-new-tab or context-menu action:
 *  the archive view is a curation surface, not a second session manager.
 *  Clicking (or the anchor) opens the session through the normal open path
 *  and never mutates the archive set. */
function ArchiveSessionRow({
  session,
  isSelected,
  isUnread,
  onOpen,
  onUnarchived,
}: {
  session: SessionInfo;
  isSelected: boolean;
  isUnread: boolean;
  onOpen: (session: SessionInfo) => void;
  onUnarchived: (id: string) => void;
}) {
  const { locale, t } = useI18n();
  // Same title derivation as SessionItem: stored name, else the collapsed
  // first message, else the id head.
  const displayFirstMessage = session.firstMessageDisplay
    ?? skillExpansionToCommand(session.firstMessage)
    ?? session.firstMessage;
  const title = session.name || displayFirstMessage.slice(0, 50) || session.id.slice(0, 12);
  return (
    <div
      onClick={() => onOpen(session)}
      style={{
        height: SESSION_LIST_ITEM_HEIGHT,
        display: "flex",
        alignItems: "center",
        paddingLeft: 14,
        paddingRight: 8,
        cursor: "pointer",
        background: isSelected ? "var(--bg-selected)" : "transparent",
        borderLeft: isSelected ? "2px solid var(--accent)" : "2px solid transparent",
        transition: "background 0.1s",
        gap: 6,
        overflow: "hidden",
      }}
    >
      <div style={{ flex: 1, minWidth: 0 }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 5,
            minWidth: 0,
            fontSize: 12,
            fontWeight: isSelected ? 500 : 400,
            lineHeight: 1.4,
            color: "var(--text)",
          }}
          title={title}
        >
          {/* A real link (middle-click / Ctrl-click still work) whose plain
              activation opens the session VIEW-ONLY — the archive set is
              never touched here. */}
          <a
            href={sessionDeepLink(session.id)}
            style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0, color: "inherit", textDecoration: "none", cursor: "inherit" }}
            onClick={(event) => {
              event.stopPropagation();
              if (!isPlainActivation(event)) return;
              event.preventDefault();
              onOpen(session);
            }}
          >
            {title}
          </a>
          <HarnessMarker session={session} />
        </div>
        <div style={{ marginTop: 2, display: "flex", alignItems: "center", gap: 8, color: "var(--text-dim)", fontSize: 11, minWidth: 0 }}>
          {/* Unread indication: passive unread does NOT unarchive — the dot
              shows here so the archived session's new activity stays visible. */}
          {isUnread ? (
            <UnreadSessionIndicator />
          ) : (
            <span title={session.modified}>{formatRelativeTime(session.modified, locale)}</span>
          )}
          <span>
            {session.detailsPending ? "…" : t("sidebar.messagesCount", { count: session.messageCount })}
          </span>
        </div>
      </div>
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          onUnarchived(session.id);
        }}
        title={t("sidebar.unarchive")}
        aria-label={t("sidebar.unarchive")}
        style={{
          display: "flex", alignItems: "center", justifyContent: "center",
          width: 32, height: 32, padding: 0, flexShrink: 0,
          background: "var(--bg-hover)", border: "1px solid var(--border)",
          borderRadius: 7, color: "var(--text-muted)",
          cursor: "pointer",
          transition: "background 0.12s, color 0.12s, border-color 0.12s",
        }}
        onMouseEnter={(e) => {
          e.currentTarget.style.background = "var(--bg-selected)";
          e.currentTarget.style.color = "var(--accent)";
          e.currentTarget.style.borderColor = "rgba(37,99,235,0.35)";
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.background = "var(--bg-hover)";
          e.currentTarget.style.color = "var(--text-muted)";
          e.currentTarget.style.borderColor = "var(--border)";
        }}
      >
        {/* Unarchive: the archive box with its lid open and an arrow leaving
            it upward. Label is an e2e locator contract:
            取消归档 / 取消封存 / Unarchive, exactly. */}
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M5 7v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7" />
          <path d="M12 15V4" />
          <path d="M8.5 7.5 12 4l3.5 3.5" />
        </svg>
      </button>
    </div>
  );
}

/**
 * Compact per-project activity badges for the workspace selector dropdown items:
 * a spinning running icon + count and an unread dot + count. Renders nothing
 * when the project has no activity. Counts share the accent / unread colors of
 * the per-session indicators so the two stay visually consistent.
 */
function showProjectActivity(
  activity: { running: number; unread: number } | undefined,
  t: (key: string) => string,
): ReactNode {
  if (!activity || (activity.running === 0 && activity.unread === 0)) return null;
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 5, flexShrink: 0, marginLeft: 6 }}>
      {activity.running > 0 && (
        <span
          title={t("sidebar.agentRunning")}
          aria-label={`${t("sidebar.agentRunning")} (${activity.running})`}
          style={{ display: "inline-flex", alignItems: "center", gap: 3, color: "var(--accent)", fontSize: 10, fontFamily: "var(--font-mono)" }}
        >
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" aria-hidden="true" style={{ display: "block" }}>
            <g>
              <path d="M21 12a9 9 0 1 1-3.8-7.4" stroke="currentColor" strokeWidth="2.8" strokeLinecap="round" />
              <animateTransform attributeName="transform" type="rotate" from="0 12 12" to="360 12 12" dur="0.9s" repeatCount="indefinite" />
            </g>
          </svg>
          {activity.running}
        </span>
      )}
      {activity.unread > 0 && (
        <span
          title={t("sidebar.newSessionActivity")}
          aria-label={`${t("sidebar.newSessionActivity")} (${activity.unread})`}
          style={{ display: "inline-flex", alignItems: "center", gap: 3, color: "#0891b2", fontSize: 10, fontFamily: "var(--font-mono)" }}
        >
          <span style={{ width: 6, height: 6, borderRadius: "50%", background: "currentColor", display: "inline-block" }} />
          {activity.unread}
        </span>
      )}
    </span>
  );
}

function SessionItem({
  session,
  isSelected,
  isRunning,
  isAwaiting,
  isUnread,
  isFiltered = false,
  onClick,
  onPointerEnter,
  onFocus,
  onRenamed,
  onDeleted,
  onArchived,
  depth = 0,
  hasChildren = false,
  collapsed = false,
  onToggleCollapse,
}: {
  session: SessionInfo;
  isSelected: boolean;
  isRunning?: boolean;
  /** Awaiting input (wi pi#101): the run is parked on an extension dialog
   *  or a human question. Takes the row's inline indicator slot over the
   *  running spinner — the session is not making progress, it waits on a
   *  person. */
  isAwaiting?: boolean;
  isUnread?: boolean;
  /** Reveal-mode marking (wi pi#65): the row matches the active session
   *  filter and is visible only because the reveal toggle is ON — rendered
   *  muted/italic with a marker chip instead of blending in. */
  isFiltered?: boolean;
  onClick: () => void;
  /** Hover/focus prefetch (pi#83): warms the session view before the click. */
  onPointerEnter?: () => void;
  onFocus?: () => void;
  onRenamed?: () => void;
  onDeleted?: (id: string) => void;
  /** Archive action (wi pi#103): mutates only the client-side archive
   *  set — never tabs, selection, or the delete path. */
  onArchived?: (id: string) => void;
  depth?: number;
  hasChildren?: boolean;
  collapsed?: boolean;
  onToggleCollapse?: () => void;
}) {
  const { locale, t } = useI18n();
  const [hovered, setHovered] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  // Select the whole name once the rename input is mounted (startRename's
  // immediate setTimeout can fire before the input exists).
  useEffect(() => {
    if (renaming) {
      const id = requestAnimationFrame(() => inputRef.current?.select());
      return () => cancelAnimationFrame(id);
    }
  }, [renaming]);

  // A stored first message may be an SDK-expanded <skill> block; collapse it
  // back to the compact /skill:name args command the user typed before using
  // it as the auto-name fallback, mirroring MessageView's rendering. Long
  // expansions arrive from the LIST payload already truncated, so the row
  // carries `firstMessageDisplay` (the compact command computed server-side
  // from the FULL text before the cut) — without it the collapse regex
  // cannot find the closing envelope and the title would render raw markup.
  const displayFirstMessage = session.firstMessageDisplay
    ?? skillExpansionToCommand(session.firstMessage)
    ?? session.firstMessage;
  const title = session.name || displayFirstMessage.slice(0, 50) || session.id.slice(0, 12);

  const startRename = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    if (session.transient) return;
    setRenameValue(session.name || displayFirstMessage.slice(0, 50) || session.id.slice(0, 12));
    setRenaming(true);
  }, [session.name, session.transient, displayFirstMessage, session.id]);

  const commitRename = useCallback(async () => {
    const name = renameValue.trim();
    setRenaming(false);
    // No-op when unchanged: the fallback title (first message / id) isn't a
    // real stored name, so don't persist it as one. (The rename input seeds
    // from the same collapsed displayFirstMessage, so an untouched rename of
    // a skill-invoked session stays a no-op instead of persisting raw XML.)
    if (renameValue === title || name === (session.name ?? "")) return;
    try {
      await fetch(`/api/sessions/${encodeURIComponent(session.id)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      onRenamed?.();
    } catch {
      // ignore
    }
  }, [renameValue, session.id, session.name, onRenamed, title]);

  const performDelete = useCallback(async () => {
    if (session.transient) return;
    setConfirmDelete(false);
    setDeleting(true);
    try {
      await fetch(`/api/sessions/${encodeURIComponent(session.id)}`, { method: "DELETE" });
      onDeleted?.(session.id);
    } catch {
      setDeleting(false);
    }
  }, [session.id, session.transient, onDeleted]);

  const handleDeleteClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    if (e.shiftKey) {
      void performDelete();
    } else {
      setConfirmDelete(true);
    }
  }, [performDelete]);

  const handleDeleteConfirm = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    void performDelete();
  }, [performDelete]);

  const handleDeleteCancel = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    setConfirmDelete(false);
  }, []);

  const handleContextMenu = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    const handled = dispatchSessionRowContextMenu({
      id: session.id,
      path: session.path,
      cwd: session.cwd,
      name: session.name,
      clientX: e.clientX,
      clientY: e.clientY,
      refresh: () => { onRenamed?.(); },
    });
    if (!handled) return;
    e.preventDefault();
    e.stopPropagation();
  }, [onRenamed, session.cwd, session.id, session.name, session.path]);

  // Fixed-height outer wrapper — content swaps in place so the list never reflows
  return (
    <div
      onClick={confirmDelete || renaming ? undefined : onClick}
      onContextMenu={confirmDelete || renaming ? undefined : handleContextMenu}
      // pi#83: hover/focus prefetch — warm the session view so the click's
      // mount takes the snapshot fast path.
      onPointerEnter={() => { setHovered(true); onPointerEnter?.(); }}
      onFocus={() => onFocus?.()}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => { setHovered(false); }}
      style={{
        height: SESSION_LIST_ITEM_HEIGHT,
        display: "flex",
        alignItems: "center",
        paddingLeft: depth > 0 ? depth * 12 + 14 : 14,
        paddingRight: 8,
        cursor: confirmDelete || renaming ? "default" : "pointer",
        background: confirmDelete
          ? "rgba(239,68,68,0.06)"
          : isSelected ? "var(--bg-selected)" : hovered ? "var(--bg-hover)" : "transparent",
        borderLeft: confirmDelete
          ? "2px solid #ef4444"
          : isSelected ? "2px solid var(--accent)" : "2px solid transparent",
        transition: "background 0.1s",
        opacity: deleting ? 0.5 : 1,
        gap: 6,
        overflow: "hidden",
      }}
    >
      {confirmDelete ? (
        /* ── Delete confirmation: same height, two flat buttons ── */
        <>
          <div style={{ flex: 1, minWidth: 0, fontSize: 12, color: "var(--text)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {t("sidebar.deleteSession", { title: title.slice(0, 22) + (title.length > 22 ? "…" : "") })}
          </div>
          <div style={{ display: "flex", gap: 5, flexShrink: 0 }}>
            <button
              onClick={handleDeleteConfirm}
              style={{
                display: "flex", alignItems: "center", justifyContent: "center", gap: 4,
                height: 30, padding: "0 11px",
                background: "#ef4444", border: "none",
                borderRadius: 6, color: "#fff",
                cursor: "pointer", fontSize: 12, fontWeight: 600,
                whiteSpace: "nowrap",
              }}
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
                <path d="M10 11v6M14 11v6" />
                <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2" />
              </svg>
              {t("sidebar.delete")}
            </button>
            <button
              onClick={handleDeleteCancel}
              style={{
                display: "flex", alignItems: "center", justifyContent: "center",
                height: 30, padding: "0 11px",
                background: "var(--bg)", border: "1px solid var(--border)",
                borderRadius: 6, color: "var(--text-muted)",
                cursor: "pointer", fontSize: 12, fontWeight: 500,
                whiteSpace: "nowrap",
              }}
            >
              {t("sidebar.cancel")}
            </button>
          </div>
        </>
      ) : renaming ? (
        /* ── Rename: input fills the same row ── */
        <input
          ref={inputRef}
          value={renameValue}
          onChange={(e) => setRenameValue(e.target.value)}
          onBlur={commitRename}
          onKeyDown={(e) => {
            if (e.key === "Enter") commitRename();
            if (e.key === "Escape") setRenaming(false);
          }}
          autoFocus
          style={{
            flex: 1,
            fontSize: 12,
            padding: "5px 8px",
            border: "1px solid var(--accent)",
            borderRadius: 5,
            outline: "none",
            background: "var(--bg)",
            color: "var(--text)",
            height: 30,
          }}
        />
      ) : (
        /* ── Normal view ── */
        <>
          {/* Subagent indicator for child sessions */}
          {depth > 0 && (
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
              <rect x="5" y="7" width="14" height="11" rx="2" />
              <path d="M9 11h.01M15 11h.01M9 15h6M12 7V4M10 4h4" />
            </svg>
          )}
          <div style={{ flex: 1, minWidth: 0 }}>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 5,
                minWidth: 0,
                fontSize: 12,
                fontWeight: isSelected ? 500 : 400,
                lineHeight: 1.4,
                color: isFiltered ? "var(--text-dim)" : "var(--text)",
                fontStyle: isFiltered ? "italic" : "normal",
              }}
              title={title}
            >
              {session.transient ? (
                // No file yet: a link would open a session no other tab can load.
                <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0 }}>
                  {title}
                </span>
              ) : (
                // A real link, so middle-click, Ctrl/Cmd-click and the browser's
                // own gestures open the session in another tab. A plain click (or
                // Enter) stays here and selects it as before — never `target`,
                // which would send plain clicks away too.
                <a
                  href={sessionDeepLink(session.id)}
                  style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0, color: "inherit", textDecoration: "none", cursor: "inherit" }}
                  onClick={(event) => {
                    // The row's handler must not select it again, nor on a modified click.
                    event.stopPropagation();
                    if (!isPlainActivation(event)) return;
                    event.preventDefault();
                    onClick();
                  }}
                >
                  {title}
                </a>
              )}
              {isFiltered && (
                <span
                  title={t("sidebar.filteredMarker")}
                  style={{
                    flexShrink: 0,
                    fontSize: 10,
                    lineHeight: 1.4,
                    padding: "0 5px",
                    color: "var(--text-dim)",
                    background: "var(--bg-hover)",
                    border: "1px solid var(--border)",
                    borderRadius: 4,
                    fontStyle: "normal",
                    textTransform: "lowercase",
                  }}
                >
                  {t("sidebar.filteredMarker")}
                </span>
              )}
              {/* Last element of the title line, pushed right: the title ellipsizes first. */}
              <HarnessMarker session={session} />
            </div>
            <div style={{ marginTop: 2, display: "flex", alignItems: "center", gap: 8, color: "var(--text-dim)", fontSize: 11, minWidth: 0 }}>
              {isAwaiting ? (
                <AwaitingInputIndicator permission={session.harness === "claude"} />
              ) : isRunning ? (
                <RunningSessionIndicator />
              ) : isUnread ? (
                <UnreadSessionIndicator />
              ) : (
                <span title={session.modified}>{formatRelativeTime(session.modified, locale)}</span>
              )}
              <span>
                {session.detailsPending ? "…" : t("sidebar.messagesCount", { count: session.messageCount })}
              </span>
              {session.isWorktree && session.branch && (
                <span
                  title={`Worktree: ${session.cwd}`}
                  style={{ display: "flex", alignItems: "center", gap: 3, color: "var(--accent)", minWidth: 0, overflow: "hidden" }}
                >
                  <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
                    <line x1="6" y1="3" x2="6" y2="15" />
                    <circle cx="18" cy="6" r="3" />
                    <circle cx="6" cy="18" r="3" />
                    <path d="M18 9a9 9 0 0 1-9 9" />
                  </svg>
                  <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{session.branch}</span>
                </span>
              )}
            </div>
          </div>

          {/* Collapse toggle — always visible when has children */}
          {hasChildren && (
            <button
              onClick={(e) => { e.stopPropagation(); onToggleCollapse?.(); }}
              title={t(collapsed ? "sidebar.expandSubagents" : "sidebar.collapseSubagents")}
              style={{
                display: "flex", alignItems: "center", justifyContent: "center",
                width: 20, height: 20, padding: 0, flexShrink: 0,
                background: "none", border: "none",
                color: "var(--text-dim)", cursor: "pointer",
                transform: collapsed ? "rotate(-90deg)" : "none",
                transition: "transform 0.15s",
              }}
            >
              <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="2 3.5 5 6.5 8 3.5" />
              </svg>
            </button>
          )}

          {/* Action buttons — shown on hover */}
          {hovered && !session.transient && (
            <div style={{ display: "flex", gap: 4, flexShrink: 0 }}>
              <button
                onClick={startRename}
                title={t("sidebar.rename")}
                style={{
                  display: "flex", alignItems: "center", justifyContent: "center",
                  width: 32, height: 32, padding: 0,
                  background: "var(--bg-hover)", border: "1px solid var(--border)",
                  borderRadius: 7, color: "var(--text-muted)",
                  cursor: "pointer", flexShrink: 0,
                  transition: "background 0.12s, color 0.12s, border-color 0.12s",
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.background = "var(--bg-selected)";
                  e.currentTarget.style.color = "var(--accent)";
                  e.currentTarget.style.borderColor = "rgba(37,99,235,0.35)";
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.background = "var(--bg-hover)";
                  e.currentTarget.style.color = "var(--text-muted)";
                  e.currentTarget.style.borderColor = "var(--border)";
                }}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z" />
                </svg>
              </button>
              {/* Open in new tab: the row's session in a browser tab of its own,
                  by the same link its title carries. This tab's selection, cwd
                  and file tabs stay as they are, and nothing is asked of the
                  server here: the new tab loads the session as a reload would.
                  Synchronous inside the click, so the browser counts it as the
                  user's and does not block it as a popup. Its return value says
                  nothing: with noopener the browser returns null whether the tab
                  opened or not. */}
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  window.open(sessionDeepLink(session.id), "_blank", "noopener,noreferrer");
                }}
                title={t("sidebar.openInNewTab")}
                style={{
                  display: "flex", alignItems: "center", justifyContent: "center",
                  width: 32, height: 32, padding: 0,
                  background: "var(--bg-hover)", border: "1px solid var(--border)",
                  borderRadius: 7, color: "var(--text-muted)",
                  cursor: "pointer", flexShrink: 0,
                  transition: "background 0.12s, color 0.12s, border-color 0.12s",
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.background = "var(--bg-selected)";
                  e.currentTarget.style.color = "var(--accent)";
                  e.currentTarget.style.borderColor = "rgba(37,99,235,0.35)";
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.background = "var(--bg-hover)";
                  e.currentTarget.style.color = "var(--text-muted)";
                  e.currentTarget.style.borderColor = "var(--border)";
                }}
              >
                {/* An arrow leaving a box: a session row's "Open in new tab"
                    (a browser tab, not a copy). */}
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M14 4h6v6" />
                  <path d="M20 4 11 13" />
                  <path d="M18 14v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4" />
                </svg>
              </button>
              <button
                onClick={handleDeleteClick}
                title={t("sidebar.deleteWithShiftClick")}
                style={{
                  display: "flex", alignItems: "center", justifyContent: "center",
                  width: 32, height: 32, padding: 0,
                  background: "var(--bg-hover)", border: "1px solid var(--border)",
                  borderRadius: 7, color: "var(--text-muted)",
                  cursor: "pointer", flexShrink: 0,
                  transition: "background 0.12s, color 0.12s, border-color 0.12s",
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.background = "rgba(239,68,68,0.08)";
                  e.currentTarget.style.color = "#ef4444";
                  e.currentTarget.style.borderColor = "rgba(239,68,68,0.35)";
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.background = "var(--bg-hover)";
                  e.currentTarget.style.color = "var(--text-muted)";
                  e.currentTarget.style.borderColor = "var(--border)";
                }}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="3 6 5 6 21 6" />
                  <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
                  <path d="M10 11v6M14 11v6" />
                  <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2" />
                </svg>
              </button>
              {/* Archive (wi pi#103): hidden for RUNNING sessions — running
                  activity must force-unarchive (see the sidebar effect), so
                  offering the action on a running row would be undone
                  immediately. Local set mutation only: the click stops
                  propagation (no row selection, no tab change — archiving an
                  open session keeps its chat tab) and adds the id to the
                  client-side archive set. The label is an e2e locator
                  contract: 归档 / 封存 / Archive, exactly. */}
              {!isRunning && (
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    onArchived?.(session.id);
                  }}
                  title={t("sidebar.archive")}
                  aria-label={t("sidebar.archive")}
                  style={{
                    display: "flex", alignItems: "center", justifyContent: "center",
                    width: 32, height: 32, padding: 0,
                    background: "var(--bg-hover)", border: "1px solid var(--border)",
                    borderRadius: 7, color: "var(--text-muted)",
                    cursor: "pointer", flexShrink: 0,
                    transition: "background 0.12s, color 0.12s, border-color 0.12s",
                  }}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.background = "var(--bg-selected)";
                    e.currentTarget.style.color = "var(--accent)";
                    e.currentTarget.style.borderColor = "rgba(37,99,235,0.35)";
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.background = "var(--bg-hover)";
                    e.currentTarget.style.color = "var(--text-muted)";
                    e.currentTarget.style.borderColor = "var(--border)";
                  }}
                >
                  {/* An archive box: a tray over a body with a slot. */}
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <rect x="3" y="3" width="18" height="4" rx="1" />
                    <path d="M5 7v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7" />
                    <path d="M10 12h4" />
                  </svg>
                </button>
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}
