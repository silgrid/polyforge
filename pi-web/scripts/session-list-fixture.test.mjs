import "#test-env";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

// Unit tests for the synthetic session corpus generator (polyforge#140 u4):
// the manifest must be an independent record of what is actually on disk
// (bytes, mtime, sha256), every line must be real JSONL, every claude session
// must be registered in the synthetic index so it appears in the list, and
// the corpus must contain the shapes the oracles need (a >large-file single
// line, forks, several project dirs).

const packageRoot = process.cwd();
const script = path.join(packageRoot, "scripts", "session-list-fixture.mjs");

function generate(t, extra = []) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-fixture-"));
  const manifest = path.join(root, "manifest.json");
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const out = execFileSync(process.execPath, [
    script,
    "--root", root,
    "--pi-files", "9",
    "--pi-bytes", "400000",
    "--claude-files", "4",
    "--claude-bytes", "200000",
    "--large-file-min-bytes", "30000",
    "--project-dirs", "3",
    "--manifest", manifest,
    ...extra,
  ], { encoding: "utf8" });
  return { root, manifest, manifestPath: manifest, out: JSON.parse(out) };
}

test("the manifest records what is actually on disk", (t) => {
  const { root, manifestPath } = generate(t);
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  assert.equal(manifest.schema, "session-list-fixture/1");

  for (const group of ["pi", "claude"]) {
    assert.ok(manifest[group].length > 0, group);
    for (const entry of manifest[group]) {
      const file = path.join(root, entry.path);
      const stats = fs.statSync(file);
      assert.equal(stats.size, entry.bytes, `${entry.path}: bytes`);
      assert.equal(Math.round(stats.mtimeMs), Math.round(entry.mtimeMs), `${entry.path}: mtime`);
      const sha = createHash("sha256").update(fs.readFileSync(file)).digest("hex");
      assert.equal(sha, entry.sha256, `${entry.path}: sha256`);
    }
  }
  assert.equal(manifest.totals.piFiles, manifest.pi.length);
  assert.equal(manifest.totals.claudeFiles, manifest.claude.length);
  assert.equal(manifest.totals.piBytes, manifest.pi.reduce((sum, entry) => sum + entry.bytes, 0));
});

test("every session file is valid JSONL with the shapes the oracles need", (t) => {
  const { root, manifestPath } = generate(t);
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));

  for (const entry of manifest.pi) {
    const lines = fs.readFileSync(path.join(root, entry.path), "utf8").split("\n").filter((line) => line.trim());
    const header = JSON.parse(lines[0]);
    assert.equal(header.type, "session");
    assert.equal(typeof header.id, "string");
    assert.equal(typeof header.cwd, "string");
    for (const line of lines) JSON.parse(line);
  }
  const fork = manifest.pi.some((entry) => {
    const lines = fs.readFileSync(path.join(root, entry.path), "utf8").split("\n");
    return JSON.parse(lines[0]).parentSession !== undefined;
  });
  assert.ok(fork, "a fork row (parentSession) is part of the corpus");
  const dirs = new Set(manifest.pi.map((entry) => path.dirname(entry.path)));
  assert.ok(dirs.size >= 3, `several project dirs (${dirs.size})`);

  for (const entry of manifest.claude) {
    const lines = fs.readFileSync(path.join(root, entry.path), "utf8").split("\n").filter((line) => line.trim());
    for (const line of lines) JSON.parse(line);
    assert.ok(lines.some((line) => JSON.parse(line).type === "last-prompt"), `${entry.path}: a checkpoint row`);
  }
  assert.ok(manifest.totals.largeFiles >= 1, "a single line above the large-file bound exists");
  const bigEntry = [...manifest.pi, ...manifest.claude].find((entry) => entry.singleLineMax >= 30000);
  assert.ok(bigEntry, "the manifest names the large single line");
});

test("every claude session is registered in the synthetic index", (t) => {
  const { root, manifestPath } = generate(t);
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  const index = JSON.parse(fs.readFileSync(path.join(root, "agent", "claude-sessions.json"), "utf8"));
  assert.deepEqual(
    index.sessions.map((record) => record.id).sort(),
    manifest.claude.map((entry) => path.basename(entry.path, ".jsonl")).sort(),
  );
  for (const record of index.sessions) {
    assert.equal(typeof record.cwd, "string");
    assert.equal(typeof record.firstMessage, "string");
  }
});

test("the manifest's totals are the files' own arithmetic", (t) => {
  const { root, manifestPath } = generate(t);
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  for (const group of ["pi", "claude"]) {
    assert.equal(manifest.totals[`${group}Files`], manifest[group].length, `${group}: file count`);
    assert.equal(manifest.totals[`${group}Bytes`], manifest[group].reduce((sum, entry) => sum + entry.bytes, 0), `${group}: byte total`);
  }
  assert.equal(manifest.totals.projectDirs, new Set(manifest.pi.map((entry) => path.dirname(path.join(root, entry.path)))).size,
    "projectDirs is the number of distinct session dirs actually written");
  if (manifest.totals.projectDirs > 0) {
    const onDisk = fs.readdirSync(path.join(root, "agent", "sessions"), { withFileTypes: true }).length;
    assert.equal(onDisk, Math.min(3, manifest.totals.projectDirs), "the session dirs exist under the agent dir");
  }
});

test("the corpus is reproducible per seed and differs across seeds", (t) => {
  const first = generate(t, ["--seed", "7"]);
  const secondRoot = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-fixture-seed-"));
  t.after(() => fs.rmSync(secondRoot, { recursive: true, force: true }));
  execFileSync(process.execPath, [
    script, "--root", secondRoot,
    "--pi-files", "9", "--pi-bytes", "400000",
    "--claude-files", "4", "--claude-bytes", "200000",
    "--large-file-min-bytes", "30000", "--project-dirs", "3",
    "--seed", "7", "--manifest", path.join(secondRoot, "manifest.json"),
  ], { encoding: "utf8" });
  const same = JSON.parse(fs.readFileSync(path.join(secondRoot, "manifest.json"), "utf8"));
  assert.deepEqual(same.pi.map((entry) => entry.sha256), JSON.parse(fs.readFileSync(first.manifestPath, "utf8")).pi.map((entry) => entry.sha256),
    "the same seed regenerates the same corpus");

  const other = generate(t, ["--seed", "8"]);
  const otherManifest = JSON.parse(fs.readFileSync(other.manifestPath, "utf8"));
  const firstManifest = JSON.parse(fs.readFileSync(first.manifestPath, "utf8"));
  assert.notDeepEqual(otherManifest.pi.map((entry) => entry.sha256), firstManifest.pi.map((entry) => entry.sha256),
    "a different seed generates a different corpus");
});

test("the generator refuses nonsense arguments", () => {
  assert.throws(() => execFileSync(process.execPath, [script, "--root", "/tmp", "--manifest", "/tmp/m.json", "--pi-files", "0"], { encoding: "utf8" }));
  assert.throws(() => execFileSync(process.execPath, [script, "--manifest", "/tmp/m.json"], { encoding: "utf8" }));
});
