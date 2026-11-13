// Pins the tag-push contract of docs/release.md (pi#126): GitHub creates no
// push events for tags when more than three tags are pushed at once, so the
// release checklist must push only the release tag — never `--tags`. The
// v0.0.35 release pushed 58 tags in one `--tags` push and the `Release`
// workflow (on: push: tags: ["v*"]) silently never started.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const doc = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "release.md"), "utf8");

test("release.md never pushes with --tags", () => {
  assert.doesNotMatch(
    doc,
    /git push[^\n]*--tags/,
    "a `git push ... --tags` ships every local tag in one push; GitHub creates no tag push events when more than 3 tags are pushed at once, so the Release workflow silently never starts (v0.0.35, 2026-10-23)",
  );
});

test("release.md pushes the release tag explicitly", () => {
  assert.match(
    doc,
    /git push[^\n]*\bv<version>/,
    "§4 must push the release tag by name (git push origin main v<version>), not rely on a refspec that sweeps up other tags",
  );
});
