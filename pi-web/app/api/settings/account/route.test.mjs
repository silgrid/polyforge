// carries: app/api/settings/account/route.ts, lib/harness/account.ts, lib/polyforge-doctor.ts, lib/harness/claude.ts
// polyforge#142 (85a2, AC1/AC6): the account route against stubbed claude,
// pi and pf binaries and a throwaway HOME. The GET composes the
// interactive claude card and the per-Cell rows from one doctor read (the
// three Cell kinds: default, shared-default, an independent source); the
// POST writes a credential into the native env block of the interactive
// session and of the checked Cells' claude-config. Every sentinel planted
// in the fixtures — and every value the tests write — must survive in the
// files alone: never in a response, never in an argv the stubs receive.
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const here = path.dirname(new URL(import.meta.url).pathname);
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-account-route-"));
const home = path.join(scratch, "home");
const data = path.join(scratch, "data");
const opDir = path.join(home, ".pi", "agent");
const ownDir = path.join(scratch, "own-agent");
const claudeHome = path.join(home, ".claude");
const binDir = path.join(scratch, "bin");
for (const dir of [opDir, claudeHome, binDir, data,
  path.join(data, "cells", "default", "claude-config"),
  path.join(data, "cells", "beta", "claude-config"),
  path.join(data, "cells", "gamma", "claude-config"),
  path.join(data, "cells", "default", "step-agent"),
  ownDir]) {
  fs.mkdirSync(dir, { recursive: true });
}
// the interactive session's settings.json: a gateway pair plus an OAuth
// mint, all sentinel values
fs.writeFileSync(path.join(claudeHome, "settings.json"), JSON.stringify({
  model: "opus",
  env: {
    ANTHROPIC_AUTH_TOKEN: "RT-SENTINEL-INTER-TOKEN",
    ANTHROPIC_BASE_URL: "https://gw.example",
    CLAUDE_CODE_OAUTH_TOKEN: "sk-ant-oat01-RT-SENTINEL-INTER-OAUTH",
    OTHER_FLAG: "plain",
  },
}, null, 2));
// the Cells' claude-config settings.json files (the pf stub names these roots)
const cellSettings = (dir, env) => fs.writeFileSync(path.join(data, "cells", dir, "claude-config", "settings.json"), JSON.stringify({ model: "opus", env }, null, 2));
cellSettings("default", { ANTHROPIC_AUTH_TOKEN: "RT-SENTINEL-DEF-TOKEN" });
cellSettings("beta", { OTHER_FLAG: "kept" });
// gamma's is torn: an unrecognized format must be refused, never overwritten
fs.writeFileSync(path.join(data, "cells", "gamma", "claude-config", "settings.json"), "{not json");
// the pi credential sources the pf stub reports
fs.writeFileSync(path.join(opDir, "settings.json"), JSON.stringify({ defaultProvider: "sub2api" }));
fs.writeFileSync(path.join(opDir, "auth.json"), JSON.stringify({ sub2api: { type: "api_key", key: "RT-SENTINEL-PI-OP" } }));
fs.writeFileSync(path.join(ownDir, "settings.json"), JSON.stringify({ defaultProvider: "kimi" }));
fs.writeFileSync(path.join(ownDir, "auth.json"), JSON.stringify({ kimi: { type: "oauth", key: "RT-SENTINEL-PI-OWN" } }));

// the doctor answer of the pf stub: the three Cell kinds
const DOCTOR = JSON.stringify({
  ok: true, checks: [],
  cells: {
    default: {
      harnesses: {
        pi: { bin: "pi", found: true, config_root: path.join(data, "cells", "default", "step-agent"), credential: "set", source: opDir, source_kind: "operator" },
        claude: { bin: "claude", found: true, config_root: path.join(data, "cells", "default", "claude-config"), source: opDir, source_kind: "operator",
          settings_env: { ANTHROPIC_AUTH_TOKEN: "set", ANTHROPIC_API_KEY: "unset", ANTHROPIC_BASE_URL: "set", CLAUDE_CODE_OAUTH_TOKEN: "unset" }, credentials_file: "absent" },
      },
    },
    beta: {
      harnesses: {
        pi: { config_root: path.join(data, "cells", "beta", "step-agent"), credential: "unset", source: opDir, source_kind: "shared-default" },
        claude: { config_root: path.join(data, "cells", "beta", "claude-config"), source: opDir, source_kind: "shared-default", settings_env: {}, credentials_file: "absent" },
      },
    },
    gamma: {
      harnesses: {
        pi: { config_root: path.join(data, "cells", "gamma", "step-agent"), credential: "set", source: ownDir, source_kind: "directory" },
        claude: { config_root: path.join(data, "cells", "gamma", "claude-config"), source: ownDir, source_kind: "directory", settings_env: {}, credentials_file: "absent" },
      },
    },
  },
});

