// Cache session-list metadata without building the SDK's unused allMessagesText.
// The transcript parse runs on the worker pool (lib/session-scan-worker-source.ts
// through lib/session-parse-worker-pool.ts), off the main thread (polyforge#140).
// Normal listings rescan only new/changed files; summary listings reuse whatever
// the index already holds and fall back to header/stat metadata for the files
// that changed, which a later normal listing hydrates. A file that only grew
// since its last scan is parsed from where that scan stopped (#1259).
// ponytail: size/mtime fingerprints miss same-size edits with restored mtime,
// and the resume check compares only the inode, the header line and the bytes
// before the resume offset: an in-place edit that keeps all three (same-length,
// same inode), then an append, reads as an append, and the stale row persists
// across later appends until a change that does not grow the file, or replaces
// it, forces a full read. Use content hashes if detecting those edits becomes
// necessary.
import { existsSync, readFileSync } from "node:fs";
import { open, readdir, stat } from "node:fs/promises";
import type { FileHandle } from "node:fs/promises";
import type { Dirent } from "node:fs";
import { basename, join } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import { writePrivateFileAtomicSync } from "./atomic-file";
import { runSessionScanTask, type SessionScanTaskResult } from "./session-parse-worker-pool";
import { isRecord } from "./type-guards";

export interface ScannedSessionInfo {
	path: string;
	id: string;
	cwd: string;
	name?: string;
	created: Date;
	modified: Date;
	messageCount: number;
	firstMessage: string;
	parentSessionPath?: string;
	/** True when only header/stat metadata was available for this listing. */
	detailsPending?: boolean;
}

interface Fingerprint {
	size: number;
	mtimeMs: number;
}

const SUMMARY_HEADER_MAX_BYTES = 64 * 1024;

/**
 * Where the next scan of a file that only grew picks up. `offset` is just past
 * the last newline-terminated line parsed; the row's own name, count and first
 * message are the running totals (a scan that counted an unterminated last line
 * keeps no resume point). `check` hashes the header line, [0, headerEnd), with
 * the RESUME_SAMPLE_BYTES before `offset`: a rewrite in place fails it. `ino`
 * catches a file replaced by a rename (sed -i, editors, git checkout, a backup
 * restored), whatever its bytes.
 */
interface ResumePoint {
	ino: number;
	offset: number;
	headerEnd: number;
	check: string;
	/** False while no user message had text: the row shows "(no messages)". */
	hasFirstMessage: boolean;
	lastActivityTime?: number;
}

interface IndexEntry {
	fp: Fingerprint;
	info: ScannedSessionInfo;
	resume?: ResumePoint;
}

type RawEntry = Record<string, unknown>;

// Bump on any change to what the worker source's foldEntry() counts
// (lib/session-scan-worker-source.ts): a resumed scan adds to totals
// counted under the rules its prefix was scanned with.
const INDEX_FORMAT_VERSION = 1;
/** A trailing throttle: a run's rescans rewrite the index once per window. */
const INDEX_PERSIST_DELAY_MS = 5_000;

declare global {
	var __piWebScanIndex: Map<string, IndexEntry> | undefined;
	var __piWebScanIndexLoaded: boolean | undefined;
	var __piWebScanIndexSave: PendingIndexSave | undefined;
}

/** A queued index write: where it goes and what, both fixed by the last scan that changed it. */
interface PendingIndexSave {
	path: string;
	entries: Array<[string, IndexEntry]>;
	timer: NodeJS.Timeout;
}

function parseLine(line: string): RawEntry | null {
	if (!line.trim()) return null;
	try {
		const entry = JSON.parse(line) as RawEntry;
		return entry && typeof entry === "object" ? entry : null;
	} catch {
		return null;
	}
}

