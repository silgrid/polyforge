#!/usr/bin/env node
// polyforge#140: the synthetic session corpus the session-list oracles run on.
//
//   node scripts/session-list-fixture.mjs --root "$F" --pi-files 1000 \
//     --pi-bytes 734003200 --claude-files 50 --claude-bytes 314572800 \
//     --large-file-min-bytes 22020096 --manifest "$O/manifest.json"
//
// Writes real JSONL only — no sparse files, no padding whitespace: every pi
// file opens with a session header and carries user/assistant/toolResult
// messages, fork rows (parentSession), subagent meta rows and one U+2028 line;
// every claude transcript is claude 2.1.3x-shaped (user/assistant rows, a
// last-prompt checkpoint, a cost-state tail) and is registered in the
// synthetic agent dir's claude-sessions.json, so it actually appears in the
// list. A few files exceed --large-file-min-bytes with a single huge message
// line — the parse the main thread used to stall on. Distinct, spread mtimes
// are stamped with utimes so the catalogue order is stable.
//
// The manifest is the independent record: actual file count, per-file
// bytes/mtimeMs/sha256 (paths relative to --root) and the totals the read
// budgets (AC2/AC3) are judged against.
import { createHash } from "node:crypto";
import { mkdirSync, readdirSync, utimesSync, writeFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";

function arg(name, fallback) {
  const index = process.argv.indexOf(`--${name}`);
  if (index === -1 || index + 1 >= process.argv.length) {
    if (fallback !== undefined) return fallback;
    usage(`missing --${name}`);
  }
  return process.argv[index + 1];
}

function usage(message) {
  if (message) console.error(`session-list-fixture: ${message}`);
  console.error("usage: node scripts/session-list-fixture.mjs --root DIR --manifest FILE [--pi-files N] [--pi-bytes N] [--claude-files N] [--claude-bytes N] [--large-file-min-bytes N] [--project-dirs N] [--seed N]");
  process.exit(2);
}

const DAY_MS = 24 * 60 * 60 * 1000;

// A small deterministic PRNG (mulberry32): the corpus is reproducible per seed.
function prng(seed) {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const text = (random, length) => {
  const words = ["session", "list", "fixture", "scan", "index", "worker", "message", "transcript", "cache", "fingerprint"];
  let out = "";
  while (out.length < length) out += `${words[Math.floor(random() * words.length)]} `;
  return out.slice(0, Math.max(1, length));
};

function sha256OfFile(content) {
  return createHash("sha256").update(content).digest("hex");
}

const iso = (baseMs, offsetMs) => new Date(baseMs + offsetMs).toISOString();

function piSessionLines({ id, cwd, createdMs, parentSession, bigLineBytes, targetBytes, random }) {
  const lines = [];
  const header = { type: "session", version: 3, id, cwd, timestamp: iso(createdMs, 0) };
  if (parentSession) header.parentSession = parentSession;
  lines.push(JSON.stringify(header));
  let entryId = 0;
  const entry = () => `e${id}-${entryId++}`;
  const addMessage = (role, content, extra = {}) => lines.push(JSON.stringify({
    type: "message", id: entry(), parentId: null, timestamp: iso(createdMs, entryId * 1000), message: { role, content }, ...extra,
  }));
  addMessage("user", text(random, 80 + Math.floor(random() * 200)));
  addMessage("assistant", [{ type: "text", text: text(random, 120) }]);
  addMessage("toolResult", [{ type: "text", text: text(random, 90) }]);
  if (parentSession) lines.push(JSON.stringify({ type: "subagent_meta", agent: "general-purpose", description: text(random, 40) }));
  lines.push(JSON.stringify({ type: "session_info", name: `fixture ${id}` }));
  if (bigLineBytes > 0) {
    // One single JSON line bigger than the large-file bound: the parse that
    // must not occupy the server's main thread.
    addMessage("user", text(random, bigLineBytes));
  }
  // Fill to the byte target with ordinary alternating messages.
  while (Buffer.byteLength(lines.join("\n") + "\n", "utf8") < targetBytes) {
    addMessage(entryId % 2 ? "assistant" : "user", text(random, 200 + Math.floor(random() * 800)));
  }
  // One raw U+2028 stays inside its line, as pi's loader reads it.
  lines.push(JSON.stringify({ type: "message", id: entry(), parentId: null, timestamp: iso(createdMs, entryId * 1000), message: { role: "user", content: "first\u2028second" } }));
  return lines;
}

function claudeTranscriptLines({ id, createdMs, bigLineBytes, targetBytes, random }) {
  const lines = [];
  lines.push(JSON.stringify({ type: "atis-latch", atis: "", sessionId: id }));
  lines.push(JSON.stringify({ type: "queue-operation", operation: "enqueue", timestamp: iso(createdMs, 0), sessionId: id, content: text(random, 60) }));
  let uuidIndex = 0;
  const uuid = () => `${id.slice(0, 8)}-${String(uuidIndex++).padStart(4, "0")}-4aaa-8bbb-cccccccccccc`;
  let parent = null;
  const userRow = (content) => {
    const row = { parentUuid: parent, isSidechain: false, type: "user", message: { role: "user", content }, uuid: uuid(), timestamp: iso(createdMs, uuidIndex * 1000), cwd: "/tmp/claude-fixture", sessionId: id, version: "2.1.312" };
    parent = row.uuid;
    lines.push(JSON.stringify(row));
    return row;
  };
  const assistantRow = (content) => {
    const row = { parentUuid: parent, isSidechain: false, type: "assistant", message: { role: "assistant", content: [{ type: "text", text: content }] }, uuid: uuid(), timestamp: iso(createdMs, uuidIndex * 1000), cwd: "/tmp/claude-fixture", sessionId: id, version: "2.1.312" };
    parent = row.uuid;
    lines.push(JSON.stringify(row));
    return row;
  };
  const first = userRow(text(random, 100));
  assistantRow(text(random, 150));
  if (bigLineBytes > 0) userRow(text(random, bigLineBytes));
  while (Buffer.byteLength(lines.join("\n") + "\n", "utf8") < targetBytes) {
    userRow(text(random, 200 + Math.floor(random() * 600)));
    assistantRow(text(random, 200 + Math.floor(random() * 600)));
  }
  lines.push(JSON.stringify({ type: "last-prompt", lastPrompt: first.message.content, leafUuid: parent, sessionId: id }));
  lines.push(JSON.stringify({ type: "cost-state", sessionId: id, totalCostUSD: 0.01, modelUsage: {} }));
  return lines;
}

function writeJsonl(path, lines) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, lines.map((line) => `${line}\n`).join(""));
}

