import "#test-env";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fauxAssistantMessage, fauxProvider, fauxText } from "@earendil-works/pi-ai";
import {
  createAgentSession,
  DefaultResourceLoader,
  ModelRuntime,
  SessionManager,
  SettingsManager,
} from "@earendil-works/pi-coding-agent";
import { createJiti } from "jiti";

const agentDir = await mkdtemp(join(tmpdir(), "pi-web-rename-agent-"));
const previousAgentDir = process.env.PI_CODING_AGENT_DIR;
process.env.PI_CODING_AGENT_DIR = agentDir;

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { PATCH: renameSession } = await jiti.import("./[id]/route.ts");
const { AgentSessionWrapper } = await jiti.import("../../../lib/rpc-manager.ts");
const { cacheSessionPath, invalidateSessionPathCache } = await jiti.import("../../../lib/session-reader.ts");

test.after(async () => {
  if (previousAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = previousAgentDir;
  await rm(agentDir, { recursive: true, force: true });
});

const rename = (id, name) => renameSession(
  new Request(`http://localhost/api/sessions/${id}`, { method: "PATCH", body: JSON.stringify({ name }) }),
  { params: Promise.resolve({ id }) },
);
const fileEntries = async (path) => (await readFile(path, "utf8")).trim().split("\n").map((line) => JSON.parse(line));

function withRegistry(t, entries) {
  const previousRegistry = globalThis.__piSessions;
  const previousLocks = globalThis.__piStartLocks;
  globalThis.__piSessions = new Map(entries);
  globalThis.__piStartLocks = new Map();
  t.after(() => {
    globalThis.__piSessions = previousRegistry;
    globalThis.__piStartLocks = previousLocks;
  });
  return { registry: globalThis.__piSessions, locks: globalThis.__piStartLocks };
}

function idleSessionOnDisk(t) {
  const manager = SessionManager.create(agentDir);
  manager.appendMessage({ role: "user", content: "hello", timestamp: Date.now() });
  manager.appendMessage({ role: "assistant", content: [{ type: "text", text: "hi" }], timestamp: Date.now() });
  const id = manager.getSessionId();
  cacheSessionPath(id, manager.getSessionFile());
  t.after(() => invalidateSessionPathCache(id));
  return { id, path: manager.getSessionFile() };
}

async function liveSession(t, extensionFactories) {
  const cwd = await mkdtemp(join(tmpdir(), "pi-web-rename-cwd-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  const faux = fauxProvider({ models: [{ id: "faux-rename" }] });
  const modelRuntime = await ModelRuntime.create({ authPath: join(agentDir, "auth.json"), modelsPath: null, refreshOnCreate: false });
  modelRuntime.registerNativeProvider(faux.provider);
  const settingsManager = SettingsManager.inMemory();
  const resourceLoader = new DefaultResourceLoader({
    cwd,
    agentDir,
    settingsManager,
    extensionFactories,
    noSkills: true,
    noPromptTemplates: true,
    noThemes: true,
    noContextFiles: true,
  });
  await resourceLoader.reload();
  const { session } = await createAgentSession({
    cwd,
    agentDir,
    modelRuntime,
    model: faux.getModel("faux-rename"),
    sessionManager: SessionManager.create(cwd, join(agentDir, "sessions")),
    settingsManager,
    resourceLoader,
  });
  const wrapper = new AgentSessionWrapper(session);
  t.after(() => wrapper.destroy());
  wrapper.start();
  wrapper.beginExtensionBinding();
  withRegistry(t, [[session.sessionId, wrapper]]);
  return { faux, session, wrapper };
}

test("a live session is renamed through its AgentSession: on its leaf, with session_info_changed (#1388)", async (t) => {
  const seen = [];
  const listener = (pi) => { pi.on("session_info_changed", (event) => { seen.push(event.name); }); };
  const { faux, session, wrapper } = await liveSession(t, [listener]);
  await wrapper.waitUntilReady();

  faux.setResponses([fauxAssistantMessage([fauxText("first")]), fauxAssistantMessage([fauxText("second")])]);
  await session.prompt("first prompt");
  // As registerRpcWrapper() does: the file path is known, so a write on disk was possible.
  cacheSessionPath(session.sessionId, session.sessionFile);
  t.after(() => invalidateSessionPathCache(session.sessionId));
  const tipBefore = session.sessionManager.getLeafId();

  const response = await rename(session.sessionId, "  Renamed live  ");
  assert.equal(response.status, 200);
  assert.equal(session.sessionManager.getSessionName(), "Renamed live", "the live session knows its name");
  assert.deepEqual(seen, ["Renamed live"], "extensions got session_info_changed");

  await session.prompt("second prompt");
  const entries = await fileEntries(session.sessionFile);
  const info = entries.filter((entry) => entry.type === "session_info");
  assert.equal(info.length, 1);
  assert.equal(info[0].parentId, tipBefore, "appended at the live leaf");
  assert.ok(entries.some((entry) => entry.parentId === info[0].id), "the next prompt continues from the rename: no dead branch");
});

test("a session_start that never settles does not hold the rename", async (t) => {
  const seen = [];
  const stuck = (pi) => {
    // An extension parked on a dialog nobody answers.
    pi.on("session_start", () => new Promise(() => {}));
    pi.on("session_info_changed", (event) => { seen.push(event.name); });
  };
  const { session } = await liveSession(t, [stuck]);

  let timer;
  const timedOut = new Promise((resolve) => { timer = setTimeout(() => resolve("timed out"), 2_000); });
  const response = await Promise.race([rename(session.sessionId, "While binding"), timedOut]);
  clearTimeout(timer);
  assert.notEqual(response, "timed out");
  assert.equal(response.status, 200);
  assert.equal(session.sessionManager.getSessionName(), "While binding");
  assert.deepEqual(seen, ["While binding"]);
});

test("a rename waits for a start in flight and goes through the wrapper it registers", async (t) => {
  const { id, path } = idleSessionOnDisk(t);
  const { registry, locks } = withRegistry(t, []);
  const names = [];
  const wrapper = { isAlive: () => true, inner: { setSessionName: (name) => names.push(name) } };
  let finishStart;
  locks.set(id, new Promise((resolve) => { finishStart = resolve; }));

  const pending = rename(id, "During start");
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(names, []);
  registry.set(id, wrapper);
  locks.delete(id);
  finishStart({ session: wrapper, realSessionId: id });

  assert.equal((await pending).status, 200);
  assert.deepEqual(names, ["During start"]);
  assert.ok(!(await fileEntries(path)).some((entry) => entry.type === "session_info"), "nothing written behind the wrapper");
});

test("a session shutting down is renamed on disk once its wrapper is disposed; one without a wrapper at once", async (t) => {
  const { id, path } = idleSessionOnDisk(t);
  let disposed;
  const closing = {
    isAlive: () => false,
    waitUntilDisposed: () => new Promise((resolve) => { disposed = () => resolve(true); }),
    inner: { setSessionName: () => assert.fail("a closing session is not renamed in place") },
  };
  withRegistry(t, [[id, closing]]);

  const pending = rename(id, "After shutdown");
  await new Promise((resolve) => setImmediate(resolve));
  assert.ok(!(await fileEntries(path)).some((entry) => entry.type === "session_info"), "not while it may still append");
  disposed();
  assert.equal((await pending).status, 200);
  assert.equal(SessionManager.open(path).getSessionName(), "After shutdown");

  globalThis.__piSessions.clear();
  assert.equal((await rename(id, "")).status, 200);
  assert.equal(SessionManager.open(path).getSessionName(), undefined, "an empty name still clears the title");
});
