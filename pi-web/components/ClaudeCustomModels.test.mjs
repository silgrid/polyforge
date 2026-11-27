// carries: components/ClaudeCustomModels.tsx, components/HarnessModelSettings.tsx, lib/i18n/messages/en.ts, lib/i18n/messages/zh-CN.ts, lib/i18n/messages/zh-TW.ts
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import test from "node:test";
import { createJiti } from "jiti";

// Behavioural coverage of the models page's Claude section (85a3-3), on the
// react-hook shim (hooks/__fixtures__/react-hook-shim.mjs): entering the
// section reads the list; an explicit check fires the protected migration
// POST — a preview shows the
// merge (the native rows kept, the missing names appended, nothing written
// until「合并」, a cancel writes nothing), a done answer reloads the list,
// and a blocked/deferred reason is visible text. The editor writes every
// add and remove as the full next list through the adapter's PUT, a refusal
// is visible, and the managed-shadowed「已保存、未生效」note shows. The test,
// never the network, decides when each read settles; expected values are
// hand-derived from the owner rulings (Q2/Q3/Q6), never from the component's
// output. `t` returns the key, so the assertions name i18n keys.
const fixture = (name) => fileURLToPath(new URL(`../hooks/__fixtures__/${name}`, import.meta.url));
const shimPath = fixture("react-hook-shim.mjs");
const jsxRuntimePath = fixture("react-jsx-runtime.mjs");
const stubsPath = path.join(fs.mkdtempSync("/tmp/pf178-clcm-stub-"), "stubs.mjs");
fs.writeFileSync(stubsPath, `const t = (key) => key;
const i18n = { t, locale: "en" };
export function useI18n() { return i18n; }
export function useSettingsDraft() { return false; }
let references = [];
export function setReferences(next) { references = next; }
export async function modelReferenceImpact() { return references; }
`);
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  alias: {
    react: shimPath,
    "react/jsx-runtime": jsxRuntimePath,
    "@/hooks/useI18n": stubsPath,
    "@/lib/model-reference-impact": stubsPath,
    "./SettingsDrafts": stubsPath,
  },
});
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { setReferences } = await jiti.import(stubsPath);
const { ClaudeCustomModels } = await jiti.import("./ClaudeCustomModels.tsx");

/** The element tree as the JSX an automatic runtime emits, function components expanded in place. */
function walk(node, out = []) {
  if (Array.isArray(node)) { for (const child of node) walk(child, out); return out; }
  if (!node || typeof node !== "object" || !("props" in node)) return out;
  if (typeof node.type === "function") return walk(node.type(node.props), out);
  out.push(node);
  walk(node.props.children, out);
  return out;
}
const nodes = (hook) => walk(hook.result);
const textOf = (hook) => nodes(hook).map((node) => {
  const children = node.props.children;
  if (typeof children === "string") return children;
  if (Array.isArray(children)) return children.filter((child) => typeof child === "string").join(" ");
  return "";
});
const findButton = (hook, match) => nodes(hook).find((node) => node.type === "button" && textOf({ result: node }).join("").includes(match))?.props
  ?? nodes(hook).find((node) => node.type === "button" && JSON.stringify(node.props).includes(match))?.props;
const byAttr = (hook, attr) => nodes(hook).filter((node) => node.type !== "function" && attr in node.props);
/** The remove button of one listed custom model's row. */
const removeButtonOf = (hook, model) => nodes(hook)
  .filter((node) => node.type === "li")
  .find((row) => JSON.stringify(row.props).includes(model))
  && nodes(hook)
    .filter((node) => node.type === "li" && JSON.stringify(node.props).includes(model))
    .flatMap((row) => walk(row.props.children))
    .find((node) => node.type === "button")?.props;

const settle = async () => {
  for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve));
};

