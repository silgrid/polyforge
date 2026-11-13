import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const {
  allowedPushEndpointHostSuffixes,
  checkPushEndpoint,
  hostMatchesSuffix,
  DEFAULT_PUSH_ENDPOINT_HOST_SUFFIXES,
} = await createJiti(import.meta.url, { interopDefault: true }).import("./push-endpoint-guards.ts");

test("real browser push service endpoints pass the built-in allowlist", () => {
  const builtIns = DEFAULT_PUSH_ENDPOINT_HOST_SUFFIXES;
  for (const endpoint of [
    "https://fcm.googleapis.com/fcm/send/abc",
    "https://updates.push.services.mozilla.com/wpush/v2/abc",
    "https://web.push.apple.com/v1/push/abc",
  ]) {
    const check = checkPushEndpoint(endpoint, builtIns);
    assert.equal(check.ok, true, endpoint);
    assert.equal(check.host, new URL(endpoint).hostname);
  }
});

test("subdomains of an allowed suffix are allowed, other hosts are refused with a typed code", () => {
  assert.equal(hostMatchesSuffix("fcm.googleapis.com", "fcm.googleapis.com"), true);
  assert.equal(hostMatchesSuffix("edge.fcm.googleapis.com", "fcm.googleapis.com"), true);
  assert.equal(hostMatchesSuffix("evilfcm.googleapis.com", "fcm.googleapis.com"), false);
  assert.equal(hostMatchesSuffix("evil.example", "fcm.googleapis.com"), false);

  const refused = checkPushEndpoint("https://evil.example/push", ["fcm.googleapis.com"]);
  assert.deepEqual(refused, { ok: false, reason: "pushEndpointHostNotAllowed" });
});

test("non-https endpoints are refused with the typed scheme code", () => {
  assert.deepEqual(
    checkPushEndpoint("http://fcm.googleapis.com/fcm/send/abc", ["fcm.googleapis.com"]),
    { ok: false, reason: "pushEndpointScheme" },
  );
});

test("unparseable endpoints are refused with the typed url code", () => {
  assert.deepEqual(
    checkPushEndpoint("not a url at all", ["fcm.googleapis.com"]),
    { ok: false, reason: "invalidEndpointUrl" },
  );
});

test("the operator env var extends the built-in suffixes without disabling them", () => {
  const suffixes = allowedPushEndpointHostSuffixes({
    PI_WEB_PUSH_ALLOWED_ENDPOINT_SUFFIXES: "push.example.com, ,selfhost.example.com",
  });
  assert.ok(suffixes.includes("push.example.com"));
  assert.ok(suffixes.includes("selfhost.example.com"));
  for (const builtIn of DEFAULT_PUSH_ENDPOINT_HOST_SUFFIXES) {
    assert.ok(suffixes.includes(builtIn), builtIn);
  }
  assert.equal(
    checkPushEndpoint("https://push.example.com/endpoint", suffixes).ok,
    true,
  );
});
