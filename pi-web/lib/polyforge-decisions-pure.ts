import type { PolyforgeJob } from "./polyforge-status";
import { isPolyforgeTaskId } from "./polyforge-tasks-pure";

/**
 * Client-safe vocabulary of the Polyforge panel's decision feed:
 * the daemon's machine-level feed of every human decision
 * (`GET /decisions`, printed verbatim by `pf --json watch decisions`), its one
 * sanitizer, the Polyforge button's attention items and click walk, the
 * feed-only attention cards of dropped work items, and the jump
 * target of an entry. No I/O here; the server read is
 * `lib/polyforge-decisions.ts`.
 */

/** The classes the daemon writes (the contract's table); each has a
 *  `polyforge.decisions.class.<class>` label. Any other class is shown by
 *  its wire `phrase`. */
export const DECISION_CLASSES: readonly string[] = [
  "needs_input",
  "blocked",
  "failed",
  "completed",
  "canceled",
  "paused",
  "terminal",
  "step_dead",
  "candidates_exhausted",
  "carrier_push_failed",
  "submit_rejected",
  "submit_dropped",
  "daemon_restarted",
  "superseded",
  "capped",
  "interrupted",
];

export function isKnownDecisionClass(value: string): boolean {
  return DECISION_CLASSES.includes(value);
}

export type PolyforgeDecisionKind = "work_item" | "task";

/** One feed entry, as the route answers it (the wire names; empty fields absent). */
export interface PolyforgeDecision {
  id: number;
  at: string;
  kind: PolyforgeDecisionKind;
  profile?: string;
  wi?: string;
  slug?: string;
  task_id?: string;
  class: string;
  source?: string;
  phrase?: string;
  question_id?: string;
  repo?: string;
  /** A task's terminal summary, verbatim (bounded). */
  text?: string;
  attention: boolean;
  resolved: boolean;
  dismissed: boolean;
  /** The owner replied to this entry's question: the daemon
   *  marks it `attention:false`, still unresolved until the step hands off. */
  replied: boolean;
  /** The reply's own stamp, when replied. */
  replied_at?: string;
}

export interface PolyforgeDecisionFeed {
  /** Newest first, as the daemon orders them. */
  decisions: PolyforgeDecision[];
  /** The number of open entries. */
  attention: number;
}

export const DECISION_PHRASE_MAX = 200;
export const DECISION_TEXT_MAX = 16_000;
const AT_MAX = 64;
const SLUG_MAX = 120;
const REPO_MAX = 200;

const TOKEN_PATTERN = /^[a-z][a-z0-9_]{0,63}$/;
const WI_PATTERN = /^wi_[A-Za-z0-9]{1,64}$/;
const PROFILE_PATTERN = /^[a-z0-9][a-z0-9-]{0,31}$/;
const QUESTION_PATTERN = /^[A-Za-z0-9._:#-]{1,80}$/;
const SLUG_PATTERN = /^[^\s\u0000-\u001f]+$/;

function boundedText(value: unknown, max: number): string | undefined {
  if (typeof value !== "string" || !value.trim()) return undefined;
  return value.length > max ? value.slice(0, max) : value;
}

function matching(value: unknown, pattern: RegExp): string | undefined {
  return typeof value === "string" && pattern.test(value) ? value : undefined;
}

/** One wire entry → a decision, or null when it is malformed (an id that is
 *  no positive integer, an unknown kind, a class that is no token, a missing
 *  attention / resolved / dismissed boolean). Optional fields degrade one by
 *  one; `replied` is true only for a boolean true. Pure, never throws. */
export function sanitizePolyforgeDecision(value: unknown): PolyforgeDecision | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const v = value as Record<string, unknown>;
  if (typeof v.id !== "number" || !Number.isSafeInteger(v.id) || v.id <= 0) return null;
  if (v.kind !== "work_item" && v.kind !== "task") return null;
  const cls = matching(v.class, TOKEN_PATTERN);
  if (!cls) return null;
  if (typeof v.attention !== "boolean" || typeof v.resolved !== "boolean" || typeof v.dismissed !== "boolean") return null;
  const profile = matching(v.profile, PROFILE_PATTERN);
  const wi = matching(v.wi, WI_PATTERN);
  const slug = typeof v.slug === "string" && v.slug.length <= SLUG_MAX ? matching(v.slug, SLUG_PATTERN) : undefined;
  // the daemon's task ids (task.NewID): `task_` + 16 lowercase hex — the
  // Tasks tab's own validator, one fact in one place
  const taskId = isPolyforgeTaskId(v.task_id) ? v.task_id : undefined;
  const source = matching(v.source, TOKEN_PATTERN);
  const phrase = boundedText(v.phrase, DECISION_PHRASE_MAX);
  const questionId = matching(v.question_id, QUESTION_PATTERN);
  const repo = boundedText(v.repo, REPO_MAX);
  const text = boundedText(v.text, DECISION_TEXT_MAX);
  const repliedAt = boundedText(v.replied_at, AT_MAX);
  // Key order is the wire's, so a sanitized entry reads like the daemon's.
  return {
    id: v.id,
    at: boundedText(v.at, AT_MAX) ?? "",
    kind: v.kind,
    ...(profile ? { profile } : {}),
    ...(wi ? { wi } : {}),
    ...(slug ? { slug } : {}),
    ...(taskId ? { task_id: taskId } : {}),
    class: cls,
    ...(source ? { source } : {}),
    ...(phrase ? { phrase } : {}),
    ...(questionId ? { question_id: questionId } : {}),
    ...(repo ? { repo } : {}),
    ...(text ? { text } : {}),
    attention: v.attention,
    resolved: v.resolved,
    dismissed: v.dismissed,
    replied: v.replied === true,
    ...(repliedAt ? { replied_at: repliedAt } : {}),
  };
}