/** A fetch recorder: the test settles each request in order, never the network. */
function setup() {
  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = (url, init) => {
    const request = { url, init, settled: false };
    request.promise = new Promise((resolve) => { request.resolve = resolve; });
    requests.push(request);
    return request.promise;
  };
  const refreshes = [];
  const hook = renderHook(ClaudeCustomModels, { onRefresh: () => refreshes.push(refreshes.length) });
  const answer = async (index, body, status = 200) => {
    await settle();
    const request = requests[index];
    assert.ok(request && !request.settled, `request ${index} is open (${requests.length} made)`);
    request.settled = true;
    request.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
    await settle();
    hook.flush();
  };
  const requestBody = (index) => JSON.parse(String(requests[index].init?.body ?? "{}"));
  return {
    hook,
    requests,
    refreshes,
    answer,
    requestBody,
    restore() { globalThis.fetch = originalFetch; hook.unmount(); },
  };
}

const modelPayload = (customModels, userCustomModels = customModels) => ({ harness: "claude", concept: "model", data: { customModels, userCustomModels } });
const PREVIEW = {
  status: "preview",
  native: [{ model: "native-keep", label: "K" }],
  missing: ["claude-old-1"],
  digest: "d".repeat(64),
  shadowed: false,
};

test("explicit migration check previews kept native rows and missing names; merge confirms", async () => {
  const state = setup();
  try {
    await state.answer(0, modelPayload(["native-keep"]));
    findButton(state.hook, "models.claudeCustomCheckMigration").onClick();
    await state.answer(1, PREVIEW);
    assert.equal(state.requests.length, 2, "the mount fired the POST and the list read");
    assert.equal(state.requests[0].url, "/api/harness/claude/model");
    assert.equal(state.requests[1].url, "/api/claude-models-migration");
    assert.deepEqual(state.requestBody(1), {}, "the first POST carries no confirm");
    const banner = byAttr(state.hook, "data-claude-migration-preview");
    assert.equal(banner.length, 1);
    const text = textOf(state.hook).join("\n");
    assert.ok(text.includes("models.claudeCustomPreviewTitle"));
    assert.ok(text.includes("models.claudeCustomNativeKept"));
    assert.ok(text.includes("native-keep (K)"), "the native row keeps its label");
    assert.ok(text.includes("models.claudeCustomMissing"));
    assert.ok(text.includes("claude-old-1"));
    // Nothing was written by the preview: only the migration POST and the read.
    assert.ok(state.requests.every((request) => request.init?.method !== "PUT"), "the preview wrote something");

    const merge = findButton(state.hook, "models.claudeCustomMerge");
    assert.ok(merge, "the merge button is on screen");
    merge.onClick();
    await state.answer(2, { status: "done", changed: true, shadowed: false });
    await state.answer(3, modelPayload(["native-keep", "claude-old-1"]));
    assert.equal(state.requests[2].url, "/api/claude-models-migration");
    assert.deepEqual(state.requestBody(2), { confirm: PREVIEW.digest });
    assert.equal(byAttr(state.hook, "data-claude-migration-preview").length, 0, "the banner stayed after the merge");
    assert.equal(state.refreshes.length, 1, "the merge did not refresh the page");
  } finally {
    state.restore();
  }
});

test("a cancel dismisses the preview without any write", async () => {
  const state = setup();
  try {
    await state.answer(0, modelPayload(["native-keep"]));
    findButton(state.hook, "models.claudeCustomCheckMigration").onClick();
    await state.answer(1, PREVIEW);
    const cancel = findButton(state.hook, "common.cancel");
    assert.ok(cancel, "the cancel button is on screen");
    cancel.onClick();
    state.hook.flush();
    assert.equal(byAttr(state.hook, "data-claude-migration-preview").length, 0);
    assert.ok(state.requests.every((request) => request.init?.method !== "PUT"), "the cancel wrote something");
    assert.equal(state.requests.filter((request) => request.url === "/api/claude-models-migration").length, 1, "the cancel re-POSTed");
  } finally {
    state.restore();
  }
});

