import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const { validatePositionalCliArgument } = await createJiti(import.meta.url, {
  interopDefault: true,
}).import("./skills-cli-args.ts");

test("ordinary package and query strings pass as positionals", () => {
  assert.deepEqual(validatePositionalCliArgument("  owner/repo "), { ok: true, value: "owner/repo" });
  assert.deepEqual(validatePositionalCliArgument("find me a skill"), { ok: true, value: "find me a skill" });
  assert.deepEqual(validatePositionalCliArgument("https://example.com/skill.md"), {
    ok: true,
    value: "https://example.com/skill.md",
  });
});

test("leading-dash strings are refused with the typed code", () => {
  // Verified against skills@1.5.21: parseAddOptions swallows these as option
  // flags (-g flips a project install to a global one) and `--` is not an
  // end-of-options marker there, so the route must refuse them.
  for (const value of ["-g", "--global", "--metadata", "--owner=evil", "-", "--"]) {
    assert.deepEqual(validatePositionalCliArgument(value), {
      ok: false,
      reason: "leadingDashCliArgument",
    }, value);
  }
});

test("an interior dash or a leading dash inside a longer sentence stays a positional", () => {
  assert.equal(validatePositionalCliArgument("some-package").ok, true);
  assert.equal(validatePositionalCliArgument("a --fake flag in the middle").ok, true);
  assert.deepEqual(validatePositionalCliArgument("a --fake flag in the middle"), {
    ok: true,
    value: "a --fake flag in the middle",
  });
});
