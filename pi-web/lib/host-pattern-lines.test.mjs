// carries: lib/host-pattern-lines.ts
// polyforge#51 / #277: a Host pattern list is edited one regex per line; a save
// writes each line trimmed with blank lines dropped, and a landed save settles
// the editor to exactly those lines. Expected values are hand-derived from that
// rule, not taken from the implementation.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

const { patternLines } = await import("./host-pattern-lines.ts");

test("blank and whitespace-only lines are dropped, every line is trimmed", () => {
  assert.deepEqual(patternLines("\\b402\\b\npayment retry\n"), ["\\b402\\b", "payment retry"]);
  assert.deepEqual(patternLines("  a  \n\n   \n\tb\t\n"), ["a", "b"]);
});

test("an empty or blank text writes no pattern", () => {
  assert.deepEqual(patternLines(""), []);
  assert.deepEqual(patternLines("\n \n"), []);
});

test("a regex's own inner spaces are kept", () => {
  assert.deepEqual(patternLines("\\bupstream\\b hiccup"), ["\\bupstream\\b hiccup"]);
});
