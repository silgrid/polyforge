import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const index = await jiti.import("./claude-index.ts");
const { getSessionListVersion } = await jiti.import("./session-reader.ts");

function agentDir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-index-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function record(id, extra = {}) {
  return {
    id,
    cwd: "/work",
    model: "claude-sonnet-5-5",
    created: "2026-11-01T00:00:00.000Z",
    modified: "2026-11-01T00:00:00.000Z",
    firstMessage: `hello ${id}`,
    usage: { ...index.EMPTY_CLAUDE_USAGE },
    ...extra,
  };
}

test("a missing or corrupt index reads as empty", (t) => {
  const dir = agentDir(t);
  assert.deepEqual(index.readClaudeIndex(dir), []);
  fs.writeFileSync(index.claudeIndexPath(dir), "{not json");
  assert.deepEqual(index.readClaudeIndex(dir), []);
  fs.writeFileSync(index.claudeIndexPath(dir), JSON.stringify({ sessions: [{ id: "x" }, record("ok")] }));
  assert.deepEqual(index.readClaudeIndex(dir).map((entry) => entry.id), ["ok"]);
});

test("upsert, rename, archive and remove persist, each bumping the list version", async (t) => {
  const dir = agentDir(t);
  let version = getSessionListVersion();
  await index.upsertClaudeSessionRecord(record("a"), dir);
  assert.ok(getSessionListVersion() > version);
  await index.upsertClaudeSessionRecord(record("b"), dir);
  assert.deepEqual(index.readClaudeIndex(dir).map((entry) => entry.id), ["a", "b"]);

  version = getSessionListVersion();
  await index.updateClaudeSessionRecord("a", (entry) => ({ ...entry, name: "Renamed", archived: true }), dir);
  assert.ok(getSessionListVersion() > version);
  assert.equal(index.getClaudeSessionRecord("a", dir).name, "Renamed");
  assert.equal(index.getClaudeSessionRecord("a", dir).archived, true);
  assert.equal(await index.updateClaudeSessionRecord("missing", (entry) => entry, dir), undefined);

  assert.equal(await index.removeClaudeSessionRecord("a", dir), true);
  assert.equal(await index.removeClaudeSessionRecord("a", dir), false);
  assert.deepEqual(index.readClaudeIndex(dir).map((entry) => entry.id), ["b"]);
  if (process.platform !== "win32") assert.equal(fs.statSync(index.claudeIndexPath(dir)).mode & 0o777, 0o600);
  assert.deepEqual(fs.readdirSync(dir), ["claude-sessions.json"]);
});

test("concurrent writes are serialized: none is lost", async (t) => {
  const dir = agentDir(t);
  await Promise.all(Array.from({ length: 20 }, (_, i) => index.upsertClaudeSessionRecord(record(`s${i}`), dir)));
  assert.equal(index.readClaudeIndex(dir).length, 20);
});

test("usage totals add each turn's result aggregate", async (t) => {
  const dir = agentDir(t);
  await index.upsertClaudeSessionRecord(record("u"), dir);
  await index.recordClaudeUsage("u", { input: 10, output: 5, cacheRead: 100, cacheWrite: 7, cost: 0.5 }, dir);
  await index.recordClaudeUsage("u", { input: 1, output: 2, cacheRead: 3, cacheWrite: 4, cost: 0.25 }, dir);
  assert.deepEqual(index.getClaudeSessionRecord("u", dir).usage, { input: 11, output: 7, cacheRead: 103, cacheWrite: 11, cost: 0.75 });
});

test("legacy #219 records are read without dropping any: a model name stays as claude takes it (an alias included), preset ignored, an invalid name -> default", (t) => {
  const dir = agentDir(t);
  const legacy = (id, extra) => ({ id, cwd: "/work", created: "2026-11-01T00:00:00.000Z", ...extra });
  const text = JSON.stringify({ version: 1, sessions: [
    legacy("alias", { modelAlias: "sonnet", preset: "read-only" }),
    legacy("opus", { modelAlias: "opus", preset: "writable" }),
    legacy("haiku", { modelAlias: "haiku" }),
    legacy("invalid", { model: "-not a model name" }),
    legacy("bad-preset", { modelAlias: "sonnet", preset: "full" }),
    legacy("none", {}),
    legacy("full", { model: "claude-fable-5-1", modelAlias: "haiku" }),
  ] });
  fs.writeFileSync(index.claudeIndexPath(dir), text);
  const read = Object.fromEntries(index.readClaudeIndex(dir).map((entry) => [entry.id, entry]));
  assert.deepEqual(Object.keys(read), ["alias", "opus", "haiku", "invalid", "bad-preset", "none", "full"]);
  // claude's own aliases are passed to --model verbatim: no table maps them to full names.
  assert.equal(read.alias.model, "sonnet");
  assert.equal(read.opus.model, "opus");
  assert.equal(read.haiku.model, "haiku");
  assert.equal(read.invalid.model, "default");
  assert.equal(read["bad-preset"].model, "sonnet");
  assert.equal(read.none.model, "default");
  assert.equal(read.full.model, "claude-fable-5-1", "the canonical field wins over a legacy alias");
  for (const entry of Object.values(read)) {
    assert.ok(!("preset" in entry), entry.id);
    assert.ok(!("modelAlias" in entry), entry.id);
  }
  assert.equal(fs.readFileSync(index.claudeIndexPath(dir), "utf8"), text, "reading never rewrites the file");
});

