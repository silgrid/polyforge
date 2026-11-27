"use client";

import { useCallback, useEffect, useRef, useState } from "react";

type LoginTerminal = { id: string; commandId: string; closing?: "close"; exited?: boolean };

/** Owns only the one-shot login terminal created by this settings flow. */
export function useAccountLogin(onStatusRead: () => void) {
  const [terminal, setTerminal] = useState<LoginTerminal | null>(null);
  const [opening, setOpening] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [checkedAfterExit, setCheckedAfterExit] = useState(false);
  const mounted = useRef(true);
  const generation = useRef(0);
  const pending = useRef(false);
  const current = useRef<LoginTerminal | null>(null);
  const read = useRef(onStatusRead);
  useEffect(() => { read.current = onStatusRead; }, [onStatusRead]);
  const disposeTerminal = useCallback((id: string) => fetch(`/api/terminal/${encodeURIComponent(id)}`, { method: "DELETE", keepalive: true }).catch(() => undefined), []);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      generation.current++;
      if (current.current) void disposeTerminal(current.current.id);
      current.current = null;
    };
  }, [disposeTerminal]);
  const open = useCallback(async (commandId = "auth-login") => {
    if (pending.current || current.current) return;
    pending.current = true;
    const request = ++generation.current;
    setOpening(true); setError(null); setCheckedAfterExit(false);
    try {
      const response = await fetch("/api/terminal", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ login: { harness: "claude", id: commandId } }) });
      const body = await response.json();
      if (!mounted.current || generation.current !== request) {
        if (response.ok && typeof body.id === "string") void disposeTerminal(body.id);
        return;
      }
      if (!response.ok || typeof body.id !== "string") throw new Error(body.message ?? body.error ?? `HTTP ${response.status}`);
      const next = { id: body.id, commandId };
      current.current = next; setTerminal(next);
    } catch (cause) {
      if (mounted.current && generation.current === request) setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      if (generation.current === request) {
        pending.current = false;
        if (mounted.current) setOpening(false);
      }
    }
  }, [disposeTerminal]);
  const completed = useCallback((id: string) => {
    if (!mounted.current || current.current?.id !== id || current.current.exited) return;
    current.current = { ...current.current, exited: true };
    setTerminal(current.current);
    setCheckedAfterExit(true);
    read.current(); // Exit code is not proof of authentication.
  }, []);
  const close = useCallback(() => {
    generation.current++; pending.current = false; setOpening(false);
    if (current.current) {
      current.current = { ...current.current, closing: "close" };
      setTerminal(current.current);
    }
  }, []);
  const closed = useCallback((id: string) => {
    if (!mounted.current || current.current?.id !== id) return;
    current.current = null; setTerminal(null); setCheckedAfterExit(true); read.current();
  }, []);
  const closeFailed = useCallback((id: string) => {
    if (!mounted.current || current.current?.id !== id) return;
    current.current = { ...current.current, closing: undefined };
    setTerminal(current.current); setError("cleanup.loginCloseFailed");
  }, []);
  const restart = useCallback(async () => {
    const previous = current.current;
    if (!previous || !previous.exited) return;
    await disposeTerminal(previous.id);
    if (!mounted.current || current.current?.id !== previous.id) return;
    current.current = null; setTerminal(null);
    void open(previous.commandId);
  }, [disposeTerminal, open]);
  return { terminal, opening, error, checkedAfterExit, open, close, completed, closed, closeFailed, restart };
}
