import { createHash } from "node:crypto";
import { constants } from "node:fs";
import { open, readdir, realpath, stat, type FileHandle } from "node:fs/promises";
import { homedir } from "node:os";
import { basename, isAbsolute, join, sep } from "node:path";
import { load as parseYaml } from "js-yaml";
import { readRegularFileTextAsync } from "./regular-file";
import { maskPolyforgeCredentials, type PolyforgeJob } from "./polyforge-status";
import { isPolyforgeProfileName, POLYFORGE_DEFAULT_PROFILE } from "./polyforge-cells-pure";
import {
  CARD_ENTRY_MAX,
  CARD_FIRST_PAGE,
  CARD_POLL_PAGE,
  CARD_RESPONSE_MAX_BYTES,
  CARD_RESPONSE_MAX_ENTRIES,
  capCodePoints,
  composeProgressEventLine,
  formatCardCursor,
  isPolyforgeWorkItemId,
  parseCardCursor,
  polyforgeJobRef,
  sortPolyforgeCards,
  type PolyforgeCard,
  type PolyforgeCardEntry,
  type PolyforgeCardProgress,
  type PolyforgeCardStep,
  type PolyforgeProgressLine,
  type PolyforgeProgressSnapshot,
  type PolyforgeProgressSubagent,
  type PolyforgeProgressTool,
} from "./polyforge-cards-pure";

/**
 * Server-side reader of the Polyforge Jobs-tab card stream.
 * Read-only, straight from the daemon's own files — no pf spawn per card:
 *
 * - `<data root>/runtime-state.json` `jobs[]` → a job's `capsule_dir` and
 *   job-level `transcript` (data root: `$POLYFORGE_DATA_DIR`, else
 *   `~/.local/share/polyforge`, the daemon's own default);
 * - `<data root>/events/wi-<wi>.jsonl` → earliest `job_started`, per-step
 *   `step_started` / `step_done` runs (bounded read);
 * - `<capsule>/capsule.json` `bundle_name` → `<capsule>/workflows/<bundle>/
 *   workflow.yaml` `steps[].id` → declared step order;
 * - `<capsule>/runtime-state/work-item.json` (`goal`) and `execution.json`
 *   (`current_step`, per-step `status` / `step_started_at`);
 * - `<capsule>/runtime-state/spawn-journal.jsonl` latest line → the current
 *   step's `harness`, `started_at`, `harness_session` and `transcript`. The
 *   harness's live session file is read first (pi: the newest file under
 *   `<data root>/step-agent-sessions/<encoded capsule>/`, unless that
 *   session itself started before the step; claude: `<data root>/claude-config/projects/<encoded
 *   capsule>/<harness_session>.jsonl`), then the journal's `transcript` (the
 *   capsule copy, complete only once the step ends). The job record's own
 *   `transcript` is the job's first step, never the current one, so it is
 *   not a progress source.
 *
 * Every path derives from a wi id validated by its shape and found in the
 * daemon's own job record; a transcript must resolve inside the capsule or
 * the data root. Content leaves only through the entry whitelist: assistant
 * narration text, credential-masked — never a tool call, its
 * name or command/argument, tool results, thinking, user/system prompts. Every failure degrades to an
 * absent field or `available: false`; nothing here throws to the route.
 * Every read is asynchronous (`node:fs/promises`): a 2 s poll
 * of a job's transcripts, up to the 128 MiB bound, never blocks the event loop.
 *
 * The job detail's execution-progress face, whole-job
 * (`readProgressSnapshot`), is a separate, wider projection:
 * every spawn's transcript as narration (assistant `text` and non-empty
 * `thinking`) plus one masked
 * `<name>: <primary argument>` line per tool call (`progressEntries`), merged
 * with one composed line per retained lifecycle event. It carries every line
 * (no per-line cap, no line bound) and never reaches the
 * card stream above, whose whitelist stays narration only. Narration
 * keeps its original whitespace (masked only), each
 * tool line carries the light `tool` fields (name, masked argument, status,
 * duration from its paired result) and a call's full masked input and output
 * are read only on demand, one call at a time (`readProgressToolDetail`).
 */

/** Small JSON records (job index, capsule, work item, execution). */
const RECORD_MAX_BYTES = 4 * 1024 * 1024;
/** A lane's workflow.yaml (its lane keys). */
const WORKFLOW_MAX_BYTES = 512 * 1024;
/** Events: read whole up to this; beyond it a head (job_started) + tail window. */
const EVENTS_MAX_BYTES = 512 * 1024;
const EVENTS_HEAD_BYTES = 32 * 1024;
/** Spawn journal: only its newest line matters. */
const JOURNAL_TAIL_BYTES = 16 * 1024;
/** Transcript bytes read per card per request (tail on first load, forward slice after). */
export const TRANSCRIPT_WINDOW_BYTES = 64 * 1024;
/** Goal text kept for the hover title. */
const GOAL_MAX = 2000;

const OPEN_FLAGS = constants.O_RDONLY | (constants.O_NONBLOCK || 0);
const HARNESS_SESSION_PATTERN = /^[A-Za-z0-9-]{1,128}$/;

export function polyforgeDataRoot(): string {
  const configured = process.env.POLYFORGE_DATA_DIR?.trim();
  return configured && isAbsolute(configured) ? configured : join(homedir(), ".local", "share", "polyforge");
}

/**
 * one pf Host serves several Cells and each Cell's state
 * (runtime-state.json, events/, step-agent-sessions/, claude-config/) lives
 * under `<data root>/cells/<profile>/`, `default` first. A data root with no
 * `cells/` (the pre-Cell layout) is its own single state dir. Never throws.
 */
export async function polyforgeCellRoots(root: string = polyforgeDataRoot()): Promise<string[]> {
  let names: string[];
  try {
    names = (await readdir(join(root, "cells"), { withFileTypes: true }))
      .filter((entry) => entry.isDirectory() && /^[a-z0-9][a-z0-9-]{0,31}$/.test(entry.name))
      .map((entry) => entry.name);
  } catch {
    return [root];
  }
  if (names.length === 0) return [root];
  names.sort((a, b) => (a === "default" ? -1 : b === "default" ? 1 : a.localeCompare(b)));
  return names.map((name) => join(root, "cells", name));
}

/** The state dir of one work reference's Cell: the job's
 *  WorkRef.profile (`default` when absent) under `<data root>/cells/`; a
 *  data root with no `cells/` is its own single state dir. Never resolved
 *  from a work-item literal — equal literals of two Cells stay apart.
 *  undefined for a malformed profile. Never throws. */
export async function polyforgeCellRoot(profile: string | undefined, root: string = polyforgeDataRoot()): Promise<string | undefined> {
  const name = profile ?? POLYFORGE_DEFAULT_PROFILE;
  if (!isPolyforgeProfileName(name)) return undefined;
  const roots = await polyforgeCellRoots(root);
  if (roots.length === 1 && roots[0] === root) return root;
  return join(root, "cells", name);
}

// ---- bounded file reads ----

/** Read `[start, start+length)` of a regular file; undefined when missing,
 *  not a regular file or unreadable. `size` is the file size at open. */
async function readSlice(path: string, start: number | ((size: number) => number), length: number | ((size: number, start: number) => number)):
  Promise<{ buffer: Buffer; start: number; size: number; ino: number; dev: number } | undefined> {
  let handle: FileHandle;
  try {
    handle = await open(path, OPEN_FLAGS);
  } catch {
    return undefined;
  }
  try {
    const stats = await handle.stat();
    if (!stats.isFile()) return undefined;
    const size = stats.size;
    const from = Math.max(0, Math.min(size, typeof start === "function" ? start(size) : start));
    const want = Math.max(0, Math.min(size - from, typeof length === "function" ? length(size, from) : length));
    const buffer = Buffer.alloc(want);
    let read = 0;
    while (read < want) {
      const { bytesRead } = await handle.read(buffer, read, want - read, from + read);
      if (bytesRead === 0) break;
      read += bytesRead;
    }
    return { buffer: buffer.subarray(0, read), start: from, size, ino: stats.ino, dev: stats.dev };
  } catch {
    return undefined;
  } finally {
    await handle.close().catch(() => undefined);
  }
}

async function readJson(path: string, maxBytes = RECORD_MAX_BYTES): Promise<unknown> {
  try {
    const text = await readRegularFileTextAsync(path, maxBytes);
    return text === undefined ? undefined : JSON.parse(text);
  } catch {
    return undefined;
  }
}

