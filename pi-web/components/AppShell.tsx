"use client";

import { useState, useCallback, useRef, useEffect, useLayoutEffect, useMemo, type RefObject } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useGlobalKeyboardShortcuts } from "@/hooks/useKeyboardShortcuts";
import { useBackgroundTasks, markTerminalNotified } from "@/hooks/useBackgroundTasks";
import { usePolyforge } from "@/hooks/usePolyforge";
import { SessionSidebar } from "./SessionSidebar";
import { ChatWindow } from "./ChatWindow";
import type { ChatScrollPosition } from "@/lib/chat-scroll-position";
import { FileViewer } from "./FileViewer";
import { BackgroundTasksPanel } from "./BackgroundTasksPanel";
import { PolyforgePanel } from "./PolyforgePanel";
import type { PolyforgePromptTarget } from "./PolyforgeMessages";
import { parsePolyforgePrompt, type PolyforgePrompt } from "@/lib/polyforge-messages-pure";
import { TabBar, type Tab } from "./TabBar";
import { openFileTab, saveFileViewerState, saveParkedFileViewerState, switchFileWorkspace, type ParkedFileWorkspace } from "./file-tab-state";
import { SettingsPanel, SettingsSectionIcon } from "./SettingsPanel";
import { ProjectTrustDialog, type ProjectTrustFailure } from "./ProjectTrustDialog";
import { BranchNavigator, hasSessionBranches } from "./BranchNavigator";
import { SystemPromptPanel } from "./SystemPromptPanel";
import { ToolDefinitionsPanel } from "./ToolDefinitionsPanel";
import { AgentSessionPanel } from "./AgentSessionPanel";
import { DebugPanel, DebugPanelBoundary } from "./DebugPanel";
import { SessionChatPanel } from "./SessionChatPanel";
import { TerminalPanel } from "./TerminalPanel";
import { newTerminalTab, restoreTerminalTabs, TERMINAL_TABS_KEY, type TerminalTab } from "./terminal-tab-state";
import { useTheme } from "@/hooks/useTheme";
import { useFontPreferences } from "@/hooks/useFontPreferences";
import { useI18n } from "@/hooks/useI18n";
import { useIsMobile, useIsNarrowMobile } from "@/hooks/useIsMobile";
import { useViewportHeight } from "@/hooks/useViewportHeight";
import { useResizablePanel } from "@/hooks/useResizablePanel";
import { useResizableSheet } from "@/hooks/useResizableSheet";
import { useAudio } from "@/hooks/useAudio";
import { SplitPaneLayout } from "./SplitPaneLayout";
import {
  openPane as openPaneOp,
  closePane as closePaneOp,
  setCompletionBadge,
  clearBadgeOnFocus,
  coalesceCompletionSound,
  isNewSessionTab,
  resolveBackgroundTasksSessionId,
  resolveSidebarSessionId,
  openNewSessionTab,
  hasSessionTab,
  reorderPaneTabs,
  NEW_SESSION_TAB_ID,
  type PaneTab,
} from "@/lib/pane-state";
import { readOpenPaneTabs, writeOpenPaneTabs } from "@/lib/pane-tab-state";
import { reorderById, syncTabOrder, applyTabOrder } from "@/lib/tab-order";
import { projectDisplayNameForPath } from "@/lib/project-groups";
import { listCustomDirectories } from "@/lib/custom-directories";
import { copyText } from "@/lib/clipboard";
import { sendAgentCommand } from "@/lib/agent-client";
import { getFileName } from "@/lib/file-paths";
import { buildAtMentionText, buildFileAtMentionsText, buildFileLineMentionText } from "@/lib/file-fuzzy";
import {
  claimExtensionAttentionNotification,
  shouldShowBrowserNotification,
  showBrowserNotification,
} from "@/lib/browser-notifications";
import { setupPushSubscription } from "@/lib/push-client";
import { startPageVisibilityTracking } from "@/lib/page-debug";
import type { DebugClientSession } from "@/lib/debug-types";
import { getInitialNavigation, linkedSessionId, withTabOpen } from "@/lib/initial-navigation";
import { clearTabOpenSession, getTabOpen, setTabOpenNewSession, setTabOpenSession } from "@/lib/tab-session";
import { mergeCatalogRow } from "./session-catalog-helpers";
import { appendDraftText, rekeyDraft } from "@/lib/draft-store";
import {
  clearLastOpen,
  getLastOpenSession,
  setLastOpenSession,
  workspaceKeyOf,
} from "@/lib/workspace-memory";
import {
  getDefaultRightPanelWidth,
  getPolyforgeDockMaxWidth,
  getRightPanelMaxWidth,
  getSidebarMaxWidth,
  POLYFORGE_DOCK_DEFAULT_WIDTH,
  POLYFORGE_DOCK_MAX_WIDTH,
  POLYFORGE_DOCK_MIN_WIDTH,
  RIGHT_PANEL_FALLBACK_WIDTH,
  RIGHT_PANEL_MAX_WIDTH,
  RIGHT_PANEL_MIN_WIDTH,
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
} from "@/lib/panel-layout";
import type { BackgroundTasksClientEvent, BlockingExtensionUiRequest, SessionInfo, SessionTreeNode } from "@/lib/types";
import type { McpErrorResponse, ProjectTrustStatus } from "@/lib/api-types";
import type { AgentEndInfo } from "@/hooks/useAgentSession";
import type { ChatInputHandle } from "./ChatInput";
import type { SessionStatsInfo } from "@/lib/pi-types";
import { formatActiveDuration, formatCompactCount, formatCostShort } from "@/lib/session-stats-format";
import type { FileViewerState } from "@/lib/file-viewer-state";
import { PRESET_READ_ONLY, type ToolEntry } from "@/lib/tool-presets";
import { getAgentsBadge, getSessionFamily, isHiddenRelation } from "@/lib/session-family";
import { getLastSettingsSection, settingsSectionRequiresProject, type SettingsSection } from "@/lib/settings-navigation";
import { focusIfLost } from "@/lib/stacked-dialog";

type SessionCopyField = "file" | "id" | "projectDir" | "gitBranch" | "gitWorktree";
type AutoNameStatus =
  | { kind: "idle" }
  | { kind: "naming" }
  | { kind: "success" }
  | { kind: "error"; message: string };

// app/globals.css: 36px, 44px on a phone or touch screen.
const TOP_BAR_ICON_BUTTON_SIZE = "var(--top-bar-height)";
const AGENT_PANEL_WIDTH = 420;
// pi's built-in tools that never change a file; any other tool may (#1144).
const READ_ONLY_TOOL_NAMES = new Set(PRESET_READ_ONLY);
const TOOL_END_REFRESH_MS = 1000;

// Split-view preference (pi#27): tab mode is the DEFAULT; the stored value
// only exists to honor an explicit opt-out. Absent/corrupt → default ON.
const SPLIT_VIEW_ENABLED_STORAGE_KEY = "pi-web:split-view-enabled";
/** the desktop dock's persisted pin ("1" = pinned). */
const PF_DOCK_PINNED_KEY = "pi-web:polyforge-dock-pinned";
/** The mobile breakpoint (hooks/useIsMobile.ts, app/globals.css). */
const MOBILE_MEDIA_QUERY = "(max-width: 640px)";

function readSplitPaneEnabled(): boolean {
  if (typeof window === "undefined") return true;
  try {
    return window.localStorage.getItem(SPLIT_VIEW_ENABLED_STORAGE_KEY) !== "false";
  } catch {
    return true;
  }
}

function writeSplitPaneEnabled(enabled: boolean): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(SPLIT_VIEW_ENABLED_STORAGE_KEY, enabled ? "true" : "false");
  } catch {
    // ignore storage quota / privacy-mode errors
  }
}

function parkedNewSessionDraftKey(cwd: string): string {
  return `parked-new:${cwd}`;
}

/** Panel tab id of the last sub-agent tab, or null when there is none. */
function lastAgentTabId(tabs: readonly { sessionId: string }[]): string | null {
  const last = tabs.at(-1);
  return last ? `agent:${last.sessionId}` : null;
}