test("the editor writes the full next list per add and remove, and a refusal is visible", async () => {
  const state = setup();
  try {
    findButton(state.hook, "models.claudeCustomCheckMigration").onClick();
    await state.answer(1, { status: "done", changed: false, shadowed: false });
    await state.answer(0, modelPayload(["claude-a", "claude-b"]));
    assert.ok(textOf(state.hook).join("\n").includes("claude-a"));
    // An unchanged explicit migration check needs no catalog refresh.
    assert.equal(state.refreshes.length, 0, "an unchanged migration needs no refresh");
    // Remove claude-a: the PUT carries the full next list.
    const remove = removeButtonOf(state.hook, "claude-a");
    assert.ok(remove, "the remove button is on screen");
    remove.onClick();
    await settle();
    await state.answer(2, { harness: "claude", concept: "model", result: { status: "written", summary: "modelPicker.options = claude-b" } }, 200);
    assert.equal(state.requests[2].init.method, "PUT");
    assert.equal(state.requests[2].url, "/api/harness/claude/model");
    assert.deepEqual(state.requestBody(2), { customModels: ["claude-b"] });
    // A successful write owns its state: no extra read, only the page refresh.
    assert.equal(state.requests.length, 3, "the edit re-read the list it already knows");
    assert.equal(state.refreshes.length, 1, "the edit refreshes the page");
    // A refused write keeps the list and shows the reason.
    const refused = removeButtonOf(state.hook, "claude-b");
    assert.ok(refused, "the second remove button is on screen");
    refused.onClick();
    await settle();
    await state.answer(3, { error: "write-refused", message: "claude 2.1.200 is below the registered minimum 2.1.242 for modelPicker" }, 400);
    assert.ok(textOf(state.hook).join("\n").includes("below the registered minimum"), "the refusal reason is missing");
    assert.ok(textOf(state.hook).join("\n").includes("claude-b"), "a refused remove dropped the row");
    // Add a name through the input.
    const input = byAttr(state.hook, "aria-label").find((node) => node.type === "input")?.props;
    input.onChange({ target: { value: "claude-c" } });
    state.hook.flush();
    const add = findButton(state.hook, "models.claudeCustomAdd");
    add.onClick();
    await state.answer(4, { harness: "claude", concept: "model", result: { status: "written" } });
    assert.equal(state.requests[4].init.method, "PUT");
    // claude-a went with the successful remove; the refused one kept claude-b.
    assert.deepEqual(state.requestBody(4), { customModels: ["claude-b", "claude-c"] });
  } finally {
    state.restore();
  }
});

test("referenced Claude model removal stays visible and sends no PUT (#285)", async () => {
  const state = setup();
  try {
    await state.answer(0, modelPayload(["claude-a"]));
    findButton(state.hook, "models.claudeCustomCheckMigration").onClick();
    await state.answer(1, { status: "done", changed: false });
    setReferences(["default · claude:claude-a", "Host · fast · claude:claude-a:high"]);
    removeButtonOf(state.hook, "claude-a").onClick();
    await settle(); state.hook.flush();
    assert.equal(state.requests.length, 2, "a referenced removal must stop before PUT");
    assert.ok(textOf(state.hook).includes("models.referenceBlocked"));
    assert.ok(textOf(state.hook).join("\n").includes("claude-a"));
  } finally { setReferences([]); state.restore(); }
});