/** The daemon's `{decisions, attention}` → the panel's feed, or null when the
 *  document is no feed. Malformed entries are dropped; the wire's order
 *  (newest first) is kept; `attention` is the wire count when it is a
 *  non-negative integer, else the count of open entries. Pure. */
export function sanitizePolyforgeDecisionFeed(value: unknown): PolyforgeDecisionFeed | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const v = value as { decisions?: unknown; attention?: unknown };
  if (!Array.isArray(v.decisions)) return null;
  const decisions = v.decisions.flatMap((entry) => {
    const decision = sanitizePolyforgeDecision(entry);
    return decision ? [decision] : [];
  });
  const attention = typeof v.attention === "number" && Number.isSafeInteger(v.attention) && v.attention >= 0
    ? v.attention
    : decisions.filter(isOpenDecision).length;
  return { decisions, attention };
}

/** OPEN = needs a human: `attention && !resolved && !dismissed`. */
export function isOpenDecision(decision: PolyforgeDecision): boolean {
  return decision.attention && !decision.resolved && !decision.dismissed;
}

export type PolyforgeDecisionState = "open" | "replied" | "resolved" | "dismissed";

/** The state an entry shows: resolved > dismissed > replied > open — the
 *  daemon's resolution and the owner's explicit dismiss outrank a reply
 *  (answered, waiting for the step; muted like the others). */
export function decisionState(decision: PolyforgeDecision): PolyforgeDecisionState {
  if (decision.resolved) return "resolved";
  if (decision.dismissed) return "dismissed";
  if (decision.replied) return "replied";
  return "open";
}

function profileOf(value: { profile?: string }): string {
  return value.profile || "default";
}

/** What an entry names: `task <id>`, else the work item's slug (else its id),
 *  prefixed `<profile>:` for a non-default Cell; "" when it names none. */
export function decisionTargetLabel(decision: Pick<PolyforgeDecision, "kind" | "task_id" | "wi" | "slug" | "profile">): string {
  if (decision.kind === "task") return decision.task_id ? `task ${decision.task_id}` : "";
  const name = decision.slug || decision.wi;
  if (!name) return "";
  return profileOf(decision) === "default" ? name : `${profileOf(decision)}:${name}`;
}

/** The source an entry shows beside its class: only when it differs (e.g.
 *  `failed_gate` for `failed`); a task entry's source is its kind, already
 *  named by its target, so it shows none. */
export function decisionSourceShown(decision: Pick<PolyforgeDecision, "kind" | "class" | "source">): string | undefined {
  if (decision.kind === "task" || !decision.source || decision.source === decision.class) return undefined;
  return decision.source;
}

export type PolyforgeDecisionTarget =
  | { kind: "job"; ref: string; profile?: string }
  | { kind: "task"; id: string };

function jobMatches(job: Pick<PolyforgeJob, "wi" | "slug" | "profile">, decision: Pick<PolyforgeDecision, "wi" | "slug" | "profile">): boolean {
  const named = (decision.wi && job.wi === decision.wi) || (!decision.wi && decision.slug && job.slug === decision.slug);
  return Boolean(named) && profileOf(job) === profileOf(decision);
}

/** Where an entry leads (owner ruling 2026-11-09: every
 *  interaction belongs to its work item): a work item → its job detail in
 *  Work › Jobs — the live job of the last status read (its Cell), else the
 *  work item by id in the entry's own Cell (a job the daemon dropped still
 *  has a detail: the watch snapshot and the event-log progress); a task →
 *  Work › Tasks; null when it names nothing openable. */
