import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { createAgentLanguageExtension } = await jiti.import("./agent-language-prompt.ts");
const { AGENT_LANGUAGE_INSTRUCTION } = await jiti.import("./agent-language.ts");

function handlerFor(language) {
  const handlers = [];
  const extension = createAgentLanguageExtension(() => language.current);
  extension.factory({ on: (event, handler) => handlers.push([event, handler]) });
  assert.equal(extension.hidden, true);
  assert.deepEqual(handlers.map(([event]) => event), ["before_agent_start"]);
  return handlers[0][1];
}

test("a 中文 session's run prompt gets the instruction after one blank line", () => {
  const handler = handlerFor({ current: "zh-CN" });
  assert.deepEqual(handler({ systemPrompt: "PI PROMPT", prompt: "user text" }), {
    systemPrompt: `PI PROMPT\n\n${AGENT_LANGUAGE_INSTRUCTION}`,
  });
});

test("an English session changes nothing", () => {
  const language = { current: "en" };
  const handler = handlerFor(language);
  assert.equal(handler({ systemPrompt: "PI PROMPT", prompt: "user text" }), undefined);
  // The language is read on every run.
  language.current = "zh-CN";
  assert.ok(handler({ systemPrompt: "PI PROMPT" }).systemPrompt.endsWith(AGENT_LANGUAGE_INSTRUCTION));
});
