"use client";

import { useCallback, useSyncExternalStore } from "react";
import {
  POLYFORGE_MESSAGES_LIST_COLLAPSE_EVENT,
  readMessagesListCollapsed,
  writeMessagesListCollapsed,
} from "@/lib/polyforge-messages-list-collapse";

/** Re-read on a toggle in this tab (the collapse event, sent whether or not
 *  storage kept it) or a write in another tab (`storage`). No fetch. */
function subscribe(onChange: () => void): () => void {
  window.addEventListener("storage", onChange);
  window.addEventListener(POLYFORGE_MESSAGES_LIST_COLLAPSE_EVENT, onChange);
  return () => {
    window.removeEventListener("storage", onChange);
    window.removeEventListener(POLYFORGE_MESSAGES_LIST_COLLAPSE_EVENT, onChange);
  };
}

// The server render (and hydration) uses "expanded"; the stored state
// applies right after, so server and client markup agree.
const serverSnapshot = () => false;

/** The Messages contact column's collapsed strip (the
 *  ≤640px sheet shares it), stored under
 *  `pi-web:polyforge-messages-list-collapsed`. */
export function usePolyforgeMessagesListCollapse(): {
  listCollapsed: boolean;
  setListCollapsed: (collapsed: boolean) => void;
} {
  const listCollapsed = useSyncExternalStore(subscribe, readMessagesListCollapsed, serverSnapshot);
  const setListCollapsed = useCallback((collapsed: boolean) => { writeMessagesListCollapsed(collapsed); }, []);
  return { listCollapsed, setListCollapsed };
}