export function decisionJumpTarget(decision: PolyforgeDecision, jobs: readonly PolyforgeJob[]): PolyforgeDecisionTarget | null {
  if (decision.kind === "task") return decision.task_id ? { kind: "task", id: decision.task_id } : null;
  const job = jobs.find((candidate) => jobMatches(candidate, decision));
  if (job) return { kind: "job", ref: job.wi || job.slug, ...(job.profile ? { profile: job.profile } : {}) };
  return decision.wi ? { kind: "job", ref: decision.wi, profile: profileOf(decision) } : null;
}

/** The entries of one work item, newest first: its id (else its slug, for an
 *  entry without an id) in its own Cell. */
export function itemDecisions(decisions: readonly PolyforgeDecision[], item: { wi?: string; slug?: string; profile?: string }): PolyforgeDecision[] {
  return decisions.filter((decision) => decision.kind === "work_item"
    && jobMatches({ wi: item.wi ?? "", slug: item.slug ?? "", profile: item.profile }, decision));
}

/** The CLI command that resumes a work item: the wi id (a slug's `#` is not
 *  shell-safe everywhere), `--profile` for a named Cell — the composer's
 *  copyable command on a park a resume releases. */
export function resumeCommand(wi: string, profile?: string): string {
  const cell = profile && profile !== "default" ? `--profile ${profile} ` : "";
  return `pf ${cell}resume ${wi}`;
}

/** The `pf cancel` command of a work item in its Cell (a block no resume
 *  releases: cancel it and file it again). */
export function cancelCommand(wi: string, profile?: string): string {
  const cell = profile && profile !== "default" ? `--profile ${profile} ` : "";
  return `pf ${cell}cancel ${wi}`;
}

/** The blocked kinds the panel knows and labels
 *  (`polyforge.blocked.kind.<kind>`): the kinds the daemon's watch snapshot
 *  can carry — `watchBlocked`'s literals and the driver's `BlockerKind*`
 *  constants, held equal by `test/w415_blocked_kinds_test.go`. Workflow
 *  contract 4: `incomplete` is a step's own `blocked`
 *  hand-off, `driver` every supervision block (timeouts, exits without a
 *  hand-off, no usable model) and any park the daemon names no kind of,
 *  `write_failed` a `done` hand-off's hub writes that failed, `contract` a
 *  lane snapshot of another contract. An unknown kind still renders (its
 *  raw name) and is still resumable. */
export const KNOWN_BLOCKED_KINDS: readonly string[] = ["incomplete", "driver", "contract", "write_failed"];

/** The blocked kinds no `pf resume` releases: `contract` — the capsule's lane
 *  snapshot targets another workflow contract version than the daemon runs,
 *  and a resume only parks it again; the work item is cancelled and filed
 *  anew. The composer offers only the copyable `pf cancel` on them. */
export const TERMINAL_BLOCKED_KINDS: readonly string[] = ["contract"];

/** One read at a time: a call while a read runs joins it and asks for exactly
 *  one more read after it (so a caller that needs its own effect read back —
 *  a dismiss — still sees a read that started after it); a settled or failed
 *  read frees the slot. */
export function singleFlight(read: () => Promise<void>): () => Promise<void> {
  let inFlight: Promise<void> | null = null;
  let again = false;
  return () => {
    if (inFlight) {
      again = true;
      return inFlight;
    }
    const run = async () => {
      do {
        again = false;
        await read();
      } while (again);
    };
    const pending = run().finally(() => {
      inFlight = null;
    });
    inFlight = pending;
    return pending;
  };
}

/** A background task's entries (`pf task`; tied to no work item), by task id. */
export function taskDecisions(decisions: readonly PolyforgeDecision[], taskId: string): PolyforgeDecision[] {
  return decisions.filter((decision) => decision.kind === "task" && decision.task_id === taskId);
}

/** A task needs attention while one of its entries is open (failed / capped / interrupted). */
export function taskNeedsAttention(entries: readonly PolyforgeDecision[]): boolean {
  return entries.some(isOpenDecision);
}

export type PolyforgeAttentionItem =
  | { key: string; kind: "job"; job: PolyforgeJob }
  | { key: string; kind: "decision"; decision: PolyforgeDecision };

/** The Polyforge button's attention items, in click order: the jobs waiting for an
 *  answer (#413's needs_input jobs, list order), then every other open feed
 *  entry (newest first). A needs_input entry whose work item is already one
 *  of those jobs (same Cell) is that job, counted once. */
