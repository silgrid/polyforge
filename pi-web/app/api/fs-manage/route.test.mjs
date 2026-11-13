import "#test-env";
import assert from "node:assert/strict";
import { lstatSync, readdirSync, realpathSync, readlinkSync, renameSync, rmSync } from "node:fs";
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// ---------------------------------------------------------------------------
// Route tests for /api/fs-manage (wi pi#59): jiti-imports the route and
// drives POST(new Request(...)) over TMP FIXTURES ONLY. Scopes are injected
// via __setRegistrationScopesForTesting, live cwds via the guards module's
// injected session-cwd seam, and guard/mutation failures via its fs/mutator
// seams — no real AgentSessions are ever constructed.
// ---------------------------------------------------------------------------

const routeSource = await readFile(new URL("./route.ts", import.meta.url), "utf8");
const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { POST } = await jiti.import("./route.ts");
const {
  __setFsManageSessionCwdSourceForTesting: setCwdSource,
  __setFsManageFsSeamsForTesting: setFsSeams,
  __setFsManageMutatorForTesting: setMutator,
} = await jiti.import("../../../lib/fs-manage-guards.ts");
const { __setRegistrationScopesForTesting: setScopes } = await jiti.import("../../../lib/root-registration-policy.ts");
const { getAdditionalAllowedRoots } = await jiti.import("../../../lib/allowed-roots.ts");

function eacces() {
  return Object.assign(new Error("permission denied"), { code: "EACCES" });
}

async function post(body, headers = {}) {
  const response = await POST(new Request("http://localhost/api/fs-manage", {
    method: "POST",
    headers: { host: "localhost", "Content-Type": "application/json", ...headers },
    body: JSON.stringify(body),
  }));
  return { status: response.status, data: await response.json() };
}

/**
 * Controlled fixture: a fake home under tmpdir (scopes injected via the
 * registration seam — never the real homedir layout), an outside sibling,
 * and a work directory inside the home. Every seam installed by a test is
 * restored in t.after.
 */
async function fixture(t) {
  const home = await mkdtemp(path.join(os.tmpdir(), "pi-web-fs-manage-home-"));
  const outside = await mkdtemp(path.join(os.tmpdir(), "pi-web-fs-manage-out-"));
  const work = path.join(home, "work");
  await mkdir(work);
  const registeredRoots = getAdditionalAllowedRoots();
  const addedRoots = [];
  t.after(() => {
    rm(home, { recursive: true, force: true });
    rm(outside, { recursive: true, force: true });
    for (const root of addedRoots) registeredRoots.delete(root);
    setScopes(null, null);
    setCwdSource(null);
    setFsSeams(null);
    setMutator(null);
  });
  setScopes(home, null);
  return {
    home,
    outside,
    work,
    register(root) {
      registeredRoots.add(root);
      addedRoots.push(root);
    },
  };
}

/** Byte-for-byte snapshot of a fixture tree (files, dirs, symlink targets). */
async function snapshotTree(root) {
  const entries = [];
  async function walk(dir, prefix) {
    for (const name of readdirSync(dir)) {
      const rel = prefix ? `${prefix}/${name}` : name;
      const full = path.join(dir, name);
      const stat = lstatSync(full);
      if (stat.isSymbolicLink()) {
        entries.push({ rel, type: "symlink", target: readlinkSync(full) });
      } else if (stat.isDirectory()) {
        entries.push({ rel, type: "dir" });
        await walk(full, rel);
      } else {
        entries.push({ rel, type: "file", content: (await readFile(full)).toString("base64") });
      }
    }
  }
  await walk(root, "");
  return entries.sort((a, b) => a.rel.localeCompare(b.rel));
}

async function linkDir(target, linkPath) {
  if (process.platform === "win32") {
    await symlink(target, linkPath, "junction");
  } else {
    await symlink(target, linkPath);
  }
}

