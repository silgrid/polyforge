/**
 * Global "sidebar files placement" preference (wi B3): where the file
 * explorer sits in the session sidebar — stacked under the sessions pane
 * (the fork's classic split, the default) or behind its own Sessions |
 * Files tab. Phones always use tabs regardless of the stored value (the
 * sidebar derives that from its mobile breakpoint, not from here).
 *
 * Single global, browser-local preference, persisted under one fixed key
 * (the WI's fork key "pi-web:files-placement"; upstream v0.12's differently
 * named placement key is NOT read or migrated — this is a
 * fork-scoped setting). Same best-effort, corrupt-tolerant,
 * storage-injectable pattern as lib/build-outputs-preference.ts and
 * lib/pinned-expansion.ts: unavailable, quota-limited, or corrupt storage
 * degrades to the default and never throws; every accessor re-reads
 * storage (no module cache).
 *
 * setFilesPlacement also broadcasts a change event on `window`, and
 * useFilesPlacement() subscribes through useSyncExternalStore, so the
 * settings panel and the sidebar react to the same store without prop
 * drilling. The server snapshot is the default, so SSR and the first
 * client paint agree (hydration), then the real preference syncs in.
 */

import { useSyncExternalStore } from "react";

export type FilesPlacement = "stacked" | "tabs";

export const FILES_PLACEMENT_STORAGE_KEY = "pi-web:files-placement";
export const FILES_PLACEMENT_DEFAULT: FilesPlacement = "stacked";
export const FILES_PLACEMENT_CHANGE_EVENT = "pi-web:files-placement-change";

export interface FilesPlacementChange {
  value: FilesPlacement;
}

interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

interface EventTargetLike {
  addEventListener(type: string, listener: (event: unknown) => void): void;
  removeEventListener(type: string, listener: (event: unknown) => void): void;
  dispatchEvent(event: unknown): unknown;
}

function getBrowserStorage(): StorageLike | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function getBrowserWindow(): EventTargetLike | null {
  return typeof window === "undefined" ? null : (window as unknown as EventTargetLike);
}

/** Only "tabs" is a non-default value; anything else (absent, corrupt,
 * legacy shapes) reads as the default stacked layout. */
export function parseFilesPlacement(raw: string | null): FilesPlacement {
  return raw === "tabs" ? "tabs" : FILES_PLACEMENT_DEFAULT;
}

/**
 * Current global preference. Absent/unavailable/corrupt storage reads as
 * "stacked". Every accessor re-reads storage, so a reload or hot-reload
 * sees fresh state.
 */
export function getFilesPlacement(
  storage: StorageLike | null = getBrowserStorage(),
): FilesPlacement {
  if (!storage) return FILES_PLACEMENT_DEFAULT;
  try {
    return parseFilesPlacement(storage.getItem(FILES_PLACEMENT_STORAGE_KEY));
  } catch {
    return FILES_PLACEMENT_DEFAULT;
  }
}

/**
 * Persist the global preference and broadcast the change so every
 * subscribed surface re-reads it. Best-effort: quota or privacy-mode
 * failures are silently ignored, and the broadcast carries the EFFECTIVE
 * value (read back from the SAME storage the write targeted) so every
 * surface agrees instead of half-applying the requested value (the
 * build-outputs review-B1 rule).
 */
export function setFilesPlacement(
  value: FilesPlacement,
  storage: StorageLike | null = getBrowserStorage(),
  eventTarget: EventTargetLike | null = getBrowserWindow(),
): void {
  try {
    storage?.setItem(FILES_PLACEMENT_STORAGE_KEY, value);
  } catch {
    // Browser storage is best-effort.
  }
  emitFilesPlacementChange({ value: getFilesPlacement(storage) }, eventTarget);
}

/** Broadcast a preference change; package-private, exported for tests. */
export function emitFilesPlacementChange(
  change: FilesPlacementChange,
  eventTarget: EventTargetLike | null = getBrowserWindow(),
): void {
  if (!eventTarget) return;
  try {
    eventTarget.dispatchEvent(new CustomEvent(FILES_PLACEMENT_CHANGE_EVENT, { detail: change }));
  } catch {
    // Environments without CustomEvent (SSR) simply do not broadcast.
  }
}

/**
 * Subscribe to preference changes. Returns an unsubscribe function, the
 * standard shape for React effect cleanup and useSyncExternalStore.
 */
export function subscribeFilesPlacement(
  listener: (change: FilesPlacementChange) => void,
  eventTarget: EventTargetLike | null = getBrowserWindow(),
): () => void {
  if (!eventTarget) return () => {};
  const handler = (event: unknown) => {
    const detail = (event as { detail?: FilesPlacementChange }).detail;
    if (detail) listener(detail);
  };
  eventTarget.addEventListener(FILES_PLACEMENT_CHANGE_EVENT, handler);
  return () => eventTarget.removeEventListener(FILES_PLACEMENT_CHANGE_EVENT, handler);
}

function getSnapshot(): FilesPlacement {
  return getFilesPlacement();
}

function getServerSnapshot(): FilesPlacement {
  return FILES_PLACEMENT_DEFAULT;
}

/**
 * React binding: the live preference with a default server snapshot, so a
 * server-prerendered sidebar renders stacked (today's layout) and swaps to
 * tabs only after hydration without a mismatch.
 */
export function useFilesPlacement(): FilesPlacement {
  return useSyncExternalStore(subscribeFilesPlacement, getSnapshot, getServerSnapshot);
}
