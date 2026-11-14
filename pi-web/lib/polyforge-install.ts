import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { sanitizeProjectCommandEnvironment } from "./project-command-env";
import { maskPolyforgeCredentials, probePolyforgeVersion } from "./polyforge-status";
import {
  POLYFORGE_INSTALL_FORCE_WINDOW_MS,
  POLYFORGE_INSTALL_REF,
  POLYFORGE_INSTALL_REFUSED_EXIT,
  POLYFORGE_INSTALL_REPO,
  isInstallApiKey,
  isInstallHost,
  isInstallRefusal,
  lastInstallError,
  parseInstallRefusal,
  polyforgeInstallBusyJobs,
  sanitizeInstallVersion,
  type PolyforgeInstallRun,
  type PolyforgeInstallState,
  type PolyforgeInstallVersions,
} from "./polyforge-install-pure";

/**
 * Server side of Settings › Polyforge's pf install / update entry.
 * One run at a time, owned by this server process and not
 * by the page that started it: closing the page never stops it, and a page
 * opened later reads its progress or its result here.
 *
 * A run fetches `tools/install.sh` from the release channel through `gh api`
 * (gh's own credentials; no token on any argv) into a private temp
 * directory and runs it with `bash <file> - [host]`: the api key, when a
 * first install needs one, is written to the script's stdin only — never
 * argv, never the environment, never a log, never a response. Every output
 * line is kept (bounded), the key and credential shapes masked, and fanned
 * out to the subscribed streams. install.sh's exit 3 is its refusal that
 * PF_INSTALL_FORCE=1 lifts; a forced run is accepted only as the answer to
 * such a refusal: it names that refused run, which must still be the last
 * run and at most POLYFORGE_INSTALL_FORCE_WINDOW_MS old (the page asks a
 * person to confirm it separately, against the jobs running now).
 */

export class PolyforgeInstallError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
    this.name = "PolyforgeInstallError";
  }
}

export type PolyforgeInstallEvent =
  | { type: "line"; n: number; text: string }
  | { type: "end"; run: PolyforgeInstallRun };

type Listener = (event: PolyforgeInstallEvent) => void;

interface RunRecord {
  run: PolyforgeInstallRun;
  /** The kept output window; line n (1-based) sits at index n - firstLine. */
  lines: string[];
  firstLine: number;
  listeners: Set<Listener>;
}

interface InstallStore {
  current: RunRecord | null;
  latest?: { value: string | null; error: string | null; at: number };
}

declare global {
  var __piWebPolyforgeInstall: InstallStore | undefined;
}

function store(): InstallStore {
  return globalThis.__piWebPolyforgeInstall ??= { current: null };
}

const LINE_MAX = 2000;
const LINES_KEPT = 5000;
const READ_TIMEOUT_MS = 15_000;
const FETCH_TIMEOUT_MS = 60_000;
const RUN_TIMEOUT_MS = 20 * 60_000;
const LATEST_TTL_MS = 60_000;
const SCRIPT_MAX_BYTES = 2 * 1024 * 1024;
const MESSAGE_MAX = 512;

interface Collected {
  code: number | null;
  stdout: string;
  stderr: string;
}

/** One bounded spawn (argv only, no shell, stdin closed). */
function collect(command: string, args: string[], timeoutMs: number, maxBytes = 1024 * 1024): Promise<Collected> {
  return new Promise((resolve) => {
    let stdout = "";
    let stderr = "";
    let settled = false;
    const done = (result: Collected) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(result);
    };
    const child = spawn(command, args, { shell: false, stdio: ["ignore", "pipe", "pipe"] });
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      done({ code: null, stdout, stderr: `${command} did not answer in time` });
    }, timeoutMs);
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => {
      stdout += chunk;
      if (stdout.length > maxBytes) child.kill("SIGKILL");
    });
    child.stderr.on("data", (chunk: string) => { if (stderr.length < maxBytes) stderr += chunk; });
    child.on("error", () => done({ code: null, stdout: "", stderr: `${command} is not available` }));
    child.on("close", (code) => done({ code: stdout.length > maxBytes ? null : code, stdout, stderr }));
  });
}

