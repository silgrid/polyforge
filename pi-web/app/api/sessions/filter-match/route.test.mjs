import "#test-env";
import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const originalAgentDir = process.env.PI_CODING_AGENT_DIR;
const testAgentDir = await mkdtemp(join(tmpdir(), "pi-web-filter-match-"));
process.env.PI_CODING_AGENT_DIR = testAgentDir;

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { GET } = await jiti.import("./route.ts");
const { SessionManager } = await jiti.import("@earendil-works/pi-coding-agent");
const { invalidateSessionListCache } = await jiti.import("../../../../lib/session-reader.ts");

const manager = SessionManager.create(testAgentDir);
// The needle sits far beyond the 160-char preview boundary the LIST payload
// now cuts at: only the server's cached FULL text can match it.
const hiddenNeedle = "orchestrator-worker-needle";
manager.appendMessage({
  role: "user",
  content: "x".repeat(400) + ` ${hiddenNeedle} beyond the preview boundary ` + "y".repeat(100),
  timestamp: Date.now(),
});
// The SDK only flushes the .jsonl once an assistant message exists.
manager.appendMessage({
  role: "assistant",
  content: [{ type: "text", text: "done" }],
  timestamp: Date.now(),
});
const manager2 = SessionManager.create(testAgentDir);
manager2.appendMessage({
  role: "user",
  content: "a plain session with no needle anywhere",
  timestamp: Date.now(),
});
manager2.appendMessage({
  role: "assistant",
  content: [{ type: "text", text: "done" }],
  timestamp: Date.now(),
});
const needleSessionId = manager.getSessionId();
const plainSessionId = manager2.getSessionId();
invalidateSessionListCache();

test.after(async () => {
  if (originalAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = originalAgentDir;
  invalidateSessionListCache();
  await rm(testAgentDir, { recursive: true, force: true });
});

function match(patterns) {
  return new Request(
    `http://localhost/api/sessions/filter-match?patterns=${encodeURIComponent(JSON.stringify(patterns))}`,
    { headers: { Host: "localhost" } },
  );
}

test("a pattern matching only beyond the preview boundary still matches (review r1)", async () => {
  const response = await GET(match([hiddenNeedle]));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.ok(Array.isArray(payload.matchedIds));
  assert.deepEqual(payload.matchedIds, [needleSessionId]);
});

test("the same needle no longer appears in the trimmed LIST payload the client sees", async () => {
  // Guards the fixture itself: the needle must live only in the full cached
  // text, i.e. beyond the LIST preview cut.
  const { trimSessionListFirstMessages } = await jiti.import("../../../../lib/session-list-payload.ts");
  const { listAllSessions } = await jiti.import("../../../../lib/session-reader.ts");
  const trimmed = trimSessionListFirstMessages(await listAllSessions({ force: true }));
  const row = trimmed.find((session) => session.id === needleSessionId);
  assert.ok(row);
  assert.equal(row.firstMessage.includes(hiddenNeedle), false);
  assert.equal(row.firstMessageTruncated, true);
});

test("case-insensitive matching and name matches behave like the client-side rules", async () => {
  const response = await GET(match([hiddenNeedle.toUpperCase()]));
  assert.deepEqual((await response.json()).matchedIds, [needleSessionId]);

  const byName = await GET(match(["plain session"]));
  assert.deepEqual((await byName.json()).matchedIds, [plainSessionId]);
});

test("non-matching patterns return an empty set; whitespace-only patterns never match", async () => {
  const response = await GET(match(["nothing-matches-this"]));
  assert.deepEqual((await response.json()).matchedIds, []);
  const blank = await GET(match(["   "]));
  assert.deepEqual((await blank.json()).matchedIds, []);
});

test("malformed pattern payloads are refused with a typed code", async () => {
  for (const patterns of ["not-json", "[1,2]", JSON.stringify("string"), JSON.stringify({ a: 1 })]) {
    const response = await GET(new Request(
      `http://localhost/api/sessions/filter-match?patterns=${encodeURIComponent(patterns)}`,
    ));
    assert.equal(response.status, 400, patterns);
    assert.deepEqual(await response.json(), { error: "invalidPatterns" });
  }
});
