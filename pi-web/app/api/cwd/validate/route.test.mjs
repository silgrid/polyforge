import "#test-env";
import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { realpathSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// The route's registrable-root guard admits the operator homedir plus
// PI_WEB_ALLOWED_ROOT_PREFIXES. This suite works under os.tmpdir(), so
// configure it as an operator prefix BEFORE the route module (and its guard)
// evaluates the environment for the first time in this process.
process.env.PI_WEB_ALLOWED_ROOT_PREFIXES = os.tmpdir();

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { POST } = await jiti.import("./route.ts");
const { projectIdentityKey } = await jiti.import("../../../../lib/project-identity.ts");
const { getAdditionalAllowedRoots, normalizeSlashes } = await jiti.import("../../../../lib/allowed-roots.ts");

function post(cwd) {
  return POST(new Request("http://localhost/api/cwd/validate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ cwd }),
  }));
}

test("validated cwd responses include server-resolved project identity", async (t) => {
  const cwd = await mkdtemp(path.join(os.tmpdir(), "pi-web-cwd-validate-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));

  const response = await post(cwd);

  assert.equal(response.status, 200);
  // Canonical expectation: the guard canonicalizes via realpath, so a
  // symlinked tmpdir ancestor must not break this assertion (r2 P2, pi#55).
  const canonical = realpathSync(cwd);
  assert.deepEqual(await response.json(), {
    success: true,
    cwd: canonical,
    projectRoot: canonical,
    projectKey: projectIdentityKey(canonical),
  });
});

test("a prefix-allowed cwd is still promoted into the allowed roots", async (t) => {
  const cwd = await mkdtemp(path.join(os.tmpdir(), "pi-web-cwd-validate-ok-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));

  const response = await post(cwd);
  assert.equal(response.status, 200);
  // The allowlist stores slash-normalized keys; membership checks must
  // normalize the canonical path the same way (r2 P2, pi#55).
  assert.ok(getAdditionalAllowedRoots().has(normalizeSlashes(realpathSync(cwd))));
});

test("the filesystem root is refused with a typed 403 and never registered", async () => {
  const rootsBefore = new Set(getAdditionalAllowedRoots());

  const response = await post("/");

  assert.equal(response.status, 403);
  const body = await response.json();
  assert.equal(body.reason, "outsideRegistrationPrefix");
  assert.ok(typeof body.error === "string" && body.error.length > 0);
  // allowFileRoot must never have run: the additional-roots set is unchanged.
  const rootsAfter = getAdditionalAllowedRoots();
  assert.deepEqual([...rootsAfter].sort(), [...rootsBefore].sort());
  assert.equal(rootsAfter.has("/"), false);
});

test("a directory outside every registration prefix is refused with a typed 403 and never registered", async () => {
  // Cross-platform guaranteed-existing outside directory: the volume root
  // of the tmpdir drive — on POSIX "/", on Windows e.g. "C:\\". It exists,
  // is a directory, and sits outside both the operator homedir and the
  // tmpdir prefix configured above (the /etc literal failed on Windows,
  // where it does not exist — review r1 P2, pi#55).
  const candidate = path.parse(path.resolve(os.tmpdir())).root;
  const rootsBefore = new Set(getAdditionalAllowedRoots());

  const response = await post(candidate);

  assert.equal(response.status, 403);
  const body = await response.json();
  assert.equal(body.reason, "outsideRegistrationPrefix");
  const rootsAfter = getAdditionalAllowedRoots();
  assert.deepEqual([...rootsAfter].sort(), [...rootsBefore].sort());
  assert.equal(rootsAfter.has(candidate), false);
});