function boundedMessage(text: string, fallback: string): string {
  const trimmed = maskPolyforgeCredentials(text.trim());
  return (trimmed || fallback).slice(0, MESSAGE_MAX);
}

/** install.sh's own `has_config` rule: cli.toml exists and mentions api_key.
 *  Only the boolean leaves this function. */
export function polyforgeConfigHasKey(home: string = homedir()): boolean {
  try {
    return readFileSync(join(home, ".config", "polyforge", "cli.toml"), "utf8").includes("api_key");
  } catch {
    return false;
  }
}

async function readLatest(force: boolean): Promise<{ value: string | null; error: string | null }> {
  const s = store();
  if (!force && s.latest && Date.now() - s.latest.at < LATEST_TTL_MS) return s.latest;
  const run = await collect("gh", ["release", "list", "--repo", POLYFORGE_INSTALL_REPO, "--limit", "1", "--json", "tagName", "--jq", ".[0].tagName"], READ_TIMEOUT_MS);
  const value = run.code === 0 ? sanitizeInstallVersion(run.stdout) : null;
  const error = value ? null : boundedMessage(run.code === 0 ? "" : run.stderr, `no release found on ${POLYFORGE_INSTALL_REPO}`);
  s.latest = { value, error, at: Date.now() };
  return s.latest;
}

/** The versions and the job count the page shows; every read degrades on
 *  its own (a field with its reason), never an exception. `force` bypasses
 *  the presence and latest-release caches. */
export async function readPolyforgeInstallVersions(force = false): Promise<PolyforgeInstallVersions> {
  const presence = await probePolyforgeVersion(force);
  let running: string | null = null;
  let daemonReachable: boolean | null = null;
  let daemonError: string | null = null;
  let jobs: PolyforgeInstallVersions["jobs"] = [];
  if (presence.present) {
    const status = await collect(polyforgePfBin(), ["--json", "ops", "status"], READ_TIMEOUT_MS);
    let parsed: unknown = null;
    try { parsed = status.code === 0 ? JSON.parse(status.stdout) : null; } catch { parsed = null; }
    const busy = polyforgeInstallBusyJobs(parsed);
    if (busy) {
      daemonReachable = true;
      jobs = busy;
      running = sanitizeInstallVersion((parsed as { version?: unknown }).version);
    } else {
      daemonReachable = false;
      daemonError = boundedMessage(status.stderr, "the daemon does not answer");
    }
  }
  const latest = await readLatest(force);
  return {
    repo: POLYFORGE_INSTALL_REPO,
    pfPresent: presence.present,
    installed: presence.present ? presence.version : null,
    latest: latest.value,
    latestError: latest.error,
    running,
    daemonReachable,
    daemonError,
    jobs,
    piWeb: process.env.NEXT_PUBLIC_APP_VERSION ?? "",
    needsConfig: !polyforgeConfigHasKey(),
  };
}

/** The current (or last) run, without its output; null before the first. */
export function polyforgeInstallRun(): PolyforgeInstallRun | null {
  const current = store().current;
  return current ? { ...current.run } : null;
}

/** Subscribe to the current run: the kept lines after `after`, the run, and
 *  an unsubscribe. A finished run's subscriber gets its lines and then only
 *  needs the run (already final). null when no run exists. */
export function subscribePolyforgeInstall(after: number, listener: Listener): {
  lines: { n: number; text: string }[];
  run: PolyforgeInstallRun;
  unsubscribe: () => void;
} | null {
  const record = store().current;
  if (!record) return null;
  const from = Math.max(after + 1, record.firstLine);
  const lines: { n: number; text: string }[] = [];
  for (let n = from; n <= record.run.lineCount; n++) lines.push({ n, text: record.lines[n - record.firstLine] });
  if (record.run.state === "running") record.listeners.add(listener);
  return { lines, run: { ...record.run }, unsubscribe: () => { record.listeners.delete(listener); } };
}

function notify(record: RunRecord, event: PolyforgeInstallEvent) {
  for (const listener of [...record.listeners]) {
    try { listener(event); } catch { record.listeners.delete(listener); }
  }
}

