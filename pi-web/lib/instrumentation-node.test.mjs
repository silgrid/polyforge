// carries: instrumentation-node.ts
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("../instrumentation-node.ts", import.meta.url), "utf8");

test("server startup starts the pi SDK import without waiting for it (#964)", () => {
  // Next waits for register() before answering any request, so the import must
  // run in the background; the first /api/sessions then reuses the loaded SDK.
  assert.match(source, /\n {2}import\("@earendil-works\/pi-coding-agent"\)\.catch\(\(\) => \{\}\);/);
  assert.doesNotMatch(source, /await import\("@earendil-works\//);
});

test("server startup reconciles pf's agent_language without waiting for it", () => {
  // The backfill spawns pf (up to a version probe, a capability read and a
  // config list), so register() must not wait for it, and a failure must not
  // surface as an unhandled rejection.
  const body = source.slice(source.indexOf("export function registerNodeInstrumentation"));
  assert.match(body, /reconcilePolyforgeAgentLanguage\(/);
  assert.doesNotMatch(body, /await\s+[\w.]*reconcilePolyforgeAgentLanguage\(/);
  assert.match(body, /reconcilePolyforgeAgentLanguage\([^;]*\.catch\(\(\) => \{\}\);/s);
  assert.doesNotMatch(source, /await import\("@\/lib\/polyforge-config-write"\)/);
});