test("a 409 conflict replaces the banner with the fresh preview — no error, the next「合并」carries the new digest (r2#1)", async () => {
  const state = setup();
  try {
    await state.answer(0, modelPayload(["native-keep"]));
    findButton(state.hook, "models.claudeCustomCheckMigration").onClick();
    await state.answer(1, PREVIEW);
    const merge = findButton(state.hook, "models.claudeCustomMerge");
    merge.onClick();
    // The source or target changed while the user looked: the confirm answers
    // 409 with the fresh preview riding along.
    await state.answer(2, {
      status: "conflict",
      native: [{ model: "native-keep" }, { model: "user-added" }],
      missing: ["claude-old-1", "claude-late-2"],
      digest: "a".repeat(64),
      shadowed: false,
    }, 409);
    const text = textOf(state.hook).join("\n");
    assert.ok(text.includes("claude-late-2"), "the banner kept the stale preview");
    assert.ok(text.includes("user-added"), "the fresh native rows are missing");
    assert.ok(!text.includes("409"), `the conflict surfaced as an error: ${text}`);
    assert.equal(byAttr(state.hook, "data-claude-migration-preview").length, 1, "the banner closed on the conflict");
    // The next confirm carries the fresh digest, not the stale one.
    findButton(state.hook, "models.claudeCustomMerge").onClick();
    await state.answer(3, { status: "done", changed: true, shadowed: false });
    await state.answer(4, modelPayload(["native-keep", "user-added", "claude-old-1", "claude-late-2"]));
    assert.deepEqual(state.requestBody(3), { confirm: "a".repeat(64) });
    assert.equal(byAttr(state.hook, "data-claude-migration-preview").length, 0, "the banner stayed after the fresh confirm");
  } finally {
    state.restore();
  }
});

test("the editor lists only the user layer's own names — a managed picker never enters the PUT list (r1#1)", async () => {
  const state = setup();
  try {
    await state.answer(0, modelPayload(["managed-only", "user-own"], ["user-own"]));
    findButton(state.hook, "models.claudeCustomCheckMigration").onClick();
    await state.answer(1, { status: "done", changed: false, shadowed: true });
    const text = textOf(state.hook).join("\n");
    assert.ok(text.includes("user-own"), "the user layer's name is missing from the editor");
    assert.ok(!text.includes("managed-only"), "a managed name entered the editor's list");
    // Remove the user layer's one name: the PUT carries the user layer's own
    // next state, never the merged view.
    const remove = removeButtonOf(state.hook, "user-own");
    assert.ok(remove, "the remove button is on screen");
    remove.onClick();
    await state.answer(2, { harness: "claude", concept: "model", result: { status: "written" } });
    assert.deepEqual(state.requestBody(2), { customModels: [] });
    // Add one back: still the user layer alone.
    const input = byAttr(state.hook, "aria-label").find((node) => node.type === "input")?.props;
    input.onChange({ target: { value: "fresh-user-name" } });
    state.hook.flush();
    findButton(state.hook, "models.claudeCustomAdd").onClick();
    await state.answer(3, { harness: "claude", concept: "model", result: { status: "written" } });
    assert.deepEqual(state.requestBody(3), { customModels: ["fresh-user-name"] });
  } finally {
    state.restore();
  }
});

test("a blocked migration keeps its reason visible, and the managed-shadowed note shows「已保存、未生效」", async () => {
  const state = setup();
  try {
    await state.answer(0, modelPayload([]));
    findButton(state.hook, "models.claudeCustomCheckMigration").onClick();
    await state.answer(1, { status: "blocked", reason: "the retired customModels entry is not an array", shadowed: true });
    const text = textOf(state.hook).join("\n");
    assert.ok(text.includes("models.claudeCustomBlocked"), "the blockage prefix is missing");
    assert.ok(text.includes("the retired customModels entry is not an array"), "the blockage reason is missing");
    assert.ok(text.includes("models.claudeCustomShadowed"), "the shadowed note is missing");
    assert.ok(text.includes("models.claudeCustomEmpty"));
  } finally {
    state.restore();
  }
});

