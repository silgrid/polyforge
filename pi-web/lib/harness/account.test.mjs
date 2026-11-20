// carries: lib/harness/account.ts, lib/harness/claude.ts, lib/harness/concepts.ts
// polyforge#142 (85a2, AC1/AC3): the adapter account reads — the commands
// the account page's interactive card and Cell rows run, and the login
// command declarations the terminal whitelist is built from. Expected
// shapes come from the real CLIs' outputs (claude auth status JSON with
// exit 1 when signed out; pi auth check --json --no-refresh answering
// ready/not_ready with authType), never from the implementation, and the
// sentinel strings planted in the fixtures must survive nowhere.
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { claudeSessionStatus, piStepStatus, PI_AUTH_CHECK_ARGV } = await jiti.import("./account.ts");
const { createClaudeAdapter, claudeCredentialsFollowConfigRoot } = await jiti.import("./claude.ts");
const { createPiAdapter } = await jiti.import("./pi.ts");

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-harness-account-"));
/** One stub binary: records its argv (word by word) and env NAMES to a log,
 * then answers `body` with `code`. Never touches a network. */
function stubBinary(name, body, code = 0) {
  const dir = path.join(scratch, "bin");
  fs.mkdirSync(dir, { recursive: true });
  const log = path.join(scratch, `${name}-calls.jsonl`);
  const file = path.join(dir, name);
  fs.writeFileSync(file, `#!/usr/bin/env node
const fs = require("node:fs");
const log = ${JSON.stringify(log)};
fs.appendFileSync(log, JSON.stringify({ argv: process.argv.slice(2), envNames: Object.keys(process.env).sort() }) + "\\n");
process.stdout.write(${JSON.stringify(body)});
process.exit(${code});
`);
  fs.chmodSync(file, 0o755);
  return file;
}

test.after(() => { fs.rmSync(scratch, { recursive: true, force: true }); });

// the real claude answers this JSON on stdout, exit 0 signed in / 1 signed
// out (verified against claude 2.1.326 on this machine)
const CLAUDE_IN = JSON.stringify({
  loggedIn: true, authMethod: "oauth_token", apiProvider: "firstParty",
  configDirectory: "/home/op/.claude", loginExpiresAt: null,
});
const CLAUDE_OUT = JSON.stringify({
  loggedIn: false, authMethod: "none", configDirectory: "/home/op/.claude",
});

test("claude session status: the auth status run answers loggedIn, authMethod and configDirectory", async () => {
  const status = await claudeSessionStatus({ claudeBinary: stubBinary("claude", CLAUDE_IN) });
  assert.deepEqual(status, {
    available: true, loggedIn: true, authMethod: "oauth_token", configDirectory: "/home/op/.claude",
  });
  // a signed-out claude exits 1 — the JSON still parses
  const out = await claudeSessionStatus({ claudeBinary: stubBinary("claude-out", CLAUDE_OUT, 1) });
  assert.equal(out.available, true);
  assert.equal(out.loggedIn, false);
  assert.equal(out.authMethod, "none");
  const calls = fs.readFileSync(path.join(scratch, "claude-calls.jsonl"), "utf8").trim().split("\n").map((l) => JSON.parse(l));
  assert.deepEqual(calls.map((c) => c.argv), [["auth", "status"]]);
});

test("claude session status: no claude and a non-JSON answer degrade, never throw, never echo output", async () => {
  const none = await claudeSessionStatus({ claudeBinary: path.join(scratch, "no-such-claude") });
  assert.equal(none.available, false);
  assert.equal(none.loggedIn, null);
  assert.ok(typeof none.reason === "string" && none.reason.length > 0);
  assert.ok(!none.reason?.includes("SENTINEL"), "the degradation reason must not echo the output");
  const garbage = await claudeSessionStatus({ claudeBinary: stubBinary("claude-bad", "not json at all SENTINEL", 0) });
  assert.equal(garbage.available, false);
  assert.ok(!garbage.reason?.includes("SENTINEL"), "a non-JSON answer must not be echoed");
});

