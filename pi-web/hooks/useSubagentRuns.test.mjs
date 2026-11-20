// carries: hooks/useSubagentRuns.ts, lib/subagent-runs-view.ts
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./useSubagentRuns.ts", import.meta.url), "utf8");

test("the hook polls the parent-scoped route for its own session only", () => {
  assert.match(source, /\/api\/agent\/\$\{encodeURIComponent\(sessionId\)\}\/subagent-runs/);
  // The poll exists only while a session is given, and stops with it.
  assert.match(source, /if \(!sessionId\) return undefined;/);
  assert.match(source, /setInterval/);
  assert.match(source, /clearInterval/);
  // A session switch drops the previous session's poll before the next read.
  assert.match(source, /\[sessionId\]/);
  // A switch also clears the previous session's rows before the first read.
  assert.match(source, /setRuns\(\[\]\);/);
});
