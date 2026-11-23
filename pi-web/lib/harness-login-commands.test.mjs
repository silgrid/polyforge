// carries: lib/harness-login-commands.ts, lib/harness/adapter.ts, lib/harness/concepts.ts
// polyforge#142 (85a2, AC3): the terminal login whitelist — the resolver
// that turns {harness, id, cell} into the argv, env and cwd a one-shot
// login terminal runs. The data is the adapters' own loginCommands
// declaration (never client input); a Cell-scoped command gets its config
// root from the server's own doctor read. Expected values come from the
// Q3/Q4/Q5 rulings: claude's two commands on the operator's interactive
// environment, pi none, cwd the operator's home.
import "#test-env";
import assert from "node:assert/strict";
import os from "node:os";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { resolveLoginCommand } = await jiti.import("./harness-login-commands.ts");

test("claude's two declared commands resolve verbatim on the operator's environment", async () => {
  const login = await resolveLoginCommand({ harness: "claude", id: "auth-login" });
  assert.deepEqual(login, { ok: true, command: { argv: ["claude", "auth", "login"], env: {}, cwd: os.homedir() } });
  const token = await resolveLoginCommand({ harness: "claude", id: "setup-token" });
  assert.deepEqual(token, { ok: true, command: { argv: ["claude", "setup-token"], env: {}, cwd: os.homedir() } });
});

test("an undeclared harness or id is refused; pi declares no terminal login (Q4)", async () => {
  for (const bad of [
    { harness: "no-such", id: "auth-login" },
    { harness: "claude", id: "no-such" },
    { harness: "pi", id: "auth-login" },
    { harness: "", id: "auth-login" },
    { harness: "claude" },
  ]) {
    const refused = await resolveLoginCommand(bad);
    assert.equal(refused.ok, false, JSON.stringify(bad));
    assert.equal(refused.status, 400);
    assert.ok(refused.reason.length > 0);
  }
});

test("a cell request on a non-cell command is refused — the operator env is the declared environment", async () => {
  const refused = await resolveLoginCommand({ harness: "claude", id: "auth-login", cell: "default" });
  assert.equal(refused.ok, false);
  assert.equal(refused.status, 400);
  assert.ok(refused.reason.includes("interactive"));
});

test("a cell-scoped declaration gets its config root from the server's own doctor read", async () => {
  const cellsAnswer = { cells: [{ profile: "beta", harnesses: { claude: { configRoot: "/h/beta/claude-config" } } }], error: null };
  const adapters = [{
    id: "claude",
    capabilities: {
      id: "claude", configRootEnv: "CLAUDE_CONFIG_DIR",
      loginCommands: [{ id: "cell-login", argv: ["claude", "auth", "login"], cell: true }],
    },
  }];
  const resolved = await resolveLoginCommand(
    { harness: "claude", id: "cell-login", cell: "beta" },
    { adapters, doctorRead: async () => cellsAnswer },
  );
  assert.deepEqual(resolved, {
    ok: true,
    command: {
      argv: ["claude", "auth", "login"],
      env: { CLAUDE_CONFIG_DIR: "/h/beta/claude-config" },
      cwd: os.homedir(),
    },
  });
  // a Cell the doctor does not know, and a failed doctor read, refuse
  const unknown = await resolveLoginCommand(
    { harness: "claude", id: "cell-login", cell: "no-such" },
    { adapters, doctorRead: async () => cellsAnswer },
  );
  assert.equal(unknown.ok, false);
  assert.ok(unknown.reason.includes("no-such"));
  const degraded = await resolveLoginCommand(
    { harness: "claude", id: "cell-login", cell: "beta" },
    { adapters, doctorRead: async () => ({ cells: null, error: "pf doctor did not answer in time" }) },
  );
  assert.equal(degraded.ok, false);
  assert.ok(degraded.reason.includes("doctor"));
  // a Cell with no config root for the harness refuses too
  const rootless = await resolveLoginCommand(
    { harness: "claude", id: "cell-login", cell: "beta" },
    { adapters, doctorRead: async () => ({ cells: [{ profile: "beta", harnesses: {} }], error: null }) },
  );
  assert.equal(rootless.ok, false);
});