test("the route sits behind the standard trust gate; every typed outcome answers HTTP 200", async () => {
  // Source contract: the trust check is the first statement of POST, before
  // any body parsing or guard runs.
  const postStart = routeSource.indexOf("export async function POST(");
  const trustCheck = routeSource.indexOf("if (!isApiRequestAllowed(request))");
  assert.ok(postStart !== -1 && trustCheck > postStart, "POST must gate on isApiRequestAllowed");
  assert.ok(routeSource.indexOf("await request.json()") > trustCheck);

  // Behavioral: a cross-origin request is refused 403 with nothing mutated.
  const target = path.join(await mkdtemp(path.join(os.tmpdir(), "pi-web-fs-manage-gate-")), "entry");
  await mkdir(target);
  try {
    const refused = await post(
      { action: "delete", path: target, confirm: "entry" },
      { origin: "http://evil.example" },
    );
    assert.equal(refused.status, 403);
    assert.ok(lstatSync(target).isDirectory(), "a gate-refused request must not mutate");
  } finally {
    rmSync(path.dirname(target), { recursive: true, force: true });
  }
});

test("invalidBody: every malformed shape (incl. cross-parent nextPath) answers 200 with the typed code, fixture untouched", async (t) => {
  const { work } = await fixture(t);
  const entry = path.join(work, "entry");
  await mkdir(entry);
  const before = await snapshotTree(work);

  const malformed = [
    {},
    { action: "move", path: entry },
    { action: "delete", path: "relative/path" },
    { action: "delete", path: 42 },
    { action: "rename", path: entry }, // rename without nextPath
    { action: "rename", path: entry, nextPath: "relative" },
    // Sibling-only rename: a cross-parent nextPath is invalidBody.
    { action: "rename", path: entry, nextPath: path.join(path.dirname(work), "elsewhere") },
  ];
  for (const body of malformed) {
    const result = await post(body);
    assert.equal(result.status, 200, `${JSON.stringify(body)} is answered on HTTP 200`);
    assert.deepEqual(result.data, { ok: false, reason: "invalidBody" });
  }
  // A non-JSON body is invalidBody too.
  const response = await POST(new Request("http://localhost/api/fs-manage", {
    method: "POST",
    headers: { host: "localhost", "Content-Type": "application/json" },
    body: "not json",
  }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: false, reason: "invalidBody" });

  assert.deepEqual(await snapshotTree(work), before, "no malformed request mutated anything");
});

test("nonexistent: a missing target AND a missing intermediate classify as nonexistent (fixture untouched)", async (t) => {
  const { work } = await fixture(t);
  const before = await snapshotTree(work);

  for (const target of [path.join(work, "gone"), path.join(work, "missing", "middle", "leaf")]) {
    const result = await post({ action: "delete", path: target, confirm: "gone" });
    assert.equal(result.status, 200);
    assert.deepEqual(result.data, { ok: false, reason: "nonexistent" });
  }
  assert.deepEqual(await snapshotTree(work), before);
});

test("symlinkEntry: a symlink target is refused; the referent AND the entry stay untouched", async (t) => {
  const { work } = await fixture(t);
  const referent = path.join(work, "real");
  await mkdir(referent);
  await writeFile(path.join(referent, "keep.txt"), "keep");
  const link = path.join(work, "link");
  await linkDir(referent, link);
  const before = await snapshotTree(work);

  for (const body of [
    { action: "delete", path: link, confirm: "link" },
    { action: "rename", path: link, nextPath: path.join(work, "moved") },
  ]) {
    const result = await post(body);
    assert.equal(result.status, 200);
    assert.deepEqual(result.data, { ok: false, reason: "symlinkEntry" });
  }
  assert.ok(lstatSync(link).isSymbolicLink(), "the entry itself is unchanged");
  assert.equal((await readFile(path.join(referent, "keep.txt"))).toString(), "keep", "the referent is unchanged");
  assert.deepEqual(await snapshotTree(work), before);
});

test("notDirectory: a file target is refused with the fixture untouched", async (t) => {
  const { work } = await fixture(t);
  const file = path.join(work, "notes.txt");
  await writeFile(file, "keep");
  const before = await snapshotTree(work);

  const result = await post({ action: "delete", path: file, confirm: "notes.txt" });
  assert.equal(result.status, 200);
  assert.deepEqual(result.data, { ok: false, reason: "notDirectory" });
  assert.deepEqual(await snapshotTree(work), before);
});

