// carries: lib/model-page-draft.ts
// polyforge#258 (T3) AC2: the default model and per-model reasoning are a
// draft until 「保存（N）」; each landed write clears only its own entry, and a
// failure keeps the rest. Expected request bodies come from the routes'
// published contracts (/api/models/default and /api/harness/pi/model), not
// from the component's output.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const m = await jiti.import("./model-page-draft.ts");

test("the model page draft changes nothing until it is written", () => {
  const empty = m.emptyDraft();
  assert.equal(m.draftDirtyCount(empty), 0);
  assert.deepEqual(m.draftWrites(empty, null), [], "no draft, no request");

  const piDefault = m.setDraftDefaultModel(empty, { harness: "pi", provider: "gw", id: "one" });
  assert.equal(m.draftDirtyCount(piDefault), 1);
  const withEffort = m.setDraftEffort(piDefault, "gw/one", "high");
  assert.equal(m.draftDirtyCount(withEffort), 2);
  assert.deepEqual(empty, { defaultModel: null, efforts: {} }, "the setters return a new draft, never mutate the old one");

  assert.deepEqual(m.draftWrites(piDefault, null), [
    { kind: "default", url: "/api/models/default", body: { provider: "gw", modelId: "one" } },
  ]);
  assert.deepEqual(m.draftWrites(withEffort, "/project"), [
    { kind: "default", url: "/api/models/default", body: { provider: "gw", modelId: "one", cwd: "/project" } },
    { kind: "effort", key: "gw/one", url: "/api/harness/pi/model", body: { modelThinkingLevel: { key: "gw/one", level: "high" } } },
  ], "the default lands before the per-model levels; the cwd rides the default write");

  assert.deepEqual(m.draftWrites(m.setDraftDefaultModel(empty, { harness: "claude", provider: "anthropic", id: "opus" }), null), [
    { kind: "default", url: "/api/models/default", body: { harness: "claude", model: "opus" } },
  ], "a claude default writes its harness and model name, never a provider");

  assert.deepEqual(m.draftWrites(m.setDraftEffort(empty, "gw/one", ""), null), [
    { kind: "effort", key: "gw/one", url: "/api/harness/pi/model", body: { modelThinkingLevel: { key: "gw/one", level: null } } },
  ], "「跟随全局」 clears the per-model level with null");
});

test("a landed write clears only its own draft entry", () => {
  const draft = m.setDraftEffort(
    m.setDraftDefaultModel(m.emptyDraft(), { harness: "pi", provider: "gw", id: "one" }),
    "gw/one", "high");
  const [defaultWrite, effortWrite] = m.draftWrites(draft, null);
  const afterDefault = m.settleWrite(draft, defaultWrite);
  assert.equal(afterDefault.defaultModel, null, "the saved default leaves the draft");
  assert.deepEqual(afterDefault.efforts, { "gw/one": "high" }, "an unsaved level stays for the next save");
  const afterBoth = m.settleWrite(afterDefault, effortWrite);
  assert.equal(m.draftDirtyCount(afterBoth), 0);
});
