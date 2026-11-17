// carries: components/PolyforgeWorkItemParams*, app/api/polyforge/params/*, lib/polyforge-params*
// PolyforgeWorkItemParams.test.mjs (u7, polyforge#79): the 工单详情's
// params section on the react-hook shim (the Tasks-refresh precedent):
// one /api/polyforge/params read per open (a gesture, never a timer),
// every step's declared params rendered with their final value and
// source (AC1); a QUEUED work item's rows carry the set/clear write
// controls (the panel's own write face — the same /api/polyforge/override
// POST), a claimed one is read-only; a refused or unreadable read
// degrades to the error line, never a throw.
import "#test-env";
import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

const fixture = (name) => fileURLToPath(new URL(`../hooks/__fixtures__/${name}`, import.meta.url));
const shimPath = fixture("react-hook-shim.mjs");
const jsxRuntimePath = fixture("react-jsx-runtime.mjs");
const stubsPath = fixture("polyforge-tasks-stubs.mjs");
const modulePath = (path) => fileURLToPath(new URL(`../${path}`, import.meta.url));
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  alias: {
    react: shimPath,
    "react/jsx-runtime": jsxRuntimePath,
    "@/hooks/useI18n": stubsPath,
    "@/lib/polyforge-params": modulePath("lib/polyforge-params.ts"),
  },
});
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { PolyforgeWorkItemParams } = await jiti.import("./PolyforgeWorkItemParams.tsx");

const QUEUED_VIEW = {
  wi: "wi_Q79",
  posture: "queued",
  steps: [
    { id: "build", status: "not started", params: [{ name: "image", value: "base-img", source: "bundle demo step build params.image" }] },
    { id: "review", status: "not started", params: [
      { name: "flag", value: "", source: "bundle demo step review params.flag" },
      { name: "image", value: "reg/ticket:3", source: "step override" },
    ] },
  ],
};
const RUNNING_VIEW = { ...QUEUED_VIEW, wi: "wi_R79", posture: "running", steps: [{ id: "build", status: "done", params: QUEUED_VIEW.steps[0].params }] };

const settle = async () => {
  for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve));
};

function walk(node, out = []) {
  if (Array.isArray(node)) {
    for (const child of node) walk(child, out);
    return out;
  }
  if (!node || typeof node !== "object" || !("props" in node)) return out;
  if (typeof node.type === "function") return walk(node.type(node.props), out);
  out.push(node);
  walk(node.props.children, out);
  return out;
}
const nodes = (hook) => walk(hook.result.current ?? hook.result);
const textOf = (node) => JSON.stringify(node.props.children);

function setup(wi = "wi_Q79") {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = (url, init) => {
    const request = { url, init, settled: false };
    request.promise = new Promise((resolve) => {
      request.resolve = (status, body) => {
        request.settled = true;
        resolve(new Response(JSON.stringify(body ?? {}), { status, headers: { "Content-Type": "application/json" } }));
      };
    });
    calls.push(request);
    return request.promise;
  };
  const hook = renderHook(PolyforgeWorkItemParams, { wi });
  const answer = async (index, status, body) => {
    calls[index].resolve(status, body);
    await settle();
    hook.flush();
  };
  return { hook, calls, answer, done: () => { globalThis.fetch = originalFetch; } };
}

test("one params read per open renders every step's declared params with the final value and source; a claimed ticket is read-only", async () => {
  const { hook, calls, answer, done } = setup("wi_R79");
  assert.equal(calls.length, 1, "exactly one read on open");
  assert.equal(calls[0].url, "/api/polyforge/params?wi=wi_R79");
  await answer(0, 200, { params: RUNNING_VIEW });
  assert.equal(nodes(hook).filter((n) => "data-pf-wi-params-step" in (n.props ?? {})).length, 1, "one step section");
  const row = nodes(hook).find((n) => "data-pf-wi-param" in (n.props ?? {}));
  assert.ok(row, "the param row renders");
  assert.match(textOf(row), /image/);
  assert.match(textOf(row), /base-img/);
  assert.match(textOf(row), /bundle demo step build params.image/);
  assert.equal(nodes(hook).filter((n) => "data-pf-wi-param-set" in (n.props ?? {})).length, 0, "a claimed ticket's rows are read-only");
  done();
});