test("outsideRegistrationPrefix: outside dirs, home, a registered root, an ancestor, and a stale-registered ancestor are all refused", async (t) => {
  const f = await fixture(t);

  // A REGISTERED root (its parent lies inside a prefix, so only the
  // protected-site rule refuses it).
  const registered = path.join(f.work, "registered");
  await mkdir(registered, { recursive: true });
  f.register(registered);

  // An ANCESTOR of a registered root.
  const ancestor = path.join(f.work, "anc");
  const nested = path.join(ancestor, "child");
  await mkdir(nested, { recursive: true });
  f.register(nested);

  // A STALE registered root: registered, then deleted on disk — the
  // registration must keep protecting its existing ancestors.
  const nest = path.join(f.work, "nest");
  const stale = path.join(nest, "stale-root");
  await mkdir(stale, { recursive: true });
  f.register(stale);
  await rm(stale, { recursive: true, force: true });

  const before = await snapshotTree(f.home);

  // A directory outside every registration prefix.
  assert.deepEqual(
    (await post({ action: "delete", path: f.outside, confirm: path.basename(f.outside) })).data,
    { ok: false, reason: "outsideRegistrationPrefix" },
  );
  assert.ok(lstatSync(f.outside).isDirectory());

  // Home itself.
  assert.deepEqual(
    (await post({ action: "delete", path: f.home, confirm: path.basename(f.home) })).data,
    { ok: false, reason: "outsideRegistrationPrefix" },
  );

  assert.deepEqual(
    (await post({ action: "delete", path: registered, confirm: "registered" })).data,
    { ok: false, reason: "outsideRegistrationPrefix" },
  );
  assert.ok(lstatSync(registered).isDirectory());

  assert.deepEqual(
    (await post({ action: "delete", path: ancestor, confirm: "anc" })).data,
    { ok: false, reason: "outsideRegistrationPrefix" },
  );
  assert.ok(lstatSync(ancestor).isDirectory());

  // The stale root's existing ancestor /work/nest stays protected even
  // though the registered directory itself is gone.
  assert.deepEqual(
    (await post({ action: "delete", path: nest, confirm: "nest" })).data,
    { ok: false, reason: "outsideRegistrationPrefix" },
  );
  assert.ok(lstatSync(nest).isDirectory());

  // No refusal mutated the fixture tree.
  assert.deepEqual(await snapshotTree(f.home), before);
});

test("pathInUse: live/starting session cwds block the entry and its ancestors; unrelated cwds do not", async (t) => {
  const f = await fixture(t);
  const sessionDir = path.join(f.work, "session");
  await mkdir(sessionDir);
  const unrelated = path.join(f.work, "unrelated");
  await mkdir(unrelated);
  const other = path.join(f.work, "other");
  await mkdir(other);

  // Live and starting cwds arrive through the same rpc-manager export; the
  // injected seam stands in for both (the union itself is covered by
  // lib/rpc-manager.test.mjs).
  setCwdSource(() => [sessionDir, unrelated]);

  assert.deepEqual(
    (await post({ action: "delete", path: sessionDir, confirm: "session" })).data,
    { ok: false, reason: "pathInUse" },
  );
  // An ancestor of a session cwd is blocked too.
  assert.deepEqual(
    (await post({ action: "delete", path: f.work, confirm: "work" })).data,
    { ok: false, reason: "pathInUse" },
  );
  // An unrelated session cwd does not block a different entry: the delete
  // succeeds and the directory is really gone.
  const otherReal = realpathSync(other);
  const okDelete = await post({ action: "delete", path: other, confirm: "other" });
  assert.equal(okDelete.status, 200);
  assert.deepEqual(okDelete.data, { ok: true, path: otherReal });
  assert.ok(!lstatSync(other, { throwIfNoEntry: false }));
  // The session cwd itself is untouched.
  assert.ok(lstatSync(sessionDir).isDirectory());
});

test("confirmMismatch: a wrong or missing typed name refuses and keeps the fixture untouched", async (t) => {
  const { work } = await fixture(t);
  const entry = path.join(work, "proj");
  await mkdir(entry);
  const before = await snapshotTree(work);

  for (const confirm of [undefined, "wrong", "Proj"]) {
    const result = await post({ action: "delete", path: entry, confirm });
    assert.equal(result.status, 200);
    assert.deepEqual(result.data, { ok: false, reason: "confirmMismatch" });
  }
  assert.deepEqual(await snapshotTree(work), before);
});

test("targetExists: identical, existing and dangling-symlink destinations all refuse (no same-identity exemption)", async (t) => {
  const { work } = await fixture(t);
  const alpha = path.join(work, "alpha");
  const beta = path.join(work, "beta");
  await mkdir(alpha);
  await mkdir(beta);
  await linkDir(path.join(work, "nowhere"), path.join(work, "taken"));
  const before = await snapshotTree(work);

  for (const nextPath of [alpha, beta, path.join(work, "taken")]) {
    const result = await post({ action: "rename", path: alpha, nextPath });
    assert.equal(result.status, 200);
    assert.deepEqual(result.data, { ok: false, reason: "targetExists" });
  }
  assert.deepEqual(await snapshotTree(work), before);
});

