// repro: every pi-web `pf` call site must honor `PF_BIN`.
//
// Reported failure: the service process already carried
// `PF_BIN=/root/.local/share/polyforge/bin/pf`, but every call site spawned the
// bare command `pf`, which only searches PATH. A shell whose PATH lacked the
// polyforge bin directory then made the dock report "Polyforge is not
// installed" even though pf was running and reachable through PF_BIN.
//
// These tests put a stub `pf` at an absolute path outside PATH and assert that
// detection, the dock reads, memory, messages, tasks and the configuration
// read/write paths all execute it. The same stub is then removed to pin the
// absent case: PF_BIN pointing at a missing file reads as "not installed"
// with a visible reason, never a crash. Finally, with PF_BIN unset, the old
// PATH lookup still works unchanged.
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);

const TASK_ID = "task_0123456789abcdef";
const MEMORY_ID = "mem_abc123";
const CONVERSATION_ID = "conv_1";
const MESSAGE_ID = "msg_1";

function stubTree() {
  const work = mkdtempSync(join(tmpdir(), "pf-bin-"));
  const bin = join(work, "pf-bin");
  const emptyPath = join(work, "no-pf-path");
  const agent = join(work, "agent");
  mkdirSync(bin, { recursive: true });
  mkdirSync(emptyPath, { recursive: true });
  mkdirSync(agent, { recursive: true });
  const log = join(work, "argv.log");
  writeFileSync(log, "");
  const pf = join(bin, "pf");
  writeFileSync(pf, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args) + "\\n");
