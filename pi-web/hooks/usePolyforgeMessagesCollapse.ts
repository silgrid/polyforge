"use client";

import { useCallback, useSyncExternalStore } from "react";
import {
  POLYFORGE_MESSAGES_COLLAPSE_EVENT,
  POLYFORGE_MESSAGES_COLLAPSE_NONE,
  readCollapsedProjects,
  writeProjectCollapsed,
} from "@/lib/polyforge-messages-collapse";

/** Re-read on a toggle in this tab (the collapse event, sent whether or not
 *  storage kept it) or a write in another tab (`storage`). No fetch. */
function subscribe(onChange: () => void): () => void {
  window.addEventListener("storage", onChange);
  window.addEventListener(POLYFORGE_MESSAGES_COLLAPSE_EVENT, onChange);
  return () => {
    window.removeEventListener("storage", onChange);
    window.removeEventListener(POLYFORGE_MESSAGES_COLLAPSE_EVENT, onChange);
  };
}

// A stable snapshot: the same object until a toggle or a stored change.
const readSnapshot = readCollapsedProjects;
// The server render (and hydration) uses "none collapsed"; the stored state
// applies right after, so server and client markup agree.
const serverSnapshot = () => POLYFORGE_MESSAGES_COLLAPSE_NONE;

/** The Messages list's collapsed project groups, stored under
 *  `pi-web:polyforge-messages-collapsed`. */
export function usePolyforgeMessagesCollapse(): {
  collapsed: ReadonlySet<string>;
  toggleProject: (project: string) => void;
} {
  const collapsed = useSyncExternalStore(subscribe, readSnapshot, serverSnapshot);
  const toggleProject = useCallback((project: string) => {
    writeProjectCollapsed(project, !readCollapsedProjects().has(project));
  }, []);
  return { collapsed, toggleProject };
}
