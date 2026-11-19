// The worker-side implementation of a pi session file's scan, as one plain
// JavaScript source string: `new Worker(SESSION_SCAN_WORKER_SOURCE, { eval: true })`
// runs it (lib/session-parse-worker-pool.ts), so the parse of a session
// transcript — a single 20 MiB JSON line included — never occupies the server's
// main thread. The string is the only copy of the scan: the TypeScript scanner
// (lib/session-list-scanner.ts) keeps the index, fingerprints and ordering and
// hands every full or resumed parse to this source through the pool.
//
// `require` works in an eval worker; the `parentPort` global does not, so the
// first line requires it. Every message is one task `{ id, kind, ... }`; the
// answer is `{ id, ok: true, result }` or `{ id, ok: false, error }`, and a
// `scan` result carries `readStarts`: the byte offsets its stream reads began
// at, so the main thread's task log records the same facts the old
// `createReadStream` interception did.
export const SESSION_SCAN_WORKER_SOURCE = String.raw`
"use strict";
const { parentPort } = require("node:worker_threads");
const { createHash } = require("node:crypto");
const { createReadStream, readFileSync } = require("node:fs");
const { open, stat } = require("node:fs/promises");

const RESUME_SAMPLE_BYTES = 512;
const NEWLINE = Buffer.from("\n");

function parseLine(line) {
	if (!line.trim()) return null;
	try {
		const entry = JSON.parse(line);
		return entry && typeof entry === "object" ? entry : null;
	} catch {
		return null;
	}
}

function extractTextContent(message) {
	const content = message.content;
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	return content
		.filter(function (block) {
			return !!block &&
				typeof block === "object" &&
				block.type === "text" &&
				typeof block.text === "string";
		})
		.map(function (block) { return block.text; })
		.join(" ");
}

function activityTimeOf(entry) {
	const message = entry.message;
	if (
		!message ||
		typeof message.role !== "string" ||
		!("content" in message) ||
		(message.role !== "user" && message.role !== "assistant")
	) {
		return undefined;
	}
	if (typeof message.timestamp === "number") return message.timestamp;
	const t = new Date(entry.timestamp).getTime();
	return Number.isNaN(t) ? undefined : t;
}

function readHeader(entry, head) {
	return {
		id: entry.id,
		cwd: typeof entry.cwd === "string" ? entry.cwd : "",
		created: new Date(entry.timestamp),
		parentSessionPath: typeof entry.parentSession === "string" ? entry.parentSession : undefined,
		time: typeof entry.timestamp === "string" ? new Date(entry.timestamp).getTime() : NaN,
		// A resumed scan rebuilds the header from the row, which holds it
		// faithfully only for a string id and timestamp.
		head: typeof entry.id === "string" && typeof entry.timestamp === "string" ? head : undefined,
	};
}

// Uses the SDK's buildSessionInfo() semantics for displayed metadata. A change
// to these rules must bump the scanner's INDEX_FORMAT_VERSION (resumed totals
// keep the old ones).
function foldEntry(totals, entry) {
	if (entry.type === "session_info") {
		totals.name =
			typeof entry.name === "string" && entry.name.trim()
				? entry.name.trim()
				: undefined;
	}
	if (entry.type !== "message") return;
	totals.messageCount++;

	const activityTime = activityTimeOf(entry);
	if (typeof activityTime === "number") {
		totals.lastActivityTime = Math.max(totals.lastActivityTime ?? 0, activityTime);
	}

	if (totals.firstMessage) return;
	const message = entry.message;
	if (
		!message ||
		typeof message.role !== "string" ||
		!("content" in message)
	)
		return;
	if (message.role !== "user") return;
	totals.firstMessage = extractTextContent(message);
}

function sampleCheck(head, tail) {
	return createHash("sha1").update(head).update(tail).digest("base64");
}

// Drop leading parts not needed for the last RESUME_SAMPLE_BYTES of 'parts'.
function keepLastBytes(parts) {
	let total = 0;
	for (const part of parts) total += part.length;
	let first = 0;
	while (first < parts.length - 1 && total - parts[first].length >= RESUME_SAMPLE_BYTES) {
		total -= parts[first].length;
		first++;
	}
	return first > 0 ? parts.slice(first) : parts;
}

// One newline-free run of bytes split into lines as the scan's readline read
// did on Node 22: \r\n and a lone \r end a line. U+2028 and U+2029, which
// JSON.stringify leaves raw and Node 24's readline splits on, stay inside the
// line, as pi's own loader keeps them.
function linesOf(bytes) {
	const text = bytes.toString("utf8");
	return text.includes("\r") ? text.split("\r") : [text];
}

// Hand each newline-terminated line from 'start' on to 'onLine', with the
// offset just past its newline. Returns null when 'onLine' returns false.
// Every stream this opens is recorded in 'readStarts'.
async function readLines(filePath, start, sample, readStarts, onLine) {
	let offset = start;
	let pending = [];
	// The bytes read up to 'offset', as views, trimmed to what the sample needs
	// once per chunk (not per line: a full scan of short lines stays as fast).
	let read = [sample];
	readStarts.push(start);
	for await (const chunk of createReadStream(filePath, { start })) {
		let newline = chunk.indexOf(0x0a);
		if (newline === -1) {
			pending.push(chunk);
			continue;
		}
		const carried = pending;
		let lineStart = 0;
		while (newline !== -1) {
			const piece = chunk.subarray(lineStart, newline);
			const bytes = pending.length > 0 ? Buffer.concat([...pending, piece]) : piece;
			pending = [];
			offset += bytes.length + 1;
			if (!onLine(bytes, offset)) return null;
			lineStart = newline + 1;
			newline = chunk.indexOf(0x0a, lineStart);
		}
		read = keepLastBytes([...read, ...carried, chunk.subarray(0, lineStart)]);
		if (lineStart < chunk.length) pending.push(chunk.subarray(lineStart));
	}
	const joined = Buffer.concat(read);
	return {
		offset,
		sample: joined.subarray(Math.max(0, joined.length - RESUME_SAMPLE_BYTES)),
		rest: Buffer.concat(pending),
	};
}

// The header line and the bytes before the resume offset, when they still hash
// to the resume point's check; null sends the caller to a full scan.
async function readResumeSample(filePath, resume) {
	const handle = await open(filePath, "r");
	try {
		const tailStart = Math.max(0, resume.offset - RESUME_SAMPLE_BYTES);
		const head = Buffer.alloc(resume.headerEnd);
		const tail = Buffer.alloc(resume.offset - tailStart);
		const headRead = await handle.read(head, 0, head.length, 0);
		const tailRead = await handle.read(tail, 0, tail.length, tailStart);
		if (headRead.bytesRead !== head.length || tailRead.bytesRead !== tail.length) return null;
		return sampleCheck(head, tail) === resume.check ? { head, tail } : null;
	} finally {
		await handle.close();
	}
}

// Scan a session file for its row, from where 'previous' stopped when the
// file only grew since and its sampled prefix is unchanged, else from byte 0.
// Either way the row is the one a full read of the file gives.
// 'previous', when given, is the serializable subset of the scanner's index
// entry: the row's identity and the resume point a resumed scan rebuilds from.
async function scanSessionFile(filePath, previous) {
	const readStarts = [];
	try {
		const stats = await stat(filePath);
		const resumed = previous && previous.resume && previous.resume.ino === stats.ino
			? await readResumeSample(filePath, previous.resume)
			: null;
		let header = null;
		let headerEnd = 0;
		let start = 0;
		let sample = Buffer.alloc(0);
		const totals = { messageCount: 0, firstMessage: "" };
		if (previous && previous.resume && resumed) {
			const info = previous.info;
			const created = new Date(info.created);
			start = previous.resume.offset;
			sample = resumed.tail;
			header = {
				id: info.id,
				cwd: info.cwd,
				created,
				parentSessionPath: info.parentSessionPath,
				time: created.getTime(),
				head: resumed.head,
			};
			headerEnd = previous.resume.headerEnd;
			totals.name = info.name;
			totals.messageCount = info.messageCount;
			totals.firstMessage = previous.resume.hasFirstMessage ? info.firstMessage : "";
			totals.lastActivityTime = previous.resume.lastActivityTime;
		}

		let restCounted = false;
		const parseLines = function (bytes, end) {
			for (const line of linesOf(bytes)) {
				const entry = parseLine(line);
				if (!entry) continue;
				if (end === null) restCounted = true;
				if (!header) {
					if (entry.type !== "session") return false;
					// Only a header on the file's first line is sampled for resuming.
					const firstLine = end !== null && end === bytes.length + 1;
					header = readHeader(entry, firstLine ? Buffer.concat([bytes, NEWLINE]) : undefined);
					headerEnd = firstLine ? end : 0;
					continue;
				}
				foldEntry(totals, entry);
			}
			return true;
		};

		const scan = await readLines(filePath, start, sample, readStarts, parseLines);
		// The unterminated rest counts for this listing, as it did when the scan
		// read lines with readline; a resume point is kept only when it changed nothing.
		if (!scan || (scan.rest.length > 0 && !parseLines(scan.rest, null))) {
			return { info: null, resume: undefined, readStarts };
		}
		if (!header) return { info: null, resume: undefined, readStarts };
		const modified =
			typeof totals.lastActivityTime === "number" && totals.lastActivityTime > 0
				? new Date(totals.lastActivityTime)
				: !Number.isNaN(header.time)
					? new Date(header.time)
					: stats.mtime;

		const info = {
			path: filePath,
			id: header.id,
			cwd: header.cwd,
			name: totals.name,
			parentSessionPath: header.parentSessionPath,
			created: header.created,
			modified,
			messageCount: totals.messageCount,
			firstMessage: totals.firstMessage || "(no messages)",
		};
		// An inode of 0 (no stable one on this filesystem) never resumes.
		const resume = header.head && headerEnd > 0 && !restCounted && stats.ino > 0
			? {
				ino: stats.ino,
				offset: scan.offset,
				headerEnd,
				check: sampleCheck(header.head, scan.sample),
				hasFirstMessage: totals.firstMessage !== "",
				...(totals.lastActivityTime !== undefined ? { lastActivityTime: totals.lastActivityTime } : {}),
			}
			: undefined;
		return { info, resume, readStarts };
	} catch {
		return { info: null, resume: undefined, readStarts };
	}
}


// ---------------------------------------------------------------------------
// A claude transcript's projected message count (the adapter's
// claudeTranscriptToMessages(entries, {}).messages.length), ported for the
// worker: the active branch (claudeActiveBranch()), then one count per row the
// walk would emit. Only the rules that change HOW MANY messages a row emits are
// ported; delivery decorations (queued / absorbed markers) and the run/note
// bookkeeping do not change the count. lib/claude-session-list.test.mjs
// cross-checks this port against the adapter over every transcript fixture.

function isObjectCount(value) {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

// A row of claude's conversation tree: a main-conversation entry of a chained type with its own uuid.
function treeRow(entry) {
	if (!isObjectCount(entry) || typeof entry.uuid !== "string" || !entry.uuid || entry.isSidechain === true) return null;
	const type = entry.type;
	return type === "user" || type === "assistant" || type === "system" || type === "attachment" ? entry : null;
}

function isCompactBoundary(entry) {
	return entry.type === "system" && entry.subtype === "compact_boundary";
}

function chainParentOf(entry) {
	return entry && typeof entry.parentUuid === "string" && entry.parentUuid ? entry.parentUuid : undefined;
}

function treeParentOf(entry) {
	return chainParentOf(entry)
		?? (isCompactBoundary(entry) && typeof entry.logicalParentUuid === "string" && entry.logicalParentUuid ? entry.logicalParentUuid : undefined);
}

// A user entry that starts a turn (a prompt, claude's follow-up anchor or its
// command bookkeeping); never a tool result or a hidden companion.
function startsTurn(entry) {
	return entry.type === "user" && entry.isMeta !== true && entry.isCompactSummary !== true
		&& isObjectCount(entry.message) && toolResultBlocks(entry.message.content).length === 0;
}

// claudeActiveBranch(), ported verbatim from lib/claude-adapter.ts.
function claudeActiveBranch(entries) {
	const rows = new Map();
	let checkpoint = undefined;
	let checkpointExplicit = false;
	let checkpointKeyed = false;
	let lastRow = undefined;
	let lastMessage = undefined;
	let newestRow = undefined;
	let newestAt = "";
	for (const raw of entries) {
		const row = treeRow(raw);
		if (row) {
			rows.set(row.uuid, row);
			const forkBriefing = row.type === "attachment" && isObjectCount(row.attachment) && row.attachment.type === "fork_briefing";
			if (!forkBriefing) {
				lastRow = row.uuid;
				checkpointExplicit = false;
				if (typeof row.timestamp === "string" && row.timestamp > newestAt) {
					newestRow = row.uuid;
					newestAt = row.timestamp;
				}
			}
			if (row.type === "user" || row.type === "assistant") lastMessage = row.uuid;
			if (isCompactBoundary(row)) {
				checkpoint = undefined;
				checkpointExplicit = false;
			}
			continue;
		}
		if (isObjectCount(raw) && raw.type === "last-prompt") {
			if ("leafUuid" in raw) checkpointKeyed = true;
			if (typeof raw.leafUuid === "string" && raw.leafUuid) {
				checkpointExplicit = raw.explicit === true || (checkpointExplicit && raw.leafUuid === checkpoint);
				checkpoint = raw.leafUuid;
			}
		}
	}

	let anchor = lastRow;
	if (checkpoint && checkpointExplicit && rows.has(checkpoint)) {
		anchor = checkpoint;
	} else if (!checkpointKeyed && lastRow && newestRow && newestRow !== lastRow) {
		const target = lastRow;
		const under = new Map([[target, true]]);
		const isUnder = (from) => {
			const walked = new Set();
			let found = false;
			for (let at = from; at !== undefined; at = chainParentOf(rows.get(at))) {
				const known = under.get(at);
				if (known !== undefined) {
					found = known;
					break;
				}
				if (walked.has(at)) break;
				walked.add(at);
			}
			for (const at of walked) under.set(at, found);
			return found;
		};
		let bestAt = "";
		for (const row of rows.values()) {
			if (row.uuid === target || typeof row.timestamp !== "string" || row.timestamp < bestAt || !isUnder(row.uuid)) continue;
			anchor = row.uuid;
			bestAt = row.timestamp;
		}
	}

	let leaf = null;
	const seen = new Set();
	for (let at = anchor; at && rows.has(at) && !seen.has(at); at = chainParentOf(rows.get(at))) {
		seen.add(at);
		const row = rows.get(at);
		if (row.type === "user" || row.type === "assistant") {
			leaf = at;
			break;
		}
	}
	if (leaf === null) leaf = lastMessage ?? null;

	const path = [];
	const onPath = new Set();
	const resumable = new Set();
	let crossedBoundary = false;
	for (let at = leaf ?? undefined; at && rows.has(at) && !onPath.has(at);) {
		const row = rows.get(at);
		path.push(at);
		onPath.add(at);
		if (!crossedBoundary) resumable.add(at);
		if (isCompactBoundary(row)) crossedBoundary = true;
		at = treeParentOf(row);
	}

	const children = new Map();
	for (const row of rows.values()) {
		const parent = treeParentOf(row);
		if (!parent) continue;
		const list = children.get(parent);
		if (list) list.push(row.uuid);
		else children.set(parent, [row.uuid]);
	}
	const subtree = (root) => {
		const found = [];
		const reached = new Set();
		for (const pending = [root]; pending.length > 0;) {
			const next = pending.pop();
			if (reached.has(next) || onPath.has(next)) continue;
			reached.add(next);
			found.push(next);
			pending.push(...(children.get(next) ?? []));
		}
		return found;
	};
	const hidden = new Set();
	for (const at of path) {
		for (const child of children.get(at) ?? []) {
			if (onPath.has(child) || hidden.has(child)) continue;
			const row = rows.get(child);
			if (startsTurn(row)) {
				for (const id of subtree(child)) hidden.add(id);
			} else if (row.type === "attachment" || row.type === "system") {
				const under = subtree(child);
				if (under.some((id) => startsTurn(rows.get(id)))) for (const id of under) hidden.add(id);
			}
		}
	}
	return { leaf, path, resumable, hidden };
}

// The entry predicates the walk's emission count depends on.
function toolResultBlocks(content) {
	return Array.isArray(content) ? content.filter((block) => isObjectCount(block) && block.type === "tool_result") : [];
}

function textOfCount(content) {
	if (typeof content === "string") return content;
	let out = "";
	if (!Array.isArray(content)) return out;
	for (const block of content) {
		if (!isObjectCount(block)) continue;
		if (block.type === "text" && typeof block.text === "string") out += (out ? "\n" : "") + block.text;
	}
	return out;
}

// claude's bookkeeping of a command it ran itself: no user typed it, no bubble.
function isCommandBookkeeping(entry, message) {
	if (entry.type !== "user") return false;
	if (entry.isCompactSummary === true || entry.isVisibleInTranscriptOnly === true) return true;
	return entry.promptSource !== "sdk"
		&& typeof message.content === "string"
		&& /^<(command-name|local-command-stdout|local-command-stderr|local-command-caveat)>/.test(message.content);
}

// How many messages subagentEntries() emits for one conversation row.
function emitCount(type, message) {
	if (type === "assistant") {
		// toAssistantBlocks(): a plain string is one block when non-empty; an
		// array keeps text/thinking/tool_use blocks. Only emptiness matters.
		if (typeof message.content === "string") return message.content ? 1 : 0;
		if (!Array.isArray(message.content)) return 0;
		for (const block of message.content) {
			if (!isObjectCount(block)) continue;
			// toAssistantBlocks() has no image branch: an image-only assistant
			// content emits nothing (review r2 N1); a tool_use needs only a
			// string id, the empty string included.
			if (block.type === "text" && typeof block.text === "string") return 1;
			if (block.type === "thinking" && typeof block.thinking === "string" && block.thinking) return 1;
			if (block.type === "tool_use" && typeof block.id === "string") return 1;
		}
		return 0;
	}
	const results = toolResultBlocks(message.content);
	if (results.length > 0) return results.length;
	if (typeof message.content === "string") return message.content ? 1 : 0;
	if (!Array.isArray(message.content)) return 0;
	for (const block of message.content) {
		if (!isObjectCount(block)) continue;
		if (block.type === "text" && typeof block.text === "string") return 1;
		if (block.type === "image" && isObjectCount(block.source) && typeof block.source.data === "string") return 1;
	}
	return 0;
}

// The transcript's projected message count: what
// claudeTranscriptToMessages(entries, {}).messages.length is on the main thread.
function countTranscript(entries) {
	const branch = claudeActiveBranch(entries);
	const hidden = branch.hidden;
	let count = 0;
	for (const raw of entries) {
		if (hidden.size > 0 && isObjectCount(raw) && raw.isSidechain !== true && typeof raw.uuid === "string" && hidden.has(raw.uuid)) continue;
		if (!isObjectCount(raw)) continue;
		if (raw.type === "system" && raw.subtype === "compact_boundary") {
			count += 1;
			continue;
		}
		if (raw.type === "queue-operation") continue;
		if (raw.type === "attachment" && isObjectCount(raw.attachment) && raw.isSidechain !== true) {
			const attachment = raw.attachment;
			if (attachment.type !== "queued_command") continue;
			if (attachment.isMeta === true) continue;
			const prompt = attachment.prompt;
			if (typeof prompt === "string" && prompt.trimStart().startsWith("<task-notification>")) continue;
			if (typeof prompt === "string" || Array.isArray(prompt)) {
				if (attachment.commandMode !== undefined && attachment.commandMode !== "prompt") continue;
				count += emitCount("user", { content: prompt });
			}
			continue;
		}
		if (raw.type !== "user" && raw.type !== "assistant") continue;
		if (raw.isSidechain === true) continue;
		// On the main walk a meta entry is never a coordinator message (that
		// needs isSidechain), so isMeta excludes the row outright.
		if (raw.isMeta === true) continue;
		if (!isObjectCount(raw.message)) continue;
		if (isCommandBookkeeping(raw, raw.message)) continue;
		if (raw.type === "user") {
			const text = typeof raw.message.content === "string" ? raw.message.content : textOfCount(raw.message.content);
			if (raw.turnOrigin === "task_notification" || text.trimStart().startsWith("<task-notification>")) {
				count += 1;
				continue;
			}
		}
		count += emitCount(raw.type, raw.message);
	}
	return count;
}


const SUMMARY_HEADER_MAX_BYTES = 64 * 1024;

// A session row from the header line and stat alone (deferredSessionInfo()).
async function headerSummary(filePath, fp) {
	let handle;
	try {
		handle = await open(filePath, "r");
		const buffer = Buffer.alloc(SUMMARY_HEADER_MAX_BYTES);
		const read = await handle.read(buffer, 0, buffer.length, 0);
		if (read.bytesRead <= 0) return null;
		const source = buffer.subarray(0, read.bytesRead).toString("utf8");
		for (const raw of source.split("\n")) {
			const line = raw.replace(/\r$/, "");
			if (!line.trim()) continue;
			let entry;
			try {
				entry = JSON.parse(line);
			} catch {
				continue;
			}
			// A line that parses to a JSON primitive is not a header: skip it
			// and keep looking, as the old main-thread reader did (r2 N1).
			if (!entry || typeof entry !== "object" || entry.type !== "session") continue;
			if (typeof entry.id !== "string" || typeof entry.cwd !== "string" || typeof entry.timestamp !== "string") return null;
			const created = new Date(entry.timestamp);
			if (!Number.isFinite(created.getTime())) return null;
			return {
				path: filePath,
				id: entry.id,
				cwd: entry.cwd,
				...(typeof entry.parentSession === "string" ? { parentSessionPath: entry.parentSession } : {}),
				created,
				modified: new Date(fp.mtimeMs),
				messageCount: 0,
				firstMessage: "",
				detailsPending: true,
			};
		}
		return null;
	} catch {
		return null;
	} finally {
		if (handle) await handle.close().catch(() => undefined);
	}
}

parentPort.on("message", function (task) {
	(async function () {
		if (task.kind === "scan") {
			const result = await scanSessionFile(task.path, task.previous);
			parentPort.postMessage({ id: task.id, ok: true, result });
			return;
		}
		if (task.kind === "header") {
			// A summary listing's deferred row: the file's identity from its
			// first 64 KiB alone, no transcript parse. The main thread caches it
			// by fingerprint, so an unchanged file is never re-read (review r2#8).
			parentPort.postMessage({ id: task.id, ok: true, result: await headerSummary(task.path, task.fp) });
			return;
		}
		if (task.kind === "claude-count") {
			// A claude transcript's projected message count, computed here so
			// only the number crosses the thread: the main thread never holds
			// the text or the parsed entries (polyforge#140 review r2#5). The
			// parse follows the adapter's parseClaudeTranscript(); the count
			// ports claudeTranscriptToMessages()'s emission rules, cross-checked
			// against the adapter over every transcript fixture the repo holds.
			let text = "";
			try {
				text = readFileSync(task.path, "utf8");
			} catch {
				parentPort.postMessage({ id: task.id, ok: true, result: 0 });
				return;
			}
			const entries = [];
			for (const line of text.split("\n")) {
				if (!line.trim()) continue;
				try {
					entries.push(JSON.parse(line));
				} catch {
					// A line claude is still writing.
				}
			}
			parentPort.postMessage({ id: task.id, ok: true, result: countTranscript(entries) });
			return;
		}
		parentPort.postMessage({ id: task.id, ok: false, error: "unknown task kind: " + String(task.kind) });
	})().catch(function (error) {
		parentPort.postMessage({ id: task.id, ok: false, error: String((error && error.stack) || error) });
	});
});
`;
