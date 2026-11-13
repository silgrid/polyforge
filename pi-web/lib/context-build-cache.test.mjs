import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, writeFile, utimes, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

const { cachedSessionBuild, clearSessionBuildCache } = await import(new URL("../lib/context-build-cache.ts", import.meta.url));

test("memoizes the build per file + params and re-serves it", async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), "pf-build-cache-"));
  t.after(() => { clearSessionBuildCache(); return rm(dir, { recursive: true, force: true }); });
  const file = path.join(dir, "a.jsonl");
  await writeFile(file, "x", "utf8");

  let builds = 0;
  const build = () => { builds += 1; return { n: builds }; };
  const first = cachedSessionBuild(file, { tail: 50 }, build);
  const second = cachedSessionBuild(file, { tail: 50 }, build);
  assert.equal(builds, 1);
  assert.equal(second, first);
  // Different params = different key = rebuild.
  cachedSessionBuild(file, { tail: 200 }, build);
  assert.equal(builds, 2);
});

test("an appended file (new fingerprint) invalidates the cached build", async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), "pf-build-cache-"));
  t.after(() => { clearSessionBuildCache(); return rm(dir, { recursive: true, force: true }); });
  const file = path.join(dir, "b.jsonl");
  await writeFile(file, "v1", "utf8");
  let builds = 0;
  const build = () => ({ n: ++builds });
  cachedSessionBuild(file, {}, build);
  // Force a mtime change with the same content size.
  const later = new Date(Date.now() + 5000);
  await utimes(file, later, later);
  cachedSessionBuild(file, {}, build);
  assert.equal(builds, 2);
});

test("live sessions (empty path) and unreadable files bypass the cache", async () => {
  clearSessionBuildCache();
  let builds = 0;
  const build = () => ({ n: ++builds });
  assert.equal(cachedSessionBuild("", {}, build).n, 1);
  assert.equal(cachedSessionBuild("", {}, build).n, 2);
  assert.equal(cachedSessionBuild("/nonexistent/x.jsonl", {}, build).n, 3);
  assert.equal(cachedSessionBuild("/nonexistent/x.jsonl", {}, build).n, 4);
  assert.equal(builds, 4);
});

test("LRU bound: at most 8 entries are retained", async (t) => {
  clearSessionBuildCache();
  const dir = await mkdtemp(path.join(tmpdir(), "pf-build-cache-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  let builds = 0;
  const build = () => ({ n: ++builds });
  const files = [];
  for (let i = 0; i < 10; i++) {
    const f = path.join(dir, `s${i}.jsonl`);
    await writeFile(f, String(i), "utf8");
    files.push(f);
  }
  for (const f of files) cachedSessionBuild(f, {}, build);
  assert.equal(builds, 10);
  // The first two were evicted; re-requesting rebuilds them.
  cachedSessionBuild(files[0], {}, build);
  cachedSessionBuild(files[1], {}, build);
  assert.equal(builds, 12);
  // The last two are still cached.
  cachedSessionBuild(files[8], {}, build);
  cachedSessionBuild(files[9], {}, build);
  assert.equal(builds, 12);
});
