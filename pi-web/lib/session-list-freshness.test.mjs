import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// Unit tests for the session list's freshness (polyforge#140 u3, owner ruling
// 2-A): an ordinary listing must reflect external writes that finished before
// it started — the old 30 s result cache is gone; the scan itself revalidates
// the file set and every (size, mtime) fingerprint — and a catalogue that
// keeps being invalidated mid-scan fails after bounded retries instead of
// looping forever or serving an unmarked stale result.

const jiti = createJiti(import.meta.url);
const {
  listAllSessions,
  invalidateSessionListCache,
  getSessionListVersion,
} = await jiti.import("./session-reader.ts");
const scanner = await jiti.import("./session-list-scanner.ts");
// Jiti caches imported bindings, so keep one dispatch function for all mocks.
let listSessions = scanner.listSessionsIncremental;
scanner.listSessionsIncremental = (...args) => listSessions(...args);

const timestamp = "2026-01-01T00:00:00.000Z";
const line = (entry) => JSON.stringify(entry) + "\n";
const message = (id, role, content) => ({
  type: "message", id, parentId: null, timestamp, message: { role, content },
});

function resetState() {
  globalThis.__piSessionListCache = undefined;
  globalThis.__piSessionListPromise = undefined;
  globalThis.__piSessionListPromiseGeneration = undefined;
  globalThis.__piSessionListGeneration = 0;
  scanner.resetSessionScanIndexForTests();
}

function fixture(t) {
  const previousAgentDir = process.env.PI_CODING_AGENT_DIR;
  const root = mkdtempSync(join(tmpdir(), "pi-web-freshness-"));
  const dir = join(root, "sessions", "project");
  fs.mkdirSync(dir, { recursive: true });
  process.env.PI_CODING_AGENT_DIR = root;
  resetState();
  const reads = [];
  globalThis.__piWebScanTaskLog = reads;
  t.after(() => {
    globalThis.__piWebScanTaskLog = undefined;
    listSessions = scanner.listSessionsIncremental;
    resetState();
    if (previousAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
    else process.env.PI_CODING_AGENT_DIR = previousAgentDir;
    rmSync(root, { recursive: true, force: true });
  });
  function write(id, entries = []) {
    const path = join(dir, `${id}.jsonl`);
    fs.writeFileSync(path, line({ type: "session", version: 3, id, cwd: root, timestamp }) + entries.map(line).join(""));
    return path;
  }
  return { root, write, reads };
}

test("an ordinary listing reflects external creates, appends and deletes with no force", async (t) => {
  const { write } = fixture(t);
  const a = write("a", [message("a1", "user", "first question")]);
  let listed = await listAllSessions();
  assert.equal(listed.length, 1);
  assert.equal(listed[0].messageCount, 1);

  // Another process's write, finished before the next request starts.
  fs.appendFileSync(a, line(message("a2", "assistant", "the reply")));
  listed = await listAllSessions();
  assert.equal(listed[0].messageCount, 2, "an append is seen by the very next listing");

  write("b", [message("b1", "user", "a new session")]);
  listed = await listAllSessions();
  assert.deepEqual(listed.map((session) => session.id).sort(), ["a", "b"], "a new file is seen by the very next listing");

  fs.rmSync(a);
  listed = await listAllSessions();
  assert.deepEqual(listed.map((session) => session.id), ["b"], "a deletion is seen by the very next listing");
});

test("an unchanged catalogue re-reads no transcript", async (t) => {
  const { write, reads } = fixture(t);
  write("a", [message("a1", "user", "first question")]);
  await listAllSessions();
  reads.length = 0;
  await listAllSessions();
  assert.equal(reads.length, 0, "the fingerprint revalidation reads no file content");
});

test("a scan that keeps being invalidated fails after bounded retries", async (t) => {
  fixture(t);
  let scans = 0;
  const originalListSessions = listSessions;
  listSessions = async () => {
    scans += 1;
    if (scans > 10) throw new Error("scan runaway: retries are not bounded");
    // A mutation lands while every scan is in flight.
    invalidateSessionListCache();
    return [];
  };
  t.after(() => {
    listSessions = originalListSessions;
  });

  await assert.rejects(listAllSessions(), /session list kept changing during the scan/);
  assert.equal(scans, 4, "one scan plus the retry bound of three, no more");
});

test("a request never joins a scan that started before it arrived (2-A)", async (t) => {
  const { write } = fixture(t);
  write("a", [message("a1", "user", "first question")]);
  let releaseFirstScan;
  let firstScanStarted;
  const firstScanStartedPromise = new Promise((resolve) => {
    firstScanStarted = resolve;
  });
  const firstScanGate = new Promise((resolve) => {
    releaseFirstScan = resolve;
  });
  const originalListSessions = listSessions;
  let scans = 0;
  listSessions = async (...args) => {
    scans += 1;
    if (scans === 1) {
      // The pre-write scan: it enumerated the sessions dir before the write,
      // so its result is whatever existed then — here, only "a".
      firstScanStarted();
      await firstScanGate;
      return (await originalListSessions()).filter((row) => row.id === "a");
    }
    return originalListSessions(...args);
  };
  t.after(() => {
    listSessions = originalListSessions;
  });

  // The first request's scan enumerates while a second file is written and
  // finished behind the gate; the second request arrives after that write.
  const firstCall = listAllSessions();
  await firstScanStartedPromise;
  write("late", [message("l1", "user", "written during the first scan")]);
  const secondCall = listAllSessions();
  releaseFirstScan();
  const firstRows = await firstCall;
  assert.deepEqual(firstRows.map((row) => row.id).sort(), ["a"], "the gated scan predates the write");

  const secondRows = await secondCall;
  assert.ok(secondRows.some((row) => row.id === "late"),
    "a request that arrived after the write must not be served the older scan's result");
  assert.equal(secondRows.find((row) => row.id === "late").messageCount, 1,
    "the late row is whole, not just present: its one message was counted");
});

test("one invalidation mid-scan still settles on a consistent catalogue", async (t) => {
  fixture(t);
  let scans = 0;
  const originalListSessions = listSessions;
  listSessions = async () => {
    scans += 1;
    if (scans === 1) invalidateSessionListCache();
    return [];
  };
  t.after(() => {
    listSessions = originalListSessions;
  });

  await listAllSessions();
  assert.equal(scans, 2, "the invalidated scan is retried once");
  const version = getSessionListVersion();
  await listAllSessions();
  assert.equal(getSessionListVersion(), version, "a quiet revalidation bumps nothing");
});
