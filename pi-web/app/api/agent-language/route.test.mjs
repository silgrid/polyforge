// carries: app/api/agent-language/*, lib/api-types.ts, lib/polyforge-config-write.ts, AGENTS.md
import "#test-env";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";
import { NextRequest } from "next/server.js";

const originalAgentDir = process.env.PI_CODING_AGENT_DIR;
const originalPfBin = process.env.PF_BIN;
const agentDir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-agent-language-route-"));
const pfDir = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-agent-language-pf-"));
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

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const { GET, PUT } = await jiti.import("./route.ts");
const file = path.join(agentDir, "pi-web-settings.json");

function put(body, headers = {}) {
  return new NextRequest("http://localhost/api/agent-language", {
    method: "PUT",
    headers: { Host: "localhost", Origin: "http://localhost", "Sec-Fetch-Site": "same-origin", "Content-Type": "application/json", ...headers },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
}

async function read() {
  const response = await GET();
  assert.equal(response.status, 200);
  return (await response.json()).language;
}

test("GET reads English by default and after a corrupted file", async () => {
  fs.rmSync(file, { force: true });
  assert.equal(await read(), "en");
  fs.writeFileSync(file, "{ broken");
  assert.equal(await read(), "en");
  fs.rmSync(file, { force: true });
});

test("PUT saves and returns the language", async () => {
  const response = await PUT(put({ language: "zh-CN" }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { language: "zh-CN" });
  assert.equal(await read(), "zh-CN");
  assert.equal(JSON.parse(fs.readFileSync(file, "utf8")).agentLanguage, "zh-CN");
});

test("PUT refuses untrusted requests, wrong content types and invalid bodies without writing", async () => {
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "zh-CN" }));
  const before = fs.readFileSync(file, "utf8");
  const cases = [
    [put({ language: "en" }, { Origin: "https://evil.example", "Sec-Fetch-Site": "cross-site" }), 403],
    [put({ language: "en" }, { Host: "evil.example" }), 403],
    [put({ language: "en" }, { "Content-Type": "text/plain" }), 415],
    [put("{ nope"), 400],
    [put(["en"]), 400],
    [put({ language: "klingon" }), 400],
    [put({ language: "en", extra: 1 }), 400],
    [put({}), 400],
  ];
  for (const [request, status] of cases) {
    const response = await PUT(request);
    assert.equal(response.status, status);
    assert.equal(typeof (await response.json()).error, "string");
  }
  assert.equal(fs.readFileSync(file, "utf8"), before);
});

test("PUT reports a refused write of a malformed file as 500 and keeps the file", async () => {
  fs.writeFileSync(file, "{ broken");
  const response = await PUT(put({ language: "zh-CN" }));
  assert.equal(response.status, 500);
  assert.equal(fs.readFileSync(file, "utf8"), "{ broken");
  fs.rmSync(file, { force: true });
});

// the save also writes pf's `[daemon] agent_language` of the
// default Cell, through `pf config set`, when the installed pf has the key.
// A stub pf (PF_BIN) records every argv; its behaviour is read from state.json.
// Like the real pf (internal/polyforge/cli/config_cas.go), the list reports the
// file's sha256 and a set whose `--expect-state {"file_sha256": …}` no longer
// matches is refused CORE-409, exit 1, with nothing written ( r1#1).
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
if (args.includes("--version")) { process.stdout.write("v2.30.0\\n"); process.exit(0); }
if (rest[0] === "version") out({ version: "v2.30.0", capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] });
const scoped = rest[0] === "--profile" && rest[1] === "default";
const verb = scoped ? rest.slice(2) : rest;
if (verb[0] === "config" && verb[1] === "list") {
  if (state.listFails) { process.stderr.write(JSON.stringify({ error: { code: "CORE-500", detail: "config list failed in the stub" } }) + "\\n"); process.exit(1); }
  if (state.listGarbage) { process.stdout.write("this is not json\\n"); process.exit(0); }
  const items = [{ key: "step_timeout", value: "30m", source: "default", class: "live" }];
  // Like the real pf: a key pf's file does not set lists source default, value null.
  const source = state.source === "file" ? "file" : "default";
  const value = source === "file" ? state.value : null;
  if (state.hasKey) items.push({ key: "agent_language", value, source, class: "live", schema: { kind: "enum", values: ["en", "zh-CN"] } });
  out({ file: { path: "/x/cli.toml", sha256: fileHash(state), exists: true }, items });
}
if (verb[0] === "config" && verb[1] === "set") {
  if (state.refuse) { process.stderr.write(JSON.stringify({ error: { code: "CORE-400", detail: "agent_language refused by the stub" } }) + "\\n"); process.exit(1); }
  const at = verb.indexOf("--expect-state");
  if (at !== -1 && JSON.parse(verb[at + 1]).file_sha256 !== fileHash(state)) {
    process.stderr.write(JSON.stringify({ code: "CORE-409", detail: "the file changed since it was read" }) + "\\n");
    process.exit(1);
  }
  if (verb[2] === "agent_language") fs.writeFileSync(statePath, JSON.stringify({ ...state, source: "file", value: verb[3] }));
  out({ outcome: "applied" });
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
fs.chmodSync(stubPf, 0o755);

function useStubPf(t, state) {
  fs.writeFileSync(stubState, JSON.stringify(state));
  fs.writeFileSync(stubLog, "");
  process.env.PF_BIN = stubPf;
  t.after(() => { process.env.PF_BIN = missingPf; });
}

const pfCalls = () => fs.readFileSync(stubLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const setCalls = () => pfCalls().filter((args) => args.includes("set"));

test("PUT writes [daemon] agent_language through pf config set on the default Cell, then the local file", async (t) => {
  useStubPf(t, { hasKey: true, refuse: false });
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "en" }));
  const response = await PUT(put({ language: "zh-CN" }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { language: "zh-CN" });
  assert.deepEqual(setCalls(), [["--profile", "default", "--json", "config", "set", "agent_language", "zh-CN"]]);
  assert.ok(pfCalls().some((args) => args.join(" ") === "--profile default --json config list"), "the capability is read from the default Cell's config list");
  assert.equal(JSON.parse(fs.readFileSync(file, "utf8")).agentLanguage, "zh-CN");
});

