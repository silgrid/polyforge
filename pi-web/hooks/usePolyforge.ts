"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { PolyforgeOpsStatus, PolyforgeWatchStatus } from "@/lib/polyforge-status";
import {
  ackDecision,
  conversationForContact,
  mergeNewestPage,
  mergeOlderPage,
  parsePolyforgeAnalysis,
  type PolyforgeAnalysis,
  type PolyforgeContact,
  type PolyforgeConversation,
  type PolyforgeMessage,
  type PolyforgeProjectGroup,
} from "@/lib/polyforge-messages-pure";
import {
  cardCursors,
  nextCardSnapshot,
  type PolyforgeCard,
  type PolyforgeCardsResponse,
} from "@/lib/polyforge-cards-pure";
import { needsInputJobs } from "@/lib/polyforge-conversation-pure";
import {
  attentionItems,
  attentionTarget,
  nextAttentionItem,
  sanitizePolyforgeDecisionFeed,
  singleFlight,
  type PolyforgeAttentionItem,
  type PolyforgeDecisionFeed,
} from "@/lib/polyforge-decisions-pure";
import { usePolyforgePanelPreferences } from "./usePolyforgePanelPreferences";

/**
 * Panel-facing client state for the Polyforge dock panel (pi#108) and its
 * Messages tab.
 *
 * - A status fetch at mount decides presence (`pfPresent`). While pf is
 *   absent the status route is re-probed every 30s and on window focus,
 * so a pf that becomes available turns present without a
 *   reload; besides those status reads a pf-less install sees exactly one
 *   messages read at mount (all 2xx, no pf spawn; the users read runs only
 *   while the open dock shows Messages), so unrelated e2e flows log no
 *   console errors.
 * - With pf present: the status route is polled every 3s while the panel is
 *   open and every 30s while closed (badge).
 * - While open with a selection: the watch route is polled every 3s. A null
 *   status (daemon blip, job gone) keeps the previous snapshot — the
 *   selection and its detail survive the job leaving the job list.
 * - Messages reuse the SAME 3s/30s dock cadence (no new timer): the
 *   conversation list rides the status interval, and while a conversation is
 *   open its newer messages are fetched on the same 3s tick (one `--after`
 *   read, never a second backward page). Auto-acknowledgment goes through
 *   the one pure `ackDecision` helper. The configured contacts
 * and the project groups ride the same
 *   list read — no timer of their own; the groups only on reads made while
 *   the open dock shows Messages (`?groups=0` otherwise, no `projects get`).
 * - Project membership: one users read (`/api/polyforge/users`,
 *   the admin probe and the picker's humans) each time the open dock shows
 *   Messages — never at page load or on another tab (no new request or pf verb
 *   outside Messages); not gated on the presence probe, since the route
 *   degrades and spawns nothing when pf is absent. No timer.
 *   `admin` is null until it answers. A membership change POSTs once and, on success, awaits
 *   a fresh list read before resolving (no optimistic change); a refusal
 *   throws pf's text.
 * - A silent contact (no conversation yet) is its own selection: it opens
 *   an empty pane, loads no history, creates nothing and acks nothing; its
 *   first send opens the conversation the send's answer names.
 * - The Jobs-tab card stream is the one exception with its
 *   own cadence: one `GET /api/polyforge/cards` per tick for every card,
 *   only while the dock is open on Work › Jobs, at the browser-stored poll
 *   interval (Settings › Polyforge), with an immediate read whenever that
 *   surface becomes visible. A failed or degraded tick keeps the last cards.
 * - The decision feed rides every status read that finds pf
 *   present (the 3s / 30s cadence and the manual refresh; no timer of its
 *   own), so the Polyforge button's attention state holds with the dock closed. A failed
 *   read keeps the last feed and says why; pf absent clears it.
 */

const OPEN_STATUS_POLL_MS = 3000;
const CLOSED_STATUS_POLL_MS = 30000;
const OPEN_WATCH_POLL_MS = 3000;
/** The cards request's client bound: longer than the
 *  server's 10 s `pf ops status` timeout, so a normal answer is never cut;
 *  a request that never settles is aborted and frees the in-flight slot. */
const CARD_REQUEST_TIMEOUT_MS = 15000;

type StatusResponse = { pfPresent?: boolean; status?: PolyforgeOpsStatus | null; error?: string };
type DecisionsResponse = { pfPresent?: boolean; feed?: unknown; error?: string };
type DismissResponse = { ok?: boolean; dismissed?: number; error?: string };

/** a task the attention walk opens in Work › Tasks; `seq`
 *  makes a repeated open a new request. */
