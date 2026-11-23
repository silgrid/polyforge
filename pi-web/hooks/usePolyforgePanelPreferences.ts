"use client";

import { useSyncExternalStore } from "react";
import {
  POLYFORGE_NOTIFICATIONS,
  POLYFORGE_POLL_INTERVAL,
  POLYFORGE_PREFERENCES_EVENT,
  POLYFORGE_TITLE_LENGTH,
  readPolyforgeNotificationPreference,
  readPolyforgePreference,
} from "@/lib/polyforge-panel-preferences";

/** Re-read on a save in this tab (the preferences event) or another tab
 *  (`storage`). Browser storage only; no fetch. */
function subscribe(onChange: () => void): () => void {
  window.addEventListener("storage", onChange);
  window.addEventListener(POLYFORGE_PREFERENCES_EVENT, onChange);
  return () => {
    window.removeEventListener("storage", onChange);
    window.removeEventListener(POLYFORGE_PREFERENCES_EVENT, onChange);
  };
}

const readTitleLength = () => readPolyforgePreference(POLYFORGE_TITLE_LENGTH);
const readPollInterval = () => readPolyforgePreference(POLYFORGE_POLL_INTERVAL);
const readNotificationsEnabled = () => readPolyforgeNotificationPreference();
// The server render (and hydration) uses the defaults; the stored values
// apply right after, so server and client markup agree.
const defaultTitleLength = () => POLYFORGE_TITLE_LENGTH.defaultValue;
const defaultPollInterval = () => POLYFORGE_POLL_INTERVAL.defaultValue;
const defaultNotificationsEnabled = () => POLYFORGE_NOTIFICATIONS.defaultValue;

/** The panel's browser-stored preferences: the card stream's
 *  `titleLength` in code points and `pollIntervalSec` in seconds (both
 *  clamped) and the `notificationsEnabled` switch (polyforge#240), all live
 *  without a reload. */
export function usePolyforgePanelPreferences(): { titleLength: number; pollIntervalSec: number; notificationsEnabled: boolean } {
  const titleLength = useSyncExternalStore(subscribe, readTitleLength, defaultTitleLength);
  const pollIntervalSec = useSyncExternalStore(subscribe, readPollInterval, defaultPollInterval);
  const notificationsEnabled = useSyncExternalStore(subscribe, readNotificationsEnabled, defaultNotificationsEnabled);
  return { titleLength, pollIntervalSec, notificationsEnabled };
}
