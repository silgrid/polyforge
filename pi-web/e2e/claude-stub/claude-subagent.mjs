#!/usr/bin/env node
/**
 * PATH-injected stand-in for the `claude` CLI (e2e, authored before
 * `code`). It replays the real 2.1.312 background-subagent recording under
 * `lib/__fixtures__/claude-2.1.312/subagent/`, rewriting the recorded session id
 * and cwd to this launch's, and writing claude-shaped transcript/sidecar files
 * for the history path. Between the subagent's text and its `task_notification`
 * it holds for PI_E2E_SUBAGENT_HOLD_MS so a page can expand the still-running row.
 *
 * Records only argv, the session id and the process id to PI_E2E_CLAUDE_STUB_LOG.
 */
import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureDir = process.env.PI_E2E_CLAUDE_FIXTURE_DIR || join(here, "..", "..", "lib/__fixtures__/claude-2.1.312/subagent");
const recordSessionId = "44444444-4444-4444-8444-444444444444";
const agentId = "a9fb2ff3aa8e8ff40";
const token = "SUBAGENT_PROBE_TOKEN_253";
const holdMs = Number(process.env.PI_E2E_SUBAGENT_HOLD_MS || 30_000);
const valueOf = (flag) => {
  const equals = process.argv.slice(2).find((arg) => arg.startsWith(`${flag}=`));
  if (equals) return equals.slice(flag.length + 1);
  const index = process.argv.slice(2).indexOf(flag);
  return index >= 0 ? process.argv.slice(2)[index + 1] : undefined;
};
const sessionId = valueOf("--resume") ?? valueOf("--session-id") ?? recordSessionId;
const cwd = process.cwd();
const configDir = process.env.CLAUDE_CONFIG_DIR || join(process.env.HOME || "", ".claude");
const lines = (name) => readFileSync(join(fixtureDir, name), "utf8").split("\n").filter(Boolean);
const rewrite = (text) => text
  .replaceAll(recordSessionId, sessionId)
  .replaceAll("/tmp/claude-rec-253/work", cwd)
  .replaceAll("/tmp/claude-0/-tmp-claude-rec-253-work", `/tmp/claude-0/${cwd.replace(/[^A-Za-z0-9._-]/g, "-")}`);
const events = lines("stream.jsonl").map((line) => JSON.parse(rewrite(line)));
const transcriptLines = lines("transcript.jsonl").map((line) => rewrite(line));
const sidecarLines = lines(`subagents/agent-${agentId}.jsonl`).map((line) => rewrite(line));
const meta = rewrite(readFileSync(join(fixtureDir, `subagents/agent-${agentId}.meta.json`), "utf8")).trim();
const transcriptPath = join(configDir, "projects", cwd.replace(/[^A-Za-z0-9._-]/g, "-"), `${sessionId}.jsonl`);
const sidecarDir = join(dirname(transcriptPath), sessionId, "subagents");
const emit = (event) => process.stdout.write(`${JSON.stringify(event)}\n`);
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const write = (path, text) => {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, text);
};

if (process.env.PI_E2E_CLAUDE_STUB_LOG) {
  appendFileSync(process.env.PI_E2E_CLAUDE_STUB_LOG, `${JSON.stringify({ kind: "launch", pid: process.pid, sessionId, argv: process.argv.slice(2), cwd, configDir })}\n`);
}
process.on("SIGTERM", () => process.exit(0));
process.on("SIGINT", () => process.exit(0));

emit(events[0]); // the real init event, so the session knows its id and tool list

let replayed = false;
let buffer = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  let newline = buffer.indexOf("\n");
  while (newline >= 0) {
    const line = buffer.slice(0, newline).trim();
    buffer = buffer.slice(newline + 1);
    newline = buffer.indexOf("\n");
    if (!line || replayed) continue;
    let event;
    try { event = JSON.parse(line); } catch { continue; }
    if (event?.type !== "user") continue;
    replayed = true;
    void replay();
  }
});
process.stdin.on("end", () => process.exit(0));

async function replay() {
  const notification = transcriptLines.findIndex((line) => JSON.parse(line).turnOrigin === "task_notification");
  write(transcriptPath, `${transcriptLines.slice(0, notification).join("\n")}\n`);
  let subagentStarted = false;
  let followUpWritten = false;
  for (const event of events.slice(1)) {
    if ((!subagentStarted) && typeof event.parent_tool_use_id === "string" && event.parent_tool_use_id) {
      subagentStarted = true;
      write(join(sidecarDir, `agent-${agentId}.jsonl`), `${sidecarLines.join("\n")}\n`);
      write(join(sidecarDir, `agent-${agentId}.meta.json`), `${meta}\n`);
    }
    if (event.type === "system" && event.subtype === "init" && !followUpWritten) {
      followUpWritten = true;
      appendFileSync(transcriptPath, `${transcriptLines.slice(notification).join("\n")}\n`);
    }
    emit(event);
    const content = event?.message?.content;
    const isSubagentText = event.type === "assistant"
      && typeof event.parent_tool_use_id === "string"
      && JSON.stringify(content || "").includes(token);
    await sleep(isSubagentText ? holdMs : 25);
  }
  process.stdin.resume();
}
