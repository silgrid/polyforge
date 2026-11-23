/**
 * Client-safe browser-notification vocabulary of the Polyforge decision
 * feed (polyforge#240): which open entries need a human (the owner's
 * allowlist), the notification copy, the localStorage ledger that makes an
 * entry notify once, the one-time permission explanation marker and the
 * browser's permission state. No React, no fetch; the hook is
 * `hooks/usePolyforgeNotifications.ts`.
 */
import type { PolyforgeDecision } from "./polyforge-decisions-pure";

export const OWNER_NOTIFICATION_CLASSES: readonly string[] = [];

export function isOwnerNotificationDecision(_decision: PolyforgeDecision): boolean {
  return false;
}

export const NOTIFICATION_SUMMARY_MAX = 0;

export const NOTIFICATION_LEDGER_MAX = 0;

export interface OwnerNotificationCopy {
  title: string;
  body: string;
  tag: string;
}

export function ownerNotificationCopy(_decision: PolyforgeDecision, _classLabel: string): OwnerNotificationCopy {
  return { title: "", body: "", tag: "" };
}

export function oneLineSummary(_text: string | undefined, _fallback: string, _max = 0): string {
  return "";
}

export interface NotificationLedger {
  present: boolean;
  ids: number[];
}

export const NOTIFICATION_LEDGER_KEY = "";

export const NOTIFICATION_HINT_KEY = "";

export interface LedgerStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export type LedgerRead = { ok: true; present: boolean; ids: number[] } | { ok: false };

export function parseNotificationLedger(_raw: string | null): NotificationLedger {
  return { present: false, ids: [] };
}

export function mergeNotificationLedger(_ids: readonly number[], _fresh: readonly number[], _max = 0): number[] {
  return [];
}

export function newNotificationIds(_decisions: readonly PolyforgeDecision[], _seen: ReadonlySet<number>): number[] {
  return [];
}

export function readNotificationLedger(_storage?: LedgerStorage): LedgerRead {
  return { ok: false };
}

export function writeNotificationLedger(_ids: readonly number[], _storage?: LedgerStorage): boolean {
  return false;
}

export function readNotificationHintShown(_storage?: LedgerStorage): boolean {
  return false;
}

export function markNotificationHintShown(_storage?: LedgerStorage): boolean {
  return false;
}

export type OwnerNotificationPermission = "granted" | "denied" | "default" | "unsupported";

export function notificationPermission(): OwnerNotificationPermission {
  return "unsupported";
}

export function requestNotificationPermission(): Promise<OwnerNotificationPermission> {
  return Promise.resolve("unsupported");
}
