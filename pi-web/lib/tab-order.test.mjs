import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { reorderById, syncTabOrder, applyTabOrder } from "./tab-order.ts";

const byId = (item) => item.id;

test("reorderById moves the dragged item before the target", () => {
  const items = [{ id: "a" }, { id: "b" }, { id: "c" }];
  const result = reorderById(items, byId, "c", "a", false);
  assert.deepEqual(result.map(byId), ["c", "a", "b"]);
});

test("reorderById moves the dragged item after the target", () => {
  const items = [{ id: "a" }, { id: "b" }, { id: "c" }];
  const result = reorderById(items, byId, "a", "b", true);
  assert.deepEqual(result.map(byId), ["b", "a", "c"]);
});

test("reorderById handles rightward and leftward moves symmetrically", () => {
  const items = [{ id: "a" }, { id: "b" }, { id: "c" }, { id: "d" }];
  assert.deepEqual(reorderById(items, byId, "d", "b", false).map(byId), ["a", "d", "b", "c"]);
  assert.deepEqual(reorderById(items, byId, "d", "b", true).map(byId), ["a", "b", "d", "c"]);
  assert.deepEqual(reorderById(items, byId, "a", "d", true).map(byId), ["b", "c", "d", "a"]);
  assert.deepEqual(reorderById(items, byId, "a", "d", false).map(byId), ["b", "c", "a", "d"]);
});

test("reorderById no-ops on self-drop or unknown ids", () => {
  const items = [{ id: "a" }, { id: "b" }];
  assert.equal(reorderById(items, byId, "a", "a", false), items);
  assert.equal(reorderById(items, byId, "zzz", "a", false), items);
  assert.equal(reorderById(items, byId, "a", "zzz", false), items);
});

test("reorderById preserves object identity of every item (only position changes)", () => {
  const a = { id: "a", data: 1 };
  const b = { id: "b", data: 2 };
  const result = reorderById([a, b], byId, "b", "a", false);
  assert.equal(result[0], b);
  assert.equal(result[1], a);
});

test("syncTabOrder keeps existing order and appends new live ids at the end", () => {
  assert.deepEqual(syncTabOrder(["b", "a"], ["a", "b", "c"]), ["b", "a", "c"]);
});

test("syncTabOrder drops ids no longer live", () => {
  assert.deepEqual(syncTabOrder(["a", "b", "c"], ["a", "c"]), ["a", "c"]);
});

test("syncTabOrder on an empty order adopts the live order", () => {
  assert.deepEqual(syncTabOrder([], ["x", "y"]), ["x", "y"]);
});

test("syncTabOrder survives a reorder across an open/close cycle", () => {
  // User reordered to [c, a, b]; b then closes; a new tab d opens.
  const reordered = ["c", "a", "b"];
  const afterClose = syncTabOrder(reordered, ["c", "a"]);
  assert.deepEqual(afterClose, ["c", "a"]);
  const afterOpen = syncTabOrder(afterClose, ["c", "a", "d"]);
  assert.deepEqual(afterOpen, ["c", "a", "d"], "new tab appends at the tail, reorder survives");
});

test("applyTabOrder renders items in the tracked order", () => {
  const items = [{ id: "a" }, { id: "b" }, { id: "c" }];
  assert.deepEqual(applyTabOrder(["c", "a", "b"], items, byId).map(byId), ["c", "a", "b"]);
});

test("applyTabOrder appends live items missing from the order at the end", () => {
  const items = [{ id: "a" }, { id: "b" }, { id: "c" }];
  assert.deepEqual(applyTabOrder([], items, byId).map(byId), ["a", "b", "c"]);
  assert.deepEqual(applyTabOrder(["b"], items, byId).map(byId), ["b", "a", "c"]);
});

test("applyTabOrder ignores stale ids in the order that no longer match a live item", () => {
  const items = [{ id: "a" }, { id: "b" }];
  assert.deepEqual(applyTabOrder(["z", "b", "a"], items, byId).map(byId), ["b", "a"]);
});

test("applyTabOrder never duplicates an item even with a duplicate id in order", () => {
  const items = [{ id: "a" }, { id: "b" }];
  assert.deepEqual(applyTabOrder(["a", "a", "b"], items, byId).map(byId), ["a", "b"]);
});