export function AppShell() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const [initialNavigation, setInitialNavigation] = useState(() => getInitialNavigation(searchParams));
  // Keep the system-theme subscription mounted for the lifetime of the app.
  useTheme();
  // Restore fonts even when Settings and the chat composer have not been opened.
  useFontPreferences();
  const { locale, t: translate } = useI18n();
  const isMobile = useIsMobile();
  const isNarrowMobile = useIsNarrowMobile();
  useViewportHeight();

  // Split-pane state (pi#4): ordered pane tabs and the focused pane id. Pane
  // widths are auto-computed from the measured pane-area width (pi#20); the
  // old manual visible-pane cap is gone.
  // Split view is the DEFAULT (pi#27, user-confirmed): pane routing and the
  // embedded pane headers engage immediately on first visit. A persisted
  // disable preference (localStorage pi-web:split-view-enabled = "false")
  // keeps the classic single-chat layout for users who opted out.
  const [splitPaneEnabled, setSplitPaneEnabledState] = useState(readSplitPaneEnabled);
  // pi#27: persist explicit toggles; collapse paths write false so a user who
  // closed everything keeps the classic layout on next entry, while the
  // default (absent value) stays ON.
  const setSplitPaneEnabled = useCallback((enabled: boolean) => {
    setSplitPaneEnabledState(enabled);
    writeSplitPaneEnabled(enabled);
  }, []);
  const [paneTabs, setPaneTabs] = useState<PaneTab[]>([]);
  const [focusedPaneId, setFocusedPaneId] = useState<string | null>(null);
  // Pane-tab restore (this wi): one-shot latch for the persisted-strip
  // restore effect below. It also gates the pi#27 entry fallback (which must
  // not fire the sentinel new-session tab while a strip may still restore)
  // and the persistence writer (so the mount-time empty strip never clobbers
  // the record). The mirrored state gives those consumers reactivity.
  const paneRestoreAttemptedRef = useRef(false);
  const [paneRestoreAttempted, setPaneRestoreAttempted] = useState(false);
  const lastSoundAtRef = useRef(0);
  const splitPaneLayoutRef = useRef<{ scrollPaneIntoView: (id: string) => void } | null>(null);

  // A newly opened pane (sidebar routing or the split-enable first pane) must
  // scroll into view: panes are appended at the tail of paneTabs, so when the
  // count grows the last tab is the new pane.
  const paneCountRef = useRef(0);
  useEffect(() => {
    if (paneTabs.length > paneCountRef.current && paneTabs.length > 0) {
      const newPaneId = paneTabs[paneTabs.length - 1].sessionId;
      splitPaneLayoutRef.current?.scrollPaneIntoView(newPaneId);
    }
    paneCountRef.current = paneTabs.length;
  }, [paneTabs]);

  // Once the user has granted notification permission, register a Web Push
  // subscription so the server can notify backgrounded PWAs (notably iOS,
  // which suspends page JS and never receives the SSE completion event).
  useEffect(() => {
    if (typeof window === "undefined" || !("Notification" in window)) return;
    if (Notification.permission !== "granted") return;
    void setupPushSubscription(locale);
  }, [locale]);
  // Audio ownership lives here (not in ChatWindow) so the completion tone can
  // also fire for tasks finishing in a non-active workspace whose ChatWindow
  // is not mounted. ChatWindow receives the audio callbacks as props.
  const { soundEnabled, onSoundToggle, playDoneSound, unlockAudio, holdAudio, soundEnabledRef } = useAudio();
  const [quoteSelectionEnabled, setQuoteSelectionEnabled] = useState(false);
  useEffect(() => {
    try {
      setQuoteSelectionEnabled(localStorage.getItem("pi-quote-selection-enabled") === "true");
    } catch {
      // Browser storage is best-effort.
    }
  }, []);
  const handleQuoteSelectionChange = useCallback((enabled: boolean) => {
    setQuoteSelectionEnabled(enabled);
    try {
      localStorage.setItem("pi-quote-selection-enabled", String(enabled));
    } catch {
      // Keep the current page usable when storage is unavailable.
    }
  }, []);
  const notifiedAttentionRequestIdsRef = useRef(new Set<string>());
  // Sessions that finished while the tab was out of sight (#1484): the browser
  // Notification fires for the selected session, which the sidebar's unread
  // markers exclude ("already looking" — false while hidden). The favicon keeps
  // their badge until the tab is opened again; never persisted, never dotted.
  const [hiddenFinishedSessionIds, setHiddenFinishedSessionIds] = useState<Set<string>>(new Set());
  const handleBackgroundTaskDone = useCallback(() => {
    if (soundEnabledRef.current) playDoneSound();
  }, [playDoneSound, soundEnabledRef]);
  const [selectedSession, setSelectedSession] = useState<SessionInfo | null>(null);
  // Latest selection readable from async callbacks whose captured state is
  // stale (e.g. a delete that completes after the user navigated away).
  const selectedSessionRef = useRef(selectedSession);
  selectedSessionRef.current = selectedSession;
  const [bgPanelOpen, setBgPanelOpen] = useState(false);
  const [bgSelectedTaskId, setBgSelectedTaskId] = useState<string | null>(null);
  // pi#108: the Polyforge dock panel shares the bg-tasks slot; the two are
  // mutually exclusive (each open handler closes the other).
  const [pfPanelOpen, setPfPanelOpen] = useState(false);
  // the desktop dock's pin, persisted globally under
  // PF_DOCK_PINNED_KEY ("1" = pinned; absent/invalid = unpinned). While
  // pinned the outside-press dismissal below is not installed; the topbar
  // toggle and the panel's close still close the dock and keep the pin; a
  // load while pinned reopens the dock on a desktop viewport. The mobile
  // sheet has no pin.
  const [pfDockPinned, setPfDockPinned] = useState(false);
  // pi#28: the background-tasks panel follows the FOCUSED pane's session. In
  // split view pane focus only updates focusedPaneId (never selectedSession),
  // so the old selectedSession?.id read left the panel permanently
  // "unavailable" while a real session pane was focused.
  const lastSessionPaneIdRef = useRef<string | null>(null);
  useEffect(() => {
    if (focusedPaneId && !isNewSessionTab(focusedPaneId)) {
      lastSessionPaneIdRef.current = focusedPaneId;
    }
  }, [focusedPaneId]);
  const activeBgSessionId = useMemo(
    () => resolveBackgroundTasksSessionId({
      splitPaneEnabled: splitPaneEnabled && !isMobile,
      focusedPaneId,
      selectedSessionId: selectedSession?.id ?? null,
      // Read at render; the ref is intentionally not a dependency.
      lastSessionPaneId: lastSessionPaneIdRef.current,
      paneTabs,
    }),
    [splitPaneEnabled, isMobile, focusedPaneId, selectedSession?.id, paneTabs],
  );
  const activeBgSessionIdRef = useRef<string | null>(null);
  activeBgSessionIdRef.current = activeBgSessionId;
  // Split-view sidebar follow (this wi): the sidebar's highlight derives from
  // the FOCUSED pane's session with the same inputs as the background-tasks
  // memo above, so the two surfaces always agree on the "current" session.
  // Pane focus still never writes selectedSession (pi#33) — only the
  // highlight follows it — and when split view closes (or on mobile) the
  // derivation returns the classic selection, so the highlight falls back
  // with no further user action.
  const sidebarSessionId = useMemo(
    () => resolveSidebarSessionId({
      splitPaneEnabled: splitPaneEnabled && !isMobile,
      focusedPaneId,
      selectedSessionId: selectedSession?.id ?? null,
      // Read at render; the ref is intentionally not a dependency.
      lastSessionPaneId: lastSessionPaneIdRef.current,
      paneTabs,
    }),
    [splitPaneEnabled, isMobile, focusedPaneId, selectedSession?.id, paneTabs],
  );
  const {
    state: bgTasksState,
    logs: bgTaskLogs,
    refresh: refreshBgTasks,
    applyEvent: applyBgTasksEvent,
    fetchLogs: fetchBgTaskLogs,
    killTask: killBgTask,
  } = useBackgroundTasks(activeBgSessionId, bgPanelOpen);
  const {
    pfPresent,
    opsStatus: pfOpsStatus,
    opsError: pfOpsError,
    cards: pfCards,
    cardsError: pfCardsError,
    runningCount: pfRunningCount,
    selectedWi: pfSelectedWi,
    selectedProfile: pfSelectedProfile,
    watchStatus: pfWatchStatus,
    select: selectPolyforgeJob,
    refreshStatus: refreshPolyforgeStatus,
    activeTab: pfActiveTab,
    setActiveTab: setPfActiveTab,
    workTab: pfWorkTab,
    setWorkTab: setPfWorkTab,
    workItemsRunning: pfWorkItemsRunning,
    setWorkItemsRunning: setPfWorkItemsRunning,
    messagesCapable: pfMessagesCapable,
    messagesReason: pfMessagesReason,
    messagesError: pfMessagesError,
    messagesCell: pfMessagesCell,
    conversations: pfConversations,
    contacts: pfContacts,
    groups: pfGroups,
    membershipAdmin: pfMembershipAdmin,
    users: pfUsers,
    changeMembership: changePfMembership,
    unreadTotal: pfUnreadTotal,
    selectedConversation: pfSelectedConversation,
    selectConversation: selectPfConversation,
    selectedContact: pfSelectedContact,
    selectContact: selectPfContact,
    messages: pfMessages,
    initialPageLoaded: pfMessagesInitialPageLoaded,
    blocked: pfMessagesBlocked,
    loadingOlder: pfMessagesLoadingOlder,
    loadOlder: loadPfOlderMessages,
    acknowledgeExplicitly: acknowledgePfMessagesExplicitly,
    sendMessage: sendPfMessage,
    analyzeMessage: analyzePfMessage,
    analyses: pfAnalyses,
    openAnchor: openPfAnchor,
    decisionsFeed: pfDecisionsFeed,
    dismissDecisions: dismissPfDecisions,
    taskRequest: pfTaskRequest,
    clearTaskRequest: clearPfTaskRequest,
    attentionCount: pfAttentionCount,
    openNextAttention: openPfNextAttention,
  } = usePolyforge(pfPanelOpen);

  // pi#113: while the DESKTOP dock is open, a pointer press anywhere outside
  // its own DOM dismisses it — and the press still reaches its target: no
  // preventDefault/stopPropagation, and the dock is an overlay, so nothing
  // under it unmounts between press and release (mem_Y6pX8exg: replacing the
  // press target mid-click eats the click). The topbar toggle is exempt: its
  // own click already toggles the dock closed, and a pointerdown dismissal
  // here would flip that functional update into a close-then-reopen. Mobile
  // keeps its backdrop tap (unchanged); the listener exists only while the
  // desktop dock is open.
  // not while a Messages 「@」 run is in flight — its answer
  // may land under its message, which closing the dock would lose.
  const pfPromptBusyRef = useRef(false);
  const setPfPromptBusy = useCallback((busy: boolean) => {
    pfPromptBusyRef.current = busy;
  }, []);
  useEffect(() => {
    if (!pfPanelOpen || isMobile || pfDockPinned) return;
    const dismissOnOutsidePress = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Element)) return;
      if (target.closest("[data-pf-dock], [data-pf-toggle]")) return;
      if (pfPromptBusyRef.current) return;
      setPfPanelOpen(false);
    };
    document.addEventListener("pointerdown", dismissOnOutsidePress);
    return () => document.removeEventListener("pointerdown", dismissOnOutsidePress);
  }, [pfPanelOpen, isMobile, pfDockPinned]);

  // one-shot at mount — read the pin, and reopen the dock when
  // pinned on a desktop viewport (the shared slot: bg-tasks closes first).
  // The viewport is read from the media query itself: useIsMobile's first
  // snapshot may not reflect it yet at mount.
  useEffect(() => {
    let pinned = false;
    try {
      pinned = window.localStorage.getItem(PF_DOCK_PINNED_KEY) === "1";
    } catch {
      // storage unavailable — unpinned
    }
    if (!pinned) return;
    setPfDockPinned(true);
    if (window.matchMedia(MOBILE_MEDIA_QUERY).matches) return;
    setBgPanelOpen(false);
    setPfPanelOpen(true);
  }, []);

  const togglePfDockPin = useCallback(() => {
    const next = !pfDockPinned;
    setPfDockPinned(next);
    try {
      if (next) window.localStorage.setItem(PF_DOCK_PINNED_KEY, "1");
      else window.localStorage.removeItem(PF_DOCK_PINNED_KEY);
    } catch {
      // best effort persistence
    }
  }, [pfDockPinned]);

  // Live background-task events arrive per session pane (split view feeds every
  // pane's stream; the classic single chat feeds its own). Panel state only
  // accepts events from the session the panel currently follows; the terminal
  // notification path stays session-wide (pi#28).
  const handleBackgroundTasksEvent = useCallback((sourceSessionId: string, event: BackgroundTasksClientEvent) => {
    const isActiveSession = sourceSessionId === activeBgSessionIdRef.current;
    if (isActiveSession) applyBgTasksEvent(event);
    if (event.type !== "background_task_terminal") return;
    const task = event.task;
    if (!markTerminalNotified(task.id)) return;
    if (isActiveSession) setBgSelectedTaskId(task.id);
    if (shouldShowBrowserNotification()) {
      void showBrowserNotification({
        title: translate("bgTasks.notification.title"),
        body: translate("bgTasks.notification.body")
          .replace("{name}", task.name || task.id)
          .replace("{status}", translate(`bgTasks.status.${task.status}`)),
        sessionUrl: `/?session=${encodeURIComponent(sourceSessionId)}`,
        tag: `pi-bg-task:${task.id}`,
        onClick: () => {
          window.focus();
          // Shared dock slot: the jump must land on a visible bg-tasks panel,
          // so it closes the Polyforge dock it would otherwise cover (B-2).
          setBgPanelOpen(true);
          setPfPanelOpen(false);
          setBgSelectedTaskId(task.id);
        },
      });
    }
  }, [applyBgTasksEvent, translate]);
  const [sessionCatalog, setSessionCatalog] = useState<SessionInfo[]>([]);
  // Pane-tab restore (this wi): the sidebar reports its session list only
  // after the initial load settles, so this flag means "the live catalog is
  // final for gating purposes" — an empty but settled list still flips it,
  // which a not-yet-loaded list never does.
  const [sessionCatalogReported, setSessionCatalogReported] = useState(false);
  const handleSessionsChange = useCallback((sessions: SessionInfo[]) => {
    setSessionCatalog(sessions);
    setSessionCatalogReported(true);
    // The sidebar hydrates metadata after the selected session has already
    // mounted. Merge that update into the active session without changing the
    // ChatWindow key or restarting its history load. (upstream, pi#56 merge)
    setSelectedSession((current) => {
      if (!current) return current;
      const refreshed = sessions.find((session) => session.id === current.id);
      return refreshed ? mergeCatalogRow(current, refreshed) : current;
    });
  }, []);
  // Sidebar-detected external write (TUI / another pi process) targeting the
  // selected session; converted into a keyed signal that ChatWindow consumes
  // to reload that session from disk exactly once per key.
  const [externalSessionChange, setExternalSessionChange] = useState<{ sessionId: string; key: number } | null>(null);
  const handleExternalSessionChange = useCallback((sessionId: string) => {
    setExternalSessionChange((previous) => ({ sessionId, key: (previous?.key ?? 0) + 1 }));
  }, []);
  const sessionsWithSelection = useMemo(() => {
    if (!selectedSession) return sessionCatalog;
    return [
      ...sessionCatalog.filter((session) => session.id !== selectedSession.id),
      selectedSession,
    ];
  }, [selectedSession, sessionCatalog]);
  const activeSessionFamily = useMemo(
    () => getSessionFamily(sessionsWithSelection, selectedSession?.id),
    [selectedSession?.id, sessionsWithSelection],
  );
  const [runningSessionIds, setRunningSessionIds] = useState<Set<string>>(() => new Set());
  const handleRunningSessionIdsChange = useCallback((ids: Set<string>) => {
    setRunningSessionIds((previous) => {
      if (previous.size === ids.size && [...ids].every((id) => previous.has(id))) return previous;
      return ids;
    });
  }, []);
  const hasSubagentSessions = Boolean(activeSessionFamily?.subagents.length);
  const agentsBadge = activeSessionFamily ? getAgentsBadge(activeSessionFamily.subagents, runningSessionIds) : null;
  const agentsButtonLabel = agentsBadge?.running
    ? translate("agentSwitcher.titleRunning", { count: agentsBadge.count })
    : translate("agentSwitcher.title");
  // The temporary id distinguishes consecutive fresh composers in one cwd.
  const [newSessionCwd, setNewSessionCwd] = useState<string | null>(null);
  const [newSessionDraftId, setNewSessionDraftId] = useState("initial");
  const activeNewSessionDraftKeyRef = useRef<string | null>(null);
  const [initialCwdStatus, setInitialCwdStatus] = useState<"idle" | "validating" | "ready" | "error">(
    () => initialNavigation.requestedCwd ? "validating" : "idle",
  );
  const [initialCwdError, setInitialCwdError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [sessionKey, setSessionKey] = useState(0);
  const sessionScrollPositionsRef = useRef(new Map<string, ChatScrollPosition>());
  const handleSessionScrollPositionChange = useCallback((sessionId: string, position: ChatScrollPosition) => {
    sessionScrollPositionsRef.current.set(sessionId, position);
  }, []);
  const [searchTarget, setSearchTarget] = useState<{ sessionId: string; entryId: string; blockIndex?: number } | null>(null);
  const handleSearchTargetHandled = useCallback((target: { sessionId: string; entryId: string }) => {
    setSearchTarget((current) => current === target ? null : current);
  }, []);
  const [explorerRefreshKey, setExplorerRefreshKey] = useState(0);
  const [settingsSection, setSettingsSection] = useState<SettingsSection | null>(null);
  // The plain Settings button opens a phone's Settings on its list of sections; shortcuts open theirs.
  const [settingsOpenAtHome, setSettingsOpenAtHome] = useState(false);
  const [modelsRefreshKey, setModelsRefreshKey] = useState(0);
  const [projectTrust, setProjectTrust] = useState<ProjectTrustStatus | null>(null);
  const [projectTrustDialogOpen, setProjectTrustDialogOpen] = useState(false);
  const [projectTrustBusy, setProjectTrustBusy] = useState(false);
  const [projectTrustError, setProjectTrustError] = useState<ProjectTrustFailure | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(() => !initialNavigation.sidebarCollapsed);
  const desktopSidebarOpenRef = useRef(!initialNavigation.sidebarCollapsed);
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const [historyExportSessionId, setHistoryExportSessionId] = useState<string | null>(null);
  const [rightPanelExpanded, setRightPanelExpanded] = useState(false);
  const rightPanelFullWidth = rightPanelOpen && rightPanelExpanded && !isMobile;
  useEffect(() => {
    if (!rightPanelOpen || isMobile) setRightPanelExpanded(false);
  }, [rightPanelOpen, isMobile]);
  // pi#8: the in-panel history export never outlives the panel closing, and a
  // file tab switch takes the panel back from the export view.
  useEffect(() => {
    if (!rightPanelOpen) setHistoryExportSessionId(null);
  }, [rightPanelOpen]);
  const [mobileToolbarMoreOpen, setMobileToolbarMoreOpen] = useState(false);
  const moreControlsLabel = mobileToolbarMoreOpen ? translate("chat.close") : translate("chat.moreControls");
  const [mobileSidebarReady, setMobileSidebarReady] = useState(false);
  const sidebarWidthRef = useRef(SIDEBAR_DEFAULT_WIDTH);
  const rightPanelWidthRef = useRef(RIGHT_PANEL_FALLBACK_WIDTH);
  const getResponsiveRightPanelWidth = useCallback(
    () => typeof window === "undefined"
      ? RIGHT_PANEL_FALLBACK_WIDTH
      : getDefaultRightPanelWidth(window.innerWidth),
    [],
  );
  const getResponsiveSidebarMaxWidth = useCallback(
    () => typeof window === "undefined"
      ? SIDEBAR_MAX_WIDTH
      : getSidebarMaxWidth({
        viewportWidth: window.innerWidth,
        rightPanelOpen,
        rightPanelWidth: rightPanelWidthRef.current,
      }),
    [rightPanelOpen],
  );
  const getResponsiveRightPanelMaxWidth = useCallback(
    () => typeof window === "undefined"
      ? RIGHT_PANEL_MAX_WIDTH
      : getRightPanelMaxWidth({
        viewportWidth: window.innerWidth,
        sidebarOpen,
        sidebarWidth: sidebarWidthRef.current,
      }),
    [sidebarOpen],
  );
  const sidebarResizer = useResizablePanel({
    ariaLabel: translate("layout.resizeSidebar"),
    cssVariable: "--sidebar-width",
    defaultWidth: SIDEBAR_DEFAULT_WIDTH,
    getMaxWidth: getResponsiveSidebarMaxWidth,
    growthDirection: "right",
    maxWidth: SIDEBAR_MAX_WIDTH,
    minWidth: SIDEBAR_MIN_WIDTH,
    storageKey: "pi-sidebar-width",
    widthRef: sidebarWidthRef,
  });
  const rightPanelResizer = useResizablePanel({
    ariaLabel: translate("layout.resizeFilePanel"),
    cssVariable: "--right-panel-width",
    defaultWidth: RIGHT_PANEL_FALLBACK_WIDTH,
    getDefaultWidth: getResponsiveRightPanelWidth,
    getMaxWidth: getResponsiveRightPanelMaxWidth,
    growthDirection: "left",
    maxWidth: RIGHT_PANEL_MAX_WIDTH,
    minWidth: RIGHT_PANEL_MIN_WIDTH,
    storageKey: "pi-right-panel-width",
    widthRef: rightPanelWidthRef,
  });
  // pi#117: the desktop Polyforge dock opens at 460px and resizes via its
  // left-edge handle, clamped to [340, 90vw], persisted globally under one
  // localStorage key (absent/invalid reads as 460). The bg-tasks dock in the
  // same slot stays fixed at 340 — the two widths are not coupled. pi#120
  // ended the "mobile 62% sheet is unchanged" half of this comment: the
  // mobile sheets now resize through their own shared mechanism below.
  const pfDockWidthRef = useRef(POLYFORGE_DOCK_DEFAULT_WIDTH);
  const getResponsivePfDockMaxWidth = useCallback(
    () => typeof window === "undefined"
      ? POLYFORGE_DOCK_MAX_WIDTH
      : getPolyforgeDockMaxWidth(window.innerWidth),
    [],
  );
  const pfDockResizer = useResizablePanel({
    ariaLabel: translate("polyforge.resizeDock"),
    cssVariable: "--pf-dock-width",
    defaultWidth: POLYFORGE_DOCK_DEFAULT_WIDTH,
    getMaxWidth: getResponsivePfDockMaxWidth,
    growthDirection: "left",
    maxWidth: POLYFORGE_DOCK_MAX_WIDTH,
    minWidth: POLYFORGE_DOCK_MIN_WIDTH,
    storageKey: "pi-web:polyforge-dock-width",
    widthRef: pfDockWidthRef,
  });
  // pi#120: the mobile sheets (Polyforge + bg-tasks, same dock slot) resize
  // by dragging their top edge — one shared mechanism, two consumers, each
  // persisting its own height (absent/invalid/out-of-range reads as 62%,
  // clamped to [30%, 90%], never dismisses: the backdrop tap stays the only
  // dismiss gesture). Desktop keeps its fixed heights — these resizers
  // serve the mobile sheets only (their refs attach to nothing while the
  // desktop branch renders).
  const pfSheetResizer = useResizableSheet({
    ariaLabel: translate("polyforge.resizeSheet"),
    storageKey: "pi-web:polyforge-sheet-height",
  });
  const bgSheetResizer = useResizableSheet({
    ariaLabel: translate("bgTasks.resizeSheet"),
    storageKey: "pi-web:bg-tasks-sheet-height",
  });
  const reclampSidebarWidth = sidebarResizer.reclampWidth;
  const reclampRightPanelWidth = rightPanelResizer.reclampWidth;
  // On mobile the sidebar is an overlay drawer; hide it by default so the chat
  // is visible on load. Runs once the breakpoint resolves after hydration.
  // Mobile drawer actions must not change the remembered desktop preference.
  useEffect(() => {
    setSidebarOpen(isMobile ? false : desktopSidebarOpenRef.current);
  }, [isMobile]);
  useEffect(() => {
    setMobileSidebarReady(true);
  }, []);
  useEffect(() => {
    if (!rightPanelOpen) return;
    reclampSidebarWidth();
    reclampRightPanelWidth();
  }, [reclampRightPanelWidth, reclampSidebarWidth, rightPanelOpen]);
  const chatInputRef = useRef<ChatInputHandle | null>(null);
  // pi#33: split panes each own an imperative composer handle keyed by pane
  // id. A single shared ref cannot serve N panes: without a per-pane handle,
  // "Edit from here" (replaceMessage), @-mention insertText, addImages and
  // queued-message restore were silent no-ops in split view — the reported
  // desktop bug where the composer stayed empty after the click.
  const paneChatInputRefsRef = useRef<Map<string, RefObject<ChatInputHandle | null>>>(new Map());
  const getPaneChatInputRef = useCallback((paneId: string): RefObject<ChatInputHandle | null> => {
    const refs = paneChatInputRefsRef.current;
    let paneRef = refs.get(paneId);
    if (!paneRef) {
      paneRef = { current: null };
      refs.set(paneId, paneRef);
    }
    return paneRef;
  }, []);
  // Immediate cleanup at the close site (pi#33); the paneTabs hygiene effect
  // below sweeps the remaining removal paths (supersede/adoption).
  const releasePaneChatInputRef = useCallback((paneId: string) => {
    paneChatInputRefsRef.current.delete(paneId);
  }, []);
  // Registry hygiene (pi#33): a pane id with no open tab releases its
  // composer-handle entry, so closed panes, the superseded sentinel tab and
  // the sentinel adopted by a created session never keep stale handles.
  useEffect(() => {
    const open = new Set(paneTabs.map((tab) => tab.sessionId));
    for (const paneId of paneChatInputRefsRef.current.keys()) {
      if (!open.has(paneId)) paneChatInputRefsRef.current.delete(paneId);
    }
  }, [paneTabs]);
  // AppShell-level imperative callers address the FOCUSED pane's composer
  // when split view is active (inserts must never leak into another pane),
  // falling back to the classic shared handle outside split view.
  const resolveChatInputHandle = useCallback((): ChatInputHandle | null => {
    if (splitPaneEnabled && !isMobile && focusedPaneId) {
      return paneChatInputRefsRef.current.get(focusedPaneId)?.current ?? null;
    }
    return chatInputRef.current;
  }, [splitPaneEnabled, isMobile, focusedPaneId]);
  // the Messages 「@」 action binds its fill target here, at
  // click time, through the same derivation as resolveChatInputHandle (the
  // focused split pane, else the classic/mobile composer); null when there is
  // no composer. The binding is the composer's draft key (its session) and,
  // in split view, its pane. When the answer arrives the bound pane must
  // still be open (else false: shown under the message, never redirected);
  // the composer slot that still shows that draft key gets the text, else
  // the session's stored draft does. Text only — nothing is ever sent.
  const paneTabsRef = useRef(paneTabs);
  paneTabsRef.current = paneTabs;
  const bindPromptTarget = useCallback((): PolyforgePromptTarget | null => {
    const handle = resolveChatInputHandle();
    const draftKey = handle?.draftKey();
    if (!handle || !draftKey) return null;
    const paneId = splitPaneEnabled && !isMobile && focusedPaneId ? focusedPaneId : null;
    const name = paneId
      ? paneTabsRef.current.find((tab) => tab.sessionId === paneId)?.label ?? ""
      : selectedSession
        ? selectedSession.name || selectedSession.firstMessage || selectedSession.id.slice(0, 12)
        : translate("tabs.new");
    return {
      name,
      fill: (prompt: string) => {
        if (paneId && !paneTabsRef.current.some((tab) => tab.sessionId === paneId)) return false;
        const current = paneId ? paneChatInputRefsRef.current.get(paneId)?.current : chatInputRef.current;
        if (current && current.draftKey() === draftKey) current.appendText(prompt);
        else appendDraftText(draftKey, prompt);
        return true;
      },
    };
  }, [resolveChatInputHandle, splitPaneEnabled, isMobile, focusedPaneId, selectedSession, translate]);
  // The 「@」 request: one POST to the prompt route, the one sanitizer, and
  // pf's refusal text with the below-floor marker. The answer exists only in
  // the panel's component memory; nothing is stored here.
  const requestPfPrompt = useCallback(async (messageId: string): Promise<PolyforgePrompt> => {
    const response = await fetch("/api/polyforge/messages/prompt", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ message_id: messageId }),
    });
    const body = await response.json().catch(() => ({})) as { prompt?: unknown; error?: string; upgrade?: boolean };
    const prompt = response.ok && !body.error ? parsePolyforgePrompt(body.prompt) : null;
    if (!prompt) {
      throw Object.assign(new Error(body.error ?? "the prompt is unavailable"), { upgrade: body.upgrade === true });
    }
    return prompt;
  }, []);
  // the decision feed's surface, shared by the dock and the sheet.
  const pfDecisionsSurface = {
    feed: pfDecisionsFeed,
    onDismiss: dismissPfDecisions,
    taskRequest: pfTaskRequest,
    onTaskRequestHandled: clearPfTaskRequest,
  };
  const pfMessagesSurface = {
    pfPresent,
    capable: pfMessagesCapable,
    reason: pfMessagesReason,
    conversations: pfConversations,
    contacts: pfContacts,
    groups: pfGroups,
    membershipAdmin: pfMembershipAdmin,
    users: pfUsers,
    onChangeMembership: changePfMembership,
    error: pfMessagesError,
    cell: pfMessagesCell,
    selectedConversation: pfSelectedConversation,
    onSelectConversation: selectPfConversation,
    selectedContact: pfSelectedContact,
    onSelectContact: selectPfContact,
    messages: pfMessages,
    initialPageLoaded: pfMessagesInitialPageLoaded,
    blocked: pfMessagesBlocked,
    loadingOlder: pfMessagesLoadingOlder,
    onLoadOlder: loadPfOlderMessages,
    onAcknowledge: acknowledgePfMessagesExplicitly,
    onSend: sendPfMessage,
    onAnalyze: analyzePfMessage,
    analyses: pfAnalyses,
    onOpenAnchor: openPfAnchor,
    unreadTotal: pfUnreadTotal,
    bindPromptTarget,
    onPrompt: requestPfPrompt,
    onPromptBusyChange: setPfPromptBusy,
  };
  const [pendingQuotePrompt, setPendingQuotePrompt] = useState<{ sessionId: string; text: string } | null>(null);
  const topBarRef = useRef<HTMLDivElement>(null);
  const mobileToolbarRef = useRef<HTMLDivElement>(null);
  // Branch navigator state — populated by ChatWindow via onBranchDataChange
  const [branchTree, setBranchTree] = useState<SessionTreeNode[]>([]);
  const [branchActiveLeafId, setBranchActiveLeafId] = useState<string | null>(null);
  const [branchSwitchLocked, setBranchSwitchLocked] = useState(false);
  const branchLeafChangeFnRef = useRef<((leafId: string | null) => void) | null>(null);
  const sessionHasBranches = hasSessionBranches(branchTree);

  const handleBranchDataChange = useCallback((tree: SessionTreeNode[], activeLeafId: string | null, onLeafChange: (leafId: string | null) => void, locked: boolean) => {
    setBranchTree(tree);
    setBranchActiveLeafId(activeLeafId);
    setBranchSwitchLocked(locked);
    branchLeafChangeFnRef.current = onLeafChange;
  }, []);

  const handleBranchLeafChange = useCallback((leafId: string | null) => {
    branchLeafChangeFnRef.current?.(leafId);
  }, []);

  const [systemPrompt, setSystemPrompt] = useState<string | null>(null);
  const [systemTools, setSystemTools] = useState<ToolEntry[] | null>(null);
  const [systemInfoLoading, setSystemInfoLoading] = useState(false);
  const systemInfoLoaderRef = useRef<(() => Promise<void>) | null>(null);
  const systemInfoLoadIdRef = useRef(0);
  const systemBtnRef = useRef<HTMLButtonElement>(null);

  const handleSystemPromptChange = useCallback((prompt: string | null) => {
    setSystemPrompt(prompt);
    setSystemInfoLoading(false);
  }, []);

  const handleSystemToolsChange = useCallback((tools: ToolEntry[] | null) => {
    setSystemTools(tools);
  }, []);

  const handleSystemInfoLoaderChange = useCallback((loader: (() => Promise<void>) | null) => {
    systemInfoLoadIdRef.current += 1;
    systemInfoLoaderRef.current = loader;
    setSystemInfoLoading(false);
  }, []);

  // The debug panel's reader of the chat's own state: kept in a ref, so registering renders
  // nothing, and read only by the open panel (DebugPanel), never on AppShell's renders.
  const debugSnapshotReaderRef = useRef<(() => DebugClientSession) | null>(null);
  const handleDebugSnapshotReaderChange = useCallback((reader: (() => DebugClientSession) | null) => {
    debugSnapshotReaderRef.current = reader;
  }, []);
  // The panel reports the page's visibility history from the first mount on, opened or not.
  useEffect(() => {
    startPageVisibilityTracking();
  }, []);

  // Session stats (tokens + cost) — populated by ChatWindow, displayed in top bar
  const [sessionStats, setSessionStats] = useState<SessionStatsInfo | null>(null);
  const [autoNameStatus, setAutoNameStatus] = useState<AutoNameStatus>({ kind: "idle" });
  const autoNameTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const activeSessionIdRef = useRef<string | null>(selectedSession?.id ?? null);
  activeSessionIdRef.current = selectedSession?.id ?? null;
  const handleSessionStatsChange = useCallback((stats: SessionStatsInfo | null) => {
    setSessionStats(stats);
  }, []);
  const [copiedSessionField, setCopiedSessionField] = useState<SessionCopyField | null>(null);
  const sessionCopyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const handleCopySessionField = useCallback((field: SessionCopyField, value: string) => {
    void copyText(value).then(() => {
      if (sessionCopyTimerRef.current) clearTimeout(sessionCopyTimerRef.current);
      setCopiedSessionField(field);
      sessionCopyTimerRef.current = setTimeout(() => setCopiedSessionField(null), 1400);
    });
  }, []);

  useEffect(() => {
    return () => {
      if (sessionCopyTimerRef.current) clearTimeout(sessionCopyTimerRef.current);
      if (autoNameTimerRef.current) clearTimeout(autoNameTimerRef.current);
    };
  }, []);

  // Context usage — populated by ChatWindow, displayed in top bar
  const [contextUsage, setContextUsage] = useState<{ percent: number | null; contextWindow: number; tokens: number | null } | null>(null);
  const handleContextUsageChange = useCallback((usage: { percent: number | null; contextWindow: number; tokens: number | null } | null) => {
    setContextUsage(usage);
  }, []);

  // Single active panel — only one dropdown open at a time
  const [activeTopPanel, setActiveTopPanel] = useState<"agents" | "branches" | "system" | "tools" | "session" | "debug" | null>(null);
  const [topPanelPos, setTopPanelPos] = useState<{ top: number; left: number; width: number } | null>(null);

  useEffect(() => {
    if (!sessionHasBranches) {
      setActiveTopPanel((panel) => panel === "branches" ? null : panel);
    }
  }, [sessionHasBranches]);

  useEffect(() => {
    if (!activeSessionFamily) {
      setActiveTopPanel((panel) => panel === "agents" ? null : panel);
    }
  }, [activeSessionFamily]);

  useEffect(() => {
    if (rightPanelFullWidth) setActiveTopPanel(null);
  }, [rightPanelFullWidth]);

  const toggleTopPanel = useCallback((
    panel: "agents" | "branches" | "system" | "tools" | "session" | "debug",
    keepMobileToolbarOpen = false,
  ) => {
    if (isMobile) setSidebarOpen(false);
    setActiveTopPanel((cur) => cur === panel ? null : panel);
    if (isMobile && isNarrowMobile && keepMobileToolbarOpen) setMobileToolbarMoreOpen(true);
  }, [isMobile, isNarrowMobile]);

  // The Debug button on screen (one of its three places at a time); Escape gives focus back to it.
  const debugButtonRef = useRef<HTMLButtonElement | null>(null);

  // Escape inside the debug panel; it never closes another panel that took its place. With focus
  // in the panel, that focus leaves the page with it: once the panel is gone (the next frame), the
  // Debug button gets it back, unless the user moved it meanwhile (focusIfLost).
  const closeDebugPanel = useCallback((restoreFocus: boolean) => {
    setActiveTopPanel((panel) => panel === "debug" ? null : panel);
    if (restoreFocus) requestAnimationFrame(() => focusIfLost(document, debugButtonRef.current));
  }, []);

  const handleSystemInfoToggle = useCallback((
    panel: "system" | "tools",
    keepMobileToolbarOpen = false,
  ) => {
    const opening = activeTopPanel !== panel;
    toggleTopPanel(panel, keepMobileToolbarOpen);
    if (!opening || systemInfoLoading) return;

    const load = systemInfoLoaderRef.current;
    if (!load) return;
    const loadId = ++systemInfoLoadIdRef.current;
    setSystemInfoLoading(true);
    void load().catch((error) => {
      console.error("Failed to load system information:", error);
    }).finally(() => {
      if (systemInfoLoadIdRef.current === loadId) {
        setSystemInfoLoading(false);
      }
    });
  }, [activeTopPanel, systemInfoLoading, toggleTopPanel]);

  const openSessionStatsPanel = useCallback(() => {
    if (isMobile) setSidebarOpen(false);
    setMobileToolbarMoreOpen(false);
    setActiveTopPanel("session");
  }, [isMobile]);

  // The composer opens Settings too: a bare /mcp opens Settings › MCP (useAgentSession).
  const openSettingsSection = useCallback((section: SettingsSection) => {
    setSettingsOpenAtHome(false);
    setSettingsSection(section);
  }, []);

  const handleSidebarToggle = useCallback(() => {
    if (isMobile) {
      setActiveTopPanel(null);
      setMobileToolbarMoreOpen(false);
    }
    setSidebarOpen((open) => {
      const next = !open;
      if (!isMobile) desktopSidebarOpenRef.current = next;
      return next;
    });
  }, [isMobile]);

  const handleMobileToolbarMoreToggle = useCallback(() => {
    setSidebarOpen(false);
    setActiveTopPanel(null);
    setMobileToolbarMoreOpen((open) => !open);
  }, []);

  const handleRightPanelToggle = useCallback(() => {
    if (isMobile) {
      setSidebarOpen(false);
      setActiveTopPanel(null);
      setMobileToolbarMoreOpen(false);
    }
    setRightPanelOpen((open) => !open);
  }, [isMobile]);

  const handleRightPanelExpandToggle = useCallback(() => {
    setActiveTopPanel(null);
    setRightPanelExpanded((expanded) => !expanded);
  }, []);

  useEffect(() => {
    if (!mobileToolbarMoreOpen) return;

    const handlePointerDown = (event: PointerEvent) => {
      const toolbar = mobileToolbarRef.current;
      if (toolbar && event.composedPath().includes(toolbar)) return;
      setMobileToolbarMoreOpen(false);
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      setMobileToolbarMoreOpen(false);
    };

    document.addEventListener("pointerdown", handlePointerDown, true);
    document.addEventListener("keydown", handleKeyDown, true);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown, true);
      document.removeEventListener("keydown", handleKeyDown, true);
    };
  }, [mobileToolbarMoreOpen]);

  useEffect(() => {
    setMobileToolbarMoreOpen(false);
  }, [isMobile, isNarrowMobile, selectedSession?.id, newSessionDraftId]);

  useEffect(() => {
    if (!activeTopPanel || !topBarRef.current) return;
    const update = () => {
      const topBarRect = topBarRef.current!.getBoundingClientRect();
      if (activeTopPanel === "agents") {
        setTopPanelPos({
          top: topBarRect.bottom,
          left: topBarRect.left,
          width: Math.min(AGENT_PANEL_WIDTH, topBarRect.width),
        });
        return;
      }
      setTopPanelPos({ top: topBarRect.bottom, left: topBarRect.left, width: topBarRect.width });
    };
    update();
    const ro = new ResizeObserver(update);
    ro.observe(topBarRef.current);
    return () => ro.disconnect();
  }, [activeTopPanel, isMobile]);

  // Files unmount when inactive; workspace terminals stay mounted until closed.
  const [fileTabs, setFileTabs] = useState<Tab[]>([]);
  const [activeFileTabId, setActiveFileTabId] = useState<string | null>(null);
  // pi#8: a file tab switch takes the right panel back from the history export view.
  const prevActiveFileTabIdRef = useRef<string | null>(null);
  useEffect(() => {
    if (prevActiveFileTabIdRef.current !== activeFileTabId) {
      prevActiveFileTabIdRef.current = activeFileTabId;
      if (activeFileTabId !== null) setHistoryExportSessionId(null);
    }
  }, [activeFileTabId]);
  const fileWorkspaceStatesRef = useRef(new Map<string, ParkedFileWorkspace>());
  // Bumped on every workspace switch. The active viewer reports its state as
  // it unmounts, tagged with the generation it rendered in, so the report of a
  // tab just parked goes to its workspace, not to a same-path tab restored.
  const [fileWorkspaceGeneration, setFileWorkspaceGeneration] = useState(0);
  const fileWorkspaceGenerationRef = useRef(0);
  const [terminalTabs, setTerminalTabs] = useState<TerminalTab[]>([]);
  const [terminalsRestored, setTerminalsRestored] = useState(false);
  // Subagent work tabs are session-scoped and transient: no persistence, and
  // they are dropped when the project changes (they belong to its sessions).
  const [agentTabs, setAgentTabs] = useState<{ sessionId: string; label: string }[]>([]);
  const rawPanelTabs: Tab[] = [...fileTabs, ...terminalTabs.map((tab) => ({
    id: tab.id,
    label: getFileName(tab.cwd) || tab.cwd,
    filePath: tab.cwd,
    kind: "terminal" as const,
    closing: Boolean(tab.closing),
  })), ...agentTabs.map((tab) => ({
    id: `agent:${tab.sessionId}`,
    label: tab.label,
    filePath: tab.label,
    kind: "agent" as const,
  }))];
  // Right-panel tab order (pi#70): fileTabs/terminalTabs are separate state
  // arrays, concatenated file-first above, so neither array alone can
  // represent an interleaved drag order. panelTabOrder tracks the DISPLAYED
  // order only; reordering never touches fileTabs/terminalTabs themselves,
  // so each tab's bound session/pane mapping is untouched by construction.
  const [panelTabOrder, setPanelTabOrder] = useState<string[]>([]);
  const rawPanelTabIds = rawPanelTabs.map((tab) => tab.id).join("\u0000");
  useEffect(() => {
    setPanelTabOrder((prev) => syncTabOrder(prev, rawPanelTabIds ? rawPanelTabIds.split("\u0000") : []));
  }, [rawPanelTabIds]);
  const panelTabs: Tab[] = applyTabOrder(panelTabOrder, rawPanelTabs, (tab) => tab.id);
  const handleReorderPanelTab = useCallback((draggedId: string, targetId: string, after: boolean) => {
    setPanelTabOrder((prev) => reorderById(prev, (tabId) => tabId, draggedId, targetId, after));
  }, []);

  useEffect(() => {
    try {
      const saved = restoreTerminalTabs(window.sessionStorage.getItem(TERMINAL_TABS_KEY));
      setTerminalTabs(saved.tabs);
      if (saved.activeId) {
        setActiveFileTabId(saved.activeId);
        setRightPanelOpen(saved.open);
      }
    } catch { /* storage is optional */ }
    setTerminalsRestored(true);
  }, []);

  useEffect(() => {
    if (!terminalsRestored) return;
    try {
      window.sessionStorage.setItem(TERMINAL_TABS_KEY, JSON.stringify({
        tabs: terminalTabs.map(({ id, cwd }) => ({ id, cwd })),
        activeId: activeFileTabId,
        open: rightPanelOpen,
      }));
    } catch { /* storage is optional */ }
  }, [terminalTabs, activeFileTabId, rightPanelOpen, terminalsRestored]);

  const handleFileViewerStateChange = useCallback((
    generation: number,
    tabId: string,
    viewerRevision: number,
    viewerState: FileViewerState,
  ) => {
    if (generation !== fileWorkspaceGenerationRef.current) {
      saveParkedFileViewerState(fileWorkspaceStatesRef.current, generation, tabId, viewerRevision, viewerState);
      return;
    }
    setFileTabs((prev) => saveFileViewerState(prev, tabId, viewerRevision, viewerState));
  }, []);

  const changeFileWorkspace = useCallback((currentKey: string | null, nextKey: string) => {
    if (currentKey === nextKey) return;
    const activeFileId = activeFileTabId?.startsWith("file:") ? activeFileTabId : null;
    const next = switchFileWorkspace(fileWorkspaceStatesRef.current, currentKey, nextKey, {
      tabs: fileTabs,
      activeTabId: activeFileId,
      open: rightPanelOpen && Boolean(activeFileId),
    }, fileWorkspaceGenerationRef.current);
    fileWorkspaceGenerationRef.current += 1;
    setFileWorkspaceGeneration(fileWorkspaceGenerationRef.current);
    setFileTabs(next.tabs);
    // Subagent tabs belong to the previous project's sessions: dropped, not parked.
    setAgentTabs([]);
    // Terminal tabs span workspaces and keep control of the panel while active.
    if (!activeFileTabId || activeFileId || activeFileTabId.startsWith("agent:")) {
      setActiveFileTabId(next.activeTabId);
      setRightPanelOpen(next.open);
    }
  }, [activeFileTabId, fileTabs, rightPanelOpen]);

  // Same @mention format as the chat input's @ autocomplete, so the agent's
  // read tool resolves it the same way (it strips the @ prefix).
  const handleAtMention = useCallback((relativePath: string, isDir: boolean) => {
    resolveChatInputHandle()?.insertText(buildAtMentionText(relativePath, isDir));
    if (isMobile) { setRightPanelOpen(false); setSidebarOpen(false); }
  }, [isMobile, resolveChatInputHandle]);

  const handleAtMentions = useCallback((relativePaths: string[]) => {
    const mentions = buildFileAtMentionsText(relativePaths);
    if (mentions) resolveChatInputHandle()?.insertText(mentions);
    if (isMobile) { setRightPanelOpen(false); setSidebarOpen(false); }
  }, [isMobile, resolveChatInputHandle]);

  const handleFileLineMention = useCallback((relativePath: string, startLine: number, endLine: number) => {
    resolveChatInputHandle()?.insertText(buildFileLineMentionText(relativePath, startLine, endLine));
    if (isMobile) { setRightPanelOpen(false); setSidebarOpen(false); }
  }, [isMobile, resolveChatInputHandle]);

  const initialSessionId = initialNavigation.sessionId;
  const initialLinkedSessionId = linkedSessionId(initialNavigation);
  const [activeCwd, setActiveCwd] = useState<string | null>(null);
  const activeProjectKeyRef = useRef<string | null>(null);
  // True once the initial ?session= URL param has been resolved (or confirmed absent)
  const [initialSessionRestored, setInitialSessionRestored] = useState<boolean>(() => !initialSessionId);
  // sessionStorage is empty during SSR. Applying the tab's remembered session
  // in the useState initializer made the first client tree differ from the
  // server HTML (sidebar "select project" vs ""). Restore after mount instead.
  useLayoutEffect(() => {
    const next = withTabOpen(initialNavigation, getTabOpen());
    if (next === initialNavigation) return;
    setInitialNavigation(next);
    if (next.sessionId) setInitialSessionRestored(false);
  }, [initialNavigation]);
  // Suppresses sessionKey bump in handleCwdChange during the initial URL restore
  const suppressCwdBumpRef = useRef(false);
  // Set when the session the URL linked to is gone: the project the sidebar
  // adopts next keeps a fresh composer instead of restoring its last session.
  const skipWorkspaceRestoreRef = useRef(false);
  // Guards the async workspace restore so a slow response from an earlier
  // switch cannot resurrect a session into a project the user already left.
  const workspaceRestoreTokenRef = useRef(0);

  const invalidateWorkspaceRestore = useCallback(() => {
    workspaceRestoreTokenRef.current += 1;
  }, []);

  // Persist every active-session transition, including new and forked sessions
  // that bypass the sidebar selection handler. Transient sessions do not yet
  // carry projectKey, so use the active project identity until hydration.
  // The workspace memory is shared by every tab; the tab memory keeps this
  // tab's own session so a reload does not follow another tab's last pick.
  // New session is a selection too: remember the composer cwd so reload stays
  // on that UI instead of resurrecting the previous chat.
  useEffect(() => {
    if (selectedSession) {
      const projectKey = selectedSession.projectKey
        ?? activeProjectKeyRef.current
        ?? workspaceKeyOf(selectedSession);
      setLastOpenSession(projectKey, selectedSession.id);
      setTabOpenSession(selectedSession.id);
      return;
    }
    if (newSessionCwd) setTabOpenNewSession(newSessionCwd);
  }, [newSessionCwd, selectedSession]);

  useEffect(() => {
    const requestedCwd = initialNavigation.requestedCwd;
    if (!requestedCwd) return;

    const controller = new AbortController();
    setInitialCwdStatus("validating");
    setInitialCwdError(null);

    void fetch("/api/cwd/validate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ cwd: requestedCwd }),
      signal: controller.signal,
    })
      .then(async (response) => {
        const data = await response.json().catch(() => ({})) as { cwd?: string; error?: string };
        if (!response.ok || !data.cwd) {
          throw new Error(data.error ?? `HTTP ${response.status}`);
        }

        // The sidebar will notify us when it adopts this cwd. Avoid remounting
        // the just-created empty chat during that initial synchronization.
        suppressCwdBumpRef.current = true;
        const draftId = `initial:${requestedCwd}`;
        setNewSessionDraftId(draftId);
        activeNewSessionDraftKeyRef.current = `new:${draftId}:${data.cwd}`;
        setNewSessionCwd(data.cwd);
        setInitialCwdStatus("ready");
        if (!new URLSearchParams(window.location.search).get("cwd")) {
          router.replace(`?cwd=${encodeURIComponent(data.cwd)}`, { scroll: false });
        }
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setInitialCwdError(error instanceof Error ? error.message : String(error));
        setInitialCwdStatus("error");
      });

    return () => controller.abort();
  }, [initialNavigation, router]);

  // Restore the workspace's last open session after switching to it. Called
  // from handleCwdChange once the outgoing context has been reset. The session
  // is looked up against the live list so a deleted or drifted session falls
  // back to the default welcome page instead of erroring.
  const restoreWorkspaceContext = useCallback((projectKey: string, cwd: string) => {
    const token = ++workspaceRestoreTokenRef.current;
    const lastOpenSessionId = getLastOpenSession(projectKey);
    if (!lastOpenSessionId) return;
    const adopt = (d: { sessions: SessionInfo[] } | null) => {
      if (token !== workspaceRestoreTokenRef.current) return; // stale switch
      const s = d?.sessions.find((x) => x.id === lastOpenSessionId);
      if (!s) {
        // The list loaded but the remembered session is gone — forget it.
        // When the list itself failed (d === null) keep the memory so a
        // later switch retries the restore.
        if (d) clearLastOpen(projectKey);
        return;
      }
      if (workspaceKeyOf(s) !== projectKey) {
        // Defensive: the remembered session drifted out of this workspace.
        clearLastOpen(projectKey);
        return;
      }
      // Keep the temporary composer's draft in its cwd, even when the
      // remembered session belongs to another worktree of this project.
      const activeDraftKey = activeNewSessionDraftKeyRef.current;
      if (activeDraftKey) {
        rekeyDraft(activeDraftKey, parkedNewSessionDraftKey(cwd));
      }
      activeNewSessionDraftKeyRef.current = null;
      // Selecting the session must remount the chat with the session
      // present: useAgentSession loads content in a mount-only effect, so
      // the null-session welcome mount from the switch would never load
      // the restored session's messages.
      setSelectedSession(s);
      setSessionKey((k) => k + 1);
      if (new URLSearchParams(window.location.search).get("session") !== s.id) {
        router.replace(`?session=${encodeURIComponent(s.id)}`, { scroll: false });
      }
    };
    // Fast path: the sidebar already delivered the catalogue — restore
    // without waiting on a fresh /api/sessions round trip.
    if (sessionCatalog.length > 0) {
      adopt({ sessions: sessionCatalog });
      return;
    }
    void fetch("/api/sessions")
      .then((r) => (r.ok ? (r.json() as Promise<{ sessions: SessionInfo[] }>) : null))
      .then(adopt)
      .catch(() => {
        // Network hiccup: keep the remembered session for a later retry.
      });
  }, [router, sessionCatalog]);

  const handleCwdChange = useCallback((
    cwd: string | null,
    projectRoot?: string | null,
    projectKey?: string | null,
  ) => {
    invalidateWorkspaceRestore();
    const currentFreshCwd = newSessionCwd ?? activeCwd;
    setActiveCwd(cwd);
    // Skip if cwd is null (initial mount).
    if (!cwd) return;
    const skipWorkspaceRestore = skipWorkspaceRestoreRef.current;
    skipWorkspaceRestoreRef.current = false;
    const newProject = projectKey ?? projectRoot ?? cwd;
    const currentProject = activeProjectKeyRef.current
      ?? (selectedSession ? workspaceKeyOf(selectedSession) : null);
    activeProjectKeyRef.current = newProject;

    // Keep the project identity in sync during the initial URL restore without
    // remounting the just-created or restored chat.
    if (suppressCwdBumpRef.current) {
      suppressCwdBumpRef.current = false;
      return;
    }
    // The server may hydrate a normalized key after a custom cwd is already
    // active. Updating identity for the exact same cwd is not a user switch.
    if (currentFreshCwd === cwd && currentProject !== newProject) return;
    // Existing sessions stay open when the worktree selector moves within the
    // same project. A fresh composer must remount when its effective cwd moves,
    // otherwise its already-created runtime would keep sending to the old cwd.
    if (
      currentProject === newProject
      && (selectedSession !== null || currentFreshCwd === cwd)
    ) {
      return;
    }
    // Close any session that belongs to a different project — it no longer
    // matches the selected project directory.
    const previousDraftKey = activeNewSessionDraftKeyRef.current;
    if (previousDraftKey && currentFreshCwd) {
      rekeyDraft(previousDraftKey, parkedNewSessionDraftKey(currentFreshCwd));
    }
    const draftId = typeof crypto.randomUUID === "function"
      ? crypto.randomUUID()
      : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
    const draftKey = `new:${draftId}:${cwd}`;
    rekeyDraft(parkedNewSessionDraftKey(cwd), draftKey);
    setNewSessionDraftId(draftId);
    activeNewSessionDraftKeyRef.current = draftKey;
    setSelectedSession(null);
    setNewSessionCwd((prev) => {
      if (prev && prev !== cwd) return null;
      return prev;
    });
    setSessionKey((k) => k + 1);
    setBranchTree([]);
    setBranchActiveLeafId(null);
    setBranchSwitchLocked(false);
    setSystemPrompt(null);
    setSystemTools(null);
    setSystemInfoLoading(false);
    // The debug panel stays open across the switch: it follows whichever session is shown.
    setActiveTopPanel((panel) => panel === "debug" ? panel : null);
    if (currentProject !== newProject) {
      // Keep #281's project isolation, but park each workspace's file tabs
      // instead of discarding them so they return when the user switches back.
      changeFileWorkspace(currentProject, newProject);
      // Restore the workspace we switched to: its last open session, or keep
      // the default welcome page when none is remembered.
      if (!skipWorkspaceRestore) restoreWorkspaceContext(newProject, cwd);
    }
    router.replace(typeof window !== "undefined" ? window.location.pathname : "/", { scroll: false });
  }, [activeCwd, changeFileWorkspace, invalidateWorkspaceRestore, newSessionCwd, router, selectedSession, restoreWorkspaceContext]);

  const handleSelectSession = useCallback((session: SessionInfo, isRestore = false, entryId?: string, blockIndex?: number) => {
    setSearchTarget(entryId ? { sessionId: session.id, entryId, blockIndex } : null);
    invalidateWorkspaceRestore();
    const activeDraftKey = activeNewSessionDraftKeyRef.current;
    const activeDraftCwd = newSessionCwd ?? (selectedSession === null ? activeCwd : null);
    if (activeDraftKey && activeDraftCwd) {
      rekeyDraft(activeDraftKey, parkedNewSessionDraftKey(activeDraftCwd));
    }
    activeNewSessionDraftKeyRef.current = null;
    // Adopt an explicitly selected session before the sidebar reports its cwd.
    const projectKey = workspaceKeyOf(session);
    if (activeProjectKeyRef.current !== projectKey) {
      changeFileWorkspace(activeProjectKeyRef.current, projectKey);
      setActiveTopPanel((panel) => panel === "debug" ? panel : null);
    }
    activeProjectKeyRef.current = projectKey;
    // Re-clicking the already-open session must not remount the chat and
    // re-run the full load/positioning cycle. Only skip when the effective
    // cwd context already matches — otherwise a pending cwd move still needs
    // the full re-select flow.
    if (!isRestore && selectedSession) {
      const sameProject =
        workspaceKeyOf(selectedSession) === workspaceKeyOf(session);
      if (selectedSession.id === session.id && sameProject) {
        if (isMobile) setSidebarOpen(false);
        return;
      }
    }
    setNewSessionCwd(null);
    setSelectedSession(session);
    setSessionKey((k) => k + 1);
    setBranchTree([]);
    setBranchActiveLeafId(null);
    setBranchSwitchLocked(false);
    branchLeafChangeFnRef.current = null;
    setSystemPrompt(null);
    setSystemTools(null);
    setSystemInfoLoading(false);
    setInitialSessionRestored(true);
    // Split-pane routing (pi#4, opt-in): already-open session → scroll + focus;
    // else new tab. Disabled by default — the classic single-chat replace stays
    // the default layout until the toolbar toggle enables split view.
    if (splitPaneEnabled) {
      setPaneTabs((prev) => {
        // Selecting a session supersedes the new-session tab (pi#21): its
        // draft was parked above, so the next "+" restores it.
        const tabs = prev.some((t) => isNewSessionTab(t.sessionId))
          ? prev.filter((t) => !isNewSessionTab(t.sessionId))
          : prev;
        const alreadyOpen = tabs.some((t) => t.sessionId === session.id);
        if (alreadyOpen) {
          splitPaneLayoutRef.current?.scrollPaneIntoView(session.id);
          setFocusedPaneId(session.id);
          return clearBadgeOnFocus(tabs, session.id);
        }
        const label = session.name || session.firstMessage || session.id.slice(0, 12);
        return openPaneOp(tabs, session.id, label, projectDisplayNameForPath(session.projectRoot ?? session.cwd));
      });
      if (focusedPaneId !== session.id) setFocusedPaneId(session.id);
    }
    // On mobile, collapse the overlay drawer so the chat is revealed after pick.
    if (isMobile && !isRestore) setSidebarOpen(false);
    if (isRestore) {
      // Suppress the redundant sessionKey bump that would come from the
      // onCwdChange effect firing after setSelectedCwd in the sidebar
      suppressCwdBumpRef.current = true;
    }
    // Skip router.replace when the URL already has this session — calling
    // replace in production Next.js triggers a Suspense remount loop.
    // Tab-memory restore lands on `/` and must write `?session=` so reload
    // and copy-link keep this session.
    if (!isRestore || new URLSearchParams(window.location.search).get("session") !== session.id) {
      router.replace(`?session=${encodeURIComponent(session.id)}`, { scroll: false });
    }
  }, [activeCwd, changeFileWorkspace, invalidateWorkspaceRestore, router, isMobile, newSessionCwd, selectedSession, splitPaneEnabled, focusedPaneId]);

  const handleNewSession = useCallback((sessionId: string, cwd: string) => {
    invalidateWorkspaceRestore();
    // New-session page as a tab (pi#21): in split view an open new-session
    // tab is focused instead of rekeying drafts — the existing composer keeps
    // its draft untouched. Classic mode (split off) and mobile keep the exact
    // full-area composer behavior below.
    if (splitPaneEnabled && !isMobile && paneTabs.some((t) => isNewSessionTab(t.sessionId))) {
      splitPaneLayoutRef.current?.scrollPaneIntoView(NEW_SESSION_TAB_ID);
      setFocusedPaneId(NEW_SESSION_TAB_ID);
      if (isMobile) setSidebarOpen(false);
      return;
    }
    const draftKey = `new:${sessionId}:${cwd}`;
    rekeyDraft(parkedNewSessionDraftKey(cwd), draftKey);
    // Adopt the target project before the sidebar reports its cwd, as an
    // explicit session pick does. Without a key (Ctrl+Alt+N) the current cwd
    // keeps its project.
    const targetProject = (cwd === activeCwd ? activeProjectKeyRef.current : null) ?? cwd;
    if (activeProjectKeyRef.current !== targetProject) {
      changeFileWorkspace(activeProjectKeyRef.current, targetProject);
    }
    activeProjectKeyRef.current = targetProject;
    activeNewSessionDraftKeyRef.current = draftKey;
    setNewSessionDraftId(sessionId);
    setSelectedSession(null);
    setNewSessionCwd(cwd);
    setSessionKey((k) => k + 1);
    setBranchTree([]);
    setBranchActiveLeafId(null);
    setBranchSwitchLocked(false);
    setSystemPrompt(null);
    setSystemTools(null);
    setSystemInfoLoading(false);
    setActiveTopPanel((panel) => panel === "debug" ? panel : null);
    if (isMobile) setSidebarOpen(false);
    if (splitPaneEnabled && !isMobile) {
      // Open the new-session pane tab (at most one exists by construction)
      // and focus it instead of bypassing the pane layout. The sentinel's
      // label is the localized short "New" (tabs.new) — its embedded header
      // renders it as "New · <project>" via paneHeaderLabel (pi#25).
      const label = translate("tabs.new");
      const projectName = projectDisplayNameForPath(cwd);
      setPaneTabs((prev) => openNewSessionTab(prev, label, projectName).tabs);
      setFocusedPaneId(NEW_SESSION_TAB_ID);
    }
    router.replace(typeof window !== "undefined" ? window.location.pathname : "/", { scroll: false });
  }, [activeCwd, changeFileWorkspace, invalidateWorkspaceRestore, router, isMobile, splitPaneEnabled, paneTabs, translate]);

  // The new-session tab's default cwd (pi#21, user-confirmed): the FIRST
  // pinned project, else the default directory, and only then the current
  // workspace — NOT the focused session's cwd.
  const resolveNewSessionTabCwd = useCallback(async (): Promise<string | null> => {
    const customDirs = listCustomDirectories();
    if (customDirs.length > 0) return customDirs[0].path;
    try {
      // POST is the established "use default directory" semantic (pi#18):
      // it creates the directory; selection then goes through /api/cwd/validate
      // like any other directory, which allow-lists it so the composer's cwd
      // validation — and every cwd-scoped query the new tab fires (project
      // trust, models) — does not hit a 403 on a not-yet-registered dir. The
      // pi#27 entry flow used to skip that selection step, so a fresh server
      // 403'd both queries before any session existed in the directory.
      const response = await fetch("/api/default-cwd", { method: "POST" });
      const data = await response.json() as { cwd?: string };
      if (data.cwd) {
        try {
          await fetch("/api/cwd/validate", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ cwd: data.cwd }),
          });
        } catch {
          // Best-effort: an offline failure defers root registration to the
          // next validate/commit that touches this directory.
        }
        return data.cwd;
      }
    } catch {
      // fall through to the current workspace
    }
    return newSessionCwd ?? selectedSession?.cwd ?? activeCwd ?? null;
  }, [newSessionCwd, selectedSession, activeCwd]);

  // Global keyboard shortcuts (handles Esc, Ctrl+Alt+N etc.)
  useGlobalKeyboardShortcuts({
    onNewSession: (cwd: string) => handleNewSession(`kb-${Date.now()}`, cwd),
    activeCwd,
  });

  // Client-built transient SessionInfo (new session / fork) lacks the
  // server-computed projectKey, which the same-project check in
  // handleCwdChange relies on. Hydrate it from the session list so switching
  // worktrees right after creating a session doesn't close the chat.
  const hydrateSelectedSession = useCallback((sessionId: string) => {
    void fetch("/api/sessions", { cache: "no-store" })
      .then((r) => (r.ok ? (r.json() as Promise<{ sessions: SessionInfo[] }>) : null))
      .then((d) => {
        const full = d?.sessions.find((s) => s.id === sessionId);
        if (!full) return;
        setSelectedSession((prev) => (
          prev?.id === sessionId
            ? { ...prev, ...full, transient: full.transient ?? false }
            : prev
        ));
      })
      .catch(() => {});
  }, []);

  // Called by ChatWindow when a new session gets its real id from pi
  const handleSessionCreated = useCallback((session: SessionInfo, sourceDraftKey: string) => {
    setRefreshKey((k) => k + 1);
    if (activeNewSessionDraftKeyRef.current !== sourceDraftKey) return;
    invalidateWorkspaceRestore();
    activeNewSessionDraftKeyRef.current = null;
    setNewSessionCwd(null);
    setSelectedSession(session);
    hydrateSelectedSession(session.id);
    if (splitPaneEnabled && !isMobile) {
      // The created session adopts the new-session tab in place (pi#21): the
      // sentinel tab is replaced at the same index by the real session id, so
      // sibling panes keep their keys and never unmount or reorder.
      const label = session.name || session.firstMessage || session.id.slice(0, 12);
      // Project attribution (pi#25): the created session's pane header shows
      // "<project> · <session>". The transient SessionInfo may lack the
      // server-computed projectRoot (hydrateSelectedSession backfills it),
      // so fall back to the cwd basename.
      const projectName = projectDisplayNameForPath(session.projectRoot ?? session.cwd);
      setPaneTabs((prev) => {
        if (!prev.some((t) => isNewSessionTab(t.sessionId))) {
          return openPaneOp(prev, session.id, label, projectName);
        }
        return prev.map((t) =>
          isNewSessionTab(t.sessionId)
            ? { sessionId: session.id, label, projectName, hasBadge: false }
            : t,
        );
      });
      setFocusedPaneId(session.id);
    }
    router.replace(`?session=${encodeURIComponent(session.id)}`, { scroll: false });
  }, [invalidateWorkspaceRestore, router, hydrateSelectedSession, splitPaneEnabled, isMobile]);

  const deliverSessionNotification = useCallback(({
    targetSession,
    title,
    body,
    tag,
  }: {
    targetSession: SessionInfo | null;
    title: string;
    body: string;
    tag?: string;
  }) => {
    if (!("Notification" in window)) return;

    const fire = () => {
      const sessionUrl = targetSession ? `/?session=${encodeURIComponent(targetSession.id)}` : "/";
      void showBrowserNotification({
        title,
        body,
        sessionUrl,
        tag,
        onClick: () => {
          window.focus();
          if (targetSession) handleSelectSession(targetSession);
        },
      });
    };

    if (Notification.permission === "granted") {
      fire();
      void setupPushSubscription(locale);
    } else if (Notification.permission === "default") {
      void Notification.requestPermission().then((p) => {
        if (p === "granted") {
          fire();
          void setupPushSubscription(locale);
        }
      });
    }
  }, [handleSelectSession, locale]);

  const handleAgentEnd = useCallback((end: AgentEndInfo) => {
    // The sidebar's reload merges the selected session's new row into it
    // (handleSessionsChange), so no list fetch of its own here.
    setRefreshKey((k) => k + 1);
    setExplorerRefreshKey((k) => k + 1);

    if (end.aborted || end.subagentsActive || isHiddenRelation(selectedSession?.relation)) return;
    if (!shouldShowBrowserNotification()) return;
    const targetSession = selectedSession;
    if (targetSession) {
      setHiddenFinishedSessionIds((prev) => {
        if (prev.has(targetSession.id)) return prev;
        const next = new Set(prev);
        next.add(targetSession.id);
        return next;
      });
    }
    deliverSessionNotification({
      targetSession,
      title: targetSession?.name ?? translate("i18n.sessionComplete"),
      body: translate("i18n.taskFinished"),
      tag: targetSession ? `pi-session-complete:${targetSession.id}` : "pi-session-complete",
    });
  }, [deliverSessionNotification, selectedSession, translate]);

  // The badge is for an unseen tab: opening it puts the finished chat on screen.
  useEffect(() => {
    const clearIfSeen = () => {
      if (document.visibilityState !== "visible" || !document.hasFocus()) return;
      setHiddenFinishedSessionIds((prev) => (prev.size === 0 ? prev : new Set()));
    };
    clearIfSeen();
    document.addEventListener("visibilitychange", clearIfSeen);
    window.addEventListener("focus", clearIfSeen);
    return () => {
      document.removeEventListener("visibilitychange", clearIfSeen);
      window.removeEventListener("focus", clearIfSeen);
    };
  }, []);

  const handleAttentionNeeded = useCallback((request: BlockingExtensionUiRequest) => {
    if (isHiddenRelation(selectedSession?.relation)) return;
    if (!shouldShowBrowserNotification()) return;
    if (!claimExtensionAttentionNotification(request, notifiedAttentionRequestIdsRef.current)) return;

    deliverSessionNotification({
      targetSession: selectedSession,
      title: translate("i18n.attentionNeeded"),
      body: request.method === "custom"
        ? translate("i18n.extensionInputNeeded")
        : request.title,
      tag: `pi-extension-ui:${request.id}`,
    });
  }, [deliverSessionNotification, selectedSession, translate]);

  const handleAutoName = useCallback(async () => {
    const sessionId = selectedSession?.id;
    if (!sessionId || autoNameStatus.kind === "naming") return;
    if (autoNameTimerRef.current) clearTimeout(autoNameTimerRef.current);
    setActiveTopPanel(null);
    setAutoNameStatus({ kind: "naming" });

    try {
      const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/auto-name`, {
        method: "POST",
      });
      const body = (await response.json().catch(() => ({}))) as { title?: string; error?: string };
      if (!response.ok || !body.title) {
        throw new Error(body.error || `HTTP ${response.status}`);
      }

      const title = body.title.trim();
      setRefreshKey((key) => key + 1);
      if (activeSessionIdRef.current !== sessionId) return;
      setSelectedSession((current) => current?.id === sessionId ? { ...current, name: title } : current);
      setSessionStats((current) => current?.sessionId === sessionId ? { ...current, sessionName: title } : current);
      setAutoNameStatus({ kind: "success" });
      autoNameTimerRef.current = setTimeout(() => setAutoNameStatus({ kind: "idle" }), 1800);
    } catch (error) {
      if (activeSessionIdRef.current !== sessionId) return;
      const message = error instanceof Error ? error.message : String(error);
      setAutoNameStatus({ kind: "error", message });
      autoNameTimerRef.current = setTimeout(() => setAutoNameStatus({ kind: "idle" }), 5000);
    }
  }, [autoNameStatus.kind, selectedSession?.id]);

  useEffect(() => {
    if (autoNameTimerRef.current) clearTimeout(autoNameTimerRef.current);
    setAutoNameStatus({ kind: "idle" });
  }, [selectedSession?.id]);

  const handleExplorerRefresh = useCallback(() => {
    setExplorerRefreshKey((k) => k + 1);
  }, []);

  // A run's writes show in the file tree as they happen, not only when it ends
  // (#1144): at most one refresh a second, after any tool but the read-only ones.
  const toolEndRefreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const handleToolEnd = useCallback((toolName: string) => {
    if (READ_ONLY_TOOL_NAMES.has(toolName) || toolEndRefreshTimerRef.current) return;
    toolEndRefreshTimerRef.current = setTimeout(() => {
      toolEndRefreshTimerRef.current = null;
      setExplorerRefreshKey((k) => k + 1);
    }, TOOL_END_REFRESH_MS);
  }, []);
  useEffect(() => () => {
    if (toolEndRefreshTimerRef.current) clearTimeout(toolEndRefreshTimerRef.current);
  }, []);

  const handleSessionForked = useCallback((newSessionId: string) => {
    invalidateWorkspaceRestore();
    activeNewSessionDraftKeyRef.current = null;
    setRefreshKey((k) => k + 1);
    setSessionKey((k) => k + 1);
    setNewSessionCwd(null);
    setSelectedSession((prev) => ({
      ...(prev ?? { path: "", cwd: "", created: "", modified: "", messageCount: 0, firstMessage: "" }),
      id: newSessionId,
      transient: false,
    }));
    hydrateSelectedSession(newSessionId);
    router.replace(`?session=${encodeURIComponent(newSessionId)}`, { scroll: false });
  }, [invalidateWorkspaceRestore, router, hydrateSelectedSession]);

  const handleAskInNewChat = useCallback(async (
    prompt: string,
    sourceSessionId: string,
    sourceEntryId: string,
  ) => {
    const result = await sendAgentCommand<{ newSessionId?: string }>(sourceSessionId, {
      type: "fork_branch",
      entryId: sourceEntryId,
    });
    if (!result?.newSessionId) throw new Error(translate("chat.quoteForkFailed"));
    setPendingQuotePrompt({ sessionId: result.newSessionId, text: prompt });
    handleSessionForked(result.newSessionId);
  }, [handleSessionForked, translate]);

  // The sidebar calls this when the initial session is not in its list.
  const handleInitialRestoreDone = useCallback(() => {
    setInitialSessionRestored(true);
    if (initialLinkedSessionId) skipWorkspaceRestoreRef.current = true;
  }, [initialLinkedSessionId]);

  const handleSessionDeleted = useCallback((sessionId: string) => {
    invalidateWorkspaceRestore();
    setRefreshKey((k) => k + 1);
    // The DELETE can outlive a session switch: this callback's captured
    // selectedSession is from the delete click. Read the latest selection
    // and only fall back to the empty composer when the user is still on
    // the deleted session at the moment removal completes.
    const active = selectedSessionRef.current;
    if (active?.id === sessionId) {
      clearTabOpenSession(sessionId);
      const cwd = active.cwd;
      const draftId = typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
      setNewSessionDraftId(draftId);
      activeNewSessionDraftKeyRef.current = cwd ? `new:${draftId}:${cwd}` : null;
      setSelectedSession(null);
      setNewSessionCwd(cwd ?? null);
      setSessionKey((k) => k + 1);
      setBranchTree([]);
      setBranchActiveLeafId(null);
      setBranchSwitchLocked(false);
      setSystemPrompt(null);
      setSystemTools(null);
      setSystemInfoLoading(false);
      setActiveTopPanel((panel) => panel === "debug" ? panel : null);
      router.replace(cwd ? `?cwd=${encodeURIComponent(cwd)}` : (typeof window !== "undefined" ? window.location.pathname : "/"), { scroll: false });
    }
  }, [invalidateWorkspaceRestore, router]);

  const handleOpenFile = useCallback((
    filePath: string,
    fileName: string,
    options?: { sourceSessionId?: string | null; modeHint?: "diff"; page?: number },
  ) => {
    const sourceSessionId = options?.sourceSessionId;
    const modeHint = options?.modeHint;
    const page = options?.page;
    const tabId = `file:${filePath}`;
    setFileTabs((prev) => openFileTab(prev, {
      fileName,
      filePath,
      modeHint,
      page,
      sourceSessionId,
      tabId,
    }));
    setActiveFileTabId(tabId);
    setRightPanelOpen(true);
    // On mobile the file panel is full-screen; close the drawer so it shows.
    if (isMobile) setSidebarOpen(false);
  }, [isMobile]);

  const handleOpenLinkedFile = useCallback((filePath: string, page?: number) => {
    handleOpenFile(filePath, getFileName(filePath), { sourceSessionId: selectedSession?.id ?? null, page });
  }, [handleOpenFile, selectedSession?.id]);

  // Split panes open a link against their OWN session (pane focus never
  // updates selectedSession), but the callback identity must stay stable:
  // MarkdownBody rebuilds its component map whenever onOpenFile changes, and
  // a fresh inline arrow per AppShell render let a trusted click land on a
  // replaced <a> (code review). Cache one handler per pane id and
  // read the latest handleOpenFile through a ref.
  const handleOpenFileRef = useRef(handleOpenFile);
  useEffect(() => { handleOpenFileRef.current = handleOpenFile; }, [handleOpenFile]);
  const paneOpenFileHandlersRef = useRef(new Map<string, (filePath: string, page?: number) => void>());
  const getPaneOpenFileHandler = useCallback((sid: string) => {
    const cached = paneOpenFileHandlersRef.current.get(sid);
    if (cached) return cached;
    const handler = (filePath: string, page?: number) => {
      handleOpenFileRef.current(filePath, getFileName(filePath), { sourceSessionId: sid, page });
    };
    paneOpenFileHandlersRef.current.set(sid, handler);
    return handler;
  }, []);

  const handleOpenTerminal = useCallback((cwd: string) => {
    const existing = terminalTabs.find((tab) => tab.cwd === cwd);
    const tab = existing ?? newTerminalTab(cwd);
    if (!existing) setTerminalTabs((tabs) => [...tabs, tab]);
    setActiveFileTabId(tab.id);
    setRightPanelOpen(true);
    if (isMobile) setSidebarOpen(false);
  }, [terminalTabs, isMobile]);

  // Subagent work opens in a right-panel tab instead of hijacking the main chat.
  const handleOpenSubagentTab = useCallback((sessionId: string, label: string) => {
    setAgentTabs((prev) => {
      if (prev.some((tab) => tab.sessionId === sessionId)) return prev;
      return [...prev, { sessionId, label }];
    });
    setActiveFileTabId(`agent:${sessionId}`);
    setRightPanelOpen(true);
    if (isMobile) setSidebarOpen(false);
  }, [isMobile]);

  // The agent switcher's side-panel button: opens a subagent's tab, or closes
  // it when the open panel already has it (the panel closes with its last tab).
  // The switcher stays open beside the panel, except on phones, where the
  // panel covers the chat.
  const openSubagentTabIds = useMemo(
    () => new Set(rightPanelOpen ? agentTabs.map((tab) => tab.sessionId) : []),
    [agentTabs, rightPanelOpen],
  );
  const handleToggleSubagentTab = (sessionId: string, label: string) => {
    if (openSubagentTabIds.has(sessionId)) {
      handleCloseFileTab(`agent:${sessionId}`);
      return;
    }
    if (isMobile) setActiveTopPanel(null);
    handleOpenSubagentTab(sessionId, label);
  };

  const handleTerminalClosed = (tab: TerminalTab) => {
    const replacement = tab.closing === "restart" ? newTerminalTab(tab.cwd) : null;
    const remaining = terminalTabs.filter((item) => item.id !== tab.id);
    setTerminalTabs((tabs) => tabs.flatMap((item) => item.id !== tab.id ? [item] : replacement ? [replacement] : []));
    setActiveFileTabId((current) => current !== tab.id ? current : replacement?.id ?? remaining.at(-1)?.id ?? fileTabs.at(-1)?.id ?? lastAgentTabId(agentTabs));
    if (!replacement && !remaining.length && !fileTabs.length && !agentTabs.length) setRightPanelOpen(false);
  };

  const handleCloseFileTab = useCallback((tabId: string) => {
    if (terminalTabs.some((tab) => tab.id === tabId)) {
      setTerminalTabs((tabs) => tabs.map((tab) => tab.id === tabId && !tab.closing ? { ...tab, closing: "close" } : tab));
      return;
    }
    if (tabId.startsWith("agent:")) {
      const remainingAgents = agentTabs.filter((tab) => `agent:${tab.sessionId}` !== tabId);
      setAgentTabs(remainingAgents);
      setActiveFileTabId((cur) => {
        if (cur !== tabId) return cur;
        return fileTabs.at(-1)?.id ?? terminalTabs.at(-1)?.id ?? lastAgentTabId(remainingAgents);
      });
      if (fileTabs.length === 0 && terminalTabs.length === 0 && remainingAgents.length === 0) setRightPanelOpen(false);
      return;
    }
    setFileTabs((prev) => {
      const next = prev.filter((t) => t.id !== tabId);
      if (next.length === 0 && terminalTabs.length === 0 && agentTabs.length === 0) setRightPanelOpen(false);
      return next;
    });
    setActiveFileTabId((cur) => {
      if (cur !== tabId) return cur;
      const remaining = fileTabs.filter((t) => t.id !== tabId);
      return remaining.at(-1)?.id ?? terminalTabs.at(-1)?.id ?? lastAgentTabId(agentTabs);
    });
  }, [fileTabs, terminalTabs, agentTabs]);

  // a claude session has no HTML export, background tasks or subagents.
  const selectedIsClaude = selectedSession?.harness === "claude";

  // One session, one chat: opening a subagent in the main chat closes its tab.
  const selectedSessionId = selectedSession?.id;
  useEffect(() => {
    if (agentTabs.some((tab) => tab.sessionId === selectedSessionId)) handleCloseFileTab(`agent:${selectedSessionId}`);
  }, [agentTabs, handleCloseFileTab, selectedSessionId]);

  const handleViewFullHistory = useCallback(() => {
    if (!selectedSession) return;
    // Render the export snapshot inside the right panel instead of a contextless
    // new tab (pi#8): the panel chrome owns focus and closing, so the user can
    // always get back. Toggling: showing it again while open dismisses it.
    setHistoryExportSessionId((current) =>
      current === selectedSession.id && rightPanelOpen ? null : selectedSession.id);
    setRightPanelOpen(true);
  }, [selectedSession, rightPanelOpen]);

  // Show chat area if a session is selected, or if we have a cwd to start a new session in
  const effectiveNewSessionCwd = newSessionCwd ?? (selectedSession === null && activeCwd ? activeCwd : null);
  const newSessionDraftKey = selectedSession === null && effectiveNewSessionCwd
    ? `new:${newSessionDraftId}:${effectiveNewSessionCwd}`
    : null;
  useLayoutEffect(() => {
    activeNewSessionDraftKeyRef.current = newSessionDraftKey;
  }, [newSessionDraftKey]);
  const showChat = selectedSession !== null || effectiveNewSessionCwd !== null;
  const projectTrustCwd = selectedSession?.cwd ?? effectiveNewSessionCwd;
  // While restoring initial session from URL, don't show the placeholder
  const showPlaceholder = initialSessionRestored && !showChat;

  // Pane-tab restore (this wi): a browser reload re-opens every previously
  // open split-view pane, not just the single ?session= deep-link target.
  // One-shot: the attempt is consumed as soon as the sidebar's session list
  // settles, in every layout mode — classic and mobile never restore, but
  // consuming the attempt there keeps a later split toggle from replaying a
  // stale record over the chat the user is already looking at.
  useEffect(() => {
    if (!sessionCatalogReported) return;
    if (paneRestoreAttemptedRef.current) return;
    paneRestoreAttemptedRef.current = true;
    setPaneRestoreAttempted(true);
    if (!splitPaneEnabled || isMobile) return;
    const record = readOpenPaneTabs();
    const byId = new Map(sessionCatalog.map((session) => [session.id, session]));
    // Resolve persisted tabs against the live catalog — deleted sessions
    // drop out — and rebuild the strip in persisted order with live labels.
    const restored: PaneTab[] = [];
    for (const tab of record?.tabs ?? []) {
      const session = byId.get(tab.sessionId);
      if (!session) continue;
      restored.push({
        sessionId: session.id,
        // Live name first; the persisted label is the fallback (spec R3),
        // then firstMessage, then the id prefix.
        label: session.name || tab.label || session.firstMessage || session.id.slice(0, 12),
        projectName: projectDisplayNameForPath(session.projectRoot ?? session.cwd),
        hasBadge: false,
      });
    }
    // Nothing restorable keeps the strip and the pi#27 entry fallback
    // (new-session tab) exactly as they were.
    if (restored.length === 0) return;
    // A valid ?session= deep-link target the persisted strip did not contain
    // is appended as an extra pane.
    const deepLink = initialSessionId ? byId.get(initialSessionId) ?? null : null;
    if (deepLink && !restored.some((t) => t.sessionId === deepLink.id)) {
      restored.push({
        sessionId: deepLink.id,
        label: deepLink.name || deepLink.firstMessage || deepLink.id.slice(0, 12),
        projectName: projectDisplayNameForPath(deepLink.projectRoot ?? deepLink.cwd),
        hasBadge: false,
      });
    }
    // Focus resolution, fixed order: persisted focused pane → valid
    // deep-link target → first restored pane.
    const persistedFocus = record?.focusedPaneId ?? null;
    const focus = persistedFocus && restored.some((t) => t.sessionId === persistedFocus)
      ? persistedFocus
      : deepLink?.id ?? restored[0].sessionId;
    const focusSession = byId.get(focus) ?? null;
    if (!focusSession) return;
    setPaneTabs(restored);
    // Select the focused pane's session through the ordinary selection path:
    // its split-pane already-open branch (isRestore) scrolls and focuses it
    // without duplicating or remounting the pane.
    handleSelectSession(focusSession, true);
    // The pi#26 workspace last-open restore must not resurrect a different
    // single session over the rebuilt strip, and the URL must reflect the
    // focused pane.
    invalidateWorkspaceRestore();
    if (new URLSearchParams(window.location.search).get("session") !== focus) {
      router.replace(`?session=${encodeURIComponent(focus)}`, { scroll: false });
    }
  }, [sessionCatalogReported, sessionCatalog, splitPaneEnabled, isMobile, initialSessionId, handleSelectSession, invalidateWorkspaceRestore, router]);

  // Pane-tab persistence writer (this wi): every strip/focus change after the
  // one-shot restore writes the record, so the next reload re-opens exactly
  // these panes. The restore-attempt gate keeps the mount-time empty strip
  // from clobbering the record, and classic/mobile modes persist nothing.
  useEffect(() => {
    if (!splitPaneEnabled || isMobile) return;
    if (!paneRestoreAttempted) return;
    writeOpenPaneTabs(paneTabs, focusedPaneId);
  }, [splitPaneEnabled, isMobile, paneRestoreAttempted, paneTabs, focusedPaneId]);

  // pi#27: entry lands on the new-session tab. When the initial restore
  // completes with nothing restorable (no ?session=, no last-open session)
  // and tab mode is on with an empty strip, resolve the default cwd (first
  // pinned project → default directory) and open the focused sentinel pane.
  // The placeholder page is thereby retired for the default experience; it
  // still renders for users who persisted a split-view disable.
  const entryNewSessionFiredRef = useRef(false);
  useEffect(() => {
    if (entryNewSessionFiredRef.current) return;
    if (!initialSessionRestored) return;
    // Pane-tab restore (this wi): while a persisted strip may still be
    // restored, hold the entry fallback back — otherwise the sentinel
    // new-session tab fires first and the restore would replace it.
    if (splitPaneEnabled && !isMobile && !paneRestoreAttempted) return;
    // In tab mode an EMPTY STRIP is not "superseded" by a cwd alone: the
    // sidebar's auto-select (most-recent project) sets activeCwd during the
    // same load window, and without the sentinel new-session tab the split
    // layout renders nothing (the classic composer never mounts in tab
    // mode). The entry fallback therefore fires for an empty strip even
    // when a cwd was auto-selected — exactly the race the pre-restore entry
    // used to win at mount time. Superseded in tab mode = a selected
    // session or a non-empty strip (incl. a restored one, set by the
    // restore effect earlier in this same commit).
    const entrySuperseded = splitPaneEnabled && !isMobile
      ? Boolean(selectedSession) || paneTabs.length > 0
      : Boolean(selectedSession) || Boolean(effectiveNewSessionCwd) || paneTabs.length > 0;
    if (entrySuperseded) {
      entryNewSessionFiredRef.current = true;
      return;
    }
    if (!splitPaneEnabled || isMobile) {
      entryNewSessionFiredRef.current = true;
      return;
    }
    entryNewSessionFiredRef.current = true;
    void resolveNewSessionTabCwd().then((cwd) => {
      if (cwd) handleNewSession(`entry-${Date.now()}`, cwd);
    });
  }, [initialSessionRestored, selectedSession, effectiveNewSessionCwd, paneTabs.length, splitPaneEnabled, isMobile, paneRestoreAttempted, resolveNewSessionTabCwd, handleNewSession]);
  // The Debug button shows with a chat only: without one, its panel would have nothing to close it.
  useEffect(() => {
    if (!showChat) setActiveTopPanel((panel) => panel === "debug" ? null : panel);
  }, [showChat]);

  useEffect(() => {
    setProjectTrust(null);
    setProjectTrustDialogOpen(false);
    setProjectTrustError(null);
    if (!projectTrustCwd) return;

    const controller = new AbortController();
    // The answer also lists the project's MCP servers (`mcpFile`, `mcpServers`), unused here: the
    // trust dialog fetches the listing again when it opens, since the file can change in between.
    fetch(`/api/project-trust?cwd=${encodeURIComponent(projectTrustCwd)}`, {
      signal: controller.signal,
    })
      .then(async (response) => {
        const data = await response.json() as ProjectTrustStatus & { error?: string };
        if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
        setProjectTrust(data);
      })
      .catch((error) => {
        if (error instanceof DOMException && error.name === "AbortError") return;
        console.error("Failed to load project trust:", error);
      });
    return () => controller.abort();
  }, [projectTrustCwd]);

  const handleTrustProject = useCallback(async () => {
    if (!projectTrustCwd || projectTrustBusy) return;
    setProjectTrustBusy(true);
    setProjectTrustError(null);
    try {
      const response = await fetch("/api/project-trust", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ cwd: projectTrustCwd }),
      });
      const data = await response.json() as ProjectTrustStatus & Partial<McpErrorResponse>;
      if (!response.ok || data.error) {
        // The dialog translates the reason; the English error is only its diagnostic.
        setProjectTrustError({ error: data.error ?? `HTTP ${response.status}`, ...(data.reason ? { reason: data.reason } : {}) });
        return;
      }
      setProjectTrust(data);
      setProjectTrustDialogOpen(false);
      setModelsRefreshKey((key) => key + 1);
      setSessionKey((key) => key + 1);
    } catch (error) {
      setProjectTrustError({ error: error instanceof Error ? error.message : String(error) });
    } finally {
      setProjectTrustBusy(false);
    }
  }, [projectTrustBusy, projectTrustCwd]);

  // The restricted-mode banner and Settings › MCP's trust notice open the same dialog.
  const openProjectTrustDialog = useCallback(() => {
    setProjectTrustError(null);
    setProjectTrustDialogOpen(true);
  }, []);

  // Settings › MCP added a project server: `.pi/mcp.json` alone makes a folder require trust, and a
  // fresh folder was trusted in the same step. Every mounted section reloads in place on the new
  // status (projectTrustReloadKey); nothing was rebuilt, so the chat needs no new session key.
  const handleProjectTrustChanged = useCallback((cwd: string, status: ProjectTrustStatus) => {
    if (cwd === projectTrustCwd) setProjectTrust(status);
  }, [projectTrustCwd]);

  const activeFileTab = fileTabs.find((tab) => tab.id === activeFileTabId) ?? null;
  // Subagent work tabs: like files, only the active one is mounted.
  const activeAgentTab = agentTabs.find((tab) => `agent:${tab.sessionId}` === activeFileTabId) ?? null;
  const activeCwdName = activeCwd ? getFileName(activeCwd) || activeCwd : null;
  const windowTitle = activeCwdName ? `${activeCwdName} - Pi Web` : "Pi Web";

  useEffect(() => {
    const syncWindowTitle = () => {
      if (document.title !== windowTitle) document.title = windowTitle;
    };

    syncWindowTitle();
    const observer = new MutationObserver(syncWindowTitle);
    observer.observe(document.head, { childList: true, subtree: true, characterData: true });
    return () => observer.disconnect();
  }, [windowTitle]);

  const sidebarContent = (
    <>
      <SessionSidebar
        selectedSessionId={selectedSession?.id ?? null}
        highlightSessionId={sidebarSessionId}
        followHighlightIntoView={splitPaneEnabled && !isMobile}
        onSelectSession={handleSelectSession}
        onNewSession={handleNewSession}
        initialSessionId={initialSessionId}
        skipInitialProjectSelection={initialNavigation.requestedCwd !== null}
        onInitialRestoreDone={handleInitialRestoreDone}
        refreshKey={refreshKey}
        onSessionDeleted={handleSessionDeleted}
        selectedCwd={selectedSession?.cwd ?? newSessionCwd ?? null}
        onCwdChange={handleCwdChange}
        onOpenFile={handleOpenFile}
        onOpenTerminal={handleOpenTerminal}
        activeFilePath={activeFileTab?.filePath ?? null}
        explorerRefreshKey={explorerRefreshKey}
        onExplorerRefresh={handleExplorerRefresh}
        onAtMention={handleAtMention}
        onAtMentions={handleAtMentions}
        onBackgroundTaskDone={handleBackgroundTaskDone}
        hiddenFinishedSessionIds={hiddenFinishedSessionIds}
        onRunningSessionIdsChange={handleRunningSessionIdsChange}
        onSessionsChange={handleSessionsChange}
        onExternalSessionChange={handleExternalSessionChange}
      />
      <div style={{ padding: "8px", flexShrink: 0, display: "flex", justifyContent: "space-between", gap: 4 }}>
        {([
          ["models", translate("common.models")],
          ["skills", translate("common.skills")],
        ] as const).map(([section, label]) => {
          const disabled = settingsSectionRequiresProject(section) && !projectTrustCwd;
          return (
            <button
              key={section}
              type="button"
              onClick={() => openSettingsSection(section)}
              disabled={disabled}
              title={disabled ? translate("settings.projectRequired") : label}
              aria-label={label}
              style={{
                flex: 1, display: "flex", alignItems: "center", justifyContent: "center", gap: 6,
                height: 32, padding: 0, background: "none", border: "none",
                borderRadius: 9, color: "var(--text-muted)", cursor: disabled ? "default" : "pointer",
                fontSize: 12, opacity: disabled ? 0.35 : 1,
                transition: "background 0.12s, color 0.12s",
              }}
              onMouseEnter={(event) => { if (!disabled) { event.currentTarget.style.background = "var(--bg-hover)"; event.currentTarget.style.color = "var(--text)"; } }}
              onMouseLeave={(event) => { event.currentTarget.style.background = "none"; event.currentTarget.style.color = "var(--text-muted)"; }}
            >
              <SettingsSectionIcon section={section} size={14} strokeWidth={2} />
              <span>{label}</span>
            </button>
          );
        })}
        <button
          type="button"
          onClick={() => {
            setSettingsOpenAtHome(true);
            setSettingsSection(getLastSettingsSection(projectTrustCwd));
          }}
          title={translate("common.settings")}
          aria-label={translate("common.settings")}
          style={{
            flex: 1, display: "flex", alignItems: "center", justifyContent: "center", gap: 6,
            height: 32, padding: 0, background: "none", border: "none",
            borderRadius: 9, color: "var(--text-muted)", cursor: "pointer",
            fontSize: 12, transition: "background 0.12s, color 0.12s",
          }}
          onMouseEnter={(event) => { event.currentTarget.style.background = "var(--bg-hover)"; event.currentTarget.style.color = "var(--text)"; }}
          onMouseLeave={(event) => { event.currentTarget.style.background = "none"; event.currentTarget.style.color = "var(--text-muted)"; }}
        >
          <SettingsSectionIcon section="general" size={14} strokeWidth={2} />
          <span>{translate("common.settings")}</span>
        </button>
      </div>
    </>
  );

  const renderProjectTrustWarning = (mobileBanner: boolean) => {
    if (!showChat || !projectTrust?.requiresTrust || projectTrust.trusted) return null;
    return (
      <button
        type="button"
        onClick={openProjectTrustDialog}
        title={translate("trust.resourcesNotLoaded")}
        aria-label={translate("trust.resourcesNotLoaded")}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: mobileBanner ? "flex-start" : "center",
          gap: 6,
          width: mobileBanner ? "100%" : undefined,
          minHeight: mobileBanner ? 32 : undefined,
          height: mobileBanner ? undefined : "100%",
          padding: mobileBanner ? "6px 12px" : "0 12px",
          background: mobileBanner ? "color-mix(in srgb, #d97706 8%, var(--bg-panel))" : "none",
          border: "none",
          borderRight: mobileBanner ? "none" : "1px solid var(--border)",
          borderBottom: mobileBanner ? "1px solid var(--border)" : "none",
          color: "#d97706",
          cursor: "pointer",
          flexShrink: 0,
          fontSize: 11,
          lineHeight: 1.35,
          textAlign: "left",
        }}
        data-mobile-trust-banner={mobileBanner ? "true" : undefined}
      >
        <svg
          width="13"
          height="13"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
          style={{ flexShrink: 0 }}
        >
          <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z" />
          <path d="M12 8v4" />
          <path d="M12 16h.01" />
        </svg>
        <span>{translate("trust.resourcesNotLoaded")}</span>
      </button>
    );
  };

  // While subagents run: their count, ringed by a spinner; otherwise all of them.
  // The agents switcher button, shared by the wide topbar and the phone's narrow toolbar.
  // A claude harness session keeps the pi top bar plain: no agents switcher there.
  const renderAgentsButton = (mobile: boolean) => (
    !selectedIsClaude && hasSubagentSessions && agentsBadge ? (
      <button
        type="button"
        onClick={() => toggleTopPanel("agents", mobile)}
        title={agentsButtonLabel}
        aria-label={agentsButtonLabel}
        aria-pressed={activeTopPanel === "agents"}
        style={{
          position: "relative",
          display: "flex", alignItems: "center", justifyContent: "center", gap: 6,
          width: mobile ? TOP_BAR_ICON_BUTTON_SIZE : undefined,
          height: "100%", padding: mobile ? 0 : "0 12px",
          background: activeTopPanel === "agents" ? "var(--bg-selected)" : "none",
          border: "none",
          borderTop: activeTopPanel === "agents" ? "2px solid var(--accent)" : "2px solid transparent",
          borderRight: "1px solid var(--border)",
          color: activeTopPanel === "agents" ? "var(--text)" : "var(--text-muted)",
          cursor: "pointer", flexShrink: 0, fontSize: 11, whiteSpace: "nowrap",
          transition: "color 0.1s, background 0.1s",
        }}
        data-mobile-toolbar-action={mobile ? "agents" : undefined}
      >
        <svg width={mobile ? 16 : 13} height={mobile ? 16 : 13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <rect x="5" y="7" width="14" height="11" rx="2" /><path d="M9 11h.01M15 11h.01M9 15h6M12 7V4M10 4h4" />
        </svg>
        {!mobile && <span>{translate("agentSwitcher.title")}</span>}
        {renderAgentsBadge(agentsBadge, mobile)}
      </button>
    ) : null
  );
  const renderAgentsBadge = (badge: { count: number; running: boolean }, mobile: boolean) => (
    <span
      aria-hidden="true"
      style={{
        position: "relative",
        minWidth: 15, height: 15, padding: "0 4px", display: "grid", placeItems: "center",
        borderRadius: 7,
        background: badge.running ? "color-mix(in srgb, var(--accent) 22%, var(--bg-panel))" : "var(--bg-selected)",
        color: "var(--accent)",
        fontSize: 10, lineHeight: 1, fontVariantNumeric: "tabular-nums",
        fontWeight: badge.running ? 600 : undefined,
        ...(mobile ? { position: "absolute", top: 3, right: 3, minWidth: 13, height: 13, padding: "0 3px", fontSize: 9 } : {}),
      }}
    >
      {badge.running && (
        <svg
          className="animate-spin motion-reduce:animate-none"
          viewBox="0 0 24 24" fill="none" aria-hidden="true"
          style={{ position: "absolute", top: -3, left: -3, width: "calc(100% + 6px)", height: "calc(100% + 6px)", pointerEvents: "none" }}
        >
          <circle cx="12" cy="12" r="11" stroke="currentColor" strokeWidth="2" opacity="0.2" />
          <path d="M23 12a11 11 0 0 0-11-11" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        </svg>
      )}
      {badge.count}
    </span>
  );

  // one stable handler for the topbar toggle, used by both
  // the resident phone slot and the wide/desktop inline slot.
  const togglePolyforgePanel = useCallback(() => {
    setPfPanelOpen((open) => {
      const next = !open;
      if (next) setBgPanelOpen(false);
      return next;
    });
  }, []);

  //, widened by, carried by the button since
  // the attention walk. A click on the Polyforge button while
  // the dock is closed and something needs a human (the jobs waiting for an
  // answer plus the decision feed's other open entries) opens the dock
  // (bg-tasks closes, the shared slot) at the next one: a work item's job
  // detail in Work › Jobs (selected), or a task in Work › Tasks.
  const openPfNeedsInput = useCallback(() => {
    openPfNextAttention();
    setBgPanelOpen(false);
    setPfPanelOpen(true);
  }, [openPfNextAttention]);

  // while anything needs a human the button itself is amber
  // (open or closed; aria-pressed still says whether the dock is open), its
  // name carries the count and data-pf-attention the number; with nothing
  // pending it is exactly the plain toggle. The same renderer serves the wide
  // toolbar and the ≤480px resident slot.
  const renderPolyforgeButton = (mobile: boolean) => {
    if (!pfPresent) return null;
    const attention = pfAttentionCount > 0;
    // Dock closed, a click walks to the next item and the name says so; dock
    // open, a click closes it and the name carries the count alone.
    const label = attention
      ? `${translate("polyforge.open")} · ${translate(pfPanelOpen ? "polyforge.needsInput.count" : "polyforge.needsInput.badgeTitle", { count: pfAttentionCount })}`
      : translate("polyforge.open");
    return (
      <button
        type="button"
        data-pf-toggle
        data-pf-attention={attention ? pfAttentionCount : undefined}
        onClick={attention && !pfPanelOpen ? openPfNeedsInput : togglePolyforgePanel}
        title={label}
        aria-label={label}
        aria-pressed={pfPanelOpen}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          position: "relative",
          width: mobile ? TOP_BAR_ICON_BUTTON_SIZE : undefined,
          minWidth: mobile ? undefined : 34,
          height: "100%",
          padding: mobile ? 0 : "0 10px",
          background: attention ? "#d97706" : pfPanelOpen ? "var(--bg-selected)" : "none",
          border: "none",
          borderRight: "1px solid var(--border)",
          color: attention ? "#fff" : pfRunningCount ? "var(--text)" : "var(--text-muted)",
          cursor: "pointer",
          fontSize: 11,
          flexShrink: 0,
          whiteSpace: "nowrap",
          transition: "color 0.1s, background 0.1s",
        }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M4 21V9l8-5 8 5v12" /><path d="M4 21h16" /><path d="M12 4v5" /><path d="M9 21v-6h6v6" />
        </svg>
        {pfUnreadTotal > 0 && (
          <span
            aria-label={translate("polyforge.messages.unread")}
            title={translate("polyforge.messages.unread")}
            style={{
              position: "absolute",
              bottom: 6,
              left: mobile ? 6 : 10,
              width: 8,
              height: 8,
              borderRadius: 4,
              background: "var(--accent)",
              display: "inline-block",
            }}
          />
        )}
        {pfRunningCount !== null && pfRunningCount > 0 && (
          <span
            aria-label={String(pfRunningCount)}
            style={{
              position: "absolute",
              top: 6,
              right: mobile ? 6 : 10,
              minWidth: 14,
              height: 14,
              padding: "0 3px",
              borderRadius: 7,
              background: "var(--accent)",
              color: "var(--bg-panel)",
              fontSize: 9,
              fontWeight: 700,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              lineHeight: 1,
            }}
          >
            {pfRunningCount > 9 ? "9+" : pfRunningCount}
          </span>
        )}
      </button>
    );
  };

  const renderChatToolbarActions = (mobile: boolean, options: { includePolyforge?: boolean } = {}) => {
    // The phone's resident Polyforge slot owns the button at <=480px; the
    // overlay and the wide inline row keep their original composition.
    const includePolyforge = options.includePolyforge ?? true;
    // The chat actions keep their own gate; the Polyforge button has its own.
    const showChatActions = mobile || showChat;
    if (!showChatActions && !(includePolyforge && pfPresent)) return null;
    return (
      <div style={{ display: "flex", alignItems: "stretch", height: "100%" }}>
        {includePolyforge && renderPolyforgeButton(mobile)}
        {showChatActions && (<>
        {selectedIsClaude ? (
          // HTML export is pi's: unavailable, said in visible text (hidden on a phone's icon row).
          !mobile && (
            <span
              className="claude-export-unavailable"
              style={{ display: "flex", alignItems: "center", padding: "0 12px", borderRight: "1px solid var(--border)", color: "var(--text-dim)", fontSize: 11, whiteSpace: "nowrap", flexShrink: 0 }}
            >
              {translate("claude.exportUnavailable")}
            </span>
          )
        ) : <button
          type="button"
          onClick={() => {
            handleViewFullHistory();
            if (mobile && isNarrowMobile) setMobileToolbarMoreOpen(true);
          }}
          disabled={!selectedSession}
          title={selectedSession ? translate("history.full") : translate("history.unsaved")}
          aria-label={translate("history.full")}
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            gap: 6,
            width: mobile ? TOP_BAR_ICON_BUTTON_SIZE : undefined,
            height: "100%",
            padding: mobile ? 0 : "0 12px",
            background: "none",
            border: "none",
            borderTop: "2px solid transparent",
            borderRight: "1px solid var(--border)",
            color: selectedSession ? "var(--text-muted)" : "var(--text-dim)",
            cursor: selectedSession ? "pointer" : "not-allowed",
            opacity: selectedSession ? 1 : 0.45,
            flexShrink: 0,
            fontSize: 11,
            whiteSpace: "nowrap",
            transition: "color 0.1s, background 0.1s, opacity 0.1s",
          }}
          onMouseEnter={(event) => {
            if (!selectedSession) return;
            event.currentTarget.style.color = "var(--text)";
            event.currentTarget.style.background = "var(--bg-hover)";
          }}
          onMouseLeave={(event) => {
            event.currentTarget.style.color = selectedSession ? "var(--text-muted)" : "var(--text-dim)";
            event.currentTarget.style.background = "none";
          }}
          data-mobile-toolbar-action={mobile ? "history" : undefined}
        >
          <svg
            width={mobile ? 16 : 12}
            height={mobile ? 16 : 12}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            style={{
              color: selectedSession ? "var(--text-muted)" : "var(--text-dim)",
              flexShrink: 0,
            }}
            aria-hidden="true"
          >
            <path d="M3 12a9 9 0 1 0 3-6.7L3 8" />
            <path d="M3 3v5h5" />
            <path d="M12 7v5l3 2" />
          </svg>
          {!mobile && <span>{translate("history.label")}</span>}
        </button>}
        {!selectedIsClaude && (() => {
          // 上下文压缩后当前消息可能不再包含 user 消息，需同时参考会话文件的消息总数。
          const hasMessages = Boolean(
            selectedSession
            && ((sessionStats?.userMessages ?? 0) > 0 || selectedSession.messageCount > 0),
          );
          const disabled = !selectedSession || selectedSession.transient || !hasMessages || autoNameStatus.kind === "naming";
          const isSuccess = autoNameStatus.kind === "success";
          const isError = autoNameStatus.kind === "error";
          const label = autoNameStatus.kind === "naming"
            ? translate("title.generating")
            : isSuccess
              ? translate("title.updated")
              : isError
                ? translate("title.failed")
                : translate("title.generate");
          const title = !selectedSession || selectedSession.transient
            ? translate("title.unsaved")
            : !hasMessages
              ? translate("title.noMessages")
              : isError
                ? autoNameStatus.message
                : translate("title.generateSession");

          return (
            <button
              type="button"
              onClick={() => {
                void handleAutoName();
                if (mobile && isNarrowMobile) setMobileToolbarMoreOpen(true);
              }}
              disabled={disabled}
              title={title}
              aria-label={label}
              style={{
                display: "flex", alignItems: "center", justifyContent: "center", gap: 6,
                width: mobile ? TOP_BAR_ICON_BUTTON_SIZE : undefined,
                height: "100%", padding: mobile ? 0 : "0 12px",
                background: "none", border: "none",
                borderTop: "2px solid transparent",
                borderRight: "1px solid var(--border)",
                color: isError ? "#dc2626" : isSuccess ? "var(--accent)" : disabled ? "var(--text-dim)" : "var(--text-muted)",
                cursor: disabled ? "not-allowed" : "pointer",
                opacity: disabled && autoNameStatus.kind !== "naming" ? 0.45 : 1,
                flexShrink: 0, fontSize: 11, whiteSpace: "nowrap",
                transition: "color 0.1s, background 0.1s, opacity 0.1s",
              }}
              onMouseEnter={(event) => {
                if (disabled) return;
                event.currentTarget.style.color = isError ? "#dc2626" : "var(--text)";
                event.currentTarget.style.background = "var(--bg-hover)";
              }}
              onMouseLeave={(event) => {
                event.currentTarget.style.color = isError ? "#dc2626" : isSuccess ? "var(--accent)" : disabled ? "var(--text-dim)" : "var(--text-muted)";
                event.currentTarget.style.background = "none";
              }}
              data-mobile-toolbar-action={mobile ? "name" : undefined}
            >
              {autoNameStatus.kind === "naming" ? (
                <svg className="animate-spin" width={mobile ? 16 : 13} height={mobile ? 16 : 13} viewBox="0 0 24 24" fill="none" aria-hidden="true">
                  <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2" opacity="0.25" />
                  <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
                </svg>
              ) : isSuccess ? (
                <svg width={mobile ? 16 : 13} height={mobile ? 16 : 13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              ) : (
                <svg width={mobile ? 16 : 13} height={mobile ? 16 : 13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="m15 4 5 5L7 22l-5-5Z" />
                  <path d="m14 5 5 5" />
                  <path d="M6 4V2M5 3H3M19 19v3M17.5 20.5h3" />
                </svg>
              )}
              {!mobile && <span>{label}</span>}
            </button>
          );
        })()}
        {renderAgentsButton(mobile)}
        {sessionHasBranches && (mobile ? (
          <button
            type="button"
            onClick={() => toggleTopPanel("branches", true)}
            title={translate("i18n.branches")}
            aria-label={translate("i18n.branches")}
            aria-pressed={activeTopPanel === "branches"}
            style={{
              display: "flex", alignItems: "center", justifyContent: "center",
              width: TOP_BAR_ICON_BUTTON_SIZE, height: "100%", padding: 0,
              background: activeTopPanel === "branches" ? "var(--bg-selected)" : "none",
              border: "none",
              borderTop: activeTopPanel === "branches" ? "2px solid var(--accent)" : "2px solid transparent",
              borderRight: "1px solid var(--border)",
              color: activeTopPanel === "branches" ? "var(--text)" : "var(--text-muted)",
              cursor: "pointer", flexShrink: 0,
            }}
            data-mobile-toolbar-action="branches"
          >
            <svg width={mobile ? 16 : 12} height={mobile ? 16 : 12} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ color: branchTree.length > 0 ? "var(--accent)" : "var(--text-dim)" }} aria-hidden="true">
              <line x1="6" y1="3" x2="6" y2="15" />
              <circle cx="18" cy="6" r="3" />
              <circle cx="6" cy="18" r="3" />
              <path d="M18 9a9 9 0 0 1-9 9" />
            </svg>
          </button>
        ) : (
          <BranchNavigator
            tree={branchTree}
            activeLeafId={branchActiveLeafId}
            onLeafChange={handleBranchLeafChange}
            locked={branchSwitchLocked}
            inline
            containerRef={topBarRef}
            open={activeTopPanel === "branches"}
            onToggle={() => toggleTopPanel("branches")}
            hasSession
          />
        ))}
        <button
          ref={systemBtnRef}
          type="button"
          onClick={() => handleSystemInfoToggle("system", mobile)}
          disabled={mobile && !showChat}
          title={translate("system.prompt")}
          aria-label={translate("system.prompt")}
          aria-pressed={activeTopPanel === "system"}
          style={{
            display: "flex", alignItems: "center", justifyContent: "center", gap: 6,
            width: mobile ? TOP_BAR_ICON_BUTTON_SIZE : undefined,
            height: "100%", padding: mobile ? 0 : "0 12px",
            background: activeTopPanel === "system" ? "var(--bg-selected)" : "none",
            border: "none",
            borderTop: activeTopPanel === "system" ? "2px solid var(--accent)" : "2px solid transparent",
            borderRight: "1px solid var(--border)",
            cursor: mobile && !showChat ? "not-allowed" : "pointer",
            color: activeTopPanel === "system" ? "var(--text)" : "var(--text-muted)",
            opacity: mobile && !showChat ? 0.45 : 1,
            fontSize: 11, whiteSpace: "nowrap", transition: "color 0.1s, background 0.1s",
          }}
          onMouseEnter={(event) => {
            if (mobile && !showChat) return;
            event.currentTarget.style.color = "var(--text)";
          }}
          onMouseLeave={(event) => {
            event.currentTarget.style.color = activeTopPanel === "system" ? "var(--text)" : "var(--text-muted)";
          }}
          data-mobile-toolbar-action={mobile ? "system" : undefined}
        >
          <svg width={mobile ? 16 : 12} height={mobile ? 16 : 12} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ color: systemPrompt ? "var(--accent)" : "var(--text-dim)", flexShrink: 0 }} aria-hidden="true">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
            <polyline points="14 2 14 8 20 8" />
            <line x1="8" y1="13" x2="16" y2="13" />
            <line x1="8" y1="17" x2="13" y2="17" />
          </svg>
          {!mobile && <span>{translate("system.label")}</span>}
        </button>
        <button
          type="button"
          onClick={() => handleSystemInfoToggle("tools", mobile)}
          disabled={mobile && !showChat}
          title={translate("tools.title")}
          aria-label={translate("tools.title")}
          aria-pressed={activeTopPanel === "tools"}
          style={{
            display: "flex", alignItems: "center", justifyContent: "center", gap: 6,
            width: mobile ? TOP_BAR_ICON_BUTTON_SIZE : undefined,
            height: "100%", padding: mobile ? 0 : "0 12px",
            background: activeTopPanel === "tools" ? "var(--bg-selected)" : "none",
            border: "none",
            borderTop: activeTopPanel === "tools" ? "2px solid var(--accent)" : "2px solid transparent",
            borderRight: "1px solid var(--border)",
            cursor: mobile && !showChat ? "not-allowed" : "pointer",
            color: activeTopPanel === "tools" ? "var(--text)" : "var(--text-muted)",
            opacity: mobile && !showChat ? 0.45 : 1,
            fontSize: 11, whiteSpace: "nowrap", transition: "color 0.1s, background 0.1s",
          }}
          onMouseEnter={(event) => {
            if (mobile && !showChat) return;
            event.currentTarget.style.color = "var(--text)";
          }}
          onMouseLeave={(event) => {
            event.currentTarget.style.color = activeTopPanel === "tools" ? "var(--text)" : "var(--text-muted)";
          }}
          data-mobile-toolbar-action={mobile ? "tools" : undefined}
        >
          <svg width={mobile ? 16 : 12} height={mobile ? 16 : 12} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ color: systemTools?.some((tool) => tool.active) ? "var(--accent)" : "var(--text-dim)", flexShrink: 0 }} aria-hidden="true">
            <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.8-3.8a6 6 0 0 1-7.9 7.9l-6.9 6.9a2.1 2.1 0 0 1-3-3l6.9-6.9a6 6 0 0 1 7.9-7.9z" />
          </svg>
          {!mobile && <span>{translate("tools.label")}</span>}
        </button>
        </>)}
      </div>
    );
  };

  const renderSessionStatsButton = (mobile: boolean) => {
    if (!mobile && (!showChat || (!sessionStats && !contextUsage))) return null;

    const tokens = sessionStats?.tokens;
    const cost = sessionStats?.cost ?? 0;
    const formatCompact = formatCompactCount;
    const costText = formatCostShort(cost);

    let contextColor = "var(--text-muted)";
    let desktopContextText: string | null = null;
    let mobileContextText: string | null = null;
    if (contextUsage?.contextWindow) {
      const percent = contextUsage.percent;
      if (percent !== null && percent > 90) contextColor = "#ef4444";
      else if (percent !== null && percent > 70) contextColor = "rgba(234,179,8,0.95)";
      desktopContextText = percent !== null
        ? `${percent.toFixed(0)}% / ${formatCompact(contextUsage.contextWindow)}`
        : `? / ${formatCompact(contextUsage.contextWindow)}`;
      mobileContextText = percent !== null ? `${percent.toFixed(0)}%` : null;
    }

    const tooltipParts: string[] = [];
    if (tokens) {
      tooltipParts.push(`${translate("session.input")}: ${tokens.input.toLocaleString(locale)}`);
      tooltipParts.push(`${translate("session.output")}: ${tokens.output.toLocaleString(locale)}`);
      tooltipParts.push(`${translate("session.cacheRead")}: ${tokens.cacheRead.toLocaleString(locale)}`);
      tooltipParts.push(`${translate("session.cacheWrite")}: ${tokens.cacheWrite.toLocaleString(locale)}`);
      if (cost > 0) tooltipParts.push(`${translate("session.cost")}: $${cost.toFixed(4)}`);
    }
    if (contextUsage?.contextWindow) {
      const percent = contextUsage.percent;
      tooltipParts.push(`${translate("session.context")}: ${percent !== null ? `${percent.toFixed(1)}%` : "?"} / ${contextUsage.contextWindow.toLocaleString(locale)}`);
    }
    const tooltip = tooltipParts.join("  |  ");
    const covered = mobile && isNarrowMobile && mobileToolbarMoreOpen;
    const hasMobileValues = Boolean(
      (tokens && (tokens.input > 0 || tokens.output > 0))
      || costText
      || mobileContextText,
    );

    return (
      <>
      <button
        type="button"
        onClick={() => toggleTopPanel("session")}
        disabled={!showChat || covered}
        tabIndex={covered ? -1 : undefined}
        title={tooltip || translate("session.title")}
        aria-label={translate("session.title")}
        aria-pressed={activeTopPanel === "session"}
        aria-hidden={covered ? true : undefined}
        className={mobile ? "mobile-session-stats" : undefined}
        data-mobile-toolbar-stats={mobile ? "true" : undefined}
        style={{
          // The Debug button before it takes the free space (renderDebugButton).
          marginLeft: 0,
          display: "flex", alignItems: "center", justifyContent: mobile ? "flex-start" : "flex-end",
          flex: mobile ? 1 : undefined,
          minWidth: 0,
          gap: mobile ? 7 : 10,
          paddingLeft: mobile ? 6 : 12,
          paddingRight: mobile ? 6 : 12,
          height: "100%",
          overflowX: mobile ? "auto" : "hidden",
          overflowY: "hidden",
          visibility: covered ? "hidden" : "visible",
          pointerEvents: covered ? "none" : "auto",
          background: activeTopPanel === "session" ? "var(--bg-selected)" : "none",
          border: "none",
          borderTop: activeTopPanel === "session" ? "2px solid var(--accent)" : "2px solid transparent",
          fontSize: 11, color: "var(--text-muted)",
          whiteSpace: "nowrap", cursor: showChat ? "pointer" : "default",
          fontVariantNumeric: "tabular-nums",
          transition: "color 0.1s, background 0.1s",
        }}
        onMouseEnter={(event) => {
          if (showChat && !covered) event.currentTarget.style.color = "var(--text)";
        }}
        onMouseLeave={(event) => {
          event.currentTarget.style.color = activeTopPanel === "session" ? "var(--text)" : "var(--text-muted)";
        }}
      >
        {mobile ? (
          <span style={{ display: "flex", alignItems: "center", gap: 7, marginLeft: "auto", minWidth: "max-content", height: "100%" }}>
            {tokens && tokens.input > 0 && (
              <span className="mobile-session-stat-io" style={{ display: "flex", alignItems: "center", gap: 2, flexShrink: 0 }}>
                <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <line x1="5" y1="8.5" x2="5" y2="1.5" /><polyline points="2 4 5 1.5 8 4" />
                </svg>
                {formatCompact(tokens.input)}
              </span>
            )}
            {tokens && tokens.output > 0 && (
              <span className="mobile-session-stat-io" style={{ display: "flex", alignItems: "center", gap: 2, flexShrink: 0 }}>
                <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <line x1="5" y1="1.5" x2="5" y2="8.5" /><polyline points="2 6 5 8.5 8 6" />
                </svg>
                {formatCompact(tokens.output)}
              </span>
            )}
            {costText && (
              <span className="mobile-session-stat-cost" style={{ color: "var(--text)", fontWeight: 500, flexShrink: 0 }}>
                {costText}
              </span>
            )}
            {mobileContextText && (
              <span style={{ color: contextColor, flexShrink: 0 }}>
                {mobileContextText}
              </span>
            )}
            {!hasMobileValues && showChat && (
              <span style={{ overflow: "hidden", textOverflow: "ellipsis", color: "var(--text-dim)" }}>
                {translate("session.title")}
              </span>
            )}
          </span>
        ) : (
          <>
            {tokens && tokens.input > 0 && (
              <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
                <svg width="12" height="12" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <line x1="5" y1="8.5" x2="5" y2="1.5" /><polyline points="2 4 5 1.5 8 4" />
                </svg>
                {formatCompact(tokens.input)}
              </span>
            )}
            {tokens && tokens.output > 0 && (
              <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
                <svg width="12" height="12" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <line x1="5" y1="1.5" x2="5" y2="8.5" /><polyline points="2 6 5 8.5 8 6" />
                </svg>
                {formatCompact(tokens.output)}
              </span>
            )}
            {tokens && tokens.cacheRead > 0 && (
              <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
                <svg width="12" height="12" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M8.5 5a3.5 3.5 0 1 1-1-2.45" /><polyline points="6.5 1.5 8.5 2.5 7.5 4.5" />
                </svg>
                {formatCompact(tokens.cacheRead)}
              </span>
            )}
            {costText && (
              <span style={{ display: "flex", alignItems: "center", color: "var(--text)", fontWeight: 500 }}>
                {costText}
              </span>
            )}
            {desktopContextText && (
              <span style={{ display: "flex", alignItems: "center", gap: 4, color: contextColor }}>
                <svg width="12" height="12" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M1 9 L1 5 Q1 1 5 1 Q9 1 9 5 L9 9" /><line x1="1" y1="9" x2="9" y2="9" />
                </svg>
                {desktopContextText}
              </span>
            )}
          </>
        )}
      </button>
      </>
    );
  };

  // Icon only, right of the chat's actions; on a desktop it takes the bar's free space, so it
  // sits left of the session stats. It only opens or closes its panel: nothing is loaded here.
  const renderDebugButton = (mobile: boolean) => {
    if (!showChat) return null;
    const open = activeTopPanel === "debug";
    return (
      <button
        ref={debugButtonRef}
        type="button"
        onClick={() => toggleTopPanel("debug", mobile)}
        title={translate("debug.title")}
        aria-label={translate("debug.title")}
        aria-pressed={open}
        style={{
          marginLeft: mobile ? 0 : "auto",
          display: "flex", alignItems: "center", justifyContent: "center",
          width: TOP_BAR_ICON_BUTTON_SIZE, height: "100%", padding: 0,
          background: open ? "var(--bg-selected)" : "none",
          border: "none",
          borderTop: open ? "2px solid var(--accent)" : "2px solid transparent",
          ...(mobile ? { borderRight: "1px solid var(--border)" } : { borderLeft: "1px solid var(--border)" }),
          color: open ? "var(--text)" : "var(--text-muted)",
          cursor: "pointer", flexShrink: 0, transition: "color 0.1s, background 0.1s",
        }}
        onMouseEnter={(event) => {
          event.currentTarget.style.color = "var(--text)";
        }}
        onMouseLeave={(event) => {
          event.currentTarget.style.color = open ? "var(--text)" : "var(--text-muted)";
        }}
        data-mobile-toolbar-action={mobile ? "debug" : undefined}
      >
        <svg width={mobile ? 16 : 13} height={mobile ? 16 : 13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ color: open ? "var(--accent)" : "var(--text-dim)", flexShrink: 0 }} aria-hidden="true">
          <path d="M8 2l1.9 1.9M16 2l-1.9 1.9M9 7.1v-1a3 3 0 0 1 6 0v1" />
          <path d="M12 20c-3.3 0-6-2.7-6-6v-3a4 4 0 0 1 4-4h4a4 4 0 0 1 4 4v3c0 3.3-2.7 6-6 6zM12 20v-9" />
          <path d="M6.5 9C4.6 8.8 3 7.1 3 5M6 13H2M3 21c0-2.1 1.7-3.9 3.8-4M21 5c0 2.1-1.6 3.8-3.5 4M22 13h-4M17.2 17c2.1.1 3.8 1.9 3.8 4" />
        </svg>
      </button>
    );
  };

  const renderMainFileToggle = (mobile: boolean) => {
    const covered = mobile && isNarrowMobile && mobileToolbarMoreOpen;
    return (
      <button
        type="button"
        onClick={handleRightPanelToggle}
        disabled={covered}
        tabIndex={covered ? -1 : undefined}
        aria-controls="file-panel"
        aria-expanded={rightPanelOpen}
        aria-hidden={covered ? true : undefined}
        title={rightPanelOpen ? translate("files.hidePanel") : translate("files.showPanel")}
        aria-label={rightPanelOpen ? translate("files.hidePanel") : translate("files.showPanel")}
        data-mobile-toolbar-file={mobile ? "true" : undefined}
        style={{
          // With a chat the Debug button is right-aligned instead (renderDebugButton).
          marginLeft: !mobile && !showChat ? "auto" : 0,
          display: "flex", alignItems: "center", justifyContent: "center",
          width: TOP_BAR_ICON_BUTTON_SIZE, height: TOP_BAR_ICON_BUTTON_SIZE, padding: 0,
          visibility: covered ? "hidden" : "visible",
          pointerEvents: covered ? "none" : "auto",
          background: rightPanelOpen ? "var(--bg-selected)" : "none",
          border: "none", borderLeft: "1px solid var(--border)",
          color: rightPanelOpen ? "var(--text)" : "var(--text-muted)",
          cursor: "pointer", flexShrink: 0, transition: "color 0.12s, background 0.12s",
        }}
        onMouseEnter={(event) => { if (!covered) event.currentTarget.style.color = "var(--text)"; }}
        onMouseLeave={(event) => { event.currentTarget.style.color = rightPanelOpen ? "var(--text)" : "var(--text-muted)"; }}
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <rect x="3" y="3" width="18" height="18" rx="2" /><line x1="15" y1="3" x2="15" y2="21" />
        </svg>
      </button>
    );
  };

  return (
    <>
    <style>{`
      @keyframes session-info-pop {
        0% {
          opacity: 0;
          transform: translateY(-24px);
          filter: blur(6px);
          box-shadow: 0 2px 8px rgba(0,0,0,0);
        }
        55% {
          opacity: 1;
          transform: translateY(0);
          filter: blur(0);
          background: color-mix(in srgb, var(--accent) 8%, var(--bg-panel));
          box-shadow: 0 18px 44px rgba(37,99,235,0.16);
        }
        100% {
          opacity: 1;
          transform: translateY(0);
          filter: blur(0);
          background: var(--bg-panel);
          box-shadow: 0 10px 28px rgba(0,0,0,0.10);
        }
      }
      @keyframes session-info-light-wash {
        0% {
          opacity: 0;
          transform: translateX(-110%) skewX(-16deg);
        }
        24% {
          opacity: 0.42;
        }
        100% {
          opacity: 0;
          transform: translateX(115%) skewX(-16deg);
        }
      }
      .session-info-popover {
        position: relative;
        overflow: hidden;
        transform-origin: top right;
        animation: session-info-pop 360ms ease-out both;
        will-change: transform, opacity, filter, background, box-shadow;
      }
      .session-info-popover::after {
        content: "";
        position: absolute;
        top: 0;
        bottom: 0;
        left: 0;
        width: 44%;
        pointer-events: none;
        background: linear-gradient(90deg, transparent, color-mix(in srgb, var(--accent) 24%, transparent), transparent);
        animation: session-info-light-wash 620ms ease-out both;
      }
      @media (prefers-reduced-motion: reduce) {
        .session-info-popover,
        .session-info-popover::after {
          animation: none;
        }
      }
      .mobile-session-stats {
        container-type: inline-size;
        /* phone stats scroll instead of clipping; the wrapper's
           auto margin keeps the old right alignment while the items fit. */
        scrollbar-width: none;
        -webkit-overflow-scrolling: touch;
        overscroll-behavior-x: contain;
      }
      .mobile-session-stats::-webkit-scrollbar {
        display: none;
      }
      @container (max-width: 158px) {
        .mobile-session-stat-io {
          display: none !important;
        }
      }
      @container (max-width: 88px) {
        .mobile-session-stat-cost {
          display: none !important;
        }
      }
      @media (max-width: 640px) {
        .sidebar-overlay-backdrop.sidebar-mobile-pending {
          opacity: 0 !important;
          pointer-events: none !important;
        }
        .sidebar-container.sidebar-mobile-pending.sidebar-open {
          transform: translateX(calc(-100% - env(safe-area-inset-left)));
          box-shadow: none;
        }
      }
    `}</style>
    <div style={{
      display: "flex",
      width: "100%",
      height: "var(--app-viewport-height, 100dvh)",
      paddingLeft: "env(safe-area-inset-left)",
      paddingRight: "env(safe-area-inset-right)",
      overflow: "hidden",
      background: "var(--bg)",
    }}>
      {/* Mobile overlay backdrop */}
      <div
        className={`sidebar-overlay-backdrop${mobileSidebarReady ? "" : " sidebar-mobile-pending"}`}
        onClick={() => setSidebarOpen(false)}
        style={{
          position: "fixed",
          inset: 0,
          zIndex: 199,
          background: "rgba(0,0,0,0.4)",
          opacity: sidebarOpen ? 1 : 0,
          pointerEvents: sidebarOpen ? "auto" : "none",
          transition: "opacity 0.25s ease",
        }}
      />

      {/* Left sidebar */}
      <div
        ref={sidebarResizer.panelRef}
        id="session-sidebar"
        inert={rightPanelFullWidth}
        className={`sidebar-container${sidebarOpen ? " sidebar-open" : " sidebar-closed"}${mobileSidebarReady ? "" : " sidebar-mobile-pending"}${sidebarResizer.isResizing ? " sidebar-resizing" : ""}`}
        style={{
          "--sidebar-width": `${sidebarResizer.width}px`,
          background: "var(--bg-panel)",
          borderRight: "1px solid var(--border)",
          display: "flex",
          flexDirection: "column",
          flexShrink: 0,
          paddingTop: "var(--safe-area-top, 0px)",
          paddingBottom: "var(--safe-area-bottom, 0px)",
          zIndex: 200,
        } as React.CSSProperties}
      >
        {sidebarContent}
      </div>
      {sidebarOpen && (
        <div
          {...sidebarResizer.separatorProps}
          inert={rightPanelFullWidth}
          aria-controls="session-sidebar"
          className={`panel-resize-handle sidebar-resize-handle${sidebarResizer.isResizing ? " is-resizing" : ""}`}
          data-resize-handle="sidebar"
          title={`${translate("layout.resizeSidebar")}: ${translate("layout.resizeHint")}`}
        />
      )}

      {/* Center: chat */}
      <div inert={rightPanelFullWidth} style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minWidth: 0 }}>
        {/* Top bar with sidebar toggle */}
        <div ref={topBarRef} style={{ flexShrink: 0, background: "var(--bg-panel)" }}>
        <div style={{ display: "flex", alignItems: "center", position: "relative", borderBottom: "1px solid var(--border)", height: "calc(var(--top-bar-height) + env(safe-area-inset-top))", paddingTop: "env(safe-area-inset-top)" }}>
          {!isMobile && !splitPaneEnabled && selectedSession && (
            <button
              type="button"
              onClick={() => {
                // Opt-in: enabling split view opens the current session as the first pane.
                setSplitPaneEnabled(true);
                const sid = selectedSession.id;
                const label = selectedSession.name || selectedSession.firstMessage || sid.slice(0, 12);
                setPaneTabs((prev) => (prev.some((t) => t.sessionId === sid) ? prev : openPaneOp(prev, sid, label, projectDisplayNameForPath(selectedSession.projectRoot ?? selectedSession.cwd))));
                setFocusedPaneId(sid);
              }}
              title="Enable split view"
              aria-label="Enable split view"
              style={{
                display: "flex", alignItems: "center", justifyContent: "center",
                width: TOP_BAR_ICON_BUTTON_SIZE, height: TOP_BAR_ICON_BUTTON_SIZE, padding: 0,
                background: "none", border: "none", borderRight: "1px solid var(--border)",
                color: "var(--text-muted)", cursor: "pointer", flexShrink: 0,
              }}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
                <rect x="2" y="4" width="20" height="16" rx="1.5" /><line x1="12" y1="4" x2="12" y2="20" />
              </svg>
            </button>
          )}
          <button
            onClick={handleSidebarToggle}
             title={sidebarOpen ? translate("sidebar.hide") : translate("sidebar.show")}
             aria-label={sidebarOpen ? translate("sidebar.hide") : translate("sidebar.show")}
            style={{
              display: "flex", alignItems: "center", justifyContent: "center",
              width: TOP_BAR_ICON_BUTTON_SIZE, height: TOP_BAR_ICON_BUTTON_SIZE, padding: 0,
              background: "none", border: "none", borderRight: "1px solid var(--border)",
              color: "var(--text-muted)", cursor: "pointer", flexShrink: 0, transition: "color 0.12s",
            }}
            onMouseEnter={(e) => { e.currentTarget.style.color = "var(--text)"; }}
            onMouseLeave={(e) => { e.currentTarget.style.color = "var(--text-muted)"; }}
          >
            {sidebarOpen ? (
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <rect x="3" y="3" width="18" height="18" rx="2" /><line x1="9" y1="3" x2="9" y2="21" />
              </svg>
            ) : (
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                <line x1="3" y1="6" x2="21" y2="6" /><line x1="3" y1="12" x2="21" y2="12" /><line x1="3" y1="18" x2="21" y2="18" />
              </svg>
            )}
          </button>
          {isMobile && (
            <div
              ref={mobileToolbarRef}
              data-mobile-toolbar="true"
              style={{
                position: "relative",
                display: "flex",
                alignItems: "stretch",
                flex: 1,
                minWidth: 0,
                height: "100%",
              }}
            >
              {isNarrowMobile && renderAgentsButton(true)}
              {isNarrowMobile && (
                <button
                  type="button"
                  onClick={handleMobileToolbarMoreToggle}
                  title={moreControlsLabel}
                  aria-label={moreControlsLabel}
                  aria-controls="mobile-toolbar-actions"
                  aria-expanded={mobileToolbarMoreOpen}
                  data-mobile-toolbar-more="true"
                  style={{
                    position: "relative",
                    zIndex: mobileToolbarMoreOpen ? 21 : undefined,
                    display: "flex", alignItems: "center", justifyContent: "center",
                    width: TOP_BAR_ICON_BUTTON_SIZE, height: TOP_BAR_ICON_BUTTON_SIZE, padding: 0,
                    background: mobileToolbarMoreOpen ? "var(--bg-selected)" : "none",
                    border: "none", borderRight: "1px solid var(--border)",
                    color: mobileToolbarMoreOpen ? "var(--text)" : "var(--text-muted)",
                    cursor: "pointer", flexShrink: 0, transition: "color 0.12s, background 0.12s",
                  }}
                >
                  {mobileToolbarMoreOpen ? (
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                      <line x1="5" y1="5" x2="19" y2="19" /><line x1="19" y1="5" x2="5" y2="19" />
                    </svg>
                  ) : (
                    <svg width="17" height="17" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                      <circle cx="5" cy="12" r="1.5" /><circle cx="12" cy="12" r="1.5" /><circle cx="19" cy="12" r="1.5" />
                    </svg>
                  )}
                </button>
              )}
              {!isNarrowMobile && renderChatToolbarActions(true)}
              {isNarrowMobile && renderPolyforgeButton(true)}
              {!isNarrowMobile && renderDebugButton(true)}
              {renderSessionStatsButton(true)}
              {renderMainFileToggle(true)}
              {isNarrowMobile && mobileToolbarMoreOpen && (
                <div
                  id="mobile-toolbar-actions"
                  role="toolbar"
                  aria-label={translate("chat.moreControls")}
                  data-mobile-toolbar-actions="true"
                  style={{
                    position: "absolute",
                    top: 0,
                    right: 0,
                    bottom: 0,
                    // Right of the Agents and More buttons, which stay in place.
                    left: `calc(2 * ${TOP_BAR_ICON_BUTTON_SIZE})`,
                    zIndex: 20,
                    display: "flex",
                    alignItems: "stretch",
                    // More buttons than a 320px phone fits (the Debug button made seven): scroll, never clip.
                    overflowX: "auto",
                    overflowY: "hidden",
                    scrollbarWidth: "none",
                    background: "color-mix(in srgb, var(--bg-panel) 94%, var(--bg))",
                    boxShadow: "4px 0 18px rgba(0,0,0,0.12)",
                    backdropFilter: "blur(10px)",
                  }}
                >
                  {renderChatToolbarActions(true, { includePolyforge: false })}
                  {renderDebugButton(true)}
                </div>
              )}
            </div>
          )}
          {!isMobile && (
            <>
              {renderProjectTrustWarning(false)}
              {renderChatToolbarActions(false)}
              {renderDebugButton(false)}
              {renderSessionStatsButton(false)}
            </>
          )}
          {!isMobile && renderMainFileToggle(false)}
          {isMobile && sessionHasBranches && (
            <BranchNavigator
              tree={branchTree}
              activeLeafId={branchActiveLeafId}
              onLeafChange={handleBranchLeafChange}
              locked={branchSwitchLocked}
              inline
              compact
              containerRef={topBarRef}
              open={activeTopPanel === "branches"}
              onToggle={() => toggleTopPanel("branches")}
              hasSession={showChat}
              hideInlineButton
            />
          )}
          {/* Top panel dropdown — shared, only one active at a time */}
          {activeTopPanel && topPanelPos && (
            <div style={{
              position: "fixed",
              top: topPanelPos.top,
              left: topPanelPos.left,
              width: topPanelPos.width,
              maxHeight: `calc(100dvh - ${topPanelPos.top}px)`,
              overflowY: "auto",
              zIndex: 500,
            }}>
              {activeTopPanel === "agents" && activeSessionFamily && selectedSession && (
                <AgentSessionPanel
                  rootSession={activeSessionFamily.root}
                  subagents={activeSessionFamily.subagents}
                  selectedSessionId={selectedSession.id}
                  runningSessionIds={runningSessionIds}
                  onSelectSession={handleSelectSession}
                  openSubagentTabIds={openSubagentTabIds}
                  onToggleSubagentTab={handleToggleSubagentTab}
                />
              )}
              {activeTopPanel === "system" && (
                <SystemPromptPanel
                  loading={systemInfoLoading}
                  prompt={systemPrompt}
                  translate={translate}
                />
              )}
              {activeTopPanel === "tools" && (
                <ToolDefinitionsPanel
                  loading={systemInfoLoading}
                  tools={systemTools}
                  translate={translate}
                />
              )}
              {activeTopPanel === "session" && (
                <div className="session-info-popover" style={{
                  background: "var(--bg-panel)",
                  borderBottom: "1px solid var(--border)",
                  boxShadow: "0 10px 28px rgba(0,0,0,0.10)",
                  padding: "12px 16px",
                }}>
                  {sessionStats ? (() => {
                    const totalActiveMs = sessionStats.totalActiveMs ?? 0;
                    const ws = selectedSession;
                    const sessionRows = [
                       ...(sessionStats.sessionName ? [{ label: translate("session.name"), value: sessionStats.sessionName, copyField: null }] : []),
                       { label: translate("session.file"), value: sessionStats.sessionFile ?? translate("session.inMemory"), copyField: "file" as const },
                       { label: translate("session.id"), value: sessionStats.sessionId, copyField: "id" as const },
                       ...(totalActiveMs > 0 ? [{ label: translate("session.totalActive"), value: formatActiveDuration(totalActiveMs), copyField: null }] : []),
                    ];
                    const projectRows = [
                      ...(ws ? [{ label: translate("session.projectDir"), value: ws.projectRoot ?? ws.cwd, copyField: "projectDir" as const }] : []),
                      ...(ws?.branch ? [{ label: translate("session.gitBranch"), value: ws.branch, copyField: "gitBranch" as const }] : []),
                      ...(ws?.isWorktree ? [{ label: translate("session.gitWorktree"), value: ws.cwd, copyField: "gitWorktree" as const }] : []),
                    ];
                    const messageRows = [
                       [translate("session.user"), sessionStats.userMessages.toLocaleString(locale)],
                       [translate("session.assistant"), sessionStats.assistantMessages.toLocaleString(locale)],
                       [translate("session.toolCalls"), sessionStats.toolCalls.toLocaleString(locale)],
                       [translate("session.toolResults"), sessionStats.toolResults.toLocaleString(locale)],
                       [translate("session.total"), sessionStats.totalMessages.toLocaleString(locale)],
                    ];
                    const tokenRows = [
                       [translate("session.input"), sessionStats.tokens.input.toLocaleString(locale)],
                       [translate("session.output"), sessionStats.tokens.output.toLocaleString(locale)],
                       ...(sessionStats.tokens.cacheRead > 0 ? [[translate("session.cacheRead"), sessionStats.tokens.cacheRead.toLocaleString(locale)]] : []),
                       ...(sessionStats.tokens.cacheWrite > 0 ? [[translate("session.cacheWrite"), sessionStats.tokens.cacheWrite.toLocaleString(locale)]] : []),
                       [translate("session.total"), sessionStats.tokens.total.toLocaleString(locale)],
                    ];
                    const ctx = contextUsage ?? sessionStats.contextUsage;
                    const extraTokenRows = [
                       ...(sessionStats.cost > 0 ? [[translate("session.cost"), `$${sessionStats.cost.toFixed(4)}`]] : []),
                       ...(ctx?.contextWindow ? [[translate("session.context"), `${ctx.percent !== null ? `${ctx.percent.toFixed(1)}%` : "?"} / ${formatCompactCount(ctx.contextWindow)}`]] : []),
                       // Cache hit rate = cache reads / (input + cache writes + cache reads) — the denominator covers all input-class tokens.
                       ...(sessionStats.tokens.cacheRead + sessionStats.tokens.cacheWrite > 0 && sessionStats.tokens.cacheRead + sessionStats.tokens.cacheWrite + sessionStats.tokens.input > 0
                         ? [[translate("session.cacheHitRate"), `${(sessionStats.tokens.cacheRead / (sessionStats.tokens.cacheRead + sessionStats.tokens.cacheWrite + sessionStats.tokens.input) * 100).toFixed(1)}%`]]
                         : []),
                    ];
                    const section = (
                      title: string,
                      sectionRows: string[][],
                      valueAlign: "left" | "right" = "left",
                      compact = false,
                    ) => (
                        <div style={{ minWidth: 0 }}>
                          <div style={{ fontSize: 11, fontWeight: 700, color: "var(--text)", marginBottom: 6 }}>{title}</div>
                          <div style={{
                            display: "grid",
                            gridTemplateColumns: compact ? "max-content max-content" : "auto minmax(0, 1fr)",
                            columnGap: compact ? 14 : 12,
                            rowGap: 4,
                            justifyContent: compact ? "start" : undefined,
                          }}>
                            {sectionRows.map(([label, value]) => (
                              <div key={`${title}:${label}`} style={{ display: "contents" }}>
                                <div style={{ color: "var(--text-dim)", whiteSpace: "nowrap" }}>{label}</div>
                                <div style={{
                                  color: "var(--text-muted)",
                                  minWidth: 0,
                                  overflowWrap: compact ? "normal" : "anywhere",
                                  textAlign: valueAlign,
                                  whiteSpace: valueAlign === "right" ? "nowrap" : "normal",
                                }}>{value}</div>
                              </div>
                            ))}
                          </div>
                        </div>
                      );
                    const copyTitleKey: Record<SessionCopyField, string> = {
                      file: "session.copyFile",
                      id: "session.copyId",
                      projectDir: "session.copyProjectDir",
                      gitBranch: "session.copyGitBranch",
                      gitWorktree: "session.copyGitWorktree",
                    };
                    const copyButton = (field: SessionCopyField, value: string) => {
                      const copied = copiedSessionField === field;
                      return (
                        <button
                          type="button"
                          title={copied ? translate("session.copied") : translate(copyTitleKey[field])}
                          onClick={() => handleCopySessionField(field, value)}
                          style={{
                            alignSelf: "start",
                            display: "inline-flex",
                            alignItems: "center",
                            justifyContent: "center",
                            width: 22,
                            height: 22,
                            marginTop: -2,
                            color: copied ? "var(--accent)" : "var(--text-dim)",
                            background: "transparent",
                            border: "1px solid var(--border)",
                            borderRadius: 4,
                            cursor: "pointer",
                            flex: "0 0 auto",
                            transition: "color 0.12s, border-color 0.12s, background 0.12s",
                          }}
                          onMouseEnter={(e) => {
                            e.currentTarget.style.color = "var(--accent)";
                            e.currentTarget.style.borderColor = "var(--accent)";
                            e.currentTarget.style.background = "var(--bg-hover)";
                          }}
                          onMouseLeave={(e) => {
                            e.currentTarget.style.color = copied ? "var(--accent)" : "var(--text-dim)";
                            e.currentTarget.style.borderColor = "var(--border)";
                            e.currentTarget.style.background = "transparent";
                          }}
                        >
                          {copied ? (
                            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                              <polyline points="20 6 9 17 4 12" />
                            </svg>
                          ) : (
                            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                              <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
                              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                            </svg>
                          )}
                        </button>
                      );
                    };
                    const sessionInfoSection = (
                      <div style={{ minWidth: 0 }}>
                         <div style={{ fontSize: 11, fontWeight: 700, color: "var(--text)", marginBottom: 6 }}>{translate("session.infoSection")}</div>
                        <div style={{ display: "grid", gridTemplateColumns: "auto minmax(0, 1fr) auto", columnGap: 12, rowGap: 8, alignItems: "start" }}>
                          {sessionRows.map((row) => (
                            <div key={`session-info:${row.label}`} style={{ display: "contents" }}>
                              <div style={{ color: "var(--text-dim)", whiteSpace: "nowrap" }}>{row.label}</div>
                              <div style={{
                                color: "var(--text-muted)",
                                minWidth: 0,
                                overflowWrap: "anywhere",
                                wordBreak: "break-word",
                                whiteSpace: "normal",
                              }}>{row.value}</div>
                              <div>{row.copyField ? copyButton(row.copyField, row.value) : null}</div>
                            </div>
                          ))}
                        </div>
                      </div>
                    );
                    const projectInfoSection = projectRows.length > 0 ? (
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontSize: 11, fontWeight: 700, color: "var(--text)", marginBottom: 6 }}>{translate("session.projectSection")}</div>
                        <div style={{ display: "grid", gridTemplateColumns: "auto minmax(0, 1fr) auto", columnGap: 12, rowGap: 8, alignItems: "start" }}>
                          {projectRows.map((row) => (
                            <div key={`project-info:${row.label}`} style={{ display: "contents" }}>
                              <div style={{ color: "var(--text-dim)", whiteSpace: "nowrap" }}>{row.label}</div>
                              <div style={{
                                color: "var(--text-muted)",
                                minWidth: 0,
                                overflowWrap: "anywhere",
                                wordBreak: "break-word",
                                whiteSpace: "normal",
                              }}>{row.value}</div>
                              <div>{row.copyField ? copyButton(row.copyField, row.value) : null}</div>
                            </div>
                          ))}
                        </div>
                      </div>
                    ) : null;

                    return (
                      <div style={{
                        display: "grid",
                        gridTemplateColumns: isMobile
                          ? "1fr"
                          : "minmax(360px, 1.7fr) minmax(140px, 0.55fr) minmax(190px, 0.75fr)",
                        gap: isMobile ? 16 : 24,
                        fontSize: 12,
                        lineHeight: 1.5,
                        fontFamily: "var(--font-mono)",
                      }}>
                        <div style={{ display: "flex", flexDirection: "column", gap: isMobile ? 16 : 20 }}>
                          {sessionInfoSection}
                          {projectInfoSection}
                        </div>
                         {section(translate("session.messages"), messageRows)}
                         {section(translate("session.tokens"), [...tokenRows, ...extraTokenRows], "right", true)}
                      </div>
                    );
                  })() : (
                    <div style={{ fontSize: 12, color: "var(--text-muted)", fontStyle: "italic" }}>
                       {translate("session.load")}
                    </div>
                  )}
                </div>
              )}
              {activeTopPanel === "debug" && (
                <DebugPanelBoundary fallback={translate("debug.failed")}>
                  <DebugPanel
                    readerRef={debugSnapshotReaderRef}
                    selectedSessionId={selectedSession?.id ?? null}
                    sessions={sessionsWithSelection}
                    onClose={closeDebugPanel}
                  />
                </DebugPanelBoundary>
              )}
            </div>
          )}

        </div>
        {isMobile && renderProjectTrustWarning(true)}
        </div>

        {/* Chat content */}
        <div style={{ flex: 1, overflow: "hidden", position: "relative" }}>
          {showChat && !isMobile && paneTabs.length > 0 ? (
            <SplitPaneLayout
              ref={splitPaneLayoutRef}
              tabs={paneTabs}
              focusedId={focusedPaneId}
              runningSessionIds={runningSessionIds}
              onFocusPane={(sid) => {
                setFocusedPaneId(sid);
                setPaneTabs((prev) => clearBadgeOnFocus(prev, sid));
              }}
              onReorderPane={(draggedId, targetId, after) => {
                setPaneTabs((prev) => reorderPaneTabs(prev, draggedId, targetId, after));
              }}
              onClosePane={(sid) => {
                releasePaneChatInputRef(sid);
                const closingNewSessionTab = isNewSessionTab(sid);
                const remaining = paneTabs.filter((t) => t.sessionId !== sid);
                setPaneTabs((prev) => closePaneOp(prev, sid));
                if (closingNewSessionTab) {
                  // Closing the new-session tab itself: only when it was the
                  // last remaining tab does split view collapse (the
                  // previous last-pane behavior).
                  if (remaining.length === 0) {
                    setSplitPaneEnabled(false);
                    setFocusedPaneId(null);
                  } else if (focusedPaneId === sid) {
                    setFocusedPaneId(remaining[remaining.length - 1].sessionId);
                  }
                  return;
                }
                if (!hasSessionTab(remaining)) {
                  // Auto new-session page (pi#21): the last session pane
                  // closed, so keep split view enabled and open (or focus)
                  // the new-session tab instead of the classic revert.
                  if (remaining.some((t) => isNewSessionTab(t.sessionId))) {
                    setFocusedPaneId(NEW_SESSION_TAB_ID);
                    splitPaneLayoutRef.current?.scrollPaneIntoView(NEW_SESSION_TAB_ID);
                  } else {
                    void resolveNewSessionTabCwd().then((cwd) => {
                      if (cwd) {
                        handleNewSession(`pane-${Date.now()}`, cwd);
                      } else {
                        setSplitPaneEnabled(false);
                        setFocusedPaneId(null);
                      }
                    });
                  }
                  return;
                }
                if (focusedPaneId === sid) {
                  setFocusedPaneId(remaining[remaining.length - 1].sessionId);
                }
              }}
              renderPane={(sid, focused) => {
                // The sentinel tab renders the new-session composer ChatWindow;
                // it reads the same cwd/draft props as the classic composer,
                // so drafts survive pane switches and tab close/reopen.
                if (isNewSessionTab(sid)) {
                  return (
                    <ChatWindow
                      key={NEW_SESSION_TAB_ID}
                      session={null}
                      isActivePane={focused}
                      newSessionCwd={effectiveNewSessionCwd}
                      newSessionDraftKey={newSessionDraftKey}
                      sessionRunning={false}
                      chatInputRef={getPaneChatInputRef(NEW_SESSION_TAB_ID)}
                      onSessionCreated={handleSessionCreated}
                      onOpenSettings={openSettingsSection}
                    />
                  );
                }
                const paneSession = sid === selectedSession?.id ? selectedSession : sessionCatalog.find((s) => s.id === sid) ?? null;
                if (!paneSession) return null;
                return (
                  <ChatWindow
                    key={sid}
                    session={paneSession}
                    isActivePane={focused}
                    newSessionCwd={null}
                    newSessionDraftKey={null}
                    sessionRunning={runningSessionIds.has(sid)}
                    chatInputRef={getPaneChatInputRef(sid)}
                    // pi#23: only the focused pane reports usage/stats to the
                    // global topbar state. Unfocused panes pass undefined, so
                    // ChatWindow's cleanup (keyed on the callback) nulls the
                    // old value on blur and the newly focused pane's effect
                    // re-reports its current value on focus — last writer is
                    // always the focused pane, never an unfocused one.
                    onSessionStatsChange={focused ? handleSessionStatsChange : undefined}
                    onContextUsageChange={focused ? handleContextUsageChange : undefined}
                    // pi#28: every pane feeds its own background-task events;
                    // handleBackgroundTasksEvent filters by the session the
                    // panel currently follows. Terminal notifications stay
                    // session-wide, so tasks finishing in an unfocused pane
                    // still notify (pane focus only gates panel state).
                    onBackgroundTasksEvent={(event) => handleBackgroundTasksEvent(sid, event)}
                    // Split view is the default layout, so local-file markdown
                    // links must reach the same viewer as the classic window;
                    // without this they render target=_blank and open a bare
                    // popup. Authorize against THIS pane's
                    // session: pane focus never updates selectedSession, so
                    // handleOpenLinkedFile would tag a non-selected pane's
                    // link with another session's id (403 outside the roots).
                    onOpenFile={getPaneOpenFileHandler(sid)}
                    onOpenSettings={openSettingsSection}
                    onAgentEnd={(end) => {
                      // A stopped run did not finish anything: no badge, no chime.
                      if (end.aborted) return;
                      if (sid !== focusedPaneId) {
                        setPaneTabs((prev) => setCompletionBadge(prev, sid));
                        const now = Date.now();
                        if (coalesceCompletionSound(lastSoundAtRef.current, now)) {
                          lastSoundAtRef.current = now;
                          if (soundEnabledRef.current) playDoneSound();
                        }
                      }
                    }}
                  />
                );
              }}
            />
          ) : showChat ? (
            <ChatWindow
              key={sessionKey}
              session={selectedSession}
              searchTarget={searchTarget?.sessionId === selectedSession?.id ? searchTarget : null}
              onSearchTargetHandled={handleSearchTargetHandled}
              initialScrollPosition={selectedSession ? sessionScrollPositionsRef.current.get(selectedSession.id) ?? null : null}
              onScrollPositionChange={handleSessionScrollPositionChange}
              sessionRunning={Boolean(selectedSession && runningSessionIds.has(selectedSession.id))}
              newSessionCwd={effectiveNewSessionCwd}
              newSessionDraftKey={newSessionDraftKey}
              onAgentEnd={handleAgentEnd}
              onToolEnd={handleToolEnd}
              onAttentionNeeded={handleAttentionNeeded}
              onBackgroundTasksEvent={selectedSession ? (event) => handleBackgroundTasksEvent(selectedSession.id, event) : undefined}
              onSessionCreated={handleSessionCreated}
              onSessionForked={handleSessionForked}
              modelsRefreshKey={modelsRefreshKey}
              chatInputRef={chatInputRef}
              onBranchDataChange={handleBranchDataChange}
              onSystemPromptChange={handleSystemPromptChange}
              onSystemToolsChange={handleSystemToolsChange}
              onSystemInfoLoaderChange={handleSystemInfoLoaderChange}
              onDebugSnapshotReaderChange={handleDebugSnapshotReaderChange}
              onSessionStatsChange={handleSessionStatsChange}
              onSessionStatsPanelOpen={openSessionStatsPanel}
              onOpenSettings={openSettingsSection}
              onContextUsageChange={handleContextUsageChange}
              externalSessionChange={externalSessionChange}
              onOpenFile={handleOpenLinkedFile}
              onFilesUploaded={handleExplorerRefresh}
              onOpenSubagent={handleOpenSubagentTab}
              onAskInNewChat={handleAskInNewChat}
              quoteSelectionEnabled={quoteSelectionEnabled}
              initialPrompt={pendingQuotePrompt?.sessionId === selectedSession?.id ? pendingQuotePrompt?.text : undefined}
              onInitialPromptConsumed={() => setPendingQuotePrompt(null)}
              soundEnabled={soundEnabled}
              onSoundToggle={onSoundToggle}
              playDoneSound={playDoneSound}
              unlockAudio={unlockAudio}
              holdAudio={holdAudio}
            />
          ) : initialCwdStatus === "validating" ? (
            <div
              role="status"
              style={{ height: "100%", display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 8, padding: 24, color: "var(--text-muted)", textAlign: "center" }}
            >
               <div style={{ fontSize: 14, color: "var(--text)" }}>{translate("workspace.opening")}</div>
              <div style={{ maxWidth: "min(720px, 100%)", overflowWrap: "anywhere", fontFamily: "var(--font-mono)", fontSize: 12 }}>
                {initialNavigation.requestedCwd}
              </div>
            </div>
          ) : initialCwdStatus === "error" ? (
            <div
              role="alert"
              style={{ height: "100%", display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 8, padding: 24, color: "var(--text-muted)", textAlign: "center" }}
            >
               <div style={{ fontSize: 14, color: "#dc2626" }}>{translate("workspace.unable")}</div>
              <div style={{ maxWidth: "min(720px, 100%)", overflowWrap: "anywhere", fontFamily: "var(--font-mono)", fontSize: 12 }}>
                {initialNavigation.requestedCwd}
              </div>
              <div style={{ maxWidth: 720, fontSize: 12 }}>{initialCwdError}</div>
            </div>
          ) : showPlaceholder ? (
            activeCwd ? (
              <div style={{ height: "100%", display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text-muted)", fontSize: 15 }}>
                 {translate("workspace.selectSession")}
              </div>
            ) : (
              <div style={{ position: "absolute", top: 12, left: 12, display: "flex", alignItems: "flex-start", gap: 8, userSelect: "none", pointerEvents: "none" }}>
                <svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.7, flexShrink: 0 }}>
                  <line x1="20" y1="12" x2="4" y2="12" /><polyline points="10 6 4 12 10 18" />
                </svg>
                <div>
                   <div style={{ fontSize: 18, fontWeight: 600, color: "var(--text)", marginBottom: 8 }}>{translate("workspace.getStarted")}</div>
                  <div style={{ fontSize: 12, color: "var(--text-muted)", lineHeight: 1.8 }}>
                     <span style={{ color: "var(--text-dim)", marginRight: 6 }}>1.</span>{translate("workspace.selectProject")}<br />
                     <span style={{ color: "var(--text-dim)", marginRight: 6 }}>2.</span>{translate("workspace.addModels")}
                  </div>
                </div>
              </div>
            )
          ) : null}
          {bgPanelOpen && (
            isMobile ? (
              <>
                <div
                  onClick={() => setBgPanelOpen(false)}
                  style={{ position: "absolute", inset: 0, zIndex: 39, background: "rgba(0,0,0,0.35)" }}
                />
                <div
                  ref={bgSheetResizer.sheetRef}
                  role="dialog"
                  aria-label={translate("bgTasks.title")}
                  style={{
                    position: "absolute", left: 0, right: 0, bottom: 0,
                    height: `${bgSheetResizer.heightPct}%`,
                    zIndex: 40, background: "var(--bg-panel)", borderTop: "1px solid var(--border)",
                    boxShadow: "0 -12px 40px rgba(0,0,0,0.22)",
                    paddingBottom: "var(--safe-area-bottom, 0px)",
                  }}
                >
                  {/* pi#120: top-edge drag handle (a DOM child of the sheet,
                      above the backdrop — dragging resizes, never dismisses). */}
                  <div
                    {...bgSheetResizer.separatorProps}
                    className={`pf-sheet-resize-handle${bgSheetResizer.isResizing ? " is-resizing" : ""}`}
                    data-resize-handle="bg-tasks-sheet"
                  />
                  <BackgroundTasksPanel
                    sessionId={activeBgSessionId}
                    state={bgTasksState}
                    logs={bgTaskLogs}
                    onRefresh={refreshBgTasks}
                    onFetchLogs={fetchBgTaskLogs}
                    onKill={killBgTask}
                    onClose={() => setBgPanelOpen(false)}
                    selectedTaskId={bgSelectedTaskId}
                    onSelectTask={setBgSelectedTaskId}
                  />
                </div>
              </>
            ) : (
              <div
                role="complementary"
                aria-label={translate("bgTasks.title")}
                style={{
                  position: "absolute", top: 0, right: 0, bottom: 0, width: 340,
                  zIndex: 25, background: "var(--bg-panel)", borderLeft: "1px solid var(--border)",
                  boxShadow: "-8px 0 24px rgba(0,0,0,0.10)",
                }}
              >
                <BackgroundTasksPanel
                  sessionId={activeBgSessionId}
                  state={bgTasksState}
                  logs={bgTaskLogs}
                  onRefresh={refreshBgTasks}
                  onFetchLogs={fetchBgTaskLogs}
                  onKill={killBgTask}
                  onClose={() => setBgPanelOpen(false)}
                  selectedTaskId={bgSelectedTaskId}
                  onSelectTask={setBgSelectedTaskId}
                />
              </div>
            )
          )}
          {pfPanelOpen && (
            isMobile ? (
              <>
                <div
                  onClick={() => setPfPanelOpen(false)}
                  style={{ position: "absolute", inset: 0, zIndex: 39, background: "rgba(0,0,0,0.35)" }}
                />
                <div
                  ref={pfSheetResizer.sheetRef}
                  role="dialog"
                  aria-label={translate("polyforge.title")}
                  style={{
                    position: "absolute", left: 0, right: 0, bottom: 0,
                    height: `${pfSheetResizer.heightPct}%`,
                    zIndex: 40, background: "var(--bg-panel)", borderTop: "1px solid var(--border)",
                    boxShadow: "0 -12px 40px rgba(0,0,0,0.22)",
                    paddingBottom: "var(--safe-area-bottom, 0px)",
                  }}
                >
                  {/* pi#120: top-edge drag handle (a DOM child of the sheet,
                      above the backdrop — dragging resizes, never dismisses). */}
                  <div
                    {...pfSheetResizer.separatorProps}
                    className={`pf-sheet-resize-handle${pfSheetResizer.isResizing ? " is-resizing" : ""}`}
                    data-resize-handle="polyforge-sheet"
                  />
                  <PolyforgePanel
                    opsStatus={pfOpsStatus}
                    opsError={pfOpsError}
                    cards={pfCards}
                    cardsError={pfCardsError}
                    watchStatus={pfWatchStatus}
                    selectedWi={pfSelectedWi}
                    selectedProfile={pfSelectedProfile}
                    onSelect={selectPolyforgeJob}
                    onClose={() => setPfPanelOpen(false)}
                    onRefresh={refreshPolyforgeStatus}
                    activeTab={pfActiveTab}
                    onActiveTab={setPfActiveTab}
                    workTab={pfWorkTab}
                    onWorkTab={setPfWorkTab}
                    workItemsRunning={pfWorkItemsRunning}
                    onWorkItemsRunningChange={setPfWorkItemsRunning}
                    memoryCwd={projectTrustCwd}
                    messagesSurface={pfMessagesSurface}
                    decisionsSurface={pfDecisionsSurface}
                    showPin={false}
                    pinned={pfDockPinned}
                    onTogglePin={togglePfDockPin}
                  />
                </div>
              </>
            ) : (
              <div
                ref={pfDockResizer.panelRef}
                role="complementary"
                data-pf-dock
                id="polyforge-dock"
                aria-label={translate("polyforge.title")}
                style={{
                  position: "absolute", top: 0, right: 0, bottom: 0,
                  width: "var(--pf-dock-width)",
                  zIndex: 25, background: "var(--bg-panel)", borderLeft: "1px solid var(--border)",
                  boxShadow: "-8px 0 24px rgba(0,0,0,0.10)",
                  "--pf-dock-width": `${pfDockResizer.width}px`,
                } as React.CSSProperties}
              >
                {/* pi#117: the resize handle straddles the dock's left edge
                    but stays a DOM child of it, so the pi#113 outside-
                    pointerdown dismissal counts it as part of the dock —
                    dragging never closes the dock. */}
                <div
                  {...pfDockResizer.separatorProps}
                  aria-controls="polyforge-dock"
                  className={`pf-dock-resize-handle${pfDockResizer.isResizing ? " is-resizing" : ""}`}
                  data-resize-handle="polyforge-dock"
                  title={`${translate("polyforge.resizeDock")}: ${translate("layout.resizeHint")}`}
                />
                <PolyforgePanel
                  opsStatus={pfOpsStatus}
                  opsError={pfOpsError}
                  cards={pfCards}
                  cardsError={pfCardsError}
                  watchStatus={pfWatchStatus}
                  selectedWi={pfSelectedWi}
                  selectedProfile={pfSelectedProfile}
                  onSelect={selectPolyforgeJob}
                  onClose={() => setPfPanelOpen(false)}
                  onRefresh={refreshPolyforgeStatus}
                  activeTab={pfActiveTab}
                  onActiveTab={setPfActiveTab}
                  workTab={pfWorkTab}
                  onWorkTab={setPfWorkTab}
                  workItemsRunning={pfWorkItemsRunning}
                  onWorkItemsRunningChange={setPfWorkItemsRunning}
                  memoryCwd={projectTrustCwd}
                  messagesSurface={pfMessagesSurface}
                  decisionsSurface={pfDecisionsSurface}
                  showPin
                  pinned={pfDockPinned}
                  onTogglePin={togglePfDockPin}
                />
              </div>
            )
          )}
        </div>
      </div>

      <div
        aria-hidden="true"
        className={`right-panel-overlay-backdrop${rightPanelOpen ? " is-open" : ""}`}
        onClick={() => setRightPanelOpen(false)}
      />
      {rightPanelOpen && (
        <div
          {...rightPanelResizer.separatorProps}
          inert={rightPanelFullWidth}
          aria-controls="file-panel"
          className={`panel-resize-handle right-panel-resize-handle${rightPanelResizer.isResizing ? " is-resizing" : ""}`}
          data-resize-handle="right-panel"
          title={`${translate("layout.resizeFilePanel")}: ${translate("layout.resizeHint")}`}
        />
      )}

      {/* Right panel: file viewer — always mounted, width animated via CSS */}
      <div
        ref={rightPanelResizer.panelRef}
        id="file-panel"
        className={`right-panel-container${rightPanelOpen ? " right-panel-open" : " right-panel-closed"}${rightPanelFullWidth ? " right-panel-full-width" : ""}${rightPanelResizer.isResizing ? " right-panel-resizing" : ""}`}
        style={{
          "--right-panel-width": `${rightPanelResizer.width}px`,
          display: "flex",
          flexDirection: "column",
          borderLeft: "1px solid var(--border)",
          background: "var(--bg)",
        } as React.CSSProperties}
      >
        {/* Right panel tab bar */}
        <div style={{
          display: "flex",
          alignItems: "center",
          flexShrink: 0,
          height: "calc(var(--top-bar-height) + env(safe-area-inset-top))",
          paddingTop: "env(safe-area-inset-top)",
          background: "var(--bg-panel)",
          borderBottom: "1px solid var(--border)",
        }}>
          <div style={{ flex: 1, overflow: "hidden" }}>
            <TabBar
              tabs={panelTabs}
              activeTabId={activeFileTabId ?? ""}
              onSelectTab={setActiveFileTabId}
              onCloseTab={handleCloseFileTab}
              onReorderTab={handleReorderPanelTab}
            />
          </div>
          <button
            type="button"
            className="file-panel-expand-button"
            onClick={handleRightPanelExpandToggle}
            aria-controls="file-panel"
            aria-pressed={rightPanelFullWidth}
            title={translate(rightPanelFullWidth ? "files.restorePanelWidth" : "files.expandPanel")}
            aria-label={translate(rightPanelFullWidth ? "files.restorePanelWidth" : "files.expandPanel")}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d={rightPanelFullWidth
                ? "M9 3v6H3m12-6v6h6M9 21v-6H3m12 6v-6h6M3 3l6 6m12-6-6 6M3 21l6-6m12 6-6-6"
                : "M8 3H3v5m13-5h5v5M3 16v5h5m13-5v5h-5M3 3l6 6m12-6-6 6M3 21l6-6m12 6-6-6"} />
            </svg>
          </button>
          <button
            type="button"
            onClick={() => setRightPanelOpen(false)}
            aria-controls="file-panel"
            aria-expanded={rightPanelOpen}
            title={translate("files.hidePanel")}
            aria-label={translate("files.hidePanel")}
            style={{
              display: "flex", alignItems: "center", justifyContent: "center",
              width: TOP_BAR_ICON_BUTTON_SIZE, height: TOP_BAR_ICON_BUTTON_SIZE, padding: 0,
              background: "var(--bg-selected)", border: "none", borderLeft: "1px solid var(--border)",
              color: "var(--text)", cursor: "pointer", flexShrink: 0, transition: "color 0.12s",
            }}
            onMouseEnter={(event) => { event.currentTarget.style.color = "var(--accent)"; }}
            onMouseLeave={(event) => { event.currentTarget.style.color = "var(--text)"; }}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <rect x="3" y="3" width="18" height="18" rx="2" /><line x1="15" y1="3" x2="15" y2="21" />
            </svg>
          </button>
        </div>

        {/* Only the active viewer is mounted. Lightweight per-tab state is restored on activation. */}
        <div style={{ flex: 1, minHeight: 0, overflow: "hidden", paddingBottom: "var(--safe-area-bottom, 0px)", display: "flex", flexDirection: "column" }}>
          {historyExportSessionId ? (
            <>
              <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "4px 10px", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
                <span style={{ flex: 1, fontSize: 11, color: "var(--text-dim)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {translate("history.snapshot")}
                </span>
                <a
                  href={`/api/sessions/${encodeURIComponent(historyExportSessionId)}/export?inline=1`}
                  target="_blank"
                  rel="noopener noreferrer"
                  title={translate("history.openInTab")}
                  style={{ fontSize: 11, color: "var(--text-muted)", textDecoration: "none", flexShrink: 0, display: "flex", alignItems: "center", gap: 4 }}
                >
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /><polyline points="15 3 21 3 21 9" /><line x1="10" y1="14" x2="21" y2="3" />
                  </svg>
                  {translate("history.openInTab")}
                </a>
              </div>
              <iframe
                src={`/api/sessions/${encodeURIComponent(historyExportSessionId)}/export?inline=1`}
                title={translate("history.full")}
                style={{ flex: 1, width: "100%", minHeight: 0, border: "none", background: "var(--bg)" }}
              />
            </>
          ) : activeFileTab?.filePath ? (
            <FileViewer
              key={`${fileWorkspaceGeneration}:${activeFileTab.id}:${activeFileTab.viewerRevision ?? 0}`}
              filePath={activeFileTab.filePath}
              cwd={activeCwd ?? undefined}
              sourceSessionId={activeFileTab.sourceSessionId}
              gitRefreshKey={explorerRefreshKey}
              initialDisplayMode={activeFileTab.initialDisplayMode}
              initialPage={activeFileTab.page}
              initialState={activeFileTab.viewerState}
              watchEnabled={rightPanelOpen}
              onStateChange={(viewerState) => handleFileViewerStateChange(
                fileWorkspaceGeneration,
                activeFileTab.id,
                activeFileTab.viewerRevision ?? 0,
                viewerState,
              )}
              onMentionLines={rightPanelOpen ? handleFileLineMention : undefined}
              onAtMention={handleAtMention}
              onOpenFile={(filePath, page) => handleOpenFile(
                filePath,
                getFileName(filePath),
                { sourceSessionId: activeFileTab.sourceSessionId, page },
              )}
            />
          ) : !terminalTabs.some((tab) => tab.id === activeFileTabId) && !activeAgentTab ? (
            <div style={{ height: "100%", display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text-dim)", fontSize: 12 }}>
               {translate("files.noneOpen")}
            </div>
          ) : null}
          {terminalTabs.map((tab) => (
            <div key={tab.id} hidden={tab.id !== activeFileTabId} style={{ width: "100%", height: "100%" }}>
              <TerminalPanel
                tab={tab}
                active={rightPanelOpen && tab.id === activeFileTabId}
                onRestart={() => setTerminalTabs((tabs) => tabs.map((item) => item.id === tab.id ? { ...item, closing: "restart" } : item))}
                onClosed={() => handleTerminalClosed(tab)}
                onCloseError={() => setTerminalTabs((tabs) => tabs.map((item) => item.id === tab.id ? { ...item, closing: undefined } : item))}
              />
            </div>
          ))}
          {activeAgentTab && (
            <SessionChatPanel
              key={activeAgentTab.sessionId}
              sessionId={activeAgentTab.sessionId}
              running={runningSessionIds.has(activeAgentTab.sessionId)}
              modelsRefreshKey={modelsRefreshKey}
              quoteSelectionEnabled={quoteSelectionEnabled}
              onOpenFile={handleOpenLinkedFile}
              onOpenSubagent={handleOpenSubagentTab}
              onSessionForked={(newSessionId) => {
                if (isMobile) setRightPanelOpen(false);
                handleSessionForked(newSessionId);
              }}
              onOpenSettings={openSettingsSection}
              onToolEnd={handleToolEnd}
              onFilesUploaded={handleExplorerRefresh}
              soundEnabled={soundEnabled}
              onSoundToggle={onSoundToggle}
              playDoneSound={playDoneSound}
              unlockAudio={unlockAudio}
              holdAudio={holdAudio}
            />
          )}
        </div>
      </div>
    </div>
    {settingsSection && (
      <SettingsPanel
        cwd={projectTrustCwd}
        sessionId={selectedSession?.id ?? null}
        initialSection={settingsSection}
        openAtHome={settingsOpenAtHome}
        quoteSelectionEnabled={quoteSelectionEnabled}
        onQuoteSelectionChange={handleQuoteSelectionChange}
        onClose={() => {
          setSettingsSection(null);
          setModelsRefreshKey((key) => key + 1);
        }}
        onSessionReloaded={() => setSessionKey((key) => key + 1)}
        projectTrust={projectTrust}
        onOpenTrustDialog={openProjectTrustDialog}
        onProjectTrustChanged={handleProjectTrustChanged}
      />
    )}
    {projectTrustDialogOpen && projectTrustCwd && (
      <ProjectTrustDialog
        cwd={projectTrustCwd}
        busy={projectTrustBusy}
        error={projectTrustError}
        onCancel={() => {
          if (!projectTrustBusy) setProjectTrustDialogOpen(false);
        }}
        onConfirm={() => void handleTrustProject()}
        onStatus={setProjectTrust}
      />
    )}
    </>
  );
}
