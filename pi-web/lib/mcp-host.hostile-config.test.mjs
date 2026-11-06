import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// Repro for wi pi#105: a hostile `.pi/mcp.json` must not take the MCP host's
// batch down. One entry carries a deeply nested unknown field; the SDK's
// validator does not bound unknown fields on a stdio entry, so the entry
// reaches the host, whose per-entry keying (`canonicalJson()`) recurses once
// per nesting level. The reported failure: that recursion throws, the sync
// aborts, and every other server of the batch — the valid project entries and
// the user's global ones — never connects.

const jiti = createJiti(import.meta.url, { moduleCache: false });
const { McpHost } = await jiti.import("./mcp-host.ts");
const { loadPiSdkInternals } = await jiti.import("./pi-sdk-internals.ts");

const MCP_COMMAND = { name: "mcp", sourceInfo: { path: "builtin:mcp" } };

class FakeTransport {
  sent = [];
  messageListeners = new Set();
  closeListeners = new Set();
  async send(message) {
    this.sent.push(message);
  }
  onMessage(listener) {
    this.messageListeners.add(listener);
    return () => this.messageListeners.delete(listener);
  }
  onClose(listener) {
    this.closeListeners.add(listener);
    return () => this.closeListeners.delete(listener);
  }
  receive(message) {
    for (const listener of this.messageListeners) listener(message);
  }
  close() {
    for (const listener of this.closeListeners) listener();
  }
  /** The client side of a handshake: initialize answered, then initialized sent. */
  async handshake(capabilities) {
    this.receive({ jsonrpc: "2.0", id: 0, result: { protocolVersion: "2025-06-18", capabilities } });
    await this.send({ jsonrpc: "2.0", method: "notifications/initialized" });
  }
}

/** A JSON value nested `levels` deep: ~6 bytes per level, a few KB in total. */
function deepValue(levels) {
  const root = {};
  let current = root;
  for (let i = 0; i < levels; i++) {
    current.x = {};
    current = current.x;
  }
  return root;
}

/**
 * A host over real `mcp.json` files and the real SDK config loader, in a
 * trusted project. Every server the host registers connects at once, so a
 * prompt never waits. `projectServers` lands in `<cwd>/.pi/mcp.json` as a
 * `git pull` would deliver it.
 */
async function hostileFixture(t, { globalServers, projectServers }) {
  const sdk = await loadPiSdkInternals();
  assert.equal(sdk.ok, true, sdk.reason);
  const root = await mkdtemp(join(tmpdir(), "pi-web-mcp-host-hostile-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const agentDir = join(root, "agent");
  const cwd = join(root, "project");
  await mkdir(agentDir, { recursive: true });
  await mkdir(join(cwd, ".pi"), { recursive: true });
  await writeFile(join(agentDir, "mcp.json"), JSON.stringify({ mcpServers: globalServers }));
  await writeFile(join(cwd, ".pi", "mcp.json"), JSON.stringify({ mcpServers: projectServers }));

  const handlers = new Map();
  const registered = new Map();
  let factory;
  const pi = {
    on(event, handler) {
      handlers.set(event, [...(handlers.get(event) ?? []), handler]);
    },
    getCommands: () => [MCP_COMMAND],
    registerMcpServer(name, config) {
      registered.set(name, config);
      // The extension opens the connection and the server answers.
      void factory({ name, config, source: "<inline:pi-web-mcp-host>", scope: "extension" }, cwd, undefined).handshake({});
    },
    unregisterMcpServer(name) {
      registered.delete(name);
    },
  };
  const host = new McpHost({
    agentDir,
    internals: { loadMcpConfig: (options) => sdk.loadMcpConfig(options) },
    codemodeAvailable: () => true,
    mayReadProjectConfig: () => true,
    promptWaitMs: 500,
    idleMs: 0,
  });
  host.extension().factory(pi);
  factory = host.wrapTransportFactory(() => new FakeTransport());
  const ctx = { cwd, isProjectTrusted: () => true, isIdle: () => true };
  for (const handler of handlers.get("session_start") ?? []) handler({ type: "session_start" }, ctx);

  return {
    host,
    registered,
    prompt: () => host.prepareForPrompt(new AbortController().signal),
    /** Replace `<cwd>/.pi/mcp.json`, as a later `git pull` would. */
    writeProjectServers: (servers) => writeFile(join(cwd, ".pi", "mcp.json"), JSON.stringify({ mcpServers: servers })),
    /** What the SDK handed the host, for the precondition the report rests on. */
    loadResult: () => sdk.loadMcpConfig({ agentDir, cwd, projectTrusted: true }),
  };
}

test("a deeply nested entry must not take the batch's other servers or the global ones down", async (t) => {
  const { registered, host, prompt, loadResult } = await hostileFixture(t, {
    globalServers: { docs: { command: "docs-srv" } },
    projectServers: {
      // Hostile: an unknown field nested a few thousand levels deep, in ~24 KB.
      bomb: { command: "srv", extra: deepValue(4000) },
      good: { command: "good-srv" },
    },
  });

  // Precondition: the SDK's validator does not bound unknown fields, so the
  // hostile entry reaches the host beside the valid ones.
  const loaded = loadResult();
  assert.deepEqual(loaded.servers.map((server) => server.name), ["docs", "bomb", "good"]);
  assert.deepEqual(loaded.errors, []);

  await prompt();

  // The reported failure: the whole sync aborted on the hostile entry, so
  // neither the valid project entry nor the global one connected. The host
  // must instead survive the entry — skipped with a log, or keyed totally —
  // and connect every other server of the batch.
  assert.ok(registered.has("docs"), "the user's global server still connects");
  assert.ok(registered.has("good"), "a valid entry in the same file still connects");
  // prepareForPrompt() waits (bounded) only for servers with `direct` tools;
  // these connect in the background, so their handshakes may settle after the
  // prompt: wait for them, bounded.
  const states = () => host
    .serverStates()
    .map((server) => `${server.name}:${server.state}`)
    .filter((line) => line !== "bomb:ready" && line !== "bomb:connecting" && line !== "bomb:failed" && line !== "bomb:not-registered");
  for (const deadline = Date.now() + 2_000; Date.now() < deadline && states().some((line) => line.endsWith(":connecting"));) {
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  assert.deepEqual(states(), ["docs:ready", "good:ready"], "every other server of the batch is connected");
});

test("a hostile entry must not stop later prompts from syncing either", async (t) => {
  const { registered, prompt, writeProjectServers } = await hostileFixture(t, {
    globalServers: { docs: { command: "docs-srv" } },
    projectServers: {
      bomb: { command: "srv", extra: deepValue(4000) },
      good: { command: "good-srv" },
    },
  });

  await prompt();
  await prompt();
  assert.ok(registered.has("good"), "a later prompt still syncs the batch");

  // Removing the hostile entry restores the batch, whatever happened before.
  await writeProjectServers({ good: { command: "good-srv" } });
  await prompt();
  assert.ok(registered.has("good"), "a clean file connects its servers");
});