// the stub binaries. claude answers `auth status` itself and delegates the
// initialize probe to the recorded stub; pi answers per PI_CODING_AGENT_DIR;
// pf answers the doctor. Each records the argv it received (never a value).
const claudeLog = path.join(scratch, "claude-calls.jsonl");
const piLog = path.join(scratch, "pi-calls.jsonl");
const pfLog = path.join(scratch, "pf-calls.jsonl");
fs.writeFileSync(path.join(binDir, "claude"), `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  printf '%s\\n' '{"argv":"'"$*"'"}}' >> ${JSON.stringify(claudeLog)}
  echo '{"loggedIn":true,"authMethod":"api_key","configDirectory":"${claudeHome}"}'
  exit 0
fi
export PI_TEST_CLAUDE_STUB_VERSION=\${PI_TEST_CLAUDE_STUB_VERSION:-2.1.326}
exec node ${path.join(here, "..", "..", "..", "..", "lib", "__fixtures__", "claude-stub", "claude.mjs")} "$@"
`);
fs.writeFileSync(path.join(binDir, "pi"), `#!/usr/bin/env node
const fs = require("node:fs");
const log = ${JSON.stringify(piLog)};
fs.appendFileSync(log, JSON.stringify({ argv: process.argv.slice(2), envNames: Object.keys(process.env).sort() }) + "\\n");
const dir = process.env.PI_CODING_AGENT_DIR ?? "";
const body = dir === ${JSON.stringify(ownDir)}
  ? { status: "not_ready", provider: "kimi", reason: "credentials_not_configured" }
  : { status: "ready", provider: "sub2api", authType: "api_key" };
process.stdout.write(JSON.stringify(body));
`);
fs.writeFileSync(path.join(binDir, "pf"), `#!/usr/bin/env node
const fs = require("node:fs");
fs.appendFileSync(${JSON.stringify(pfLog)}, JSON.stringify(process.argv.slice(2)) + "\\n");
process.stdout.write(${JSON.stringify(DOCTOR)});
`);
for (const name of ["claude", "pi", "pf"]) fs.chmodSync(path.join(binDir, name), 0o755);

process.env.HOME = home;
process.env.CLAUDE_CONFIG_DIR = claudeHome;
process.env.PF_BIN = path.join(binDir, "pf");
process.env.PI_TEST_CLAUDE_STUB_LOG = path.join(scratch, "stub.jsonl");
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
delete process.env.PI_CODING_AGENT_DIR;
delete process.env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS;

const SENTINELS = [
  "RT-SENTINEL-INTER-TOKEN", "RT-SENTINEL-INTER-OAUTH", "RT-SENTINEL-DEF-TOKEN",
  "RT-SENTINEL-PI-OP", "RT-SENTINEL-PI-OWN", "RT-SENTINEL-NEW-KEY", "RT-SENTINEL-NEW-TOKEN",
];

test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const route = await jiti.import("./route.ts");

const noSentinel = (text) => {
  for (const sentinel of SENTINELS) assert.ok(!text.includes(sentinel), `the response leaks ${sentinel}`);
};

const request = (init) => new Request("http://localhost/api/settings/account", {
  headers: { host: "localhost" },
  ...init,
});
async function get(scope) {
  const req = scope === undefined ? request({}) : new Request(`http://localhost/api/settings/account?scope=${scope}`, { headers: { host: "localhost" } });
  const response = await route.GET(req);
  return { status: response.status, body: await response.json() };
}
async function post(body) {
  const response = await route.POST(request({
    method: "POST",
    headers: { host: "localhost", "content-type": "application/json" },
    body: JSON.stringify(body),
  }));
  return { status: response.status, text: await response.text() };
}

test("GET: the interactive card marks the Console profile Q3 names — present only as a fact, never read (review r1#6)", async () => {
  // absent in the throwaway home
  const bare = await get();
  assert.equal(bare.body.claude.consoleProfile, "absent", "no ~/.config/anthropic in the fixture home");
  // present once the directory exists — the route stats it, never reads it
  fs.mkdirSync(path.join(home, ".config", "anthropic"), { recursive: true });
  const marked = await get();
  assert.equal(marked.body.claude.consoleProfile, "present");
  noSentinel(JSON.stringify(marked.body));
  // and a marker file inside it never reaches the answer
  fs.writeFileSync(path.join(home, ".config", "anthropic", "keep"), "RT-SENTINEL-CONSOLE");
  const after = await get();
  noSentinel(JSON.stringify(after.body));
  assert.equal(after.body.claude.consoleProfile, "present");
  fs.rmSync(path.join(home, ".config", "anthropic"), { recursive: true, force: true });
});

