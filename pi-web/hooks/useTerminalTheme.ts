import { useSyncExternalStore } from "react";
import {
  parseTerminalTheme,
  TERMINAL_THEME_DEFAULT,
  TERMINAL_THEME_STORAGE_KEY,
  type TerminalTheme,
} from "@/lib/terminal-theme";

function readStoredTheme(): TerminalTheme {
  try {
    return parseTerminalTheme(window.localStorage.getItem(TERMINAL_THEME_STORAGE_KEY));
  } catch {
    return TERMINAL_THEME_DEFAULT;
  }
}

let theme: TerminalTheme | null = null;
const listeners = new Set<() => void>();

function getSnapshot(): TerminalTheme {
  if (theme === null) theme = readStoredTheme();
  return theme;
}

function notify(): void {
  listeners.forEach((listener) => listener());
}

// Another tab chose; `key` is null when that tab cleared its storage.
function onStorage(event: StorageEvent): void {
  if (event.key !== null && event.key !== TERMINAL_THEME_STORAGE_KEY) return;
  const next = readStoredTheme();
  if (next === theme) return;
  theme = next;
  notify();
}

function subscribe(listener: () => void): () => void {
  if (listeners.size === 0) {
    window.addEventListener("storage", onStorage);
    // Nobody listened for another tab's choice meanwhile.
    theme = readStoredTheme();
  }
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) window.removeEventListener("storage", onStorage);
  };
}

export function setTerminalTheme(next: TerminalTheme): void {
  if (next === getSnapshot()) return;
  theme = next;
  try {
    window.localStorage.setItem(TERMINAL_THEME_STORAGE_KEY, next);
  } catch {
    // Applies in memory even if the browser cannot save it.
  }
  notify();
}

/** Every open terminal shares one choice, kept per browser. */
export function useTerminalTheme(): TerminalTheme {
  return useSyncExternalStore(subscribe, getSnapshot, () => TERMINAL_THEME_DEFAULT);
}
