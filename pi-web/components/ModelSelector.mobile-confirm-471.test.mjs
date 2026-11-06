import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { describe, test } from "node:test";
import { Script } from "node:vm";
import { createJiti } from "jiti";
import ts from "typescript";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const { gateModelSwitch } = await jiti.import("./ModelSelector.tsx");
const { interpolateMessage } = await jiti.import("@/lib/i18n/format");
const { enLocale } = await jiti.import("@/lib/i18n/messages/en");
const { zhCNLocale } = await jiti.import("@/lib/i18n/messages/zh-CN");
const { zhTWLocale } = await jiti.import("@/lib/i18n/messages/zh-TW");

const current = { provider: "fixture", modelId: "claude-opus-5-5", name: "claude-opus-5-5" };
const other = { provider: "fixture", modelId: "claude-fable-5-1", name: "claude-fable-5-1" };
const options = [current, other];

// ModelSelector's own `choose`, evaluated as ChatInput.test.mjs evaluates it,
// but fed the handlers the gate returns.
function extractChoose() {
  const source = ts.createSourceFile("ModelSelector.tsx", readFileSync(new URL("./ModelSelector.tsx", import.meta.url), "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  function findChoose(node) {
    if (ts.isVariableDeclaration(node) && node.name.getText(source) === "choose") return node.initializer;
    return ts.forEachChild(node, findChoose);
  }
  return new Script(ts.transpileModule(findChoose(source).getText(source), {
    compilerOptions: { target: ts.ScriptTarget.ES2020 },
  }).outputText);
}

function harness({ on, answer }) {
  const calls = [];
  const asked = [];
  const handlers = gateModelSwitch(
    {
      onChange: (provider, modelId) => calls.push(["change", provider, modelId]),
      onReselect: (provider, modelId) => calls.push(["reselect", provider, modelId]),
    },
    {
      on,
      from: current.name,
      options,
      ask: (from, to) => {
        asked.push([from, to]);
        return answer;
      },
    },
  );
  const choose = extractChoose().runInNewContext({
    value: { provider: current.provider, modelId: current.modelId },
    isAutoSelection: false,
    setOpen: () => {},
    setFilter: () => {},
    onChange: handlers.onChange,
    onReselect: handlers.onReselect,
  });
  return { choose, calls, asked };
}

describe("a phone confirms a session model switch", () => {
  test("confirming another model sends one switch with the chosen model", () => {
    const { choose, calls, asked } = harness({ on: true, answer: true });
    choose(other);
    assert.deepEqual(asked, [[current.name, other.name]]);
    assert.deepEqual(calls, [["change", other.provider, other.modelId]]);
  });

  test("cancelling sends nothing and keeps the model", () => {
    const { choose, calls, asked } = harness({ on: true, answer: false });
    choose(other);
    assert.deepEqual(asked, [[current.name, other.name]]);
    assert.deepEqual(calls, []);
  });

  test("choosing the current model asks nothing and sends nothing", () => {
    const { choose, calls, asked } = harness({ on: true, answer: true });
    choose(current);
    assert.deepEqual(asked, []);
    assert.deepEqual(calls, []);
  });

  test("the confirmation reads from <current> to <new> in every locale", () => {
    const params = { from: current.name, to: other.name };
    const message = (locale) => {
      const text = locale.messages["chat.confirmModelSwitch"];
      assert.equal(typeof text, "string", `${locale.id} has chat.confirmModelSwitch`);
      return interpolateMessage(text, params);
    };
    assert.equal(message(zhCNLocale), "从 claude-opus-5-5 切换到 claude-fable-5-1？");
    for (const locale of [enLocale, zhTWLocale]) {
      const text = message(locale);
      assert.ok(text.indexOf(current.name) >= 0 && text.indexOf(current.name) < text.indexOf(other.name), `${locale.id}: ${text}`);
    }
  });

  test("a desktop selector switches at once and keeps the reselect", () => {
    const { choose, calls, asked } = harness({ on: false, answer: false });
    choose(other);
    choose(current);
    assert.deepEqual(asked, []);
    assert.deepEqual(calls, [
      ["change", other.provider, other.modelId],
      ["reselect", current.provider, current.modelId],
    ]);
  });
});
