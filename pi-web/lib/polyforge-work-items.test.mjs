// carries: lib/polyforge-work-items.ts, lib/polyforge-work-items-pure.ts, app/api/polyforge/work-items/route.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-work-items.ts");
const detect = await jiti.import("./polyforge-detect.ts");

// The real merged `pf --json queue` row shape (artifacts/real-queue-snapshot.json),
// with every dropped field carrying a sentinel.
const REAL_ROW = {
  id: "wi_7oKHIcJ3v7Ue", slug: "polyforge#264", seq: 264, project_id: "prj_SENTINEL", project: "polyforge",
  goal: "  chore: verbatim goal  ", content: "CONTENT_SENTINEL", parent_id: "wi_PARENTSENTINEL", status: "canceled",
  priority: "normal", milestone: "MILESTONE_SENTINEL", labels: ["LABEL_SENTINEL"], attrs: { k: "ATTRS_SENTINEL" },
  reporter: "REPORTER_SENTINEL", assignee: "ASSIGNEE_SENTINEL", waiting_on: "WAITING_ON_SENTINEL",
  waiting_for: [{ slug: "WAITING_FOR_SENTINEL", status: "queued" }], bundle_name: "chore",
  created_at: "2026-10-26T09:34:34.926975429Z", updated_at: "UPDATED_SENTINEL", closed_at: "CLOSED_SENTINEL",
  session_fingerprint: "SESSION_SENTINEL", session_machine: "MACHINE_SENTINEL", session_started_at: "STARTED_SENTINEL",
  clean_pause: false, repo_inventory: [{ name: "REPO_SENTINEL" }],
};
const LIST_KEYS = ["bundle_name", "created_at", "goal", "id", "priority", "project", "slug", "status"];
const DETAIL_KEYS = ["assignee", "bundle_name", "closed_at", "content", "content_len", "created_at", "goal", "id", "labels",
  "milestone", "priority", "project", "reporter", "slug", "status", "updated_at", "waiting_for", "waiting_on"];

test("the work-items floor is 2.19.0 and the boundary is inclusive", () => {
  assert.equal(lib.POLYFORGE_WORK_ITEMS_MINIMUM_PF_VERSION, "2.19.0");
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.18.9", lib.POLYFORGE_WORK_ITEMS_MINIMUM_PF_VERSION), true);
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.19.0", lib.POLYFORGE_WORK_ITEMS_MINIMUM_PF_VERSION), false);
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.13", lib.POLYFORGE_WORK_ITEMS_MINIMUM_PF_VERSION), false);
});

test("the list passport keeps exactly the eight keys verbatim and drops everything else", () => {
  const { items, total } = lib.sanitizeWorkItemListAnswer({ items: [REAL_ROW], total: 1 });
  assert.equal(total, 1);
  assert.deepEqual(items, [{
    id: "wi_7oKHIcJ3v7Ue", slug: "polyforge#264", project: "polyforge", status: "canceled", priority: "normal",
    bundle_name: "chore", goal: "  chore: verbatim goal  ", created_at: "2026-10-26T09:34:34.926975429Z",
  }]);
  assert.deepEqual(Object.keys(items[0]).sort(), LIST_KEYS);
  const serialized = JSON.stringify(items);
  for (const dropped of ["CONTENT_SENTINEL", "prj_SENTINEL", "wi_PARENTSENTINEL", "MILESTONE_SENTINEL", "LABEL_SENTINEL", "ATTRS_SENTINEL",
    "REPORTER_SENTINEL", "ASSIGNEE_SENTINEL", "WAITING_ON_SENTINEL", "WAITING_FOR_SENTINEL", "UPDATED_SENTINEL", "CLOSED_SENTINEL",
    "SESSION_SENTINEL", "MACHINE_SENTINEL", "STARTED_SENTINEL", "REPO_SENTINEL", "seq", "clean_pause", "session_", "waiting_"]) {
    assert.ok(!serialized.includes(dropped), `the list must not carry ${dropped}`);
  }
});

