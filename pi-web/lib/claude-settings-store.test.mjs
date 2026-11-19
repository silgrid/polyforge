// carries: lib/claude-settings-store.ts, hooks/useClaudeSettings.ts, components/ClaudeSettings.tsx, components/GeneralSettings.tsx
// polyforge#149 (#85 85a1b, owner ruling Q5 A): the 界面与对话 pane and the
// legacy Claude page share one holder of Settings › Claude's values, so a save
// from either page merges onto the newest snapshot and PUTs the whole package,
// one save at a time — a stale page can never overwrite what the other just
// saved. The API and the file are unchanged; expected bodies follow the
// route's whole-package contract ({launchPermissionMode, customModels,
// includeHookEvents}), never this implementation's internals.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { createClaudeSettingsStore } = await jiti.import("./claude-settings-store.ts");
const { useClaudeSettings } = await jiti.import("../hooks/useClaudeSettings.ts");

const BASE = { launchPermissionMode: "default", customModels: [], includeHookEvents: false };

/** A fetch impl with programmable answers; PUTs are recorded with a gate per turn. */
function fakeFetch({ get = { ...BASE, planAvailable: true }, puts = [] } = {}) {
  const bodies = [];
  const gates = [];
  const impl = async (url, init) => {
    if (!init || init.method !== "PUT") {
      return new Response(JSON.stringify(get), { status: 200, headers: { "Content-Type": "application/json" } });
    }
    bodies.push(JSON.parse(String(init.body)));
    const gate = puts[bodies.length - 1] ?? {};
    if (gate.hold) await gate.hold;
    const answer = gate.answer ?? { ...(gate.base ? bodies.at(-1) : get), ...(gate.patch ?? {}), planAvailable: true };
    return new Response(JSON.stringify(answer), {
      status: gate.status ?? 200,
      headers: { "Content-Type": "application/json" },
    });
  };
  return { impl, bodies, gates };
}

test("loads the settings and planAvailable, and notifies its subscribers", async () => {
  const { impl } = fakeFetch();
  const store = createClaudeSettingsStore(impl);
  assert.equal(store.getSnapshot().status, "loading");
  const seen = [];
  const unsubscribe = store.subscribe(() => seen.push(store.getSnapshot()));
  await store.refresh();
  assert.deepEqual(seen.map((snapshot) => snapshot.status), ["ready"]);
  assert.deepEqual(store.getSnapshot().settings, BASE);
  assert.equal(store.getSnapshot().planAvailable, true);
  // A failed read surfaces as the error snapshot, keeping what was loaded.
  const failing = createClaudeSettingsStore(async () => new Response("nope", { status: 500 }));
  await failing.refresh();
  assert.equal(failing.getSnapshot().status, "error");
  assert.ok(failing.getSnapshot().loadError);
  unsubscribe();
});

test("save puts the whole package of the newest snapshot and applies the answer", async () => {
  const { impl, bodies } = fakeFetch({ puts: [{ patch: { includeHookEvents: true } }] });
  const store = createClaudeSettingsStore(impl);
  await store.refresh();
  const result = await store.save({ includeHookEvents: true });
  assert.equal(result.ok, true);
  assert.equal(bodies.length, 1, "one save is one PUT");
  assert.deepEqual(bodies[0], { launchPermissionMode: "default", customModels: [], includeHookEvents: true }, "the PUT must carry the whole package");
  assert.equal(store.getSnapshot().settings.includeHookEvents, true, "the answer is the new snapshot");
  // The snapshot object only changes when the state does.
  const before = store.getSnapshot();
  await store.refresh();
  assert.equal(store.getSnapshot(), before, "an unchanged refresh keeps the snapshot identity");
});

test("serialized saves share the latest result across pages", async () => {
  // 常规开 hook → 旧页改模型 → 回常规: the second save's PUT must carry the
  // first save's result — the queue merges onto the newest snapshot at its turn.
  let releaseFirst;
  const hold = new Promise((resolve) => { releaseFirst = resolve; });
  const { impl, bodies } = fakeFetch({ puts: [{ hold, patch: { includeHookEvents: true } }, { base: true, patch: { customModels: ["claude-custom-1"] } }] });
  const store = createClaudeSettingsStore(impl);
  await store.refresh();
  const hookSave = store.save({ includeHookEvents: true });
  const modelSave = store.save({ customModels: ["claude-custom-1"] });
  releaseFirst();
  const [hookResult, modelResult] = await Promise.all([hookSave, modelSave]);
  assert.equal(hookResult.ok, true);
  assert.equal(modelResult.ok, true);
  assert.equal(bodies[0].includeHookEvents, true);
  assert.deepEqual(bodies[1], { launchPermissionMode: "default", customModels: ["claude-custom-1"], includeHookEvents: true },
    "the second PUT dropped the first save's hook switch — the pages can overwrite each other again");
  assert.deepEqual(store.getSnapshot().settings.customModels, ["claude-custom-1"]);
  assert.equal(store.getSnapshot().settings.includeHookEvents, true);

  // The reverse order carries the other way around.
  const reverse = fakeFetch({ puts: [{ patch: { customModels: ["claude-custom-2"] } }, { base: true, patch: { includeHookEvents: true } }] });
  const second = createClaudeSettingsStore(reverse.impl);
  await second.refresh();
  await Promise.all([
    second.save({ customModels: ["claude-custom-2"] }),
    second.save({ includeHookEvents: true }),
  ]);
  assert.deepEqual(reverse.bodies[1], { launchPermissionMode: "default", customModels: ["claude-custom-2"], includeHookEvents: true },
    "the reverse order dropped the custom model");
});

test("a failed save keeps the last snapshot and answers its error", async () => {
  const { impl, bodies } = fakeFetch({ puts: [{ status: 400, answer: { error: "plan is not available" } }] });
  const store = createClaudeSettingsStore(impl);
  await store.refresh();
  const result = await store.save({ launchPermissionMode: "plan" });
  assert.deepEqual(result, { ok: false, error: "plan is not available" });
  assert.equal(store.getSnapshot().settings.launchPermissionMode, "default", "a refused save changed the snapshot");
  assert.equal(bodies.length, 1);
  // The queue survives a failure: the next save still runs.
  const after = await store.save({ includeHookEvents: true });
  assert.equal(after.ok, true);
  assert.equal(store.getSnapshot().settings.includeHookEvents, true);
});

test("the singleton and the hook face are the shared store both pages use", async () => {
  const { claudeSettingsStore } = await jiti.import("./claude-settings-store.ts");
  for (const key of ["subscribe", "getSnapshot", "refresh", "save"]) {
    assert.equal(typeof claudeSettingsStore[key], "function", `the singleton store lacks ${key}`);
  }
  // The hook wraps the store: its module exists and names the singleton's face.
  assert.equal(typeof useClaudeSettings, "function");
  const source = await jiti.import("../hooks/useClaudeSettings.ts", { source: true }) ?? "";
  assert.match(String(source), /useSyncExternalStore/, "the hook must subscribe through useSyncExternalStore");
  assert.match(String(source), /claudeSettingsStore/, "the hook must default to the shared singleton");
});
