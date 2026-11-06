/**
 * Client-safe pure model and helpers for the Messages tab:
 * response types, the page merges, the one acknowledgment decision, the
 * anchor split, the flat direct contact list, the project
 * groups and the structured message analysis (:
 * the closed vocabularies, the one wire sanitizer, the primary action). No
 * Node import lives here, so both the server module and the client
 * hook/panel can use it.
 */

import { isPolyforgeTaskId } from "./polyforge-tasks-pure";

export interface PolyforgeConversation {
  id: string;
  kind: string;
  /** The peer user id — the send address, never rendered. */
  peer?: string;
  /** The peer's display name; absent/empty renders as the unknown-user
   *  placeholder, never an id fallback. */
  peer_name?: string;
  last_seq: number;
  unread: number;
  created_at: string;
}

/** A configured contact: one human the caller can message,
 *  from `pf msg contacts`. `id` is the send address only — never rendered,
 *  titled or labelled; `display_name` may be "" (the unknown-user
 *  placeholder renders, never the id). */
export interface PolyforgeContact {
  id: string;
  display_name: string;
}

/** One `cli.toml`-mapped project's group: the project name
 *  and its human contacts (id + display name only — the contact set's own
 *  projection; roles, machines and the caller never enter). `error` is that
 *  project's own failed members read; the other groups are unaffected. */
export interface PolyforgeProjectGroup {
  project: string;
  contacts: PolyforgeContact[];
  error?: string;
}

/** One project's members read, as the server joins it: the member user ids
 *  (roles already dropped), or the read's own failure text. */
export type PolyforgeProjectMembersRead = { ids: string[] } | { error: string };

export interface PolyforgeMessage {
  id: string;
  seq: number;
  text: string;
  about_wi?: string;
  reply_to?: string;
  /** The conversation the message lives in — a send's answer carries it so
   *  the resulting conversation can be opened without re-reading the list. */
  conversation_id?: string;
  created_at: string;
  mine: boolean;
}

export interface PolyforgeMessagePage {
  messages: PolyforgeMessage[];
  last_seq: number;
  /** True when the page was full, so an older page may still exist. */
  has_older?: boolean;
}

const CONVERSATION_TOKEN = /^conv_[A-Za-z0-9_]+$/;
const MESSAGE_TOKEN = /^msg_[A-Za-z0-9_]+$/;
const USER_TOKEN = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/;

export function isPolyforgeConversationId(value: unknown): value is string {
  return typeof value === "string" && CONVERSATION_TOKEN.test(value);
}

export function isPolyforgeMessageId(value: unknown): value is string {
  return typeof value === "string" && MESSAGE_TOKEN.test(value);
}

export function isPolyforgeRecipient(value: unknown): value is string {
  return typeof value === "string" && USER_TOKEN.test(value);
}

