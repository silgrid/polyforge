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

const current = { provider: "fixture", modelId: "fake-model", name: "Fake Model" };
const other = { provider: "fixture", modelId: "fake-model-2", name: "Fake Model 2" };
const options = [current, other];

// ModelSelector's own `saveDefault` (the row star's onSave), evaluated from the
// source the way the session-switch test evaluates `choose`, but fed the
// handlers the gate returns.
function extractSaveDefault() {
  const source = ts.createSourceFile("ModelSelector.tsx", readFileSync(new URL("./ModelSelector.tsx", import.meta.url), "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  function find(node) {
    if (ts.isVariableDeclaration(node) && node.name.getText(source) === "saveDefault") return node.initializer;
    return ts.forEachChild(node, find);
  }
  return new Script(ts.transpileModule(find(source).getText(source), {
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
      onSetDefault: (provider, modelId) => calls.push(["default", provider, modelId]),
    },
    {
      on,
      from: current.name,
      current: { provider: current.provider, modelId: current.modelId },
      options,
      ask: (from, to) => {
        asked.push([from, to]);
        return answer;
      },
    },
  );
  const saveDefault = extractSaveDefault().runInNewContext({
    setOpen: () => {},
    setFilter: () => {},
    onSetDefault: handlers.onSetDefault,
  });
  return { saveDefault, calls, asked };
}

describe("a phone confirms a starred model switch", () => {
  test("confirming a star on another model saves it once", () => {
    const { saveDefault, calls, asked } = harness({ on: true, answer: true });
    saveDefault(other);
    assert.deepEqual(asked, [[current.name, other.name]]);
    assert.deepEqual(calls, [["default", other.provider, other.modelId]]);
  });

  test("cancelling a star on another model saves nothing", () => {
    const { saveDefault, calls, asked } = harness({ on: true, answer: false });
    saveDefault(other);
    assert.deepEqual(asked, [[current.name, other.name]]);
    assert.deepEqual(calls, []);
  });

  test("a star on the current model asks nothing", () => {
    const { saveDefault, calls, asked } = harness({ on: true, answer: false });
    saveDefault(current);
    assert.deepEqual(asked, []);
    assert.deepEqual(calls, [["default", current.provider, current.modelId]]);
  });

  test("a desktop star saves at once", () => {
    const { saveDefault, calls, asked } = harness({ on: false, answer: false });
    saveDefault(other);
    assert.deepEqual(asked, []);
    assert.deepEqual(calls, [["default", other.provider, other.modelId]]);
  });
});
