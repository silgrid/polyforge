import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fauxAssistantMessage, fauxProvider, fauxText, getCurrentSystemPrompt, getCurrentTools } from "@earendil-works/pi-ai";
import { ModelRuntime, SessionManager } from "@earendil-works/pi-coding-agent";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const { createSubagentController } = await jiti.import("./subagent-runtime.ts");
const { readSubagentSessionResources, SUBAGENT_META_TYPE } = await jiti.import("./subagents.ts");

// Drive the real controller, profile parser, SDK resource loader and persistent
// child session. A source-pattern check cannot catch an SDK startup path that
// reactivates tools after the selector helper has filtered them out.
test("subagent extension selections and denials reach the child and its persisted resource snapshot", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "pi-web-subagent-loadout-"));
  const agentDir = join(dir, "agent");
  const cwd = join(dir, "project");
  const previousEnv = { PI_CODING_AGENT_DIR: process.env.PI_CODING_AGENT_DIR, PI_OFFLINE: process.env.PI_OFFLINE };
  const children = [];
  process.env.PI_CODING_AGENT_DIR = agentDir;
  process.env.PI_OFFLINE = "1";
  t.after(async () => {
    for (const child of children) child.dispose();
    for (const [key, value] of Object.entries(previousEnv)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
    await rm(dir, { recursive: true, force: true });
  });
  await mkdir(join(agentDir, "extensions"), { recursive: true });
  await mkdir(join(agentDir, "agents"), { recursive: true });
  await mkdir(cwd);
  await writeFile(join(agentDir, "settings.json"), JSON.stringify({
    packages: [],
    compaction: { enabled: false },
    retry: { enabled: false },
  }));
  for (const [name, tools] of [["foo", ["foo_allowed", "foo_denied"]], ["bar", ["bar_tool"]]]) {
    await writeFile(join(agentDir, "extensions", `${name}.ts`), `
      export default function (pi) {
        for (const name of ${JSON.stringify(tools)}) {
          pi.registerTool({
            name, label: name, description: name,
            parameters: { type: "object", properties: {} },
            async execute() { throw new Error("The loadout test must not execute tools"); },
          });
        }
      }
    `);
  }

  const faux = fauxProvider({ models: [{ id: "loadout-test" }] });
  const modelRuntime = await ModelRuntime.create({
    authPath: join(agentDir, "auth.json"), modelsPath: null, refreshOnCreate: false,
  });
  modelRuntime.registerNativeProvider(faux.provider);
  const parent = {
    cwd,
    sessionFile: join(dir, "parent.jsonl"),
    isAlive: () => true,
    inner: {
      sessionManager: { getSessionId: () => "loadout-parent" },
      getActiveToolNames: () => [],
      agent: { state: { thinkingLevel: "off" } },
      modelRuntime,
      model: faux.getModel("loadout-test"),
    },
  };
  const controller = createSubagentController({
    getSession: (id) => id === "loadout-parent" ? parent : undefined,
    registerSession: (inner) => children.push(inner),
    reopenSession: async () => { throw new Error("unexpected reopen"); },
    resolveSessionPath: async () => null,
    invalidateSessionList: () => {},
    isBuiltInSubagentsEnabled: () => true,
  });

  const cases = [
    { name: "unscoped", tools: "read", expected: ["read", "foo_allowed", "foo_denied", "bar_tool"] },
    { name: "explicit-emptied", tools: "read, ext:foo/*", denied: "ext:foo", expected: ["read"] },
    { name: "default-denied", tools: "read", denied: "ext:foo", expected: ["read", "bar_tool"] },
    { name: "default-deny-all", tools: "read", denied: "ext:*", expected: ["read"] },
    { name: "narrow-denial", tools: "read, ext:foo/*", denied: "ext:foo/foo_denied", expected: ["read", "foo_allowed"] },
    { name: "extensions-off", tools: "read, ext:foo/*", loadExtensions: false, expected: ["read"] },
  ];
  for (const scenario of cases) {
    await t.test(scenario.name, async () => {
      await writeFile(join(agentDir, "agents", `${scenario.name}.md`), [
        "---",
        `tools: ${scenario.tools}`,
        `load_extensions: ${scenario.loadExtensions ?? true}`,
        "load_skills: false",
        "isolation: off",
        ...(scenario.denied ? [`disallowed_tools: ${scenario.denied}`] : []),
        "---",
        "Return a short report without calling tools.",
      ].join("\n"));
      let declared;
      faux.setResponses([(context) => {
        declared = getCurrentTools(context.messages).map((tool) => tool.name).sort();
        return fauxAssistantMessage([fauxText("loadout checked")]);
      }]);
      const execution = await controller.extensionRuntime.start({
        parentContext: parent.inner,
        parentToolCallId: `call-${scenario.name}`,
        profile: scenario.name,
        task: "Report readiness without calling tools.",
        description: scenario.name,
      });
      const result = await execution.completion;
      assert.equal(result.status, "completed", result.error);
      const child = children.at(-1);
      const expected = [...scenario.expected].sort();
      assert.deepEqual(child.getActiveToolNames().sort(), expected, "actual child loadout");
      assert.deepEqual(declared, expected, "tools declared to the provider");

      const entries = (await readFile(child.sessionFile, "utf8")).trim().split("\n").map(JSON.parse);
      const metadata = entries.find((entry) => entry.type === "custom" && entry.customType === SUBAGENT_META_TYPE);
      assert.ok(metadata, "metadata must be persisted, not just present in memory");
      assert.deepEqual([...metadata.data.resourceSnapshot.tools].sort(), expected);
      assert.equal(metadata.data.resourceSnapshot.loadExtensions, scenario.loadExtensions ?? true);
      // The reload path must read the same narrowed loadout from disk.
      const resources = readSubagentSessionResources(SessionManager.open(child.sessionFile).getEntries());
      assert.deepEqual([...resources.tools].sort(), expected);
      child.dispose();
    });
  }
});

