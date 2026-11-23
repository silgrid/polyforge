"use client";

import { useCallback, useEffect, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { usePolyforgePanelPreferences } from "@/hooks/usePolyforgePanelPreferences";
import { POLYFORGE_NOTIFICATIONS, writePolyforgeNotificationPreference } from "@/lib/polyforge-panel-preferences";
import {
  notificationPermission,
  readNotificationLedger,
  requestNotificationPermission,
  type OwnerNotificationPermission,
} from "@/lib/polyforge-notifications";
import { ConfigButton, ConfigDetailGrid, ConfigDetailGridRow, ConfigSwitch } from "./SettingsUi";

const PERMISSION_KEY: Record<OwnerNotificationPermission, string> = {
  granted: "polyforge.settings.notificationsPermissionGranted",
  default: "polyforge.settings.notificationsPermissionDefault",
  denied: "polyforge.settings.notificationsPermissionDenied",
  unsupported: "polyforge.settings.notificationsPermissionUnsupported",
};

/** Settings › Polyforge's browser-notification card (polyforge#240 Q4):
 *  the switch (default on), the browser's permission as visible text, and
 *  the explicit authorization gesture — the page never pops the browser
 *  prompt by itself. Everything is browser-local; no request is made. */
export function PolyforgeNotificationSettings() {
  const { t } = useI18n();
  const { notificationsEnabled } = usePolyforgePanelPreferences();
  const [permission, setPermission] = useState<OwnerNotificationPermission>(() => notificationPermission());
  const [storageBlocked, setStorageBlocked] = useState(false);
  const [busy, setBusy] = useState(false);

  // Returning to the tab re-reads the permission the owner changed in the
  // browser's own settings; nothing is requested here.
  useEffect(() => {
    const refresh = () => setPermission(notificationPermission());
    refresh();
    document.addEventListener("visibilitychange", refresh);
    window.addEventListener("focus", refresh);
    return () => {
      document.removeEventListener("visibilitychange", refresh);
      window.removeEventListener("focus", refresh);
    };
  }, []);

  // An unusable ledger pauses delivery (no dedupe); the card says so.
  useEffect(() => {
    setStorageBlocked(!readNotificationLedger().ok);
  }, []);

  const toggle = useCallback((enabled: boolean) => {
    writePolyforgeNotificationPreference(POLYFORGE_NOTIFICATIONS, enabled);
  }, []);
  const authorize = useCallback(async () => {
    setBusy(true);
    try {
      setPermission(await requestNotificationPermission());
    } finally {
      setBusy(false);
    }
  }, []);

  return (
    <section className="polyforge-config-card polyforge-config-notifications" aria-label={t("polyforge.settings.notificationsTitle")}>
      <p className="polyforge-config-card-title">{t("polyforge.settings.notificationsTitle")}</p>
      <p className="polyforge-config-card-hint">{t("polyforge.settings.notificationsHint")}</p>
      <ConfigDetailGrid>
        <ConfigDetailGridRow label={t("polyforge.settings.notificationsSwitch")} tone="plain">
          <ConfigSwitch
            checked={notificationsEnabled}
            label={t("polyforge.settings.notificationsSwitch")}
            onChange={toggle}
          />
        </ConfigDetailGridRow>
        <ConfigDetailGridRow label={t("polyforge.settings.notificationsPermissionLabel")} tone="plain">
          <span className="polyforge-config-notification-permission">
            <span data-pf-notification-permission={permission}>{t(PERMISSION_KEY[permission])}</span>
            {permission === "default" && (
              <ConfigButton size="small" onClick={() => void authorize()} disabled={busy}>
                {t("polyforge.settings.notificationsAuthorize")}
              </ConfigButton>
            )}
          </span>
        </ConfigDetailGridRow>
      </ConfigDetailGrid>
      {permission === "denied" && (
        <p className="config-scope-switch-reason">{t("polyforge.settings.notificationsDeniedHint")}</p>
      )}
      {permission === "unsupported" && (
        <p className="config-scope-switch-reason">{t("polyforge.settings.notificationsUnsupportedHint")}</p>
      )}
      {storageBlocked && (
        <p className="config-scope-switch-reason">{t("polyforge.settings.notificationsStorageBlocked")}</p>
      )}
    </section>
  );
}