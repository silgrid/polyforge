// carries: components/PolyforgeNotifications.tsx, hooks/__fixtures__/react-hook-shim.mjs
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createJiti } from "jiti";

// Review r2#1 (polyforge#240): a browser whose notification ledger cannot be
// read or written pauses delivery, and the in-page note must not become a
// permanent, undismissable overlay — it shows only while the switch is on and
// can be dismissed for the page. The real component runs on the hook shim with
// a fake `Notification`, page and localStorage.
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
  static permission = "granted";
  static requestPermission() { return Promise.resolve("granted"); }
  constructor() { this.closed = false; }
  close() { this.closed = true; }
}

const page = { visibilityState: "hidden", hasFocus: () => false };
function install(storage) {
  globalThis.Notification = FakeNotification;
  FakeNotification.permission = "granted";
  globalThis.window = { localStorage: storage, dispatchEvent: () => true, addEventListener: () => {}, removeEventListener: () => {}, focus: () => {} };
  globalThis.document = page;
  page.visibilityState = "hidden";
  page.hasFocus = () => false;
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

async function render(decisions) {
  const hook = renderHook(
    (props) => PolyforgeNotifications({ decisions: props.decisions, onOpenDecision: () => {} }),
    { decisions },
  );
  await settle(); hook.flush();
  return hook;
}

test("a write-blocked ledger shows the paused note only while the switch is on, and it closes", async () => {
  const data = new Map();
  const writeFails = {
    getItem: (key) => (data.has(key) ? data.get(key) : null),
    setItem: () => { throw new Error("quota exceeded"); },
  };
  install(writeFails);
  const hook = await render([decision({ id: 5 })]);
  try {
    assert.ok(nodes(hook).some((node) => node.type === "div" && node.props["data-pf-notification-hint"] !== undefined),
      "the paused note shows while the switch is on");
    assert.match(body(hook), /polyforge\.settings\.notificationsStorageBlocked/, "it names the paused delivery");
    const dismiss = nodes(hook).find((node) => node.type === "button" && node.props["data-pf-notification-dismiss"] !== undefined);
    assert.ok(dismiss, "the paused note can be dismissed for this page");
    dismiss.props.onClick();
    await settle(); hook.flush();
    assert.equal(hook.result, null, "a dismissed paused note stays away");
  } finally {
    hook.unmount();
  }
});

test("with the switch off a blocked ledger shows no note at all", async () => {
  const refusing = {
    getItem: (key) => {
      if (key === "pi-web:polyforge-notifications") return "false";
      throw new Error("denied");
    },
    setItem: () => { throw new Error("denied"); },
  };
  install(refusing);
  const hook = await render([decision({ id: 7 })]);
  try {
    assert.equal(hook.result, null, "the switch off keeps the paused note away");
  } finally {
    hook.unmount();
  }
});