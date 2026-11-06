import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-memory.ts");
const pure = await jiti.import("./polyforge-memory-pure.ts");

test("the Memory floor is 2.20.12 (browse-mode recall) and the boundary is inclusive", async () => {
  assert.equal(lib.POLYFORGE_MEMORY_MINIMUM_PF_VERSION, "2.20.12");
  const detect = await jiti.import("./polyforge-detect.ts");
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.11", lib.POLYFORGE_MEMORY_MINIMUM_PF_VERSION), true, "2.20.11 is below the floor");
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.12", lib.POLYFORGE_MEMORY_MINIMUM_PF_VERSION), false, "2.20.12 is the floor");
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.21.0", lib.POLYFORGE_MEMORY_MINIMUM_PF_VERSION), false);
  assert.equal(lib.MEMORY_BODY_FORWARD_CAP, 262144, "the forwarded body is capped at pf's own human-mode cap");
});

test("the hit passport keeps exactly the allowed keys and drops owner/project/sha/excerpt/hint and their values", () => {
  const raw = {
    id: "mem_abc123", project_id: "prj_SENTINEL", owner_user_id: "u_SENTINEL",
    work_item_id: "wi_1", type: "doc.decision", title: "t", visibility: "private",
    content: "SHOULD-NOT-PASS", content_sha: "sha_SENTINEL", content_excerpt: "SHOULD-NOT-PASS",
    internal_hint: "HINT_SENTINEL", tags: ["audit", "review", "", 7], related: ["mem_x", "not-an-id"],
    status: "active", created_at: "2026-10-20T00:00:00Z", updated_at: "2026-10-26T00:00:00Z", content_len: 79,
  };
  const hit = lib.sanitizeMemoryHit(raw);
  assert.deepEqual(Object.keys(hit).sort(), [
    "content_len", "created_at", "id", "related", "status", "tags", "title", "type", "updated_at", "visibility", "work_item_id",
  ]);
  assert.deepEqual(hit.tags, ["audit", "review"], "tags stay trimmed strings");
  assert.deepEqual(hit.related, ["mem_x", "not-an-id"], "related ids pass as strings");
  const serialized = JSON.stringify(lib.sanitizeMemorySearchAnswer({ items: [raw], total: 1 }));
  for (const dropped of ["owner_user_id", "project_id", "content_sha", "content_excerpt", "internal_hint",
    "u_SENTINEL", "prj_SENTINEL", "sha_SENTINEL", "HINT_SENTINEL", "SHOULD-NOT-PASS"]) {
    assert.ok(!serialized.includes(dropped), `the search answer must not carry ${dropped}`);
  }
  assert.equal(lib.sanitizeMemoryHit({ id: "not_mem", title: "x" }), null, "an invalid id drops the row");
  assert.deepEqual(lib.sanitizeMemorySearchAnswer({}), [], "a malformed answer reads as no hits");
  assert.deepEqual(lib.sanitizeMemorySearchAnswer(null), []);
});

test("the detail passport adds only content, capped at the forward cap on a code-point boundary", () => {
  const base = { id: "mem_abc123", type: "t", title: "t", visibility: "project", status: "active",
    tags: [], related: [], work_item_id: null, created_at: "c", updated_at: "u", content_len: 999999 };
  const detail = lib.sanitizeMemoryDetail({ ...base, content: "short", owner_user_id: "u_SENTINEL" });
  assert.deepEqual(Object.keys(detail).sort(), ["content", "content_len", "created_at", "id", "related", "status", "tags", "title", "type", "updated_at", "visibility", "work_item_id"]);
  assert.equal(detail.content, "short");
  // A multibyte char that would straddle the cap is dropped whole, never split.
  const exact = "a".repeat(262144);
  assert.equal(lib.sanitizeMemoryDetail({ ...base, content: exact }).content.length, 262144, "a body at the cap passes whole");
  const straddling = exact.slice(0, 262143) + "é" + "b".repeat(10);
  const capped = lib.sanitizeMemoryDetail({ ...base, content: straddling }).content;
  assert.ok(Buffer.byteLength(capped, "utf8") <= 262144, "never over the cap");
  for (const char of capped) assert.ok(Buffer.byteLength(char, "utf8") === 1 || char !== "�", "no split code point");
  assert.ok(!capped.includes("é"), "the straddling char is dropped whole, not split");
  assert.equal(lib.sanitizeMemoryDetail({ id: "bad" }), null, "an invalid id reads as no memory");
});

