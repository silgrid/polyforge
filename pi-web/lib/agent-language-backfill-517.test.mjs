// carries: lib/polyforge-config-write.ts, instrumentation-node.ts, docs/agents/settings-ui.md
// at Pi Web's start, an unset pf `[daemon] agent_language` of the
// default Cell is filled from Pi Web's stored Agent language; a key pf has set is
// never overwritten, and an older pf, no pf or a failing pf writes nothing.
import "#test-env";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

const originalAgentDir = process.env.PI_CODING_AGENT_DIR;
const originalPfBin = process.env.PF_BIN;
const agentDir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-agent-language-517-"));
const pfDir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-agent-language-517-pf-"));
// No machine pf is ever reached: a test that wants pf points PF_BIN at the stub.
const missingPf = path.join(pfDir, "no-such-pf");
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.PF_BIN = missingPf;
after(() => {
  fs.rmSync(agentDir, { recursive: true, force: true });
  fs.rmSync(pfDir, { recursive: true, force: true });
  if (originalAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = originalAgentDir;
  if (originalPfBin === undefined) delete process.env.PF_BIN;
  else process.env.PF_BIN = originalPfBin;
});

const jiti = createJiti(import.meta.url, { alias: { "@": path.resolve(import.meta.dirname, "..") }, interopDefault: true, moduleCache: false });
const { reconcilePolyforgeAgentLanguage } = await jiti.import("./polyforge-config-write.ts");
const { readStoredAgentLanguage } = await jiti.import("./agent-language-config.ts");
const file = path.join(agentDir, "pi-web-settings.json");

// A stateful pf stand-in: state.json holds { hasKey, source, value, listFails,
// racingWrite }; every argv is recorded; `config set agent_language X` makes the
// key source file. Like the real pf (internal/polyforge/cli/config_cas.go), the
// list reports the file's sha256 and a set carrying `--expect-state
// {"file_sha256": …}` that no longer matches is refused CORE-409, exit 1, with
// nothing written. `racingWrite: X` is another writer that sets the key to X
// right after the list was read (r1#1).
const fileHash = (state) => crypto.createHash("sha256").update(JSON.stringify({ agent_language: state.source === "file" ? state.value : null })).digest("hex");
const stubPf = path.join(pfDir, "pf");
const stubState = path.join(pfDir, "state.json");
const stubLog = path.join(pfDir, "argv.log");
fs.writeFileSync(stubPf, `#!${process.execPath}
const fs = require("fs"); const path = require("path"); const crypto = require("crypto");
const fileHash = ${fileHash.toString()};
const dir = __dirname;
const statePath = path.join(dir, "state.json");
const state = JSON.parse(fs.readFileSync(statePath, "utf8"));
const args = process.argv.slice(2);
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
const rest = args.filter((a) => a !== "--json");
const out = (doc) => { process.stdout.write(JSON.stringify(doc) + "\\n"); process.exit(0); };
const unknownKey = () => { process.stderr.write(JSON.stringify({ error: { code: "CLI-400", detail: "unknown key agent_language" } }) + "\\n"); process.exit(1); };
if (args.includes("--version")) { process.stdout.write("v2.30.0\\n"); process.exit(0); }
if (rest[0] === "version") out({ version: "v2.30.0", capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] });
const scoped = rest[0] === "--profile" && rest[1] === "default";
const verb = scoped ? rest.slice(2) : rest;
const value = state.source === "file" ? state.value : null;
if (verb[0] === "config" && verb[1] === "list") {
  if (state.listFails) { process.stderr.write(JSON.stringify({ error: { code: "CORE-500", detail: "config list failed in the stub" } }) + "\\n"); process.exit(1); }
  const items = [{ key: "step_timeout", value: "30m", source: "default", class: "live" }];
  if (state.hasKey) items.push({ key: "agent_language", value, source: state.source, class: "live", schema: { kind: "enum", values: ["en", "zh-CN"] } });
  const doc = { file: { path: "/x/cli.toml", sha256: fileHash(state), exists: true }, items };
  if (state.racingWrite) {
    const { racingWrite, ...before } = state;
    fs.writeFileSync(statePath, JSON.stringify({ ...before, source: "file", value: racingWrite }));
  }
  out(doc);
}
if (verb[0] === "config" && verb[1] === "get" && verb[2] === "agent_language") {
  if (!state.hasKey) unknownKey();
  out({ key: "agent_language", value, source: state.source });
}
if (verb[0] === "config" && verb[1] === "set" && verb[2] === "agent_language") {
  if (!state.hasKey) unknownKey();
  const at = verb.indexOf("--expect-state");
  if (at !== -1) {
    const want = JSON.parse(verb[at + 1]).file_sha256;
    if (want !== fileHash(state)) {
      process.stderr.write(JSON.stringify({ code: "CORE-409", detail: "the file changed since it was read", extra: { diff: [{ key: "agent_language", current: value }] } }) + "\\n");
      process.exit(1);
    }
  }
  fs.writeFileSync(statePath, JSON.stringify({ ...state, source: "file", value: verb[3] }));
  out({ outcome: "applied" });
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
fs.chmodSync(stubPf, 0o755);

function startStubPf(t, state) {
  fs.writeFileSync(stubState, JSON.stringify(state));
  fs.writeFileSync(stubLog, "");
  process.env.PF_BIN = stubPf;
  t.after(() => { process.env.PF_BIN = missingPf; });
}

function store(language) {
  if (language === undefined) fs.rmSync(file, { force: true });
  else fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: language }));
}

const pfCalls = () => fs.readFileSync(stubLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const setCalls = () => pfCalls().filter((args) => args.includes("set"));
const readState = () => JSON.parse(fs.readFileSync(stubState, "utf8"));

function pfGetAgentLanguage() {
  const run = spawnSync(stubPf, ["--profile", "default", "--json", "config", "get", "agent_language"], { encoding: "utf8" });
  assert.equal(run.status, 0, run.stderr);
  return JSON.parse(run.stdout);
}

test("startup backfill: pf unset and Pi Web zh-CN → pf config get agent_language is zh-CN", async (t) => {
  startStubPf(t, { hasKey: true, source: "default", value: null, listFails: false });
  store("zh-CN");
  const result = await reconcilePolyforgeAgentLanguage(readStoredAgentLanguage());
  assert.deepEqual(result, { language: "zh-CN", source: "file" });
  // r1#1: the set carries the CAS on the hash its own list read.
  const expectState = JSON.stringify({ file_sha256: fileHash({ source: "default", value: null }) });
  assert.deepEqual(setCalls(), [["--profile", "default", "--json", "config", "set", "agent_language", "zh-CN", "--expect-state", expectState]]);
  const got = pfGetAgentLanguage();
  assert.equal(got.value, "zh-CN");
  assert.equal(got.source, "file");
});

test("startup backfill: pf explicitly en and Pi Web zh-CN → pf stays en, no set", async (t) => {
  startStubPf(t, { hasKey: true, source: "file", value: "en", listFails: false });
  store("zh-CN");
  const result = await reconcilePolyforgeAgentLanguage(readStoredAgentLanguage());
  assert.deepEqual(result, { language: "en", source: "file" });
  assert.ok(pfCalls().some((args) => args.join(" ") === "--profile default --json config list"), "pf's state is read from the default Cell's config list");
  assert.deepEqual(setCalls(), [], "no set is spawned");
  const got = pfGetAgentLanguage();
  assert.equal(got.value, "en");
  assert.equal(got.source, "file");
});

test("startup backfill: a pf without the key resolves without error and writes nothing", async (t) => {
  store("zh-CN");

  const older = { hasKey: false, source: "default", value: null, listFails: false };
  startStubPf(t, older);
  assert.equal(await reconcilePolyforgeAgentLanguage(readStoredAgentLanguage()), null);
  assert.ok(pfCalls().some((args) => args.includes("list")), "the list was read");
  assert.deepEqual(setCalls(), [], "no set is spawned");
  assert.deepEqual(readState(), older, "pf's state is unchanged");

  const failing = { hasKey: true, source: "default", value: null, listFails: true };
  startStubPf(t, failing);
  assert.equal(await reconcilePolyforgeAgentLanguage(readStoredAgentLanguage()), null);
  assert.ok(pfCalls().some((args) => args.includes("list")), "the list was read");
  assert.deepEqual(setCalls(), [], "no set is spawned");
  assert.deepEqual(readState(), failing, "pf's state is unchanged");

  process.env.PF_BIN = missingPf;
  assert.equal(await reconcilePolyforgeAgentLanguage(readStoredAgentLanguage()), null);
});

test("startup backfill: Pi Web on the default (en) and pf unset → nothing written", async (t) => {
  for (const language of [undefined, "en"]) {
    const unset = { hasKey: true, source: "default", value: null, listFails: false };
    startStubPf(t, unset);
    store(language);
    const result = await reconcilePolyforgeAgentLanguage(readStoredAgentLanguage());
    assert.deepEqual(result, { language: "en", source: "default" }, `stored ${language ?? "nothing"}`);
    assert.deepEqual(setCalls(), [], `no set is spawned (stored ${language ?? "nothing"})`);
    assert.deepEqual(readState(), unset, "pf's state is unchanged");
  }
});

test("startup backfill: a pf written explicitly between the read and the set keeps its value ( r1#1)", async (t) => {
  startStubPf(t, { hasKey: true, source: "default", value: null, listFails: false, racingWrite: "en" });
  store("zh-CN");
  const result = await reconcilePolyforgeAgentLanguage(readStoredAgentLanguage());
  const got = pfGetAgentLanguage();
  assert.equal(got.value, "en", "the explicit write that landed between the list and the set stays");
  assert.equal(got.source, "file");
  assert.deepEqual(result, { language: "en", source: "file" });
  assert.equal(setCalls().length, 1, "a refused set is not retried");
});
