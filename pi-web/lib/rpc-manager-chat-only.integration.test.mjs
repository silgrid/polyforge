import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createAgentSessionServices } from "@earendil-works/pi-coding-agent";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { interopDefault: true });

const EXTENSION_MODEL_ERROR = "extprov/ext-model is provided by an extension, and Chat only loads no extensions. Choose another model or tool preset.";

test("Chat only names a model only an extension provides instead of a bare not found (#804)", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "pi-web-chat-only-model-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const previousDir = process.env.PI_CODING_AGENT_DIR;
  process.env.PI_CODING_AGENT_DIR = dir;
  t.after(() => { if (previousDir === undefined) delete process.env.PI_CODING_AGENT_DIR; else process.env.PI_CODING_AGENT_DIR = previousDir; });
  // The extension's provider is the only model source, and the default model.
  await mkdir(join(dir, "extensions"), { recursive: true });
  await writeFile(join(dir, "extensions", "extprov.ts"), `export default function (pi) {
  pi.registerProvider("extprov", { baseUrl: "http://127.0.0.1:9", apiKey: "dummy", api: "openai-completions",
    models: [{ id: "ext-model", name: "Ext", reasoning: false, input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }] });
}
`);
  await writeFile(join(dir, "settings.json"), JSON.stringify({ defaultProvider: "extprov", defaultModel: "ext-model" }));

  const { rememberProviderModels } = await jiti.import("./deferred-provider-models.ts");
  const { startRpcSession } = await jiti.import("./rpc-manager.ts");
  // The model selector's listing loads extensions, as /api/models does.
  const listing = await createAgentSessionServices({ cwd: dir, agentDir: dir });
  await rememberProviderModels(listing.modelRuntime);

  const { session } = await startRpcSession("__new__chat-only", "", dir, { toolNames: [] });
  t.after(() => session.shutdown());
  await assert.rejects(
    session.send({ type: "set_model", provider: "extprov", modelId: "ext-model" }),
    { message: EXTENSION_MODEL_ERROR },
  );
  // Left without a model, the prompt names the default it could not load, not a missing API key.
  // NOTE(fork): NOT assertable on pi SDK 1.0.0 — a chat-only session whose default model is
  // unavailable never ends up model-less (createAgentSessionFromServices falls back to a
  // built-in default), so the upstream-verbatim prompt guard cannot fire. It activates with
  // the SDK 1.1.0 bump (PR #81's lane); the guard itself is grafted verbatim in rpc-manager.ts.

  // A model the browser chose is refused rather than replaced by another one.
  await assert.rejects(
    startRpcSession("__new__chat-only-chosen", "", dir, {
      toolNames: [],
      initialModel: { provider: "extprov", modelId: "ext-model" },
    }),
    { message: EXTENSION_MODEL_ERROR },
  );
});

const INSTRUCTION = "Use Simplified Chinese (简体中文) for replies, narration and reports. Keep code, code comments, commit messages, files you write, technical terms and identifiers as they are, or as the project's conventions require. Thinking in Chinese is best effort only.";

test("Chat only sends its exact prompt plus the Agent language instruction for 中文, and the exact prompt for English", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "pi-web-chat-only-language-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const previousDir = process.env.PI_CODING_AGENT_DIR;
  process.env.PI_CODING_AGENT_DIR = join(dir, "agent");
  t.after(() => { if (previousDir === undefined) delete process.env.PI_CODING_AGENT_DIR; else process.env.PI_CODING_AGENT_DIR = previousDir; });
  await mkdir(join(dir, "agent"), { recursive: true });
  const cwd = join(dir, "project");
  await mkdir(cwd);
  await writeFile(join(cwd, "AGENTS.md"), "EXACT CHAT PROMPT");
  const { startRpcSession } = await jiti.import("./rpc-manager.ts");
  const { readSessionAgentLanguage } = await jiti.import("./session-agent-language.ts");

  // The server setting decides a new session's language.
  await writeFile(join(dir, "agent", "pi-web-settings.json"), JSON.stringify({ version: 1, agentLanguage: "zh-CN" }));
  const { session: zh } = await startRpcSession("__new__chat-only-zh", "", cwd, { toolNames: [] });
  t.after(() => zh.shutdown());
  assert.equal((await zh.send({ type: "get_state" })).systemPrompt, `EXACT CHAT PROMPT\n\n${INSTRUCTION}`);
  assert.equal(readSessionAgentLanguage(zh.inner.sessionManager.getEntries()), "zh-CN");

  await writeFile(join(dir, "agent", "pi-web-settings.json"), JSON.stringify({ version: 1, agentLanguage: "en" }));
  const { session: en } = await startRpcSession("__new__chat-only-en", "", cwd, { toolNames: [] });
  t.after(() => en.shutdown());
  assert.equal((await en.send({ type: "get_state" })).systemPrompt, "EXACT CHAT PROMPT");
  // The existing 中文 session keeps its language after the setting changed.
  assert.equal((await zh.send({ type: "get_state" })).systemPrompt, `EXACT CHAT PROMPT\n\n${INSTRUCTION}`);
});
