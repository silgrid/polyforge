"use client";

import { useCallback, useEffect, useId, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { PolyforgeAnalysisCard } from "@/components/PolyforgeAnalysisCard";
import { PolyforgeFileTicketDialog } from "@/components/PolyforgeFileTicketDialog";
import { useI18n } from "@/hooks/useI18n";
import { useIsMobile } from "@/hooks/useIsMobile";
import { usePolyforgeMessagesCollapse } from "@/hooks/usePolyforgeMessagesCollapse";
import { usePolyforgeMessagesListCollapse } from "@/hooks/usePolyforgeMessagesListCollapse";
import { useResizablePanel } from "@/hooks/useResizablePanel";
import {
  getPolyforgeMessagesListDefaultWidth,
  getPolyforgeMessagesListMaxWidth,
  POLYFORGE_MESSAGES_LIST_MAX_WIDTH,
  POLYFORGE_MESSAGES_LIST_MIN_WIDTH,
} from "@/lib/panel-layout";
import {
  appendQuoteBlock,
  avatarColor,
  avatarInitial,
  compareContactsByName,
  contactsOutsideGroups,
  conversationForContact,
  conversationName,
  directRows,
  firstWorkItemBinding,
  groupConversations,
  splitAnchor,
  splitWorkItemLinks,
  POLYFORGE_ANALYZE_MINIMUM_PF_VERSION,
  POLYFORGE_PROMPT_MINIMUM_PF_VERSION,
  type PolyforgeAnalysis,
  type PolyforgePrompt,
  type PolyforgeContact,
  type PolyforgeConversation,
  type PolyforgeMessage,
  type PolyforgeProjectGroup,
} from "@/lib/polyforge-messages-pure";

/**
 * The dock's Messages surface, its layout redone and
 * its contact list added: a Slack-like two-column panel — the list
 * as a right-hand column (the flat 单聊 area: direct conversation rows first,
 * then the silent configured contacts by display name; the 群聊 section;
 * initial-plus-colour avatars, display names, unread badges; no compose
 * entry — a contact row is the whole gesture) beside the bubble stream (own
 * bubbles right without attribution, peer bubbles left with a first-of-run
 * avatar and name, timestamps kept). No surface ever renders a raw user id:
 * every identity is the display name or the unknown-user placeholder. It
 * renders only inside the dock panel (the desktop dock and the mobile sheet
 * share the same component, two panes side by side at every width). On
 * desktop the list's left divider is also its resize separator
 *. Between the direct area and 群聊 sit the project groups
 *: one collapsible group per `cli.toml`-mapped project with
 * its contact count, an explicit empty or read-failure note, and rows that
 * open the contact's existing conversation or its empty pane (no unread
 * badge — unread lives on the direct row only). An admin (the users read
 * answered) gets an add control per group — a picker of human non-members,
 * added as writer — and a remove control per row, each behind a confirmation
 * naming the project and the display name; anyone else sees the groups
 * read-only with an explicit needs-admin notice. Its own storage is that width
 * and, through `usePolyforgeMessagesCollapse`, the collapsed groups. It never
 * writes a chat session or the agent composer.
 *
 * Message analysis: every received message — direct and
 * group — carries an inline icon+text Analyze button in the normal
 * foreground (never on the user's own messages, no menu, no hover-only
 * control). Its in-flight state is per message: that message's control is
 * disabled and starts nothing more while another message may still start.
 * A refusal (slot-full, no task chain, a failed task, the below-floor upgrade
 * text) renders in that message's own slot with the control left available;
 * a result renders as the collapsible PolyforgeAnalysisCard under the message
 * (expanded on arrival; the collapsed set is component memory). Its 【建票】
 * opens PolyforgeFileTicketDialog (a confirmed registration ticket; the
 * created slug is named under the card), its 【回复】 sets that message as the
 * reply target and fills the composer with the analysis's reply draft when
 * there is one — never overwriting the composer without one, never sending —
 * and its task link opens the task's detail in Work › Tasks (onOpenTask).
 *
 * The 「@」 action: every message row — received and own —
 * carries a visible 「@」 button beside Analyze (never inside the analysis
 * card). A click first binds the fill target (`bindPromptTarget`, AppShell's
 * click-time snapshot of the focused composer), then asks
 * `POST /api/polyforge/messages/prompt` for the execution prompt. The answer
 * is appended to that bound composer (or that session's stored draft) and
 * `已填入 <会话名>` is reported under the message; with no composer at click
 * time the prompt is shown under the message with
 * `无目标会话，已显示在消息下`, and a target that closed meanwhile shows it
 * under the message too — never redirected elsewhere. Nothing is ever sent.
 * A refusal (the floor's upgrade text, a full lane, a failed task) shows
 * under the message and leaves the action usable. In-flight state is per
 * message (one run per message, any number across messages); every outcome
 * is component memory and is gone with the panel.
 *
 * Work-item links: the composer carries no separate anchor
 * field. A message text's first `@project#number` token outside a `>` quote
 * line whose project is one of the panel's mapped projects becomes the
 * send's `about_wi` (exactly `project#number`; the text is sent unchanged),
 * and every syntactically valid token in any bubble renders in place as a
 * clickable link opening Work › Jobs through onOpenAnchor. The old anchor
 * line above a bubble is hidden only when its `about_wi` duplicates such a
 * token; field-era and CLI anchors (`task-39`, `wi_…`, a `/path`
 * suffix) keep that line. Own messages carry 【引用】 (a plain-text quote
 * block appended to the composer, never sent, leaving any reply target
 * untouched) instead of the peer 【回复】.
 *
 * Contact-column strip; the mobile sheet folds too:
 * the list column folds to a strip carrying the expand
 * control and the total-unread dot, and the chat pane takes the rest. On
 * desktop the strip is 28px; at ≤640px it is 40px with a 40px-tall column
 * header row holding the 单聊 heading and the 40×40 collapse control, and
 * the strip's 40×40 expand control sits above the same dot. While folded
 * the #222 resize separator is not rendered and no width is written, so
 * expanding shows the stored #222 width again (the sheet returns to its
 * fixed clamp); the state lives under its own key
 * (`usePolyforgeMessagesListCollapse`), beside #222's width and #223's
 * groups.
 */

export interface PolyforgeMessagesProps {
  pfPresent: boolean;
  capable: boolean;
  reason: string | null;
  conversations: PolyforgeConversation[];
  contacts: PolyforgeContact[];
  /** The mapped projects' groups; [] renders no group. */
  groups: PolyforgeProjectGroup[];
  /** The admin probe: true enables add/remove, false shows the read-only
   *  notice, null (not answered yet) shows neither. */
  membershipAdmin: boolean | null;
  /** The picker's human users (id + display name only). */
  users: PolyforgeContact[];
  /** One confirmed membership change; resolves after the groups re-read and
   *  rejects with pf's text on a refusal. */
  onChangeMembership: (input: { action: "add" | "remove"; project: string; userId: string }) => Promise<void>;
  error: string | null;
  /** the Cell the conversations are read from (null = unknown). */
  cell?: string | null;
  selectedConversation: string | null;
  onSelectConversation: (id: string | null) => void;
  /** The open silent contact (no conversation yet): an empty pane whose
   *  first send addresses `contact.id`. */
  selectedContact: PolyforgeContact | null;
  onSelectContact: (contact: PolyforgeContact) => void;
  messages: PolyforgeMessage[];
  initialPageLoaded: boolean;
  blocked: boolean;
  loadingOlder: boolean;
  onLoadOlder: () => void;
  onAcknowledge: () => void;
  onSend: (input: { to: string; text: string; aboutWi?: string; replyTo?: string }) => Promise<unknown>;
  /** One analysis of one received message; rejects with pf's text (a
   *  PolyforgeAnalyzeError whose `upgrade` marks the below-floor refusal). */
  onAnalyze: (messageId: string) => Promise<PolyforgeAnalysis>;
  /** The page-memory analyses by message id (the hook drops them on close). */
  analyses: Record<string, PolyforgeAnalysis>;
  onOpenAnchor: (ref: string) => void;
  /** The analysis card's task link: open that task in Work › Tasks. */
  onOpenTask: (taskId: string) => void;
  /** The total unread count (the panel's own sum): the collapsed strip's dot. */
  unreadTotal: number;
  /** the 「@」 action's click-time binding of its fill target
   *  (null: no composer at click time). */
  bindPromptTarget: () => PolyforgePromptTarget | null;
  /** one prompt for one message (the prompt route); rejects
   *  with pf's text, `upgrade` marking the below-floor refusal. */
  onPrompt: (messageId: string) => Promise<PolyforgePrompt>;
  /** whether any 「@」 run is in flight (false on unmount):
   *  the desktop dock is not dismissed by an outside press meanwhile, so a
   *  prompt shown under its message is not lost to a click elsewhere. */
  onPromptBusyChange: (busy: boolean) => void;
}

/** The fill target the 「@」 action binds at click time. */
export interface PolyforgePromptTarget {
  /** The bound session's display name (the 已填入 feedback line). */
  name: string;
  /** Append the prompt to the bound composer, or to its session's stored
   *  draft when that composer is not mounted; false when the bound target
   *  has closed — then nothing was written anywhere. */
  fill: (prompt: string) => boolean;
}

/** One 「@」 outcome under its message (component memory only). */
interface PromptOutcome {
  /** The prompt shown under the message (no target, or the target closed). */
  shown?: string;
  /** The feedback line. */
  feedback?: string;
  error?: { text: string; upgrade: boolean };
}



function AnchorText({ anchor, onOpenAnchor }: { anchor: string; onOpenAnchor: (ref: string) => void }) {
  const { ref, rest } = splitAnchor(anchor);
  if (!ref) return <span>{anchor}</span>;
  return (
    <span>
      <button
        type="button"
        onClick={(event) => { event.stopPropagation(); onOpenAnchor(ref); }}
        aria-label={ref}
        style={{ background: "none", border: "none", padding: 0, color: "var(--accent)", cursor: "pointer", font: "inherit", textDecoration: "underline" }}
      >
        {ref}
      </button>
      {rest && <span>{rest}</span>}
    </span>
  );
}

/** The initial-plus-colour avatar. `seed` is the stable peer id (hash input
 *  only, never rendered); `name` is the display name the initial comes from.
 *  aria-hidden: the visible name beside it already identifies the peer. */
function Avatar({ seed, name }: { seed: string; name: string }) {
  return (
    <span
      aria-hidden="true"
      style={{
        flexShrink: 0, width: 22, height: 22, borderRadius: 11,
        background: avatarColor(seed), color: "#ffffff",
        display: "inline-flex", alignItems: "center", justifyContent: "center",
        fontSize: 11, fontWeight: 700,
      }}
    >
      {avatarInitial(name)}
    </span>
  );
}

/** A copy of `record` without `key` (per-message component state). */
function withoutKey<T>(record: Record<string, T>, key: string): Record<string, T> {
  if (!(key in record)) return record;
  const next = { ...record };
  delete next[key];
  return next;
}

/** HH:MM from the sanitized created_at; the raw string when unparsable. */
function formatTimestamp(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return `${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
}

export function PolyforgeMessages({
  pfPresent,
  capable,
  reason,
  conversations,
  contacts: configuredContacts,
  groups,
  membershipAdmin,
  users,
  onChangeMembership,
  error,
  cell = null,
  selectedConversation,
  onSelectConversation,
  selectedContact,
  onSelectContact,
  messages,
  initialPageLoaded,
  blocked,
  loadingOlder,
  onLoadOlder,
  onAcknowledge,
  onSend,
  onAnalyze,
  analyses,
  onOpenAnchor,
  onOpenTask,
  unreadTotal,
  bindPromptTarget,
  onPrompt,
  onPromptBusyChange,
}: PolyforgeMessagesProps) {
  const { t } = useI18n();
  const isMobile = useIsMobile();
  const { collapsed, toggleProject } = usePolyforgeMessagesCollapse();
  // The whole-column strip at every width.
  const { listCollapsed, setListCollapsed } = usePolyforgeMessagesListCollapse();
  // The whole Messages surface measures the panel the list's bounds derive
  // from (the dock drag moves this width without a window resize).
  const messagesPanelRef = useRef<HTMLDivElement | null>(null);
  const listWidthRef = useRef(0);
  const historyRef = useRef<HTMLDivElement | null>(null);
  const stickToBottomRef = useRef(true);
  // A programmatic scrollTop write also fires onScroll; only a real user
  // gesture may turn reaching the bottom into an explicit acknowledgment.
  const userScrolledRef = useRef(false);
  // The open conversation's peer is the recipient; it is captured on open so
  // a list poll blip cannot blank a reply's recipient.
  const [lastPeer, setLastPeer] = useState("");
  const [replyTo, setReplyTo] = useState<string | null>(null);
  // per-message analysis state — the messages in flight (the
  // ref is the guard, the state renders it), each message's last refusal and
  // the cards the user collapsed. Component memory only.
  const analyzingRef = useRef(new Set<string>());
  const [analyzing, setAnalyzing] = useState<Record<string, true>>({});
  const [analysisErrors, setAnalysisErrors] = useState<Record<string, { text: string; upgrade: boolean }>>({});
  const [collapsedCards, setCollapsedCards] = useState<Record<string, true>>({});
  // per-message 「@」 state — the messages in flight (the ref is
  // the guard, the state renders it) and each message's last outcome.
  const promptingRef = useRef(new Set<string>());
  const [prompting, setPrompting] = useState<Record<string, true>>({});
  const [promptOutcomes, setPromptOutcomes] = useState<Record<string, PromptOutcome>>({});
  const promptBusy = Object.keys(prompting).length > 0;
  useEffect(() => {
    onPromptBusyChange(promptBusy);
  }, [onPromptBusyChange, promptBusy]);
  useEffect(() => () => onPromptBusyChange(false), [onPromptBusyChange]);
  // The message whose 【建票】 dialog is open, and the tickets created from
  // each card (named under it).
  const [ticketFor, setTicketFor] = useState<string | null>(null);
  const [ticketNotices, setTicketNotices] = useState<Record<string, string>>({});
  // The composer's draft, the refusal text and the in-flight flag.
  const [draftText, setDraftText] = useState("");
  const textFieldId = useId();
  const composerTextRef = useRef<HTMLTextAreaElement | null>(null);
  const [sendError, setSendError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  // the group whose add picker is open, the one membership
  // change in flight, and the last refused change's text (shown in its group).
  const [addingTo, setAddingTo] = useState<string | null>(null);
  const [membershipBusy, setMembershipBusy] = useState(false);
  const [membershipError, setMembershipError] = useState<{ project: string; text: string } | null>(null);

  const messagesPanelWidth = useCallback(
    () => messagesPanelRef.current?.getBoundingClientRect().width ?? 0,
    [],
  );
  const listDefaultWidth = useCallback(() => {
    const panelWidth = messagesPanelWidth();
    return panelWidth > 0 ? getPolyforgeMessagesListDefaultWidth(panelWidth) : POLYFORGE_MESSAGES_LIST_MIN_WIDTH;
  }, [messagesPanelWidth]);
  const listMaxWidth = useCallback(() => {
    const panelWidth = messagesPanelWidth();
    return panelWidth > 0 ? getPolyforgeMessagesListMaxWidth(panelWidth) : POLYFORGE_MESSAGES_LIST_MAX_WIDTH;
  }, [messagesPanelWidth]);
  // desktop only. persistClamp stays off so a dock narrowed
  // past the stored width only shrinks what is shown, never the saved value.
  const listResizer = useResizablePanel({
    ariaLabel: t("polyforge.messages.resizeList"),
    cssVariable: "--pf-messages-list-width",
    defaultWidth: POLYFORGE_MESSAGES_LIST_MIN_WIDTH,
    getDefaultWidth: listDefaultWidth,
    getMaxWidth: listMaxWidth,
    growthDirection: "left",
    maxWidth: POLYFORGE_MESSAGES_LIST_MAX_WIDTH,
    minWidth: POLYFORGE_MESSAGES_LIST_MIN_WIDTH,
    persistClamp: false,
    storageKey: "pi-web:polyforge-messages-list-width",
    widthRef: listWidthRef,
  });
  const reclampListWidth = listResizer.reclampWidth;

  // The hook refits on window resize only; a dock drag (or a sheet change)
  // changes this surface's own width, so re-fit from its box as well.
  useEffect(() => {
    const element = messagesPanelRef.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => { reclampListWidth(); });
    observer.observe(element);
    return () => observer.disconnect();
    // The unavailable/capable branches and the list share this ref; when the
    // rendered branch changes, observe the element that is actually there.
  }, [reclampListWidth, pfPresent, capable]);

  const selectedRow = selectedConversation
    ? conversations.find((conversation) => conversation.id === selectedConversation)
    : undefined;
  const selectedPeer = selectedRow?.peer ?? "";
  useEffect(() => { if (selectedPeer) setLastPeer(selectedPeer); }, [selectedPeer]);
  // The send address: the open conversation's peer, or — for a silent
  // contact with no conversation yet — the contact's id. Neither is rendered.
  const recipient = selectedConversation ? (selectedPeer || lastPeer) : (selectedContact?.id ?? "");
  // The pane's identity: the avatar hash seed and the display name.
  const paneSeed = selectedConversation ? selectedPeer : (selectedContact?.id ?? "");

  // A newly opened conversation or contact resets the composer's reply state.
  useEffect(() => { setReplyTo(null); }, [selectedConversation, selectedContact]);

  // Opening a conversation always positions the history at the newest message,
  // even while a local job is blocked: blocked suppresses only ARRIVAL
  // auto-scroll. A programmatic scroll never becomes an ack (userScrolledRef).
  useEffect(() => {
    stickToBottomRef.current = true;
    userScrolledRef.current = false;
    if (!initialPageLoaded) return;
    const element = historyRef.current;
    if (element) element.scrollTop = element.scrollHeight;
  }, [selectedConversation, initialPageLoaded]);

  useEffect(() => {
    const element = historyRef.current;
    if (!element || blocked || !stickToBottomRef.current) return;
    element.scrollTop = element.scrollHeight;
  }, [messages, blocked]);

  const markUserScroll = useCallback(() => { userScrolledRef.current = true; }, []);

  const handleScroll = useCallback((event: React.UIEvent<HTMLDivElement>) => {
    const element = event.currentTarget;
    const atBottom = element.scrollHeight - element.clientHeight - element.scrollTop < 24;
    stickToBottomRef.current = atBottom;
    if (element.scrollTop <= 4) onLoadOlder();
    if (atBottom && userScrolledRef.current) {
      userScrolledRef.current = false;
      onAcknowledge();
    }
  }, [onLoadOlder, onAcknowledge]);

  const replyOriginal = replyTo ? messages.find((message) => message.id === replyTo) : undefined;

  // The composer sends to the open conversation's peer, or to the open
  // silent contact's id (its first send; the hook then opens the
  // conversation the send's answer names). The composer clears the moment
  // the send is accepted for submission and restores the attempt on a
  // refusal (pf's 4xx text shows under it), so a rendered message body
  // always means the message actually reached the history (never an
  // in-flight composer echo).
  const submit = useCallback(async (event: React.FormEvent) => {
    event.preventDefault();
    if (sending) return;
    const attempt = { text: draftText };
    setSending(true);
    setSendError(null);
    setDraftText("");
    // the binding comes from the text itself — the first token
    // outside a `>` quote line whose project is mapped in this panel. No
    // match sends plain text; the text is sent verbatim either way.
    const aboutWi = firstWorkItemBinding(attempt.text, groups.map((group) => group.project));
    try {
      await onSend({
        to: recipient,
        text: attempt.text,
        ...(aboutWi ? { aboutWi } : {}),
        ...(replyTo ? { replyTo } : {}),
      });
      setReplyTo(null);
    } catch (submitError) {
      setDraftText(attempt.text);
      setSendError(submitError instanceof Error ? submitError.message : String(submitError));
    } finally {
      setSending(false);
    }
  }, [sending, draftText, groups, onSend, recipient, replyTo]);

  const analyze = useCallback(async (messageId: string) => {
    if (analyzingRef.current.has(messageId)) return;
    analyzingRef.current.add(messageId);
    setAnalyzing((previous) => ({ ...previous, [messageId]: true }));
    setAnalysisErrors((previous) => withoutKey(previous, messageId));
    try {
      await onAnalyze(messageId);
      setCollapsedCards((previous) => withoutKey(previous, messageId));
    } catch (analyzeError) {
      const text = analyzeError instanceof Error ? analyzeError.message : String(analyzeError);
      const upgrade = Boolean((analyzeError as { upgrade?: unknown } | null)?.upgrade);
      setAnalysisErrors((previous) => ({ ...previous, [messageId]: { text, upgrade } }));
    } finally {
      analyzingRef.current.delete(messageId);
      setAnalyzing((previous) => withoutKey(previous, messageId));
    }
  }, [onAnalyze]);

  // 「@」: the target is bound at click time, before the request; the answer
  // fills only that target. Nothing is sent.
  const promptMessage = useCallback(async (messageId: string) => {
    if (promptingRef.current.has(messageId)) return;
    const target = bindPromptTarget();
    promptingRef.current.add(messageId);
    setPrompting((previous) => ({ ...previous, [messageId]: true }));
    setPromptOutcomes((previous) => withoutKey(previous, messageId));
    let outcome: PromptOutcome;
    try {
      const { prompt } = await onPrompt(messageId);
      if (!target) {
        outcome = { shown: prompt, feedback: t("polyforge.messages.promptNoTarget") };
      } else if (target.fill(prompt)) {
        outcome = { feedback: t("polyforge.messages.promptFilled", { name: target.name }) };
      } else {
        outcome = { shown: prompt };
      }
    } catch (promptError) {
      const text = promptError instanceof Error ? promptError.message : String(promptError);
      outcome = { error: { text, upgrade: Boolean((promptError as { upgrade?: unknown } | null)?.upgrade) } };
    }
    setPromptOutcomes((previous) => ({ ...previous, [messageId]: outcome }));
    promptingRef.current.delete(messageId);
    setPrompting((previous) => withoutKey(previous, messageId));
  }, [bindPromptTarget, onPrompt, t]);

  // 【回复】: that message becomes the reply target; a reply draft fills the
  // composer, and without one the composer keeps its text. Nothing is sent.
  const replyFromAnalysis = useCallback((messageId: string, analysis: PolyforgeAnalysis) => {
    flushSync(() => {
      setReplyTo(messageId);
      if (analysis.reply_draft) setDraftText(analysis.reply_draft);
    });
  }, []);

  // 【引用】 on an own message: a plain-text quote block of its
  // text appended to the draft — never sent, never overwriting it, and never
  // touching replyTo (a pending peer reply survives). Repeated clicks append
  // again; the textarea keeps focus with the caret at the end.
  const quoteOwnMessage = useCallback((text: string) => {
    flushSync(() => setDraftText((previous) => appendQuoteBlock(previous, text)));
    const field = composerTextRef.current;
    if (!field) return;
    field.focus();
    field.setSelectionRange(field.value.length, field.value.length);
  }, []);

  const toggleCard = useCallback((messageId: string) => {
    setCollapsedCards((previous) => {
      if (previous[messageId]) return withoutKey(previous, messageId);
      return { ...previous, [messageId]: true };
    });
  }, []);

  // One membership change, behind a confirmation naming the project and the
  // display name (or the placeholder). Cancelling returns before any request.
  // The groups change only through the hook's awaited re-read; a refusal
  // shows pf's text in that group and changes nothing shown.
  const changeMembership = useCallback(async (action: "add" | "remove", project: string, user: PolyforgeContact) => {
    setAddingTo(null);
    if (membershipBusy) return;
    const name = user.display_name.trim() || t("polyforge.messages.unknownUser");
    const question = action === "add"
      ? t("polyforge.messages.confirmAddMember", { name, project })
      : t("polyforge.messages.confirmRemoveMember", { name, project });
    if (!window.confirm(question)) return;
    setMembershipBusy(true);
    setMembershipError(null);
    try {
      await onChangeMembership({ action, project, userId: user.id });
    } catch (changeError) {
      setMembershipError({ project, text: changeError instanceof Error ? changeError.message : String(changeError) });
    } finally {
      setMembershipBusy(false);
    }
  }, [membershipBusy, onChangeMembership, t]);

  // The composer: it sends to the open conversation's peer or the open
  // silent contact (no recipient field — the header names the peer).
  const composer = (
    <form onSubmit={(event) => void submit(event)} style={{ display: "flex", flexDirection: "column", gap: 6, padding: "8px 12px", borderTop: "1px solid var(--border)", flexShrink: 0 }}>
      {replyOriginal && (
        <div style={{ fontSize: 11, color: "var(--text-muted)", borderLeft: "2px solid var(--border)", paddingLeft: 6 }}>
          {t("polyforge.messages.quote")}: {replyOriginal.text}
        </div>
      )}
      {/* each field is named by its label alone (htmlFor, not a
          wrapping label), so a filled or prefilled draft never becomes part
          of the field's accessible name. the anchor field is
          gone; the textarea's placeholder names the inline token form. */}
      <div style={{ display: "flex", flexDirection: "column", gap: 2, fontSize: 11, color: "var(--text-dim)" }}>
        <label htmlFor={textFieldId}>{t("polyforge.messages.text")}</label>
        <textarea ref={composerTextRef} value={draftText} id={textFieldId} onChange={(event) => setDraftText(event.target.value)} rows={3}
          placeholder={t("polyforge.messages.textPlaceholder")}
          style={{ background: "var(--bg)", color: "var(--text)", border: "1px solid var(--border)", borderRadius: 4, padding: "4px 6px", fontSize: 12, resize: "vertical" }} />
      </div>
      {sendError && <div style={{ fontSize: 11, color: "#dc2626" }}>{sendError}</div>}
      <button type="submit" disabled={sending}
        style={{ alignSelf: "flex-end", background: "var(--accent)", color: "var(--bg-panel)", border: "none", borderRadius: 4, padding: "4px 12px", fontSize: 12, cursor: sending ? "default" : "pointer" }}>
        {t("polyforge.messages.send")}
      </button>
    </form>
  );

  if (!pfPresent) {
    return <div ref={messagesPanelRef} style={{ padding: 16, color: "var(--text-dim)", fontSize: 12 }}>{t("polyforge.unavailable")}</div>;
  }
  if (!capable) {
    return (
      <div ref={messagesPanelRef} style={{ padding: 16, display: "flex", flexDirection: "column", gap: 6, fontSize: 12 }}>
        <span style={{ color: "var(--text)" }}>{t("polyforge.messages.upgrade")}</span>
        <span style={{ color: "var(--text-dim)" }}>{reason ?? t("polyforge.messages.upgradeReason")}</span>
      </div>
    );
  }

  // One tree and one two-pane layout for desktop and mobile:
  // the list column and the chat pane are always side by side.
  const chatOpen = selectedConversation !== null || selectedContact !== null;

  const sections = groupConversations(conversations);
  // a contact listed in a project group lives there; the
  // direct area keeps its conversation rows and only the ungrouped silent
  // contacts (all of them when no group renders).
  const contacts = contactsOutsideGroups(configuredContacts, groups);
  const direct = directRows(conversations, contacts);

  const renderRow = (conversation: PolyforgeConversation) => {
    const name = conversationName(conversation);
    return (
      <button
        key={conversation.id}
        type="button"
        // Re-clicking the already-open row is a no-op: a second select would
        // blank the open history and restart its page fetch (the row stays
        // rendered in the two-column layout, so the click can repeat).
        onClick={() => { if (conversation.id !== selectedConversation) onSelectConversation(conversation.id); }}
        style={{
          display: "flex", alignItems: "center", gap: 6, padding: "6px 8px",
          background: conversation.id === selectedConversation ? "var(--bg-selected)" : "none",
          border: "none", cursor: "pointer", textAlign: "left", width: "100%", font: "inherit",
        }}
      >
        <Avatar seed={conversation.peer ?? conversation.id} name={name} />
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 12, color: "var(--text)" }}>
          {name || t("polyforge.messages.unknownUser")}
        </span>
        {conversation.unread > 0 && (
          <span aria-label={t("polyforge.messages.unread")} title={t("polyforge.messages.unread")}
            style={{ minWidth: 16, height: 16, padding: "0 4px", borderRadius: 8, background: "var(--accent)", color: "var(--bg-panel)", fontSize: 10, fontWeight: 700, display: "flex", alignItems: "center", justifyContent: "center", flexShrink: 0 }}>
            {conversation.unread}
          </span>
        )}
      </button>
    );
  };

  // A silent contact row: avatar, display name (or the placeholder), no
  // unread. Clicking it opens the empty pane — no conversation is created
  // and nothing is acknowledged; re-clicking the open contact is a no-op.
  const renderContact = (contact: PolyforgeContact) => {
    const name = contact.display_name.trim();
    const open = selectedConversation === null && selectedContact?.id === contact.id;
    return (
      <button
        key={`contact:${contact.id}`}
        type="button"
        onClick={() => { if (!open) onSelectContact(contact); }}
        style={{
          display: "flex", alignItems: "center", gap: 6, padding: "6px 8px",
          background: open ? "var(--bg-selected)" : "none",
          border: "none", cursor: "pointer", textAlign: "left", width: "100%", font: "inherit",
        }}
      >
        <Avatar seed={contact.id} name={name} />
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 12, color: "var(--text)" }}>
          {name || t("polyforge.messages.unknownUser")}
        </span>
      </button>
    );
  };

  // A project group's contact row: the display name (or the
  // placeholder) and no unread badge. A contact with a direct conversation
  // opens it; one without opens the empty pane — nothing is created or
  // acknowledged. Re-clicking what is already open is a no-op.
  const renderGroupContact = (project: string, contact: PolyforgeContact) => {
    const name = contact.display_name.trim();
    const existing = conversationForContact(conversations, contact.id);
    const open = existing
      ? existing.id === selectedConversation
      : selectedConversation === null && selectedContact?.id === contact.id;
    return (
      <div key={`group:${project}:${contact.id}`} style={{ display: "flex", alignItems: "center" }}>
        <button
          type="button"
          onClick={() => {
            if (open) return;
            if (existing) onSelectConversation(existing.id);
            else onSelectContact(contact);
          }}
          style={{
            display: "flex", alignItems: "center", gap: 6, padding: "6px 8px 6px 16px", flex: 1, minWidth: 0,
            background: open ? "var(--bg-selected)" : "none",
            border: "none", cursor: "pointer", textAlign: "left", font: "inherit",
          }}
        >
          <Avatar seed={contact.id} name={name} />
          <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 12, color: "var(--text)" }}>
            {name || t("polyforge.messages.unknownUser")}
          </span>
        </button>
        {membershipAdmin === true && (
          <button
            type="button"
            disabled={membershipBusy}
            onClick={() => void changeMembership("remove", project, contact)}
            aria-label={t("polyforge.messages.removeMember", { name: name || t("polyforge.messages.unknownUser"), project })}
            style={{ flexShrink: 0, background: "none", border: "none", color: "var(--text-muted)", cursor: membershipBusy ? "default" : "pointer", fontSize: 12, padding: "0 8px" }}
          >
            ×
          </button>
        )}
      </div>
    );
  };

  // The add picker: the human users not already in the group
  // (machines never arrive; the caller is dropped server-side), by display
  // name. Choosing one asks for the confirmation; there is no role choice.
  const renderPicker = (group: PolyforgeProjectGroup) => {
    const members = new Set(group.contacts.map((contact) => contact.id));
    const candidates = users.filter((user) => !members.has(user.id)).sort(compareContactsByName);
    return (
      <div style={{ margin: "2px 8px 4px 16px", border: "1px solid var(--border)", borderRadius: 4, display: "flex", flexDirection: "column" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "4px 6px", fontSize: 11, color: "var(--text-dim)" }}>
          <span style={{ flex: 1, minWidth: 0 }}>{t("polyforge.messages.pickMember")}</span>
          <button type="button" onClick={() => setAddingTo(null)}
            style={{ background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 11, padding: 0 }}>
            {t("polyforge.messages.pickClose")}
          </button>
        </div>
        {candidates.map((user) => {
          const name = user.display_name.trim();
          return (
            <button
              key={`pick:${group.project}:${user.id}`}
              type="button"
              onClick={() => void changeMembership("add", group.project, user)}
              style={{ display: "flex", alignItems: "center", gap: 6, padding: "4px 6px", background: "none", border: "none", cursor: "pointer", textAlign: "left", font: "inherit" }}
            >
              <Avatar seed={user.id} name={name} />
              <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 12, color: "var(--text)" }}>
                {name || t("polyforge.messages.unknownUser")}
              </span>
            </button>
          );
        })}
        {candidates.length === 0 && (
          <div style={{ padding: "4px 6px", fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.messages.pickEmpty")}</div>
        )}
      </div>
    );
  };

  // One project group: a header button (name, count, aria-expanded) that
  // folds it — expanded unless this browser stored it collapsed — then its
  // rows, its read-failure note, or the explicit empty placeholder. The
  // count is the group's contact count whether or not it is folded.
  const renderProjectGroup = (group: PolyforgeProjectGroup) => {
    const expanded = !collapsed.has(group.project);
    const bodyId = `pf-messages-group-${group.project}`;
    return (
      <div key={`project:${group.project}`}>
        <div style={{ display: "flex", alignItems: "center" }}>
          <button
            type="button"
            aria-expanded={expanded}
            aria-controls={bodyId}
            aria-label={t("polyforge.messages.projectGroupLabel", { project: group.project, count: group.contacts.length })}
            onClick={() => toggleProject(group.project)}
            style={{
              display: "flex", alignItems: "center", gap: 4, padding: "4px 8px", flex: 1, minWidth: 0,
              background: "none", border: "none", cursor: "pointer", textAlign: "left", font: "inherit",
              fontSize: 11, color: "var(--text-muted)",
            }}
          >
            <span aria-hidden="true" style={{ width: 10, flexShrink: 0 }}>{expanded ? "▾" : "▸"}</span>
            <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{group.project}</span>
            <span style={{ flexShrink: 0, color: "var(--text-dim)" }}>{group.contacts.length}</span>
          </button>
          {membershipAdmin === true && !group.error && (
            <button
              type="button"
              disabled={membershipBusy}
              onClick={() => { setMembershipError(null); setAddingTo(addingTo === group.project ? null : group.project); }}
              aria-label={t("polyforge.messages.addMember", { project: group.project })}
              style={{ flexShrink: 0, background: "none", border: "none", color: "var(--text-muted)", cursor: membershipBusy ? "default" : "pointer", fontSize: 13, padding: "0 8px" }}
            >
              +
            </button>
          )}
        </div>
        {addingTo === group.project && membershipAdmin === true && renderPicker(group)}
        {membershipError?.project === group.project && (
          <div role="alert" style={{ padding: "2px 8px 4px 16px", fontSize: 11, color: "#dc2626", overflowWrap: "anywhere" }}>
            {t("polyforge.messages.membershipFailed")}: {membershipError.text}
          </div>
        )}
        {expanded && (
          <div id={bodyId}>
            {group.error ? (
              <div style={{ padding: "2px 8px 4px 16px", fontSize: 11, color: "#dc2626", overflowWrap: "anywhere" }}>
                {t("polyforge.messages.groupReadFailed")}: {group.error}
              </div>
            ) : group.contacts.length === 0 ? (
              <div data-empty-group={group.project} style={{ padding: "2px 8px 4px 16px", fontSize: 11, color: "var(--text-dim)" }}>
                {t("polyforge.messages.groupEmpty")}
              </div>
            ) : (
              group.contacts.map((contact) => renderGroupContact(group.project, contact))
            )}
          </div>
        )}
      </div>
    );
  };

  // The list column: the flat 单聊 area (conversation rows first, then the
  // silent contacts by display name, one row per peer) and the 群聊 section,
  // each with its empty placeholder. On desktop its left divider is the
  // resize separator; the stored width survives a narrow dock
  // and the chat pane always keeps its 200px floor. Mobile keeps the fixed
  // clamp with no handle, but folds to its own 40px strip
  // through the shared key.
  const listColumn = (
    <div
      ref={listResizer.panelRef}
      style={{
        position: "relative",
        width: listCollapsed ? (isMobile ? 40 : 28)
          : isMobile ? "clamp(120px, 35%, 220px)" : "var(--pf-messages-list-width)",
        flexShrink: 0, minHeight: 0,
        display: "flex", flexDirection: "column",
        "--pf-messages-list-width": `${listResizer.width}px`,
      } as React.CSSProperties}
    >
      {listCollapsed ? (
        <div
          data-pf-messages-strip=""
          style={{ flex: 1, minHeight: 0, borderLeft: "1px solid var(--border)", display: "flex", flexDirection: "column", alignItems: "center", gap: 8, paddingTop: isMobile ? 0 : 6 }}
        >
          <button
            type="button"
            aria-label={t("polyforge.messages.expandList")}
            title={t("polyforge.messages.expandList")}
            onClick={() => setListCollapsed(false)}
            style={{ background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", padding: isMobile ? 0 : 2, width: isMobile ? 40 : undefined, height: isMobile ? 40 : undefined, display: "inline-flex", alignItems: "center", justifyContent: "center" }}
          >
            <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="m15 18-6-6 6-6" />
            </svg>
          </button>
          {/* The total-unread indicator: filled while anything is unread,
              an empty ring at zero (its label names the count either way). */}
          <span
            data-unread-dot=""
            aria-label={t("polyforge.messages.unreadTotal", { count: unreadTotal })}
            title={t("polyforge.messages.unreadTotal", { count: unreadTotal })}
            style={{
              width: 8, height: 8, borderRadius: 4, display: "inline-block", boxSizing: "border-box",
              background: unreadTotal > 0 ? "var(--accent)" : "none", border: unreadTotal > 0 ? "none" : "1px solid var(--border)",
            }}
          />
        </div>
      ) : (<>
        {cell && (
          <div data-pf-tab-cell={cell} style={{ padding: "4px 8px 0", fontSize: 11, color: "var(--text-dim)", flexShrink: 0 }}>{t("polyforge.tabCell", { cell })}</div>
        )}
      {!isMobile && (
        <div
          {...listResizer.separatorProps}
          aria-controls="pf-messages-list"
          className={`pf-messages-resize-handle${listResizer.isResizing ? " is-resizing" : ""}`}
          data-resize-handle="polyforge-messages-list"
          title={`${t("polyforge.messages.resizeList")}: ${t("layout.resizeHint")}`}
        />
      )}
      {isMobile && (
        <div
          data-pf-messages-column-header=""
          style={{ height: 40, flexShrink: 0, display: "flex", alignItems: "center", borderLeft: "1px solid var(--border)", paddingLeft: 8 }}
        >
          <span style={{ flex: 1, minWidth: 0, fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.messages.sectionDirect")}</span>
          <button
            type="button"
            aria-label={t("polyforge.messages.collapseList")}
            title={t("polyforge.messages.collapseList")}
            onClick={() => setListCollapsed(true)}
            style={{ width: 40, height: 40, flexShrink: 0, background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", padding: 0, display: "inline-flex", alignItems: "center", justifyContent: "center" }}
          >
            <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="m9 18 6-6-6-6" />
            </svg>
          </button>
        </div>
      )}
      <div
        id="pf-messages-list"
        style={{
          flex: 1, minHeight: 0,
          borderLeft: "1px solid var(--border)",
          overflowY: "auto",
        }}
      >
        {error && <div style={{ padding: "6px 8px", color: "#dc2626", fontSize: 11, flexShrink: 0 }}>{error}</div>}
        {!isMobile && (
          <div style={{ padding: "6px 8px 2px", fontSize: 11, color: "var(--text-dim)", flexShrink: 0 }}>
            {t("polyforge.messages.sectionDirect")}
          </div>
        )}
        {direct.map((row) => (row.kind === "conversation" ? renderRow(row.conversation) : renderContact(row.contact)))}
        {direct.length === 0 && (
          <div style={{ padding: "4px 8px", fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.messages.empty")}</div>
        )}
        {groups.length > 0 && (
          <div style={{ padding: "6px 8px 2px", fontSize: 11, color: "var(--text-dim)", flexShrink: 0 }}>
            {t("polyforge.messages.sectionProjects")}
          </div>
        )}
        {groups.length > 0 && membershipAdmin === false && (
          <div style={{ padding: "2px 8px 4px", fontSize: 11, color: "var(--text-muted)" }}>
            {t("polyforge.messages.membershipNeedsAdmin")}
          </div>
        )}
        {groups.map(renderProjectGroup)}
        <div style={{ padding: "6px 8px 2px", fontSize: 11, color: "var(--text-dim)", flexShrink: 0 }}>
          {t("polyforge.messages.sectionGroups")}
        </div>
        {sections.groups.map(renderRow)}
        {sections.groups.length === 0 && (
          <div style={{ padding: "4px 8px", fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.messages.noGroups")}</div>
        )}
      </div>
      {!isMobile && (
        <button
          type="button"
          aria-label={t("polyforge.messages.collapseList")}
          title={t("polyforge.messages.collapseList")}
          onClick={() => setListCollapsed(true)}
          style={{ position: "absolute", top: 4, right: 4, background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", padding: 2, display: "inline-flex" }}
        >
          <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="m9 18 6-6-6-6" />
          </svg>
        </button>
      )}
      </>)}
    </div>
  );

  // The chat pane: with nothing open it carries only the select-a-conversation
  // empty state (the history region and composer are not rendered, so
  // nothing acks and no conversation was auto-opened); with a conversation
  // or a silent contact open it is the header, the bubble stream (empty for
  // a contact until its first send) and the composer.
  const ticketMessage = ticketFor ? messages.find((message) => message.id === ticketFor) : undefined;
  const ticketAnalysis = ticketFor ? analyses[ticketFor] : undefined;
  const headerName = selectedRow
    ? conversationName(selectedRow)
    : (selectedContact?.display_name.trim() ?? "");
  const chatPane = !chatOpen ? (
    <div style={{ flex: 1, minHeight: 0, display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text-dim)", fontSize: 12, padding: 16 }}>
      {t("polyforge.messages.select")}
    </div>
  ) : (
    <div style={{ flex: 1, minWidth: 0, minHeight: 0, display: "flex", flexDirection: "column" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
        <button type="button" onClick={() => onSelectConversation(null)}
          style={{ background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 11, padding: 0 }}>
          ← {t("polyforge.messages.back")}
        </button>
        <Avatar seed={paneSeed} name={headerName} />
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 12, color: "var(--text)" }}>
          {headerName || t("polyforge.messages.unknownUser")}
        </span>
      </div>

      <div
        ref={historyRef}
        onScroll={handleScroll}
        onWheel={markUserScroll}
        onTouchMove={markUserScroll}
        onPointerDown={markUserScroll}
        onKeyDown={markUserScroll}
        aria-label={t("polyforge.messages.history")}
        style={{ flex: 1, minHeight: 0, overflowY: "auto", padding: "8px 12px", display: "flex", flexDirection: "column", gap: 8 }}
      >
        {loadingOlder && <div style={{ fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.messages.loadingOlder")}</div>}
        {messages.map((message, index) => {
          const original = message.reply_to
            ? messages.find((candidate) => candidate.id === message.reply_to)
            : undefined;
          // In a direct conversation every consecutive non-own neighbour is
          // the same peer, so the attribution shows on the first of a run.
          const firstOfRun = index === 0 || messages[index - 1].mine;
          // every valid token in the body is linkable, and the
          // separate anchor line stays only when it is not already in the body.
          const bodySegments = splitWorkItemLinks(message.text);
          const anchorIsInline = Boolean(message.about_wi) && bodySegments.some((segment) => segment.ref === message.about_wi);
          return (
            <div
              key={message.id}
              onClick={onAcknowledge}
              style={{
                display: "flex", flexDirection: "column", gap: 2, padding: "6px 8px", borderRadius: 6,
                cursor: "default", background: message.mine ? "var(--user-bg)" : "var(--bg-hover)",
                alignSelf: message.mine ? "flex-end" : "flex-start", maxWidth: "80%",
              }}
            >
              {!message.mine && firstOfRun && (
                <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
                  <Avatar seed={paneSeed} name={headerName} />
                  <span style={{ fontSize: 11, color: "var(--text)" }}>
                    {headerName || t("polyforge.messages.unknownUser")}
                  </span>
                </span>
              )}
              {message.about_wi && !anchorIsInline && (
                <span style={{ fontSize: 11, color: "var(--text-muted)" }}>
                  <AnchorText anchor={message.about_wi} onOpenAnchor={onOpenAnchor} />
                </span>
              )}
              <span style={{ fontSize: 12, color: message.about_wi ? "var(--text)" : "var(--text-dim)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                {bodySegments.map((segment, segmentIndex) => segment.ref ? (
                  <button
                    key={`${message.id}-${segmentIndex}`}
                    type="button"
                    onClick={(event) => { event.stopPropagation(); onOpenAnchor(segment.ref!); }}
                    aria-label={segment.ref}
                    style={{ background: "none", border: "none", padding: 0, color: "var(--accent)", cursor: "pointer", font: "inherit", textDecoration: "underline" }}
                  >
                    {segment.text}
                  </button>
                ) : (
                  <span key={`${message.id}-${segmentIndex}`}>{segment.text}</span>
                ))}
              </span>
              {message.reply_to && (
                <span style={{ fontSize: 11, color: "var(--text-muted)", borderLeft: "2px solid var(--border)", paddingLeft: 6 }}>
                  {t("polyforge.messages.quote")}: {original ? <span>{original.text}</span> : message.reply_to}
                </span>
              )}
              <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span style={{ fontSize: 10, color: "var(--text-dim)" }}>{formatTimestamp(message.created_at)}</span>
                {message.mine ? (
                  <button type="button" onClick={(event) => { event.stopPropagation(); quoteOwnMessage(message.text); }}
                    style={{ background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 11, padding: 0 }}>
                    {t("polyforge.messages.quoteMessage")}
                  </button>
                ) : (
                  <button type="button" onClick={(event) => { event.stopPropagation(); flushSync(() => setReplyTo(message.id)); }}
                    style={{ background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 11, padding: 0 }}>
                    {t("polyforge.messages.reply")}
                  </button>
                )}
                {!message.mine && (
                  <button type="button" onClick={(event) => { event.stopPropagation(); void analyze(message.id); }}
                    disabled={Boolean(analyzing[message.id])}
                    aria-busy={Boolean(analyzing[message.id])}
                    style={{
                      display: "inline-flex", alignItems: "center", gap: 4, padding: "1px 6px",
                      background: "none", border: "1px solid var(--border)", borderRadius: 4,
                      color: "var(--text)", fontSize: 11, lineHeight: "16px",
                      cursor: analyzing[message.id] ? "default" : "pointer", opacity: analyzing[message.id] ? 0.6 : 1,
                    }}>
                    <svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                      strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
                      className={analyzing[message.id] ? "animate-spin" : undefined}>
                      {analyzing[message.id]
                        ? <path d="M21 12a9 9 0 1 1-6.2-8.56" />
                        : <><circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" /></>}
                    </svg>
                    {analyzing[message.id] ? t("polyforge.messages.analyzing") : t("polyforge.messages.analyze")}
                  </button>
                )}
                <button type="button" onClick={(event) => { event.stopPropagation(); void promptMessage(message.id); }}
                  disabled={Boolean(prompting[message.id])}
                  aria-busy={Boolean(prompting[message.id])}
                  aria-label={prompting[message.id] ? t("polyforge.messages.prompting") : t("polyforge.messages.prompt")}
                  title={prompting[message.id] ? t("polyforge.messages.prompting") : t("polyforge.messages.prompt")}
                  style={{
                    display: "inline-flex", alignItems: "center", gap: 4, padding: "1px 6px",
                    background: "none", border: "1px solid var(--border)", borderRadius: 4,
                    color: "var(--text)", fontSize: 11, lineHeight: "16px", fontWeight: 600,
                    cursor: prompting[message.id] ? "default" : "pointer", opacity: prompting[message.id] ? 0.6 : 1,
                  }}>
                  {prompting[message.id] && (
                    <svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                      strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="animate-spin">
                      <path d="M21 12a9 9 0 1 1-6.2-8.56" />
                    </svg>
                  )}
                  @
                </button>
              </span>
              {promptOutcomes[message.id]?.error && (
                <div role="alert" style={{ fontSize: 11, color: "#dc2626", overflowWrap: "anywhere" }}>
                  {promptOutcomes[message.id].error?.upgrade
                    ? t("polyforge.messages.promptUpgrade", { version: POLYFORGE_PROMPT_MINIMUM_PF_VERSION })
                    : promptOutcomes[message.id].error?.text}
                </div>
              )}
              {promptOutcomes[message.id]?.shown && (
                <pre aria-label={t("polyforge.messages.promptShown")}
                  style={{
                    margin: 0, padding: "4px 6px", border: "1px solid var(--border)", borderRadius: 4,
                    background: "var(--bg)", color: "var(--text)", fontSize: 11, fontFamily: "inherit",
                    whiteSpace: "pre-wrap", overflowWrap: "anywhere", userSelect: "text", cursor: "text",
                  }}>
                  {promptOutcomes[message.id].shown}
                </pre>
              )}
              {promptOutcomes[message.id]?.feedback && (
                <div role="status" style={{ fontSize: 11, color: "var(--text-muted)", overflowWrap: "anywhere" }}>
                  {promptOutcomes[message.id].feedback}
                </div>
              )}
              {!message.mine && analysisErrors[message.id] && (
                <div role="alert" style={{ fontSize: 11, color: "#dc2626", overflowWrap: "anywhere" }}>
                  {analysisErrors[message.id].upgrade
                    ? t("polyforge.messages.analysisUpgrade", { version: POLYFORGE_ANALYZE_MINIMUM_PF_VERSION })
                    : analysisErrors[message.id].text}
                </div>
              )}
              {!message.mine && analyses[message.id] && (
                <PolyforgeAnalysisCard
                  analysis={analyses[message.id]}
                  expanded={!collapsedCards[message.id]}
                  onToggle={() => toggleCard(message.id)}
                  onFileTicket={() => setTicketFor(message.id)}
                  onReply={() => replyFromAnalysis(message.id, analyses[message.id])}
                  onOpenTask={onOpenTask}
                />
              )}
              {!message.mine && ticketNotices[message.id] && (
                <div role="status" style={{ fontSize: 11, color: "var(--text-muted)", overflowWrap: "anywhere" }}>
                  {t("polyforge.messages.ticketCreated", { slug: ticketNotices[message.id] })}
                </div>
              )}
            </div>
          );
        })}
      </div>

      {composer}
      {ticketMessage && ticketAnalysis && (
        <PolyforgeFileTicketDialog
          key={ticketMessage.id}
          analysis={ticketAnalysis}
          message={ticketMessage}
          projects={groups.map((group) => group.project)}
          onClose={() => setTicketFor(null)}
          onCreated={(slug) => {
            setTicketNotices((previous) => ({ ...previous, [ticketMessage.id]: slug }));
            setTicketFor(null);
          }}
        />
      )}
    </div>
  );

  // row-reverse keeps the column flush right while it stays first in DOM
  // order (the layout's own oracle).
  return (
    <div ref={messagesPanelRef} style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "row-reverse" }}>
      {listColumn}
      {chatPane}
    </div>
  );
}
