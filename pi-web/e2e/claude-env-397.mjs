// carries: lib/claude-argv*, lib/project-command-env*, e2e/claude-stub/claude.mjs
// — a claude session's processes do not inherit the Pi Web server's own
// NODE_ENV, TMUX or TMUX_PANE.
//
// The server runs with a TMUX / TMUX_PANE of its own (a fake socket path: nothing here
// ever talks to a tmux server) and the NODE_ENV `next` gives it. A claude session is
// created through `POST /api/agent/new` with the PATH-injected claude stub
// (e2e/claude-stub/claude.mjs); its shell turn runs the user's `$ <command>` line as
// claude's Bash tool would, in the child's own environment, and the session's history
// (`GET /api/sessions/<id>`, read from the stub's claude-shaped transcript) holds the
// command's output as the Bash tool result. The suite runs on an isolated HOME,
// PI_CODING_AGENT_DIR and CLAUDE_CONFIG_DIR. No browser: the contract is the child's
// environment, which no page shows.
//
// Each scenario prints one `PASS S397-nn` line on success, `FAIL S397-nn: …` otherwise.
import assert from "node:assert/strict";
import { rmSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { assertNoDevServer, claudeScratch, createCheck, createHarness, errorText, pidAlive, readJsonLines, setupFailed } from "./harness.mjs";

assertNoDevServer();
const kit = claudeScratch("pi-web-claude-397-");
const { artifacts, scratch, home, agentDir, project, stubDir, stubLog, configDir } = kit;

const SERVER_ONLY = ["NODE_ENV", "TMUX", "TMUX_PANE"];
const PROBE = "echo ${NODE_ENV:-unset} ${TMUX:-unset}";

const h = createHarness();
let base;
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: ([id], text) => `${id}: ${text}` });

const stubRecords = () => readJsonLines(stubLog);

/** Polls a real condition every 250 ms until its deadline; the last value when it never held. */
async function waitFor(predicate, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  let value;
  while (Date.now() < deadline) {
    try {
      value = await predicate();
      if (value?.done) return value;
    } catch {
      // not ready yet
    }
    await delay(250);
  }
  return value;
}

async function apiFetch(pathname, init = {}) {
  return fetch(`${base}${pathname}`, {
    ...init,
    headers: { ...(init.body ? { "Content-Type": "application/json" } : {}), ...(init.headers ?? {}) },
    signal: AbortSignal.timeout(60_000),
  });
}

const textOf = (content) => (typeof content === "string"
  ? content
  : Array.isArray(content) ? content.map((block) => (typeof block?.text === "string" ? block.text : "")).join("") : "");

h.trapInterrupt();

try {
  const env = {
    ...process.env,
    HOME: home,
    PI_CODING_AGENT_DIR: agentDir,
    // The repo's e2e convention (#219): the password gate is off for the readiness probe.
    PI_WEB_PASSWORD: "",
    CLAUDE_CONFIG_DIR: configDir,
    PI_WEB_ALLOWED_ROOT_PREFIXES: scratch,
    // Pi Web's own tmux, as when it runs inside one; the socket does not exist.
    TMUX: join(scratch, "no-such-tmux-socket") + ",1,0",
    TMUX_PANE: "%397",
    PATH: `${stubDir}:${process.env.PATH ?? ""}`,
    PI_E2E_CLAUDE_STUB_LOG: stubLog,
    NEXT_TELEMETRY_DISABLED: "1",
  };
  ({ base } = await h.startServer({
    name: "claude-env-397",
    logFile: join(artifacts, "claude-env-397-server.log"),
    from: env,
  }));

  // S397-01 — AC2: in a claude session `echo ${NODE_ENV:-unset} ${TMUX:-unset}` prints
  // `unset unset`, and the child was launched without any of the three variables.
  await scenario("S397-01", async () => {
    const created = await apiFetch("/api/agent/new", {
      method: "POST",
      body: JSON.stringify({ cwd: project, type: "prompt", message: `$ ${PROBE}`, harness: "claude", model: "claude-sonnet-5-5" }),
    });
    const body = await created.json().catch(() => ({}));
    assert.equal(created.status, 200, `POST /api/agent/new -> ${created.status} ${JSON.stringify(body).slice(0, 300)}`);
    const id = body.sessionId ?? body.id;
    assert.ok(typeof id === "string" && id.length > 0, "the create route returned no claude session id");

    const found = await waitFor(async () => {
      const response = await apiFetch(`/api/sessions/${encodeURIComponent(id)}`);
      if (response.status !== 200) return { done: false, results: [] };
      const detail = await response.json();
      const results = (detail.context?.messages ?? [])
        .filter((message) => message.role === "toolResult")
        .map((message) => textOf(message.content).trim());
      return { done: results.length > 0, results };
    }, 60_000);
    assert.ok(found?.done, `the session's history never held the Bash tool result (seen: ${JSON.stringify(found?.results ?? [])})`);
    assert.deepEqual(found.results, ["unset unset"], "the session's command saw the server's NODE_ENV or TMUX");

    const launch = stubRecords().find((entry) => entry.kind === "launch" && entry.sessionId === id);
    assert.ok(launch, "the stub recorded no launch for the session");
    for (const name of SERVER_ONLY) assert.ok(!launch.envKeys.includes(name), `the claude child inherited ${name}`);
    assert.ok(stubRecords().some((entry) => entry.kind === "shell" && entry.command === PROBE), "the stub ran no shell turn for the probe");
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  await h.close();
  for (const launch of stubRecords().filter((entry) => entry.kind === "launch")) {
    if (pidAlive(launch.pid)) {
      try { process.kill(launch.pid, "SIGKILL"); } catch { /* already gone */ }
    }
  }
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL claude-env-397: ${failures.length} scenario(s) failed`);
  process.exitCode = 1;
}
