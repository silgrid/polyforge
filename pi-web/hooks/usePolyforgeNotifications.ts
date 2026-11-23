"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { shouldShowBrowserNotification } from "@/lib/browser-notifications";
import type { PolyforgeDecision } from "@/lib/polyforge-decisions-pure";
import type { TranslationParams } from "@/lib/i18n/types";
import {
  isOwnerNotificationDecision,
  markNotificationHintShown,
  mergeNotificationLedger,
  newNotificationIds,
  notificationPermission,
  ownerNotificationCopy,
  readNotificationHintShown,
  readNotificationLedger,
  requestNotificationPermission,
  writeNotificationLedger,
  type OwnerNotificationPermission,
} from "@/lib/polyforge-notifications";
import { usePolyforgePanelPreferences } from "./usePolyforgePanelPreferences";

export interface PolyforgeNotificationsOptions {
  /** The sanitized decision feed, or null before its first good read. */
  decisions: readonly PolyforgeDecision[] | null;
  translate: (key: string, params?: TranslationParams) => string;
  /** Open one entry in the panel (polyforge#240 Q7: the entry the
   *  notification was built from, never the walk's next one). */
  onOpenDecision: (decision: PolyforgeDecision) => void;
}

export interface PolyforgeNotificationsState {
  /** The settings switch (default on). */
  enabled: boolean;
  /** The browser's permission; `unsupported` when the API is absent. */
  permission: OwnerNotificationPermission;
  /** The one-time in-app explanation of a missing permission is showing. */
  hint: boolean;
  /** The ledger cannot be read or written, so delivery is paused (no dedupe). */
  storageBlocked: boolean;
  dismissHint: () => void;
  requestPermission: () => Promise<OwnerNotificationPermission>;
}

/**
 * Polyforge#240: turn new owner decisions of the daemon's feed into browser
 * notifications.
 *
 * The rules are the owner's (2026-11-23): only needs_input / blocked / failed
 * entries, only while the tab is not in the foreground, one notification per
 * entry id (a localStorage ledger, so a reload never repeats one), the first
 * successful feed only establishes a baseline, and a missing permission is
 * never asked for automatically — the first entry that would have notified
 * shows a one-time in-app explanation instead. An entry observed in the
 * foreground, while the switch is off, or without permission is consumed
 * (never back-filled). No timer: the feed's own read cadence drives this.
 */
export function usePolyforgeNotifications(options: PolyforgeNotificationsOptions): PolyforgeNotificationsState {
  const { decisions, translate, onOpenDecision } = options;
  const { notificationsEnabled } = usePolyforgePanelPreferences();
  const [permission, setPermission] = useState<OwnerNotificationPermission>(() => notificationPermission());
  const [hint, setHint] = useState(false);
  const [storageBlocked, setStorageBlocked] = useState(false);

  // Review r1#1: a notification lives past the render that delivered it, so
  // its click must run the LATEST open callback — the selection and jobs it
  // reads are the ones current when the owner clicks.
  const onOpenDecisionRef = useRef(onOpenDecision);
  onOpenDecisionRef.current = onOpenDecision;

  const dismissHint = useCallback(() => setHint(false), []);
  const requestPermission = useCallback(async () => {
    const answer = await requestNotificationPermission();
    setPermission(answer);
    return answer;
  }, []);

  useEffect(() => {
    if (decisions === null) return;
    const current = notificationPermission();
    setPermission(current);

    const ledger = readNotificationLedger();
    if (!ledger.ok) {
      // No ledger, no dedupe: delivering would repeat, so pause instead.
      setStorageBlocked(true);
      return;
    }
    setStorageBlocked(false);
    const fresh = newNotificationIds(decisions, new Set(ledger.ids));
    if (!ledger.present) {
      // The first successful feed only establishes the baseline (Q6 A). A
      // storage that cannot take it would re-baseline on every read and
      // silently never notify, so it pauses delivery and says so.
      if (!writeNotificationLedger(mergeNotificationLedger(ledger.ids, fresh))) setStorageBlocked(true);
      return;
    }
    if (fresh.length === 0) return;
    // Claim before delivering: a crash between the two may lose one
    // notification, never repeat one (Q5 B's best effort).
    if (!writeNotificationLedger(mergeNotificationLedger(ledger.ids, fresh))) {
      setStorageBlocked(true);
      return;
    }
    if (!notificationsEnabled) return;

    const notifiable = fresh.flatMap((id) => {
      const decision = decisions.find((entry) => entry.id === id);
      return decision && isOwnerNotificationDecision(decision) ? [decision] : [];
    });
    if (notifiable.length === 0) return;
    if (!shouldShowBrowserNotification()) return;

    if (current !== "granted" || typeof Notification === "undefined") {
      // Q4 A: no automatic browser prompt; explain once, in the page.
      if (!readNotificationHintShown() && markNotificationHintShown()) setHint(true);
      return;
    }

    for (const decision of notifiable) {
      try {
        const label = translate(`polyforge.decisions.class.${decision.class}`);
        const copy = ownerNotificationCopy(
          decision,
          label === `polyforge.decisions.class.${decision.class}` ? (decision.phrase ?? decision.class) : label,
        );
        const notification = new Notification(copy.title, { body: copy.body, tag: copy.tag });
        notification.onclick = () => {
          window.focus?.();
          notification.close();
          onOpenDecisionRef.current(decision);
        };
      } catch {
        // A browser refusing the constructor delivers nothing; never retried.
      }
    }
  }, [decisions, notificationsEnabled, translate]);

  return { enabled: notificationsEnabled, permission, hint, storageBlocked, dismissHint, requestPermission };
}