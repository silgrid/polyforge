import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { realpathSync } from "node:fs";
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
  isRegistrableRoot,
  __setRegistrationScopesForTesting: setScopes,
} = await jiti.import("./root-registration-policy.ts");
const { getAdditionalAllowedRoots } = await jiti.import("./allowed-roots.ts");

// ---------------------------------------------------------------------------
// Fixtures are CONTROLLED: a fake home and an outside sibling both created
// under os.tmpdir(). The guard's scopes are injected via the test seam, so no
// test depends on the real homedir/tmpdir LAYOUT (on Windows the tmpdir lives
// UNDER the homedir, which would silently turn "outside-home" fixtures into
// registrable paths — review r1 P2, pi#55). Canonical paths are compared
// against realpathSync, never path.resolve.
// ---------------------------------------------------------------------------

async function controlledScopes(t) {
  const home = await mkdtemp(path.join(os.tmpdir(), "pi-web-policy-home-"));
  const outside = await mkdtemp(path.join(os.tmpdir(), "pi-web-policy-out-"));
  t.after(() => {
    rm(home, { recursive: true, force: true });
    rm(outside, { recursive: true, force: true });
  });
  setScopes(home, null);
  return { home, outside };
}

// Windows needs a directory junction (no symlink privileges required);
// POSIX uses a real symlink. Both resolve through realpathSync.
async function linkDir(target, linkPath) {
  if (process.platform === "win32") {
    await symlink(target, linkPath, "junction");
  } else {
    await symlink(target, linkPath);
  }
}

test("a path inside the operator homedir is registrable, canonicalized via realpath", async (t) => {
  const { home } = await controlledScopes(t);
  const dir = await mkdtemp(path.join(home, "inside-"));
  t.after(() => rm(dir, { recursive: true, force: true }));

  const outcome = isRegistrableRoot(dir);
  assert.equal(outcome.ok, true);
  assert.equal(outcome.path, realpathSync(dir));
});

test("the filesystem root is refused", async (t) => {
  await controlledScopes(t);
  // POSIX: "/" is absolute but outside the controlled scope; win32: "/" is
  // not an absolute path — both produce the same typed refusal.
  const outcome = isRegistrableRoot("/");
  assert.equal(outcome.ok, false);
  assert.equal(outcome.reason, "outsideRegistrationPrefix");
});

test("a directory outside every registration prefix is refused", async (t) => {
  const { outside } = await controlledScopes(t);

  const outcome = isRegistrableRoot(outside);
  assert.equal(outcome.ok, false);
  assert.equal(outcome.reason, "outsideRegistrationPrefix");
});

test("a symlink inside a prefix that escapes via realpath is refused", async (t) => {
  const { home, outside } = await controlledScopes(t);

  const link = path.join(home, "escape");
  await linkDir(outside, link);

  // Lexically the candidate sits inside the scope, but its realpath is
  // outside every prefix, so the guard must refuse it.
  const outcome = isRegistrableRoot(link);
  assert.equal(outcome.ok, false);
  assert.equal(outcome.reason, "outsideRegistrationPrefix");
});

test("an operator-configured prefix admits a path under it", async (t) => {
  const prefix = await mkdtemp(path.join(os.tmpdir(), "pi-web-policy-prefix-"));
  t.after(() => rm(prefix, { recursive: true, force: true }));
  const nested = path.join(prefix, "nested");
  await mkdir(nested);
  t.after(() => rm(prefix, { recursive: true, force: true }));

  // Controlled home is irrelevant here; the env prefix admits nested.
  const home2 = await mkdtemp(path.join(os.tmpdir(), "pi-web-policy-home2-"));
  t.after(() => rm(home2, { recursive: true, force: true }));
  setScopes(home2, prefix);
  const outcome = isRegistrableRoot(nested);
  assert.equal(outcome.ok, true);
  assert.equal(outcome.path, realpathSync(nested));
});

test("empty and relative env entries are ignored", async (t) => {
  const { home } = await controlledScopes(t);
  // Re-inject the same controlled home, now with garbage env entries: they
  // contribute nothing, home containment still works, nothing extra passes.
  setScopes(home, "  , relative/path ,,");
  assert.equal(isRegistrableRoot(home).ok, true);
  assert.equal(isRegistrableRoot("/").ok, false);
});

test("an already-registered additional allowed root revalidates idempotently", async (t) => {
  const { outside } = await controlledScopes(t);

  const roots = getAdditionalAllowedRoots();
  const before = new Set(roots);
  roots.add(outside);
  t.after(() => {
    for (const entry of before) roots.add(entry);
    roots.delete(outside);
  });

  const outcome = isRegistrableRoot(outside);
  assert.equal(outcome.ok, true);
  assert.equal(outcome.path, realpathSync(outside));
});

test("nonexistent paths and regular files return their typed refusal codes", async (t) => {
  const { home } = await controlledScopes(t);
  const missing = path.join(home, "does-not-exist-anywhere");
  assert.deepEqual(isRegistrableRoot(missing), { ok: false, reason: "nonexistent" });

  const file = path.join(home, "plain.txt");
  await writeFile(file, "x");
  t.after(() => rm(file, { force: true }));
  assert.deepEqual(isRegistrableRoot(file), { ok: false, reason: "notDirectory" });

  // Relative candidates can never lie within an absolute registration prefix.
  assert.deepEqual(isRegistrableRoot("relative/dir"), { ok: false, reason: "outsideRegistrationPrefix" });
});

test("memoized scopes ignore later env changes until re-derivation (documented behavior)", async (t) => {
  const { home, outside } = await controlledScopes(t);

  // The scopes were memoized by the first evaluation above. Mutating the env
  // now must NOT leak new prefixes into the memoized decision (the parse is
  // once-per-process by design)...
  process.env.PI_WEB_ALLOWED_ROOT_PREFIXES = outside;
  assert.equal(isRegistrableRoot(outside).ok, false);
  delete process.env.PI_WEB_ALLOWED_ROOT_PREFIXES;

  // ...but an explicit re-injection DOES take effect immediately.
  setScopes(home, outside);
  assert.equal(isRegistrableRoot(outside).ok, true);
  assert.equal(isRegistrableRoot("/").ok, false);
});