export function isPositiveSeq(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

/** Prepend an older page, deduping by message id and keeping ascending seq
 *  order. No gap is invented and no duplicate survives. Pure. */
export function mergeOlderPage(
  existing: PolyforgeMessage[],
  older: PolyforgeMessage[],
): PolyforgeMessage[] {
  const seen = new Set(existing.map((message) => message.id));
  const merged = [...older.filter((message) => !seen.has(message.id)), ...existing];
  return merged.sort((a, b) => a.seq - b.seq);
}

/** Merge a newest-page refresh into the loaded history: replace/insert by id,
 *  keep already-loaded older pages, keep ascending order. Pure. */
export function mergeNewestPage(
  existing: PolyforgeMessage[],
  newest: PolyforgeMessage[],
): PolyforgeMessage[] {
  const byId = new Map(existing.map((message) => [message.id, message]));
  for (const message of newest) byId.set(message.id, message);
  return [...byId.values()].sort((a, b) => a.seq - b.seq);
}

/** The acknowledgment decision (one place, pure): auto-ack only while the
 *  panel is open, Messages is the active tab, a conversation is open and no
 *  local job is blocked; always the NEWEST DISPLAYED sequence, never one the
 *  user has not seen; never a sequence already acknowledged. An explicit user
 *  gesture (message click / scroll-to-bottom) bypasses visibility and the
 *  blocked-job suppression. */
export interface AckDecisionInput {
  panelOpen: boolean;
  /** The dock's top-level tab (Work | Memory | Messages, the
   *  former Jobs and Tasks tabs now Work's sub-tabs): only "messages" may
   *  auto-ack — the union keeps the hook's tab state passing through
   *  unchanged. */
  activeTab: "memory" | "messages" | "work";
  conversationId: string | null;
  blocked: boolean;
  newestDisplayedSeq: number;
  lastAckedSeq: number;
  explicit?: boolean;
}

export function ackDecision(input: AckDecisionInput): number | null {
  if (!input.conversationId) return null;
  if (!isPositiveSeq(input.newestDisplayedSeq)) return null;
  if (input.newestDisplayedSeq <= input.lastAckedSeq) return null;
  if (!input.explicit) {
    if (!input.panelOpen || input.activeTab !== "messages" || input.blocked) return null;
  }
  return input.newestDisplayedSeq;
}

/** Split a work-item anchor into the clickable work-item part and the rest
 *  of the reference. Only the work-item prefix reaches the Jobs watch read;
 *  the remainder renders as plain text and is never fetched. Pure. */
export function splitAnchor(anchor: string): { ref: string; rest: string } {
  const value = anchor.trim();
  const match = /^(wi_[A-Za-z0-9]+|[A-Za-z0-9][A-Za-z0-9_-]*#\d+|[A-Za-z0-9][A-Za-z0-9_-]*-\d+)/.exec(value);
  if (!match) return { ref: "", rest: value };
  const ref = match[1];
  const rest = value.slice(ref.length);
  if (rest && !rest.startsWith("/")) return { ref: "", rest: value };
  return { ref, rest };
}

// ---- inline @project#number work-item tokens ----

/** The one token definition: the `@` sits at the text/line start or after a
 *  character that is not a word character, then a project name (a leading
 *  alphanumeric, then alphanumerics, `_` or `-`) and a `#`-prefixed number; the
 *  character after the number (if any) is not a word character. Only this
 *  `#` form is recognised — no `wi_…`, `project-N` or `/path` token. */
const WORK_ITEM_TOKEN = /(^|[^A-Za-z0-9_])@([A-Za-z0-9][A-Za-z0-9_-]*)#(\d+)(?![A-Za-z0-9_])/g;

export interface PolyforgeWorkItemToken {
  /** The token as typed, including its leading `@`. */
  text: string;
  /** `project#number` — the binding value and the onOpenAnchor ref. */
  ref: string;
  project: string;
  number: string;
  /** The `@`'s character offset. */
  start: number;
  /** The offset just past the number. */
  end: number;
  /** True when the token sits on a line whose first character is `>`. */
  quoteLine: boolean;
}

/** Every syntactically valid token in `text`, in order. One definition feeds
 *  both the send binding and the bubble rendering. Pure. */
export function findWorkItemTokens(text: string): PolyforgeWorkItemToken[] {
  const pattern = new RegExp(WORK_ITEM_TOKEN.source, "g");
  const tokens: PolyforgeWorkItemToken[] = [];
  for (const match of text.matchAll(pattern)) {
    const start = (match.index ?? 0) + (match[1]?.length ?? 0);
    const project = match[2];
    const number = match[3];
    const ref = `${project}#${number}`;
    const token = `@${ref}`;
    const lineStart = text.lastIndexOf("\n", start - 1) + 1;
    tokens.push({
      text: token,
      ref,
      project,
      number,
      start,
      end: start + token.length,
      quoteLine: text[lineStart] === ">",
    });
  }
  return tokens;
}

/** A bubble body split into plain runs and clickable token runs (the `@` stays
 *  visible). Rendering does no project-list check. Pure. */
export interface PolyforgeTextSegment {
  text: string;
  /** `project#number` when this run is a token; absent for plain text. */
  ref?: string;
}

export function splitWorkItemLinks(text: string): PolyforgeTextSegment[] {
  const tokens = findWorkItemTokens(text);
  if (tokens.length === 0) return [{ text }];
  const segments: PolyforgeTextSegment[] = [];
  let cursor = 0;
  for (const token of tokens) {
    if (token.start > cursor) segments.push({ text: text.slice(cursor, token.start) });
    segments.push({ text: token.text, ref: token.ref });
    cursor = token.end;
  }
  if (cursor < text.length) segments.push({ text: text.slice(cursor) });
  return segments;
}

/** The send binding: the first token outside a `>` quote line whose project
 *  is exactly (case-sensitive) one of `projects`; `project#number`, else null.
 *  No work-item existence check and no I/O. Pure. */
export function firstWorkItemBinding(text: string, projects: readonly string[]): string | null {
  const known = new Set(projects);
  for (const token of findWorkItemTokens(text)) {
    if (token.quoteLine) continue;
    if (known.has(token.project)) return token.ref;
  }
  return null;
}

/** The quote block one own-message 【引用】 inserts: every line prefixed with
 *  `> ` (an empty line becomes `>`), then one blank line. Pure. */
export function quoteBlock(text: string): string {
  return `${text.split("\n").map((line) => (line ? `> ${line}` : ">")).join("\n")}\n\n`;
}

/** Append a quote block to a draft: an empty draft becomes the block; a
 *  non-empty one keeps its text with trailing newlines trimmed, then a blank
 *  line, then the block. Never overwrites. Pure. */
export function appendQuoteBlock(draft: string, text: string): string {
  const block = quoteBlock(text);
  return draft === "" ? block : `${draft.replace(/\n+$/, "")}\n\n${block}`;
}

// ---- the Slack-like layout's identity helpers ----

/** The conversation's display name (the peer's), trimmed; "" when absent,
 *  empty or blank. The caller renders the unknown-user placeholder for "" —
 *  never a raw id fallback. Pure. */
export function conversationName(conversation: PolyforgeConversation): string {
  return (conversation.peer_name ?? "").trim();
}

/** The avatar initial: the first code point of the display name, upper-cased
 *  (code-point safe, not UTF-16 unit); "?" when there is no name. Pure. */
export function avatarInitial(name: string): string {
  const first = [...name.trim()][0];
  return first ? first.toUpperCase() : "?";
}

/** A deterministic per-user colour (FNV-1a hash of the stable peer id →
 *  hue). The result is a CSS colour string only — the seed is never rendered
 *  as text. Pure. */
export function avatarColor(seed: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < seed.length; i++) {
    hash ^= seed.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return `hsl(${hash % 360}, 45%, 45%)`;
}

/** Split the list into the direct (单聊) and group (群聊 — any other kind)
 *  sections, preserving order. Pure. */
export function groupConversations(
  list: PolyforgeConversation[],
): { direct: PolyforgeConversation[]; groups: PolyforgeConversation[] } {
  const direct: PolyforgeConversation[] = [];
  const groups: PolyforgeConversation[] = [];
  for (const conversation of list) {
    if (conversation.kind === "direct") direct.push(conversation);
    else groups.push(conversation);
  }
  return { direct, groups };
}

/** The contact order every list uses: display name ascending, blank names
 *  last, the id only as a deterministic tie-break (never rendered). Pure. */
export function compareContactsByName(a: PolyforgeContact, b: PolyforgeContact): number {
  const left = a.display_name.trim();
  const right = b.display_name.trim();
  if (!left !== !right) return left ? -1 : 1;
  return left.localeCompare(right) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
}

/** One row of the flat direct (单聊) area: an existing direct conversation,
 *  or a silent contact — a configured contact with no direct conversation. */
export type PolyforgeDirectRow =
  | { kind: "conversation"; conversation: PolyforgeConversation }
  | { kind: "contact"; contact: PolyforgeContact };

/** The flat direct area: the direct conversation rows in
 *  their current order (unread intact), then the silent contacts sorted by
 *  display name (blank names last, id as the tie-break), one row per peer —
 *  a contact whose id is already a direct conversation's peer, or repeats an
 *  earlier contact, adds no row. Group conversations are not part of it.
 *  Pure. */
export function directRows(
  conversations: PolyforgeConversation[],
  contacts: PolyforgeContact[],
): PolyforgeDirectRow[] {
  const { direct } = groupConversations(conversations);
  const seen = new Set<string>();
  for (const conversation of direct) if (conversation.peer) seen.add(conversation.peer);
  const silent: PolyforgeContact[] = [];
  for (const contact of contacts) {
    if (seen.has(contact.id)) continue;
    seen.add(contact.id);
    silent.push(contact);
  }
  silent.sort(compareContactsByName);
  return [
    ...direct.map((conversation) => ({ kind: "conversation" as const, conversation })),
    ...silent.map((contact) => ({ kind: "contact" as const, contact })),
  ];
}

/** The direct conversation whose peer is this contact: the
 *  one a silent contact's peer created with a first message, else null. A
 *  group conversation never matches. Pure. */
export function conversationForContact(
  conversations: PolyforgeConversation[],
  contactId: string,
): PolyforgeConversation | null {
  return conversations.find((conversation) => conversation.kind === "direct" && conversation.peer === contactId) ?? null;
}

/** A mapping's project names as groups use them: trimmed, deduplicated,
 *  ascending by name. Pure. */
export function normalizeProjectNames(names: string[]): string[] {
  const unique = new Set<string>();
  for (const name of names) {
    const trimmed = name.trim();
    if (trimmed) unique.add(trimmed);
  }
  return [...unique].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
}

/** The project groups: one per mapped project in name order;
 *  each project's member ids joined by id to the contact set — an id absent
 *  from it (a machine, the caller, a non-human) adds no row, an id repeated
 *  adds one — sorted like every contact list. A failed members read keeps
 *  its group with no contacts and its own error. Pure. */
export function buildProjectGroups(
  projects: string[],
  memberReads: Record<string, PolyforgeProjectMembersRead | undefined>,
  contacts: PolyforgeContact[],
): PolyforgeProjectGroup[] {
  const byId = new Map<string, PolyforgeContact>();
  for (const contact of contacts) if (!byId.has(contact.id)) byId.set(contact.id, contact);
  return normalizeProjectNames(projects).map((project) => {
    const read = memberReads[project];
    if (!read || "error" in read) {
      return { project, contacts: [], error: read?.error || "the project members read failed" };
    }
    const seen = new Set<string>();
    const rows: PolyforgeContact[] = [];
    for (const id of read.ids) {
      const contact = byId.get(id);
      if (!contact || seen.has(id)) continue;
      seen.add(id);
      rows.push({ id: contact.id, display_name: contact.display_name });
    }
    rows.sort(compareContactsByName);
    return { project, contacts: rows };
  });
}

/** The silent contacts the flat direct area still lists once project groups
 *  render: a contact shown in any group lives there, not
 *  twice; with no groups every contact stays. Pure. */
export function contactsOutsideGroups(
  contacts: PolyforgeContact[],
  groups: PolyforgeProjectGroup[],
): PolyforgeContact[] {
  const grouped = new Set<string>();
  for (const group of groups) for (const contact of group.contacts) grouped.add(contact.id);
  return contacts.filter((contact) => !grouped.has(contact.id));
}

// ---- structured message analysis ----

/** The analysis floor: the pf release whose `pf msg analyze` runs one task
 *  and answers the structured result. Separate from the Messages floor
 *  (2.20.12): below it only the analysis entry is refused. */
export const POLYFORGE_ANALYZE_MINIMUM_PF_VERSION = "2.20.18";

/** The closed analysis type vocabulary (pf's own set, in its order). */
export const POLYFORGE_ANALYSIS_TYPES = [
  "requirement", "bug", "ruling", "discussion", "clarification", "outreach", "other",
] as const;
export type PolyforgeAnalysisType = typeof POLYFORGE_ANALYSIS_TYPES[number];

/** The closed suggested-action vocabulary. */
export const POLYFORGE_SUGGESTED_ACTIONS = ["file_ticket", "reply", "note_only"] as const;
export type PolyforgeSuggestedAction = typeof POLYFORGE_SUGGESTED_ACTIONS[number];

/** Display bounds applied at the wire boundary (the daemon stays literal): a
 *  summary over 2000 code points is cut with a trailing `…`, and a reply
 *  draft over pf's own 4096-byte message bound is dropped (it could not be
 *  sent anyway). */
export const POLYFORGE_ANALYSIS_SUMMARY_MAX_CODE_POINTS = 2000;
export const POLYFORGE_REPLY_DRAFT_MAX_BYTES = 4096;

/** One structured analysis, exactly as the panel may hold it (page memory
 *  only). `reply_draft` exists only beside a `reply` suggestion. */
export interface PolyforgeAnalysis {
  message_id: string;
  task_id: string;
  project: string;
  type: PolyforgeAnalysisType;
  summary: string;
  suggested_actions: PolyforgeSuggestedAction[];
  model: string;
  duration: string;
  reply_draft?: string;
}

export function isPolyforgeAnalysisType(value: unknown): value is PolyforgeAnalysisType {
  return typeof value === "string" && (POLYFORGE_ANALYSIS_TYPES as readonly string[]).includes(value);
}

export function isPolyforgeSuggestedAction(value: unknown): value is PolyforgeSuggestedAction {
  return typeof value === "string" && (POLYFORGE_SUGGESTED_ACTIONS as readonly string[]).includes(value);
}

function trimmedString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function utf8Bytes(text: string): number {
  return new TextEncoder().encode(text).length;
}

/** The one analysis wire sanitizer: a message id, a task id, a closed-set
 *  type and a non-blank summary are required (anything else is not the
 *  structured shape: null); unknown and duplicate actions are dropped; a
 *  draft survives only beside `reply`, non-blank and within the send bound.
 *  Every other field — the retired `conclusion` included — is dropped. */
export function parsePolyforgeAnalysis(raw: unknown): PolyforgeAnalysis | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const body = raw as Record<string, unknown>;
  const messageId = trimmedString(body.message_id);
  const taskId = trimmedString(body.task_id);
  const summaryText = trimmedString(body.summary);
  if (!MESSAGE_TOKEN.test(messageId) || !isPolyforgeTaskId(taskId) || !isPolyforgeAnalysisType(body.type) || !summaryText) {
    return null;
  }
  const points = Array.from(summaryText);
  const summary = points.length > POLYFORGE_ANALYSIS_SUMMARY_MAX_CODE_POINTS
    ? `${points.slice(0, POLYFORGE_ANALYSIS_SUMMARY_MAX_CODE_POINTS - 1).join("")}…`
    : summaryText;
  const actions: PolyforgeSuggestedAction[] = [];
  if (Array.isArray(body.suggested_actions)) {
    for (const action of body.suggested_actions) {
      if (isPolyforgeSuggestedAction(action) && !actions.includes(action)) actions.push(action);
    }
  }
  const analysis: PolyforgeAnalysis = {
    message_id: messageId,
    task_id: taskId,
    project: trimmedString(body.project),
    type: body.type,
    summary,
    suggested_actions: actions,
    model: trimmedString(body.model),
    duration: trimmedString(body.duration),
  };
  const draft = trimmedString(body.reply_draft);
  if (actions.includes("reply") && draft && utf8Bytes(draft) <= POLYFORGE_REPLY_DRAFT_MAX_BYTES) {
    analysis.reply_draft = draft;
  }
  return analysis;
}

/** The suggested primary action: `reply` when suggested, else
 *  `file_ticket`, else none (`note_only` or nothing leaves both actions
 *  secondary). */
export function primarySuggestedAction(actions: readonly PolyforgeSuggestedAction[]): "reply" | "file_ticket" | null {
  if (actions.includes("reply")) return "reply";
  if (actions.includes("file_ticket")) return "file_ticket";
  return null;
}

// ---- 「@」: a message turned into an execution prompt ----

/** The prompt floor: the first pf release with `pf msg prompt` (the next
 *  patch after the latest tag when this was written; the polyforge
 *  `docs/upgrade.md` section names the same version). Below it only the
 *  「@」 action's route refuses; the action stays visible. */
export const POLYFORGE_PROMPT_MINIMUM_PF_VERSION = "2.20.20";

/** pf's message-to-prompt answer: exactly these four fields, the same names
 *  the daemon answers (one fixture shape pins both sides). */
export interface PolyforgePrompt {
  prompt: string;
  task_id: string;
  model: string;
  duration: string;
}

/** The one prompt wire sanitizer: a non-blank prompt and a task id are
 *  required, the model and duration are strings, and any other shape is
 *  refused (null) — never translated. Only the four fields survive; any
 *  extra field is dropped. The prompt keeps its inner text exactly, trimmed
 *  at its ends only. */
export function parsePolyforgePrompt(raw: unknown): PolyforgePrompt | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const body = raw as Record<string, unknown>;
  const prompt = trimmedString(body.prompt);
  const taskId = trimmedString(body.task_id);
  if (!prompt || !isPolyforgeTaskId(taskId) || typeof body.model !== "string" || typeof body.duration !== "string") {
    return null;
  }
  return { prompt, task_id: taskId, model: body.model.trim(), duration: body.duration.trim() };
}