test("GET: the interactive claude card and the three Cell kinds, no value anywhere (AC1)", async () => {
  const { status, body } = await get();
  assert.equal(status, 200);
  noSentinel(JSON.stringify(body));
  // the interactive card: the stub's auth status plus the settings env names
  assert.deepEqual(body.claude, {
    available: true, loggedIn: true, authMethod: "api_key", configDirectory: claudeHome,
    envCredentials: { ANTHROPIC_AUTH_TOKEN: true, ANTHROPIC_BASE_URL: true, CLAUDE_CODE_OAUTH_TOKEN: true, ANTHROPIC_API_KEY: false },
    consoleProfile: "absent", envCredentialsState: "observed", externalEnvCredentialKeys: [],
  });
  // the Cell rows: default first, then the named Cells
  assert.deepEqual(body.cells.map((c) => c.profile), ["default", "beta", "gamma"]);
  const def = body.cells[0];
  assert.equal(def.harnesses.pi.sourceKind, "operator");
  assert.equal(def.harnesses.pi.status, "ready");
  assert.equal(def.harnesses.pi.authType, "api_key");
  assert.equal(def.harnesses.claude.credentialKinds.gateway, true);
  const beta = body.cells[1];
  assert.equal(beta.harnesses.pi.sourceKind, "shared-default");
  assert.equal(beta.harnesses.claude.configRoot, path.join(data, "cells", "beta", "claude-config"));
  const gamma = body.cells[2];
  assert.equal(gamma.harnesses.pi.source, ownDir);
  assert.equal(gamma.harnesses.pi.sourceKind, "directory");
  assert.equal(gamma.harnesses.pi.status, "not_ready");
  assert.equal(gamma.harnesses.pi.checkReason, "credentials_not_configured");
  assert.equal(gamma.harnesses.pi.usable, false);
  // the pi checks ran once per distinct source dir, every one with
  // --no-refresh and never --credentials or a print-*
  const calls = fs.readFileSync(piLog, "utf8").trim().split("\n").map((l) => JSON.parse(l));
  assert.ok(calls.length >= 2, `the pi checks ran per source dir (${calls.length})`);
  for (const call of calls) {
    assert.equal(call.argv[call.argv.length - 1], "--no-refresh");
    assert.ok(!call.argv.includes("--credentials"));
    assert.ok(call.argv.every((word) => !word.startsWith("print-")));
  }
  // one doctor read per GET, argv as declared
  const pfCalls = fs.readFileSync(pfLog, "utf8").trim().split("\n").map((l) => JSON.parse(l));
  assert.deepEqual(pfCalls[0], ["--json", "doctor"]);
  // the claude status run: auth status alone, no value argument
  const claudeCalls = fs.readFileSync(claudeLog, "utf8").trim().split("\n").map((l) => l.replace(/^.*"argv":"([^"]*)".*$/, "$1").split(" ").filter(Boolean));
  assert.ok(claudeCalls.some((argv) => argv.join(" ") === "auth status"));
  for (const argv of claudeCalls) {
    for (const word of argv) assert.ok(!word.includes("RT-SENTINEL"), `the claude argv carries a value: ${word}`);
  }
});