const root = arg("root");
const manifestPath = arg("manifest");
const piFiles = Number(arg("pi-files", 1000));
const piBytes = Number(arg("pi-bytes", 734003200));
const claudeFiles = Number(arg("claude-files", 50));
const claudeBytes = Number(arg("claude-bytes", 314572800));
const largeFileMinBytes = Number(arg("large-file-min-bytes", 22020096));
const projectDirs = Number(arg("project-dirs", 16));
const seed = Number(arg("seed", 140));
if (![piFiles, piBytes, claudeFiles, claudeBytes].every((n) => Number.isInteger(n) && n >= 0) || piFiles === 0) usage("counts and byte totals must be non-negative integers");
if (claudeBytes > 0 && claudeFiles === 0) usage("--claude-bytes without --claude-files");
const random = prng(seed);

// Deterministic: the corpus is a function of --seed alone (the reproducibility
// test regenerates it and compares hashes), so the time base is seeded too.
const baseMs = new Date("2026-01-01T00:00:00.000Z").getTime() - (seed % 64) * DAY_MS;
const agentDir = join(root, "agent");
const claudeConfigDir = join(root, "claude-config");

// ---- pi sessions -----------------------------------------------------------
const largePiFiles = piFiles >= 8 ? 6 : 1;
const bigLineBytes = Math.max(largeFileMinBytes + 1024 * 1024, Math.floor(piBytes / Math.max(1, piFiles) / 2));
const piPerFile = piFiles > 0 ? Math.max(1, Math.floor((piBytes - largePiFiles * bigLineBytes) / piFiles)) : 0;