export type PolyforgeTaskRequest = { id: string; seq: number };
type WatchResponse = { status?: PolyforgeWatchStatus | null; error?: string };
type MessagesResponse = {
  pfPresent?: boolean;
  pfVersion?: string | null;
  capable?: boolean;
  reason?: string;
  conversations?: PolyforgeConversation[];
  contacts?: PolyforgeContact[];
  groups?: PolyforgeProjectGroup[];
  error?: string;
  /** the Cell the list is read from. */
  cell?: string;
};
type PageResponse = {
  capable?: boolean;
  messages?: PolyforgeMessage[];
  last_seq?: number;
  has_older?: boolean;
  error?: string;
};
type UsersResponse = { admin?: boolean; users?: PolyforgeContact[]; error?: string };
type MembershipResponse = { ok?: boolean; error?: string };
type SendResponse = { ok?: boolean; message?: PolyforgeMessage; error?: string };
type AnalyzeResponse = { ok?: boolean; analysis?: unknown; error?: string; upgrade?: boolean };

/** An analysis refusal: pf's text, and whether it is the
 *  below-floor refusal (the panel then shows its own upgrade text). */
export class PolyforgeAnalyzeError extends Error {
  readonly upgrade: boolean;

  constructor(message: string, upgrade = false) {
    super(message);
    this.name = "PolyforgeAnalyzeError";
    this.upgrade = upgrade;
  }
}

/** Keep the last snapshot when a watch response degrades to null (pure). */
export function nextWatchSnapshot(
  previous: PolyforgeWatchStatus | null,
  body: WatchResponse | null,
): PolyforgeWatchStatus | null {
  return body?.status ?? previous;
}

/** The dock's top-level tabs, in DOM order
 *  Work | Memory | Messages. Work hosts the former Jobs and Tasks tabs as its two
 *  sub-tabs (`PolyforgeWorkTab`). The Tasks and Memory surfaces own their
 *  reads (gesture-driven, no timer); only Jobs and Messages ride this hook's
 *  3s/30s cadence. */
export type PolyforgeTab = "memory" | "messages" | "work";

/** Work's sub-tabs (adds 工单列表, `items`; adds
 *  待集成, `integrate`, which reads on its own gestures). Page memory only:
 *  kept across a dock close/reopen (this hook outlives the dock), reset by a
 *  reload, never written to storage. */
export type PolyforgeWorkTab = "jobs" | "tasks" | "items" | "integrate";

export interface MembershipChangeInput {
  action: "add" | "remove";
  project: string;
  userId: string;
}

export interface SendMessageInput {
  to: string;
  text: string;
  aboutWi?: string;
  replyTo?: string;
}

