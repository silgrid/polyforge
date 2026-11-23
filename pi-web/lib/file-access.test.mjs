import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

// Loaded through jiti so the module's own extensionless imports resolve the way
// the app resolves them (tsconfig moduleResolution: "bundler"); bare
// `import("./path-security.ts")` only works while that file has no imports.
async function loadSubject() {
  const { createJiti } = await import("jiti");
  return createJiti(import.meta.url).import("./path-security.ts");
}

test("rejects an existing path that escapes an allowed root through a symlink", async (t) => {
  const { isExistingPathWithinRoots, isPathWithinRoots } = await loadSubject();
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-file-access-"));
  t.after(() => fs.rmSync(base, { recursive: true, force: true }));
  const allowed = path.join(base, "allowed");
  const outside = path.join(base, "outside");
  fs.mkdirSync(allowed);
  fs.mkdirSync(outside);
  fs.writeFileSync(path.join(outside, "secret.txt"), "secret");
  const link = path.join(allowed, "link");
  fs.symlinkSync(outside, link, process.platform === "win32" ? "junction" : "dir");
  const target = path.join(link, "secret.txt");
  const roots = new Set([allowed]);

  assert.equal(isPathWithinRoots(target, roots), true);
  assert.equal(isExistingPathWithinRoots(target, roots), false);
});

test("rejects a `..` that climbs out beside a link's target", async (t) => {
  const { hasParentDirectorySegment, isExistingPathWithinRoots } = await loadSubject();
  const base = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-file-access-")));
  t.after(() => fs.rmSync(base, { recursive: true, force: true }));
  const allowed = path.join(base, "allowed");
  const outside = path.join(base, "outside");
  fs.mkdirSync(allowed);
  fs.mkdirSync(path.join(outside, "linked"), { recursive: true });
  fs.symlinkSync(path.join(outside, "linked"), path.join(allowed, "link"), process.platform === "win32" ? "junction" : "dir");
  const roots = new Set([allowed]);

  // realpathSync reads `allowed/link/..` as `allowed`; opening it lists `outside`.
  const climb = `${allowed}${path.sep}link${path.sep}..`;
  assert.equal(isExistingPathWithinRoots(climb, roots), false);
  assert.equal(isExistingPathWithinRoots(path.join(allowed, "link"), new Set([allowed, outside])), true);

  assert.equal(hasParentDirectorySegment("/a/b/../c"), true);
  assert.equal(hasParentDirectorySegment("C:\\a\\..\\c"), true);
  assert.equal(hasParentDirectorySegment("/a/..b/c.."), false);
  if (process.platform !== "win32") assert.equal(hasParentDirectorySegment("/a/b\\..\\c"), false);
});

// ── the allowed-roots cache (polyforge#252) ──────────────────────────────
// Behavioural coverage of the 5 s cache's invalidation contract: pi-web's own
// delete drops it at once, a scan that raced the delete never writes the
// pre-delete roots back, and allowFileRoot()'s grants (a just-created session's
// cwd) survive. `./session-reader` is replaced by a stub whose scan the test
// holds, so the delete/rebuild interleaving is deterministic, not timing-based.
const cacheStubPath = fileURLToPath(new URL("./__tests__/session-reader-stub.mjs", import.meta.url));
const cacheJiti = (await import("jiti")).createJiti(import.meta.url, {
  alias: { "./session-reader": cacheStubPath },
});
const { allowFileRoot, getAllowedFileRoots, invalidateAllowedFileRoots } = await cacheJiti.import("./file-access.ts");
const cacheReader = await cacheJiti.import(cacheStubPath);

function resetAllowedRootsCache(sessions = []) {
  globalThis.__piAllowedRootsCache = undefined;
  globalThis.__piAdditionalAllowedRoots = new Set();
  cacheReader.setSessions(sessions);
}

test("a scan that raced a delete is not written back", async () => {
  resetAllowedRootsCache([{ cwd: "/work/deleted-project" }]);
  const release = cacheReader.holdNextScan();
  const inFlight = getAllowedFileRoots();
  // The delete lands while the scan waits on its session read.
  invalidateAllowedFileRoots();
  cacheReader.setSessions([]);
  release();
  const raced = await inFlight;
  assert.equal(raced.has("/work/deleted-project"), true, "the in-flight read may still see its pre-delete scan");

  const after = await getAllowedFileRoots();
  assert.equal(after.has("/work/deleted-project"), false,
    "the pre-delete scan must not be written back after the delete invalidated the cache");
});

test("a just-created session's cwd joins a warm cache at once and survives a delete", async () => {
  // AC2's regression is the warm path: a cache built less than 5 s ago must
  // take a new session's cwd without re-scanning (lib/allowed-roots.ts writes
  // it into the current cache), so the session is usable immediately instead
  // of after the TTL.
  resetAllowedRootsCache([{ cwd: "/work/existing-project" }]);
  const warm = await getAllowedFileRoots();
  assert.equal(warm.has("/work/existing-project"), true, "the warm cache was built");
  const scansBefore = cacheReader.state.calls;

  allowFileRoot("/work/new-session");
  const hit = await getAllowedFileRoots();
  assert.equal(cacheReader.state.calls, scansBefore,
    "the warm cache answered without a re-scan");
  assert.equal(hit.has("/work/new-session"), true,
    "allowFileRoot() makes a new session's cwd usable without waiting for the TTL");

  // A delete invalidates the cache but never withdraws allowFileRoot()'s grant.
  invalidateAllowedFileRoots();
  const afterDelete = await getAllowedFileRoots();
  assert.equal(afterDelete.has("/work/new-session"), true,
    "a delete must not withdraw allowFileRoot()'s grant before the process restarts");
  assert.equal(afterDelete.has("/work/existing-project"), true, "the surviving session's cwd stays");
});