test("validators refuse a flag keyword, a bad type, bad tags and a bad project; accept the six types", () => {
  const params = { cwd: "/ws", q: "memory panel", project: "workbench", type: "*" };
  assert.equal(lib.validateMemorySearch(params).ok, true);
  for (const type of ["*", "doc.*", "experience.*", "fact.*", "methodology.*", "rule.*"]) {
    const result = lib.validateMemorySearch({ ...params, type });
    assert.ok(result.ok, `${type} is one of the six`);
  }
  for (const [label, bad] of [
    ["flag keyword", { ...params, q: "--limit 1" }],
    ["empty keyword", { ...params, q: "  " }],
    ["control bytes", { ...params, q: "a\nb" }],
    ["unknown type", { ...params, type: "bogus.*" }],
    ["bad tags", { ...params, tags: "a b" }],
    ["trailing comma", { ...params, tags: "a," }],
    ["off-shape project", { ...params, project: "../../etc/passwd" }],
    ["bad cwd", { ...params, cwd: "" }],
    ["control-byte cwd", { ...params, cwd: "/ws\n" }],
  ]) {
    const result = lib.validateMemorySearch(bad);
    assert.ok(!result.ok && result.error, `${label} is refused with a field-level message`);
  }
  assert.ok(lib.validateMemorySearch({ ...params, tags: "audit,review", any: "1" }).ok, "a well-shaped filtered search passes");
  const optional = lib.validateMemorySearch({ ...params, tags: "", any: "0" });
  assert.ok(optional.ok && optional.value.tags === undefined && optional.value.any === undefined, "empty tags and a non-1 any stay absent");
  assert.equal(pure.isPolyforgeMemoryId("mem_abc123"), true);
  assert.equal(pure.isPolyforgeMemoryId("mem_   "), false, "mem_ + whitespace is not an id");
  assert.equal(pure.isPolyforgeMemoryId("../../etc/passwd"), false);
  assert.equal(pure.isMemoryTagList("audit,review"), true);
  assert.equal(pure.isMemoryTagList(""), false);
});

test("hits re-sort newest-first against relevance order and page at 20", () => {
  const hit = (id, updated) => ({ id, type: "doc.note", title: id, visibility: "project", status: "active",
    tags: [], related: [], work_item_id: null, created_at: "2026-10-01T00:00:00Z", updated_at: updated, content_len: 1 });
  // Relevance order C, A, B; updated_at order A < B < C — display reads C, B, A.
  const sorted = pure.sortMemoryHitsNewestFirst([
    hit("mem_C", "2026-10-03T00:00:00Z"), hit("mem_A", "2026-10-01T00:00:00Z"), hit("mem_B", "2026-10-02T00:00:00Z"),
  ]);
  assert.deepEqual(sorted.map((row) => row.id), ["mem_C", "mem_B", "mem_A"]);
  const twentyOne = Array.from({ length: 21 }, (_, i) => hit(`mem_${String(i).padStart(2, "0")}`, `2026-10-01T00:00:${String(20 - i).padStart(2, "0")}Z`));
  const page1 = pure.memoryPage(twentyOne, 1);
  const page2 = pure.memoryPage(twentyOne, 2);
  assert.equal(page1.length, 20, "the first page holds 20 rows");
  assert.deepEqual(page2.map((row) => row.id), ["mem_20"], "the 21st hit is the whole second page");
  assert.deepEqual(pure.memoryPage(twentyOne, 3), [], "no page past the hits");
  assert.deepEqual(pure.memoryPage(twentyOne, 0), [], "an out-of-range page reads empty");
  assert.equal(pure.MEMORY_PAGE_SIZE, 20);
  assert.equal(pure.MEMORY_RESULT_LIMIT, 200);
  assert.equal(pure.MEMORY_COLLAPSE_BYTES, 2000);
});

