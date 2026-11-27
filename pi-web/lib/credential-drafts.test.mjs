import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { createJiti } from "jiti";
const jiti = createJiti(import.meta.url);
const { credentialDraftKey, settleCredentialDrafts, retainCredentialDrafts } = await jiti.import("./credential-drafts.ts");
const attempt = (targets, value, kind = "api-key") => ({ targets, value, kind, baseUrl: kind === "gateway" ? "https://fixture.invalid/v1" : "", useInteractive: false });

test("a failed target keeps its original secret when another target saves a different secret", () => {
  const failedA = settleCredentialDrafts({}, attempt(["A"], "fixture-X"), [{ target: "A", status: "refused" }]);
  const afterB = settleCredentialDrafts(failedA, attempt(["B"], "fixture-Y"), [{ target: "B", status: "written" }]);
  assert.deepEqual(afterB, failedA);
  assert.equal(afterB[credentialDraftKey("A", "api-key")].value, "fixture-X");
  assert.equal(Object.keys(afterB).length, 1);
});
test("partial settlement is per target and kind; absent outcomes remain unconfirmed", () => {
  const gateway = settleCredentialDrafts({}, attempt(["A"], "fixture-gateway", "gateway"));
  const pending = settleCredentialDrafts(gateway, attempt(["A", "B"], "fixture-key"));
  const partial = settleCredentialDrafts(pending, attempt(["A", "B"], "fixture-key"), [{ target: "A", status: "written" }, { target: "B", status: "refused" }]);
  assert.equal(partial[credentialDraftKey("A", "api-key")], undefined);
  assert.equal(partial[credentialDraftKey("A", "gateway")].value, "fixture-gateway");
  assert.equal(partial[credentialDraftKey("B", "api-key")].state, "failed");
  assert.equal(gateway[credentialDraftKey("A", "gateway")].state, "unconfirmed");
  assert.equal(pending[credentialDraftKey("A", "api-key")].value, "fixture-key", "settlement must not mutate an older snapshot");
});
test("restoring one failed draft retains the other target currently being edited", () => {
  const a = settleCredentialDrafts({}, attempt(["A"], "fixture-X"));
  const held = retainCredentialDrafts(a, attempt(["B"], "fixture-Y"), credentialDraftKey("A", "api-key"));
  assert.equal(held[credentialDraftKey("A", "api-key")].value, "fixture-X");
  assert.equal(held[credentialDraftKey("B", "api-key")].value, "fixture-Y");
  assert.equal(held[credentialDraftKey("B", "api-key")].state, "pending");
});