/** Read only the first physical line needed to identify a session file. */
async function readSessionHeaderSummary(filePath: string): Promise<RawEntry | null> {
	let handle: FileHandle | undefined;
	try {
		handle = await open(filePath, "r");
		const buffer = Buffer.allocUnsafe(SUMMARY_HEADER_MAX_BYTES);
		const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
		if (bytesRead <= 0) return null;
		const source = buffer.subarray(0, bytesRead).toString("utf8");
		for (const line of source.split("\n")) {
			const entry = parseLine(line.replace(/\r$/, ""));
			if (!entry) continue;
			return entry.type === "session" ? entry : null;
		}
		return null;
	} catch {
		return null;
	} finally {
		await handle?.close().catch(() => undefined);
	}
}

function hasMatchingFingerprint(
	cached: IndexEntry | undefined,
	fingerprint: Fingerprint,
): cached is IndexEntry {
	return Boolean(
		cached
		&& cached.fp.size === fingerprint.size
		&& cached.fp.mtimeMs === fingerprint.mtimeMs,
	);
}

/** Only a file larger than at its last scan may resume where that scan stopped. */
function hasGrown(cached: IndexEntry | undefined, fingerprint: Fingerprint): cached is IndexEntry {
	return Boolean(
		cached?.resume
		&& fingerprint.size > cached.fp.size
		&& fingerprint.size >= cached.resume.offset,
	);
}

/** Build a session row from the header alone, without parsing the transcript. */
async function deferredSessionInfo(
	filePath: string,
	fingerprint: Fingerprint,
): Promise<ScannedSessionInfo | null> {
	const header = await readSessionHeaderSummary(filePath);
	if (
		!header
		|| typeof header.id !== "string"
		|| typeof header.cwd !== "string"
		|| typeof header.timestamp !== "string"
	) return null;

	const created = new Date(header.timestamp);
	if (!Number.isFinite(created.getTime())) return null;

	return {
		path: filePath,
		id: header.id,
		cwd: header.cwd,
		created,
		modified: new Date(fingerprint.mtimeMs),
		messageCount: 0,
		firstMessage: "",
		...(typeof header.parentSession === "string" ? { parentSessionPath: header.parentSession } : {}),
		detailsPending: true,
	};
}

// The parse itself runs on the worker pool (lib/session-parse-worker-pool.ts,
// executing lib/session-scan-worker-source.ts): a 20 MiB single JSON line
// never occupies the server's main thread (polyforge#140). This side keeps the
// index, fingerprints and ordering, and hands the file's full or resumed scan
// to the pool.
async function scanSessionFile(
	filePath: string,
	previous?: IndexEntry,
): Promise<Omit<IndexEntry, "fp"> | null> {
	const result = await runSessionScanTask({
		kind: "scan",
		path: filePath,
		// Only a file the caller judged grown carries a resume point; anything
		// else scans from byte 0.
		...(previous?.resume
			? {
				previous: {
					info: {
						id: previous.info.id,
						cwd: previous.info.cwd,
						created: previous.info.created.toISOString(),
						...(previous.info.name !== undefined ? { name: previous.info.name } : {}),
						...(previous.info.parentSessionPath !== undefined
							? { parentSessionPath: previous.info.parentSessionPath }
							: {}),
						firstMessage: previous.info.firstMessage,
						messageCount: previous.info.messageCount,
					},
					resume: previous.resume,
				},
			}
			: {}),
	}) as SessionScanTaskResult | null;
	if (!result?.info) return null;
	return {
		info: result.info,
		...(result.resume ? { resume: result.resume } : {}),
	};
}

export async function scanSessionFileInfo(
	filePath: string,
): Promise<ScannedSessionInfo | null> {
	return (await scanSessionFile(filePath))?.info ?? null;
}

async function enumerateSessionFiles(sessionsDir: string): Promise<string[]> {
	let dirs: Dirent[];
	try {
		const entries = await readdir(sessionsDir, { withFileTypes: true });
		dirs = entries.filter(
			(entry) => entry.isDirectory() || entry.isSymbolicLink(),
		);
	} catch {
		return [];
	}

	const files: string[] = [];
	for (const dir of dirs) {
		const dirPath = join(sessionsDir, dir.name);
		try {
			for (const f of await readdir(dirPath)) {
				if (f.endsWith(".jsonl")) files.push(join(dirPath, f));
			}
		} catch {
			// unreadable project dir: same skip-as-absent semantics as the SDK
		}
	}
	return files;
}