function emit(record: RunRecord, raw: string, secret: string | null) {
  let text = secret ? raw.split(secret).join("***") : raw;
  text = maskPolyforgeCredentials(text).slice(0, LINE_MAX);
  record.lines.push(text);
  record.run.lineCount += 1;
  if (record.lines.length > LINES_KEPT) {
    record.lines.shift();
    record.firstLine += 1;
  }
  notify(record, { type: "line", n: record.run.lineCount, text });
}

function finish(record: RunRecord, state: Exclude<PolyforgeInstallState, "running">, exitCode: number | null, error: string | null) {
  record.run.state = state;
  record.run.exitCode = exitCode;
  record.run.endedAt = new Date().toISOString();
  record.run.error = state === "failed" ? error : null;
  record.run.refusal = state === "refused" ? parseInstallRefusal(record.lines) : null;
  notify(record, { type: "end", run: { ...record.run } });
  record.listeners.clear();
}

export interface PolyforgeInstallRequest {
  force?: unknown;
  /** A forced run names the refused run it answers. */
  refusedRun?: unknown;
  apiKey?: unknown;
  host?: unknown;
}

function currentState(): PolyforgeInstallState | null {
  return store().current?.run.state ?? null;
}

/** Why a forced run is not accepted now, or null: it must answer the last
 *  run, a refusal, by that run's id, within the force window — a refusal
 *  never stands for the jobs that run hours later. */
function forceRefusal(refusedRun: unknown, now: number = Date.now()): string | null {
  const current = store().current;
  if (!current || current.run.state !== "refused") return "a forced update is offered only right after install.sh refused an update";
  if (refusedRun !== current.run.id) return "a forced update must name the refused run it answers";
  const endedAt = current.run.endedAt ? Date.parse(current.run.endedAt) : NaN;
  if (!Number.isFinite(endedAt) || now - endedAt > POLYFORGE_INSTALL_FORCE_WINDOW_MS) {
    return `install.sh's refusal is older than ${POLYFORGE_INSTALL_FORCE_WINDOW_MS / 60_000} minutes: update again to see the jobs running now`;
  }
  return null;
}

/** The checks every await is followed by: one run at a time, and a force
 *  still answering a fresh refusal. */
function assertStartable(force: boolean, refusedRun: unknown) {
  if (currentState() === "running") throw new PolyforgeInstallError("an install is already running", 409);
  const refused = force ? forceRefusal(refusedRun) : null;
  if (refused) throw new PolyforgeInstallError(refused, 409);
}

/** Start one run (validated before anything is fetched or spawned). The
 *  answer is the new run; its output arrives through subscribe. */
export async function startPolyforgeInstall(body: PolyforgeInstallRequest): Promise<PolyforgeInstallRun> {
  const s = store();
  if (body.force !== undefined && typeof body.force !== "boolean") throw new PolyforgeInstallError("force must be a boolean", 400);
  const force = body.force === true;
  assertStartable(force, body.refusedRun);
  const needsConfig = !polyforgeConfigHasKey();
  let secret: string | null = null;
  let host: string | null = null;
  if (needsConfig) {
    if (!isInstallHost(body.host)) throw new PolyforgeInstallError("host must be the aihub host name or address (a first install writes cli.toml)", 400);
    if (!isInstallApiKey(body.apiKey)) throw new PolyforgeInstallError("apiKey must be one line of at least 8 printable characters (a first install writes cli.toml)", 400);
    secret = body.apiKey;
    host = body.host;
  }
  const presence = await probePolyforgeVersion(true);
  // the probe awaited: another request may have started a run meanwhile,
  // and the refusal a force answers may have aged out
  assertStartable(force, body.refusedRun);
  const record: RunRecord = {
    run: {
      id: randomUUID(),
      mode: presence.present ? "update" : "install",
      force,
      state: "running",
      startedAt: new Date().toISOString(),
      endedAt: null,
      exitCode: null,
      lineCount: 0,
      error: null,
      refusal: null,
    },
    lines: [],
    firstLine: 1,
    listeners: new Set(),
  };
  s.current = record;
  void execute(record, { secret, host, force });
  return { ...record.run };
}

