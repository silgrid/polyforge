import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

import {
  applyGroupOrder,
  listGroupOrder,
  PINNED_GROUP_ORDER_STORAGE_KEY,
  setGroupOrder,
} from "./pinned-group-order.ts";

function installWindow() {
  const store = new Map();
  const fakeWindow = {
    localStorage: {
      getItem: (key) => (store.has(key) ? store.get(key) : null),
      setItem: (key, value) => store.set(key, String(value)),
      removeItem: (key) => store.delete(key),
    },
  };
  globalThis.window = fakeWindow;
  return { fakeWindow, store };
}

function projects(keys) {
  return keys.map((key) => ({ key, root: `/root/${key}` }));
}

test("reads the empty order when nothing is stored (fresh install keeps today's behavior)", () => {
  installWindow();
  assert.deepEqual(listGroupOrder(), []);
});

test("writes and re-reads a full key sequence (the move-time persistence contract)", () => {
  const { store } = installWindow();
  setGroupOrder(["c", "a", "b"]);
  assert.equal(store.get(PINNED_GROUP_ORDER_STORAGE_KEY), '["c","a","b"]');
  assert.deepEqual(listGroupOrder(), ["c", "a", "b"]);
});

test("absent or unavailable storage degrades to no order and never throws", () => {
  const original = globalThis.window;
  delete globalThis.window;
  try {
    assert.deepEqual(listGroupOrder(), []);
    setGroupOrder(["a"]);
  } finally {
    if (original !== undefined) globalThis.window = original;
  }
  // Storage that throws on read/write (quota / privacy mode) is a no-op.
  const throwing = {
    getItem: () => {
      throw new Error("quota");
    },
    setItem: () => {
      throw new Error("quota");
    },
    removeItem: () => {},
  };
  assert.deepEqual(listGroupOrder(throwing), []);
  setGroupOrder(["a"], throwing);
});

test("corrupt payloads read as no order", () => {
  const store = new Map([[PINNED_GROUP_ORDER_STORAGE_KEY, "{not json"]]);
  const storage = {
    getItem: (key) => store.get(key) ?? null,
    setItem: (key, value) => store.set(key, String(value)),
    removeItem: (key) => store.delete(key),
  };
  assert.deepEqual(listGroupOrder(storage), []);
  store.set(PINNED_GROUP_ORDER_STORAGE_KEY, JSON.stringify({ order: ["a"] }));
  assert.deepEqual(listGroupOrder(storage), []);
  // Non-string and duplicate entries collapse; first occurrence wins.
  store.set(PINNED_GROUP_ORDER_STORAGE_KEY, JSON.stringify(["a", 7, null, "a", "b"]));
  assert.deepEqual(listGroupOrder(storage), ["a", "b"]);
});

test("applyGroupOrder: empty order returns the incoming sequence unchanged", () => {
  const incoming = projects(["a", "b", "c"]);
  const result = applyGroupOrder(incoming, []);
  assert.deepEqual(result.map((p) => p.key), ["a", "b", "c"]);
  assert.notEqual(result, incoming, "returns a new array (input never aliased)");
});

test("applyGroupOrder: a full order round-trips exactly", () => {
  const result = applyGroupOrder(projects(["a", "b", "c"]), ["c", "a", "b"]);
  assert.deepEqual(result.map((p) => p.key), ["c", "a", "b"]);
  // Element identity is preserved (the same project objects, re-sequenced).
  const incoming = projects(["a", "b", "c"]);
  const reordered = applyGroupOrder(incoming, ["b", "c", "a"]);
  assert.equal(reordered[0], incoming[1]);
  assert.equal(reordered[1], incoming[2]);
  assert.equal(reordered[2], incoming[0]);
});

test("applyGroupOrder: unknown stored keys are ignored", () => {
  const result = applyGroupOrder(projects(["a", "b"]), ["x", "b", "y", "a"]);
  assert.deepEqual(result.map((p) => p.key), ["b", "a"]);
});

test("applyGroupOrder: keys absent from the order keep their default position", () => {
  // A newly pinned group (no stored entry) stays where the default list
  // put it: incoming index re-inserted into the result, clamped to length.
  let result = applyGroupOrder(projects(["a", "b", "c", "d"]), ["a", "b"]);
  assert.deepEqual(result.map((p) => p.key), ["a", "b", "c", "d"]);
  // Two unknowns interleave at their default indexes.
  result = applyGroupOrder(projects(["a", "b", "c", "d"]), ["d", "a"]);
  assert.deepEqual(result.map((p) => p.key), ["d", "b", "c", "a"]);
  // Unknown at the top of the default list stays first even when the
  // stored order would otherwise lead.
  result = applyGroupOrder(projects(["n", "a", "b"]), ["b", "a"]);
  assert.deepEqual(result.map((p) => p.key), ["n", "b", "a"]);
});

test("applyGroupOrder: an order matching no key applies nothing", () => {
  const result = applyGroupOrder(projects(["a", "b"]), ["x", "y"]);
  assert.deepEqual(result.map((p) => p.key), ["a", "b"]);
});

test("applyGroupOrder: single-group and empty lists pass through", () => {
  assert.deepEqual(applyGroupOrder(projects(["a"]), ["z"]).map((p) => p.key), ["a"]);
  assert.deepEqual(applyGroupOrder([], ["a"]), []);
});

test("move semantics: remove + reinsert at the drop target equals a repeated adjacent swap", () => {
  // The sidebar's drop handler models the upstream adjacent-move by
  // removing the dragged key and reinserting it at the target's index;
  // for neighbors that IS the swap, and for distant groups it equals
  // walking the dragged group through its neighbors one swap at a time.
  const move = (keys, dragged, target) => {
    const next = [...keys];
    const from = next.indexOf(dragged);
    const to = next.indexOf(target);
    assert.ok(from >= 0 && to >= 0, "both keys must exist");
    next.splice(from, 1);
    next.splice(to, 0, dragged);
    return next;
  };
  // Adjacent drop: a plain swap.
  assert.deepEqual(move(["a", "b", "c"], "a", "b"), ["b", "a", "c"]);
  assert.deepEqual(move(["a", "b", "c"], "c", "b"), ["a", "c", "b"]);
  // Distant drop: the same end state as repeated adjacent swaps.
  let bySwap = ["a", "b", "c", "d"];
  while (bySwap.indexOf("d") !== 1) {
    const i = bySwap.indexOf("d");
    [bySwap[i], bySwap[i - 1]] = [bySwap[i - 1], bySwap[i]];
  }
  assert.deepEqual(bySwap, move(["a", "b", "c", "d"], "d", "b"));
  // Round trip: persist the moved sequence, apply it to the default list.
  const { store } = installWindow();
  const moved = move(["a", "b", "c", "d"], "d", "b");
  setGroupOrder(moved);
  assert.equal(store.get(PINNED_GROUP_ORDER_STORAGE_KEY), '["a","d","b","c"]');
  assert.deepEqual(
    applyGroupOrder(projects(["a", "b", "c", "d"]), listGroupOrder()).map((p) => p.key),
    ["a", "d", "b", "c"],
  );
});