// ---- 【建票】: the registration-ticket dialog ----

/** pf's goal bound (`api.MaxGoalRunes`) and pi-web's content bound. */
export const FILE_TICKET_GOAL_MAX_RUNES = 500;
export const FILE_TICKET_CONTENT_MAX_BYTES = 65536;
const TICKET_QUOTE_MAX_CODE_POINTS = 200;

/** The summary's first sentence (its suggested next step): up to and
 *  including the first sentence end (`.`/`!`/`?` before whitespace or the
 *  end, a CJK `。！？`, or a line break), trimmed, cut to the goal bound. */
export function firstSentence(text: string): string {
  const points = Array.from(text.trim());
  let end = points.length;
  for (let index = 0; index < points.length; index += 1) {
    const point = points[index];
    if (point === "\n") { end = index; break; }
    if (point === "。" || point === "！" || point === "？") { end = index + 1; break; }
    if ((point === "." || point === "!" || point === "?") && (index + 1 === points.length || /\s/.test(points[index + 1]))) {
      end = index + 1;
      break;
    }
  }
  return points.slice(0, Math.min(end, FILE_TICKET_GOAL_MAX_RUNES)).join("").trim();
}

/** The dialog's prefill: the goal from the summary's first sentence, the
 *  project defaulting to the analysis's own (else the first mapped one),
 *  and the content = the source message reference (id + bounded quote)
 *  followed by the summary. Every field stays editable. */
export function fileTicketDialogDefaults(
  analysis: Pick<PolyforgeAnalysis, "summary" | "project">,
  message: Pick<PolyforgeMessage, "id" | "text">,
  mappedProjects: string[],
): { goal: string; project: string; content: string } {
  const quotePoints = Array.from(message.text.replace(/\s+/g, " ").trim());
  const quote = quotePoints.length > TICKET_QUOTE_MAX_CODE_POINTS
    ? `${quotePoints.slice(0, TICKET_QUOTE_MAX_CODE_POINTS - 1).join("")}…`
    : quotePoints.join("");
  return {
    goal: firstSentence(analysis.summary),
    project: mappedProjects.includes(analysis.project) ? analysis.project : (mappedProjects[0] ?? ""),
    content: `消息 ${message.id}: "${quote}"\n\n${analysis.summary}`,
  };
}
