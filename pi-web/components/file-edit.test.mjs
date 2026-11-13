import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, writeFile, rm, readFile } from "node:fs/promises";
import { statSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

const { saveTextFileSafely, contentLooksBinary, EDIT_MAX_BYTES } = await import(new URL("../lib/file-edit.ts", import.meta.url));

test("saves atomically and returns the new mtime", async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), "pf-file-edit-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const file = path.join(dir, "a.txt");
  await writeFile(file, "original\n", "utf8");
  const expected = statSync(file).mtimeMs;
  assert.ok(Number.isFinite(expected));

  const result = saveTextFileSafely(file, "edited\n", expected);
  assert.equal(result.status, "saved");
  assert.ok((result).mtimeMs > 0);
  assert.equal(await readFile(file, "utf8"), "edited\n");
  // No temp litter left behind.
  const leftovers = readdirSync(dir).filter((name) => name.includes(".pf-edit-"));
  assert.equal(leftovers.length, 0);
});

test("refuses the save when the file changed on disk (lost-update guard)", async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), "pf-file-edit-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const file = path.join(dir, "b.txt");
  await writeFile(file, "v1", "utf8");
  const stale = statSync(file).mtimeMs;
  await writeFile(file, "v2-on-disk", "utf8"); // external write moves the mtime

  const result = saveTextFileSafely(file, "v2-client", stale);
  assert.equal(result.status, "conflict");
  assert.ok((result).currentMtimeMs > stale);
  assert.equal(await readFile(file, "utf8"), "v2-on-disk");
});

test("enforces the 512KB ceiling and the binary sniff", async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), "pf-file-edit-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const file = path.join(dir, "c.txt");
  await writeFile(file, "x", "utf8");
  const mtime = statSync(file).mtimeMs;

  assert.equal(saveTextFileSafely(file, "y".repeat(EDIT_MAX_BYTES + 1), mtime).status, "too-large");
  assert.equal(saveTextFileSafely(file, "text\u0000with nul", mtime).status, "binary");
  assert.equal(contentLooksBinary("plain text"), false);
  assert.equal(contentLooksBinary("has \u0000 nul"), true);
  assert.equal(EDIT_MAX_BYTES, 512 * 1024);
});

test("missing files answer not-found without touching the disk", () => {
  assert.equal(saveTextFileSafely("/nonexistent/dir/file.txt", "x", 0).status, "not-found");
});
