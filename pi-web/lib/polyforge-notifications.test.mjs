// carries: lib/polyforge-notifications.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const n = await jiti.import("./polyforge-notifications.ts");

const decision = (over = {}) => ({
  id: 1,
  at: "2026-11-23T00:00:00.000Z",
  kind: "work_item",
  slug: "polyforge#240",
  class: "needs_input",
  attention: true,
  resolved: false,
  dismissed: false,
  replied: false,
  ...over,
});
const labels = {
  "polyforge.decisions.class.needs_input": "需要你回答",
  "polyforge.decisions.class.blocked": "被阻塞",
  "polyforge.decisions.class.failed": "失败",
};
const translate = (key) => labels[key] ?? key;

test("the owner allowlist is the three ruled classes and only open entries notify", () => {
  assert.equal(typeof n.isOwnerNotificationDecision, "function", "the selection helper is exported");
  for (const cls of ["needs_input", "blocked", "failed"]) {
    assert.equal(n.isOwnerNotificationDecision(decision({ class: cls })), true, `${cls} needs the owner`);
  }
  for (const over of [
    { class: "completed", attention: false },
    { class: "paused" },
    { class: "failed", resolved: true },
    { class: "blocked", dismissed: true },
    { class: "needs_input", replied: true, attention: false },
    { class: "failed", attention: false },
  ]) {
    assert.equal(n.isOwnerNotificationDecision(decision(over)), false, JSON.stringify(over));
  }
});

test("the copy carries the ticket, the class and a one-line summary", () => {
  assert.equal(typeof n.ownerNotificationCopy, "function", "the copy builder is exported");
  const copy = n.ownerNotificationCopy(decision({ text: "请选择方案\n  第二行\t细节" }), "需要你回答");
  assert.equal(copy.title, "polyforge#240 · 需要你回答", "the title names the ticket and the class");
  assert.equal(copy.body, "请选择方案 第二行 细节", "the body is the summary on one line");
  assert.equal(copy.tag, "polyforge-decision-1", "the tag is the entry's own id");

  const task = n.ownerNotificationCopy(
    decision({ kind: "task", slug: undefined, task_id: "task_0123456789abcdef", class: "failed", text: "构建失败" }),
    "失败",
  );
  assert.equal(task.title, "task task_0123456789abcdef · 失败", "a task entry names its task");

  assert.equal(n.ownerNotificationCopy(decision({ text: undefined }), "需要你回答").body, "需要你回答",
    "no summary falls back to the class label");
  assert.equal(n.ownerNotificationCopy(decision({ text: "   ", phrase: "a step asked the owner a question" }), "").body,
    "a step asked the owner a question", "no summary and no label fall back to the wire phrase");
});

test("the summary is one line of at most 120 Unicode code points", () => {
  assert.equal(typeof n.oneLineSummary, "function", "the summarizer is exported");
  assert.equal(n.NOTIFICATION_SUMMARY_MAX, 120, "the ruled bound");
  const cut = n.oneLineSummary("票".repeat(200), "x");
  assert.equal(Array.from(cut).length, 120, "the cut keeps 120 code points, ellipsis included");
  assert.ok(cut.endsWith("…"), "a cut summary says so");
  const emoji = n.oneLineSummary("😀".repeat(200), "x");
  assert.equal(Array.from(emoji).length, 120, "a surrogate pair is one code point");
  assert.equal(n.oneLineSummary(undefined, "回落"), "回落");
  assert.equal(n.oneLineSummary("  ", "回落"), "回落");
  assert.equal(n.oneLineSummary("a\tb\rc", "x"), "a b c");
});