function asObject(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function asString(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function parseLine(line: string): Record<string, unknown> | undefined {
  if (!line.trim()) return undefined;
  try {
    return asObject(JSON.parse(line));
  } catch {
    return undefined;
  }
}

// ---- daemon job records ----

export interface PolyforgeJobRecord {
  capsuleDir?: string;
}

/** `runtime-state.json` → wi → its record's capsule dir (`readJobIndex`). */
export async function readJobRecords(root: string): Promise<Map<string, PolyforgeJobRecord>> {
  return (await readJobIndex(root)).records;
}

// ---- events and step durations ----

export interface PolyforgeCardEvent {
  at: string;
  kind: string;
  step?: string;
}

/** The WI event file's lines, bounded: whole up to EVENTS_MAX_BYTES, else a
 *  head window (where `job_started` lives) plus the newest tail window. */
export async function readCardEvents(path: string): Promise<PolyforgeCardEvent[]> {
  const head = await readSlice(path, 0, EVENTS_MAX_BYTES);
  if (!head) return [];
  let lines: string[];
  if (head.size <= EVENTS_MAX_BYTES) {
    lines = head.buffer.toString("utf8").split("\n");
  } else {
    const headText = head.buffer.subarray(0, EVENTS_HEAD_BYTES).toString("utf8");
    const headLines = headText.split("\n").slice(0, -1);
    const tail = await readSlice(path, (size) => size - (EVENTS_MAX_BYTES - EVENTS_HEAD_BYTES), EVENTS_MAX_BYTES - EVENTS_HEAD_BYTES);
    const tailLines = tail ? tail.buffer.toString("utf8").split("\n").slice(1) : [];
    lines = [...headLines, ...tailLines];
  }
  const events: PolyforgeCardEvent[] = [];
  for (const line of lines) {
    const record = parseLine(line);
    const at = asString(record?.at);
    const kind = asString(record?.kind);
    if (!at || !kind) continue;
    const step = asString(asObject(record?.body)?.step);
    events.push(step ? { at, kind, step } : { at, kind });
  }
  return events;
}

export function earliestJobStarted(events: PolyforgeCardEvent[]): string | undefined {
  let best: { at: string; ms: number } | undefined;
  for (const event of events) {
    if (event.kind !== "job_started") continue;
    const ms = Date.parse(event.at);
    if (!Number.isFinite(ms)) continue;
    if (!best || ms < best.ms) best = { at: event.at, ms };
  }
  return best?.at;
}

export interface PolyforgeExecutionSteps {
  currentStep?: string;
  steps: Map<string, { status: string; startedAt?: string }>;
}

export async function readExecution(capsuleDir: string): Promise<PolyforgeExecutionSteps | undefined> {
  const execution = asObject(await readJson(join(capsuleDir, "runtime-state", "execution.json")));
  if (!execution) return undefined;
  const steps = new Map<string, { status: string; startedAt?: string }>();
  for (const [id, value] of Object.entries(asObject(execution.steps) ?? {})) {
    const step = asObject(value);
    const startedAt = asString(step?.step_started_at) ?? asString(step?.started_at);
    steps.set(id, { status: asString(step?.status) ?? "pending", ...(startedAt ? { startedAt } : {}) });
  }
  const currentStep = asString(execution.current_step);
  return { ...(currentStep ? { currentStep } : {}), steps };
}

/** Declared step ids from the bundle's workflow.yaml, in order. */
export async function readDeclaredSteps(capsuleDir: string): Promise<string[] | undefined> {
  const bundle = asString(asObject(await readJson(join(capsuleDir, "capsule.json")))?.bundle_name);
  if (!bundle || !/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(bundle)) return undefined;
  let text: string | undefined;
  try {
    text = await readRegularFileTextAsync(join(capsuleDir, "workflows", bundle, "workflow.yaml"), WORKFLOW_MAX_BYTES);
  } catch {
    return undefined;
  }
  if (text === undefined) return undefined;
  let lane: unknown;
  try {
    lane = parseYaml(text);
  } catch {
    return undefined;
  }
  const steps = asObject(lane)?.steps;
  if (!Array.isArray(steps)) return undefined;
  const ids: string[] = [];
  for (const step of steps) {
    const id = asString(asObject(step)?.id);
    if (!id) return undefined;
    if (!ids.includes(id)) ids.push(id);
  }
  return ids;
}

/**
 * One entry per step, in `order`. A run opens at `step_started` and closes
 * at the step's `step_done` (or `step_failed`/`step_dead`) or at its next
 * `step_started`; the current in-progress step's open run closes at `now`.
 * The duration is the sum over runs; `runs` counts `step_started`.
 * A step with no readable start is `unstarted` when the execution record
 * says it never left `pending`, `skipped` when it says `skipped`, and
 * `unknown` otherwise — except the current
 * in-progress step, timed from the execution record's start. Pure.
 */
export function computeCardSteps(
  order: string[],
  events: PolyforgeCardEvent[],
  execution: PolyforgeExecutionSteps | undefined,
  now: number,
): PolyforgeCardStep[] {
  const runs = new Map<string, { count: number; totalMs: number; timed: boolean; openMs: number | null }>();
  const stateOf = (step: string) => {
    let state = runs.get(step);
    if (!state) {
      state = { count: 0, totalMs: 0, timed: false, openMs: null };
      runs.set(step, state);
    }
    return state;
  };
  const close = (state: { totalMs: number; timed: boolean; openMs: number | null }, endMs: number) => {
    if (state.openMs !== null && endMs >= state.openMs) {
      state.totalMs += endMs - state.openMs;
      state.timed = true;
    }
    state.openMs = null;
  };
  for (const event of events) {
    if (!event.step) continue;
    const ms = Date.parse(event.at);
    if (event.kind === "step_started") {
      const state = stateOf(event.step);
      state.count += 1;
      if (Number.isFinite(ms)) close(state, ms);
      state.openMs = Number.isFinite(ms) ? ms : null;
    } else if (event.kind === "step_done" || event.kind === "step_failed" || event.kind === "step_dead") {
      const state = runs.get(event.step);
      if (state && Number.isFinite(ms)) close(state, ms);
    }
  }
  return order.map((id): PolyforgeCardStep => {
    const record = execution?.steps.get(id);
    const live = execution?.currentStep === id && record?.status === "in_progress";
    const state = runs.get(id);
    if (state && state.count > 0) {
      if (state.openMs !== null && live) close(state, Math.max(now, state.openMs));
      return state.timed
        ? { id, state: "timed", durationSec: Math.max(0, Math.round(state.totalMs / 1000)), runs: state.count }
        : { id, state: "unknown", runs: state.count };
    }
    if (live && record?.startedAt) {
      const startMs = Date.parse(record.startedAt);
      if (Number.isFinite(startMs)) {
        return { id, state: "timed", durationSec: Math.max(0, Math.round((now - startMs) / 1000)), runs: 1 };
      }
    }
    if (!record || record.status === "pending") return { id, state: "unstarted", runs: 0 };
    if (record.status === "skipped") return { id, state: "skipped", runs: 0 };
    return { id, state: "unknown", runs: 0 };
  });
}

/** Declared order, else the execution record's steps ordered by first
 *  observed `step_started`, then the rest in record order. */
export function cardStepOrder(
  declared: string[] | undefined,
  events: PolyforgeCardEvent[],
  execution: PolyforgeExecutionSteps | undefined,
): string[] {
  if (declared && declared.length > 0) return declared;
  const keys = [...(execution?.steps.keys() ?? [])];
  const seen: string[] = [];
  for (const event of events) {
    if (event.kind === "step_started" && event.step && keys.includes(event.step) && !seen.includes(event.step)) seen.push(event.step);
  }
  return [...seen, ...keys.filter((key) => !seen.includes(key))];
}

// ---- transcript resolution ----

export type PolyforgeHarness = "pi" | "claude";

export interface PolyforgeTranscriptSource {
  path: string;
  harness: PolyforgeHarness;
  step?: string;
}

async function realInside(path: string, roots: string[]): Promise<string | undefined> {
  let real: string;
  try {
    real = await realpath(path);
  } catch {
    return undefined;
  }
  for (const root of roots) {
    let realRoot: string;
    try {
      realRoot = await realpath(root);
    } catch {
      continue;
    }
    if (real.startsWith(realRoot.endsWith(sep) ? realRoot : realRoot + sep)) return real;
  }
  return undefined;
}

/** pi's session directory name for a cwd: `--<path, separators → ->--`. */
export function encodePiSessionDir(cwd: string): string {
  return `--${cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`;
}

/** Claude's project directory name for a cwd: every non-alphanumeric → `-`. */
export function encodeClaudeProjectDir(cwd: string): string {
  return cwd.replace(/[^A-Za-z0-9]/g, "-");
}

/** Clock-skew allowance between the spawn journal's `started_at` and the pi
 *  session's own start stamp (one host, so only a few seconds). */
const SESSION_START_SKEW_MS = 5_000;
/** Bytes read from a session file's head for its `{"type":"session"}` record. */
const SESSION_HEAD_BYTES = 4 * 1024;
/** pi's session file name: `<ISO timestamp, ':' and '.' → '-'>_<uuid>.jsonl`. */
const PI_SESSION_FILE_STAMP = /^(\d{4}-\d{2}-\d{2})T(\d{2})-(\d{2})-(\d{2})-(\d{3})Z_/;

async function newestJsonl(dir: string): Promise<{ path: string; mtime: number } | undefined> {
  try {
    const names = (await readdir(dir)).filter((name) => name.endsWith(".jsonl"));
    let best: { path: string; mtime: number } | undefined;
    for (const name of names) {
      const path = join(dir, name);
      const stats = await stat(path);
      if (stats.isFile() && (!best || stats.mtimeMs > best.mtime)) best = { path, mtime: stats.mtimeMs };
    }
    return best;
  } catch {
    return undefined;
  }
}

/** When a pi session started, by its own record, not its last write: the
 *  first record's `timestamp` (type `"session"`), else the ISO timestamp in
 *  the file name, else the file's mtime. A previous step's
 *  session can still be written after the next step starts, so its mtime
 *  says nothing about which step it belongs to. */
async function piSessionStartMs(path: string, mtime: number): Promise<number> {
  const head = await readSlice(path, 0, SESSION_HEAD_BYTES);
  const first = head ? parseLine(head.buffer.toString("utf8").split("\n")[0] ?? "") : undefined;
  if (first?.type === "session") {
    const stamp = Date.parse(asString(first.timestamp) ?? "");
    if (Number.isFinite(stamp)) return stamp;
  }
  const named = PI_SESSION_FILE_STAMP.exec(basename(path));
  if (named) {
    const stamp = Date.parse(`${named[1]}T${named[2]}:${named[3]}:${named[4]}.${named[5]}Z`);
    if (Number.isFinite(stamp)) return stamp;
  }
  return mtime;
}

/** The current step's transcript and harness, from the spawn journal's
 *  newest line: the harness's live session file first, then the journal's
 *  `transcript`. Undefined when no candidate resolves inside the
 *  capsule/data root or the harness is not one this reader parses. */
export async function resolveTranscript(root: string, capsuleDir: string): Promise<PolyforgeTranscriptSource | undefined> {
  const journal = await readSlice(join(capsuleDir, "runtime-state", "spawn-journal.jsonl"), (size) => size - JOURNAL_TAIL_BYTES, JOURNAL_TAIL_BYTES);
  let latest: Record<string, unknown> | undefined;
  if (journal) {
    const lines = journal.buffer.toString("utf8").split("\n");
    for (let i = lines.length - 1; i >= 0 && !latest; i--) latest = parseLine(lines[i]);
  }
  const harnessName = asString(latest?.harness);
  if (harnessName !== "pi" && harnessName !== "claude") return undefined;
  const harness: PolyforgeHarness = harnessName;
  const step = asString(latest?.step_id);
  const roots = [capsuleDir, root];
  const candidates: (string | undefined)[] = [];
  if (harness === "pi") {
    // The newest session of this capsule, unless it started before the step
    // (a previous step's session: the current step has not written its own yet).
    const newest = await newestJsonl(join(root, "step-agent-sessions", encodePiSessionDir(capsuleDir)));
    const startedMs = Date.parse(asString(latest?.started_at) ?? "");
    if (newest && (!Number.isFinite(startedMs) || await piSessionStartMs(newest.path, newest.mtime) >= startedMs - SESSION_START_SKEW_MS)) {
      candidates.push(newest.path);
    }
  } else {
    const session = asString(latest?.harness_session);
    if (session && HARNESS_SESSION_PATTERN.test(session)) {
      candidates.push(join(root, "claude-config", "projects", encodeClaudeProjectDir(capsuleDir), `${session}.jsonl`));
    }
  }
  candidates.push(asString(latest?.transcript));
  for (const candidate of candidates) {
    if (!candidate || !isAbsolute(candidate)) continue;
    const path = await realInside(candidate, roots);
    if (path) return { path, harness, ...(step ? { step } : {}) };
  }
  return undefined;
}

// ---- transcript parsing (the whitelist) ----

function cleanText(value: string): string {
  return capCodePoints(maskPolyforgeCredentials(value.replace(/\s+/g, " ").trim()), CARD_ENTRY_MAX);
}

/** Progress-face text: the credential mask only — no cap
 *  and the original whitespace kept, so the UI can
 *  render it as Markdown; text that is blank after a trim is no text. */
function cleanProgressText(value: string): string {
  return value.trim() ? maskPolyforgeCredentials(value) : "";
}

/** Whether a record belongs to the harness's transcript format at all. */
function recognised(record: Record<string, unknown>, harness: PolyforgeHarness): boolean {
  const type = record.type;
  if (harness === "pi") return type === "message" || type === "session";
  return type === "assistant" || type === "user" || type === "queue-operation" || type === "system";
}

/** The whitelisted entries of one transcript record: assistant text blocks
 *  only. Tool calls, tool results, thinking, user and system
 *  records produce nothing. */
export function transcriptEntries(record: Record<string, unknown>, harness: PolyforgeHarness, idPrefix: string): PolyforgeCardEntry[] {
  const message = asObject(record.message);
  if (!message || message.role !== "assistant") return [];
  if (harness === "pi" ? record.type !== "message" : record.type !== "assistant") return [];
  const content = message.content;
  const blocks = typeof content === "string" ? [{ type: "text", text: content }] : Array.isArray(content) ? content : [];
  const entries: PolyforgeCardEntry[] = [];
  blocks.forEach((value, index) => {
    const block = asObject(value);
    if (!block) return;
    if (block.type !== "text" || typeof block.text !== "string") return;
    const text = cleanText(block.text);
    if (text) entries.push({ id: `${idPrefix}.${index}`, text });
  });
  return entries;
}

/** A tool call's primary argument: a string `command`, else the first
 *  string-valued argument in key order, else nothing. */
function primaryArgument(args: unknown): string | undefined {
  const object = asObject(args);
  if (!object) return undefined;
  if (typeof object.command === "string" && object.command.trim()) return object.command;
  for (const value of Object.values(object)) {
    if (typeof value === "string" && value.trim()) return value;
  }
  return undefined;
}

/** The execution-progress entries of one transcript record,
 *  thinking included: each assistant `text` block → `narration`
 *  (as `transcriptEntries`), each non-empty `thinking` block → `narration`
 *  with `variant:"thinking"` (the UI renders it dim italic), each pi
 *  `toolCall` / claude `tool_use` block → one `tool` line `<name>: <primary
 *  argument>` (whitespace-collapsed) with its light `tool` fields — the name,
 *  the masked primary argument and `status: "running"` until
 *  `projectTranscript` pairs a result. Every line is
 *  credential-masked with no length cap; narration keeps its
 *  whitespace. Tool results, empty thinking, user/system
 *  records and every other field produce nothing. `onCall` sees each tool
 *  line with its call id and raw input (for pairing and the on-demand detail;
 *  neither enters a line). Only the progress face uses it; the card face never
 *  does. */
export function progressEntries(
  record: Record<string, unknown>,
  harness: PolyforgeHarness,
  idPrefix: string,
  onCall?: (line: PolyforgeProgressLine, callId: string | undefined, input: unknown) => void,
): PolyforgeProgressLine[] {
  const message = asObject(record.message);
  if (!message || message.role !== "assistant") return [];
  if (harness === "pi" ? record.type !== "message" : record.type !== "assistant") return [];
  const content = message.content;
  const blocks = typeof content === "string" ? [{ type: "text", text: content }] : Array.isArray(content) ? content : [];
  const entries: PolyforgeProgressLine[] = [];
  blocks.forEach((value, index) => {
    const block = asObject(value);
    if (!block) return;
    const id = `${idPrefix}.${index}`;
    if (block.type === "text" && typeof block.text === "string") {
      const text = cleanProgressText(block.text);
      if (text) entries.push({ id, kind: "narration", text });
      return;
    }
    if (block.type === "thinking" && typeof block.thinking === "string") {
      const text = cleanProgressText(block.thinking);
      if (text) entries.push({ id, kind: "narration", text, variant: "thinking" });
      return;
    }
    const toolBlock = harness === "pi" ? "toolCall" : "tool_use";
    if (block.type !== toolBlock) return;
    const name = asString(block.name);
    if (!name) return;
    const input = harness === "pi" ? block.arguments : block.input;
    const argument = primaryArgument(input);
    const text = maskPolyforgeCredentials((argument ? `${name}: ${argument}` : name).replace(/\s+/g, " ").trim());
    const tool: PolyforgeProgressTool = { name: maskPolyforgeCredentials(name), ...(argument ? { argument: maskPolyforgeCredentials(argument) } : {}), status: "running" };
    const line: PolyforgeProgressLine = { id, kind: "tool", text, tool };
    entries.push(line);
    onCall?.(line, asString(block.id), input);
  });
  return entries;
}

/** A tool result a transcript record carries: pi's `toolResult` message
 *  (`toolCallId` when present), claude's `tool_result` blocks of a user
 *  record (`tool_use_id`). */
interface ProgressToolResult {
  callId?: string;
  isError: boolean;
  content: unknown;
}

function progressToolResults(record: Record<string, unknown>, harness: PolyforgeHarness): ProgressToolResult[] {
  const message = asObject(record.message);
  if (!message) return [];
  if (harness === "pi") {
    if (record.type !== "message" || message.role !== "toolResult") return [];
    const callId = asString(message.toolCallId);
    return [{ ...(callId ? { callId } : {}), isError: message.isError === true, content: message.content }];
  }
  if (record.type !== "user" || !Array.isArray(message.content)) return [];
  const results: ProgressToolResult[] = [];
  for (const value of message.content) {
    const block = asObject(value);
    if (block?.type !== "tool_result") continue;
    const callId = asString(block.tool_use_id);
    results.push({ ...(callId ? { callId } : {}), isError: block.is_error === true, content: block.content });
  }
  return results;
}

/** A tool result's content as text: text blocks joined by a newline, any
 *  other block a neutral `[image]` / `[<type>]` placeholder (never invented
 *  text), masked, uncapped. */
function toolResultText(content: unknown): string {
  if (typeof content === "string") return maskPolyforgeCredentials(content);
  if (!Array.isArray(content)) return "";
  return maskPolyforgeCredentials(content.map((value) => {
    const block = asObject(value);
    if (block?.type === "text" && typeof block.text === "string") return block.text;
    return `[${asString(block?.type) ?? "content"}]`;
  }).join("\n"));
}

/** One tool call's full masked detail: what the expanded
 *  progress card shows. `output` is null while no result is paired. */
export interface PolyforgeProgressToolDetail extends PolyforgeProgressTool {
  input: string;
  output: string | null;
  isError: boolean;
}

/** `GET /api/polyforge/progress/tool` — one call's detail, or why not. */
export type PolyforgeProgressToolAnswer =
  | { available: true; hasResult: boolean; tool: PolyforgeProgressToolDetail }
  | { available: false; error: string };

interface ProgressCall {
  line: PolyforgeProgressLine;
  callId?: string;
  callAt?: number;
  input?: unknown;
  result?: ProgressToolResult & { at?: number };
}

/**
 * One transcript projected for the progress face: every record's
 * `progressEntries` pushed with its time (the record's `timestamp`, else its
 * spawn's start plus its ordinal in microseconds), and each tool call paired
 * with its result — by call id (pi `toolCallId`, claude
 * `tool_use_id`) when the result names one (an id no call of this transcript
 * carries pairs nothing), else the oldest unpaired call of the same
 * transcript (the recorded pi format names none). A paired call's
 * status is `error` on the result's error flag, else `ok`; unpaired stays
 * `running`; `durationMs` only when both records carry a readable timestamp
 * in order. Only the call whose line id is `keep` retains its raw input and
 * result (the detail read); the poll keeps none.
 */
interface ProjectOptions {
  /** The line id whose call keeps its raw input and result (the detail read). */
  keep?: string;
  /** Each tool line with the call id its block names (the
   *  subagents of that call attach to it). */
  onToolCall?: (line: PolyforgeProgressLine, callId: string | undefined) => void;
  /** Filled with the first and last readable record times (epoch ms) of the
   *  transcript, any record type (a subagent's span). */
  span?: { first?: number; last?: number };
}

function projectTranscript(
  buffer: Buffer,
  harness: PolyforgeHarness,
  identity: string,
  startMs: number,
  push: (atMs: number, line: PolyforgeProgressLine) => void,
  { keep, onToolCall, span }: ProjectOptions = {},
): ProgressCall | undefined {
  const calls: ProgressCall[] = [];
  const byId = new Map<string, ProgressCall>();
  let next = 0;
  let kept: ProgressCall | undefined;
  let lineStart = 0;
  let ordinal = 0;
  while (lineStart < buffer.length) {
    const newline = buffer.indexOf(0x0a, lineStart);
    if (newline < 0) break; // a line still being written
    ordinal += 1;
    if (newline - lineStart <= PROGRESS_LINE_MAX_BYTES) {
      const record = parseLine(buffer.subarray(lineStart, newline).toString("utf8"));
      if (record) {
        const stamp = Date.parse(asString(record.timestamp) ?? "");
        const atMs = Number.isFinite(stamp) ? stamp : startMs + ordinal / 1000;
        const real = Number.isFinite(stamp) ? stamp : undefined;
        if (span && real !== undefined) {
          if (span.first === undefined || real < span.first) span.first = real;
          if (span.last === undefined || real > span.last) span.last = real;
        }
        const lines = progressEntries(record, harness, `${identity}.${lineStart}`, (line, callId, input) => {
          const call: ProgressCall = { line, ...(callId ? { callId } : {}), ...(real !== undefined ? { callAt: real } : {}) };
          if (line.id === keep) { call.input = input; kept = call; }
          calls.push(call);
          if (callId && !byId.has(callId)) byId.set(callId, call);
          onToolCall?.(line, callId);
        });
        for (const line of lines) push(atMs, line);
        for (const result of progressToolResults(record, harness)) {
          let call: ProgressCall | undefined;
          if (result.callId) {
            call = byId.get(result.callId);
          } else {
            while (next < calls.length && calls[next].result) next += 1;
            call = calls[next];
          }
          if (!call || call.result) continue;
          call.result = { ...result, ...(real !== undefined ? { at: real } : {}) };
          const tool = call.line.tool!;
          tool.status = result.isError ? "error" : "ok";
          if (call.callAt !== undefined && real !== undefined && real >= call.callAt) tool.durationMs = real - call.callAt;
          if (call !== kept) call.result.content = undefined;
        }
      }
    }
    lineStart = newline + 1;
  }
  return kept;
}

export interface PolyforgeCardBudget {
  entries: number;
  bytes: number;
}

function entryBytes(entries: PolyforgeCardEntry[]): number {
  return entries.reduce((sum, entry) => sum + Buffer.byteLength(JSON.stringify(entry), "utf8") + 1, 0);
}

/**
 * The card progress for one transcript: a first load (no cursor, or one
 * for another transcript/identity, or past the end) reads the newest
 * TRANSCRIPT_WINDOW_BYTES and returns its newest CARD_FIRST_PAGE entries
 * with `reset`; a matching cursor reads forward at most that many bytes and
 * returns at most CARD_POLL_PAGE entries, the cursor stopping after the
 * last whole line consumed. `budget` (shared across the response) is
 * decremented; a card it cannot serve keeps its cursor.
 */
export async function readCardProgress(
  source: PolyforgeTranscriptSource,
  cursor: string | undefined,
  budget: PolyforgeCardBudget,
): Promise<PolyforgeCardProgress> {
  const head = await readSlice(source.path, 0, 0);
  if (!head) return { available: false, entries: [] };
  const identity = createHash("sha256")
    .update(`${source.path}\0${head.dev}\0${head.ino}\0${source.harness}\0${source.step ?? ""}`)
    .digest("hex")
    .slice(0, 16);
  const parsed = parseCardCursor(cursor);
  const incremental = parsed && parsed.identity === identity && parsed.offset <= head.size ? parsed.offset : null;

  if (incremental !== null) {
    const slice = await readSlice(source.path, incremental, TRANSCRIPT_WINDOW_BYTES);
    if (!slice) return { available: false, entries: [] };
    const text = slice.buffer;
    const fullBudget = budget.entries >= CARD_RESPONSE_MAX_ENTRIES && budget.bytes >= CARD_RESPONSE_MAX_BYTES;
    const entries: PolyforgeCardEntry[] = [];
    let consumed = 0;
    let lineStart = 0;
    while (lineStart < text.length && entries.length < CARD_POLL_PAGE) {
      const newline = text.indexOf(0x0a, lineStart);
      if (newline < 0) break;
      const record = parseLine(text.subarray(lineStart, newline).toString("utf8"));
      let take = record ? transcriptEntries(record, source.harness, `${identity}.${incremental + lineStart}`) : [];
      if (take.length > budget.entries || entryBytes(take) > budget.bytes) {
        // A line the rest of this response cannot hold waits for the next
        // poll; only a line no response could ever hold is cut to fit.
        if (entries.length > 0 || !fullBudget) break;
        const fitted: PolyforgeCardEntry[] = [];
        for (const entry of take) {
          if (fitted.length >= budget.entries || entryBytes([...fitted, entry]) > budget.bytes) break;
          fitted.push(entry);
        }
        take = fitted;
      }
      entries.push(...take);
      budget.entries -= take.length;
      budget.bytes -= entryBytes(take);
      consumed = newline + 1;
      lineStart = newline + 1;
    }
    // A single line longer than the whole window is skipped, never re-read forever.
    if (consumed === 0 && entries.length === 0 && text.length >= TRANSCRIPT_WINDOW_BYTES) consumed = text.length;
    return { available: true, entries, cursor: formatCardCursor(identity, incremental + consumed) };
  }

  const slice = await readSlice(source.path, (size) => size - TRANSCRIPT_WINDOW_BYTES, TRANSCRIPT_WINDOW_BYTES);
  if (!slice) return { available: false, entries: [] };
  const text = slice.buffer;
  let lineStart = 0;
  if (slice.start > 0) {
    const first = text.indexOf(0x0a);
    lineStart = first < 0 ? text.length : first + 1;
  }
  const end = text.lastIndexOf(0x0a) + 1;
  const all: PolyforgeCardEntry[] = [];
  let known = false;
  while (lineStart < end) {
    const newline = text.indexOf(0x0a, lineStart);
    const record = parseLine(text.subarray(lineStart, newline).toString("utf8"));
    if (record && recognised(record, source.harness)) {
      known = true;
      all.push(...transcriptEntries(record, source.harness, `${identity}.${slice.start + lineStart}`));
    }
    lineStart = newline + 1;
  }
  if (!known) return { available: false, entries: [] };
  const entries: PolyforgeCardEntry[] = [];
  let bytes = 0;
  for (let i = all.length - 1; i >= 0 && entries.length < Math.min(CARD_FIRST_PAGE, budget.entries); i--) {
    const size = entryBytes([all[i]]);
    if (bytes + size > budget.bytes) break;
    bytes += size;
    entries.unshift(all[i]);
  }
  budget.entries -= entries.length;
  budget.bytes -= bytes;
  // A budget that could not serve this first page leaves no cursor: the
  // next poll reads the newest page again instead of skipping it.
  if (entries.length === 0 && all.length > 0) return { available: true, entries: [] };
  return { available: true, entries, cursor: formatCardCursor(identity, slice.start + end), reset: true };
}

// ---- the whole-job execution progress ----

/** Spawn journal: read whole up to this (its newest window beyond it). */
const PROGRESS_JOURNAL_MAX_BYTES = 4 * 1024 * 1024;
/** Event log: read whole up to this (its newest whole lines beyond it). */
const PROGRESS_EVENTS_MAX_BYTES = 16 * 1024 * 1024;
/** A single transcript line above this is a blob record, never progress. */
const PROGRESS_LINE_MAX_BYTES = 8 * 1024 * 1024;
/** Transcript bytes summed over the whole job; beyond it the read answers
 *  `available: false` rather than an inexact `omitted` count. */
const PROGRESS_TRANSCRIPTS_MAX_BYTES = 128 * 1024 * 1024;

/** Whole lines of a bounded read: the newest `max` bytes, the leading
 *  partial line dropped when the window starts mid-file, the trailing one
 *  (a line still being written) always dropped. Each line keeps its byte
 *  offset in the file. Undefined when the file is not readable. */
async function readWholeLines(path: string, max: number): Promise<{ offset: number; text: string }[] | undefined> {
  const slice = await readSlice(path, (size) => size - max, max);
  if (!slice) return undefined;
  const buffer = slice.buffer;
  let lineStart = 0;
  if (slice.start > 0) {
    const first = buffer.indexOf(0x0a);
    lineStart = first < 0 ? buffer.length : first + 1;
  }
  const lines: { offset: number; text: string }[] = [];
  while (lineStart < buffer.length) {
    const newline = buffer.indexOf(0x0a, lineStart);
    if (newline < 0) break;
    lines.push({ offset: slice.start + lineStart, text: buffer.subarray(lineStart, newline).toString("utf8") });
    lineStart = newline + 1;
  }
  return lines;
}

export interface PolyforgeJobIndex {
  /** False: `runtime-state.json` is missing, unreadable or not a JSON object. */
  readable: boolean;
  records: Map<string, PolyforgeJobRecord>;
}

/** `runtime-state.json` → whether the daemon's job index is readable at all,
 *  and wi → its record's capsule dir (the first record per wi). */
export async function readJobIndex(root: string): Promise<PolyforgeJobIndex> {
  const records = new Map<string, PolyforgeJobRecord>();
  const index = asObject(await readJson(join(root, "runtime-state.json")));
  if (!index) return { readable: false, records };
  const jobs = index.jobs;
  if (Array.isArray(jobs)) {
    for (const job of jobs) {
      const record = asObject(job);
      const wi = record?.work_item_id;
      if (!isPolyforgeWorkItemId(wi) || records.has(wi)) continue;
      const capsuleDir = asString(record?.capsule_dir);
      records.set(wi, capsuleDir && isAbsolute(capsuleDir) ? { capsuleDir } : {});
    }
  }
  return { readable: true, records };
}

export interface PolyforgeSpawn {
  step?: string;
  spawnN?: number;
  startedAt?: string;
  harness?: string;
  harnessSession?: string;
  transcript?: string;
}

/** Every spawn of the job, in journal (file) order: one per line of
 *  `<capsule>/runtime-state/spawn-journal.jsonl`. Undefined when the journal
 *  is not readable. */
export async function readSpawnJournal(capsuleDir: string): Promise<PolyforgeSpawn[] | undefined> {
  const lines = await readWholeLines(join(capsuleDir, "runtime-state", "spawn-journal.jsonl"), PROGRESS_JOURNAL_MAX_BYTES);
  if (!lines) return undefined;
  const spawns: PolyforgeSpawn[] = [];
  for (const line of lines) {
    const record = parseLine(line.text);
    if (!record) continue;
    const spawnN = record.spawn_n;
    const step = asString(record.step_id);
    const startedAt = asString(record.started_at) ?? asString(record.at);
    const harness = asString(record.harness);
    const harnessSession = asString(record.harness_session);
    const transcript = asString(record.transcript);
    spawns.push({
      ...(step ? { step } : {}),
      ...(typeof spawnN === "number" && Number.isSafeInteger(spawnN) ? { spawnN } : {}),
      ...(startedAt ? { startedAt } : {}),
      ...(harness ? { harness } : {}),
      ...(harnessSession ? { harnessSession } : {}),
      ...(transcript ? { transcript } : {}),
    });
  }
  return spawns;
}

export interface PolyforgeProgressEvent {
  /** The line's byte offset in the log: a stable id, never a path. */
  offset: number;
  at: string;
  kind: string;
  body: Record<string, unknown>;
}

/** The WI's event log `<root>/events/wi-<wi>.jsonl`, whole up to
 *  PROGRESS_EVENTS_MAX_BYTES (else its newest whole lines), each line's
 *  time, kind and raw body. Undefined when the log is not readable. */
export async function readProgressEventLog(root: string, wi: string): Promise<PolyforgeProgressEvent[] | undefined> {
  if (!isPolyforgeWorkItemId(wi)) return undefined;
  const lines = await readWholeLines(join(root, "events", `wi-${wi}.jsonl`), PROGRESS_EVENTS_MAX_BYTES);
  if (!lines) return undefined;
  const events: PolyforgeProgressEvent[] = [];
  for (const line of lines) {
    const record = parseLine(line.text);
    const at = asString(record?.at);
    const kind = asString(record?.kind);
    if (!at || !kind) continue;
    events.push({ offset: line.offset, at, kind, body: asObject(record?.body) ?? {} });
  }
  return events;
}

/** One spawn's transcript: the newest spawn reads the harness's live
 *  session first (`resolveTranscript`, the rule — a running pi
 *  step's capsule copy holds only its prompt); every earlier spawn reads the
 *  journal's `transcript` (the capsule copy), else, for claude, its
 *  `claude-config` session. Same realpath containment; an unknown harness
 *  or an outside/missing path is no transcript. */
async function spawnTranscript(root: string, capsuleDir: string, spawn: PolyforgeSpawn, newest: boolean): Promise<PolyforgeTranscriptSource | undefined> {
  if (spawn.harness !== "pi" && spawn.harness !== "claude") return undefined;
  const harness: PolyforgeHarness = spawn.harness;
  if (newest) {
    const live = await resolveTranscript(root, capsuleDir);
    if (live && live.harness === harness) return live;
  }
  const candidates = [spawn.transcript];
  if (harness === "claude" && spawn.harnessSession && HARNESS_SESSION_PATTERN.test(spawn.harnessSession)) {
    candidates.push(join(root, "claude-config", "projects", encodeClaudeProjectDir(capsuleDir), `${spawn.harnessSession}.jsonl`));
  }
  for (const candidate of candidates) {
    if (!candidate || !isAbsolute(candidate)) continue;
    const path = await realInside(candidate, [capsuleDir, root]);
    if (path) return { path, harness, ...(spawn.step ? { step: spawn.step } : {}) };
  }
  return undefined;
}

/** A stream line before the bound: its sort keys and the wire line. */
interface ProgressStreamItem {
  atMs: number;
  rank: 0 | 1;
  order: number;
  line: PolyforgeProgressLine;
}

const unavailableSnapshot = (): PolyforgeProgressSnapshot => ({ available: false, lines: [], omitted: 0 });

/** The distinct transcripts of a job's spawns, in journal order, each with
 *  its spawn's start (0 when unreadable): `spawnTranscript` per spawn, the
 *  newest reading the live session first. A `--resume` continuation counts
 *  once: pi's names the same transcript path; claude's gets
 *  its own capsule copy of the whole native session, so a claude session
 *  several spawns name is read from its newest spawn's transcript, in its
 *  first spawn's slot and start. */
async function progressSources(root: string, capsuleDir: string, spawns: PolyforgeSpawn[]): Promise<{ source: PolyforgeTranscriptSource; startMs: number }[]> {
  const sources: { source: PolyforgeTranscriptSource; startMs: number }[] = [];
  const seen = new Set<string>();
  const sessions = new Map<string, number>();
  for (const [index, spawn] of spawns.entries()) {
    const source = await spawnTranscript(root, capsuleDir, spawn, index === spawns.length - 1);
    if (!source || seen.has(source.path)) continue;
    seen.add(source.path);
    const session = source.harness === "claude" ? spawn.harnessSession : undefined;
    const slot = session ? sessions.get(session) : undefined;
    if (slot !== undefined) {
      sources[slot].source = source;
      continue;
    }
    if (session) sessions.set(session, sources.length);
    const startMs = Date.parse(spawn.startedAt ?? "");
    sources.push({ source, startMs: Number.isFinite(startMs) ? startMs : 0 });
  }
  return sources;
}

// ---- subagents ----

/** One subagent transcript of a job and what its record says: claude's
 *  sidecar `agent-<id>.jsonl` + `.meta.json`, or the pi recorder's
 *  `<tool call id>-<i>.jsonl` + `.meta.json` (the driver's
 *  subagentrec.go), both keyed by the parent call's id (`toolUseId`). */
interface SubagentSource {
  source: PolyforgeTranscriptSource;
  callId: string;
  index: number;
  name: string;
  description: string;
  agent?: string;
  finished: boolean;
  /** The meta's own verdict (pi's recorder writes one per child); absent
   *  for claude, whose one subagent per call shares its call's result. */
  isError?: boolean;
}

/** A subagent's meta file is small; anything larger is not one. */
const SUBAGENT_META_MAX_BYTES = 64 * 1024;
/** Nesting a subagent's own subagents stops here (a cycle guard). */
const SUBAGENT_MAX_DEPTH = 8;
const SUBAGENT_TRANSCRIPT_NAME = /^[A-Za-z0-9_-]{1,200}\.jsonl$/;

/** A capsule transcript's subagent directory: `<stem>/subagents` beside
 *  it (the daemon's capsule.SubagentDir). */
function subagentDirOf(transcript: string): string {
  return join(transcript.replace(/\.jsonl$/, ""), "subagents");
}

/**
 * The subagent directories of a job's conversations, in journal order, each
 * with its harness — the same rule the daemon writes by: a conversation's
 * subagents live beside the capsule transcript that first holds it. pi: each
 * distinct spawn transcript (a resumed pi spawn names its predecessor's
 * file). claude: per native session, the session's first spawn's transcript,
 * read after the live native `<data root>/claude-config/projects/<encoded
 * capsule>/<session>/subagents/` (newer while the step runs; the capsule
 * copy is what remains once claude-config is pruned).
 */
function subagentDirs(root: string, capsuleDir: string, spawns: PolyforgeSpawn[]): { dirs: string[]; harness: PolyforgeHarness }[] {
  const out: { dirs: string[]; harness: PolyforgeHarness }[] = [];
  const seen = new Set<string>();
  for (const spawn of spawns) {
    if (spawn.harness === "pi") {
      if (!spawn.transcript || !isAbsolute(spawn.transcript) || seen.has(spawn.transcript)) continue;
      seen.add(spawn.transcript);
      out.push({ dirs: [subagentDirOf(spawn.transcript)], harness: "pi" });
    } else if (spawn.harness === "claude") {
      const session = spawn.harnessSession;
      if (!session || !HARNESS_SESSION_PATTERN.test(session) || seen.has(`claude\0${session}`)) continue;
      seen.add(`claude\0${session}`);
      const dirs = [join(root, "claude-config", "projects", encodeClaudeProjectDir(capsuleDir), session, "subagents")];
      if (spawn.transcript && isAbsolute(spawn.transcript)) dirs.push(subagentDirOf(spawn.transcript));
      out.push({ dirs, harness: "claude" });
    }
  }
  return out;
}

/** Every subagent transcript of a job (journal order of its conversation,
 *  then file name), the first directory of a conversation winning a name
 *  both hold. Each resolves inside the capsule or the data root; one
 *  without a readable meta naming its parent call is not attachable and
 *  is skipped. */
async function progressSubagentSources(root: string, capsuleDir: string, spawns: PolyforgeSpawn[]): Promise<SubagentSource[]> {
  const sources: SubagentSource[] = [];
  for (const { dirs, harness } of subagentDirs(root, capsuleDir, spawns)) {
    const taken = new Set<string>();
    for (const dir of dirs) {
      const real = await realInside(dir, [capsuleDir, root]);
      if (!real) continue;
      let names: string[];
      try {
        names = (await readdir(real)).filter((name) => SUBAGENT_TRANSCRIPT_NAME.test(name)).sort();
      } catch {
        continue;
      }
      for (const name of names) {
        if (taken.has(name)) continue;
        const path = await realInside(join(real, name), [capsuleDir, root]);
        if (!path) continue;
        const metaPath = await realInside(join(real, name.replace(/\.jsonl$/, ".meta.json")), [capsuleDir, root]);
        const meta = metaPath ? asObject(await readJson(metaPath, SUBAGENT_META_MAX_BYTES)) : undefined;
        const callId = asString(meta?.toolUseId);
        if (!meta || !callId) continue;
        taken.add(name);
        const index = typeof meta.index === "number" && Number.isSafeInteger(meta.index) ? meta.index : 0;
        const agent = asString(meta.agentType);
        sources.push({
          source: { path, harness },
          callId,
          index,
          name,
          description: maskPolyforgeCredentials(asString(meta.description) ?? ""),
          ...(agent ? { agent: maskPolyforgeCredentials(agent) } : {}),
          finished: meta.finished === true,
          ...(typeof meta.isError === "boolean" ? { isError: meta.isError } : {}),
        });
      }
    }
  }
  return sources;
}

/** The job's subagent sources by the parent call id, each call's in task
 *  order (index, then name). */
function subagentsByCall(sources: SubagentSource[]): Map<string, SubagentSource[]> {
  const byCall = new Map<string, SubagentSource[]>();
  for (const source of sources) {
    const list = byCall.get(source.callId) ?? [];
    list.push(source);
    byCall.set(source.callId, list);
  }
  for (const list of byCall.values()) list.sort((a, b) => a.index - b.index || a.name.localeCompare(b.name));
  return byCall;
}

/** A tool line waiting for the subagents of its call. */
interface PendingSubagents {
  line: PolyforgeProgressLine;
  callId: string;
}

/** The state of one subagent: finished per its meta — `error` on its
 *  meta's own verdict, else (claude, no verdict) on its parent call's
 *  result; unfinished — `running`, except a pi child whose parent call has
 *  ended: the driver records a child from stdout records, and one past the
 *  4 MiB record bound is never seen to finish, so its call's ok/error
 *  stands for it (claude's own `finished` mark needs no stand-in). */
function subagentState(sub: SubagentSource, parent: PolyforgeProgressTool | undefined): { status: PolyforgeProgressSubagent["status"]; byParent: boolean } {
  const parentEnded = parent !== undefined && parent.status !== "running";
  if (sub.finished) return { status: (sub.isError ?? parent?.status === "error") ? "error" : "done", byParent: false };
  if (sub.source.harness === "pi" && parentEnded) return { status: parent.status === "error" ? "error" : "done", byParent: true };
  return { status: "running", byParent: false };
}

/**
 * The subagents of one call, each projected like a spawn (`projectTranscript`:
 * narration + tool lines, its tool calls paired with their results, ordered
 * by time), its state from its record (`subagentState`), its first record's
 * time and, once finished, its span (one finished by its parent: the
 * parent call's duration). A subagent's own tool calls get theirs in turn
 * (claude keeps every depth in one directory), each call once, up to
 * SUBAGENT_MAX_DEPTH. `budget` is the bytes left for subagent transcripts
 * under the job bound: one that does not fit is listed with its detail
 * omitted (no lines), so subagents never cost the main view.
 */
async function projectSubagents(
  byCall: Map<string, SubagentSource[]>,
  pending: PendingSubagents,
  attached: Set<string>,
  depth: number,
  budget: { remaining: number },
): Promise<void> {
  const runs: PolyforgeProgressSubagent[] = [];
  for (const sub of byCall.get(pending.callId) ?? []) {
    const { status, byParent } = subagentState(sub, pending.line.tool);
    const head = await readSlice(sub.source.path, 0, 0);
    if (!head) continue;
    const listed = {
      description: sub.description,
      ...(sub.agent ? { agent: sub.agent } : {}),
      status,
    };
    if (head.size > budget.remaining) {
      runs.push({
        id: transcriptIdentity(sub.source.path, head, sub.source.harness), ...listed,
        ...(byParent && pending.line.tool?.durationMs !== undefined ? { durationMs: pending.line.tool.durationMs } : {}),
        lines: [], omitted: true,
      });
      continue;
    }
    budget.remaining -= head.size;
    const slice = await readSlice(sub.source.path, 0, PROGRESS_TRANSCRIPTS_MAX_BYTES);
    if (!slice) continue;
    const items: { atMs: number; order: number; line: PolyforgeProgressLine }[] = [];
    const nested: PendingSubagents[] = [];
    const span: { first?: number; last?: number } = {};
    const identity = transcriptIdentity(sub.source.path, slice, sub.source.harness);
    projectTranscript(slice.buffer, sub.source.harness, identity, 0, (atMs, line) => items.push({ atMs, order: items.length, line }), {
      span,
      onToolCall: (line, callId) => {
        if (callId && byCall.has(callId) && !attached.has(callId)) {
          attached.add(callId);
          nested.push({ line, callId });
        }
      },
    });
    if (depth < SUBAGENT_MAX_DEPTH) {
      for (const next of nested) await projectSubagents(byCall, next, attached, depth + 1, budget);
    }
    items.sort((a, b) => a.atMs - b.atMs || a.order - b.order);
    const durationMs = byParent ? pending.line.tool?.durationMs
      : sub.finished && span.first !== undefined && span.last !== undefined && span.last >= span.first ? span.last - span.first : undefined;
    runs.push({
      id: identity,
      ...listed,
      ...(span.first !== undefined ? { startAt: span.first } : {}),
      ...(durationMs !== undefined ? { durationMs } : {}),
      lines: items.map((item) => item.line),
    });
  }
  if (runs.length > 0) pending.line.subagents = runs;
}

/** A transcript's line-id identity: path, device, inode and harness hashed,
 *  so an id names no path and a replaced file is a different transcript. */
function transcriptIdentity(path: string, slice: { dev: number; ino: number }, harness: PolyforgeHarness): string {
  return createHash("sha256").update(`${path}\0${slice.dev}\0${slice.ino}\0${harness}`).digest("hex").slice(0, 16);
}

/** A tool line's id as `progressEntries` builds it:
 *  `<16-hex transcript identity>.<record byte offset>.<block index>`. */
const PROGRESS_TOOL_ID = /^([0-9a-f]{16})\.(\d{1,15})\.(\d{1,6})$/;

/** Whether a reference has a progress line id's shape (no read). Pure. */
export function isProgressToolId(value: unknown): value is string {
  return typeof value === "string" && PROGRESS_TOOL_ID.test(value);
}

/**
 * The job detail's whole-job execution progress for one
 * capsule: every spawn in the spawn journal (reruns included), each spawn's
 * transcript projected by `progressEntries` (narration + one masked tool
 * line; a `--resume` continuation is read once: pi's shared transcript
 * once, a claude session from its newest spawn's copy),
 * merged with one lifecycle line per retained event of the WI's
 * event log (`composeProgressEventLine`; `step_done` carries the duration
 * since its `step_started` of the same step and spawn). The stream is the
 * stable sort by time — events before transcript lines at the same instant,
 * then build order; a record without a readable `timestamp` sits at its
 * spawn's start plus its ordinal in microseconds — complete, no line bound
 * (`omitted` stays 0 for the wire shape).
 *
 * `wi` defaults to the capsule's own `work-item.json` id. With no capsule
 * dir (a job the daemon has dropped from its index, or a record without one),
 * only the WI's event log is read: its lifecycle lines, no
 * spawn journal and no transcript. `available: false`: neither the journal
 * nor the event log readable, the transcripts over
 * PROGRESS_TRANSCRIPTS_MAX_BYTES, or any error. Never throws; no line id
 * names a path.
 */
export async function readProgressSnapshot(root: string, capsuleDir: string | undefined, wi?: string): Promise<PolyforgeProgressSnapshot> {
  try {
    const workItem = wi ?? (capsuleDir ? asString(asObject(await readJson(join(capsuleDir, "runtime-state", "work-item.json")))?.id) : undefined);
    const spawns = capsuleDir ? await readSpawnJournal(capsuleDir) : undefined;
    const events = workItem ? await readProgressEventLog(root, workItem) : undefined;
    if (!spawns && !events) return unavailableSnapshot();
    const items: ProgressStreamItem[] = [];
    const push = (atMs: number, rank: 0 | 1, line: PolyforgeProgressLine) => items.push({ atMs, rank, order: items.length, line });

    const starts = new Map<string, number>();
    // workflow contract v2 (change 14, review r1): a restarted driver
    // re-announces a still-open ask (`awaiting_owner` with `resumed: true`,
    // driver/ask.go) — one ask is one line, keyed by its own stamp
    const asks = new Set<string>();
    for (const event of events ?? []) {
      if (event.kind === "awaiting_owner") {
        const ask = typeof event.body.ask_at === "string" && event.body.ask_at ? event.body.ask_at
          : typeof event.body.question_id === "string" ? event.body.question_id : "";
        if (ask && asks.has(ask)) continue;
        if (ask) asks.add(ask);
      }
      const atMs = Date.parse(event.at);
      const step = typeof event.body.step === "string" ? event.body.step : "";
      const spawnKey = `${step}\0${String(event.body.spawn_n ?? "")}`;
      let durationSec: number | undefined;
      if (event.kind === "step_started" && Number.isFinite(atMs)) {
        starts.set(spawnKey, atMs);
        starts.set(`${step}\0`, atMs);
      } else if (event.kind === "step_done") {
        const startMs = starts.get(spawnKey) ?? starts.get(`${step}\0`);
        if (startMs !== undefined && Number.isFinite(atMs) && atMs >= startMs) durationSec = Math.round((atMs - startMs) / 1000);
      }
      const input = { at: event.at, kind: event.kind, body: event.body, ...(durationSec !== undefined ? { durationSec } : {}) };
      const line = composeProgressEventLine(`e.${event.offset}`, input, maskPolyforgeCredentials);
      if (line) push(Number.isFinite(atMs) ? atMs : 0, 0, line);
    }

    const sources = capsuleDir && spawns ? await progressSources(root, capsuleDir, spawns) : [];
    let transcriptBytes = 0;
    for (const { source } of sources) {
      transcriptBytes += (await readSlice(source.path, 0, 0))?.size ?? 0;
    }
    if (transcriptBytes > PROGRESS_TRANSCRIPTS_MAX_BYTES) return unavailableSnapshot();
    // subagent transcripts get what the bound leaves after
    // the main ones (projectSubagents drops a detail that does not fit).
    const subagents = capsuleDir && spawns ? await progressSubagentSources(root, capsuleDir, spawns) : [];
    const budget = { remaining: PROGRESS_TRANSCRIPTS_MAX_BYTES - transcriptBytes };

    // a tool call that ran subagents carries them, on its
    // first line in stream order (each call is read once: a resumed claude
    // session from its newest copy only, `progressSources`).
    const byCall = subagentsByCall(subagents);
    const attached = new Set<string>();
    const pending: PendingSubagents[] = [];
    for (const { source, startMs } of sources) {
      const slice = await readSlice(source.path, 0, PROGRESS_TRANSCRIPTS_MAX_BYTES);
      if (!slice) continue;
      projectTranscript(slice.buffer, source.harness, transcriptIdentity(source.path, slice, source.harness), startMs, (atMs, line) => push(atMs, 1, line), {
        onToolCall: (line, callId) => {
          if (callId && byCall.has(callId) && !attached.has(callId)) {
            attached.add(callId);
            pending.push({ line, callId });
          }
        },
      });
    }
    for (const next of pending) await projectSubagents(byCall, next, attached, 1, budget);

    items.sort((a, b) => a.atMs - b.atMs || a.rank - b.rank || a.order - b.order);
    return { available: true, lines: items.map((item) => item.line), omitted: 0 };
  } catch {
    return unavailableSnapshot();
  }
}

/** The input a detail shows: a string as is, anything else as indented
 *  JSON; masked, uncapped. */
function toolInputText(input: unknown): string {
  if (typeof input === "string") return maskPolyforgeCredentials(input);
  if (input === undefined) return "";
  try {
    return maskPolyforgeCredentials(JSON.stringify(input, null, 2) ?? "");
  } catch {
    return "";
  }
}

const unavailableTool = (error: string): PolyforgeProgressToolAnswer => ({ available: false, error });

/**
 * One tool call's full detail for the expanded progress card,
 * read on demand: `id` is the tool line's id from `readProgressSnapshot`.
 * The same spawn journal and transcript resolution as the snapshot (same
 * realpath containment, same bounds); only the transcript whose identity the
 * id names is read whole, and only that call keeps its raw input and paired
 * result: `input` (a string, else indented JSON) and `output` (text blocks,
 * other blocks as `[<type>]`) masked by `maskPolyforgeCredentials`, uncapped;
 * `output: null` and `hasResult: false` while no result is paired. A
 * malformed id, no capsule or journal, a transcript no longer readable (moved,
 * replaced — a new inode is a new identity — or over the bound), or no such
 * call is `available: false` with a fixed reason. Never throws; no answer
 * names a path. The caller validates `wi` and the id shape first.
 */
export async function readProgressToolDetail(root: string, capsuleDir: string | undefined, id: string): Promise<PolyforgeProgressToolAnswer> {
  try {
    const match = PROGRESS_TOOL_ID.exec(id);
    if (!match) return unavailableTool("unknown tool call");
    if (!capsuleDir) return unavailableTool("the job's transcripts are no longer readable");
    const spawns = await readSpawnJournal(capsuleDir);
    if (!spawns) return unavailableTool("the job's transcripts are no longer readable");
    // a subagent's tool line names its subagent transcript
    const subagents = (await progressSubagentSources(root, capsuleDir, spawns)).map(({ source }) => ({ source, startMs: 0 }));
    for (const { source, startMs } of [...await progressSources(root, capsuleDir, spawns), ...subagents]) {
      const head = await readSlice(source.path, 0, 0);
      if (!head || transcriptIdentity(source.path, head, source.harness) !== match[1]) continue;
      if (head.size > PROGRESS_TRANSCRIPTS_MAX_BYTES) return unavailableTool("the transcript is too large to read");
      const slice = await readSlice(source.path, 0, PROGRESS_TRANSCRIPTS_MAX_BYTES);
      if (!slice || slice.ino !== head.ino || slice.dev !== head.dev) break;
      const call = projectTranscript(slice.buffer, source.harness, match[1], startMs, () => undefined, { keep: id });
      if (!call) return unavailableTool("the tool call is no longer in its transcript");
      const tool = call.line.tool!;
      const output = call.result ? toolResultText(call.result.content) : null;
      return {
        available: true,
        hasResult: call.result !== undefined,
        tool: { ...tool, input: toolInputText(call.input), output, isError: call.result?.isError ?? false },
      };
    }
    return unavailableTool("the tool call's transcript is no longer readable");
  } catch {
    return unavailableTool("the tool call could not be read");
  }
}

// ---- the card stream ----

async function readGoal(capsuleDir: string): Promise<string | undefined> {
  const goal = asString(asObject(await readJson(join(capsuleDir, "runtime-state", "work-item.json")))?.goal);
  return goal ? capCodePoints(goal, GOAL_MAX) : undefined;
}

function bareCard(job: PolyforgeJob): PolyforgeCard {
  return { job, steps: [], progress: { available: false, entries: [] } };
}

/** Facts of one card except its progress (no budget needed). */
async function cardFacts(job: PolyforgeJob, root: string, record: PolyforgeJobRecord | undefined, now: number): Promise<PolyforgeCard> {
  if (!isPolyforgeWorkItemId(job.wi)) return bareCard(job);
  const events = await readCardEvents(join(root, "events", `wi-${job.wi}.jsonl`));
  const startedAt = earliestJobStarted(events);
  const capsuleDir = record?.capsuleDir;
  const execution = capsuleDir ? await readExecution(capsuleDir) : undefined;
  const order = cardStepOrder(capsuleDir ? await readDeclaredSteps(capsuleDir) : undefined, events, execution);
  const goal = capsuleDir ? await readGoal(capsuleDir) : undefined;
  return {
    job,
    ...(goal ? { goal } : {}),
    ...(startedAt ? { startedAt } : {}),
    steps: computeCardSteps(order, events, execution, now),
    ...(execution?.currentStep ? { currentStep: execution.currentStep } : {}),
    progress: { available: false, entries: [] },
  };
}

/**
 * The whole card stream for `jobs` (the daemon's own `pf ops status` job
 * list), ordered by sortPolyforgeCards, with progress for each card read
 * from its cursor under one per-response entry/byte budget. Never throws:
 * a card whose files cannot be read keeps only its job facts.
 */
export async function readPolyforgeCards(
  jobs: PolyforgeJob[],
  cursors: Map<string, string>,
  now: number,
  root: string = polyforgeDataRoot(),
): Promise<PolyforgeCard[]> {
  // each job's records come from its own Cell's state dir
  // (the job's WorkRef.profile), read once per Cell
  const recordsByRoot = new Map<string, Promise<Map<string, PolyforgeJobRecord>>>();
  const recordsOf = (cellRoot: string) => {
    let read = recordsByRoot.get(cellRoot);
    if (!read) {
      read = readJobRecords(cellRoot).catch(() => new Map<string, PolyforgeJobRecord>());
      recordsByRoot.set(cellRoot, read);
    }
    return read;
  };
  const located = await Promise.all(jobs.map(async (job) => {
    const cellRoot = await polyforgeCellRoot(job.profile, root);
    const record = cellRoot && isPolyforgeWorkItemId(job.wi) ? (await recordsOf(cellRoot)).get(job.wi) : undefined;
    return { job, cellRoot, record };
  }));
  const where = new Map(located.map((entry) => [entry.job, entry]));
  const cards = sortPolyforgeCards(await Promise.all(located.map(async ({ job, cellRoot, record }) => {
    try {
      return cellRoot ? await cardFacts(job, cellRoot, record, now) : bareCard(job);
    } catch {
      return bareCard(job);
    }
  })));
  const budget: PolyforgeCardBudget = { entries: CARD_RESPONSE_MAX_ENTRIES, bytes: CARD_RESPONSE_MAX_BYTES };
  for (const card of cards) {
    const entry = where.get(card.job);
    const record = entry?.record;
    if (!entry?.cellRoot || !record?.capsuleDir) continue;
    const cursor = cursors.get(polyforgeJobRef(card.job));
    try {
      const source = await resolveTranscript(entry.cellRoot, record.capsuleDir);
      if (!source) continue;
      if (budget.entries <= 0 || budget.bytes <= 0) {
        // Out of budget: nothing read, the client's cursor stays as it is.
        card.progress = { available: true, entries: [], ...(cursor ? { cursor } : {}) };
        continue;
      }
      card.progress = await readCardProgress(source, cursor, budget);
    } catch {
      card.progress = { available: false, entries: [] };
    }
  }
  return cards;
}
