"use client";

import { useI18n } from "@/hooks/useI18n";
import { usePolyforgeNotifications } from "@/hooks/usePolyforgeNotifications";
import type { PolyforgeDecision } from "@/lib/polyforge-decisions-pure";

/**
 * Polyforge#240 Q4: the one-time in-app explanation a browser without
 * notification permission shows when a new owner decision would have
 * notified. It is the only place the page explains the permission, it never
 * pops the browser prompt by itself, and "Allow notifications" is the one
 * explicit gesture that asks. Renders nothing otherwise.
 */
export function PolyforgeNotifications({
  decisions,
  onOpenDecision,
}: {
  decisions: readonly PolyforgeDecision[] | null;
  onOpenDecision: (decision: PolyforgeDecision) => void;
}) {
  const { t } = useI18n();
  // The one explicit gesture is the card's and this banner's: the page never
  // requests the permission on its own (owner ruling Q4).
  const { hint, permission, storageBlocked, dismissHint, requestPermission } = usePolyforgeNotifications({
    decisions,
    translate: t,
    onOpenDecision,
  });
  // Review r1#4: a ledger the browser cannot write (quota, a locked-down
  // profile) pauses delivery; the same one-time banner reports it.
  if (!hint && !storageBlocked) return null;

  const authorize = async () => {
    const answer = await requestPermission();
    if (answer === "granted") dismissHint();
  };

  return (
    <div
      data-pf-notification-hint=""
      role="status"
      style={{
        position: "fixed",
        bottom: 16,
        left: "50%",
        transform: "translateX(-50%)",
        zIndex: 60,
        display: "flex",
        alignItems: "center",
        gap: 10,
        maxWidth: "min(560px, calc(100vw - 32px))",
        padding: "8px 12px",
        borderRadius: 8,
        border: "1px solid var(--border)",
        background: "var(--bg-panel)",
        boxShadow: "0 8px 24px rgba(0,0,0,0.14)",
        fontSize: 12,
        lineHeight: "18px",
        color: "var(--text)",
      }}
    >
      <span data-pf-notification-hint-text="" style={{ minWidth: 0 }}>
        {storageBlocked ? t("polyforge.settings.notificationsStorageBlocked") : t("polyforge.notifications.hint")}
      </span>
      {!storageBlocked && permission === "default" && (
        <button
          type="button"
          data-pf-notification-authorize=""
          onClick={() => void authorize()}
          style={{
            flex: "0 0 auto",
            padding: "2px 8px",
            borderRadius: 4,
            border: "1px solid var(--border)",
            background: "var(--bg-selected)",
            color: "var(--text)",
            fontSize: 12,
            lineHeight: "18px",
            cursor: "pointer",
          }}
        >
          {t("polyforge.notifications.authorize")}
        </button>
      )}
      {!storageBlocked && (
      <button
        type="button"
        data-pf-notification-dismiss=""
        onClick={dismissHint}
        style={{
          flex: "0 0 auto",
          padding: "2px 8px",
          borderRadius: 4,
          border: "1px solid var(--border)",
          background: "none",
          color: "var(--text-muted)",
          fontSize: 12,
          lineHeight: "18px",
          cursor: "pointer",
        }}
      >
        {t("polyforge.notifications.dismiss")}
      </button>
      )}
    </div>
  );
}