import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const { searchActiveBranch, entrySearchText } = await jiti.import("./session-branch-search.ts");
const { sliceActiveBranch } = await jiti.import("./session-reader.ts");

// sliceActiveBranch sanity: the shared walk this search builds on.
test("sliceActiveBranch walks parents to the root", () => {
  const chain = sliceActiveBranch(
    [
      { id: "root", parentId: null },
      { id: "mid", parentId: "root" },
      { id: "leaf", parentId: "mid" },
    ],
    "leaf",
    10,
  );
  assert.deepEqual(chain.map((e) => e.id), ["root", "mid", "leaf"]);
});

// A tiny fixture chain: root -> u1 -> a1 -> u2 -> a2, plus a FORK off u1
// (f1) that must never be reported: the search scope is the active branch.
const entries = [
  { type: "message", id: "root", parentId: null, timestamp: "2026-01-01T00:00:00Z", message: { role: "user", content: "needle at the root" } },
  { type: "message", id: "u1", parentId: "root", timestamp: "2026-01-01T00:01:00Z", message: { role: "user", content: "plain question" } },
  { type: "message", id: "f1", parentId: "u1", timestamp: "2026-01-01T00:01:30Z", message: { role: "assistant", content: [{ type: "text", text: "forked needle (must not match)" }] } },
  { type: "message", id: "a1", parentId: "u1", timestamp: "2026-01-01T00:02:00Z", message: { role: "assistant", content: [{ type: "thinking", thinking: "needle inside thinking" }, { type: "text", text: "answer one" }] } },
  { type: "message", id: "u2", parentId: "a1", timestamp: "2026-01-01T00:03:00Z", message: { role: "user", content: "needle again, needle twice" } },
  { type: "message", id: "a2", parentId: "u2", timestamp: "2026-01-01T00:04:00Z", message: { role: "assistant", content: [{ type: "tool_result", content: [{ type: "text", text: "needle in a tool result" }] }] } },
];

test("searches only the active branch, newest first, counting every match", () => {
  const response = searchActiveBranch(entries, "needle", "a2", {});
  assert.equal(response.truncated, false);
  assert.deepEqual(
    response.matches.map((m) => m.entryId),
    ["a2", "u2", "a1", "root"], // newest -> oldest, fork f1 excluded
  );
  assert.equal(response.matches.find((m) => m.entryId === "u2").count, 2);
  assert.equal(response.totalMatches, 5); // u2 twice + a1(thinking) + root + a2(tool_result)
});

test("case-insensitive by default; case toggle narrows", () => {
  assert.equal(searchActiveBranch(entries, "NeEdLe", "a2").matches.length, 4);
  assert.equal(searchActiveBranch(entries, "NeEdLe", "a2", { caseSensitive: true }).matches.length, 0);
});

test("regex mode with metacharacters", () => {
  const response = searchActiveBranch(entries, "needle.*twice", "a2", { regex: true });
  assert.equal(response.matches.length, 1);
  assert.equal(response.matches[0].entryId, "u2");
});

test("leaf undefined anchors to the newest entry", () => {
  const response = searchActiveBranch(entries, "needle", undefined, {});
  assert.equal(response.matches.length, 4);
});

test("empty query returns nothing", () => {
  const response = searchActiveBranch(entries, "   ", "a2");
  assert.deepEqual(response, { matches: [], totalMatches: 0, truncated: false });
});

test("entrySearchText skips system messages and non-message entries", () => {
  assert.equal(entrySearchText({ type: "message", message: { role: "system", content: "x" } }), "");
  assert.equal(entrySearchText({ type: "usage", tokens: 1 }), "");
  assert.equal(entrySearchText({ type: "message", message: { role: "user", content: "hi" } }), "hi");
});