const MAX_CONCURRENT_SCANS = 10;

async function runPool<T>(items: T[], worker: (item: T) => Promise<void>) {
	let next = 0;
	const inFlight = new Set<Promise<void>>();
	while (next < items.length || inFlight.size > 0) {
		while (next < items.length && inFlight.size < MAX_CONCURRENT_SCANS) {
			const item = items[next++];
			const task = worker(item).finally(() => inFlight.delete(task));
			inFlight.add(task);
		}
		if (inFlight.size > 0) await Promise.race(inFlight);
	}
}

function getIndex(): Map<string, IndexEntry> {
	if (!globalThis.__piWebScanIndex) globalThis.__piWebScanIndex = new Map();
	return globalThis.__piWebScanIndex;
}

function indexFilePath(agentDir = getAgentDir()): string {
	return join(agentDir, "pi-web-session-index.json");
}

/** A persisted resume point, or undefined when it is missing or malformed (the entry stays). */
function readResumePoint(value: unknown): ResumePoint | undefined {
	if (!isRecord(value)) return undefined;
	const { ino, offset, headerEnd, check, hasFirstMessage, lastActivityTime } = value;
	if (
		typeof ino !== "number" || !Number.isFinite(ino) || ino <= 0 ||
		typeof headerEnd !== "number" || !Number.isSafeInteger(headerEnd) || headerEnd <= 0 ||
		typeof offset !== "number" || !Number.isSafeInteger(offset) || offset < headerEnd ||
		typeof check !== "string" ||
		typeof hasFirstMessage !== "boolean" ||
		(lastActivityTime !== undefined && (typeof lastActivityTime !== "number" || !Number.isFinite(lastActivityTime)))
	) return undefined;
	return {
		ino,
		offset,
		headerEnd,
		check,
		hasFirstMessage,
		...(lastActivityTime !== undefined ? { lastActivityTime } : {}),
	};
}

function loadPersistedIndex(): void {
	if (globalThis.__piWebScanIndexLoaded) return;
	globalThis.__piWebScanIndexLoaded = true;
	const path = indexFilePath();
	if (!existsSync(path)) return;
	try {
		const parsed: unknown = JSON.parse(readFileSync(path, "utf8"));
		if (!isRecord(parsed) || parsed.version !== INDEX_FORMAT_VERSION || !isRecord(parsed.entries)) return;
		const index = getIndex();
		for (const [pathKey, entry] of Object.entries(parsed.entries)) {
			if (!isRecord(entry) || !isRecord(entry.fp) || !isRecord(entry.info)) continue;
			const { fp, info } = entry;
			if (
				typeof fp.size !== "number" || !Number.isSafeInteger(fp.size) || fp.size < 0 ||
				typeof fp.mtimeMs !== "number" || !Number.isFinite(fp.mtimeMs) ||
				info.path !== pathKey ||
				typeof info.id !== "string" ||
				typeof info.cwd !== "string" ||
				typeof info.firstMessage !== "string" ||
				(info.name !== undefined && typeof info.name !== "string") ||
				(info.parentSessionPath !== undefined && typeof info.parentSessionPath !== "string") ||
				typeof info.messageCount !== "number" || !Number.isSafeInteger(info.messageCount) || info.messageCount < 0 ||
				typeof info.created !== "string" || typeof info.modified !== "string"
			) continue;
			const created = new Date(info.created);
			const modified = new Date(info.modified);
			if (!Number.isFinite(created.getTime()) || !Number.isFinite(modified.getTime())) continue;
			index.set(pathKey, {
				fp: { size: fp.size, mtimeMs: fp.mtimeMs },
				info: {
					path: pathKey,
					id: info.id,
					cwd: info.cwd,
					name: info.name,
					parentSessionPath: info.parentSessionPath,
					firstMessage: info.firstMessage,
					messageCount: info.messageCount,
					created,
					modified,
				},
				// Entries from before resume points get one full scan when they change.
				resume: readResumePoint(entry.resume),
			});
		}
	} catch {
		// corrupt index => cold rebuild, never an error surfaced to callers
	}
}

