/**
 * Server-side session build cache (pi#83).
 *
 * The detail and context routes re-run their pure builds over the parsed
 * entries on EVERY request — tree projection, buildSessionContext, stats —
 * even when the SessionManager cache already holds the parse. Opening the
 * same tab again (or the client's background freshness read after a
 * snapshot hydrate) pays hundreds of milliseconds for identical output.
 *
 * This cache memoizes those builds keyed by the on-disk fingerprint, so any
 * external append (size/mtime change) invalidates automatically. Live RPC
 * sessions mutate constantly and bypass it. Entries are bounded LRU; the
 * cached payloads are the tail-windowed build results (≤1000 messages), so
 * the retained heap stays proportional to what the SM cache already holds.
 */

import { statSync } from "node:fs";

const MAX_ENTRIES = 8;

interface CacheEntry {
	value: unknown;
	fingerprint: string;
}

declare global {
	var __piSessionBuildCache: Map<string, CacheEntry> | undefined;
}

function store(): Map<string, CacheEntry> {
	if (!globalThis.__piSessionBuildCache) globalThis.__piSessionBuildCache = new Map();
	return globalThis.__piSessionBuildCache;
}

function fingerprintOf(filePath: string): string | null {
	try {
		const stats = statSync(filePath);
		return `${stats.size}:${stats.mtimeMs}`;
	} catch {
		return null;
	}
}

function stableParams(params: Record<string, unknown>): string {
	const keys = Object.keys(params).sort();
	return keys.map((key) => `${key}=${String(params[key])}`).join("&");
}

/**
 * Memoize `build()` for one session file + build parameters. Returns the
 * cached value when the file fingerprint and parameters match; otherwise
 * runs the build and stores it. A null fingerprint (unreadable file) or a
 * falsey `filePath` (live RPC session) bypasses the cache entirely.
 */
export function cachedSessionBuild<T>(
	filePath: string,
	params: Record<string, unknown>,
	build: () => T,
): T {
	if (!filePath) return build();
	const fingerprint = fingerprintOf(filePath);
	if (fingerprint === null) return build();
	const key = `${filePath}|${fingerprint}|${stableParams(params)}`;
	const cache = store();
	const hit = cache.get(key);
	if (hit && hit.fingerprint === fingerprint) {
		// LRU touch.
		cache.delete(key);
		cache.set(key, hit);
		return hit.value as T;
	}
	const value = build();
	cache.set(key, { value, fingerprint });
	while (cache.size > MAX_ENTRIES) {
		const oldestKey = cache.keys().next().value;
		if (oldestKey === undefined) break;
		cache.delete(oldestKey);
	}
	return value;
}

/** Test seam: drop every cached build. */
export function clearSessionBuildCache(): void {
	store().clear();
}
