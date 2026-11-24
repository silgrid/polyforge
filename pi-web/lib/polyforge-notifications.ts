/**
 * Client-safe browser-notification vocabulary of the Polyforge decision
 * feed (polyforge#240): which open entries need a human (the owner's
 * allowlist), the notification copy, the localStorage ledger that makes an
 * entry notify once, the one-time permission explanation marker and the
 * browser's permission state. No React, no fetch; the hook is
 * `hooks/usePolyforgeNotifications.ts`.
 */
import { decisionTargetLabel, isOpenDecision, type PolyforgeDecision } from "./polyforge-decisions-pure";

/** The classes a browser notification carries (owner ruling 2026-11-23,
 *  Q9): the step's question, a block and a failure. Informational classes
 *  and the other attention classes never notify. */
export const OWNER_NOTIFICATION_CLASSES: readonly string[] = ["needs_input", "blocked", "failed"];

/** An entry notifies while it is open (`attention && !resolved && !dismissed`)
 *  and its class is on the owner's allowlist. A replied, resolved or
 *  dismissed entry is consumed without a notification. */
export function isOwnerNotificationDecision(decision: PolyforgeDecision): boolean {
  return isOpenDecision(decision) && OWNER_NOTIFICATION_CLASSES.includes(decision.class);
}

/** The ruled bound of a summary in the notification body, in Unicode code points. */
export const NOTIFICATION_SUMMARY_MAX = 120;

/** The ledger keeps the newest ids; 2000 outlives the feed's 300-entry
 *  retention, so an open entry the feed still carries never notifies again. */
export const NOTIFICATION_LEDGER_MAX = 2000;

export interface OwnerNotificationCopy {
  title: string;
  body: string;
  tag: string;
}

/** One entry's notification text: the title names the entry (its slug, else
 *  its id, else its task) and the class label; the body is the entry's own
 *  summary on one line, else the class label (owner ruling Q8: the existing
 *  wire fields only, nothing queried or invented). */
export function ownerNotificationCopy(decision: PolyforgeDecision, classLabel: string): OwnerNotificationCopy {
  const label = classLabel.trim() || decision.phrase?.trim() || decision.class;
  const target = decisionTargetLabel(decision);
  return {
    title: target ? `${target} · ${label}` : label,
    body: oneLineSummary(decision.text, label),
    tag: `polyforge-decision-${decision.id}`,
  };
}

/** A summary as one line of at most `max` Unicode code points: whitespace
 *  runs (newlines included) fold to one space, and a longer text is cut with
 *  a trailing ellipsis that counts inside the bound. */
export function oneLineSummary(text: string | undefined, fallback: string, max = NOTIFICATION_SUMMARY_MAX): string {
  const collapse = (value: string | undefined): string =>
    (typeof value === "string" ? value : "").replace(/\s+/gu, " ").trim();
  const source = collapse(text) || collapse(fallback);
  const points = Array.from(source);
  if (points.length <= max) return source;
  if (max <= 0) return "";
  return points.slice(0, max - 1).join("") + "…";
}

export interface NotificationLedger {
  /** Whether a ledger is stored at all: the first successful feed only
   *  establishes a baseline (owner ruling Q6). */
  present: boolean;
  /** Positive, unique entry ids, in stored order (oldest first). */
  ids: number[];
}

/** A stored ledger parsed tolerantly: absent is no baseline yet; a corrupt
 *  or wrong-typed value reads as a stored empty baseline (never a flood of
 *  notifications). Non-positive, non-integer and duplicate ids are dropped. */
export function parseNotificationLedger(raw: string | null): NotificationLedger {
  if (raw === null || raw === undefined) return { present: false, ids: [] };
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return { present: true, ids: [] };
  }
  if (!Array.isArray(parsed)) return { present: true, ids: [] };
  const ids: number[] = [];
  for (const value of parsed) {
    if (typeof value !== "number" || !Number.isSafeInteger(value) || value <= 0 || ids.includes(value)) continue;
    ids.push(value);
  }
  return { present: true, ids };
}

/** The ledger after `fresh` ids are claimed: deduplicated, bounded to the
 *  newest `max` ids. */