test("ioFailure: an injected EACCES answers the EXACT typed shape — no prose, no paths; partial recursive deletes stay honest", async (t) => {
  const { work } = await fixture(t);
  const target = path.join(work, "locked");
  await mkdir(target);

  // Guard-stage failure: lstat throws EACCES for the target.
  setFsSeams({
    lstatSync: (p) => {
      if (p === target) throw eacces();
      return lstatSync(p);
    },
    realpathSync,
  });
  const guardStage = await post({ action: "delete", path: target, confirm: "locked" });
  assert.equal(guardStage.status, 200);
  assert.deepEqual(guardStage.data, { ok: false, reason: "ioFailure" });
  const serialized = JSON.stringify(guardStage.data);
  assert.ok(!serialized.includes("EACCES") && !serialized.includes(target), "no exception prose or paths leak");
  assert.ok(lstatSync(target).isDirectory());
  setFsSeams(null);

  // Mutation-stage failure on a rename: the source stays on disk.
  const toRename = path.join(work, "src");
  await mkdir(toRename);
  setMutator({
    removeDirectory: () => {},
    renameDirectory: () => { throw eacces(); },
  });
  const renameFailure = await post({ action: "rename", path: toRename, nextPath: path.join(work, "dst") });
  assert.deepEqual(renameFailure.data, { ok: false, reason: "ioFailure" });
  assert.ok(lstatSync(toRename).isDirectory());
  setMutator(null);

  // Partial recursive delete: the injected mutator removes ONE child then
  // fails — the response is ioFailure (never success) and the partial state
  // is honest on disk.
  const victim = path.join(work, "victim");
  await mkdir(victim);
  await writeFile(path.join(victim, "a.txt"), "a");
  await writeFile(path.join(victim, "b.txt"), "b");
  setMutator({
    removeDirectory: (p) => {
      rmSync(path.join(p, "a.txt"), { force: false });
      throw eacces();
    },
    renameDirectory: (source, destination) => renameSync(source, destination),
  });
  const partial = await post({ action: "delete", path: victim, confirm: "victim" });
  assert.equal(partial.status, 200);
  assert.deepEqual(partial.data, { ok: false, reason: "ioFailure" });
  assert.ok(!lstatSync(path.join(victim, "a.txt"), { throwIfNoEntry: false }), "the partial delete really happened");
  assert.ok(lstatSync(path.join(victim, "b.txt")), "the untouched sibling survives");
  assert.ok(lstatSync(victim).isDirectory(), "the victim itself survives a failed recursive delete");
});

test("happy rename: the entry moves on disk and the response carries the destination", async (t) => {
  const { work } = await fixture(t);
  const alpha = path.join(work, "alpha");
  await mkdir(path.join(alpha, "nested"), { recursive: true });
  await writeFile(path.join(alpha, "nested", "file.txt"), "payload");

  const result = await post({ action: "rename", path: alpha, nextPath: path.join(work, "renamed") });
  assert.equal(result.status, 200);
  assert.deepEqual(result.data, { ok: true, path: path.join(realpathSync(work), "renamed") });
  assert.ok(!lstatSync(alpha, { throwIfNoEntry: false }), "the source is gone");
  assert.equal(
    (await readFile(path.join(realpathSync(work), "renamed", "nested", "file.txt"))).toString(),
    "payload",
    "the contents traveled with the rename",
  );
});

test("happy delete: a non-empty tree is removed recursively and the response is typed", async (t) => {
  const { work } = await fixture(t);
  const victim = path.join(work, "victim");
  await mkdir(path.join(victim, "nested", "deeper"), { recursive: true });
  await writeFile(path.join(victim, "nested", "deeper", "file.txt"), "payload");

  const victimReal = realpathSync(victim);
  const result = await post({ action: "delete", path: victim, confirm: "victim" });
  assert.equal(result.status, 200);
  assert.deepEqual(result.data, { ok: true, path: victimReal });
  assert.ok(!lstatSync(victim, { throwIfNoEntry: false }), "the tree is gone");
});
