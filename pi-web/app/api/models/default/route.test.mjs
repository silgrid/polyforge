import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// Boundary regression for the review follow-up: a JSON body of null (or a
// primitive) must answer the controlled 400, never surface as a TypeError
// from reading properties off the parsed value.
const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { PUT } = await jiti.import("./route.ts");

function put(body) {
  return PUT(new Request("http://localhost/api/models/default", {
    method: "PUT",
    headers: { host: "localhost", "content-type": "application/json" },
    body: typeof body === "string" ? body : JSON.stringify(body),
  }));
}

test("a JSON-null body answers the controlled 400, not a TypeError", async () => {
  const response = await put(null);
  assert.equal(response.status, 400);
  assert.deepEqual(await response.json(), { error: "Invalid JSON body" });
});

test("JSON-primitive bodies answer the controlled 400 as well", async () => {
  for (const primitive of [7, "plain text", true]) {
    const response = await put(primitive);
    assert.equal(response.status, 400, `body: ${JSON.stringify(primitive)}`);
  }
});
