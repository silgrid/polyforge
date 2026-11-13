// Pins AGENTS.md's managed `BEGIN:nextjs-agent-rules` block to the installed
// Next. Symptom: every `next dev` (dev and every e2e suite)
// left a tracked ` M AGENTS.md`. Cause: the dev server rewrites the block
// unless `hasCurrentAgentRules()` finds it byte-identical to what its own
// generator writes, and the committed heading was `#` where Next writes `##`.
// Fix: the block is committed in byte parity; this test fails the change that
// breaks the parity (a Next bump) instead of letting it dirty every checkout.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const generator = require("next/dist/server/lib/generate-agent-files.js");
const root = dirname(dirname(fileURLToPath(import.meta.url)));

test("AGENTS.md's nextjs-agent-rules block is what the installed next writes", () => {
  assert.equal(
    generator.hasCurrentAgentRules(root),
    true,
    "AGENTS.md's BEGIN:nextjs-agent-rules block is no longer byte-identical to what the installed `next` writes, so `next dev` will rewrite it on every run; refresh the block from buildAgentRulesBlock() in next/dist/server/lib/generate-agent-files.js in the same change that bumps `next`",
  );
});