function persistIndex(): void {
	const pending = globalThis.__piWebScanIndexSave;
	if (!pending) return;
	clearTimeout(pending.timer);
	globalThis.__piWebScanIndexSave = undefined;
	try {
		writePrivateFileAtomicSync(
			pending.path,
			JSON.stringify({ version: INDEX_FORMAT_VERSION, entries: Object.fromEntries(pending.entries) }),
		);
	} catch {
		// persistence is best-effort; the in-memory index remains authoritative
	}
}

/**
 * Write the index to `path` once INDEX_PERSIST_DELAY_MS has passed, with what
 * it holds now. Later scans of the same agent dir only replace what is written;
 * a scan of another one sends the pending write to its own dir first.
 */
function queueIndexPersist(path: string): void {
	const entries = [...getIndex()];
	const pending = globalThis.__piWebScanIndexSave;
	if (pending?.path === path) {
		pending.entries = entries;
		return;
	}
	persistIndex();
	const timer = setTimeout(persistIndex, INDEX_PERSIST_DELAY_MS);
	// A pending write never holds a process open: a lost one means a rescan.
	timer.unref?.();
	globalThis.__piWebScanIndexSave = { path, entries, timer };
}

/**
 * Incremental equivalent of SessionManager.listAll(): rescans only files whose
 * (size, mtimeMs) changed since the last pass. Output ordering matches the SDK
 * catalogue (modified descending).
 */
export async function listSessionsIncremental(
	options: { deferDetails?: boolean } = {},
): Promise<ScannedSessionInfo[]> {
	loadPersistedIndex();
	const deferDetails = options.deferDetails ?? false;

	const agentDir = getAgentDir();
	const sessionsDir = join(agentDir, "sessions");
	const files = await enumerateSessionFiles(sessionsDir);
	globalThis.__piSessionMembership = { files: new Set(files) };

	const index = getIndex();
	const present = new Set(files);
	const stale: string[] = [];
	for (const known of index.keys()) {
		if (!present.has(known)) stale.push(known);
	}
	for (const pathKey of stale) index.delete(pathKey);

	const fingerprints = await Promise.all(
		files.map(async (filePath) => {
			try {
				const s = await stat(filePath);
				return {
					filePath,
					fp: { size: s.size, mtimeMs: s.mtimeMs } as Fingerprint,
				};
			} catch {
				return { filePath, fp: null as Fingerprint | null };
			}
		}),
	);

	const deferred: Array<{ filePath: string; fp: Fingerprint; resultIndex: number }> = [];
	const changed: Array<{ filePath: string; fp: Fingerprint; resultIndex: number; previous?: IndexEntry }> = [];
	const results: (ScannedSessionInfo | null)[] = new Array(files.length).fill(null);
	const mtimes: number[] = new Array(files.length).fill(Number.NEGATIVE_INFINITY);
	for (const [resultIndex, { filePath, fp }] of fingerprints.entries()) {
		if (!fp) {
			index.delete(filePath);
			continue;
		}
		mtimes[resultIndex] = fp.mtimeMs;
		const cached = index.get(filePath);
		if (hasMatchingFingerprint(cached, fp)) {
			// Complete details are already in memory and the index is persisted
			// across restarts, so on the common warm path every unchanged file
			// can paint its real count and first message immediately. Blanking
			// them would cost a request to get back what we are already holding.
			results[resultIndex] = cached.info;
			continue;
		}
		if (deferDetails) {
			// Header and stat only: a later normal listing fills in the transcript
			// details, so a first paint does not wait on parsing every file.
			deferred.push({ filePath, fp, resultIndex });
			continue;
		}
		// A running session's file grows on every append: parse only the tail.
		changed.push({ filePath, fp, resultIndex, previous: hasGrown(cached, fp) ? cached : undefined });
	}

	await runPool(deferred, async ({ filePath, fp, resultIndex }) => {
		const summary = await deferredSessionInfo(filePath, fp);
		if (summary) results[resultIndex] = summary;
		else index.delete(filePath);
	});

	await runPool(changed, async ({ filePath, fp, resultIndex, previous }) => {
		const scanned = await scanSessionFile(filePath, previous);
		if (scanned) {
			index.set(filePath, { fp, ...scanned });
			results[resultIndex] = scanned.info;
		} else {
			index.delete(filePath);
		}
	});

	if (changed.length > 0 || stale.length > 0) queueIndexPersist(indexFilePath(agentDir));

	// Preserve catalogue order for timestamp ties, independently of cache hits
	// and the order in which concurrent file reads complete. Since pi 0.86 the
	// SDK reads files newest-mtime first (then reverse filename) so resume can
	// show results progressively, and its stable sort keeps that order for
	// sessions with equal activity time.
	return results
		.flatMap((info, resultIndex) => (info ? [{ info, mtimeMs: mtimes[resultIndex] }] : []))
		.sort((a, b) =>
			b.info.modified.getTime() - a.info.modified.getTime()
			|| b.mtimeMs - a.mtimeMs
			|| basename(b.info.path).localeCompare(basename(a.info.path)),
		)
		.map(({ info }) => info);
}

