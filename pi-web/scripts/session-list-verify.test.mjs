import "#test-env";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

// Unit tests for the session-list oracle CLI (polyforge#140 u5): the modes are
// the work item's acceptance commands, so the contract they parse is pinned
// here — argument validation, the fixture manifest's totals, and the pure
// golden comparison rules (strict normal/force; summary allows only the
// owner-ruled deferred claude count). The live runs belong to verification on
// the synthetic corpus; --dry-run validates everything without spawning.

const packageRoot = process.cwd();
const script = path.join(packageRoot, "scripts", "session-list-verify.mjs");

function corpus(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-verify-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  execFileSync(process.execPath, [
    path.join(packageRoot, "scripts", "session-list-fixture.mjs"),
    "--root", root,
    "--pi-files", "3",
    "--pi-bytes", "50000",
    "--claude-files", "2",
    "--claude-bytes", "20000",
    "--large-file-min-bytes", "10000",
    "--manifest", path.join(root, "manifest.json"),
  ], { encoding: "utf8" });
  return root;
}

function dry(mode, fixture, extra = []) {
  const result = spawnSync(process.execPath, [
    script, mode, "--fixture", fixture, "--out", path.join(fixture, "out"), "--dry-run", ...extra,
  ], { encoding: "utf8" });
  return result;
}

test("each mode's dry run resolves the plan from the ticket's arguments", (t) => {
  const fixture = corpus(t);
  const cases = [
    ["responsiveness", ["--modes", "summary,normal,force", "--static", "/icons/icon-192.png", "--interval-ms", "100", "--min-samples", "100", "--p99-ms", "200"], (plan) => {
      assert.deepEqual(plan.modes, ["summary", "normal", "force"]);
      assert.equal(plan.p99Ms, 200);
      assert.equal(plan.minSamples, 100);
    }],
    ["read-volume", ["--modes", "normal,summary", "--calls", "2", "--max-second-read-ratio", "0.05"], (plan) => {
      assert.deepEqual(plan.modes, ["normal", "summary"]);
      assert.equal(plan.maxSecondReadRatio, 0.05);
    }],
    ["cold-summary", ["--empty-index", "--max-ms", "1000", "--max-read-ratio", "0.10"], (plan) => {
      assert.equal(plan.maxMs, 1000);
      assert.equal(plan.maxReadRatio, 0.10);
      assert.equal(plan.emptyIndex, true);
    }],
    ["golden", ["--baseline", "HEAD", "--candidate", "HEAD"], (plan) => {
      assert.match(plan.baseline, /^[0-9a-f]{40}$/);
      assert.deepEqual(plan.transientFields, ["sessionListVersion"]);
    }],
    ["mutations", ["--harnesses", "pi,claude", "--changes", "create,append,delete"], (plan) => {
      assert.deepEqual(plan.harnesses, ["pi", "claude"]);
      assert.deepEqual(plan.changes, ["create", "append", "delete"]);
    }],
  ];
  for (const [mode, extra, check] of cases) {
    const result = dry(mode, fixture, extra);
    assert.equal(result.status, 0, `${mode}: ${result.stdout}\n${result.stderr}`);
    const plan = JSON.parse(result.stdout);
    assert.equal(plan.dryRun, true);
    assert.equal(plan.mode, mode);
    assert.equal(path.resolve(plan.fixture), fixture);
    assert.equal(plan.manifestTotals.piFiles, 3);
    assert.equal(plan.manifestTotals.claudeFiles, 2);
    assert.ok(plan.manifestTotals.piBytes > 0);
    check(plan);
  }
});

test("the CLI refuses unknown modes, bad values and missing arguments", (t) => {
  const fixture = corpus(t);
  const refuses = [
    ["nonsense", []],
    ["read-volume", ["--modes", "summarize"]],
    ["mutations", ["--harnesses", "both"]],
    ["responsiveness", ["--p99-ms", "zero"]],
    ["golden", ["--baseline", "no-such-revision-anywhere"]],
  ];
  for (const [mode, extra] of refuses) {
    const result = dry(mode, fixture, extra);
    assert.equal(result.status, 2, `${mode} ${extra}: expected usage refusal`);
    assert.match(result.stderr, /session-list-verify:/);
  }
  assert.equal(dry(undefined, fixture, []).status, 2);
  const missing = spawnSync(process.execPath, [script, "responsiveness", "--dry-run"], { encoding: "utf8" });
  assert.equal(missing.status, 2);
  // A fixture without a manifest is refused, not guessed.
  const empty = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-verify-empty-"));
  t.after(() => fs.rmSync(empty, { recursive: true, force: true }));
  assert.equal(dry("read-volume", empty, []).status, 2);
});

test("the golden comparison is strict everywhere but the deferred claude count", async () => {
  const { _diffValues, _allowedSummaryDifferences } = await import("./session-list-verify-run.mjs");
  assert.deepEqual(_diffValues({ a: 1, b: [1, 2] }, { a: 1, b: [1, 2] }), []);
  assert.deepEqual(
    _diffValues({ a: 1, sessions: [{ id: "x", messageCount: 2 }] }, { a: 1, sessions: [{ id: "x", messageCount: 3 }] }),
    [{ path: "sessions[0].messageCount", baseline: 2, candidate: 3 }],
  );
  const allowed = _allowedSummaryDifferences(
    [{ id: "c1", harness: "claude", messageCount: 4 }],
    [{ id: "c1", harness: "claude", messageCount: 0, detailsPending: true }],
  );
  assert.equal(allowed.length, 1, "a deferred claude row's count difference is named");
  assert.equal(
    _allowedSummaryDifferences(
      [{ id: "c1", harness: "claude", messageCount: 4 }],
      [{ id: "c1", harness: "claude", messageCount: 0 }],
    ).length,
    0,
    "a count difference without detailsPending is not allowed",
  );
});
