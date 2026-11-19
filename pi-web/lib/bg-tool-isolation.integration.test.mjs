// carries: lib/bg-tools.ts, lib/rpc-manager.ts
import "#test-env";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { fauxProvider } from "@earendil-works/pi-ai";
import {
  createAgentSession,
  DefaultResourceLoader,
  ModelRuntime,
  SessionManager,
  SettingsManager,
} from "@earendil-works/pi-coding-agent";
import { createJiti } from "jiti";

// AC5: the four background-task tools exist only in Pi Web's normal sessions.
// The negative probe starts the harness the way the daemon starts a step
// agent — a real `pi --mode rpc` with the step agent directory (argv matches
// internal/polyforge/runtime/spawn_test.go's TestSpawnIsolationFlags) — and
// reads the tool list it assembled; the positive control builds a session the
// way rpc-manager does and finds all four.

const jiti = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
const { createBgTasksExtension } = await jiti.import("./bg-tools.ts");
const { getBgRuntime } = await jiti.import("./rpc-manager.ts");

const BG_TOOLS = ["bg_run", "bg_status", "bg_output", "bg_stop"];
const POLL_STEP_MS = 100;

async function pollUntil(condition, deadlineMs, what) {
  const deadline = Date.now() + deadlineMs;
  for (;;) {
    if (await condition()) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((resolve) => setTimeout(resolve, POLL_STEP_MS));
  }
}

test("a step agent started like the daemon's production spawn has none of the four tools", async (t) => {
  const agentDir = await mkdtemp(join(tmpdir(), "bg-isolation-"));
  const cwd = await mkdtemp(join(tmpdir(), "bg-isolation-cwd-"));
  t.after(() => {
    rm(agentDir, { recursive: true, force: true });
    rm(cwd, { recursive: true, force: true });
  });
  const dumpPath = join(agentDir, "tools-dump.json");
  // A dump extension in the step agent directory: whatever tools the harness
  // assembled, it writes their names out at session_start. Discovery takes
  // `extensions/*.ts` and `*.js` only, and the session store stays inside the
  // temp dir even when the test process itself carries the daemon's
  // PI_CODING_AGENT_SESSION_DIR.
  await mkdir(join(agentDir, "extensions"), { recursive: true });
  await mkdir(join(agentDir, "sessions"), { recursive: true });
  await writeFile(join(agentDir, "extensions", "bg-dump.js"), `module.exports = (pi) => {
  pi.on("session_start", () => {
    try {
      const fs = require("node:fs");
      fs.writeFileSync(process.env.BG_DUMP_TOOLS, JSON.stringify({
        all: pi.getAllTools().map((tool) => tool.name),
        active: pi.getActiveTools(),
      }));
    } catch {}
  });
};
`);
  const piBin = join(dirname(new URL(import.meta.url).pathname), "..", "node_modules", ".bin", "pi");
  assert.ok(existsSync(piBin), "the repo-local pi CLI must exist for the probe");

  const child = spawn(piBin, ["--mode", "rpc", "--no-skills", "--no-context-files", "--no-prompt-templates"], {
    cwd,
    env: {
      ...process.env,
      PI_CODING_AGENT_DIR: agentDir,
      PI_CODING_AGENT_SESSION_DIR: join(agentDir, "sessions"),
      BG_DUMP_TOOLS: dumpPath,
    },
    stdio: ["pipe", "pipe", "pipe"],
  });
  // Bounded cleanup: the probe's own child, killed by process group.
  t.after(() => {
    try { child.kill("SIGKILL"); } catch { /* already gone */ }
  });
  child.stderr.resume();
  await pollUntil(() => existsSync(dumpPath), 20_000, "the step agent's tool list");
  child.kill("SIGKILL");

  const dump = JSON.parse(await readFile(dumpPath, "utf8"));
  const names = new Set([...dump.all, ...dump.active]);
  for (const name of BG_TOOLS) {
    assert.equal(names.has(name), false, `a daemon-started step agent must not expose ${name}`);
  }
  // Sanity: the step agent did assemble its ordinary tools.
  assert.ok(dump.all.length > 0, "the probe read a real tool list");
});

test("a normal Pi Web session's assembly exposes all four tools", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "bg-positive-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const faux = fauxProvider({ models: [{ id: "faux-bg" }] });
  const modelRuntime = await ModelRuntime.create({
    authPath: join(dir, "auth.json"),
    modelsPath: null,
    refreshOnCreate: false,
  });
  modelRuntime.registerNativeProvider(faux.provider);
  const settingsManager = SettingsManager.inMemory();
  const resourceLoader = new DefaultResourceLoader({
    cwd: dir,
    agentDir: dir,
    settingsManager,
    extensionFactories: [createBgTasksExtension(getBgRuntime())],
    noExtensions: true,
    noSkills: true,
    noThemes: true,
    noPromptTemplates: true,
    noContextFiles: true,
  });
  await resourceLoader.reload();
  const { session } = await createAgentSession({
    cwd: dir,
    agentDir: dir,
    modelRuntime,
    model: faux.getModel("faux-bg"),
    sessionManager: SessionManager.inMemory(dir),
    settingsManager,
    resourceLoader,
  });
  t.after(() => session.dispose());
  const names = new Set(session.getAllTools().map((tool) => tool.name));
  for (const name of BG_TOOLS) {
    assert.ok(names.has(name), `a normal session must expose ${name}`);
  }
});