test("scoped reads isolate subprocesses and malformed interactive env stays unknown (#285)", async () => {
  const count = (file) => fs.existsSync(file) ? fs.readFileSync(file, "utf8").length : 0;
  const before = [count(pfLog), count(piLog)];
  const interactive = await get("interactive");
  assert.equal(interactive.body.scope, "interactive");
  assert.deepEqual(interactive.body.cells, []);
  assert.deepEqual([count(pfLog), count(piLog)], before);
  const claudeBefore = count(claudeLog);
  const cells = await get("cells");
  assert.equal(cells.body.claude, null);
  assert.equal(count(claudeLog), claudeBefore);
  const pi = cells.body.cells[0].harnesses.pi;
  assert.equal(pi.provider, "sub2api");
  assert.ok(Number.isFinite(Date.parse(pi.checkedAt)));
  assert.equal(pi.checkBasis, "pi-auth-check-no-refresh");
  assert.equal(pi.modelUsable, null);
  assert.equal(pi.usable, null);
  assert.equal(cells.body.cells[0].harnesses.claude.status, "unknown");
  const file = path.join(claudeHome, "settings.json");
  const saved = fs.readFileSync(file, "utf8");
  try {
    for (const malformed of ["{bad", '{"env":[]}']) {
      fs.writeFileSync(file, malformed);
      const got = await get("interactive");
      assert.equal(got.body.claude.envCredentialsState, "read-failed");
      assert.ok(Object.values(got.body.claude.envCredentials).every((value) => value === null));
      noSentinel(JSON.stringify(got.body));
    }
    fs.rmSync(file);
    const absent = await get("interactive");
    assert.equal(absent.body.claude.envCredentialsState, "absent");
    assert.ok(Object.values(absent.body.claude.envCredentials).every((value) => value === false));
    fs.mkdirSync(file);
    assert.equal((await get("interactive")).body.claude.envCredentialsState, "read-failed");
    fs.rmdirSync(file);
  } finally { fs.writeFileSync(file, saved); }
  assert.equal((await get("invalid")).status, 400);
  const pfBefore = count(pfLog);
  assert.equal((await post({ kind: "api-key", value: "RT-SENTINEL-NEW-KEY", targets: ["interactive"] })).status, 200);
  assert.equal(count(pfLog), pfBefore);
});

test("POST: the credential lands in the native env block of the interactive session and the checked Cells (AC6)", async () => {
  fs.writeFileSync(piLog, "");
  const written = await post({ kind: "api-key", value: "RT-SENTINEL-NEW-KEY", targets: ["interactive", "beta"] });
  assert.equal(written.status, 200);
  noSentinel(written.text);
  const results = JSON.parse(written.text).results;
  assert.deepEqual(results.map((r) => [r.target, r.status]), [["interactive", "written"], ["beta", "written"]]);
  // the files carry the value; other keys are kept
  const interactive = JSON.parse(fs.readFileSync(path.join(claudeHome, "settings.json"), "utf8"));
  assert.equal(interactive.env.ANTHROPIC_API_KEY, "RT-SENTINEL-NEW-KEY");
  assert.equal(interactive.env.ANTHROPIC_AUTH_TOKEN, "RT-SENTINEL-INTER-TOKEN");
  assert.equal(interactive.env.OTHER_FLAG, "plain");
  assert.equal(interactive.model, "opus");
  const beta = JSON.parse(fs.readFileSync(path.join(data, "cells", "beta", "claude-config", "settings.json"), "utf8"));
  assert.equal(beta.env.ANTHROPIC_API_KEY, "RT-SENTINEL-NEW-KEY");
  assert.equal(beta.env.OTHER_FLAG, "kept");
  // the value never reached an argv
  for (const log of [piLog, claudeLog, pfLog]) {
    if (!fs.existsSync(log)) continue;
    assert.ok(!fs.readFileSync(log, "utf8").includes("RT-SENTINEL-NEW-KEY"), `the value reached an argv: ${log}`);
  }
});

test("POST: 沿用交互会话的值 copies server-side — the request carries no value, the target file gets it", async () => {
  const copied = await post({ kind: "oauth-token", useInteractive: true, targets: ["default"] });
  assert.equal(copied.status, 200);
  noSentinel(copied.text);
  assert.deepEqual(JSON.parse(copied.text).results, [{ target: "default", status: "written" }]);
  const def = JSON.parse(fs.readFileSync(path.join(data, "cells", "default", "claude-config", "settings.json"), "utf8"));
  assert.equal(def.env.CLAUDE_CODE_OAUTH_TOKEN, "sk-ant-oat01-RT-SENTINEL-INTER-OAUTH");
  assert.equal(def.env.ANTHROPIC_AUTH_TOKEN, "RT-SENTINEL-DEF-TOKEN");
  // a gateway copy needs the interactive base URL too
  const gateway = await post({ kind: "gateway", useInteractive: true, targets: ["default"] });
  assert.equal(gateway.status, 200);
  const after = JSON.parse(fs.readFileSync(path.join(data, "cells", "default", "claude-config", "settings.json"), "utf8"));
  assert.equal(after.env.ANTHROPIC_BASE_URL, "https://gw.example");
});

