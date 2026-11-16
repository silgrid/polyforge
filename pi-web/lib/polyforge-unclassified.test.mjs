// carries: lib/polyforge-unclassified-pure.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const pure = await jiti.import("./polyforge-unclassified-pure.ts");

test("the sanitizer takes the daemon's report shape and bounds it", () => {
  const report = pure.sanitizePolyforgeUnclassified({
    window_days: 7,
    items: [
      { text: "gateway tantrum A", count: 3, last_at: "2026-11-15T10:00:00Z" },
      { text: "upstream wailed mysteriously", count: 1, last_at: "2026-11-14T09:00:00Z" },
    ],
  });
  assert.ok(report, "a well-formed report passes");
  assert.equal(report.windowDays, 7);
  assert.deepEqual(report.items, [
    { text: "gateway tantrum A", count: 3, lastAt: "2026-11-15T10:00:00Z" },
    { text: "upstream wailed mysteriously", count: 1, lastAt: "2026-11-14T09:00:00Z" },
  ]);
});

test("the sanitizer drops what is not the shape and bounds what is", () => {
  assert.equal(pure.sanitizePolyforgeUnclassified(null), null);
  assert.equal(pure.sanitizePolyforgeUnclassified("nope"), null);
  assert.equal(pure.sanitizePolyforgeUnclassified({}), null, "no items array");
  assert.equal(pure.sanitizePolyforgeUnclassified({ window_days: 7, items: "no" }), null);
  // a row that is not one is dropped, never the whole report
  const partial = pure.sanitizePolyforgeUnclassified({
    window_days: 7,
    items: [
      { text: "ok", count: 1, last_at: "x" },
      { text: 7, count: 1, last_at: "x" },
      { text: "zero count", count: 0, last_at: "x" },
      { text: "no last_at", count: 1 },
    ],
  });
  assert.ok(partial);
  assert.equal(partial.items.length, 1, "only the one well-formed row survives");
  assert.equal(partial.windowDays, 7);
  // the bounds: a text is cut, the list is capped, the order kept
  const long = "x".repeat(pure.UNCLASSIFIED_TEXT_MAX + 50);
  const many = Array.from({ length: pure.UNCLASSIFIED_ITEMS_MAX + 20 }, (_, i) => ({
    text: `t${i}`, count: 1, last_at: "z",
  }));
  const bounded = pure.sanitizePolyforgeUnclassified({ window_days: 7, items: [{ ...many[0], text: long }, ...many] });
  assert.ok(bounded);
  assert.equal(bounded.items.length, pure.UNCLASSIFIED_ITEMS_MAX, "the list is capped");
  assert.equal(bounded.items[0].text.length, pure.UNCLASSIFIED_TEXT_MAX, "a runaway text is cut");
});

// AC4: the list is the daemon's — deduplicated texts with their occurrence
// counts, Host-wide, over a window of 7 days. A count is a number of
// occurrences (a positive integer), the window is the daemon's own (7 when
// it says nothing usable), and the order is the daemon's (most frequent
// first, then newest) — the panel never re-sorts it.
test("the sanitizer keeps the daemon's order, takes only occurrence counts, and falls back to AC4's 7-day window", () => {
  const rows = [
    { text: "zeta most frequent", count: 5, last_at: "2026-11-10T08:00:00Z" },
    { text: "half an occurrence", count: 1.5, last_at: "2026-11-15T08:00:00Z" },
    { text: "negative occurrences", count: -2, last_at: "2026-11-15T08:00:00Z" },
    { text: "string count", count: "3", last_at: "2026-11-15T08:00:00Z" },
    { text: "alpha newest", count: 2, last_at: "2026-11-16T08:00:00Z" },
  ];
  const kept = [
    { text: "zeta most frequent", count: 5, lastAt: "2026-11-10T08:00:00Z" },
    { text: "alpha newest", count: 2, lastAt: "2026-11-16T08:00:00Z" },
  ];
  assert.deepEqual(pure.sanitizePolyforgeUnclassified({ items: rows }), { windowDays: 7, items: kept },
    "no window_days: AC4's 7 days; only positive-integer counts; the daemon's order");
  assert.deepEqual(pure.sanitizePolyforgeUnclassified({ window_days: 0, items: [] }), { windowDays: 7, items: [] },
    "a zero window is not a window: 7 days");
  assert.deepEqual(pure.sanitizePolyforgeUnclassified({ window_days: 30, items: [] }), { windowDays: 30, items: [] },
    "the window is the daemon's own, never hard-coded");
});
