import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

const { buildSearchMatcher } = await import(new URL("../lib/tab-search.ts", import.meta.url));

test("literal matcher finds case-insensitively by default", () => {
  const matcher = buildSearchMatcher("hello", { caseSensitive: false, regex: false });
  assert.ok(matcher);
  const matches = matcher.findAll("say Hello, then hello again");
  assert.equal(matches.length, 2);
  assert.equal(matches[0].offset, 4);
  assert.equal(matches[0].length, 5);
  assert.equal(matches[1].offset, 16);
});

test("literal matcher honours the case-sensitive toggle", () => {
  const matcher = buildSearchMatcher("Hello", { caseSensitive: true, regex: false });
  assert.ok(matcher);
  assert.equal(matcher.findAll("say Hello, then hello again").length, 1);
});

test("literal matcher escapes regex metacharacters", () => {
  const matcher = buildSearchMatcher("a.b*c", { caseSensitive: false, regex: false });
  assert.ok(matcher);
  assert.equal(matcher.findAll("a.b*c not aXbXc").length, 1);
});

test("regex matcher supports alternation and anchors", () => {
  const matcher = buildSearchMatcher("\\berror\\d+\\b", { caseSensitive: true, regex: true });
  assert.ok(matcher);
  const matches = matcher.findAll("error404 and ERROR500 and noerror1");
  assert.equal(matches.length, 1);
  assert.equal(matches[0].length, 8);
});

test("invalid regex falls back to a literal search instead of matching nothing", () => {
  const matcher = buildSearchMatcher("a[b", { caseSensitive: false, regex: true });
  assert.ok(matcher);
  assert.equal(matcher.findAll("contains a[b here").length, 1);
});

test("empty query builds no matcher", () => {
  assert.equal(buildSearchMatcher("", { caseSensitive: false, regex: false }), null);
  assert.equal(buildSearchMatcher("   ", { caseSensitive: false, regex: false }), null);
});

test("zero-width regex matches do not loop forever", () => {
  const matcher = buildSearchMatcher("a*", { caseSensitive: false, regex: true });
  assert.ok(matcher);
  const matches = matcher.findAll("abc");
  assert.ok(matches.length > 0);
  assert.ok(matches.length < 100);
});
