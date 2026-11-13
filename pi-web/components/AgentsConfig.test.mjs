import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import ts from "typescript";
import vm from "node:vm";

const source = await readFile(new URL("./AgentsConfig.tsx", import.meta.url), "utf8");

test("editor draft preserves named and empty selections independently of activation", () => {
  const declaration = source.slice(source.indexOf("function editableProfile("), source.indexOf("function profileKey("));
  const code = ts.transpileModule(declaration, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText;
  const editable = vm.runInNewContext(`${code}; editableProfile`);
  for (const skills of [["review", "audit"], []]) {
    const draft = editable({ name: "reviewer", tools: [], loadSkills: false, skills });
    assert.equal(draft.loadSkills, false);
    assert.deepEqual(Array.from(draft.skills ?? ["LOST"]), skills);
    draft.skills.push("new");
    assert.notEqual(draft.skills.length, skills.length);
  }
  for (const extensions of [["codegraph"], []]) {
    const draft = editable({ name: "reviewer", tools: [], loadExtensions: true, extensions });
    assert.deepEqual(Array.from(draft.extensions ?? ["LOST"]), extensions);
  }
});

test("draft backfills MCP as off and keeps an explicit choice", () => {
  const declaration = source.slice(source.indexOf("function editableProfile("), source.indexOf("function profileKey("));
  const code = ts.transpileModule(declaration, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText;
  const editable = vm.runInNewContext(`${code}; editableProfile`);
  assert.equal(editable({ name: "legacy", tools: [], loadSkills: false, loadExtensions: false }).mcp, "off");
  for (const mcp of ["on", "off"]) {
    assert.equal(editable({ name: "reviewer", tools: [], loadSkills: false, loadExtensions: false, mcp }).mcp, mcp);
  }
});

test("draft backfills code mode for legacy profiles and keeps an explicit choice", () => {
  const declaration = source.slice(source.indexOf("function editableProfile("), source.indexOf("function profileKey("));
  const code = ts.transpileModule(declaration, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText;
  const editable = vm.runInNewContext(`${code}; editableProfile`);
  // Profiles written before the setting existed have no value: code mode stays disabled.
  assert.equal(editable({ name: "legacy", tools: [], loadSkills: false, loadExtensions: false }).codemode, "off");
  // An explicit choice survives the draft, so duplicating a profile keeps it too.
  for (const codemode of ["on", "off", "inherit"]) {
    const draft = editable({ name: "reviewer", tools: [], loadSkills: false, loadExtensions: false, codemode });
    assert.equal(draft.codemode, codemode);
  }
});

test("a profile model's pi :level suffix names its base model; a listed colon id matches whole", () => {
  const constant = source.slice(source.indexOf("const THINKING_OPTIONS"), source.indexOf("\n", source.indexOf("const THINKING_OPTIONS")));
  const declaration = source.slice(source.indexOf("function profileModelBase("), source.indexOf("function profileKey("));
  const code = ts.transpileModule(`${constant}\n${declaration}`, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText;
  const base = vm.runInNewContext(`${code}; profileModelBase`);
  const listed = new Set(["openai/gpt-5", "ollama/qwen3:8b", "local/tuned:high"]);
  const isListed = (reference) => listed.has(reference);
  assert.equal(base("openai/gpt-5:high", isListed), "openai/gpt-5");
  assert.equal(base("openai/gpt-5", isListed), "openai/gpt-5");
  assert.equal(base("ollama/qwen3:8b", isListed), "ollama/qwen3:8b");
  assert.equal(base("local/tuned:high", isListed), "local/tuned:high");
  assert.equal(base("openai/gpt-5:turbo", isListed), "openai/gpt-5:turbo");
  assert.equal(base("openai/gpt-5:", isListed), "openai/gpt-5:");
});
