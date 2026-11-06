import "#test-env";
import assert from "node:assert/strict";
import { mkdtemp, rm, symlink } from "node:fs/promises";
import { realpathSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// Work under os.tmpdir() and configure it as an operator registration prefix
// BEFORE the route module (and its guard) first evaluates the environment.
process.env.PI_WEB_ALLOWED_ROOT_PREFIXES = os.tmpdir();
// A refused cwd must never reach the real rpc-manager, so the mocked module
// must not keep any test process alive either.
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";

// jiti module interception: route.ts imports startRpcSession from
// "@/lib/rpc-manager"; a virtual module stands in for it so tests can prove
// the guard runs BEFORE session creation.
const startRpcCalls = [];
const startRpcSession = async (key, _sessionId, cwd, options) => {
  startRpcCalls.push({ key, cwd, options });
  return {
    session: {
      send: async (command) => {
        if (command?.type === "get_state") {
          return { model: { id: "test-model", provider: "test-provider" }, thinkingLevel: "medium" };
        }
        return { echoed: command };
      },
    },
    realSessionId: "test-session-id",
  };
};

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
  virtualModules: {
    "@/lib/rpc-manager": { startRpcSession },
  },
});
const { POST } = await jiti.import("./route.ts");
const { getAdditionalAllowedRoots, normalizeSlashes } = await jiti.import("../../../../lib/allowed-roots.ts");

function post(body) {
  return POST(new Request("http://localhost/api/agent/new", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  }));
}

test("a registrable cwd still reaches startRpcSession with the same arguments and response shape", async (t) => {
  const cwd = await mkdtemp(path.join(os.tmpdir(), "pi-web-agent-new-ok-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  startRpcCalls.length = 0;

  const response = await post({ cwd, type: "ensure_session" });

  assert.equal(response.status, 200);
  assert.equal(startRpcCalls.length, 1);
  // Canonical expectations: the session receives the guard's REALPATH result,
  // and the allowlist stores slash-normalized keys (r2 P2, pi#55).
  const canonical = realpathSync(cwd);
  assert.equal(startRpcCalls[0].cwd, canonical);
  assert.match(startRpcCalls[0].key, /^__new__/);
  assert.deepEqual(await response.json(), {
    success: true,
    sessionId: "test-session-id",
    data: null,
    model: { provider: "test-provider", modelId: "test-model" },
    thinkingLevel: "medium",
  });
  assert.ok(getAdditionalAllowedRoots().has(normalizeSlashes(canonical)));
});

test("the filesystem root is refused with a typed 403: no session, no registration", async () => {
  const rootsBefore = new Set(getAdditionalAllowedRoots());
  startRpcCalls.length = 0;

  const response = await post({ cwd: "/", type: "ensure_session" });

  assert.equal(response.status, 403);
  const body = await response.json();
  assert.equal(body.reason, "outsideRegistrationPrefix");
  assert.ok(typeof body.error === "string" && body.error.length > 0);
  assert.equal(startRpcCalls.length, 0);
  const rootsAfter = getAdditionalAllowedRoots();
  assert.deepEqual([...rootsAfter].sort(), [...rootsBefore].sort());
  assert.equal(rootsAfter.has("/"), false);
});

test("a directory outside every registration prefix is refused: no session, no registration", async () => {
  // Cross-platform guaranteed-existing outside directory (volume root of
  // the tmpdir drive): POSIX "/", Windows e.g. "C:\\" — outside both the
  // operator homedir and the tmpdir prefix configured for this suite (the
  // /etc literal failed on Windows — review r1 P2, pi#55).
  const candidate = path.parse(path.resolve(os.tmpdir())).root;
  const rootsBefore = new Set(getAdditionalAllowedRoots());
  startRpcCalls.length = 0;

  const response = await post({ cwd: candidate, type: "ensure_session" });

  assert.equal(response.status, 403);
  const body = await response.json();
  assert.equal(body.reason, "outsideRegistrationPrefix");
  assert.equal(startRpcCalls.length, 0);
  const rootsAfter = getAdditionalAllowedRoots();
  assert.deepEqual([...rootsAfter].sort(), [...rootsBefore].sort());
  assert.equal(rootsAfter.has(candidate), false);
});

test("an accepted symlink cwd passes only its canonical target to startRpcSession and the allowlist", async (t) => {
  // P1 regression (review r1, pi#55): the client-supplied ALIAS must never
  // be registered or handed to the session — only the realpath target. A
  // registered alias could later be retargeted to smuggle an outside
  // directory into the trusted prefixes.
  const target = await mkdtemp(path.join(os.tmpdir(), "pi-web-agent-new-target-"));
  t.after(() => rm(target, { recursive: true, force: true }));
  const link = path.join(os.tmpdir(), `pi-web-agent-new-alias-${Date.now()}`);
  if (process.platform === "win32") {
    await symlink(target, link, "junction");
  } else {
    await symlink(target, link);
  }
  t.after(() => rm(link, { force: true }));

  const rootsBefore = new Set(getAdditionalAllowedRoots());
  startRpcCalls.length = 0;

  const response = await post({ cwd: link, type: "ensure_session" });

  assert.equal(response.status, 200);
  assert.equal(startRpcCalls.length, 1);
  const canonical = realpathSync(link);
  assert.equal(startRpcCalls[0].cwd, canonical, "the session gets the realpath target, not the alias");
  assert.ok(getAdditionalAllowedRoots().has(normalizeSlashes(canonical)), "the allowlist registers the canonical target");
  const normalizedAlias = getAdditionalAllowedRoots().has(normalizeSlashes(link));
  assert.equal(normalizedAlias, false, "the alias itself is never registered");
  for (const entry of rootsBefore) getAdditionalAllowedRoots().add(entry);
  getAdditionalAllowedRoots().delete(normalizeSlashes(canonical));
});

test("a prompt request refused by the guard keeps the prompt_rejected envelope", async () => {
  const rootsBefore = new Set(getAdditionalAllowedRoots());
  startRpcCalls.length = 0;

  const response = await post({ cwd: "/", type: "prompt", message: "hello" });

  assert.equal(response.status, 403);
  const body = await response.json();
  assert.equal(body.reason, "outsideRegistrationPrefix");
  assert.equal(body.code, "prompt_rejected");
  assert.equal(body.accepted, false);
  assert.equal(startRpcCalls.length, 0);
  assert.deepEqual([...getAdditionalAllowedRoots()].sort(), [...rootsBefore].sort());
});

test("a nonexistent cwd keeps the existing 400, and a non-directory gets notDirectory", async () => {
  startRpcCalls.length = 0;

  const missing = await post({ cwd: "/does-not-exist-anywhere", type: "ensure_session" });
  assert.equal(missing.status, 400);
  assert.equal(startRpcCalls.length, 0);

  // existsSync passes for a regular file, so the guard's notDirectory code is
  // what stops it — with the same prompt_rejected envelope for prompt sends.
  const file = await mkdtemp(path.join(os.tmpdir(), "pi-web-agent-new-file-"));
  const filePath = path.join(file, "plain.txt");
  const { writeFile } = await import("node:fs/promises");
  await writeFile(filePath, "x");
  try {
    const asFile = await post({ cwd: filePath, type: "prompt", message: "hello" });
    assert.equal(asFile.status, 403);
    const body = await asFile.json();
    assert.equal(body.reason, "notDirectory");
    assert.equal(body.code, "prompt_rejected");
    assert.equal(body.accepted, false);
    assert.equal(startRpcCalls.length, 0);
  } finally {
    await rm(file, { recursive: true, force: true });
  }
});