test("PUT answers pf's refusal and writes nothing local", async (t) => {
  useStubPf(t, { hasKey: true, refuse: true });
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "en" }));
  const before = fs.readFileSync(file, "utf8");
  const response = await PUT(put({ language: "zh-CN" }));
  assert.ok(response.status >= 400, `a refusal is not a success (got ${response.status})`);
  assert.match((await response.json()).error, /refused by the stub/);
  assert.equal(setCalls().length, 1, "the set was attempted");
  assert.equal(fs.readFileSync(file, "utf8"), before);
});

test("PUT writes only locally when the installed pf has no agent_language key", async (t) => {
  useStubPf(t, { hasKey: false, refuse: false });
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "en" }));
  const response = await PUT(put({ language: "zh-CN" }));
  assert.equal(response.status, 200);
  assert.ok(pfCalls().some((args) => args.includes("list")), "the list was read");
  assert.deepEqual(setCalls(), [], "no set is spawned");
  assert.equal(JSON.parse(fs.readFileSync(file, "utf8")).agentLanguage, "zh-CN");
});

test("PUT writes only locally when pf is not installed", async () => {
  assert.equal(process.env.PF_BIN, missingPf);
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "zh-CN" }));
  const response = await PUT(put({ language: "en" }));
  assert.equal(response.status, 200);
  assert.equal(JSON.parse(fs.readFileSync(file, "utf8")).agentLanguage, "en");
});

// Self-code review: a config list that fails or prints non-JSON counts as a pf
// without the key — the save writes locally only, and no set is spawned.
test("PUT writes only locally when pf config list exits non-zero", async (t) => {
  useStubPf(t, { hasKey: true, refuse: false, listFails: true });
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "en" }));
  const response = await PUT(put({ language: "zh-CN" }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { language: "zh-CN" });
  assert.ok(pfCalls().some((args) => args.includes("list")), "the list was read");
  assert.deepEqual(setCalls(), [], "no set is spawned");
  assert.equal(JSON.parse(fs.readFileSync(file, "utf8")).agentLanguage, "zh-CN");
});

test("PUT writes only locally when pf config list prints non-JSON", async (t) => {
  useStubPf(t, { hasKey: true, refuse: false, listGarbage: true });
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "en" }));
  const response = await PUT(put({ language: "zh-CN" }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { language: "zh-CN" });
  assert.ok(pfCalls().some((args) => args.includes("list")), "the list was read");
  assert.deepEqual(setCalls(), [], "no set is spawned");
  assert.equal(JSON.parse(fs.readFileSync(file, "utf8")).agentLanguage, "zh-CN");
});

