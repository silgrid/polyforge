// The run side of scripts/session-list-verify.mjs (polyforge#140): one module
// per the CLI's contract, imported only when a mode actually runs. Everything
// here is machine oracle: its own `next start` per run against the corpus
// fixture, a readiness probe that never touches the session list, the
// next-server process's own /proc counters, and writes that finish (fsync)
// before the measured request starts.
import { spawn, spawnSync } from "node:child_process";
import { appendFileSync, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { open } from "node:fs/promises";
import { createServer } from "node:net";
import { join, resolve } from "node:path";
import process from "node:process";

const READY_TIMEOUT_MS = 120_000;
const MARKER_PREFIX = "PI_WEB_SESSION_VERIFY_ID=";

function mkdirp(dir) {
  mkdirSync(dir, { recursive: true });
}

async function freePort() {
  return await new Promise((resolvePromise, reject) => {
    const server = createServer();
    server.listen(0, "127.0.0.1", () => {
      const port = server.address().port;
      server.close(() => resolvePromise(port));
    });
    server.on("error", reject);
  });
}

function rcharOf(pid) {
  const match = readFileSync(`/proc/${pid}/io`, "utf8").match(/^rchar:\s+(\d+)$/m);
  if (!match) throw new Error(`cannot read rchar of pid ${pid}`);
  return Number(match[1]);
}

// The next-server process: `next start` forks it; both carry our marker env,
// so the marker plus the cmdline's "next-server" name picks the one whose
// /proc counters cover every worker thread's reads.
function findNextServerPid(marker) {
  let entries;
  try {
    entries = readdirSync("/proc", { withFileTypes: true });
  } catch {
    throw new Error("cannot read /proc");
  }
  for (const entry of entries) {
    if (!/^\d+$/.test(entry.name)) continue;
    try {
      const environ = readFileSync(`/proc/${entry.name}/environ`, "utf8");
      if (!environ.includes(`${MARKER_PREFIX}${marker}`)) continue;
      const cmdline = readFileSync(`/proc/${entry.name}/cmdline`, "utf8");
      if (cmdline.includes("next-server")) return Number(entry.name);
    } catch {
      // gone or unreadable: not ours
    }
  }
  throw new Error("next-server process not found (marker env missing)");
}

function percentile(values, fraction) {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.ceil(fraction * sorted.length) - 1)];
}

class Server {
  constructor({ dir, port, fixture, marker, logPath, staticPath }) {
    this.dir = dir;
    this.port = port;
    this.fixture = fixture;
    this.marker = marker;
    this.logPath = logPath;
    this.staticPath = staticPath ?? "/icons/icon-192.png";
    this.base = `http://127.0.0.1:${port}`;
  }

  // The server serves the corpus itself: PI_CODING_AGENT_DIR is the fixture's
  // agent dir, CLAUDE_CONFIG_DIR its claude-config, and everything else a
  // synthetic HOME under it — no inherited user config, no real daemon.
  async start() {
    mkdirp(join(this.fixture, "home"));
    mkdirp(join(this.fixture, "tmp"));
    const env = {
      ...process.env,
      HOME: join(this.fixture, "home"),
      PI_CODING_AGENT_DIR: join(this.fixture, "agent"),
      CLAUDE_CONFIG_DIR: join(this.fixture, "claude-config"),
      XDG_CONFIG_HOME: join(this.fixture, "xdg"),
      TMPDIR: join(this.fixture, "tmp"),
      POLYFORGE_DATA_DIR: join(this.fixture, "pf-data"),
      PI_WEB_SESSION_VERIFY_ID: this.marker,
      NODE_ENV: "production",
    };
    delete env.PI_WEB_DIST_DIR;
    const log = await open(this.logPath, "w");
    this.logFile = log;
    this.child = spawn(process.execPath, ["node_modules/next/dist/bin/next", "start", "-H", "127.0.0.1", "-p", String(this.port)], {
      cwd: this.dir,
      env,
      stdio: ["ignore", log.fd, log.fd],
      detached: true,
    });
    this.exitError = null;
    this.child.on("exit", (code) => {
      if (!this.stopped) this.exitError = new Error(`next start exited with ${code}; see ${this.logPath}`);
    });
    // Readiness via the static asset only: the session list is the thing under
    // test and must never be warmed by the probe (a cold summary stays cold).
    const deadline = Date.now() + READY_TIMEOUT_MS;
    for (;;) {
      if (this.exitError) throw this.exitError;
      try {
        const response = await fetch(`${this.base}${this.staticPath}`);
        if (response.status === 200) {
          await response.arrayBuffer();
          break;
        }
      } catch {
        // not up yet
      }
      if (Date.now() > deadline) throw new Error(`readiness timed out; see ${this.logPath}`);
      await new Promise((resolveSleep) => setTimeout(resolveSleep, 100));
    }
    this.pid = findNextServerPid(this.marker);
    return this;
  }