export function usePolyforge(panelOpen: boolean) {
  const [pfPresent, setPfPresent] = useState(false);
  const [probed, setProbed] = useState(false);
  const [opsStatus, setOpsStatus] = useState<PolyforgeOpsStatus | null>(null);
  const [opsError, setOpsError] = useState<string | null>(null);
  // False from the moment the panel opens until this opening's own status
  // read settles: auto-ack must judge the CURRENT blocked state, never the
  // previous opening's stale snapshot.
  const [statusReady, setStatusReady] = useState(false);
  const [selectedWi, setSelectedWi] = useState<string | null>(null);
  // the selected card's Cell (its WorkRef.profile) — equal
  // work-item literals of two Cells are two jobs; undefined = not known
  // from the selection (an anchor), then the job list's first match.
  const [selectedCell, setSelectedCell] = useState<string | undefined>(undefined);
  const [watchStatus, setWatchStatus] = useState<PolyforgeWatchStatus | null>(null);

  // Messages state.
  const [activeTab, setActiveTabState] = useState<PolyforgeTab>("work");
  const [workTab, setWorkTab] = useState<PolyforgeWorkTab>("jobs");
  // the 工单列表 badge — the running count of the last list
  // read (null until the first one). Page memory only: the items surface
  // reports it, the badge keeps it after that surface unmounts; no read here.
  const [workItemsRunning, setWorkItemsRunning] = useState<number | null>(null);
  const [messagesCapable, setMessagesCapable] = useState(true);
  const [messagesReason, setMessagesReason] = useState<string | null>(null);
  const [messagesCell, setMessagesCell] = useState<string | null>(null);
  const [conversations, setConversations] = useState<PolyforgeConversation[]>([]);
  const [contacts, setContacts] = useState<PolyforgeContact[]>([]);
  const [groups, setGroups] = useState<PolyforgeProjectGroup[]>([]);
  // the admin probe (null until the users read answers) and
  // the picker's human users (id + display name only).
  const [membershipAdmin, setMembershipAdmin] = useState<boolean | null>(null);
  const [users, setUsers] = useState<PolyforgeContact[]>([]);
  const [messagesError, setMessagesError] = useState<string | null>(null);
  const [selectedConversation, setSelectedConversation] = useState<string | null>(null);
  // The open silent contact, held as the clicked row's snapshot so a poll
  // that drops or renames it never blanks the open pane's name or address.
  // Mutually exclusive with selectedConversation.
  const [selectedContact, setSelectedContact] = useState<PolyforgeContact | null>(null);
  const [messages, setMessages] = useState<PolyforgeMessage[]>([]);
  // The conversation whose newest page has settled. The panel positions the
  // history at the bottom on that first page even while a job is blocked;
  // later arrivals are the only scrolled messages the blocked state suppresses.
  const [initialPageLoaded, setInitialPageLoaded] = useState<string | null>(null);
  const [hasOlder, setHasOlder] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  // the structured analyses by message id — page memory only.
  const [analyses, setAnalyses] = useState<Record<string, PolyforgeAnalysis>>({});

  // Jobs-tab card stream. cardsRef mirrors the rendered
  // cards so the next tick reads the newest cursors without re-running the
  // poll effect; one request at a time (a tick that finds one in flight is
  // skipped), so cursors advance in order.
  const [cards, setCards] = useState<PolyforgeCard[] | null>(null);
  const [cardsError, setCardsError] = useState<string | null>(null);
  const cardsRef = useRef<PolyforgeCard[] | null>(null);
  // the decision feed (null until its first good read).
  const [decisionsFeed, setDecisionsFeed] = useState<PolyforgeDecisionFeed | null>(null);
  const [taskRequest, setTaskRequest] = useState<PolyforgeTaskRequest | null>(null);
  const taskRequestSeq = useRef(0);
  // The Tasks surface took the request: drop it, so a later mount of the
  // dock does not reopen the task (PR #860 minor 1).
  const clearTaskRequest = useCallback(() => setTaskRequest(null), []);
  const lastAttentionKey = useRef<string | null>(null);
  const cardsSeq = useRef(0);
  const cardsInFlight = useRef(false);
  const { pollIntervalSec } = usePolyforgePanelPreferences();

  const statusSeq = useRef(0);
  const watchSeq = useRef(0);
  const messagesSeq = useRef(0);
  // Whether the open dock shows Messages, for the list read's callers (the
  // timer included): only such reads carry the project groups.
  const messagesVisibleRef = useRef(false);
  const usersSeq = useRef(0);
  const usersLandedSeq = useRef(0);
  // The seq of the newest messages response that has LANDED (see
  // refreshMessages: acceptance is by landing order, not start order).
  const messagesLandedSeq = useRef(0);
  // Mirrors selectedConversation for the page writes' identity guard (w93):
  // a page write applies only to the conversation it belongs to while that
  // conversation is the open one — a response from a previous selection can
  // never land under the current one, and switching conversations no longer
  // invalidates the new selection's own in-flight first page (the old
  // page-generation counter did both, and the second half was a defect the
  // two-column layout exposed: opening Y while X is open blanked Y's page).
  const selectedConversationRef = useRef<string | null>(null);
  const messagesRef = useRef<PolyforgeMessage[]>([]);
  const lastAcked = useRef<Record<string, number>>({});

  // messagesRef mirrors the rendered page for the newest-seq reads (the
  // scroll-up and forward-refresh cursors). Every writer (open, scroll-up,
  // forward refresh) syncs here, one place.
  useEffect(() => {
    messagesRef.current = messages;
  }, [messages]);

  // the decision feed. Landing order like the status read: a
  // stale response never overwrites a newer one; a failed read keeps the
  // last feed (the button's attention state does not blink) and records why.
  // Single flight (PR #860 minor 3): one decision read at a time; a call
  // during a read joins it and asks for one more read after it (a dismiss
  // still reads its own effect back). Each read is bounded like the cards
  // read, so a hung request frees the slot.
  const readDecisions = useCallback(async () => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), CARD_REQUEST_TIMEOUT_MS);
    try {
      const response = await fetch("/api/polyforge/decisions", { signal: controller.signal });
      const body = await response.json() as DecisionsResponse;
      if (body.pfPresent === false) {
        setDecisionsFeed(null);
        return;
      }
      const feed = sanitizePolyforgeDecisionFeed(body.feed);
      if (feed) setDecisionsFeed(feed);
    } catch {
      // a failed read keeps the last feed
    } finally {
      clearTimeout(timer);
    }
  }, []);
  const refreshDecisions = useMemo(() => singleFlight(readDecisions), [readDecisions]);

  // Returns the settle promise so a manual refresh caller (the panel's
  // refresh button, pi#113) can show in-flight state; the interval and mount
  // callers ignore it. Background polls and the manual click share this one
  // function — in-flight indication lives with the click, not the fetch.
  const refreshStatus = useCallback(() => {
    const seq = ++statusSeq.current;
    return fetch("/api/polyforge/status")
      .then(async (response) => {
        const body = await response.json() as StatusResponse;
        if (seq !== statusSeq.current) return;
        setPfPresent(Boolean(body.pfPresent));
        setProbed(true);
        setOpsStatus(body.status ?? null);
        setOpsError(body.status ? null : (body.error ?? null));
        setStatusReady(true);
        // the decision feed rides every status read (the
        // 3 s / 30 s cadence and the manual refresh), no timer of its own.
        if (body.pfPresent) void refreshDecisions();
      })
      .catch(() => {
        if (seq !== statusSeq.current) return;
        setProbed(true);
        setOpsStatus(null);
        setOpsError("polyforge is unreachable");
        setStatusReady(true);
      });
  }, [refreshDecisions]);

  const refreshCards = useCallback(() => {
    if (cardsInFlight.current) return;
    cardsInFlight.current = true;
    const seq = cardsSeq.current;
    const cursors = cardCursors(cardsRef.current);
    const query = Object.keys(cursors).length > 0 ? `?cursors=${encodeURIComponent(JSON.stringify(cursors))}` : "";
    // AbortController + setTimeout: the timeout-signal helper is absent on
    // the Safari 16.2 target.
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), CARD_REQUEST_TIMEOUT_MS);
    fetch(`/api/polyforge/cards${query}`, { signal: controller.signal })
      .then((response) => response.json() as Promise<PolyforgeCardsResponse>)
      .catch(() => null)
      .finally(() => clearTimeout(timer))
      .then((body) => {
        if (seq !== cardsSeq.current) return;
        cardsInFlight.current = false;
        // A failed, degraded or timed-out tick keeps the last cards
        // (nextCardSnapshot) and frees the slot for the next tick.
        const next = nextCardSnapshot(cardsRef.current, body);
        cardsRef.current = next;
        setCards(next);
        setCardsError(body && Array.isArray(body.cards) ? null : (body?.error ?? "polyforge is unreachable"));
      });
  }, []);

  const refreshWatch = useCallback((wi: string, profile?: string) => {
    const seq = ++watchSeq.current;
    // the row's Cell is addressed explicitly (never the cwd)
    const cell = profile ? `&profile=${encodeURIComponent(profile)}` : "";
    fetch(`/api/polyforge/watch?wi=${encodeURIComponent(wi)}${cell}`)
      .then(async (response) => {
        const body = await response.json() as WatchResponse;
        if (seq !== watchSeq.current) return;
        // Null keeps the previous snapshot (selection persistence, AC6).
        setWatchStatus((previous) => nextWatchSnapshot(previous, body));
      })
      .catch(() => {
        // A network blip keeps the previous snapshot too.
      });
  }, []);

  // The conversation list rides the same dock cadence: one read at mount and
  // on every open/closed interval tick. Below the floor the route answers a
  // capability field and spawns no message verb.
  //
  // Every call starts its OWN read (w93 review): no in-flight slot is
  // shared, so sendMessage's awaited refresh always resolves with a read
  // that started after the send landed (read-your-write — a new
  // conversation's row and header name render immediately, not at the next
  // poll). The mount/dock-open/tab burst simply fires concurrent reads.
  //
  // Acceptance is by LANDING order, not start order: a start-order guard
  // would discard the first response of that burst — blanking the list until
  // the last one lands — even though it carries fresh data (w93: the list
  // and the picker must render as soon as the first response arrives). A
  // response whose seq is below one that already landed is still discarded,
  // so a late stale poll can never overwrite a newer read.
  const refreshMessages = useCallback(() => {
    const seq = ++messagesSeq.current;
    // Off the Messages tab the list read skips the project groups: no other
    // tab spawns `pf projects get`; the landed groups stay until the next
    // read made while Messages is shown.
    const withGroups = messagesVisibleRef.current;
    return fetch(withGroups ? "/api/polyforge/messages" : "/api/polyforge/messages?groups=0")
      .then(async (response) => {
        const body = await response.json() as MessagesResponse;
        if (seq <= messagesLandedSeq.current) return null;
        messagesLandedSeq.current = seq;
        setMessagesCapable(body.capable !== false);
        setMessagesReason(body.reason ?? null);
        setMessagesCell(typeof body.cell === "string" ? body.cell : null);
        setConversations(Array.isArray(body.conversations) ? body.conversations : []);
        setContacts(Array.isArray(body.contacts) ? body.contacts : []);
        if (withGroups) setGroups(Array.isArray(body.groups) ? body.groups : []);
        // `error` is the non-floor read failure; `reason` belongs to the
        // upgrade state only.
        setMessagesError(body.capable === false ? (body.reason ?? null) : (body.error ?? null));
        return body;
      })
      .catch(() => {
        if (seq <= messagesLandedSeq.current) return null;
        messagesLandedSeq.current = seq;
        setMessagesError("messages are unreachable");
        return null;
      });
  }, []);

  // The admin probe and the picker's users. Accepted by
  // landing order like the list read: a newer read never blanks the landed
  // answer while in flight, and a late older one never overwrites it. A
  // failed read is the read-only answer.
  const refreshUsers = useCallback(() => {
    const seq = ++usersSeq.current;
    return fetch("/api/polyforge/users")
      .then(async (response) => {
        const body = await response.json() as UsersResponse;
        if (seq <= usersLandedSeq.current) return;
        usersLandedSeq.current = seq;
        setMembershipAdmin(body.admin === true);
        setUsers(body.admin === true && Array.isArray(body.users) ? body.users : []);
      })
      .catch(() => {
        if (seq <= usersLandedSeq.current) return;
        usersLandedSeq.current = seq;
        setMembershipAdmin(false);
        setUsers([]);
      });
  }, []);

  // One fetch at mount decides presence; while pf is absent only the status
  // cadence below re-probes it.
  // Effect cleanups ADVANCE the guard counters: resetting to an observed
  // value + 1 rewinds them (interval polls have long moved past it), so a
  // late response from the previous selection could be accepted under the
  // current one for up to one poll.
  useEffect(() => {
    refreshStatus();
    // This ref is a fetch-generation counter, not a React-rendered node, and
    // copying it into the effect would rewind past interval-poll increments
    // (the exact stale-response defect NB-1 names).
    // eslint-disable-next-line react-hooks/exhaustive-deps
    return () => { ++statusSeq.current; };
  }, [refreshStatus]);

  // A new opening starts from an unready status: the stale snapshot cannot
  // suppress (or allow) the first auto-ack decision.
  useEffect(() => {
    if (panelOpen) setStatusReady(false);
  }, [panelOpen]);

  // Card stream: only while the dock is open on Work › Jobs — an immediate
  // read, then one request per tick at the configured interval.
  useEffect(() => {
    if (!probed || !pfPresent || !panelOpen || activeTab !== "work" || workTab !== "jobs") return;
    refreshCards();
    const interval = setInterval(refreshCards, pollIntervalSec * 1000);
    return () => {
      // Same counter rationale as the status effect: advance, never rewind;
      // the abandoned read can no longer land or hold the in-flight slot.
      // eslint-disable-next-line react-hooks/exhaustive-deps
      ++cardsSeq.current;
      cardsInFlight.current = false;
      clearInterval(interval);
    };
  }, [probed, pfPresent, panelOpen, activeTab, workTab, pollIntervalSec, refreshCards]);

  // Cadence: 3s open, 30s closed while pf is present. While pf is absent
  // the same timer re-probes presence every 30s and window
  // focus re-probes at once — one status read each, nothing else — so a pf
  // that becomes available shows the Polyforge button without a reload; on
  // the first present answer this effect re-runs as the usual cadence.
  useEffect(() => {
    if (!probed) return;
    // Opening the panel reads the current state immediately: a blocked job
    // must suppress auto-ack from the first poll, not one cadence later.
    if (pfPresent && panelOpen) refreshStatus();
    const interval = setInterval(refreshStatus, pfPresent && panelOpen ? OPEN_STATUS_POLL_MS : CLOSED_STATUS_POLL_MS);
    if (!pfPresent) window.addEventListener("focus", refreshStatus);
    return () => {
      clearInterval(interval);
      window.removeEventListener("focus", refreshStatus);
    };
  }, [probed, pfPresent, panelOpen, refreshStatus]);

  // Messages use the same interval (3s open / 30s closed), never a new timer.
  // The immediate read is NOT gated on the presence probe: the route itself
  // degrades (pf absent, below the floor) and spawns no message verb below
  // the floor, so the first list can start at mount and land as early as a
  // fresh page can render it (w93 — the list, its rows and the picker).
  // Once the probe has answered "absent" the effect reads nothing more (its
  // re-runs on the probe landing and on dock toggles stay silent), so a
  // pf-less install issues exactly the one mount read; the interval needs
  // presence: a pf-less install must not poll forever. Showing or leaving
  // Messages also reads at once (the groups ride only reads made while it is
  // shown) and restarts the same interval.
  const messagesVisible = panelOpen && activeTab === "messages";
  useEffect(() => {
    messagesVisibleRef.current = messagesVisible;
    if (probed && !pfPresent) return;
    refreshMessages();
    if (!probed || !pfPresent) return;
    const interval = setInterval(refreshMessages, panelOpen ? OPEN_STATUS_POLL_MS : CLOSED_STATUS_POLL_MS);
    return () => clearInterval(interval);
  }, [probed, pfPresent, panelOpen, messagesVisible, refreshMessages]);

  // The users read: only while the open dock shows Messages — once each time
  // Messages becomes visible (before the presence probe answers, like the
  // first list read; nothing more once pf is known absent). No timer.
  useEffect(() => {
    if (!messagesVisible || (probed && !pfPresent)) return;
    void refreshUsers();
  }, [probed, pfPresent, messagesVisible, refreshUsers]);

  // Watch the selection while the panel is open: immediately and every 3s.
  const selectedProfile = selectedCell ?? opsStatus?.jobs.find((job) => job.wi === selectedWi || job.slug === selectedWi)?.profile;
  useEffect(() => {
    if (!panelOpen || !selectedWi) return;
    refreshWatch(selectedWi, selectedProfile);
    const interval = setInterval(() => refreshWatch(selectedWi, selectedProfile), OPEN_WATCH_POLL_MS);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- same counter rationale as above
    return () => { ++watchSeq.current; clearInterval(interval); };
  }, [panelOpen, selectedWi, selectedProfile, refreshWatch]);

  const select = useCallback((wi: string | null, profile?: string) => {
    setSelectedWi(wi);
    setSelectedCell(wi === null ? undefined : profile);
    // A new selection starts from a clean snapshot; deselect clears it.
    setWatchStatus(null);
  }, []);

  // ---- Messages actions ----

  const loadNewestPage = useCallback((conversationId: string) => {
    return fetch(`/api/polyforge/messages/${encodeURIComponent(conversationId)}`)
      .then(async (response) => {
        const body = await response.json() as PageResponse;
        const page = Array.isArray(body.messages) ? body.messages : [];
        const hasOlder = body.has_older === true;
        if (selectedConversationRef.current !== conversationId) return;
        setMessages(page);
        setHasOlder(hasOlder);
        setMessagesError(body.error ?? null);
      })
      .catch(() => {
        if (selectedConversationRef.current !== conversationId) return;
        setMessagesError("messages are unreachable");
      })
      .finally(() => {
        // The panel may position the history at the bottom of this first page
        // even while a local job is blocked (only arrivals are suppressed).
        if (selectedConversationRef.current === conversationId) setInitialPageLoaded(conversationId);
      });
  }, []);

  const selectConversation = useCallback((conversationId: string | null) => {
    setSelectedContact(null);
    setSelectedConversation(conversationId);
    selectedConversationRef.current = conversationId;
    if (!conversationId) {
      setMessages([]);
      setHasOlder(false);
      setInitialPageLoaded(null);
      return;
    }
    // Opening — and re-opening — always fetches the conversation's newest
    // page (w93 review): a kept page predates whatever arrived while the
    // user was away, so the fresh read is the only source of now. The
    // identity guard keeps a previous selection's in-flight page from
    // landing under this one.
    setMessages([]);
    setHasOlder(false);
    setInitialPageLoaded(null);
    void loadNewestPage(conversationId);
  }, [loadNewestPage]);

  // Opening a silent contact closes any open conversation (its in-flight
  // pages are dropped by the identity guard) and shows the empty pane: no
  // history read, no conversation, no ack — the conversation exists only
  // once the first send lands.
  const selectContact = useCallback((contact: PolyforgeContact) => {
    selectConversation(null);
    setSelectedContact(contact);
  }, [selectConversation]);

  // An open silent contact follows the peer's first message:
  // once the list read (the existing 3 s / 30 s poll) carries a direct
  // conversation with that contact, the empty pane opens it — the same
  // single page writer a row click uses, no new timer or request. The empty
  // pane had no history to lose; a refused or failed list read changes
  // nothing (it keeps the last conversations).
  useEffect(() => {
    if (!selectedContact) return;
    const conversation = conversationForContact(conversations, selectedContact.id);
    if (conversation) selectConversation(conversation.id);
  }, [selectedContact, conversations, selectConversation]);

  const loadOlder = useCallback(() => {
    const conversationId = selectedConversation;
    const current = messagesRef.current;
    if (!conversationId || !hasOlder || loadingOlder || current.length === 0) return;
    const oldest = current[0].seq;
    setLoadingOlder(true);
    fetch(`/api/polyforge/messages/${encodeURIComponent(conversationId)}?before=${oldest}`)
      .then(async (response) => {
        const body = await response.json() as PageResponse;
        if (selectedConversationRef.current !== conversationId) return;
        const older = Array.isArray(body.messages) ? body.messages : [];
        setMessages((previous) => mergeOlderPage(previous, older));
        setHasOlder(body.has_older === true);
      })
      .catch(() => {
        // A failed older page leaves the loaded history intact.
      })
      .finally(() => {
        if (selectedConversationRef.current === conversationId) setLoadingOlder(false);
      });
  }, [selectedConversation, hasOlder, loadingOlder]);

  // Newer messages for the open conversation: one forward `?after=` read on
  // the existing 3s tick, merged without touching already-loaded older pages.
  const refreshOpen = useCallback((conversationId: string) => {
    const current = messagesRef.current;
    const newest = current.length > 0 ? current[current.length - 1].seq : 0;
    if (newest <= 0) return Promise.resolve();
    // No page-generation guard here: merges are idempotent by id, and a
    // concurrent interval refresh must never drop the send-driven one.
    return fetch(`/api/polyforge/messages/${encodeURIComponent(conversationId)}?after=${newest}`)
      .then(async (response) => {
        const body = await response.json() as PageResponse;
        const newer = Array.isArray(body.messages) ? body.messages : [];
        if (newer.length === 0) return;
        // Identity guard: a refresh of a previous selection never merges
        // under the current one (w93 — this replaces the page-generation
        // counter the effect cleanup used to bump).
        if (selectedConversationRef.current !== conversationId) return;
        setMessages((previous) => mergeNewestPage(previous, newer));
      })
      .catch(() => {
        // A blip keeps the loaded history.
      });
  }, []);

  useEffect(() => {
    if (!panelOpen || !selectedConversation) return;
    const interval = setInterval(() => refreshOpen(selectedConversation), OPEN_WATCH_POLL_MS);
    return () => clearInterval(interval);
  }, [panelOpen, selectedConversation, refreshOpen]);

  const acknowledge = useCallback((conversationId: string, seq: number) => {
    if (seq <= (lastAcked.current[conversationId] ?? 0)) return Promise.resolve();
    const previous = lastAcked.current[conversationId] ?? 0;
    lastAcked.current[conversationId] = seq;
    return fetch("/api/polyforge/messages/ack", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ conversation_id: conversationId, seq }),
    })
      .then(async (response) => {
        const body = await response.json() as { error?: string };
        if (!response.ok || body.error) throw new Error(body.error ?? "acknowledgment refused");
        setConversations((previousRows) => previousRows.map((row) => (
          row.id === conversationId
            ? { ...row, unread: row.last_seq <= seq ? 0 : row.unread }
            : row
        )));
      })
      .catch(() => {
        // The server did not record it: let the next poll try again.
        if ((lastAcked.current[conversationId] ?? 0) === seq) {
          lastAcked.current[conversationId] = previous;
        }
      });
  }, []);

  const blocked = opsStatus?.jobs.some((job) => job.status === "needs_input") ?? false;
  const newestDisplayedSeq = messages.length > 0 ? messages[messages.length - 1].seq : 0;

  // Auto-ack: the one pure decision, applied whenever the displayed page or
  // the visibility state changes. It names only the newest DISPLAYED seq.
  useEffect(() => {
    const conversationId = selectedConversation;
    if (!conversationId) return;
    if (!statusReady) return;
    const seq = ackDecision({
      panelOpen,
      activeTab,
      conversationId,
      blocked,
      newestDisplayedSeq,
      lastAckedSeq: lastAcked.current[conversationId] ?? 0,
    });
    if (seq !== null) void acknowledge(conversationId, seq);
  }, [panelOpen, activeTab, selectedConversation, blocked, newestDisplayedSeq, statusReady, acknowledge]);

  // An explicit user gesture (message click / scroll-to-bottom) acknowledges
  // the newest displayed sequence even while a job is blocked.
  const acknowledgeExplicitly = useCallback(() => {
    const conversationId = selectedConversation;
    const current = messagesRef.current;
    if (!conversationId || current.length === 0) return;
    const newest = current[current.length - 1].seq;
    const seq = ackDecision({
      panelOpen,
      activeTab,
      conversationId,
      blocked,
      newestDisplayedSeq: newest,
      lastAckedSeq: lastAcked.current[conversationId] ?? 0,
      explicit: true,
    });
    if (seq !== null) void acknowledge(conversationId, seq);
  }, [selectedConversation, panelOpen, activeTab, blocked, acknowledge]);

  const sendMessage = useCallback(async (input: SendMessageInput) => {
    const response = await fetch("/api/polyforge/messages", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        to: input.to,
        text: input.text,
        ...(input.aboutWi ? { about_wi: input.aboutWi } : {}),
        ...(input.replyTo ? { reply_to: input.replyTo } : {}),
      }),
    });
    const body = await response.json() as SendResponse;
    if (!response.ok || body.error) {
      throw new Error(body.error ?? "the message was refused");
    }
    await refreshMessages();
    const sent = body.message ?? null;
    // The send's own answer names the conversation it landed in (w93): open
    // it when it is not the one already open — a silent contact's first send
    // opens the conversation it started (and closes the contact selection) —
    // and refresh the open one when it is. The conversation id comes from
    // the answer itself, never from a raced list read.
    const openedId = sent?.conversation_id ?? null;
    if (openedId && openedId !== selectedConversation) {
      selectConversation(openedId);
    } else if (selectedConversation) {
      // Await the open conversation's refresh so the sent message (and a
      // reply's rendered quote) is in the history before the caller resolves.
      await refreshOpen(selectedConversation);
    }
    return sent;
  }, [refreshMessages, refreshOpen, selectedConversation, selectConversation]);

  // One membership change: the write route spawns exactly
  // one pf verb; success awaits a fresh list read so the group shows the
  // change before this resolves. A refusal throws pf's text and changes
  // nothing shown.
  const changeMembership = useCallback(async (input: MembershipChangeInput) => {
    const response = await fetch("/api/polyforge/projects/members", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ action: input.action, project: input.project, user_id: input.userId }),
    });
    const body = await response.json() as MembershipResponse;
    if (!response.ok || body.error || body.ok !== true) {
      throw new Error(body.error ?? "the membership change was refused");
    }
    await refreshMessages();
  }, [refreshMessages]);

  const analyzeMessage = useCallback(async (messageId: string) => {
    const response = await fetch("/api/polyforge/messages/analyze", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ message_id: messageId }),
    });
    const body = await response.json() as AnalyzeResponse;
    const analysis = response.ok && !body.error ? parsePolyforgeAnalysis(body.analysis) : null;
    if (!analysis) {
      throw new PolyforgeAnalyzeError(body.error ?? "the analyzer is unavailable", body.upgrade === true);
    }
    // Transient only: hook state, cleared when the dock closes.
    setAnalyses((previous) => ({ ...previous, [messageId]: analysis }));
    return analysis;
  }, []);

  // The analysis is transient by construction: closing the dock drops it
  // (no localStorage/sessionStorage/disk write anywhere).
  useEffect(() => {
    if (!panelOpen) setAnalyses({});
  }, [panelOpen]);

  const setActiveTab = useCallback((tab: PolyforgeTab) => {
    setActiveTabState(tab);
    if (tab === "messages") void refreshMessages();
  }, [refreshMessages]);

  // A work-item anchor opens the existing Jobs detail (Work › Jobs) through
  // the existing watch read (the shell's selectPolyforgeJob path).
  const openAnchor = useCallback((ref: string) => {
    setActiveTabState("work");
    setWorkTab("jobs");
    select(ref);
  }, [select]);

  const dismissDecisions = useCallback(async (ids: number[]) => {
    const response = await fetch("/api/polyforge/decisions/dismiss", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ ids }),
    });
    const body = await response.json() as DismissResponse;
    if (!response.ok || body.ok !== true) throw new Error(body.error ?? "the dismiss was refused");
    await refreshDecisions();
  }, [refreshDecisions]);

  // The Polyforge button's attention state (later widened and
  // moved onto the button itself): the
  // jobs waiting for an answer, then every other open feed entry (a
  // needs_input entry of a waiting job counted once). A click opens the next
  // one after the last opened (wrapping; the selected waiting job stands in
  // the first time, as #413's walk did): a work item's job detail in Work ›
  // Jobs, selected — nothing else opens (re-selecting the job
  // already shown keeps its snapshot; select() would blank it), a task in
  // Work › Tasks.
  const waitingJobs = needsInputJobs(opsStatus);
  const attention: PolyforgeAttentionItem[] = attentionItems(waitingJobs, decisionsFeed?.decisions ?? []);
  const openNextAttention = useCallback(() => {
    const item = nextAttentionItem(attention, { lastKey: lastAttentionKey.current, selectedWi, selectedProfile: selectedCell });
    if (!item) return;
    lastAttentionKey.current = item.key;
    const target = attentionTarget(item, opsStatus?.jobs ?? []);
    if (!target) return;
    setActiveTabState("work");
    if (target.kind === "task") {
      setWorkTab("tasks");
      setTaskRequest({ id: target.id, seq: ++taskRequestSeq.current });
      return;
    }
    setWorkTab("jobs");
    if (target.ref !== selectedWi || target.profile !== selectedCell) select(target.ref, target.profile);
  }, [attention, selectedWi, selectedCell, opsStatus, select]);

  const unreadTotal = conversations.reduce((sum, conversation) => sum + conversation.unread, 0);
  const runningCount = opsStatus ? opsStatus.queue.running : null;

  return {
    pfPresent,
    opsStatus,
    opsError,
    cards,
    cardsError,
    runningCount,
    selectedWi,
    selectedProfile,
    watchStatus,
    select,
    refreshStatus,
    activeTab,
    setActiveTab,
    workTab,
    setWorkTab,
    workItemsRunning,
    setWorkItemsRunning,
    messagesCapable,
    messagesReason,
    messagesCell,
    messagesError,
    conversations,
    contacts,
    groups,
    membershipAdmin,
    users,
    changeMembership,
    unreadTotal,
    selectedConversation,
    selectConversation,
    selectedContact,
    selectContact,
    messages,
    initialPageLoaded: selectedConversation !== null && initialPageLoaded === selectedConversation,
    blocked,
    hasOlder,
    loadingOlder,
    loadOlder,
    acknowledgeExplicitly,
    sendMessage,
    analyzeMessage,
    analyses,
    openAnchor,
    decisionsFeed,
    dismissDecisions,
    taskRequest,
    clearTaskRequest,
    attentionCount: attention.length,
    openNextAttention,
  };
}
