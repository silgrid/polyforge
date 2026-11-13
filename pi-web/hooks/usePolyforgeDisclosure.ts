"use client";

import { useCallback, useSyncExternalStore } from "react";
import {
  POLYFORGE_DISCLOSURE_DEFAULT,
  POLYFORGE_DISCLOSURE_EVENT,
  readPolyforgeDisclosure,
  writePolyforgeDisclosureRegion,
  type PolyforgeDisclosure,
  type PolyforgeDisclosureRegion,
} from "@/lib/polyforge-disclosure";

/** Re-read on a toggle in this tab (the disclosure event, sent whether or
 *  not storage kept it) or a write in another tab (`storage`). No fetch. */
function subscribe(onChange: () => void): () => void {
  window.addEventListener("storage", onChange);
  window.addEventListener(POLYFORGE_DISCLOSURE_EVENT, onChange);
  return () => {
    window.removeEventListener("storage", onChange);
    window.removeEventListener(POLYFORGE_DISCLOSURE_EVENT, onChange);
  };
}

// A stable snapshot: the same object until a toggle or a stored change.
const readSnapshot = readPolyforgeDisclosure;
// The server render (and hydration) uses the all-closed default; the stored
// state applies right after, so server and client markup agree.
const serverSnapshot = () => POLYFORGE_DISCLOSURE_DEFAULT;

/** The Settings › Polyforge page's open/closed regions. */
export function usePolyforgeDisclosure(): {
  disclosure: PolyforgeDisclosure;
  setRegion: (region: PolyforgeDisclosureRegion, open: boolean) => void;
} {
  const disclosure = useSyncExternalStore(subscribe, readSnapshot, serverSnapshot);
  const setRegion = useCallback((region: PolyforgeDisclosureRegion, open: boolean) => {
    writePolyforgeDisclosureRegion(region, open);
  }, []);
  return { disclosure, setRegion };
}