test("wrong types degrade to null, unusable id/slug rows are omitted, and the total falls back to the row count", () => {
  const degraded = lib.sanitizeWorkItemRow({ ...REAL_ROW, goal: 42, created_at: 1234, status: null, bundle_name: { bad: true }, project: ["x"] });
  assert.deepEqual(Object.keys(degraded).sort(), LIST_KEYS, "still exactly eight keys");
  assert.equal(degraded.goal, null, "a numeric goal is not coerced");
  assert.equal(degraded.created_at, null);
  assert.equal(degraded.status, null);
  assert.equal(degraded.bundle_name, null);
  assert.equal(degraded.project, null);
  for (const bad of [{ ...REAL_ROW, id: "not-a-work-item" }, { ...REAL_ROW, slug: 13 }, { ...REAL_ROW, slug: "" }, null, [], "x"]) {
    assert.equal(lib.sanitizeWorkItemRow(bad), null);
  }
  const answer = { items: [REAL_ROW, { ...REAL_ROW, id: "bad" }, { ...REAL_ROW, id: "wi_Second", slug: 7 }, { ...REAL_ROW, id: "wi_Third" }] };
  assert.deepEqual(lib.sanitizeWorkItemListAnswer(answer).total, 2, "absent total → sanitized row count");
  for (const total of [-1, 1.5, "9", null, Number.NaN]) {
    assert.equal(lib.sanitizeWorkItemListAnswer({ ...answer, total }).total, 2, `total ${total} is not a usable total`);
  }
  assert.equal(lib.sanitizeWorkItemListAnswer({ ...answer, total: 1162 }).total, 1162, "pf's total wins over the row count");
  assert.deepEqual(lib.sanitizeWorkItemListAnswer({ ...answer, total: 0 }).items.map((r) => r.id), ["wi_7oKHIcJ3v7Ue", "wi_Third"], "pf's order is kept");
  assert.deepEqual(lib.sanitizeWorkItemListAnswer(null), { items: [], total: 0 });
  assert.deepEqual(lib.sanitizeWorkItemListAnswer({ items: "x" }), { items: [], total: 0 });
});

// pf queue's merged document marks a queued execution ticket
// without acceptance with `acceptance_missing: true`; the list passport
// forwards that one conditional key, and only for a real boolean true.
test("the list passport forwards acceptance_missing only when pf sends true", () => {
  const marked = lib.sanitizeWorkItemRow({ ...REAL_ROW, acceptance_missing: true });
  assert.equal(marked.acceptance_missing, true, "pf's true is forwarded");
  assert.deepEqual(Object.keys(marked).sort(), [...LIST_KEYS, "acceptance_missing"].sort(), "the ninth key is the only addition");
  assert.equal(lib.sanitizeWorkItemListAnswer({ items: [{ ...REAL_ROW, acceptance_missing: true }], total: 1 }).items[0].acceptance_missing, true,
    "the merged answer keeps the mark");
  for (const value of [false, "true", 1, null, undefined]) {
    const row = lib.sanitizeWorkItemRow({ ...REAL_ROW, acceptance_missing: value });
    assert.ok(!Object.hasOwn(row, "acceptance_missing"), `acceptance_missing ${JSON.stringify(value)} is omitted`);
    assert.deepEqual(Object.keys(row).sort(), LIST_KEYS, "still exactly eight keys");
  }
  assert.ok(!Object.hasOwn(lib.sanitizeWorkItemRow(REAL_ROW), "acceptance_missing"), "an absent key stays absent");
});

test("the detail passport keeps exactly the eighteen keys, with pf's waiting facts", () => {
  const detail = lib.sanitizeWorkItemDetail({ ...REAL_ROW, labels: ["pi-web", 3], milestone: null, waiting_on: null, content: "# body" });
  assert.deepEqual(Object.keys(detail).sort(), DETAIL_KEYS);
  assert.deepEqual(detail.labels, ["pi-web"], "non-string labels are dropped");
  assert.deepEqual(detail.waiting_for, [{ slug: "WAITING_FOR_SENTINEL", status: "queued" }]);
  assert.equal(detail.waiting_on, null);
  assert.equal(detail.content, "# body");
  assert.equal(detail.content_len, 6);
  assert.equal(detail.reporter, "REPORTER_SENTINEL");
  const serialized = JSON.stringify(detail);
  for (const dropped of ["prj_SENTINEL", "wi_PARENTSENTINEL", "ATTRS_SENTINEL", "SESSION_SENTINEL", "MACHINE_SENTINEL", "STARTED_SENTINEL",
    "REPO_SENTINEL", "\"seq\"", "clean_pause", "repo_inventory", "attrs"]) {
    assert.ok(!serialized.includes(dropped), `the detail must not carry ${dropped}`);
  }
  const bare = lib.sanitizeWorkItemDetail({ id: "wi_Bare", slug: "pi#1", waiting_for: [{ slug: 3, status: "queued", extra: "X" }, "junk"] });
  assert.deepEqual(Object.keys(bare).sort(), DETAIL_KEYS, "absent fields are null, never missing keys");
  assert.deepEqual(bare.labels, []);
  assert.deepEqual(bare.waiting_for, [{ slug: null, status: "queued" }], "a blocker keeps only slug/status");
  assert.equal(bare.content, null, "absent content is null");
  assert.equal(bare.content_len, 0);
  assert.equal(lib.sanitizeWorkItemDetail({ ...REAL_ROW, waiting_for: undefined }).waiting_for, null);
  assert.equal(lib.sanitizeWorkItemDetail({ id: "bad", slug: "x" }), null);
  assert.equal(lib.sanitizeWorkItemDetail(null), null);
});

