import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { createJiti } from "jiti";
const jiti = createJiti(import.meta.url);
const { isClaudeGatewayUrl } = await jiti.import("./claude-api-config.ts");
test("gateway configuration accepts only absolute HTTP(S) addresses with a host", () => {
  for (const value of ["https://gateway.example.invalid/v1", "http://127.0.0.1:8080/api", "https://[::1]:8443/v1", " https://gateway.example.invalid/v1 "]) assert.equal(isClaudeGatewayUrl(value), true, value);
  for (const value of [undefined, null, "", "abc", "/v1", "https://", "file:///tmp/token", "javascript:alert(1)", "ftp://gateway.example.invalid", "https://host.invalid:99999/"]) assert.equal(isClaudeGatewayUrl(value), false, String(value));
});
