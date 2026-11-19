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

parentPort.on("message", function (task) {
	(async function () {
		if (task.kind === "scan") {
			const result = await scanSessionFile(task.path, task.previous);
			parentPort.postMessage({ id: task.id, ok: true, result });
			return;
		}
		if (task.kind === "claude-entries") {
			// A claude transcript's entries, parsed exactly as the adapter's
			// parseClaudeTranscript() splits them: whole lines, blanks skipped,
			// unreadable lines left out. The main thread never reads the text.
			const entries = [];
			let text = "";
			try {
				text = readFileSync(task.path, "utf8");
			} catch {
				parentPort.postMessage({ id: task.id, ok: true, result: entries });
				return;
			}
			for (const line of text.split("\n")) {
				if (!line.trim()) continue;
				try {
					entries.push(JSON.parse(line));
				} catch {
					// A line claude is still writing.
				}
			}
			parentPort.postMessage({ id: task.id, ok: true, result: entries });
			return;
		}
		parentPort.postMessage({ id: task.id, ok: false, error: "unknown task kind: " + String(task.kind) });
	})().catch(function (error) {
		parentPort.postMessage({ id: task.id, ok: false, error: String((error && error.stack) || error) });
	});
});
`;
