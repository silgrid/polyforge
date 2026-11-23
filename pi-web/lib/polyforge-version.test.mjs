// carries: lib/polyforge-version.ts
// polyforge#256 (T1, B1/B2): the browser-safe pf version precedence. The
// expected values are hand-derived from SemVer 2.0 §11 (core, then
// pre-release identifiers; build metadata ignored) and from the ticket's
// settled floor `0.2.4-0` — a 0.2.4-dev.N build is above its line's
// pre-release base and below the 0.2.4 release, while 0.2.3 and every
// 2.20.x build stay below. The line cutover (v0.2.x above v2.20.x) is the
// repository's existing product convention, pinned here so the install card
// and the step-tier gate share one comparator.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const m = await jiti.import("./polyforge-version.ts");

test("the core and pre-release precedence: numeric identifiers by number, alphanumeric in ASCII order, a shorter prefix first", () => {
  const s = (...pair) => m.comparePolyforgeVersions(...pair);
  assert.ok(s("v0.2.4-dev.10", "v0.2.4-dev.9") > 0, "dev.10 is above dev.9");
  assert.ok(s("v0.2.4-dev.9", "v0.2.4-dev.10") < 0);
  assert.equal(s("v0.2.4", "0.2.4"), 0, "a leading v does not count");
  assert.ok(s("v0.2.4", "v0.2.4-dev.49") > 0, "the release is above its pre-releases");
  assert.ok(s("v0.2.4-dev.49", "v0.2.4") < 0);
  assert.ok(s("v0.2.3", "v0.2.4-dev.49") < 0, "the core decides before the pre-release");
  assert.ok(s("v0.2.4-rc.1", "v0.2.4-dev.49") > 0, "rc is alphanumerically above dev");
  assert.ok(s("v0.2.4-1", "v0.2.4-dev") < 0, "a numeric identifier is below an alphanumeric one");
  assert.equal(s("v0.2.4+build.9", "v0.2.4"), 0, "build metadata is not a version difference");
  assert.equal(s("v0.2.4+build.9", "v0.2.4+build.10"), 0);
  assert.equal(m.comparePolyforgeVersions("banana", "v0.2.4"), null, "unparseable proves nothing");
  assert.equal(m.comparePolyforgeVersions("v0.2.4", null), null);
});

test("the named v0.2.x line ranks above every v2.20.x build; v0.1.x stays below", () => {
  const s = (...pair) => m.comparePolyforgeVersions(...pair);
  assert.ok(s("v0.2.0", "v2.20.21") > 0);
  assert.ok(s("v0.2.4-dev.49", "v2.20.21") > 0);
  assert.ok(s("v0.1.9", "v2.20.12") < 0);
  assert.ok(s("v2.20.11", "v2.20.12") < 0);
  assert.ok(s("v0.2.0", "v0.2.1") < 0, "inside the v0.2 line the compare is ordinary");
});

test("the order helper: ahead / behind / equal, null when either side is unparseable", () => {
  assert.equal(m.comparePolyforgeVersionOrder("v0.2.4-dev.49", "v0.2.3"), "ahead");
  assert.equal(m.comparePolyforgeVersionOrder("v0.2.3", "v0.2.4-dev.49"), "behind");
  assert.equal(m.comparePolyforgeVersionOrder("v0.2.4", "v0.2.4+meta"), "equal");
  assert.equal(m.comparePolyforgeVersionOrder("oops", "v0.2.4"), null);
});

test("a pre-release is a version token with a pre-release suffix", () => {
  for (const version of ["v0.2.4-dev.0", "0.2.4-dev.49", "v0.2.4-rc.1", "2.20.21-e2e"]) {
    assert.equal(m.isPolyforgePrerelease(version), true, version);
  }
  for (const version of ["v0.2.4", "0.2.4+build.9", "not-a-version"]) {
    assert.equal(m.isPolyforgePrerelease(version), false, version);
  }
});

test("the step-tier floor 0.2.4-0 admits the 0.2.4 line's dev.N builds and still refuses 0.2.3, 2.20.x and garbage", () => {
  const floor = "0.2.4-0";
  for (const version of ["v0.2.4-dev.0", "v0.2.4-dev.49", "v0.2.4-dev.999", "0.2.4-0", "v0.2.4"]) {
    assert.equal(m.isPolyforgeVersionBelowMinimum(version, floor, true), false, `${version} is capable`);
  }
  for (const version of ["v0.2.3", "v2.20.99", null, "banana"]) {
    assert.equal(m.isPolyforgeVersionBelowMinimum(version, floor, true), true, `${version} is below the floor`);
  }
  // the default floor's unknown policy is unchanged: unknown is never a guess
  assert.equal(m.isPolyforgeVersionBelowMinimum(null, floor), false);
  assert.equal(m.isPolyforgeVersionBelowMinimum("banana", floor), false);
});