// (review s10#4): a pi subagent inherits its parent session's Agent language
// through the real controller and persisted child session, not only through composition.
test("a subagent inherits its parent's Agent language: snapshot, its own entry and the last appended section", async (t) => {
  const { AGENT_LANGUAGE_INSTRUCTION } = await jiti.import("./agent-language.ts");
  const { SESSION_AGENT_LANGUAGE_TYPE } = await jiti.import("./session-agent-language.ts");
  const dir = await mkdtemp(join(tmpdir(), "pi-web-subagent-language-"));
  const agentDir = join(dir, "agent");
  const cwd = join(dir, "project");
  const previousEnv = { PI_CODING_AGENT_DIR: process.env.PI_CODING_AGENT_DIR, PI_OFFLINE: process.env.PI_OFFLINE };
  const children = [];
  process.env.PI_CODING_AGENT_DIR = agentDir;
  process.env.PI_OFFLINE = "1";
  t.after(async () => {
    for (const child of children) child.dispose();
    for (const [key, value] of Object.entries(previousEnv)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
    await rm(dir, { recursive: true, force: true });
  });
  await mkdir(join(agentDir, "agents"), { recursive: true });
  await mkdir(cwd);
  await writeFile(join(agentDir, "settings.json"), JSON.stringify({
    packages: [],
    compaction: { enabled: false },
    retry: { enabled: false },
  }));
  await writeFile(join(agentDir, "agents", "language.md"), [
    "---",
    "tools: read",
    "load_extensions: false",
    "load_skills: false",
    "isolation: off",
    "---",
    "LANGUAGE PROFILE PROMPT",
  ].join("\n"));

  const faux = fauxProvider({ models: [{ id: "language-test" }] });
  const modelRuntime = await ModelRuntime.create({
    authPath: join(agentDir, "auth.json"), modelsPath: null, refreshOnCreate: false,
  });
  modelRuntime.registerNativeProvider(faux.provider);
  const makeParent = (id, agentLanguage) => ({
    cwd,
    sessionFile: join(dir, `${id}.jsonl`),
    isAlive: () => true,
    ...(agentLanguage ? { agentLanguage } : {}),
    inner: {
      sessionManager: { getSessionId: () => id },
      getActiveToolNames: () => [],
      agent: { state: { thinkingLevel: "off" } },
      modelRuntime,
      model: faux.getModel("language-test"),
    },
  });
  const parents = new Map([
    ["parent-zh", makeParent("parent-zh", "zh-CN")],
    ["parent-en", makeParent("parent-en", "en")],
    ["parent-absent", makeParent("parent-absent", undefined)],
  ]);
  const controller = createSubagentController({
    getSession: (id) => parents.get(id),
    registerSession: (inner) => children.push(inner),
    reopenSession: async () => { throw new Error("unexpected reopen"); },
    resolveSessionPath: async () => null,
    invalidateSessionList: () => {},
    isBuiltInSubagentsEnabled: () => true,
  });

  async function runChild(parentId) {
    const parent = parents.get(parentId);
    let systemPrompt;
    faux.setResponses([(context) => {
      systemPrompt = getCurrentSystemPrompt(context.messages);
      return fauxAssistantMessage([fauxText("language checked")]);
    }]);
    const execution = await controller.extensionRuntime.start({
      parentContext: parent.inner,
      parentToolCallId: `call-${parentId}`,
      profile: "language",
      task: "Report readiness without calling tools.",
      description: parentId,
    });
    const result = await execution.completion;
    assert.equal(result.status, "completed", result.error);
    const child = children.at(-1);
    const entries = (await readFile(child.sessionFile, "utf8")).trim().split("\n").map(JSON.parse);
    const snapshot = entries.find((entry) => entry.type === "custom" && entry.customType === SUBAGENT_META_TYPE)?.data.resourceSnapshot;
    assert.ok(snapshot, "metadata must be persisted, not just present in memory");
    const languageEntries = entries
      .filter((entry) => entry.type === "custom" && entry.customType === SESSION_AGENT_LANGUAGE_TYPE)
      .map((entry) => entry.data);
    child.dispose();
    return { snapshot, languageEntries, systemPrompt };
  }

  await t.test("中文 parent", async () => {
    const { snapshot, languageEntries, systemPrompt } = await runChild("parent-zh");
    assert.equal(snapshot.agentLanguage, "zh-CN");
    assert.deepEqual(languageEntries, [{ version: 1, language: "zh-CN" }]);
    assert.equal(snapshot.appendSystemPrompt.at(-1), AGENT_LANGUAGE_INSTRUCTION, "the instruction is the last appended section");
    assert.equal(snapshot.appendSystemPrompt.filter((section) => section === AGENT_LANGUAGE_INSTRUCTION).length, 1);
    assert.ok(systemPrompt.includes("LANGUAGE PROFILE PROMPT"));
    assert.ok(systemPrompt.includes(AGENT_LANGUAGE_INSTRUCTION), "the provider system prompt carries the instruction");
  });

  for (const parentId of ["parent-en", "parent-absent"]) {
    await t.test(parentId === "parent-en" ? "English parent" : "parent without a language", async () => {
      const { snapshot, languageEntries, systemPrompt } = await runChild(parentId);
      assert.notEqual(snapshot.agentLanguage, "zh-CN");
      assert.ok(!languageEntries.some((data) => data.language === "zh-CN"), "no 中文 language entry");
      assert.ok(!snapshot.appendSystemPrompt.includes(AGENT_LANGUAGE_INSTRUCTION), "no appended instruction");
      assert.ok(systemPrompt.includes("LANGUAGE PROFILE PROMPT"));
      assert.ok(!systemPrompt.includes(AGENT_LANGUAGE_INSTRUCTION), "the provider system prompt carries no instruction");
    });
  }
});
