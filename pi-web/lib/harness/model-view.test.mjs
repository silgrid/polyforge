import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { buildModelRows } from "./model-view.ts";

const models = [
  { harness: "pi", provider: "openai", id: "gpt-6", name: "GPT 6", chatAvailable: true, reasoning: "high" },
  { harness: "claude", provider: "anthropic", id: "claude-opus", name: "Opus", chatAvailable: false, reasoning: "adaptive" },
  { harness: "pi", provider: "openai", id: "gpt-6", name: "GPT 6", chatAvailable: false, reasoning: "low" },
  { harness: "pi", provider: "gateway", id: "gpt-6", name: "Gateway GPT 6", chatAvailable: true, reasoning: "medium" },
];

test("provider identities preserve a default from either harness without alias merging", () => {
  const rows = buildModelRows([
    { harness: "pi", provider: "anthropic", id: "opus", name: "Opus", chatAvailable: true, reasoning: "low" },
    { harness: "claude", provider: "anthropic", id: "opus", name: "Opus", chatAvailable: false, reasoning: "high" },
    { harness: "pi", provider: "gateway", id: "opus", name: "Opus", chatAvailable: false },
    { harness: "claude", provider: "anthropic", id: "alias", name: "Opus", chatAvailable: false },
  ], { harness: "claude", provider: "anthropic", id: "opus" });
  assert.equal(rows.length, 3);
  const merged = rows.find((row) => row.provider === "anthropic" && row.id === "opus");
  assert.equal(merged.isDefault, true);
  assert.deepEqual(merged.harnesses, ["pi", "claude"]);
  assert.deepEqual(merged.reasoning, ["low", "high"]);
  assert.equal(merged.chatAvailable, true);
  assert.equal(rows.find((row) => row.provider === "gateway").isDefault, false);
  assert.equal(rows.find((row) => row.id === "alias").chatAvailable, false);
});

test("按 provider 合并精确 id，并保留 Claude 不支持聊天", () => {
  const rows = buildModelRows(models, { harness: "pi", provider: "openai", id: "gpt-6" });
  assert.equal(rows.length, 3);
  assert.deepEqual(rows[0], {
    provider: "anthropic", id: "claude-opus", name: "Opus", harnesses: ["claude"],
    chatAvailable: false, reasoning: ["adaptive"], isDefault: false,
  });
  assert.deepEqual(rows[1], {
    provider: "gateway", id: "gpt-6", name: "Gateway GPT 6", harnesses: ["pi"],
    chatAvailable: true, reasoning: ["medium"], isDefault: false,
  });
  assert.deepEqual(rows[2], {
    provider: "openai", id: "gpt-6", name: "GPT 6", harnesses: ["pi"],
    chatAvailable: true, reasoning: ["high", "low"], isDefault: true,
  });
});