test("the content is cut at 262144 UTF-8 bytes on a code-point boundary with content_len uncut", () => {
  const content = "é".repeat(140_000) + "TAIL";
  const detail = lib.sanitizeWorkItemDetail({ ...REAL_ROW, content });
  const uncut = Buffer.byteLength(content, "utf8");
  assert.equal(detail.content_len, uncut);
  assert.equal(Buffer.byteLength(detail.content, "utf8"), 262144);
  assert.ok(!detail.content.includes("TAIL") && !detail.content.includes("�"));
  const odd = "x" + "😀".repeat(70_000);
  const cut = lib.sanitizeWorkItemDetail({ ...REAL_ROW, content: odd }).content;
  assert.ok(Buffer.byteLength(cut, "utf8") <= 262144);
  assert.equal(Buffer.from(cut, "utf8").toString("utf8"), cut, "never a split surrogate pair");
});

test("spawn-error reasons are bounded pf stderr or fixed text, never env or argv", () => {
  const withStderr = Object.assign(new Error("Command failed: pf --json queue SECRET"), { stderr: `queue: project orbit: boom\n${"x".repeat(2000)}` });
  const reason = lib.PolyforgeWorkItemsError.fromSpawnError(withStderr).message;
  assert.match(reason, /^queue: project orbit: boom/);
  assert.ok(reason.length <= 512);
  assert.ok(!reason.includes("SECRET"));
  assert.match(lib.PolyforgeWorkItemsError.fromSpawnError(Object.assign(new Error("x"), { code: "ERR_CHILD_PROCESS_STDIO_MAXBUFFER", stderr: "" })).message, /buffer/);
  assert.match(lib.PolyforgeWorkItemsError.fromSpawnError(Object.assign(new Error("x"), { killed: true, signal: "SIGTERM", stderr: "" })).message, /within 30s/);
  assert.equal(lib.PolyforgeWorkItemsError.fromSpawnError(Object.assign(new Error("spawn pf ENOENT"), { code: "ENOENT" })).message, "polyforge is unavailable");
  assert.equal(lib.PolyforgeWorkItemsError.fromSpawnError("weird").message, "polyforge is unavailable");
});

test("the list and detail reads spawn exactly the pinned argv through PF_BIN and degrade with typed reasons", async (t) => {
  const work = mkdtempSync(join(tmpdir(), "pf-work-items-"));
  const previous = process.env.PF_BIN;
  t.after(() => {
    if (previous === undefined) delete process.env.PF_BIN; else process.env.PF_BIN = previous;
    rmSync(work, { force: true, recursive: true });
  });
  const log = join(work, "argv.log");
  const mode = join(work, "mode");
  const pf = join(work, "pf");
  writeFileSync(pf, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args) + "\\n");
const mode = fs.readFileSync(${JSON.stringify(mode)}, "utf8");
if (mode === "fail") { process.stderr.write("queue: project orbit: refused\\n"); process.exit(1); }
if (mode === "malformed") { process.stdout.write("not json\\n"); process.exit(0); }
if (args[1] === "queue") process.stdout.write(JSON.stringify({ items: [${JSON.stringify(REAL_ROW)}], total: 1 }));
else process.stdout.write(JSON.stringify(${JSON.stringify({ ...REAL_ROW, content: "body" })}));
`);
  chmodSync(pf, 0o755);
  process.env.PF_BIN = pf;
  const argv = () => readFileSync(log, "utf8").trim().split("\n").map((line) => JSON.parse(line));

  writeFileSync(mode, "ok"); writeFileSync(log, "");
  const list = await lib.listPolyforgeWorkItems();
  assert.equal(list.total, 1);
  assert.equal(list.items[0].slug, "polyforge#264");
  const detail = await lib.showPolyforgeWorkItem("wi_7oKHIcJ3v7Ue");
  assert.equal(detail.content, "body");
  assert.deepEqual(argv(), [["--json", "queue"], ["--json", "file", "show", "wi_7oKHIcJ3v7Ue"]],
    "one merged queue (no --project/--status/--limit) and one file show");

  writeFileSync(mode, "fail");
  await assert.rejects(lib.listPolyforgeWorkItems(), (error) => error instanceof lib.PolyforgeWorkItemsError && /queue: project orbit: refused/.test(error.message));
  writeFileSync(mode, "malformed");
  await assert.rejects(lib.listPolyforgeWorkItems(), (error) => error instanceof lib.PolyforgeWorkItemsError && /undecodable/.test(error.message));
  await assert.rejects(lib.showPolyforgeWorkItem("wi_7oKHIcJ3v7Ue"), /undecodable/);
});

test("the modules never log, read env, or name a write verb", async () => {
  for (const path of ["./polyforge-work-items.ts", "./polyforge-work-items-pure.ts"]) {
    const source = await readFile(new URL(path, import.meta.url), "utf8");
    assert.doesNotMatch(source, /console\.(log|error|warn)/, `${path} never logs`);
    assert.doesNotMatch(source, /process\.env/, `${path} never reads an environment value`);
    assert.doesNotMatch(source, /"(cancel|edit|claim|create|close|file", "(new|edit|close))"/, `${path} never names a write verb`);
    assert.doesNotMatch(source, /"--(project|status|limit)"/, `${path} never narrows the merged read`);
    assert.doesNotMatch(source, /fetch\(|https?:\/\//, `${path} makes no direct aihub HTTP call`);
  }
});