test("truncation is detected from the body length vs content_len, never guessed", () => {
  assert.equal(pure.isMemoryBodyTruncated("abc", 3), false);
  assert.equal(pure.isMemoryBodyTruncated("abc", 9000), true, "a body shorter than content_len is truncated");
  assert.equal(pure.isMemoryBodyTruncated("é", 2), false, "the byte length counts, not the char length");
  assert.equal(pure.isMemoryBodyTruncated("é", 3), true);
  assert.equal(pure.isMemoryBodyTruncated("abc", 0), false, "a malformed content_len reads whole");
  assert.equal(pure.isMemoryBodyTruncated("abc", Number.NaN), false);
});

test("the recall argv is exactly one spawn: flags opt-in, the keyword one positional, 200 the limit", () => {
  const base = { cwd: "/ws", q: "memory panel", project: "workbench", type: "*" };
  const plain = lib.buildRecallArgv(lib.validateMemorySearch(base).value);
  assert.deepEqual(plain, ["--json", "recall", "--project", "workbench", "--limit", "200", "memory panel"]);
  assert.ok(!plain.includes("--any") && !plain.includes("--tags") && !plain.includes("--type"));
  const filtered = lib.buildRecallArgv(lib.validateMemorySearch({ ...base, tags: "a,b", type: "doc.*", any: "1" }).value);
  const argAfter = (argv, name) => argv[argv.indexOf(name) + 1];
  assert.equal(argAfter(filtered, "--tags"), "a,b", "tags travel as one argument");
  assert.equal(argAfter(filtered, "--type"), "doc.*");
  assert.ok(filtered.includes("--any"));
  assert.equal(filtered.at(-1), "memory panel", "the keyword is the one positional, last");
  assert.equal(filtered.filter((token) => token === "--json").length, 1);
});

test("the module never logs a body, a credential or an env value", async () => {
  for (const path of ["./polyforge-memory.ts", "./polyforge-memory-pure.ts"]) {
    const source = await readFile(new URL(`${path}`, import.meta.url), "utf8");
    assert.doesNotMatch(source, /console\.(log|error|warn)/, `${path} never logs`);
    assert.doesNotMatch(source, /process\.env/, `${path} never reads an environment value`);
  }
});

test("the browse argv is one no-query pf recall: 20 rows, updated, cursor and filters opt-in", () => {
  const base = { cwd: "/ws", project: "workbench", type: "*" };
  const first = lib.validateMemoryBrowse(base);
  assert.ok(first.ok);
  assert.deepEqual(lib.buildBrowseRecallArgv(first.value),
    ["--json", "recall", "--project", "workbench", "--limit", "20", "--sort", "updated"]);
  const next = lib.validateMemoryBrowse({ ...base, type: "doc.*", tags: "audit", after: "cur20" });
  assert.ok(next.ok);
  const argv = lib.buildBrowseRecallArgv(next.value);
  const argAfter = (name) => argv[argv.indexOf(name) + 1];
  assert.equal(argAfter("--after"), "cur20", "the cursor travels unchanged as one argument");
  assert.equal(argAfter("--type"), "doc.*");
  assert.equal(argAfter("--tags"), "audit");
  const pairs = argv.slice(2);
  for (let i = 0; i < pairs.length; i += 2) assert.ok(pairs[i].startsWith("--"), `no positional keyword: ${JSON.stringify(argv)}`);
  assert.equal(pairs.length % 2, 0, "every token after the verb is a flag/value pair");
  assert.ok(lib.validateMemoryBrowse({ cwd: "/ws", project: "workbench" }).ok, "an absent type browses every type");
});

