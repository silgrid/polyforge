import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const {
  SESSION_LIST_FIRST_MESSAGE_MAX,
  trimSessionListFirstMessage,
  trimSessionListFirstMessages,
} = await createJiti(import.meta.url, { interopDefault: true }).import("./session-list-payload.ts");
const { skillExpansionToCommand } = await createJiti(import.meta.url, { interopDefault: true }).import("./slash-display.ts");

const base = {
  path: "/tmp/s.jsonl",
  id: "s",
  cwd: "/tmp",
  created: "2026-01-01T00:00:00.000Z",
  modified: "2026-01-01T00:00:00.000Z",
  messageCount: 1,
  firstMessage: "",
};

test("short first messages pass through untouched with no marker", () => {
  const session = { ...base, firstMessage: "hello" };
  assert.equal(trimSessionListFirstMessage(session), session);
  const trimmed = trimSessionListFirstMessages([session]);
  assert.equal(trimmed[0], session);
});

test("long first messages are cut to the cap and marked truncated", () => {
  const session = { ...base, firstMessage: "x".repeat(10_000) };
  const trimmed = trimSessionListFirstMessage(session);
  assert.equal(trimmed.firstMessage.length, SESSION_LIST_FIRST_MESSAGE_MAX);
  assert.equal(trimmed.firstMessage, "x".repeat(SESSION_LIST_FIRST_MESSAGE_MAX));
  assert.equal(trimmed.firstMessageTruncated, true);
  // the source row is not mutated
  assert.equal(session.firstMessage.length, 10_000);
  assert.equal(session.firstMessageTruncated, undefined);
});

test("a boundary-length message is left whole", () => {
  const session = { ...base, firstMessage: "y".repeat(SESSION_LIST_FIRST_MESSAGE_MAX) };
  const trimmed = trimSessionListFirstMessage(session);
  assert.equal(trimmed, session);
  assert.equal(trimmed.firstMessageTruncated, undefined);
});

test("a truncated SDK skill expansion carries its collapsed command as display metadata (review r1)", () => {
  const args = "some long argument string the user typed after the command";
  const full = `<skill name="my-skill" location="/repo/.agents/skills/my-skill/SKILL.md">\n` +
    `References are relative to /repo/.agents/skills/my-skill.\n\n` +
    `${"skill body reference line\n".repeat(30)}\n` +
    `</skill>\n\n${args}`;
  assert.ok(full.length > SESSION_LIST_FIRST_MESSAGE_MAX);

  const trimmed = trimSessionListFirstMessage({ ...base, firstMessage: full });
  assert.equal(trimmed.firstMessageTruncated, true);
  assert.equal(trimmed.firstMessage.length, SESSION_LIST_FIRST_MESSAGE_MAX);
  // The collapse needs the complete closing envelope, which the preview no
  // longer has — the display form is computed from the FULL text pre-cut.
  assert.equal(trimmed.firstMessageDisplay, `/skill:my-skill ${args}`);

  // The sidebar's fallback ordering (firstMessageDisplay first) reproduces
  // the same compact title from this row.
  const displayFirstMessage = trimmed.firstMessageDisplay
    ?? skillExpansionToCommand(trimmed.firstMessage)
    ?? trimmed.firstMessage;
  assert.equal(displayFirstMessage, `/skill:my-skill ${args}`);

  // A non-skill long message's display form is just the cut preview.
  const plain = trimSessionListFirstMessage({ ...base, firstMessage: "x".repeat(400) });
  assert.equal(plain.firstMessageDisplay, "x".repeat(SESSION_LIST_FIRST_MESSAGE_MAX));
});
