// carries: hooks/useSubagentRuns.ts, app/api/agent/*/subagent-runs/*, lib/subagent-runs-view.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

const agentDir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-subagent-runs-route-"));
process.env.PI_CODING_AGENT_DIR = agentDir;
after(() => {
  fs.rmSync(agentDir, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { tsconfigPaths: true, interopDefault: true, moduleCache: false });

test("the projection keeps only running and queued runs, oldest first, with the list's own fields", async () => {
  const { projectRunningSubagentRuns } = await jiti.import("@/lib/subagent-runs-view");
  const run = (fields) => ({
    sessionId: "s-1", sessionPath: "/tmp/s1.jsonl", parentSessionId: "parent-1", parentToolCallId: "toolu_1",
    profile: "general-purpose", description: "Audit", task: "do it", runInBackground: true,
    status: "running", createdAt: "2026-11-18T12:00:00.000Z", ...fields,
  });
  const projected = projectRunningSubagentRuns([
    run({ sessionId: "s-new", createdAt: "2026-11-18T12:00:20.000Z" }),
    run({ sessionId: "s-q", status: "queued", createdAt: "2026-11-18T12:00:10.000Z" }),
    run({ sessionId: "s-done", status: "completed", completedAt: "2026-11-18T12:00:05.000Z" }),
    run({ sessionId: "s-int", status: "interrupted" }),
    run({ sessionId: "s-old", createdAt: "2026-11-18T12:00:00.000Z" }),
  ]);
  // Only live runs survive; the order is the run's own dispatch time.
  assert.deepEqual(projected.map((row) => row.sessionId), ["s-old", "s-q", "s-new"]);
  assert.equal(projected[0].parentSessionId, "parent-1");
  assert.equal(projected[0].profile, "general-purpose");
  assert.equal(projected[0].description, "Audit");
  // The row carries no payload the list never shows.
  assert.deepEqual(Object.keys(projected[0]).sort(), ["createdAt", "description", "parentSessionId", "profile", "sessionId", "status"]);
});

test("the route answers one parent's running rows as JSON, empty included", async () => {
  const { GET } = await jiti.import("./route.ts");
  const answer = async (id) => {
    const response = await GET(new Request(`http://localhost/api/agent/${id}/subagent-runs`), { params: Promise.resolve({ id }) });
    const body = await response.json();
    return { status: response.status, body };
  };
  // A session nobody runs anything for answers an empty list, never an error.
  const empty = await answer("11111111-111-4111-8111-111111111111");
  assert.equal(empty.status, 200);
  assert.deepEqual(empty.body, { runs: [] });
  // A malformed ref reads the same way (an id is opaque to this route).
  const odd = await answer("not-a-session");
  assert.equal(odd.status, 200);
  assert.deepEqual(odd.body, { runs: [] });
});

test("the hook polls the parent-scoped route for its own session only", async () => {
  const { readFile } = await import("node:fs/promises");
  const source = await readFile(new URL("./useSubagentRuns.ts", import.meta.url), "utf8");
  assert.match(source, /\/api\/agent\/\$\{encodeURIComponent\(sessionId\)\}\/subagent-runs/);
  // The poll exists only while a session is given, and stops with it.
  assert.match(source, /if \(!sessionId\) return undefined;/);
  assert.match(source, /setInterval/);
  assert.match(source, /clearInterval/);
  // A session switch drops the previous session's poll before the next read.
  assert.match(source, /\[sessionId\]/);
});
