import "#test-env";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const history = await jiti.import("./polyforge-config-history.ts");

function entry(key, value, secret = false) {
  return {
    id: `${key}-${value}`,
    at: new Date().toISOString(),
    scope: key.startsWith("summarize.") ? "workspace" : "cli",
    cwd: "/ws",
    key,
    old: secret ? { set: true } : value - 1,
    new: secret ? { set: true } : value,
    secret,
    rollbackable: !secret,
  };
}

test("the history is bounded at 50, newest first, and mode 0600", async (t) => {
  const dir = mkdtempSync(join(tmpdir(), "pf-history-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  for (let i = 1; i <= 55; i++) await history.appendPolyforgeConfigHistory(entry("max_concurrent_jobs", i), dir);
  const entries = history.readPolyforgeConfigHistory(dir);
  assert.equal(entries.length, 50);
  assert.equal(entries[0].new, 55);
  const mode = statSync(history.historyPath(dir)).mode & 0o777;
  assert.equal(mode, 0o600);
});

test("api_key history stores set/unset only and is never rollbackable", async (t) => {
  const dir = mkdtempSync(join(tmpdir(), "pf-history-secret-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  await history.appendPolyforgeConfigHistory(entry("api_key", 1, true), dir);
  const text = readFileSync(history.historyPath(dir), "utf8");
  assert.ok(!text.includes("SENTINEL"));
  const [stored] = history.readPolyforgeConfigHistory(dir);
  assert.equal(stored.secret, true);
  assert.equal(stored.rollbackable, false);
  assert.deepEqual(stored.new, { set: true });
});

test("workspace entries carry their resolved workspace, not the panel cwd", async (t) => {
  const dir = mkdtempSync(join(tmpdir(), "pf-history-ws-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const resolved = "/srv/ws-a";
  await history.appendPolyforgeConfigHistory({ ...entry("summarize.interval_sec", 30), cwd: resolved }, dir);
  const [stored] = history.readPolyforgeConfigHistory(dir);
  assert.equal(stored.scope, "workspace");
  assert.equal(stored.cwd, resolved);
});