  urlFor(mode) {
    const query = mode === "summary" ? "?summary=1" : mode === "force" ? "?force=1" : "";
    return `${this.base}/api/sessions${query}`;
  }

  async call(mode) {
    const started = Date.now();
    const response = await fetch(this.urlFor(mode), { headers: { "Cache-Control": "no-store" } });
    const body = await response.text();
    return { mode, status: response.status, ms: Date.now() - started, body };
  }

  async stop() {
    this.stopped = true;
    if (this.child?.pid) {
      try {
        process.kill(-this.child.pid, "SIGTERM");
      } catch {
        // already gone
      }
    }
    await this.logFile?.close().catch(() => undefined);
  }
}

async function withServer(details, fixture, run, { dropIndex = false } = {}) {
  const port = details.port || (await freePort());
  const marker = `v${process.pid}-${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
  if (dropIndex) {
    const index = join(fixture, "agent", "pi-web-session-index.json");
    if (existsSync(index)) rmSync(index);
  }
  mkdirp(details.out);
  const server = new Server({
    dir: resolve("."),
    port,
    fixture,
    marker,
    logPath: join(details.out, `server-${marker}.log`),
    staticPath: details.staticPath,
  });
  try {
    await server.start();
    return await run(server);
  } finally {
    await server.stop();
  }
}

// A finished write: the file writes are closed and flushed; the directory
// entry is fsynced too, so "completed before the request" is what the OS says.
async function flushWrites(dir) {
  let handle;
  try {
    handle = await open(dir, "r");
    await handle.sync();
  } catch {
    // best-effort on filesystems without directory fsync
  } finally {
    await handle?.close().catch(() => undefined);
  }
}

// ---- responsiveness ---------------------------------------------------------
// Each mode is measured cold and hot apart (review r2#7): a cold run starts a
// fresh process against a dropped index and its FIRST list call is the measured
// one — no warm call, ever; a hot run makes exactly one unmeasured call first
// (that is what hot means). Samples count only while they overlap a measured
// call; a cold start that cannot reach the sample bound is repeated
// independently and the overlapping samples accumulate.
const MAX_COLD_STARTS = 10;

async function measureWindow(server, details) {
  const rcharBefore = rcharOf(server.pid);
  const samples = [];
  let probing = true;
  const probeLoop = (async () => {
    while (probing) {
      const started = performance.now();
      try {
        const response = await fetch(`${server.base}${details.staticPath}`);
        // A slow or failing probe is a sample, never a dropped one.
        samples.push(response.status === 200 ? performance.now() - started : READY_TIMEOUT_MS);
        await response.arrayBuffer();
      } catch {
        samples.push(READY_TIMEOUT_MS);
      }
      await new Promise((resolveSleep) => setTimeout(resolveSleep, details.intervalMs));
    }
  })();
  const call = await server.call(details.mode);
  // Let trailing probes drain so the window covers the call's tail.
  await new Promise((resolveSleep) => setTimeout(resolveSleep, Math.max(200, details.intervalMs * 2)));
  probing = false;
  await probeLoop;
  return { call, samples, readBytes: rcharOf(server.pid) - rcharBefore };
}

async function responsiveness(details, fixture) {
  const results = {};
  let failures = 0;
  for (const mode of details.modes) {
    const modeDetails = { ...details, mode };
    results[mode] = {};
    for (const shape of details.shapes) {
      const samples = [];
      const calls = [];
      let readBytes = 0;
      let attempt = 0;
      while (samples.length < details.minSamples && attempt < MAX_COLD_STARTS) {
        attempt += 1;
        await withServer(modeDetails, fixture, async (server) => {
          if (shape === "hot") {
            // Hot means warmed: exactly one unmeasured call first.
            const warm = await server.call(mode);
            if (warm.status !== 200) throw new Error(`hot warm-up ${mode} call: HTTP ${warm.status}`);
          }
          const measured = await measureWindow(server, modeDetails);
          samples.push(...measured.samples);
          calls.push({ status: measured.call.status, ms: measured.call.ms });
          readBytes += measured.readBytes;
        }, { dropIndex: shape === "cold" });
      }
      const p99 = percentile(samples, 0.99);
      const max = samples.length > 0 ? Math.max(...samples) : READY_TIMEOUT_MS;
      const ok = calls.length > 0 && calls.every((call) => call.status === 200)
        && samples.length >= details.minSamples && p99 < details.p99Ms;
      if (!ok) failures += 1;
      results[mode][shape] = {
        coldStarts: shape === "cold" ? attempt : 1,
        samples: samples.length,
        p99,
        max,
        calls,
        readBytes,
        ok,
      };
    }
  }
  return { ok: failures === 0, results };
}

// ---- read-volume ------------------------------------------------------------
// Each mode measures on its own server, started against a cold (dropped) index,
// so no mode's run can warm another's (review r2#8): the first call builds the
// index, the last one is measured.
async function readVolume(details, fixture) {
  const results = {};
  const totalBytes = details.manifestTotals.piBytes + details.manifestTotals.claudeBytes;
  let failures = 0;
  for (const mode of details.modes) {
    const reads = [];
    await withServer(details, fixture, async (server) => {
      for (let call = 1; call <= details.calls; call += 1) {
        const before = rcharOf(server.pid);
        const answer = await server.call(mode);
        reads.push({ call, status: answer.status, rchar: rcharOf(server.pid) - before });
      }
    }, { dropIndex: true });
    const second = reads[reads.length - 1];
    const ratio = totalBytes > 0 ? second.rchar / totalBytes : 0;
    const ok = reads.every((read) => read.status === 200) && ratio < details.maxSecondReadRatio;
    if (!ok) failures += 1;
    results[mode] = { reads, totalBytes, secondReadBytes: second.rchar, ratio, ok };
  }
  return { ok: failures === 0, results };
}

// ---- cold-summary -----------------------------------------------------------
// Both cold shapes: a cold process with a persisted index this same oracle
// built on a prior server (never assumed, review r2 N12), and a cold process
// with an empty one. Neither may pre-warm the list — readiness is the static
// asset and the first session-list request is the measured one.
async function coldSummary(details, fixture) {
  const runs = details.emptyIndex === false ? ["warm-index"] : ["warm-index", "empty-index"];
  const totalBytes = details.manifestTotals.piBytes + details.manifestTotals.claudeBytes;
  const results = {};
  let failures = 0;
  for (const shape of runs) {
    if (shape === "warm-index") {
      // Build the persisted index with a prior run: one ordinary call on its
      // own server; the measured cold start inherits what it wrote.
      await withServer(details, fixture, async (server) => {
        const warm = await server.call("normal");
        if (warm.status !== 200) throw new Error(`warm-index build call: HTTP ${warm.status}`);
      });
    }
    await withServer(details, fixture, async (server) => {
      const before = rcharOf(server.pid);
      const answer = await server.call("summary");
      const readBytes = rcharOf(server.pid) - before;
      const ok = answer.status === 200 && answer.ms <= details.maxMs && readBytes <= details.maxReadRatio * totalBytes;
      if (!ok) failures += 1;
      results[shape] = { ms: answer.ms, status: answer.status, readBytes, maxReadBytes: details.maxReadRatio * totalBytes, totalBytes, ok };
    }, { dropIndex: shape === "empty-index" });
  }
  return { ok: failures === 0, results };
}

// ---- golden ------------------------------------------------------------------
function diffValues(baseline, candidate, prefix = "") {
  const differences = [];
  const keys = new Set([...Object.keys(baseline ?? {}), ...Object.keys(candidate ?? {})]);
  for (const key of keys) {
    const path = prefix ? `${prefix}.${key}` : key;
    const a = baseline?.[key];
    const b = candidate?.[key];
    const plain = (value) => value === null || ["string", "number", "boolean", "undefined"].includes(typeof value);
    if (plain(a) && plain(b)) {
      if (a !== b) differences.push({ path, baseline: a, candidate: b });
    } else if (Array.isArray(a) && Array.isArray(b)) {
      if (a.length !== b.length) differences.push({ path, baseline: a.length, candidate: b.length, note: "array length" });
      for (let index = 0; index < Math.min(a.length, b.length); index += 1) {
        differences.push(...diffValues(a[index], b[index], `${path}[${index}]`));
      }
    } else {
      differences.push(...diffValues(a, b, path));
    }
  }
  return differences;
}

/**
 * Summary rows follow owner 1-A: a claude row whose transcript was never
 * parsed defers its count, so its `messageCount` may read 0 with
 * `detailsPending: true`. Every other summary difference is a failure, and a
 * deferred difference is only allowed on a row that says it is deferred.
 */
function allowedSummaryDifferences(baselineRows, candidateRows) {
  const differences = [];
  const byId = new Map(candidateRows.map((row) => [row.id, row]));
  for (const row of baselineRows) {
    const candidate = byId.get(row.id);
    if (candidate?.harness === "claude" && candidate.detailsPending === true
      && (row.messageCount ?? 0) !== candidate.messageCount) {
      differences.push({ path: `sessions[id=${row.id}].messageCount`, baseline: row.messageCount, candidate: candidate.messageCount, note: "deferred claude count (owner 1-A)" });
    }
  }
  return differences;
}

async function captureList(details, fixture, { dir, outPrefix }) {
  const port = details.port || (await freePort());
  const marker = `g-${outPrefix}-${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
  mkdirp(details.out);
  const server = new Server({
    dir,
    port,
    fixture,
    marker,
    logPath: join(details.out, `server-${marker}.log`),
    staticPath: details.staticPath,
  });
  try {
    await server.start();
    const captured = {};
    for (const mode of ["summary", "normal", "force"]) {
      const answer = await server.call(mode);
      if (answer.status !== 200) throw new Error(`${outPrefix} ${mode} call: HTTP ${answer.status}`);
      captured[mode] = JSON.parse(answer.body);
    }
    return captured;
  } finally {
    await server.stop();
  }
}

async function golden(details, fixture) {
  // The baseline commit serves from its own worktree with its own dependencies
  // and build; the candidate is this checkout's build. Same corpus, same call
  // order, field-by-field diff with nothing silently dropped. The list is
  // never pre-warmed before the captured calls, and each variant starts from
  // no persisted index so both scan the same files the same way.
  const baselineDir = join(fixture, "baseline-worktree");
  if (existsSync(baselineDir)) rmSync(baselineDir, { recursive: true });
  const worktree = spawnSync("git", ["worktree", "add", "-f", baselineDir, details.baseline], { encoding: "utf8" });
  if (worktree.status !== 0) throw new Error(`git worktree add failed: ${worktree.stderr}`);
  try {
    const piWebBaseline = join(baselineDir, "pi-web");
    for (const command of [["npm", ["ci", "--no-audit", "--no-fund"]], ["npm", ["run", "build"]]]) {
      const result = spawnSync(command[0], command[1], { cwd: piWebBaseline, encoding: "utf8", env: { ...process.env, PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD: "1" } });
      if (result.status !== 0) throw new Error(`${command[1][0]} failed in the baseline worktree:\n${result.stdout}\n${result.stderr}`);
    }
    let index = join(fixture, "agent", "pi-web-session-index.json");
    if (existsSync(index)) rmSync(index);
    const baselineList = await captureList(details, fixture, { dir: piWebBaseline, outPrefix: "baseline" });
    if (existsSync(index)) rmSync(index);
    const candidateList = await captureList(details, fixture, { dir: resolve("."), outPrefix: "candidate" });
    if (existsSync(index)) rmSync(index);

    const strip = (body) => {
      const copy = { ...body };
      for (const field of details.transientFields) delete copy[field];
      return copy;
    };
    const failures = [];
    for (const mode of ["normal", "force"]) {
      const differences = diffValues(strip(baselineList[mode]), strip(candidateList[mode]));
      if (differences.length > 0) failures.push(...differences.map((difference) => ({ mode, ...difference })));
    }
    // Summary: strict except the owner-ruled deferred claude counts.
    const summaryDiff = diffValues(strip(baselineList.summary), strip(candidateList.summary));
    const allowed = allowedSummaryDifferences(baselineList.summary.sessions ?? [], candidateList.summary.sessions ?? []);
    const allowedKeys = new Set(allowed.map((difference) => difference.path));
    for (const difference of summaryDiff) {
      if (!allowedKeys.has(difference.path)) failures.push({ mode: "summary", ...difference });
    }
    return { ok: failures.length === 0, differences: failures, allowed, baselineDir };
  } finally {
    spawnSync("git", ["worktree", "remove", "--force", baselineDir], { encoding: "utf8" });
  }
}

// ---- mutations --------------------------------------------------------------
const nowIso = () => new Date().toISOString();
const line = (entry) => `${JSON.stringify(entry)}\n`;

async function mutations(details, fixture) {
  const results = {};
  let failures = 0;
  await withServer(details, fixture, async (server) => {
    const agentDir = join(fixture, "agent");
    const claudeConfigDir = join(fixture, "claude-config");
    const piDir = join(agentDir, "sessions", "mutation-project");
    mkdirp(piDir);
    const claudeDir = join(claudeConfigDir, "projects", "-mutation");
    mkdirp(claudeDir);
    const writePi = (id, entries) => {
      const path = join(piDir, `mutation-${id}.jsonl`);
      writeFileSync(path, line({ type: "session", version: 3, id, cwd: piDir, timestamp: nowIso() }) + entries.map(line).join(""));
      return path;
    };
    const message = (role, content) => ({ type: "message", id: `${role}-${Date.now()}`, parentId: null, timestamp: nowIso(), message: { role, content } });

    const checks = [];
    if (details.harnesses.includes("pi")) {
      const createdPath = writePi("create", [message("user", "created")]);
      checks.push({ label: "pi:create", write: () => undefined, expect: (rows) => rows.some((row) => row.id === "mutation-create" && row.path === createdPath) });
      const appendPath = writePi("append", [message("user", "first")]);
      checks.push({ label: "pi:append", write: () => appendFileSync(appendPath, line(message("assistant", "grown"))), expect: (rows) => rows.find((row) => row.path === appendPath)?.messageCount === 2 });
      checks.push({ label: "pi:delete", write: () => rmSync(appendPath), expect: (rows) => !rows.some((row) => row.path === appendPath) });
    }
    if (details.harnesses.includes("claude")) {
      const id = "mutation-claude-1";
      const transcript = join(claudeDir, `${id}.jsonl`);
      const index = join(agentDir, "claude-sessions.json");
      const record = { id, cwd: claudeDir, model: "claude-sonnet-5-5", created: nowIso(), modified: nowIso(), firstMessage: "mutated", usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0 } };
      const writeIndex = () => {
        const parsed = existsSync(index) ? JSON.parse(readFileSync(index, "utf8")) : { version: 1, sessions: [] };
        parsed.sessions = parsed.sessions.filter((existing) => existing.id !== id).concat(record);
        writeFileSync(index, JSON.stringify(parsed));
      };
      checks.push({ label: "claude:index", write: () => {
        writeFileSync(transcript, line({ parentUuid: null, isSidechain: false, type: "user", message: { role: "user", content: "mutated" }, uuid: "m1", timestamp: nowIso(), sessionId: id }) + line({ type: "last-prompt", leafUuid: "m1", sessionId: id }));
        writeIndex();
      }, expect: (rows) => rows.some((row) => row.id === id && row.harness === "claude") });
      checks.push({ label: "claude:append", write: () => appendFileSync(transcript, line({ parentUuid: "m1", isSidechain: false, type: "assistant", message: { role: "assistant", content: [{ type: "text", text: "reply" }] }, uuid: "m2", timestamp: nowIso(), sessionId: id })), expect: (rows) => (rows.find((row) => row.id === id)?.messageCount ?? 0) > 1 });
      // 3-A: deleting the transcript keeps the registered row, path empty, count 0.
      checks.push({ label: "claude:delete", write: () => rmSync(transcript), expect: (rows) => rows.some((row) => row.id === id && row.path === "" && row.messageCount === 0) });
    }

    for (const check of checks) {
      check.write();
      await flushWrites(piDir);
      const answer = await server.call("normal");
      const rows = JSON.parse(answer.body).sessions;
      const ok = answer.status === 200 && check.expect(rows);
      if (!ok) failures += 1;
      results[check.label] = { ok };
    }
  });
  return { ok: failures === 0, results };
}

export async function run(details) {
  const fixture = details.fixture;
  let report;
  if (details.mode === "responsiveness") report = await responsiveness(details, fixture);
  else if (details.mode === "read-volume") report = await readVolume(details, fixture);
  else if (details.mode === "cold-summary") report = await coldSummary(details, fixture);
  else if (details.mode === "golden") report = await golden(details, fixture);
  else if (details.mode === "mutations") report = await mutations(details, fixture);
  else throw new Error(`unknown mode ${details.mode}`);
  const outFile = join(details.out, "result.json");
  mkdirp(details.out);
  writeFileSync(outFile, `${JSON.stringify({ mode: details.mode, ...report }, null, 2)}\n`);
  console.log(JSON.stringify({ mode: details.mode, ok: report.ok, out: outFile }));
  if (!report.ok) process.exitCode = 1;
}

export { diffValues as _diffValues, allowedSummaryDifferences as _allowedSummaryDifferences };
