"use client";

import { useSyncExternalStore } from "react";
import {
  POLYFORGE_POLL_INTERVAL,
  POLYFORGE_PREFERENCES_EVENT,
  POLYFORGE_TITLE_LENGTH,
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
// The server render (and hydration) uses the defaults; the stored values
// apply right after, so server and client markup agree.
const defaultTitleLength = () => POLYFORGE_TITLE_LENGTH.defaultValue;
const defaultPollInterval = () => POLYFORGE_POLL_INTERVAL.defaultValue;

/** The card stream's two browser-stored preferences:
 *  `titleLength` in code points and `pollIntervalSec` in seconds, both
 *  clamped, both live without a reload. */
export function usePolyforgePanelPreferences(): { titleLength: number; pollIntervalSec: number } {
  const titleLength = useSyncExternalStore(subscribe, readTitleLength, defaultTitleLength);
  const pollIntervalSec = useSyncExternalStore(subscribe, readPollInterval, defaultPollInterval);
  return { titleLength, pollIntervalSec };
}