const rest = args.filter((a) => a !== "--json");
const out = (value) => { process.stdout.write(JSON.stringify(value) + "\\n"); process.exit(0); };
const hit = {
  id: ${JSON.stringify(MEMORY_ID)},
  type: "experience.wrap",
  title: "a memory",
  visibility: "project",
  status: "active",
  tags: ["pf"],
  related: [],
  work_item_id: "wi_E2Eaaaa0001",
  created_at: "2026-10-30T00:00:00Z",
  updated_at: "2026-10-30T00:00:00Z",
  content_len: 4,
};
const task = {
  id: ${JSON.stringify(TASK_ID)},
  state: "running",
  prompt_summary: "a task",
  spec: { project: "workbench", candidates: ["pi:a"], max_duration_ns: 1, max_tokens: 2, prompt: "do the task" },
  started_at: "2026-10-30T00:00:00Z",
  wall_consumed_ns: 0,
  tokens_consumed: 0,
  created_at: "2026-10-30T00:00:00Z",
};
const list = {
  file: { path: "/tmp/cli.toml", sha256: "file-sha", exists: true },
  items: [
    { key: "user_id", value: "u_self", source: "file", class: "live" },
    { key: "max_concurrent_jobs", value: "4", source: "file", class: "live", schema: { kind: "int", min: 1 } },
    { key: "[workspace.\\"workbench\\"]", value: "workbench,pi", class: "live" },
  ],
};
const workspace = {
  mapped: true,
  workspace: "workbench",
  yaml: { path: "/tmp/ws/.polyforge.yaml", sha256: "yaml-sha", exists: true },
  summarize: {
    models: { value: ["pi:a"], source: "file", class: "live" },
    language: { value: "中文", source: "default", class: "live" },
    interval_sec: { value: 30, source: "file", class: "live" },
  },
  steps: {},
  steps_complete: true,
};
if (args.includes("--version")) { process.stdout.write("v2.20.13\\n"); process.exit(0); }
if (rest[0] === "ops" && rest[1] === "status") out({ queue: { running: 0, max_concurrent: 4 }, jobs: [], tasks: { running: 0, completed: 0 } });
if (rest[0] === "watch") out({ work_item: "wi_E2Eaaaa0001", slug: "task#189", known: true, execution: { workflow_status: "running", current_step: "repro", steps: { repro: { status: "in_progress", started_at: "2026-10-30T00:00:00Z" } } }, events: [] });
if (rest[0] === "task" && rest[1] === "list") out({ tasks: [task] });
if (rest[0] === "task" && rest[1] === "show") out(task);
if (rest[0] === "recall") out({ items: [hit], total: 1, next_cursor: "next-page" });
if (rest[0] === "memory" && rest[1] === "show") out({ ...hit, content: "body" });
if (rest[0] === "config" && rest[1] === "list") out(list);
if (rest[0] === "config" && rest[1] === "workspace") out(workspace);
if (rest[0] === "config" && rest[1] === "reload") out({ outcome: "reloaded", pending: [], overridden: [] });
if (rest[0] === "config" && (rest[1] === "set" || rest[1] === "unset")) out({ outcome: "applied" });
if (rest[0] === "msg" && rest[1] === "contacts") out({ contacts: [{ id: "u_peer", display_name: "Peer" }] });
if (rest[0] === "msg" && rest[1] === "list") out({
  conversations: [{ id: ${JSON.stringify(CONVERSATION_ID)}, kind: "direct", members: ["u_self", "u_peer"], member_names: { u_peer: "Peer" }, last_seq: 1, unread: 0 }],
  messages: [{ id: ${JSON.stringify(MESSAGE_ID)}, seq: 1, text: "hi", created_at: "2026-10-30T00:00:00Z", sender_id: "u_peer" }],
  last_seq: 1,
});
if (rest[0] === "msg" && rest[1] === "send") out({ id: ${JSON.stringify(MESSAGE_ID)}, seq: 1, text: "hi", created_at: "2026-10-30T00:00:00Z", sender_id: "u_self", conversation_id: ${JSON.stringify(CONVERSATION_ID)} });
if (rest[0] === "msg" && rest[1] === "ack") out({ outcome: "acknowledged" });
if (rest[0] === "file" && rest[1] === "create") out({ id: "wi_new", slug: "task#900" });
if (rest[0] === "msg" && rest[1] === "analyze") out({ message_id: rest[2], task_id: "task_0123456789abcdef", project: "workbench", type: "bug", summary: "done", suggested_actions: ["reply", "bogus"], model: "pi:a", duration: "1.2s", reply_draft: "ok", conclusion: "retired" });
process.stderr.write("pf stub: unknown command " + JSON.stringify(rest) + "\\n");
process.exit(2);
`);
  chmodSync(pf, 0o755);
  return { work, bin, emptyPath, agent, log, pf };
}

function useStub(t, tree, { pfBin, path }) {
  const old = {
    PF_BIN: process.env.PF_BIN,
    PATH: process.env.PATH,
    HOME: process.env.HOME,
    PI_CODING_AGENT_DIR: process.env.PI_CODING_AGENT_DIR,
  };
  const restore = (key, value) => {
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  };
  if (pfBin === undefined) delete process.env.PF_BIN;
  else process.env.PF_BIN = pfBin;
  process.env.PATH = path;
  process.env.HOME = tree.work;
  process.env.PI_CODING_AGENT_DIR = tree.agent;
  t.after(() => {
    restore("PF_BIN", old.PF_BIN);
    restore("PATH", old.PATH);
    restore("HOME", old.HOME);
    restore("PI_CODING_AGENT_DIR", old.PI_CODING_AGENT_DIR);
    rmSync(tree.work, { recursive: true, force: true });
  });
}

const calls = (tree) => readFileSync(tree.log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));

test("a valid PF_BIN outside PATH is used by every pf call site", async (t) => {
  const tree = stubTree();
  // PATH deliberately has no pf at all: only PF_BIN can reach the stub.
  useStub(t, tree, { pfBin: tree.pf, path: `${tree.emptyPath}${delimiter}/usr/bin${delimiter}/bin` });
  const absent = spawnSync("sh", ["-c", "command -v pf"], { encoding: "utf8", env: process.env });
  assert.notEqual(absent.status, 0, "fixture sanity: PATH must not resolve a bare pf");

  const status = await jiti.import("./polyforge-status.ts");
  const tasks = await jiti.import("./polyforge-tasks.ts");
  const memory = await jiti.import("./polyforge-memory.ts");
  const messages = await jiti.import("./polyforge-messages.ts");
  const config = await jiti.import("./polyforge-config.ts");
  const configWrite = await jiti.import("./polyforge-config-write.ts");

  const presence = await status.probePolyforgeVersion(true);
  assert.deepEqual(presence, { present: true, version: "v2.20.13" }, "the presence probe must execute PF_BIN");

  const ops = await status.readPolyforgeOpsStatus();
  assert.equal(ops?.queue.max, 4, "the ops read must execute PF_BIN");
  const watch = await status.getPolyforgeWatch("task#189");
  assert.equal(watch?.currentStep, "repro", "the watch read must execute PF_BIN");

  const taskRows = await tasks.listPolyforgeTasks();
  assert.equal(taskRows[0]?.id, TASK_ID, "the task list must execute PF_BIN");
  const taskDetail = await tasks.showPolyforgeTask(TASK_ID);
  assert.equal(taskDetail?.id, TASK_ID, "the task detail must execute PF_BIN");

  const hits = await memory.searchPolyforgeMemory({ cwd: tree.work, q: "pf bin", project: "workbench", type: "*" });
  assert.equal(hits[0]?.id, MEMORY_ID, "the memory search must execute PF_BIN");
  const detail = await memory.showPolyforgeMemory(MEMORY_ID);
  assert.equal(detail?.id, MEMORY_ID, "the memory detail must execute PF_BIN");
  const projects = await memory.readPolyforgeMemoryProjects(tree.work);
  assert.deepEqual(projects, { mapped: true, projects: ["workbench", "pi"] }, "the project scope read must execute PF_BIN");

  const messageRead = await messages.readPolyforgeMessages();
  assert.equal(messageRead.capable, true, "the messages capability read must execute PF_BIN");
  assert.equal(messageRead.conversations[0]?.id, CONVERSATION_ID, "the message list must execute PF_BIN");
  assert.deepEqual(messageRead.contacts, [{ id: "u_peer", display_name: "Peer" }], "the contacts read must execute PF_BIN");
  const page = await messages.readPolyforgeNewestPage(CONVERSATION_ID);
  assert.equal(page.messages[0]?.id, MESSAGE_ID, "the newest page must execute PF_BIN");
  const sent = await messages.sendPolyforgeMessage({ to: "u_peer", text: "hi" });
  assert.equal(sent?.id, MESSAGE_ID, "the send write must execute PF_BIN");
  await messages.acknowledgePolyforgeMessage(CONVERSATION_ID, 1);
  const analysis = await messages.analyzePolyforgeMessage(MESSAGE_ID);
  assert.deepEqual(analysis, {
    message_id: MESSAGE_ID, task_id: "task_0123456789abcdef", project: "workbench", type: "bug", summary: "done",
    suggested_actions: ["reply"], model: "pi:a", duration: "1.2s", reply_draft: "ok",
  }, "the analyze read must execute PF_BIN and answer only the structured fields");
  const ticket = await messages.createPolyforgeFileTicket({ goal: "g", project: "workbench", content: "c" });
  assert.deepEqual(ticket, { slug: "task#900" }, "the 建票 write must execute PF_BIN");
  assert.deepEqual(calls(tree).filter((argv) => argv.includes("create")).at(-1),
    ["--json", "file", "create", "--project", "workbench", "--goal", "g", "--content", "c"], "the 建票 argv carries no --bundle");

  const configRead = await config.readPolyforgeConfig(tree.work, { profile: "default" });
  assert.equal(configRead.capable, true, "the config read must execute PF_BIN");
  assert.equal(configRead.items.some((item) => item.key === "max_concurrent_jobs"), true);
  const write = await configWrite.writePolyforgeConfig({
    action: "set",
    key: "max_concurrent_jobs",
    value: 5,
    cwd: tree.work,
    expect_state: { file_sha256: "file-sha", values: { max_concurrent_jobs: "4" } },
  });
  assert.equal(write.ok, true, "the config write must execute PF_BIN");
  const reload = await configWrite.reloadPolyforgeConfig({ profile: "default" });
  assert.equal(reload.daemon?.queue?.max_concurrent, 4, "the reload and its ops read must execute PF_BIN");

  const executed = calls(tree).length;
  assert.ok(executed >= 15, `every read/write above must have spawned PF_BIN (saw ${executed} calls)`);
});

test("PF_BIN pointing at a missing file reads as not installed, with a readable reason", async (t) => {
  const tree = stubTree();
  const missing = join(tree.work, "missing", "pf");
  useStub(t, tree, { pfBin: missing, path: `${tree.emptyPath}${delimiter}/usr/bin${delimiter}/bin` });
  const status = await jiti.import("./polyforge-status.ts");
  const tasks = await jiti.import("./polyforge-tasks.ts");
  const memory = await jiti.import("./polyforge-memory.ts");
  const messages = await jiti.import("./polyforge-messages.ts");
  const config = await jiti.import("./polyforge-config.ts");

  assert.deepEqual(await status.probePolyforgeVersion(true), { present: false, version: null });

  const configRead = await config.readPolyforgeConfig(tree.work, { profile: "default" });
  assert.equal(configRead.pfPresent, false);
  assert.match(configRead.reason ?? "", /not installed/i, "the panel must carry a readable not-installed reason");

  const messagesRead = await messages.readPolyforgeMessages();
  assert.equal(messagesRead.capable, false, "Messages must report unavailable, not crash");
  const tasksRead = await tasks.readPolyforgeTasksCapability();
  assert.equal(tasksRead.capable, false, "Tasks must report unavailable, not crash");
  const memoryRead = await memory.readPolyforgeMemoryCapability();
  assert.equal(memoryRead.capable, false, "Memory must report unavailable, not crash");
  assert.equal(calls(tree).length, 0, "a missing PF_BIN must never evoke the stub (PATH has no pf either)");
});

test("without PF_BIN the old PATH lookup is unchanged", async (t) => {
  const tree = stubTree();
  useStub(t, tree, { pfBin: undefined, path: `${tree.bin}${delimiter}${tree.emptyPath}${delimiter}/usr/bin${delimiter}/bin` });
  const status = await jiti.import("./polyforge-status.ts");
  assert.deepEqual(await status.probePolyforgeVersion(true), { present: true, version: "v2.20.13" });
  assert.equal(calls(tree).length, 1, "the PATH-resolved stub answered the one presence probe");
});