test("a new record stores the full model name and no preset or alias", async (t) => {
  const dir = agentDir(t);
  await index.upsertClaudeSessionRecord(record("new", { model: "claude-haiku-5-5" }), dir);
  const stored = JSON.parse(fs.readFileSync(index.claudeIndexPath(dir), "utf8")).sessions[0];
  assert.equal(stored.model, "claude-haiku-5-5");
  assert.ok(!("preset" in stored));
  assert.ok(!("modelAlias" in stored));
});

test("a pre-#272 record without effort reads as the default, reading never rewrites, the next write stores model + effort", async (t) => {
  const dir = agentDir(t);
  const legacy = { id: "old", cwd: "/work", modelAlias: "sonnet", preset: "read-only", created: "2026-11-01T00:00:00.000Z", firstMessage: "hi", usage: {} };
  fs.writeFileSync(index.claudeIndexPath(dir), JSON.stringify({ version: 1, sessions: [legacy] }));
  const before = fs.readFileSync(index.claudeIndexPath(dir), "utf8");
  const [read] = index.readClaudeIndex(dir);
  assert.equal(read.model, "sonnet", "the legacy alias is the name claude takes");
  assert.equal(read.effort, "default");
  assert.equal(fs.readFileSync(index.claudeIndexPath(dir), "utf8"), before, "reading rewrote nothing");
  await index.updateClaudeSessionRecord("old", (entry) => ({ ...entry, effort: "high" }), dir);
  const [stored] = JSON.parse(fs.readFileSync(index.claudeIndexPath(dir), "utf8")).sessions;
  assert.equal(stored.model, "sonnet");
  assert.equal(stored.effort, "high");
  assert.ok(!("modelAlias" in stored) && !("preset" in stored));
  fs.writeFileSync(index.claudeIndexPath(dir), JSON.stringify({ version: 1, sessions: [{ ...legacy, effort: "off" }] }));
  assert.equal(index.readClaudeIndex(dir)[0].effort, "default", "an unknown effort reads as the default");
});

test("the Agent language round-trips; an absent or unknown value reads as English; a legacy record still parses", async (t) => {
  const dir = agentDir(t);
  await index.upsertClaudeSessionRecord(record("zh", { agentLanguage: "zh-CN" }), dir);
  assert.equal(index.getClaudeSessionRecord("zh", dir).agentLanguage, "zh-CN");
  assert.equal(JSON.parse(fs.readFileSync(index.claudeIndexPath(dir), "utf8")).sessions[0].agentLanguage, "zh-CN");
  const legacy = { id: "old", cwd: "/work", modelAlias: "sonnet", preset: "read-only", created: "2026-11-01T00:00:00.000Z", firstMessage: "hi", usage: {} };
  fs.writeFileSync(index.claudeIndexPath(dir), JSON.stringify({ version: 1, sessions: [legacy, { ...legacy, id: "odd", agentLanguage: "klingon" }] }));
  const [old, odd] = index.readClaudeIndex(dir);
  assert.equal(old.agentLanguage, "en");
  assert.equal(old.model, "sonnet");
  assert.equal(odd.agentLanguage, "en");
  // A later write (here a model switch) keeps a 中文 record 中文.
  fs.writeFileSync(index.claudeIndexPath(dir), JSON.stringify({ version: 1, sessions: [record("kept", { agentLanguage: "zh-CN" })] }));
  await index.updateClaudeSessionRecord("kept", (entry) => ({ ...entry, model: "claude-haiku-5-5" }), dir);
  assert.equal(index.getClaudeSessionRecord("kept", dir).agentLanguage, "zh-CN");
});

test("a record without launchPermissionMode or includeHookEvents reads as default and off", async (t) => {
  const dir = agentDir(t);
  await index.upsertClaudeSessionRecord(record("set", { launchPermissionMode: "acceptEdits", includeHookEvents: true }), dir);
  const set = index.getClaudeSessionRecord("set", dir);
  assert.equal(set.launchPermissionMode, "acceptEdits");
  assert.equal(set.includeHookEvents, true);
  const stored = JSON.parse(fs.readFileSync(index.claudeIndexPath(dir), "utf8")).sessions[0];
  assert.equal(stored.launchPermissionMode, "acceptEdits");
  assert.equal(stored.includeHookEvents, true);

  const legacy = { id: "old", cwd: "/work", model: "claude-sonnet-5-5", created: "2026-11-01T00:00:00.000Z", firstMessage: "hi", usage: {} };
  fs.writeFileSync(index.claudeIndexPath(dir), JSON.stringify({ version: 1, sessions: [
    legacy,
    { ...legacy, id: "odd", launchPermissionMode: "bypassPermissions", includeHookEvents: "yes" },
    { ...legacy, id: "plan", launchPermissionMode: "plan", includeHookEvents: true },
  ] }));
  const [old, odd, plan] = index.readClaudeIndex(dir);
  assert.equal(old.launchPermissionMode, "default");
  assert.equal(old.includeHookEvents, false);
  assert.equal(odd.launchPermissionMode, "default");
  assert.equal(odd.includeHookEvents, false);
  assert.equal(plan.launchPermissionMode, "plan");
  assert.equal(plan.includeHookEvents, true);
  // A later write (here a model switch) keeps what the session was created with.
  await index.updateClaudeSessionRecord("plan", (entry) => ({ ...entry, model: "claude-haiku-5-5" }), dir);
  assert.equal(index.getClaudeSessionRecord("plan", dir).launchPermissionMode, "plan");
  assert.equal(index.getClaudeSessionRecord("plan", dir).includeHookEvents, true);
});