/** Test seam: write a pending index save now instead of after its delay. */
export function flushSessionScanIndexForTests(): void {
	persistIndex();
}

/** Test seam: drop all in-memory index state and any pending index save. */
export function resetSessionScanIndexForTests(): void {
	clearTimeout(globalThis.__piWebScanIndexSave?.timer);
	globalThis.__piWebScanIndex = undefined;
	globalThis.__piWebScanIndexLoaded = undefined;
	globalThis.__piWebScanIndexSave = undefined;
	globalThis.__piSessionMembership = undefined;
	globalThis.__piSessionMembershipCheck = undefined;
}

// ============================================================================
// Filename membership: sessions another process creates or deletes.
// ============================================================================

declare global {
	/** The session files the last scan listed (or the last change a check reported). */
	var __piSessionMembership: { files: Set<string> } | undefined;
	var __piSessionMembershipCheck: { at: number; busy: boolean } | undefined;
}

export const SESSION_MEMBERSHIP_CHECK_MS = 1_000;

/**
 * Compare the session files on disk with the last scan's and call `onChange`
 * once when they differ. Directory reads only: appends to a transcript never
 * change its name, so a running session costs nothing here. At most one check
 * per SESSION_MEMBERSHIP_CHECK_MS, never two at once, and none before the
 * first scan. Returns the check's promise, or undefined when none started.
 */
export function checkSessionMembership(
	sessionsDir: string,
	onChange: () => void,
	now = Date.now(),
): Promise<void> | undefined {
	const known = globalThis.__piSessionMembership;
	const state = (globalThis.__piSessionMembershipCheck ??= { at: Number.NEGATIVE_INFINITY, busy: false });
	if (!known || state.busy || now - state.at < SESSION_MEMBERSHIP_CHECK_MS) return undefined;
	state.busy = true;
	state.at = now;
	return enumerateSessionFiles(sessionsDir)
		.then((files) => {
			// A scan recorded while this read ran replaces the baseline: drop this
			// result, the next check compares against the scan's own listing.
			if (globalThis.__piSessionMembership !== known) return;
			if (known.files.size === files.length && files.every((file) => known.files.has(file))) return;
			// Record the change so it bumps once, not on every check until a rescan.
			globalThis.__piSessionMembership = { files: new Set(files) };
			onChange();
		})
		.catch(() => undefined)
		.finally(() => {
			state.busy = false;
		});
}
