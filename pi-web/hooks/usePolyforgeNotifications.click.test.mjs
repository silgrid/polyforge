// carries: hooks/usePolyforgeNotifications.ts, hooks/__fixtures__/react-hook-shim.mjs
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// Review r1#1 (polyforge#240): a notification outlives the render that
// delivered it, so its onclick must run the LATEST open callback — the
// selection and jobs it reads are the ones current when the owner clicks, not
// the ones of the delivery render. The real hook runs on the react-hook shim
// with a fake `Notification`, page and localStorage.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { usePolyforgeNotifications } = await jiti.import("./usePolyforgeNotifications.ts");

const translate = (key) => ({ "polyforge.decisions.class.needs_input": "需要你回答" }[key] ?? key);
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };

class FakeNotification {
  static calls = [];
  static permission = "granted";
  static requestPermission() { return Promise.resolve("granted"); }
  constructor(title, options) {
    this.title = title;
    this.options = options;
    this.closed = false;
    FakeNotification.calls.push(this);
  }
  close() { this.closed = true; }
}

const memory = () => {
  const data = new Map();
  return { data, getItem: (key) => (data.has(key) ? data.get(key) : null), setItem: (key, value) => data.set(key, String(value)) };
};

const page = { visibilityState: "hidden", hasFocus: () => false };
const browser = { focusCalls: 0 };
function install() {
  globalThis.Notification = FakeNotification;
  FakeNotification.calls = [];
  FakeNotification.permission = "granted";
  globalThis.window = {
    localStorage: memory(),
    dispatchEvent: () => true,
    addEventListener: () => {},
    removeEventListener: () => {},
    focus: () => { browser.focusCalls += 1; },
  };
  globalThis.document = page;
  page.visibilityState = "hidden";
  page.hasFocus = () => false;
  browser.focusCalls = 0;
}

const decision = (over = {}) => ({
  id: 1,
  at: "2026-11-23T00:00:00Z",
  kind: "work_item",
  slug: "polyforge#240",
  class: "needs_input",
  attention: true,
  resolved: false,
  dismissed: false,
  replied: false,
  ...over,
});

async function render(decisions, onOpenDecision) {
  const hook = renderHook(
    (props) => usePolyforgeNotifications({ decisions: props.decisions, translate, onOpenDecision: props.onOpenDecision }),
    { decisions, onOpenDecision },
  );
  await settle(); hook.flush();
  return hook;
}
const feed = (hook, decisions, onOpenDecision) => { hook.rerender({ decisions, onOpenDecision }); hook.flush(); };

test("a notification click runs the latest open callback, never the delivery render's", async () => {
  install();
  const opened = [];
  const first = (entry) => opened.push(["first", entry.id]);
  const second = (entry) => opened.push(["second", entry.id]);
  const hook = await render([decision({ id: 5 })], first);
  try {
    feed(hook, [decision({ id: 101 }), decision({ id: 5 })], first);
    await settle();
    assert.equal(FakeNotification.calls.length, 1, "the entry notifies under the first callback");
    const shown = FakeNotification.calls[0];

    // The panel re-renders (a new open callback) after the notification was
    // delivered — exactly what a selection change does.
    feed(hook, [decision({ id: 101 }), decision({ id: 5 })], second);
    await settle();
    shown.onclick();

    assert.deepEqual(opened, [["second", 101]], "the click runs the current callback, not the stale one");
    assert.equal(shown.closed, true, "the clicked notification closes");
    assert.equal(browser.focusCalls, 1, "the click focuses the tab, as the session notifications do");
  } finally {
    hook.unmount();
  }
});