async function execute(record: RunRecord, options: { secret: string | null; host: string | null; force: boolean }) {
  let dir: string | null = null;
  try {
    dir = mkdtempSync(join(tmpdir(), "pi-web-pf-install-"));
    emit(record, `== fetching tools/install.sh from ${POLYFORGE_INSTALL_REPO} (${POLYFORGE_INSTALL_REF})`, null);
    const fetched = await collect("gh", [
      "api", "-H", "Accept: application/vnd.github.raw",
      `repos/${POLYFORGE_INSTALL_REPO}/contents/tools/install.sh?ref=${POLYFORGE_INSTALL_REF}`,
    ], FETCH_TIMEOUT_MS, SCRIPT_MAX_BYTES);
    if (fetched.code !== 0 || !fetched.stdout.startsWith("#!")) {
      const why = boundedMessage(fetched.code === 0 ? "" : fetched.stderr, "the answer is not a script");
      const message = `could not fetch tools/install.sh from ${POLYFORGE_INSTALL_REPO}: ${why}`;
      emit(record, `ERROR: ${message}`, options.secret);
      finish(record, "failed", null, message);
      return;
    }
    const script = join(dir, "install.sh");
    writeFileSync(script, fetched.stdout, { mode: 0o700 });
    emit(record, `== running install.sh${options.force ? " with PF_INSTALL_FORCE=1" : ""}`, null);
    const { code, timedOut } = await runInstaller(record, script, options);
    if (timedOut) finish(record, "failed", code, `install.sh was stopped after ${RUN_TIMEOUT_MS / 60_000} minutes`);
    else if (code === 0) finish(record, "succeeded", 0, null);
    else if (code === POLYFORGE_INSTALL_REFUSED_EXIT && isInstallRefusal(record.lines)) finish(record, "refused", code, null);
    else finish(record, "failed", code, lastInstallError(record.lines) ?? `install.sh exited ${code ?? "on a signal"}`);
  } catch (error) {
    finish(record, "failed", null, boundedMessage(error instanceof Error ? error.message : "", "the installer could not run"));
  } finally {
    if (dir) rmSync(dir, { recursive: true, force: true });
  }
}

/** `bash -c 'exec 2>&1; exec bash "$0" "$@"' <script> - [host]`: one ordered
 *  output stream; the key (when given) on stdin only. The environment is the
 *  server's own minus its host runtime variables (the project-command
 *  sanitizer: PI_WEB_PASSWORD, PORT, NODE_ENV, NEXT_*, TMUX — nothing of
 *  Pi Web's may reach a tmux server the install starts), with the channel
 *  pinned and PF_INSTALL_FORCE / PF_HOST set only by this request — an
 *  inherited value never forces or redirects a run. */
function runInstaller(record: RunRecord, script: string, options: { secret: string | null; host: string | null; force: boolean }): Promise<{ code: number | null; timedOut: boolean }> {
  return new Promise((resolve) => {
    const env: NodeJS.ProcessEnv = { ...sanitizeProjectCommandEnvironment(process.env), PF_INSTALL_REPO: POLYFORGE_INSTALL_REPO };
    delete env.PF_INSTALL_FORCE;
    delete env.PF_HOST;
    if (options.force) env.PF_INSTALL_FORCE = "1";
    const args = ["-c", 'exec 2>&1; exec bash "$0" "$@"', script, "-", ...(options.host ? [options.host] : [])];
    const child = spawn("bash", args, { shell: false, cwd: homedir(), env, stdio: ["pipe", "pipe", "ignore"] });
    let partial = "";
    let timedOut = false;
    let settled = false;
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill("SIGTERM");
    }, RUN_TIMEOUT_MS);
    const settle = (code: number | null) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (partial) emit(record, partial, options.secret);
      partial = "";
      resolve({ code, timedOut });
    };
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => {
      const parts = (partial + chunk).split(/\r?\n/);
      partial = parts.pop() ?? "";
      for (const line of parts) emit(record, line, options.secret);
    });
    child.on("error", () => {
      emit(record, "ERROR: bash is not available", null);
      settle(null);
    });
    child.on("close", (code) => settle(code));
    child.stdin.on("error", () => {});
    child.stdin.end(options.secret ? `${options.secret}\n` : "");
  });
}