test("the ledger parses, merges and bounds; unseen ids are the feed's new entries", () => {
  assert.deepEqual(n.parseNotificationLedger(null), { present: false, ids: [] }, "absent storage is no baseline yet");
  assert.deepEqual(n.parseNotificationLedger("[]"), { present: true, ids: [] }, "an empty stored ledger is a baseline");
  assert.deepEqual(n.parseNotificationLedger("[3,1,3,-1,0,2.5,\"4\"]"), { present: true, ids: [3, 1] },
    "only positive safe integers survive, deduplicated, in stored order");
  assert.deepEqual(n.parseNotificationLedger("not json"), { present: true, ids: [] }, "a corrupt ledger is a baseline, not a flood");
  assert.equal(typeof n.mergeNotificationLedger, "function", "the merge helper is exported");
  assert.deepEqual(n.mergeNotificationLedger([1, 2, 3], [3, 4]), [1, 2, 3, 4]);
  assert.deepEqual(n.mergeNotificationLedger([1, 2], [3, 4], 3), [2, 3, 4], "the newest ids survive the bound");
  assert.equal(n.NOTIFICATION_LEDGER_MAX, 2000, "the bound outlives the feed's 300-entry retention");
  assert.equal(typeof n.newNotificationIds, "function", "the new-id helper is exported");
  assert.deepEqual(n.newNotificationIds([decision({ id: 1 }), decision({ id: 2 }), decision({ id: 2 })], new Set([1])), [2]);
});

test("the ledger and the one-time marker survive storage and never throw when it refuses", () => {
  const memory = (initial = {}) => {
    const data = new Map(Object.entries(initial));
    return { getItem: (k) => (data.has(k) ? data.get(k) : null), setItem: (k, v) => data.set(k, String(v)), data };
  };
  const storage = memory();
  assert.deepEqual(n.readNotificationLedger(storage), { ok: true, present: false, ids: [] });
  assert.equal(n.writeNotificationLedger([7, 8], storage), true);
  assert.deepEqual(n.readNotificationLedger(storage), { ok: true, present: true, ids: [7, 8] });
  assert.equal(n.NOTIFICATION_LEDGER_KEY, "pi-web:polyforge-notified-decisions");
  assert.equal(n.readNotificationHintShown(storage), false);
  assert.equal(n.markNotificationHintShown(storage), true);
  assert.equal(n.readNotificationHintShown(storage), true);
  assert.equal(n.NOTIFICATION_HINT_KEY, "pi-web:polyforge-notification-hint-shown");

  const refusing = { getItem() { throw new Error("denied"); }, setItem() { throw new Error("denied"); } };
  assert.deepEqual(n.readNotificationLedger(refusing), { ok: false }, "an unreadable ledger is not a baseline");
  assert.equal(n.writeNotificationLedger([1], refusing), false);
  assert.equal(n.readNotificationHintShown(refusing), false);
  assert.equal(n.markNotificationHintShown(refusing), false);
});

test("permission is read from the browser and requested only while it is default", async () => {
  const original = globalThis.Notification;
  try {
    delete globalThis.Notification;
    assert.equal(n.notificationPermission(), "unsupported");
    assert.equal(await n.requestNotificationPermission(), "unsupported");

    let calls = 0;
    const fake = (permission, next = permission) => {
      function FakeNotification() {}
      FakeNotification.permission = permission;
      FakeNotification.requestPermission = () => { calls += 1; return Promise.resolve(next); };
      return FakeNotification;
    };

    globalThis.Notification = fake("granted");
    assert.equal(n.notificationPermission(), "granted");
    assert.equal(await n.requestNotificationPermission(), "granted");
    assert.equal(calls, 0, "a granted browser is never asked again");

    globalThis.Notification = fake("denied");
    assert.equal(n.notificationPermission(), "denied");
    assert.equal(await n.requestNotificationPermission(), "denied");
    assert.equal(calls, 0, "a denied browser is never asked again");

    globalThis.Notification = fake("default", "granted");
    assert.equal(n.notificationPermission(), "default");
    assert.equal(await n.requestNotificationPermission(), "granted");
    assert.equal(calls, 1, "the explicit gesture asks exactly once");
  } finally {
    if (original === undefined) delete globalThis.Notification;
    else globalThis.Notification = original;
  }
});