// polyforge#271 AC1 (A4): Claude's custom-model section has three states — the
// first read is a skeleton, a successful empty list is 「没有自定义模型」 with
// the add action, and a failed read is an alert with a retry (never the empty
// list). The state block takes `t`, so it renders without a hook context.
test("editor read states preserve drafts", async () => {
  const { ClaudeCustomReadStates } = await jiti.import("./ClaudeCustomModels.tsx");
  const t = (key) => key;
  const texts = (tree) => walk(tree).flatMap((node) => {
    const children = node.props?.children;
    return Array.isArray(children) ? children.filter((child) => typeof child === "string") : typeof children === "string" ? [children] : [];
  });
  const keys = (tree) => walk(tree).map((node) => node.props?.["data-config-skeleton-line"] !== undefined ? "data-config-skeleton-line" : node.props?.["data-claude-models-retry"] !== undefined ? "data-claude-models-retry" : node.props?.["data-claude-models-add"] !== undefined ? "data-claude-models-add" : null).filter(Boolean);
  const pending = ClaudeCustomReadStates({ loading: true, readError: null, t, onRetry: () => {} });
  assert.ok(keys(pending).includes("data-config-skeleton-line"), "the first custom-model read is a skeleton");
  assert.ok(!texts(pending).some((text) => text.includes("models.claudeCustomEmpty")), "a pending read is not the empty state");

  const failed = ClaudeCustomReadStates({ loading: false, readError: "models.claudeCustomReadFailed", t, onRetry: () => {} });
  const failedNodes = walk(failed);
  assert.ok(failedNodes.some((node) => node.props?.role === "alert"), "a failed read is an alert");
  assert.ok(keys(failed).includes("data-claude-models-retry"), "a failed read offers the retry");
  assert.ok(!texts(failed).some((text) => text.includes("models.claudeCustomEmpty")), "a failed read is not the empty list");

  const empty = ClaudeCustomReadStates({ loading: false, readError: null, t, onRetry: () => {}, onAdd: () => {} });
  assert.ok(texts(empty).some((text) => text.includes("models.claudeCustomEmpty")), "an empty list names what is missing");
  assert.ok(keys(empty).includes("data-claude-models-add"), "the empty list offers the add action");
});

// polyforge#271 round 2 (review r2#8): the real component's first read is the
// section's skeleton — the wiring the state block alone cannot prove.
test("the real section's first read is its skeleton", async () => {
  const state = setup();
  try {
    const attributes = byAttr(state.hook, "data-claude-models-loading");
    assert.ok(attributes.length > 0, "the real component's first read is not its skeleton");
    const text = textOf(state.hook).join("\n");
    assert.ok(text.includes("models.claudeCustomLoading"), "the loading label is missing");
    assert.ok(!text.includes("models.claudeCustomEmpty"), "a pending read is the empty state");
    assert.equal(state.requests.length, 1);
    assert.equal(state.requests[0].url, "/api/harness/claude/model");
    assert.equal(state.requests[0].init?.method ?? "GET", "GET", "mount performs only a read");
  } finally {
    state.restore();
  }
});

test("migration failure leaves the independently loaded list editable and check can retry", async () => {
  const state = setup();
  try {
    await state.answer(0, modelPayload(["kept"]));
    findButton(state.hook, "models.claudeCustomCheckMigration").onClick();
    await state.answer(1, { error: "migration offline" }, 500);
    assert.ok(textOf(state.hook).join(" ").includes("migration offline"));
    assert.equal(byAttr(state.hook, "data-claude-models-loading").length, 0);
    assert.ok(removeButtonOf(state.hook, "kept"));
    findButton(state.hook, "models.claudeCustomCheckMigration").onClick();
    await state.answer(2, { status: "done", changed: true });
    await state.answer(3, modelPayload(["kept", "migrated"]));
    assert.ok(removeButtonOf(state.hook, "migrated"));
    assert.equal(state.refreshes.length, 1);
  } finally { state.restore(); }
});

test("failed list read ends loading and its retry succeeds without migration POST", async () => {
  const state = setup();
  try {
    await state.answer(0, { error: "read offline" }, 500);
    assert.equal(byAttr(state.hook, "data-claude-models-loading").length, 0);
    assert.equal(byAttr(state.hook, "data-claude-models-retry").length, 1);
    findButton(state.hook, "i18n.retry").onClick();
    await state.answer(1, modelPayload(["recovered"]));
    assert.ok(removeButtonOf(state.hook, "recovered"));
    assert.ok(state.requests.every((r) => !r.init?.method));
  } finally { state.restore(); }
});