test("POST: an unrecognized format is refused with the file untouched; the gate refuses an unknown version", async () => {
  const torn = await post({ kind: "api-key", value: "RT-SENTINEL-NEW-KEY", targets: ["gamma"] });
  assert.equal(torn.status, 200);
  const results = JSON.parse(torn.text).results;
  assert.equal(results[0].status, "refused");
  assert.ok(results[0].reason.includes("unrecognized"), results[0].reason);
  noSentinel(torn.text);
  assert.equal(fs.readFileSync(path.join(data, "cells", "gamma", "claude-config", "settings.json"), "utf8"), "{not json");
  // an unknown claude version refuses every write through the shared gate:
  // a second stub directory (the probe caches per binary path) answers
  // 9.9.9, and the route resolves claude from PATH
  const gatedBin = path.join(scratch, "gated-bin");
  fs.mkdirSync(gatedBin);
  fs.writeFileSync(path.join(gatedBin, "claude"), `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then echo '{}'; exit 0; fi
export PI_TEST_CLAUDE_STUB_VERSION=9.9.9
exec node ${path.join(here, "..", "..", "..", "..", "lib", "__fixtures__", "claude-stub", "claude.mjs")} "$@"
`);
  fs.chmodSync(path.join(gatedBin, "claude"), 0o755);
  const originalPath = process.env.PATH;
  process.env.PATH = `${gatedBin}${path.delimiter}${originalPath}`;
  try {
    const gated = await post({ kind: "api-key", value: "RT-SENTINEL-NEW-KEY", targets: ["interactive"] });
    assert.equal(gated.status, 200);
    const results = JSON.parse(gated.text).results;
    assert.equal(results[0].status, "refused");
    assert.ok(results[0].reason.includes("9.9.9"), results[0].reason);
  } finally {
    process.env.PATH = originalPath;
  }
});

test("POST: empty targets never expands to all Cells and changes no credential file (#285)", async () => {
  const files = [path.join(claudeHome, "settings.json"), ...["default", "beta", "gamma"].map((profile) => path.join(data, "cells", profile, "claude-config", "settings.json"))];
  const before = files.map((file) => fs.readFileSync(file, "utf8"));
  const refused = await post({ kind: "api-key", value: "RT-SENTINEL-NO-TARGET", targets: [] });
  assert.equal(refused.status, 400);
  assert.deepEqual(files.map((file) => fs.readFileSync(file, "utf8")), before);
  noSentinel(refused.text);
});

test("POST: malformed gateway URLs and invalid copied URLs are refused before any target write", async () => {
  const paths = [path.join(claudeHome, "settings.json"), path.join(data, "cells", "beta", "claude-config", "settings.json")];
  const before = paths.map((file) => fs.readFileSync(file, "utf8"));
  for (const baseUrl of ["abc", "/v1", "https://", "file:///tmp/key", "javascript:alert(1)"]) {
    const refused = await post({ kind: "gateway", value: "RT-SENTINEL-BAD-URL", baseUrl, targets: ["interactive", "beta"] });
    assert.equal(refused.status, 400);
    noSentinel(refused.text);
    assert.deepEqual(paths.map((file) => fs.readFileSync(file, "utf8")), before);
  }
  const source = JSON.parse(before[0]);
  source.env.ANTHROPIC_AUTH_TOKEN = "RT-SENTINEL-COPY-BAD-URL";
  source.env.ANTHROPIC_BASE_URL = "not-a-url";
  fs.writeFileSync(paths[0], JSON.stringify(source));
  try {
    const refused = await post({ kind: "gateway", useInteractive: true, targets: ["beta"] });
    assert.equal(refused.status, 400);
    noSentinel(refused.text);
    assert.equal(fs.readFileSync(paths[1], "utf8"), before[1]);
  } finally { fs.writeFileSync(paths[0], before[0]); }
});

test("POST: a malformed body is a 400 before any write", async () => {
  for (const body of [
    null,
    [],
    {},
    { kind: "no-such", value: "x", targets: ["interactive"] },
    { kind: "api-key", targets: ["interactive"] },
    { kind: "api-key", value: "x", targets: [] },
    { kind: "api-key", value: "x", targets: ["no-such-cell"] },
    { kind: "gateway", value: "x", targets: ["interactive"] },
  ]) {
    const refused = await post(body);
    assert.equal(refused.status, 400, JSON.stringify(body));
    assert.ok(!refused.text.includes("SENTINEL"));
  }
  // the interactive session lacks nothing it was asked for above, but an
  // api-key copy from a session without one is a 400 too
  const before = fs.readFileSync(path.join(claudeHome, "settings.json"), "utf8");
  const interactive = JSON.parse(before);
  delete interactive.env.ANTHROPIC_API_KEY;
  fs.writeFileSync(path.join(claudeHome, "settings.json"), JSON.stringify(interactive, null, 2));
  const noKey = await post({ kind: "api-key", useInteractive: true, targets: ["interactive"] });
  assert.equal(noKey.status, 400);
});