// Self-code review: the local file is checked before pf is written, so a
// malformed pi-web-settings.json refuses the save with pf untouched.
test("PUT refuses a malformed local file before pf config set is spawned", async (t) => {
  useStubPf(t, { hasKey: true, refuse: false });
  fs.writeFileSync(file, "{ broken");
  const response = await PUT(put({ language: "zh-CN" }));
  assert.ok(response.status >= 400, `a malformed local file refuses the save (got ${response.status})`);
  assert.deepEqual(setCalls(), [], "no set is spawned");
  assert.equal(fs.readFileSync(file, "utf8"), "{ broken");
  fs.rmSync(file, { force: true });
});

// GET answers pf's actual `[daemon] agent_language` of the
// default Cell as `polyforge` (null for no pf or an older pf).
// GET only reads it — one `pf config list`, never a set; the
// backfill of an unset key runs at Pi Web's start only
// (lib/agent-language-backfill-517.test.mjs).
async function getBody() {
  const response = await GET();
  assert.equal(response.status, 200);
  return response.json();
}

test("GET reports an unset pf without backfilling it", async (t) => {
  useStubPf(t, { hasKey: true, refuse: false, source: "default" });
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "zh-CN" }));
  const stateBefore = fs.readFileSync(stubState, "utf8");
  assert.deepEqual(await getBody(), { language: "zh-CN", polyforge: { language: "en", source: "default" } });
  assert.deepEqual(setCalls(), [], "no set is spawned");
  assert.equal(fs.readFileSync(stubState, "utf8"), stateBefore);
});

test("GET spawns at most one pf and never a set", async (t) => {
  const cases = [
    ["pf unset, stored zh-CN", { hasKey: true, refuse: false, source: "default" }, { language: "en", source: "default" }],
    ["pf file sets en, stored zh-CN", { hasKey: true, refuse: false, source: "file", value: "en" }, { language: "en", source: "file" }],
    ["pf without the key", { hasKey: false, refuse: false }, null],
    ["pf config list fails", { hasKey: true, refuse: false, listFails: true }, null],
  ];
  useStubPf(t, cases[0][1]);
  for (const [name, state, polyforge] of cases) {
    fs.writeFileSync(stubState, JSON.stringify(state));
    fs.writeFileSync(stubLog, "");
    fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "zh-CN" }));
    const stateBefore = fs.readFileSync(stubState, "utf8");
    const response = await GET();
    assert.equal(response.status, 200, name);
    assert.deepEqual(await response.json(), { language: "zh-CN", polyforge }, name);
    assert.ok(pfCalls().length <= 1, `${name}: at most one pf spawn (got ${JSON.stringify(pfCalls())})`);
    assert.deepEqual(setCalls(), [], `${name}: no set is spawned`);
    assert.equal(fs.readFileSync(stubState, "utf8"), stateBefore, `${name}: pf's state is unchanged`);
    if (name === "pf unset, stored zh-CN") {
      assert.deepEqual(pfCalls(), [["--profile", "default", "--json", "config", "list"]], `${name}: the one spawn is the default Cell's config list`);
    }
  }
});

test("GET leaves an explicitly set pf alone and reports the mismatch", async (t) => {
  useStubPf(t, { hasKey: true, refuse: false, source: "file", value: "en" });
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "zh-CN" }));
  const body = await getBody();
  assert.equal(body.language, "zh-CN");
  assert.deepEqual(body.polyforge, { language: "en", source: "file" });
  assert.deepEqual(setCalls(), [], "no set is spawned");
});

test("GET with a pf that lacks the key answers 200 with polyforge null and writes nothing", async (t) => {
  useStubPf(t, { hasKey: false, refuse: false });
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "zh-CN" }));
  const stateBefore = fs.readFileSync(stubState, "utf8");
  assert.deepEqual(await getBody(), { language: "zh-CN", polyforge: null });
  assert.ok(pfCalls().some((args) => args.includes("list")), "the list was read");
  assert.deepEqual(setCalls(), [], "no set is spawned");
  assert.equal(fs.readFileSync(stubState, "utf8"), stateBefore);
  process.env.PF_BIN = missingPf;
  assert.deepEqual(await getBody(), { language: "zh-CN", polyforge: null }, "no pf installed answers polyforge null");
});

test("a sync (PUT of the stored language) makes GET report pf and Pi Web equal", async (t) => {
  useStubPf(t, { hasKey: true, refuse: false, source: "file", value: "en" });
  fs.writeFileSync(file, JSON.stringify({ version: 1, agentLanguage: "zh-CN" }));
  const response = await PUT(put({ language: "zh-CN" }));
  assert.equal(response.status, 200);
  const body = await getBody();
  assert.equal(body.language, "zh-CN");
  assert.equal(body.polyforge?.language, body.language, "pf and Pi Web report the same language");
});