// the real pi answers ready with an authType / not_ready with a reason
// (verified against pi 1.5.0 on this machine)
test("pi step status: the check answers ready/not_ready with authType and reason per source dir", async () => {
  const dirA = path.join(scratch, "src-a");
  fs.mkdirSync(dirA, { recursive: true });
  fs.writeFileSync(path.join(dirA, "settings.json"), JSON.stringify({ defaultProvider: "anthropic" }));
  const ready = stubBinary("pi", JSON.stringify({ status: "ready", provider: "anthropic", authType: "oauth" }));
  const got = await piStepStatus(dirA, { binary: ready });
  assert.deepEqual(got, { status: "ready", provider: "anthropic", authType: "oauth", reason: null });

  const notReady = stubBinary("pi-nr", JSON.stringify({ status: "not_ready", provider: "anthropic", reason: "credentials_not_configured" }));
  const gotNR = await piStepStatus(dirA, { binary: notReady });
  assert.deepEqual(gotNR, { status: "not_ready", provider: "anthropic", authType: null, reason: "credentials_not_configured" });
});

test("pi step status: every run passes --no-refresh and never --credentials or print-*", async () => {
  const dir = path.join(scratch, "src-b");
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, "settings.json"), JSON.stringify({ defaultProvider: "kimi" }));
  const calls = path.join(scratch, "pi-guard-calls.jsonl");
  const binary = stubBinary("pi-guard", "{}");
  await piStepStatus(dir, { binary });
  const recorded = fs.readFileSync(calls, "utf8").trim().split("\n").map((l) => JSON.parse(l));
  assert.equal(recorded.length, 1);
  const argv = recorded[0].argv;
  assert.deepEqual(argv.slice(0, 2), ["auth", "check"]);
  assert.ok(argv.includes("--provider"));
  assert.equal(argv[argv.length - 2], "--json");
  assert.equal(argv[argv.length - 1], "--no-refresh", "the check must never refresh OAuth (it would write auth.json)");
  assert.ok(!argv.includes("--credentials"), "--credentials would print the credential");
  assert.ok(argv.every((word) => !word.startsWith("print-")), "no print-* subcommand may run");
  assert.deepEqual(PI_AUTH_CHECK_ARGV.slice(-1), ["--no-refresh"]);
  // PI_CODING_AGENT_DIR names the source dir, and the step-agent env never
  // carries the browser password
  const names = recorded[0].envNames;
  assert.ok(!names.includes("PI_WEB_PASSWORD"));
});

test("pi step status: no declared provider answers unknown with the reason, a missing binary degrades", async () => {
  const dir = path.join(scratch, "src-c");
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, "auth.json"), JSON.stringify({ "sub2api": { type: "api_key", key: "sk-SENTINEL-AUTH" } }));
  const none = await piStepStatus(dir, { binary: stubBinary("pi-none", "{}") });
  // the first auth.json key names the provider; only the NAME leaves the file
  assert.equal(none.status, "unknown");
  assert.equal(none.provider, "sub2api");
  assert.ok(none.reason && !none.reason.includes("SENTINEL"));
  const empty = path.join(scratch, "src-d");
  fs.mkdirSync(empty, { recursive: true });
  const noProvider = await piStepStatus(empty, { binary: stubBinary("pi-empty", "{}") });
  assert.equal(noProvider.status, "unknown");
  assert.ok(noProvider.reason, "an empty source dir answers why nothing was checked");
  const gone = await piStepStatus(dir, { binary: path.join(scratch, "no-such-pi") });
  assert.equal(gone.status, "unknown");
  assert.ok(gone.reason);
});

test("capabilities: claude declares loginCommands and writes auth; pi declares none", async () => {
  const claude = createClaudeAdapter();
  assert.deepEqual(claude.capabilities.loginCommands.map((c) => c.id), ["auth-login", "setup-token"]);
  assert.deepEqual(claude.capabilities.loginCommands.map((c) => c.argv), [["claude", "auth", "login"], ["claude", "setup-token"]]);
  assert.deepEqual(claude.capabilities.writableConcepts, ["auth", "permission", "model"]);
  const pi = createPiAdapter();
  assert.deepEqual(pi.capabilities.loginCommands, []);
  assert.deepEqual(pi.capabilities.writableConcepts, ["permission", "model"]);
});

test("claude's credentials follow the config dir except on darwin's Keychain (AC7)", () => {
  assert.equal(claudeCredentialsFollowConfigRoot("linux"), true);
  assert.equal(claudeCredentialsFollowConfigRoot("win32"), true);
  assert.equal(claudeCredentialsFollowConfigRoot("darwin"), false);
});
