import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

const { searchFileText } = await import(new URL("../lib/file-search.ts", import.meta.url));

test("file search reports line numbers and snippets from an offset", async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), "pf-file-search-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const file = path.join(dir, "sample.txt");
  await writeFile(file, "alpha needle one\nbeta\ngamma needle two\ndelta\nneedle three\n", "utf8");

  const all = await searchFileText(file, "needle", {});
  assert.equal(all.matches.length, 3);
  assert.equal(all.matches[0].line, 1);
  assert.equal(all.matches[1].line, 3);
  assert.equal(all.matches[2].line, 5);
  assert.match(all.matches[0].snippet, /alpha needle one/);
  assert.equal(all.truncated, false);

  // Offset past the first two lines: line numbers stay whole-file.
  const later = await searchFileText(file, "needle", { offset: Buffer.byteLength("alpha needle one\nbeta\n", "utf8"), startLine: 3 });
  assert.equal(later.matches.length, 2);
  assert.equal(later.matches[0].line, 3);
  assert.equal(later.matches[1].line, 5);
});

test("case and regex toggles behave like the chat matcher", async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), "pf-file-search-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const file = path.join(dir, "case.txt");
  await writeFile(file, "Error 1\nerror 2\nERROR 3\n", "utf8");

  assert.equal((await searchFileText(file, "error", {})).matches.length, 3);
  assert.equal((await searchFileText(file, "error", { caseSensitive: true })).matches.length, 1);
  assert.equal((await searchFileText(file, "^error", { regex: true, caseSensitive: true })).matches.length, 1);
});

test("over-long lines are skipped without breaking the scan", async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), "pf-file-search-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const file = path.join(dir, "long.txt");
  const longLine = "x".repeat(64);
  await writeFile(file, `${"y".repeat(1024 * 1024 + 10)}\n${longLine} needle\n`, "utf8");
  const response = await searchFileText(file, "needle", {});
  assert.equal(response.matches.length, 1);
  assert.equal(response.matches[0].line, 2);
});

test("empty and oversized queries are refused", async () => {
  const response = await searchFileText("/nonexistent", "", {});
  assert.deepEqual(response, { matches: [], truncated: false });
  await assert.rejects(
    () => searchFileText("/nonexistent", "x".repeat(201), {}),
    /exceeds 200 characters/,
  );
});