test("a queued ticket's declared params carry the set and clear controls; a successful write re-reads the view once", async () => {
  const { hook, calls, answer, done } = setup();
  await answer(0, 200, { params: QUEUED_VIEW });
  assert.equal(nodes(hook).filter((n) => "data-pf-wi-param-set" in (n.props ?? {})).length, 3, "a set per declared param");
  const input = nodes(hook).find((n) => n.type === "input" && n.props["data-pf-wi-param-input"] === "image" && n.props["data-pf-wi-param-step"] === "build");
  input.props.onChange({ target: { value: "reg/new:4" } });
  hook.flush();
  nodes(hook).find((n) => n.type === "button" && n.props["data-pf-wi-param-set"] === "image" && n.props["data-pf-wi-param-step"] === "build").props.onClick();
  await settle();
  assert.equal(calls.length, 2, "the write POST");
  assert.equal(calls[1].url, "/api/polyforge/override");
  assert.deepEqual(JSON.parse(calls[1].init.body), {
    wi: "wi_Q79", action: { kind: "param", step: "build", name: "image", value: "reg/new:4" },
  });
  calls[1].resolve(200, { ok: true, result: { wi: "wi_Q79", changed: true, posture: "queued", published: false, step: { id: "build", status: "pending", skippable: false } } });
  await settle();
  hook.flush();
  assert.equal(calls.length, 3, "the write's success re-reads the view once");
  assert.equal(calls[2].url, "/api/polyforge/params?wi=wi_Q79");
  await answer(2, 200, { params: QUEUED_VIEW });
  done();
});

test("the claim race rides the 200 answer's note and shows (AC3); a refused write and a refused read degrade to their own line, never a throw", async () => {
  const { hook, calls, answer, done } = setup();
  await answer(0, 200, { params: QUEUED_VIEW });
  nodes(hook).find((n) => n.type === "button" && n.props["data-pf-wi-param-clear"] === "image" && n.props["data-pf-wi-param-step"] === "review").props.onClick();
  await settle();
  // the daemon's REAL shape (review r1#9): the race is a success answer
  // whose note names it — never a 409
  calls[1].resolve(200, { ok: true, result: { wi: "wi_Q79", changed: true, posture: "running", published: false,
    note: "the work item was claimed while the write landed (its status is running) and this change did not reach its capsule: set the param on the running ticket again",
    step: { id: "review", status: "pending", skippable: false } } });
  await settle();
  hook.flush();
  const note = nodes(hook).find((n) => "data-pf-wi-params-note" in (n.props ?? {}));
  assert.ok(note, "the note line renders");
  assert.match(textOf(note), /set the param on the running ticket again/);
  assert.ok(!nodes(hook).some((n) => "data-pf-wi-params-error" in (n.props ?? {})), "a note is not an error line");
  // a genuine refusal (an undeclared name) keeps the error line
  nodes(hook).find((n) => n.type === "button" && n.props["data-pf-wi-param-set"] === "image" && n.props["data-pf-wi-param-step"] === "build").props.onClick();
  await settle();
  calls[3].resolve(409, { ok: false, error: 'DAEMON-400: the lane declares no step param "nosuch" on step review: a param is set only where the lane declares it' });
  await settle();
  hook.flush();
  const error = nodes(hook).find((n) => "data-pf-wi-params-error" in (n.props ?? {}));
  assert.ok(error, "the refusal line renders");
  assert.match(textOf(error), /declares no step param/);
  // a refused read degrades too
  const second = setup("wi_X79");
  await second.answer(0, 200, { params: null, error: "DAEMON-409: the lane no-such does not load on this daemon" });
  assert.ok(nodes(second.hook).some((n) => "data-pf-wi-params-error" in (n.props ?? {})), "the read's refusal shows");
  second.done();
  done();
});