test("the browse validator refuses a flag-shaped, control-byte, empty or oversized cursor before any spawn", () => {
  const base = { cwd: "/ws", project: "workbench", type: "*" };
  for (const [label, after] of [["flag", "--limit"], ["control", "cur\n1"], ["empty", ""], ["oversized", "c".repeat(1025)]]) {
    const result = lib.validateMemoryBrowse({ ...base, after });
    assert.ok(!result.ok && result.error, `${label} cursor is refused`);
  }
  assert.ok(lib.validateMemoryBrowse({ ...base, after: "c".repeat(1024) }).ok, "a 1024-byte cursor passes");
  for (const [label, bad] of [["project", { ...base, project: "../x" }], ["type", { ...base, type: "bogus" }],
    ["tags", { ...base, tags: "a b" }], ["cwd", { ...base, cwd: "" }]]) {
    assert.ok(!lib.validateMemoryBrowse(bad).ok, `a bad ${label} is refused`);
  }
});

test("the browse answer keeps the hit passport, a finite total and only a valid next_cursor", () => {
  const row = { id: "mem_b1", owner_user_id: "u_SENTINEL", project_id: "prj_SENTINEL", content_excerpt: "SHOULD-NOT-PASS",
    type: "doc.note", title: "t", updated_at: "2026-10-27T00:00:00Z" };
  const page = lib.sanitizeMemoryBrowseAnswer({ items: [row], total: 29, next_cursor: "cur20", extra: "HINT_SENTINEL" });
  assert.deepEqual(Object.keys(page).sort(), ["items", "next_cursor", "total"]);
  assert.equal(page.total, 29);
  assert.equal(page.next_cursor, "cur20");
  const serialized = JSON.stringify(page);
  for (const dropped of ["u_SENTINEL", "prj_SENTINEL", "SHOULD-NOT-PASS", "HINT_SENTINEL"]) {
    assert.ok(!serialized.includes(dropped), `the browse page must not carry ${dropped}`);
  }
  assert.equal(lib.sanitizeMemoryBrowseAnswer({ items: [row], total: -3 }).total, 1, "a negative total falls back to the page length");
  assert.equal(lib.sanitizeMemoryBrowseAnswer({ items: [row], total: "29" }).total, 1, "a non-number total falls back to the page length");
  assert.equal(lib.sanitizeMemoryBrowseAnswer({ items: [row], next_cursor: "--evil" }).next_cursor, undefined, "a flag-shaped cursor never comes back");
  assert.equal(lib.sanitizeMemoryBrowseAnswer({ items: [row] }).next_cursor, undefined, "the last page carries no cursor");
  assert.deepEqual(lib.sanitizeMemoryBrowseAnswer(null), { items: [], total: 0 });
});

test("load more appends the next page in order and skips ids already shown", () => {
  const hit = (id) => ({ id, type: "doc.note", title: id, visibility: "project", status: "active", tags: [], related: [],
    work_item_id: null, created_at: "c", updated_at: "u", content_len: 1 });
  const merged = pure.mergeMemoryPages([hit("mem_1"), hit("mem_2")], [hit("mem_2"), hit("mem_3"), hit("mem_4")]);
  assert.deepEqual(merged.map((row) => row.id), ["mem_1", "mem_2", "mem_3", "mem_4"]);
  assert.deepEqual(pure.mergeMemoryPages([], [hit("mem_1")]).map((row) => row.id), ["mem_1"]);
  assert.equal(pure.MEMORY_BROWSE_LIMIT, 20);
});

test("the shared UTF-8 cap cuts on a code-point boundary and passes a text at the cap whole", () => {
  assert.equal(pure.capUtf8Bytes("abc", 3), "abc");
  assert.equal(pure.capUtf8Bytes("abé", 3), "ab", "a straddling two-byte char is dropped whole");
  assert.equal(pure.capUtf8Bytes("ab字", 4), "ab");
  assert.equal(pure.capUtf8Bytes("ab字", 5), "ab字");
  assert.equal(pure.capUtf8Bytes("😀x", 3), "", "a four-byte char is never split");
});