const piManifest = [];
let previousPiPath = null;
for (let index = 0; index < piFiles; index += 1) {
  const id = `f${String(index).padStart(6, "0")}-${seed}`;
  const dirName = `project-${index % projectDirs}`;
  const path = join(agentDir, "sessions", dirName, `2026-${String(index % 30).padStart(2, "0")}.jsonl`);
  const createdMs = baseMs + Math.floor((index / piFiles) * 25 * DAY_MS);
  // Every 7th file is a fork of the previous one (parentSession), as a
  // fork's relation row is part of the golden list.
  const parentSession = index > 0 && index % 7 === 0 ? previousPiPath : undefined;
  const lines = piSessionLines({
    id, cwd: join(root, dirName), createdMs, parentSession,
    bigLineBytes: index < largePiFiles ? bigLineBytes : 0,
    targetBytes: piPerFile, random,
  });
  writeJsonl(path, lines);
  const mtimeMs = createdMs + 3600_000;
  utimesSync(path, new Date(mtimeMs), new Date(mtimeMs));
  const content = lines.map((line) => `${line}\n`).join("");
  piManifest.push({
    path: relative(root, path),
    bytes: Buffer.byteLength(content, "utf8"),
    mtimeMs,
    sha256: sha256OfFile(content),
    singleLineMax: Math.max(...lines.map((line) => Buffer.byteLength(line, "utf8"))),
  });
  previousPiPath = path;
}

// ---- claude sessions --------------------------------------------------------
const claudeLarge = claudeFiles >= 6 ? 4 : 1;
const claudeBigLineBytes = Math.max(largeFileMinBytes + 1024 * 1024, Math.floor(claudeBytes / Math.max(1, claudeFiles) / 2));
const claudePerFile = claudeFiles > 0 ? Math.max(1, Math.floor((claudeBytes - claudeLarge * claudeBigLineBytes) / claudeFiles)) : 0;

const claudeRecords = [];
const claudeManifest = [];
for (let index = 0; index < claudeFiles; index += 1) {
  const id = `c${String(index).padStart(4, "0")}-${seed}-1111-4222-8333-444444444444`;
  const dirName = index % 4 === 0 ? "-tmp-fixture-a" : "-tmp-fixture-b";
  const path = join(claudeConfigDir, "projects", dirName, `${id}.jsonl`);
  const createdMs = baseMs + 26 * DAY_MS + Math.floor((index / Math.max(1, claudeFiles)) * 3 * DAY_MS);
  const lines = claudeTranscriptLines({
    id, createdMs,
    bigLineBytes: index < claudeLarge ? claudeBigLineBytes : 0,
    targetBytes: claudePerFile, random,
  });
  writeJsonl(path, lines);
  const mtimeMs = createdMs + 7200_000;
  utimesSync(path, new Date(mtimeMs), new Date(mtimeMs));
  const content = lines.map((line) => `${line}\n`).join("");
  claudeManifest.push({
    path: relative(root, path),
    bytes: Buffer.byteLength(content, "utf8"),
    mtimeMs,
    sha256: sha256OfFile(content),
    singleLineMax: Math.max(...lines.map((line) => Buffer.byteLength(line, "utf8"))),
  });
  claudeRecords.push({
    id,
    cwd: join(root, `claude-project-${index % 6}`),
    name: `claude fixture ${index}`,
    model: "claude-sonnet-5-5",
    effort: "default",
    agentLanguage: "en",
    launchPermissionMode: "default",
    includeHookEvents: false,
    created: new Date(createdMs).toISOString(),
    modified: new Date(mtimeMs).toISOString(),
    firstMessage: `claude fixture prompt ${index}`,
    usage: { input: 10, output: 20, cacheRead: 30, cacheWrite: 40, cost: 0.001 },
  });
}

// Registered in the synthetic index, exactly the way pi-web's own records read.
mkdirSync(agentDir, { recursive: true });
writeFileSync(join(agentDir, "claude-sessions.json"), `${JSON.stringify({ version: 1, sessions: claudeRecords }, null, 2)}\n`);

// ---- manifest ---------------------------------------------------------------
const manifest = {
  schema: "session-list-fixture/1",
  seed,
  params: { piFiles, piBytes, claudeFiles, claudeBytes, largeFileMinBytes, projectDirs },
  pi: piManifest,
  claude: claudeManifest,
  totals: {
    piFiles: piManifest.length,
    piBytes: piManifest.reduce((sum, entry) => sum + entry.bytes, 0),
    claudeFiles: claudeManifest.length,
    claudeBytes: claudeManifest.reduce((sum, entry) => sum + entry.bytes, 0),
    largeFiles: [...piManifest, ...claudeManifest].filter((entry) => entry.singleLineMax >= largeFileMinBytes).length,
    projectDirs: Math.min(projectDirs, piFiles),
  },
};
mkdirSync(dirname(manifestPath), { recursive: true });
writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
console.log(JSON.stringify({ ok: true, manifest: manifestPath, totals: manifest.totals }));
