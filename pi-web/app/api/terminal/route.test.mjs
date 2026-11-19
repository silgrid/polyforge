// carries: app/api/terminal/route.ts, lib/terminal-manager.ts, lib/harness-login-commands.ts, app/api/terminal/route.test.mjs
// polyforge#142 (85a2, AC3): the terminal route's login mode — the account
// page's 「打开终端」 runs the adapter-declared login command in a one-shot
// terminal (Q5: not a workspace shell, destroyed the moment the command
// exits, its replay buffer discarded). The client names {harness, id,
// cell}; everything else — the argv, the env, the cwd — is the server's
// own resolution. A body that carries command/env/argv is refused: the
// terminal face runs declared commands only, never arbitrary input.
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-terminal-login-"));
const binDir = path.join(scratch, "bin");
fs.mkdirSync(binDir, { recursive: true });
// a stub claude that records its argv and environment names, then exits at
// once — the login terminal runs it, observes the exit, and destroys itself
const claudeLog = path.join(scratch, "claude-calls.jsonl");
fs.writeFileSync(path.join(binDir, "claude"), `#!/bin/sh
printf '%s\\n' "{\\"argv\\":\\"$*\\"}" >> ${JSON.stringify(claudeLog)}
exit 0
`);
fs.chmodSync(path.join(binDir, "claude"), 0o755);
process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH}`;
test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const route = await jiti.import("./route.ts");
const terminalManager = await jiti.import("../../lib/terminal-manager.ts");

const post = (body) => route.POST(new Request("http://localhost/api/terminal", {
  method: "POST",
  headers: { host: "localhost", "content-type": "application/json" },
  body: JSON.stringify(body),
}));

/** A bounded poll: awaits the condition to the deadline, re-reading every step. */
async function poll(check, deadlineMs = 10_000, stepMs = 50) {
  const end = Date.now() + deadlineMs;
  while (Date.now() < end) {
    if (check()) return true;
    await new Promise((resolve) => setTimeout(resolve, stepMs));
  }
  return check();
}

test("login mode runs the declared command in a one-shot terminal that dies with it", async () => {
  const response = await post({ login: { harness: "claude", id: "auth-login" }, cols: 80, rows: 24 });
  assert.equal(response.status, 200);
  const { id } = await response.json();
  assert.ok(typeof id === "string" && id !== "", "no terminal id answered");
  // the record exists while the command runs, and is gone the moment it exits
  assert.ok(terminalManager.hasTerminal(id));
  const exited = await poll(() => !terminalManager.hasTerminal(id));
  assert.equal(exited, true, "the login terminal outlived its command");
  // the command ran with the declared argv, and the terminal env carries no
  // browser password
  const calls = fs.readFileSync(claudeLog, "utf8").trim().split("\n").map((line) => JSON.parse(line));
  assert.deepEqual(calls.at(-1), { argv: "auth login" });
  // a second login of the same command is another one-shot terminal
  const again = await post({ login: { harness: "claude", id: "setup-token" } });
  assert.equal(again.status, 200);
  const { id: second } = await again.json();
  assert.notEqual(second, id);
  assert.ok(await poll(() => !terminalManager.hasTerminal(second)));
  const callsAfter = fs.readFileSync(claudeLog, "utf8").trim().split("\n").map((line) => JSON.parse(line));
  assert.deepEqual(callsAfter.at(-1), { argv: "setup-token" });
});

test("a body the client shaped beyond {harness, id, cell} is refused — no command, env or argv from the browser", async () => {
  for (const body of [
    { login: { harness: "claude", id: "auth-login" }, command: "claude setup-token" },
    { login: { harness: "claude", id: "auth-login" }, env: { ANTHROPIC_API_KEY: "x" } },
    { login: { harness: "claude", id: "auth-login" }, argv: ["claude", "setup-token"] },
    { login: { harness: "claude", id: "auth-login" }, cwd: "/tmp" },
    { login: { harness: "claude", id: "auth-login", cell: "default" } },
    { login: { harness: "no-such", id: "auth-login" } },
    { login: { harness: "claude", id: "no-such" } },
    { login: "claude" },
    {},
  ]) {
    const refused = await post(body);
    assert.equal(refused.status, 400, JSON.stringify(body));
  }
  assert.equal(fs.readFileSync(claudeLog, "utf8").trim().split("\n").length, 2, "a refused body started a terminal");
});

test("the workspace shell mode is unchanged: cwd required, id optional", async () => {
  const refused = await post({ cols: 80, rows: 24 });
  assert.equal(refused.status, 400);
  const response = await post({ cwd: scratch, id: "a".repeat(32) });
  assert.equal(response.status, 200);
  assert.equal(await response.json(), { id: "a".repeat(32) });
  terminalManager.killTerminal("a".repeat(32));
});