export function mergeNotificationLedger(ids: readonly number[], fresh: readonly number[], max = NOTIFICATION_LEDGER_MAX): number[] {
  const merged: number[] = [];
  const add = (value: unknown): void => {
    if (typeof value !== "number" || !Number.isSafeInteger(value) || value <= 0 || merged.includes(value)) return;
    merged.push(value);
  };
  for (const value of ids) add(value);
  for (const value of fresh) add(value);
  return merged.length > max ? merged.slice(merged.length - max) : merged;
}

/** The feed's entries the ledger has not seen, in feed order. */
export function newNotificationIds(decisions: readonly PolyforgeDecision[], seen: ReadonlySet<number>): number[] {
  const out: number[] = [];
  for (const decision of decisions) {
    if (seen.has(decision.id) || out.includes(decision.id)) continue;
    out.push(decision.id);
  }
  return out;
}

export const NOTIFICATION_LEDGER_KEY = "pi-web:polyforge-notified-decisions";
export const NOTIFICATION_HINT_KEY = "pi-web:polyforge-notification-hint-shown";

export interface LedgerStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

function resolveStorage(storage?: LedgerStorage): LedgerStorage | null {
  if (storage) return storage;
  try {
    return typeof window !== "undefined" ? window.localStorage : null;
  } catch {
    return null;
  }
}

export type LedgerRead = { ok: true; present: boolean; ids: number[] } | { ok: false };

/** The browser's ledger read: an unavailable storage is `ok:false`, never a
 *  silent empty baseline (the caller pauses delivery, so nothing repeats). */
export function readNotificationLedger(storage?: LedgerStorage): LedgerRead {
  const store = resolveStorage(storage);
  if (!store) return { ok: false };
  try {
    const ledger = parseNotificationLedger(store.getItem(NOTIFICATION_LEDGER_KEY));
    return { ok: true, present: ledger.present, ids: ledger.ids };
  } catch {
    return { ok: false };
  }
}

/** Store the claimed ids (deduplicated and bounded); false when storage refuses. */
export function writeNotificationLedger(ids: readonly number[], storage?: LedgerStorage): boolean {
  const store = resolveStorage(storage);
  if (!store) return false;
  try {
    store.setItem(NOTIFICATION_LEDGER_KEY, JSON.stringify(mergeNotificationLedger(ids, [])));
    return true;
  } catch {
    return false;
  }
}

/** Whether the one-time in-app permission explanation was already shown in
 *  this browser (owner ruling Q4: once, persisted). */
export function readNotificationHintShown(storage?: LedgerStorage): boolean {
  const store = resolveStorage(storage);
  if (!store) return false;
  try {
    return store.getItem(NOTIFICATION_HINT_KEY) === "1";
  } catch {
    return false;
  }
}

export function markNotificationHintShown(storage?: LedgerStorage): boolean {
  const store = resolveStorage(storage);
  if (!store) return false;
  try {
    store.setItem(NOTIFICATION_HINT_KEY, "1");
    return true;
  } catch {
    return false;
  }
}

export type OwnerNotificationPermission = "granted" | "denied" | "default" | "unsupported";

/** The browser's notification permission; a browser without the API is
 *  `unsupported` (the settings card says so, never throws). */
export function notificationPermission(): OwnerNotificationPermission {
  if (typeof Notification === "undefined") return "unsupported";
  const permission = Notification.permission;
  return permission === "granted" || permission === "denied" ? permission : "default";
}

/** The explicit user gesture: ask only while the permission is `default`.
 *  `granted` and `denied` never ask again (owner ruling Q4). */
export function requestNotificationPermission(): Promise<OwnerNotificationPermission> {
  if (typeof Notification === "undefined" || typeof Notification.requestPermission !== "function") {
    return Promise.resolve("unsupported");
  }
  if (Notification.permission !== "default") return Promise.resolve(notificationPermission());
  return Promise.resolve()
    .then(() => Notification.requestPermission())
    .then((answer) => (answer === "granted" || answer === "denied" || answer === "default" ? answer : notificationPermission()))
    .catch(() => notificationPermission());
}