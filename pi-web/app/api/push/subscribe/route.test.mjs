import "#test-env";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const originalAgentDir = process.env.PI_CODING_AGENT_DIR;
const testAgentDir = await mkdtemp(join(tmpdir(), "pi-web-push-subscribe-"));
process.env.PI_CODING_AGENT_DIR = testAgentDir;

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { POST } = await jiti.import("./route.ts");

const originalSuffixes = process.env.PI_WEB_PUSH_ALLOWED_ENDPOINT_SUFFIXES;

test.after(async () => {
  if (originalAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = originalAgentDir;
  if (originalSuffixes === undefined) delete process.env.PI_WEB_PUSH_ALLOWED_ENDPOINT_SUFFIXES;
  else process.env.PI_WEB_PUSH_ALLOWED_ENDPOINT_SUFFIXES = originalSuffixes;
  await rm(testAgentDir, { recursive: true, force: true });
});

const KEYS = { p256dh: "p256dh-value", auth: "auth-value" };

function subscribe(endpoint, extraBody = {}) {
  return new Request("http://localhost/api/push/subscribe", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subscription: { endpoint, keys: KEYS }, locale: "en", ...extraBody }),
  });
}

test("a standard FCM endpoint is persisted", async () => {
  const response = await POST(subscribe("https://fcm.googleapis.com/fcm/send/abc"));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: true });

  const state = JSON.parse(await readFile(join(testAgentDir, "web-push.json"), "utf8"));
  assert.equal(state.subscriptions.length, 1);
  assert.equal(state.subscriptions[0].endpoint, "https://fcm.googleapis.com/fcm/send/abc");
});

test("an arbitrary host is refused with the typed endpoint code and never persisted", async () => {
  const response = await POST(subscribe("https://evil.example/push/abc"));
  assert.equal(response.status, 400);
  assert.deepEqual(await response.json(), { error: "pushEndpointHostNotAllowed" });

  const state = JSON.parse(await readFile(join(testAgentDir, "web-push.json"), "utf8"));
  assert.ok(state.subscriptions.every((s) => !s.endpoint.includes("evil.example")));
});

test("a plain-http endpoint is refused with the typed scheme code", async () => {
  const response = await POST(subscribe("http://fcm.googleapis.com/fcm/send/abc"));
  assert.equal(response.status, 400);
  assert.deepEqual(await response.json(), { error: "pushEndpointScheme" });
});

test("the operator suffix env var admits a self-hosted push service", async () => {
  process.env.PI_WEB_PUSH_ALLOWED_ENDPOINT_SUFFIXES = "push.example.com";
  try {
    const response = await POST(subscribe("https://push.example.com/endpoint"));
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { ok: true });
  } finally {
    delete process.env.PI_WEB_PUSH_ALLOWED_ENDPOINT_SUFFIXES;
  }
});

test("a malformed subscription body is refused with the typed body code", async () => {
  const response = await POST(new Request("http://localhost/api/push/subscribe", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subscription: { endpoint: "https://fcm.googleapis.com/x", keys: { p256dh: "", auth: "" } } }),
  }));
  assert.equal(response.status, 400);
  assert.deepEqual(await response.json(), { error: "invalidSubscription" });
});

test("a JSON null body is refused with the typed body code, not an uncaught TypeError (review r2)", async () => {
  const response = await POST(new Request("http://localhost/api/push/subscribe", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "null",
  }));
  assert.equal(response.status, 400);
  assert.deepEqual(await response.json(), { error: "invalidBody" });
});

test("a non-object JSON body (string/array/number) is refused with the typed body code", async () => {
  for (const body of ['"a string"', "[1,2,3]", "42"]) {
    const response = await POST(new Request("http://localhost/api/push/subscribe", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
    }));
    assert.equal(response.status, 400, body);
    assert.deepEqual(await response.json(), { error: "invalidBody" }, body);
  }
});