export function attentionItems(waitingJobs: readonly PolyforgeJob[], decisions: readonly PolyforgeDecision[]): PolyforgeAttentionItem[] {
  const items: PolyforgeAttentionItem[] = waitingJobs.map((job) => ({ key: `job:${profileOf(job)}:${job.wi || job.slug}`, kind: "job", job }));
  for (const decision of decisions) {
    if (!isOpenDecision(decision)) continue;
    if (decision.class === "needs_input" && decision.kind === "work_item" && waitingJobs.some((job) => jobMatches(job, decision))) continue;
    items.push({ key: `decision:${decision.id}`, kind: "decision", decision });
  }
  return items;
}

/** The work items that need attention but that the daemon no longer holds:
 *  the open work-item entries carrying a `wi` whose item
 *  matches none of `jobs` (by wi, else slug, in the same Cell — the jump
 *  target's rule), one per item (`<profile>/<wi>`), its newest entry, newest
 *  first in feed order. Blocked, failed, submit_rejected, submit_dropped,
 *  candidates_exhausted and the like live only in the feed once the daemon
 *  drops the job, so the Jobs card row builds their cards from this. Task,
 *  informational, resolved and dismissed entries never appear; nor does a
 *  live job's entry (a waiting job's needs_input entry is that job's card). */
export function droppedItemAttention(
  decisions: readonly PolyforgeDecision[],
  jobs: readonly Pick<PolyforgeJob, "wi" | "slug" | "profile">[],
): PolyforgeDecision[] {
  const seen = new Set<string>();
  const items: PolyforgeDecision[] = [];
  for (const decision of decisions) {
    if (decision.kind !== "work_item" || !decision.wi || !isOpenDecision(decision)) continue;
    const key = `${profileOf(decision)}/${decision.wi}`;
    if (seen.has(key)) continue;
    seen.add(key);
    if (jobs.some((job) => jobMatches(job, decision))) continue;
    items.push(decision);
  }
  return items;
}

/** Where the attention walk sends an item: a waiting job or a work item
 *  entry → its job detail (selected; nothing else opens); a
 *  task entry → Work › Tasks on that task; null when the entry names nothing
 *  openable. */
export type PolyforgeAttentionTarget =
  | { kind: "job"; ref: string; profile?: string }
  | { kind: "task"; id: string };

export function attentionTarget(item: PolyforgeAttentionItem, jobs: readonly PolyforgeJob[]): PolyforgeAttentionTarget | null {
  if (item.kind === "job") return { kind: "job", ref: item.job.wi || item.job.slug, ...(item.job.profile ? { profile: item.job.profile } : {}) };
  return decisionJumpTarget(item.decision, jobs);
}

export interface PolyforgeAttentionCursor {
  /** The key of the item the attention walk opened last. */
  lastKey: string | null;
  /** The job selected in the panel (#413's walk starts after it). */
  selectedWi: string | null;
  selectedProfile?: string;
}

/** The item the attention walk opens next: the one after the last opened (wrapping),
 *  else the one after the selected waiting job, else the first; null when
 *  nothing needs a human. */
export function nextAttentionItem(items: readonly PolyforgeAttentionItem[], cursor: PolyforgeAttentionCursor): PolyforgeAttentionItem | null {
  if (items.length === 0) return null;
  let index = cursor.lastKey ? items.findIndex((item) => item.key === cursor.lastKey) : -1;
  if (index < 0 && cursor.selectedWi) {
    index = items.findIndex((item) => item.kind === "job"
      && (item.job.wi === cursor.selectedWi || item.job.slug === cursor.selectedWi)
      && (cursor.selectedProfile === undefined || item.job.profile === undefined || item.job.profile === cursor.selectedProfile));
  }
  return items[(index + 1) % items.length];
}

/** Upper bound of the ids one dismiss names (the feed retains 300). */
export const DISMISS_IDS_MAX = 300;

export type PolyforgeDismissRequest = { ids: number[] } | { all: true };

/** The dismiss route's body → the request, or null: positive integer ids
 *  (deduplicated, at most DISMISS_IDS_MAX), or exactly `{all: true}`. */
export function parseDismissRequest(value: unknown): PolyforgeDismissRequest | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const v = value as { ids?: unknown; all?: unknown };
  if (v.all !== undefined) return v.all === true && v.ids === undefined ? { all: true } : null;
  if (!Array.isArray(v.ids) || v.ids.length === 0 || v.ids.length > DISMISS_IDS_MAX) return null;
  if (!v.ids.every((id) => typeof id === "number" && Number.isSafeInteger(id) && id > 0)) return null;
  return { ids: [...new Set(v.ids as number[])] };
}
