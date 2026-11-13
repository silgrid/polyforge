import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { lstatSync, readFileSync, realpathSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const {
  FS_MANAGE_REFUSAL_REASONS,
  runFsManageGuards,
  validateFsManageBody,
  classifyFsError,
  computeProtectedSites,
  isMutationSiteAllowed,
  isProtectedSite,
  sessionCwdBlocksEntry,
  __setFsManageSessionCwdSourceForTesting: setCwdSource,
} = await jiti.import("./fs-manage-guards.ts");
const { getAdditionalAllowedRoots } = await jiti.import("./allowed-roots.ts");
const { __setRegistrationScopesForTesting: setScopes } = await jiti.import("./root-registration-policy.ts");

// ---------------------------------------------------------------------------
// Fixtures are CONTROLLED (same discipline as root-registration-policy.test.mjs):
// a fake home created under os.tmpdir(), scopes injected via the registration
// seam, so no test depends on the real homedir/tmpdir LAYOUT. The fs seams are
// the REAL fs over tmp fixtures; throwing seams are injected per case.
// ---------------------------------------------------------------------------

const REAL_FS = { lstatSync, realpathSync };

async function controlledScopes(t) {
  const home = await mkdtemp(path.join(os.tmpdir(), "pi-web-fs-manage-home-"));
  const outside = await mkdtemp(path.join(os.tmpdir(), "pi-web-fs-manage-out-"));
  const work = path.join(home, "work");
  await mkdir(work);
  t.after(() => {
    rm(home, { recursive: true, force: true });
    rm(outside, { recursive: true, force: true });
    setScopes(null, null);
  });
  setScopes(home, null);
  return { home, outside, work };
}

// Windows needs a directory junction (no symlink privileges required); POSIX
// uses a real symlink. Both are seen as symlink entries by lstat.
async function linkDir(target, linkPath) {
  if (process.platform === "win32") {
    await symlink(target, linkPath, "junction");
  } else {
    await symlink(target, linkPath);
  }
}

function eacces() {
  return Object.assign(new Error("permission denied"), { code: "EACCES" });
}

test("the refusal codes are a fixed ordered constant (wi#59 contract order)", () => {
  assert.deepEqual(FS_MANAGE_REFUSAL_REASONS, [
    "invalidBody",
    "nonexistent",
    "symlinkEntry",
    "notDirectory",
    "outsideRegistrationPrefix",
    "pathInUse",
    "confirmMismatch",
    "targetExists",
    "ioFailure",
  ]);
});

test("invalidBody: every malformed shape, including the cross-parent nextPath", async (t) => {
  const { work } = await controlledScopes(t);
  const entry = path.join(work, "entry");

  const malformed = [
    null,
    "string",
    42,
    [],
    {},
    { action: "move", path: entry },
    { action: "rename", path: entry }, // rename without nextPath
    { action: "delete", path: "relative/path" },
    { action: "delete", path: "" },
    { action: "delete", path: 42 },
    { action: "rename", path: entry, nextPath: "relative" },
    // Sibling-only rename: a cross-parent nextPath is refused as invalidBody.
    { action: "rename", path: entry, nextPath: path.join(path.dirname(work), "elsewhere") },
  ];
  for (const body of malformed) {
    const outcome = runFsManageGuards(body, REAL_FS);
    assert.equal(outcome.ok, false, `${JSON.stringify(body)} must be invalidBody`);
    assert.equal(outcome.reason, "invalidBody");
    assert.equal(validateFsManageBody(body).ok, false);
  }
});

test("nonexistent: a missing entry AND a missing intermediate both classify as nonexistent", async (t) => {
  const { work } = await controlledScopes(t);

  const missing = runFsManageGuards({ action: "delete", path: path.join(work, "gone") }, REAL_FS);
  assert.deepEqual(missing, { ok: false, reason: "nonexistent" });

  // Missing intermediate: lstat reports ENOENT for the deepest missing
  // component (pi#58 B5) — the nearest usable ancestor classifies it.
  const intermediate = runFsManageGuards(
    { action: "delete", path: path.join(work, "missing", "middle", "leaf") },
    REAL_FS,
  );
  assert.deepEqual(intermediate, { ok: false, reason: "nonexistent" });
});

test("ioFailure: a non-ENOENT lstat error maps to the typed code only", async (t) => {
  const { work } = await controlledScopes(t);
  const target = path.join(work, "locked");
  await mkdir(target);

  const throwingFs = {
    lstatSync: (p) => {
      if (p === target) throw eacces();
      return lstatSync(p);
    },
    realpathSync,
  };
  const outcome = runFsManageGuards({ action: "delete", path: target, confirm: "locked" }, throwingFs);
  assert.deepEqual(outcome, { ok: false, reason: "ioFailure" });
  // Nothing was mutated by a guard-stage failure.
  assert.ok(lstatSync(target).isDirectory());
});

test("symlinkEntry: symlink entries are refused outright — live and dangling alike, referent AND entry unchanged", async (t) => {
  const { work } = await controlledScopes(t);
  const referent = path.join(work, "real");
  await mkdir(referent);
  const link = path.join(work, "link");
  await linkDir(referent, link);
  const dangling = path.join(work, "dangling");
  await linkDir(path.join(work, "nowhere"), dangling);

  for (const entry of [link, dangling]) {
    const outcome = runFsManageGuards(
      { action: "delete", path: entry, confirm: path.basename(entry) },
      REAL_FS,
    );
    assert.deepEqual(outcome, { ok: false, reason: "symlinkEntry" });
  }
  // The live symlink's referent AND both entries are unchanged.
  assert.ok(lstatSync(referent).isDirectory());
  assert.ok(lstatSync(link).isSymbolicLink());
  assert.ok(lstatSync(dangling).isSymbolicLink());
});

test("notDirectory: a regular file entry is refused", async (t) => {
  const { work } = await controlledScopes(t);
  const file = path.join(work, "notes.txt");
  await writeFile(file, "keep");

  const outcome = runFsManageGuards({ action: "delete", path: file, confirm: "notes.txt" }, REAL_FS);
  assert.deepEqual(outcome, { ok: false, reason: "notDirectory" });
  assert.equal(readFileSync(file, "utf8"), "keep");
});

test("outsideRegistrationPrefix: outside dirs, home, an env prefix, a registered root, and an ancestor of one are all refused", async (t) => {
  const home = await mkdtemp(path.join(os.tmpdir(), "pi-web-fs-manage-home-"));
  const prefix = await mkdtemp(path.join(os.tmpdir(), "pi-web-fs-manage-prefix-"));
  const outside = await mkdtemp(path.join(os.tmpdir(), "pi-web-fs-manage-out-"));
  t.after(() => {
    rm(home, { recursive: true, force: true });
    rm(prefix, { recursive: true, force: true });
    rm(outside, { recursive: true, force: true });
    setScopes(null, null);
  });
  setScopes(home, prefix);

  // A directory outside every prefix: the mutation site is not containable.
  assert.deepEqual(
    runFsManageGuards({ action: "delete", path: outside, confirm: path.basename(outside) }, REAL_FS),
    { ok: false, reason: "outsideRegistrationPrefix" },
  );

  // Home itself and the env prefix itself: their parents lie outside every
  // prefix, so containment fails closed.
  for (const target of [home, prefix]) {
    assert.deepEqual(
      runFsManageGuards({ action: "delete", path: target, confirm: path.basename(target) }, REAL_FS),
      { ok: false, reason: "outsideRegistrationPrefix" },
    );
  }

  // A REGISTERED root: its parent lies inside a prefix (which alone would
  // pass), so only the protected-site rule refuses it.
  const registeredRoots = getAdditionalAllowedRoots();
  const registered = path.join(home, "registered");
  await mkdir(registered);
  registeredRoots.add(registered);
  t.after(() => registeredRoots.delete(registered));
  assert.deepEqual(
    runFsManageGuards({ action: "delete", path: registered, confirm: "registered" }, REAL_FS),
    { ok: false, reason: "outsideRegistrationPrefix" },
  );

  // An ANCESTOR of a registered root: its own parent (home) is a valid
  // mutation site, so only the ancestor protection refuses it.
  const ancestor = path.join(home, "ancestor");
  const nestedRoot = path.join(ancestor, "registered-child");
  await mkdir(nestedRoot, { recursive: true });
  registeredRoots.add(nestedRoot);
  t.after(() => registeredRoots.delete(nestedRoot));
  assert.deepEqual(
    runFsManageGuards({ action: "delete", path: ancestor, confirm: "ancestor" }, REAL_FS),
    { ok: false, reason: "outsideRegistrationPrefix" },
  );
});

test("outsideRegistrationPrefix: a STALE registered root still protects its existing ancestors (fail-closed, never dropped)", async (t) => {
  const { home } = await controlledScopes(t);
  const registeredRoots = getAdditionalAllowedRoots();
  const nested = path.join(home, "nest");
  await mkdir(nested);
  const stale = path.join(nested, "stale-root");
  await mkdir(stale);
  registeredRoots.add(stale);
  t.after(() => registeredRoots.delete(stale));

  // The registered root vanishes on disk, but the registration stays: the
  // existing ancestor /nest must remain protected.
  await rm(stale, { recursive: true, force: true });
  const outcome = runFsManageGuards({ action: "delete", path: nested, confirm: "nest" }, REAL_FS);
  assert.deepEqual(outcome, { ok: false, reason: "outsideRegistrationPrefix" });

  // The protected-site set holds the resolved ancestor and home explicitly,
  // and the stale root itself stays in the set lexically.
  const sitesResult = computeProtectedSites(REAL_FS);
  assert.ok(sitesResult.ok, "the protection set resolves (stale root handled fail-closed)");
  const sites = sitesResult.sites;
  assert.ok(isProtectedSite(realpathSync(nested), sites));
  assert.ok(isProtectedSite(realpathSync(home), sites));
  assert.ok(isProtectedSite(stale, sites));
  // The stale registration is retained LEXICALLY for destination checks
  // (r2 B1, pi#60): renaming onto the stale path must be refused later.
  assert.ok(sitesResult.sites.staleLexical.has(stale));

  // Deleting an unrelated sibling under the stale root's parent still works.
  const sibling = path.join(nested, "sibling");
  await mkdir(sibling);
  assert.deepEqual(
    runFsManageGuards({ action: "delete", path: sibling, confirm: "sibling" }, REAL_FS),
    { ok: true, action: "delete", source: realpathSync(sibling), path: realpathSync(sibling) },
  );
});

test("rename onto a STALE registered root refuses: destination protection precedes existence (r2 B1, pi#60)", async (t) => {
  const { work } = await controlledScopes(t);
  const registeredRoots = getAdditionalAllowedRoots();
  const stale = path.join(work, "protected");
  await mkdir(stale);
  registeredRoots.add(stale);
  t.after(() => registeredRoots.delete(stale));

  // The registered root vanishes while its registration remains.
  await rm(stale, { recursive: true, force: true });

  // Renaming a sibling ONTO the stale registration must refuse: recreating
  // a protected registration target is exactly what the guard prevents.
  const source = path.join(work, "source");
  await mkdir(source);
  const outcome = runFsManageGuards(
    { action: "rename", path: source, nextPath: stale },
    REAL_FS,
  );
  assert.deepEqual(outcome, { ok: false, reason: "outsideRegistrationPrefix" });
  // Neither side moved.
  assert.ok(realpathSync(source), "the source is untouched");
  assert.equal(__fsExists(stale), false, "the destination was never created");
});

test("registered-root resolution failing with EACCES fails the whole request CLOSED as ioFailure (r2 B2, pi#60)", async (t) => {
  const { home } = await controlledScopes(t);
  const registeredRoots = getAdditionalAllowedRoots();
  const registered = path.join(home, "registered");
  await mkdir(registered);
  registeredRoots.add(registered);
  t.after(() => registeredRoots.delete(registered));

  const target = path.join(home, "sibling");
  await mkdir(target);
  const outcome = runFsManageGuards(
    { action: "delete", path: target, confirm: "sibling" },
    {
      lstatSync: REAL_FS.lstatSync,
      // Any resolution of the registered root throws EACCES: the protection
      // set cannot be established, so the pipeline must refuse ioFailure
      // rather than continue with an incomplete protection set.
      realpathSync: (p) => {
        if (p === registered) {
          const error = new Error("EACCES");
          error.code = "EACCES";
          throw error;
        }
        return REAL_FS.realpathSync(p);
      },
    },
  );
  assert.deepEqual(outcome, { ok: false, reason: "ioFailure" });
  // Zero mutation.
  assert.ok(__fsExists(target), "the target was not deleted");
});

function __fsExists(p) {
  try {
    REAL_FS.lstatSync(p);
    return true;
  } catch {
    return false;
  }
}

test("isMutationSiteAllowed: containment needs a resolvable prefix (fail-closed)", async (t) => {
  const { home, outside } = await controlledScopes(t);
  const sitesResult = computeProtectedSites(REAL_FS);
  assert.ok(sitesResult.ok);
  const sites = sitesResult.sites;
  assert.equal(isMutationSiteAllowed(realpathSync(home), sites), true);
  assert.equal(isMutationSiteAllowed(realpathSync(outside), sites), false);
  assert.equal(isMutationSiteAllowed(path.join(outside, "child"), sites), false);
});

test("pathInUse: session cwds block the entry and any ancestor; unrelated cwds do not", async (t) => {
  const { work } = await controlledScopes(t);
  const sessionDir = path.join(work, "session");
  await mkdir(sessionDir);
  const unrelated = path.join(work, "unrelated");
  await mkdir(unrelated);
  const other = path.join(work, "other");
  await mkdir(other);

  t.after(() => setCwdSource(null));
  setCwdSource(() => [sessionDir, unrelated]);

  // The entry IS a session cwd.
  assert.deepEqual(
    runFsManageGuards({ action: "delete", path: sessionDir, confirm: "session" }, REAL_FS),
    { ok: false, reason: "pathInUse" },
  );
  // The entry CONTAINS a session cwd.
  assert.deepEqual(
    runFsManageGuards({ action: "delete", path: work, confirm: "work" }, REAL_FS),
    { ok: false, reason: "pathInUse" },
  );
  // A non-blocking unrelated session cwd: a different entry still deletes.
  const ok = runFsManageGuards({ action: "delete", path: other, confirm: "other" }, REAL_FS);
  assert.equal(ok.ok, true);

  // An UNRESOLVABLE cwd (deleted since the session started) still protects
  // its lexical ancestors: realpath containment falls back to resolve().
  const goneCwd = path.join(work, "gone", "sub");
  setCwdSource(() => [goneCwd]);
  assert.equal(sessionCwdBlocksEntry(REAL_FS, realpathSync(work), [goneCwd]), true);
  assert.deepEqual(
    runFsManageGuards({ action: "delete", path: work, confirm: "work" }, REAL_FS),
    { ok: false, reason: "pathInUse" },
  );
  // ...but never blocks a directory it is not inside.
  assert.equal(sessionCwdBlocksEntry(REAL_FS, realpathSync(unrelated), [goneCwd]), false);
});

test("confirmMismatch: delete requires confirm === basename(path) exactly; rename needs no confirm", async (t) => {
  const { work } = await controlledScopes(t);
  const entry = path.join(work, "proj");
  await mkdir(entry);

  for (const confirm of [undefined, "", "Proj", "proj ", "projx", "other"]) {
    const outcome = runFsManageGuards({ action: "delete", path: entry, confirm }, REAL_FS);
    assert.deepEqual(outcome, { ok: false, reason: "confirmMismatch" }, `confirm=${JSON.stringify(confirm)}`);
  }
  const exact = runFsManageGuards({ action: "delete", path: entry, confirm: "proj" }, REAL_FS);
  assert.equal(exact.ok, true);
  // A rename carries no confirm and is not gated by one.
  const renameOutcome = runFsManageGuards(
    { action: "rename", path: entry, nextPath: path.join(work, "proj2") },
    REAL_FS,
  );
  assert.equal(renameOutcome.ok, true);
});

test("targetExists: identical nextPath, an existing sibling, and a dangling symlink destination all refuse (no same-identity exemption)", async (t) => {
  const { work } = await controlledScopes(t);
  const a = path.join(work, "alpha");
  const b = path.join(work, "beta");
  await mkdir(a);
  await mkdir(b);
  await linkDir(path.join(work, "nowhere"), path.join(work, "taken"));

  // Identical nextPath names the still-existing source: targetExists.
  assert.deepEqual(
    runFsManageGuards({ action: "rename", path: a, nextPath: a }, REAL_FS),
    { ok: false, reason: "targetExists" },
  );
  // An existing directory destination.
  assert.deepEqual(
    runFsManageGuards({ action: "rename", path: a, nextPath: b }, REAL_FS),
    { ok: false, reason: "targetExists" },
  );
  // A dangling symlink destination counts as existing per lstat.
  assert.deepEqual(
    runFsManageGuards({ action: "rename", path: a, nextPath: path.join(work, "taken") }, REAL_FS),
    { ok: false, reason: "targetExists" },
  );
  // Nothing moved.
  assert.ok(lstatSync(a).isDirectory());
  assert.ok(lstatSync(b).isDirectory());
  assert.ok(lstatSync(path.join(work, "taken")).isSymbolicLink());
});

test("happy paths: rename resolves source+destination, delete resolves the entry", async (t) => {
  const { work } = await controlledScopes(t);
  const a = path.join(work, "alpha");
  await mkdir(a);
  await mkdir(path.join(a, "child"));

  const rename = runFsManageGuards(
    { action: "rename", path: a, nextPath: path.join(work, "renamed") },
    REAL_FS,
  );
  assert.deepEqual(rename, {
    ok: true,
    action: "rename",
    source: realpathSync(a),
    path: path.join(realpathSync(work), "renamed"),
  });

  const entry = path.join(work, "victim");
  await mkdir(entry);
  const del = runFsManageGuards({ action: "delete", path: entry, confirm: "victim" }, REAL_FS);
  assert.deepEqual(del, { ok: true, action: "delete", source: realpathSync(entry), path: realpathSync(entry) });
});

test("classifyFsError: ENOENT → nonexistent, everything else → ioFailure", () => {
  assert.equal(classifyFsError(Object.assign(new Error(), { code: "ENOENT" })), "nonexistent");
  assert.equal(classifyFsError(Object.assign(new Error(), { code: "EACCES" })), "ioFailure");
  assert.equal(classifyFsError(Object.assign(new Error(), { code: "EBUSY" })), "ioFailure");
  assert.equal(classifyFsError(new Error("no code")), "ioFailure");
  assert.equal(classifyFsError(undefined), "ioFailure");
});
