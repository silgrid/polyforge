import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { SessionManager } from "@earendil-works/pi-coding-agent";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const {
  SESSION_AGENT_LANGUAGE_TYPE,
  appendSessionAgentLanguage,
  readSessionAgentLanguage,
} = await jiti.import("./session-agent-language.ts");

const custom = (data, customType = SESSION_AGENT_LANGUAGE_TYPE) => ({ type: "custom", customType, data, id: "c", parentId: null, timestamp: "" });

test("a session without a valid entry reads as English", () => {
  assert.equal(SESSION_AGENT_LANGUAGE_TYPE, "pi-web:agent-language");
  assert.equal(readSessionAgentLanguage([]), "en");
  assert.equal(readSessionAgentLanguage([
    custom({ version: 1, language: "klingon" }),
    custom({ version: 2, language: "zh-CN" }),
    custom(null),
    custom({ version: 1, language: "zh-CN" }, "pi-web:tool-selection"),
    { type: "message", message: { role: "user", content: "hi" } },
  ]), "en");
});

test("the newest valid entry wins, and a later compaction entry does not hide it", () => {
  assert.equal(readSessionAgentLanguage([custom({ version: 1, language: "zh-CN" })]), "zh-CN");
  assert.equal(readSessionAgentLanguage([
    custom({ version: 1, language: "en" }),
    custom({ version: 1, language: "zh-CN" }),
    custom({ version: 1, language: "bogus" }),
    { type: "compaction", summary: "s", firstKeptEntryId: "x", tokensBefore: 1 },
    { type: "message", message: { role: "user", content: "hi" } },
  ]), "zh-CN");
});

test("append writes a versioned custom entry a session manager reads back", () => {
  const manager = SessionManager.inMemory("/work");
  appendSessionAgentLanguage(manager, "zh-CN");
  const entries = manager.getEntries();
  const entry = entries.at(-1);
  assert.equal(entry.type, "custom");
  assert.equal(entry.customType, "pi-web:agent-language");
  assert.deepEqual(entry.data, { version: 1, language: "zh-CN" });
  assert.equal(readSessionAgentLanguage(entries), "zh-CN");
});
