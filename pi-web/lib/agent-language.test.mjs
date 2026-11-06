import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const language = await jiti.import("./agent-language.ts");

// The spec's `## Fixed Chinese instruction`, byte-exact.
const SPEC_INSTRUCTION = "Use Simplified Chinese (简体中文) for replies, narration and reports. Keep code, code comments, commit messages, files you write, technical terms and identifiers as they are, or as the project's conventions require. Thinking in Chinese is best effort only.";

test("the instruction is the spec's fixed text and names its boundaries", () => {
  assert.equal(language.AGENT_LANGUAGE_INSTRUCTION, SPEC_INSTRUCTION);
  for (const surface of ["replies, narration and reports", "code, code comments, commit messages, files you write, technical terms and identifiers", "project's conventions", "Thinking in Chinese is best effort only."]) {
    assert.ok(language.AGENT_LANGUAGE_INSTRUCTION.includes(surface), surface);
  }
});

test("absent, corrupt or unknown values normalize to English", () => {
  assert.deepEqual(language.AGENT_LANGUAGES, ["en", "zh-CN"]);
  assert.equal(language.AGENT_LANGUAGE_DEFAULT, "en");
  for (const value of [undefined, null, "", "zh", "klingon", 1, {}, ["zh-CN"]]) {
    assert.equal(language.normalizeAgentLanguage(value), "en", String(value));
  }
  assert.equal(language.normalizeAgentLanguage("zh-CN"), "zh-CN");
  assert.equal(language.normalizeAgentLanguage("en"), "en");
});

test("English carries no instruction and leaves a prompt byte-identical", () => {
  assert.equal(language.agentLanguageInstruction("en"), undefined);
  assert.equal(language.agentLanguageInstruction(undefined), undefined);
  assert.equal(language.agentLanguageInstruction("zh-CN"), SPEC_INSTRUCTION);
  assert.equal(language.appendAgentLanguageInstruction("EXACT", "en"), "EXACT");
  assert.equal(language.appendAgentLanguageInstruction("EXACT", undefined), "EXACT");
  assert.equal(language.appendAgentLanguageInstruction("EXACT", "zh-CN"), `EXACT\n\n${SPEC_INSTRUCTION}`);
});
