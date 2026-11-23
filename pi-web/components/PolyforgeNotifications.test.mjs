// carries: components/PolyforgeNotifications.tsx, hooks/__fixtures__/react-hook-shim.mjs
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// The one-time permission explanation banner, rendered through the hook shim
// with a fake `Notification`, page and localStorage: every assertion reads
// what the owner would see, never the source text.
const shimPath = fileURLToPath(new URL("../hooks/__fixtures__/react-hook-shim.mjs", import.meta.url));
const modulePath = (path) => fileURLToPath(new URL(`../${path}`, import.meta.url));
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  alias: {
    react: shimPath,
    "react/jsx-runtime": modulePath("hooks/__fixtures__/react-jsx-runtime.mjs"),
    "@/hooks/useI18n": modulePath("hooks/__fixtures__/polyforge-tasks-stubs.mjs"),
    "@/hooks/usePolyforgeNotifications": modulePath("hooks/usePolyforgeNotifications.ts"),
    "@/hooks/usePolyforgePanelPreferences": modulePath("hooks/usePolyforgePanelPreferences.ts"),
    "@/lib/polyforge-panel-preferences": modulePath("lib/polyforge-panel-preferences.ts"),
    "@/lib/polyforge-notifications": modulePath("lib/polyforge-notifications.ts"),
    "@/lib/polyforge-decisions-pure": modulePath("lib/polyforge-decisions-pure.ts"),
    "@/lib/browser-notifications": modulePath("lib/browser-notifications.ts"),
    "@/lib/i18n/types": modulePath("lib/i18n/types.ts"),
  },
});
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { PolyforgeNotifications } = await jiti.import("./PolyforgeNotifications.tsx");

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
const text = (node) => {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(text).join(" ");
  if (node && typeof node === "object") return text(node.props?.children);
  return "";
};
const nodes = (hook) => walk(hook.result);
const body = (hook) => text(hook.result);
const settle = async () => { for (let index = 0; index < 10; index++) await new Promise((resolve) => setImmediate(resolve)); };

class FakeNotification {
  static calls = [];
  static permission = "default";
  static requested = 0;
  static next = "granted";
  static requestPermission() {
    FakeNotification.requested += 1;
    FakeNotification.permission = FakeNotification.next;
    return Promise.resolve(FakeNotification.next);
  }
  constructor(title, options) {
    this.title = title;
    this.options = options;
    FakeNotification.calls.push(this);
  }
  close() {}
}

const memory = (initial = {}) => {
  const data = new Map(Object.entries(initial));
  return { data, getItem: (key) => (data.has(key) ? data.get(key) : null), setItem: (key, value) => data.set(key, String(value)) };
};

const page = { visibilityState: "hidden", hasFocus: () => false };
function install({ permission = "default", storage = memory() } = {}) {
  globalThis.Notification = FakeNotification;
  FakeNotification.calls = [];
  FakeNotification.requested = 0;
  FakeNotification.permission = permission;
  FakeNotification.next = "granted";
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

function mount(decisions) {
  const opened = [];
  const hook = renderHook(
    (props) => PolyforgeNotifications({ decisions: props.decisions, onOpenDecision: (entry) => opened.push(entry.id) }),
    { decisions },
  );
  return { hook, opened };
}
const feed = (hook, decisions) => { hook.rerender({ decisions }); hook.flush(); };

test("a hidden tab without permission shows the one-time explanation, and its gesture asks once", async () => {
  install();
  const { hook } = mount(null);
  await settle(); hook.flush();
  assert.equal(hook.result, null, "no explanation before any owner decision");

  feed(hook, [decision({ id: 5 })]);
  await settle();
  assert.equal(hook.result, null, "the first feed only establishes the baseline");

  feed(hook, [decision({ id: 101, text: "请选择方案" }), decision({ id: 5 })]);
  await settle(); hook.flush();
  assert.equal(typeof PolyforgeNotifications, "function");
  const banner = nodes(hook).find((node) => node.type === "div" && node.props["data-pf-notification-hint"] !== undefined);
  assert.ok(banner, "an entry that would notify explains the missing permission instead");
  assert.match(body(hook), /polyforge\.notifications\.hint/);
  const buttons = nodes(hook).filter((node) => node.type === "button");
  assert.equal(buttons.length, 2, "the explanation offers the gesture and the dismissal");
  assert.equal(FakeNotification.requested, 0, "nothing pops the browser prompt by itself");

  const authorize = buttons.find((node) => node.props["data-pf-notification-authorize"] !== undefined);
  assert.ok(authorize, "the authorization gesture is present");
  await authorize.props.onClick();
  await settle(); hook.flush();
  assert.equal(FakeNotification.requested, 1, "the gesture asks exactly once");
  assert.equal(hook.result, null, "a granted browser drops the explanation");

  feed(hook, [decision({ id: 102 }), decision({ id: 101 }), decision({ id: 5 })]);
  await settle();
  assert.equal(FakeNotification.calls.length, 1, "after the grant the next entry notifies");
  hook.unmount();
});

test("dismissing the explanation is remembered across entries and a reload", async () => {
  const storage = install();
  const { hook } = mount([decision({ id: 5 })]);
  await settle(); hook.flush();
  feed(hook, [decision({ id: 101 }), decision({ id: 5 })]);
  await settle(); hook.flush();
  const dismiss = nodes(hook).find((node) => node.type === "button" && node.props["data-pf-notification-dismiss"] !== undefined);
  assert.ok(dismiss, "the explanation can be dismissed");
  dismiss.props.onClick();
  await settle(); hook.flush();
  assert.equal(hook.result, null, "the explanation closes");

  feed(hook, [decision({ id: 102 }), decision({ id: 101 }), decision({ id: 5 })]);
  await settle();
  assert.equal(hook.result, null, "a later entry never repeats the explanation");
  assert.equal(storage.data.get("pi-web:polyforge-notification-hint-shown"), "1", "the one-time mark persists");
  hook.unmount();

  const reload = mount([decision({ id: 102 }), decision({ id: 101 }), decision({ id: 5 })]);
  await settle(); reload.hook.flush();
  feed(reload.hook, [decision({ id: 103 }), decision({ id: 102 }), decision({ id: 101 }), decision({ id: 5 })]);
  await settle();
  assert.equal(reload.hook.result, null, "a reload does not repeat the explanation");
  assert.equal(FakeNotification.calls.length, 0, "and nothing is delivered without permission");
  reload.hook.unmount();
});

test("a granted background tab delivers a notification instead of the explanation", async () => {
  install({ permission: "granted" });
  const { hook } = mount([decision({ id: 5 })]);
  await settle(); hook.flush();
  feed(hook, [decision({ id: 101, phrase: "a step asked the owner a question" }), decision({ id: 5 })]);
  await settle();
  assert.equal(hook.result, null, "a granted browser needs no explanation");
  assert.equal(FakeNotification.calls.length, 1, "the entry notifies");
  assert.equal(FakeNotification.calls[0].title, "polyforge#240 · a step asked the owner a question",
    "the notification carries the ticket and the class (the wire phrase when the label is missing)");
  hook.unmount();
});

test("a foreground tab explains nothing and is consumed", async () => {
  install();
  page.visibilityState = "visible";
  page.hasFocus = () => true;
  const { hook } = mount([decision({ id: 5 })]);
  await settle(); hook.flush();
  feed(hook, [decision({ id: 101 }), decision({ id: 5 })]);
  await settle();
  assert.equal(hook.result, null, "a foreground tab shows no explanation");
  hook.unmount();
});