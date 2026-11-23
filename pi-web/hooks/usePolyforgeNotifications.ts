"use client";

import type { PolyforgeDecision } from "@/lib/polyforge-decisions-pure";
import type { OwnerNotificationPermission } from "@/lib/polyforge-notifications";

export interface PolyforgeNotificationsOptions {
  /** The sanitized decision feed, or null before its first good read. */
  decisions: readonly PolyforgeDecision[] | null;
  translate: (key: string, params?: Record<string, unknown>) => string;
  onOpenDecision: (decision: PolyforgeDecision) => void;
}

export interface PolyforgeNotificationsState {
  enabled: boolean;
  permission: OwnerNotificationPermission;
  hint: boolean;
  storageBlocked: boolean;
  dismissHint: () => void;
  requestPermission: () => Promise<OwnerNotificationPermission>;
}

export function usePolyforgeNotifications(_options: PolyforgeNotificationsOptions): PolyforgeNotificationsState {
  return {
    enabled: false,
    permission: "unsupported",
    hint: false,
    storageBlocked: false,
    dismissHint: () => {},
    requestPermission: () => Promise.resolve("unsupported"),
  };
}