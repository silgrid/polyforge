// carries: hooks/usePolyforgeNotifications.ts, lib/polyforge-notifications.ts, hooks/__fixtures__/react-hook-shim.mjs
import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// The real hook on the react-hook shim, with a fake `Notification`, page and
// localStorage: every assertion reads what the browser would have been asked
// to show, never the source text.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, { tsconfigPaths: true, alias: { react: shimPath } });
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { usePolyforgeNotifications } = await jiti.import("./usePolyforgeNotifications.ts");

const LABELS = {
  "polyforge.decisions.class.needs_input": "需要你回答",
  "polyforge.decisions.class.blocked": "被阻塞",
  "polyforge.decisions.class.failed": "失败",
};
const translate = (key) => LABELS[key] ?? key;
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };

class FakeNotification {
  static calls = [];
  static requested = 0;
  static permission = "granted";
  static next = "granted";
  static requestPermission() { FakeNotification.requested += 1; return Promise.resolve(FakeNotification.next); }
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
function install({ permission = "granted", storage = memory() } = {}) {
  globalThis.Notification = FakeNotification;
  FakeNotification.calls = [];
  FakeNotification.requested = 0;
  FakeNotification.permission = permission;
  FakeNotification.next = permission === "granted" ? "granted" : "granted";
  globalThis.window = { localStorage: storage, dispatchEvent: () => true, addEventListener: () => {}, removeEventListener: () => {} };
  globalThis.document = page;
  page.visibilityState = "hidden";
  page.hasFocus = () => false;
  return storage;
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

function mount(decisions, opened = []) {
  const hook = renderHook(
    (props) => usePolyforgeNotifications({ decisions: props.decisions, translate, onOpenDecision: (entry) => opened.push(entry.id) }),
    { decisions },
  );
  return { hook, opened };
}
const feed = (hook, decisions) => { hook.rerender({ decisions }); hook.flush(); };

test("notifies a new owner decision once while the tab is in the background", async () => {
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  const storage = install();
  try {
    const { hook, opened } = mount(null);
    await settle(); hook.flush();
    assert.equal(FakeNotification.calls.length, 0, "no feed, no notification");

    // the first successful feed only establishes a baseline (Q6 A)
    feed(hook, [decision({ id: 5, text: "旧条目" })]);
    await settle();
    assert.equal(FakeNotification.calls.length, 0, "an existing entry is never notified on first load");
    assert.deepEqual(JSON.parse(storage.data.get("pi-web:polyforge-notified-decisions")), [5], "the baseline is the feed");

    const first = decision({ id: 101, text: "请选择方案" });
    feed(hook, [first, decision({ id: 5, text: "旧条目" })]);
    await settle();
    assert.equal(FakeNotification.calls.length, 1, "the new entry notifies exactly once");
    const shown = FakeNotification.calls[0];
    assert.equal(shown.title, "polyforge#240 · 需要你回答", "the title names the ticket and the class");
    assert.equal(shown.options.body, "请选择方案", "the body is the entry's summary");
    assert.equal(shown.options.tag, "polyforge-decision-101");

    feed(hook, [first, decision({ id: 5 })]);
    await settle();
    assert.equal(FakeNotification.calls.length, 1, "a later poll of the same entry never notifies again");

    feed(hook, [decision({ id: 102, text: "请选择方案" }), first]);
    await settle();
    assert.equal(FakeNotification.calls.length, 2, "a different id notifies even with the same text");

    shown.onclick();
    assert.deepEqual(opened, [101], "the click opens the entry the notification was built from");
    assert.equal(shown.closed, true, "the clicked notification closes");

    // a reload reads the same ledger: no repeat
    hook.unmount();
    const reload = mount([decision({ id: 102 }), first]);
    await settle(); reload.hook.flush();
    assert.equal(FakeNotification.calls.length, 2, "a reload never repeats a consumed entry");
  } finally {
    mock.timers.reset();
  }
});

test("the foreground, a disabled switch and non-owner entries are consumed without delivering", async () => {
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  try {
    // foreground: no notification, and the entry is consumed (Q6 A)
    install();
    const a = mount([]);
    await settle(); a.hook.flush();
    page.visibilityState = "visible";
    page.hasFocus = () => true;
    feed(a.hook, [decision({ id: 11 })]);
    await settle();
    assert.equal(FakeNotification.calls.length, 0, "a foreground tab never notifies");
    page.visibilityState = "hidden";
    page.hasFocus = () => false;
    feed(a.hook, [decision({ id: 12 }), decision({ id: 11 })]);
    await settle();
    assert.deepEqual(FakeNotification.calls.map((call) => call.options.tag), ["polyforge-decision-12"],
      "the foreground entry is not back-filled; only the newer one notifies");
    a.hook.unmount();

    // the switch is off: no notification, and the entry is consumed
    const storage = install({ storage: memory() });
    storage.setItem("pi-web:polyforge-notifications", "false");
    const b = mount([]);
    await settle(); b.hook.flush();
    assert.equal(b.hook.result.enabled, false);
    feed(b.hook, [decision({ id: 21 })]);
    await settle();
    assert.equal(FakeNotification.calls.length, 0, "a disabled switch never notifies");
    storage.setItem("pi-web:polyforge-notifications", "true");
    feed(b.hook, [decision({ id: 22 }), decision({ id: 21 })]);
    await settle();
    assert.deepEqual(FakeNotification.calls.map((call) => call.options.tag), ["polyforge-decision-22"],
      "an entry observed while disabled is not back-filled after enabling");
    b.hook.unmount();

    // informational and closed entries never notify
    install();
    const c = mount([]);
    await settle(); c.hook.flush();
    feed(c.hook, [
      decision({ id: 31, class: "completed", attention: false }),
      decision({ id: 32, class: "failed", resolved: true }),
      decision({ id: 33, class: "blocked", dismissed: true }),
      decision({ id: 34, class: "needs_input", replied: true, attention: false }),
    ]);
    await settle();
    assert.equal(FakeNotification.calls.length, 0, "resolved, dismissed, replied and informational entries stay silent");
    c.hook.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("without permission it never asks by itself and explains once", async () => {
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  try {
    const storage = install({ permission: "default" });
    const { hook } = mount([]);
    await settle(); hook.flush();
    assert.equal(hook.result.permission, "default");

    feed(hook, [decision({ id: 41 })]);
    await settle();
    assert.equal(FakeNotification.calls.length, 0, "no permission, no notification");
    assert.equal(FakeNotification.requested, 0, "the page never pops the browser prompt by itself");
    assert.equal(hook.result.hint, true, "the one-time explanation is shown");
    assert.equal(storage.data.get("pi-web:polyforge-notification-hint-shown"), "1", "the explanation is marked as shown");

    feed(hook, [decision({ id: 41 }), decision({ id: 42 })]);
    await settle();
    assert.equal(hook.result.hint, true, "a later entry does not repeat the explanation");

    hook.result.dismissHint();
    await settle();
    assert.equal(hook.result.hint, false, "the explanation can be dismissed");
    feed(hook, [decision({ id: 42 }), decision({ id: 43 })]);
    await settle();
    assert.equal(hook.result.hint, false, "once dismissed, it never shows again");
    hook.unmount();

    const reload = mount([decision({ id: 43 })]);
    await settle(); reload.hook.flush();
    feed(reload.hook, [decision({ id: 43 }), decision({ id: 44 })]);
    await settle();
    assert.equal(reload.hook.result.hint, false, "a reload does not repeat the explanation");

    FakeNotification.next = "granted";
    await reload.hook.result.requestPermission();
    await settle();
    assert.equal(FakeNotification.requested, 1, "the explicit gesture asks exactly once");
    assert.equal(reload.hook.result.permission, "granted");
    feed(reload.hook, [decision({ id: 44 }), decision({ id: 45 })]);
    await settle();
    assert.deepEqual(FakeNotification.calls.map((call) => call.options.tag), ["polyforge-decision-45"],
      "after the grant a background entry notifies");
    reload.hook.unmount();

    // denied: no request, one explanation
    install({ permission: "denied" });
    const denied = mount([]);
    await settle(); denied.hook.flush();
    feed(denied.hook, [decision({ id: 51 })]);
    await settle();
    assert.equal(FakeNotification.calls.length, 0);
    assert.equal(FakeNotification.requested, 0, "a denied browser is never asked again");
    assert.equal(denied.hook.result.hint, true, "denied still explains itself once");
    denied.hook.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("a refusing storage pauses delivery instead of repeating, and never throws", async () => {
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  try {
    install();
    const refusing = { getItem() { throw new Error("denied"); }, setItem() { throw new Error("denied"); } };
    globalThis.window.localStorage = refusing;
    const { hook } = mount([]);
    await settle(); hook.flush();
    feed(hook, [decision({ id: 61 })]);
    await settle();
    assert.equal(hook.result.storageBlocked, true, "the hook reports the unusable ledger");
    assert.equal(FakeNotification.calls.length, 0, "without dedupe nothing is delivered");
    assert.equal(hook.result.hint, false, "a hint needs a persistent marker, so none is claimed");
    hook.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("a browser without the Notification API delivers nothing and says so once", async () => {
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  try {
    install();
    delete globalThis.Notification;
    const { hook } = mount([]);
    await settle(); hook.flush();
    assert.equal(hook.result.permission, "unsupported");
    feed(hook, [decision({ id: 71 })]);
    await settle();
    assert.equal(hook.result.hint, true, "an unsupported browser gets the one-time explanation");
    hook.unmount();
  } finally {
    delete globalThis.Notification;
    mock.timers.reset();
  }
});