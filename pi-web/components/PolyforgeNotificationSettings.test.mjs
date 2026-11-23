// carries: components/PolyforgeNotificationSettings.tsx, hooks/__fixtures__/settings-ui-props-stubs.mjs
import "#test-env";
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// Settings › Polyforge's notification card, rendered through the react-hook
// shim: every assertion reads what the owner can switch or click, never the
// source text.
const shimPath = fileURLToPath(new URL("./__fixtures__/react-hook-shim.mjs", import.meta.url));
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
  alias: {
    react: shimPath,
    "react/jsx-runtime": fileURLToPath(new URL("./__fixtures__/react-jsx-runtime.mjs", import.meta.url)),
    "@/hooks/useI18n": fileURLToPath(new URL("./__fixtures__/polyforge-tasks-stubs.mjs", import.meta.url)),
    "./SettingsUi": fileURLToPath(new URL("./__fixtures__/settings-ui-props-stubs.mjs", import.meta.url)),
  },
});
const { renderHook } = await import(pathToFileURL(shimPath).href);
const { PolyforgeNotificationSettings } = await jiti.import("../components/PolyforgeNotificationSettings.tsx");

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
  static permission = "granted";
  static requested = 0;
  static next = "granted";
  static requestPermission() {
    FakeNotification.requested += 1;
    FakeNotification.permission = FakeNotification.next;
    return Promise.resolve(FakeNotification.next);
  }
}

const memory = (initial = {}) => {
  const data = new Map(Object.entries(initial));
  return { data, getItem: (key) => (data.has(key) ? data.get(key) : null), setItem: (key, value) => data.set(key, String(value)) };
};

function install({ permission = "granted", storage = memory() } = {}) {
  globalThis.Notification = FakeNotification;
  FakeNotification.permission = permission;
  FakeNotification.next = "granted";
  FakeNotification.requested = 0;
  globalThis.window = Object.assign(new EventTarget(), { localStorage: storage });
  globalThis.document = Object.assign(new EventTarget(), { visibilityState: "visible" });
  return storage;
}

async function render() {
  mock.timers.enable({ apis: ["setTimeout", "setInterval"] });
  const hook = renderHook(PolyforgeNotificationSettings, {});
  await settle(); hook.flush(); await settle();
  return hook;
}

test("the switch is on by default and writes the browser preference", async () => {
  try {
    const storage = install();
    const hook = await render();
    const sw = nodes(hook).find((node) => node.type === "switch");
    assert.ok(sw, "the card renders the switch");
    assert.equal(sw.props.checked, true, "notifications are on by default");
    assert.equal(sw.props.label, "polyforge.settings.notificationsSwitch");
    sw.props.onChange(false);
    await settle(); hook.flush();
    assert.equal(storage.data.get("pi-web:polyforge-notifications"), "false", "the switch stores the browser preference");
    hook.unmount();

    const stored = memory({ "pi-web:polyforge-notifications": "false" });
    install({ storage: stored });
    const off = await render();
    const offSwitch = nodes(off).find((node) => node.type === "switch");
    assert.equal(offSwitch.props.checked, false, "a stored false renders the switch off");
    off.unmount();
  } finally {
    mock.timers.reset();
  }
});

test("permission states are visible text and only default offers the authorize gesture", async () => {
  try {
    install({ permission: "granted" });
    const granted = await render();
    assert.match(body(granted), /polyforge\.settings\.notificationsPermissionGranted/);
    assert.equal(nodes(granted).some((node) => node.type === "button" && node.props.onClick), false,
      "a granted browser has no authorize button");
    assert.equal(FakeNotification.requested, 0, "opening the card never asks for permission");
    granted.unmount();

    install({ permission: "default" });
    const pending = await render();
    assert.match(body(pending), /polyforge\.settings\.notificationsPermissionDefault/);
    const authorize = nodes(pending).find((node) => node.type === "button" && node.props.onClick);
    assert.ok(authorize, "the default permission offers the explicit gesture");
    assert.equal(FakeNotification.requested, 0, "only the click asks");
    authorize.props.onClick();
    await settle(); pending.flush(); await settle();
    assert.equal(FakeNotification.requested, 1, "the gesture asks exactly once");
    assert.match(body(pending), /polyforge\.settings\.notificationsPermissionGranted/, "the card shows the new state");
    pending.unmount();

    install({ permission: "denied" });
    const denied = await render();
    assert.match(body(denied), /polyforge\.settings\.notificationsPermissionDenied/);
    assert.match(body(denied), /polyforge\.settings\.notificationsDeniedHint/);
    assert.equal(nodes(denied).some((node) => node.type === "button" && node.props.onClick), false,
      "a denied browser is never asked again");
    assert.equal(FakeNotification.requested, 0);
    denied.unmount();

    install();
    delete globalThis.Notification;
    const unsupported = await render();
    assert.match(body(unsupported), /polyforge\.settings\.notificationsPermissionUnsupported/);
    assert.match(body(unsupported), /polyforge\.settings\.notificationsUnsupportedHint/);
    unsupported.unmount();
  } finally {
    delete globalThis.Notification;
    mock.timers.reset();
  }
});

test("an unusable ledger is reported, so the owner knows notifications are paused", async () => {
  try {
    install();
    globalThis.window.localStorage = { getItem() { throw new Error("denied"); }, setItem() { throw new Error("denied"); } };
    const hook = await render();
    assert.match(body(hook), /polyforge\.settings\.notificationsStorageBlocked/);
    hook.unmount();
  } finally {
    mock.timers.reset();
  }
});