// self-review: #219 stored "claude-default" for claude's own default model. It is
// no claude alias, so passing it to --model verbatim would make an old session unresumable.
test("a #219 record whose modelAlias is claude-default reads as claude's default, not as a model name", (t) => {
  const dir = agentDir(t);
  const record = { id: "old-default", cwd: "/work", created: "2026-11-01T00:00:00.000Z", modelAlias: "claude-default", preset: "writable" };
  fs.writeFileSync(index.claudeIndexPath(dir), JSON.stringify({ version: 1, sessions: [record] }));
  const [read] = index.readClaudeIndex(dir);
  assert.equal(read.model, "default");
});

test("a record keeps's launchPermissionMode and includeHookEvents beside's permissionMode override, each read on its own ( with)", async (t) => {
  const dir = agentDir(t);
  await index.upsertClaudeSessionRecord(record("both", { launchPermissionMode: "acceptEdits", includeHookEvents: true, permissionMode: "deny" }), dir);
  const both = index.getClaudeSessionRecord("both", dir);
  assert.equal(both.permissionMode, "deny");
  assert.equal(both.launchPermissionMode, "acceptEdits");
  assert.equal(both.includeHookEvents, true);
  const [stored] = JSON.parse(fs.readFileSync(index.claudeIndexPath(dir), "utf8")).sessions;
  assert.equal(stored.permissionMode, "deny");
  assert.equal(stored.launchPermissionMode, "acceptEdits");
  assert.equal(stored.includeHookEvents, true);

  await index.upsertClaudeSessionRecord(record("follow"), dir);
  const follow = index.getClaudeSessionRecord("follow", dir);
  assert.ok(!("permissionMode" in follow), "no override: the global approval mode applies");
  assert.equal(follow.launchPermissionMode, "default");
  assert.equal(follow.includeHookEvents, false);

  // A launch permission mode stored under #442's key is no approval override.
  fs.writeFileSync(index.claudeIndexPath(dir), JSON.stringify({ version: 1, sessions: [record("mixed", { permissionMode: "acceptEdits", launchPermissionMode: "plan" })] }));
  const [mixed] = index.readClaudeIndex(dir);
  assert.ok(!("permissionMode" in mixed));
  assert.equal(mixed.launchPermissionMode, "plan");
});

test("a record's permissionMode override round-trips; an unknown value reads as no override", async (t) => {
  const dir = agentDir(t);
  await index.upsertClaudeSessionRecord(record("ask", { permissionMode: "ask" }), dir);
  await index.upsertClaudeSessionRecord(record("deny", { permissionMode: "deny" }), dir);
  await index.upsertClaudeSessionRecord(record("follow"), dir);
  assert.equal(index.getClaudeSessionRecord("ask", dir).permissionMode, "ask");
  assert.equal(index.getClaudeSessionRecord("deny", dir).permissionMode, "deny");
  assert.ok(!("permissionMode" in index.getClaudeSessionRecord("follow", dir)), "no override: the global mode applies");
  const stored = JSON.parse(fs.readFileSync(index.claudeIndexPath(dir), "utf8")).sessions;
  assert.deepEqual(stored.map((entry) => entry.permissionMode), ["ask", "deny", undefined]);
  // A later write keeps the override; clearing it removes the key.
  await index.updateClaudeSessionRecord("deny", (entry) => ({ ...entry, model: "claude-haiku-5-5" }), dir);
  assert.equal(index.getClaudeSessionRecord("deny", dir).permissionMode, "deny");
  await index.updateClaudeSessionRecord("deny", ({ permissionMode: _cleared, ...entry }) => entry, dir);
  assert.ok(!("permissionMode" in index.getClaudeSessionRecord("deny", dir)));
  // An unknown or mistyped stored value reads as no override; the record still parses.
  fs.writeFileSync(index.claudeIndexPath(dir), JSON.stringify({ version: 1, sessions: [
    record("odd", { permissionMode: "maybe" }),
    record("num", { permissionMode: 1 }),
  ] }));
  const [odd, num] = index.readClaudeIndex(dir);
  assert.equal(odd.id, "odd");
  assert.ok(!("permissionMode" in odd));
  assert.ok(!("permissionMode" in num));
});
