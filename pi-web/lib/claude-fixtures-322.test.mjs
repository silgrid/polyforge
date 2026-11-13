// carries: lib/__fixtures__/claude-2.1.322/*
// The real claude 2.1.322 recordings (lib/__fixtures__/claude-2.1.322/README.md),
// at their falsifiable points: the subagent's text is forwarded under its Agent call with
// --forward-subagent-text, and a message relayed through the main session's SendMessage
// reaches a running subagent at its next tool round and resumes a finished one.
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const fixtures = path.join(here, "__fixtures__", "claude-2.1.322");
const exists = (file) => fs.existsSync(path.join(fixtures, file));
const jsonl = (file) => {
  assert.ok(exists(file), `${file} is not recorded`);
  return fs.readFileSync(path.join(fixtures, file), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
};
/** The stream without token deltas. */
const frames = (name) => jsonl(`${name}/stream.jsonl`).filter((event) => event.type !== "stream_event");
const blocks = (event) => (Array.isArray(event.message?.content) ? event.message.content : []);
const resultText = (block) => (Array.isArray(block.content) ? block.content.map((part) => part.text ?? "").join("") : String(block.content ?? ""));
const RELAY = (agentId, description, text) => `请用 SendMessage 把下面这段话原样转给 subagent ${agentId}（${description}），不要做别的：\n${text}`;

/** The recording's one Agent call, its run's task id and the frames' indices of the task events. */
function agentRun(name) {
  const stream = frames(name);
  const call = stream.flatMap((event) => (event.type === "assistant" && !event.parent_tool_use_id ? blocks(event) : [])).find((block) => block.type === "tool_use" && block.name === "Agent");
  assert.ok(call, `${name}: no Agent call`);
  const started = stream.find((event) => event.type === "system" && event.subtype === "task_started" && event.tool_use_id === call.id);
  assert.ok(started, `${name}: no task_started for the Agent call`);
  return { stream, call, agentId: started.task_id };
}

/** The main session's SendMessage calls to `agentId`, with their results, in order. */
function sendMessages(stream, agentId) {
  const calls = stream.flatMap((event, index) => (event.type === "assistant" && !event.parent_tool_use_id ? blocks(event).map((block) => ({ block, index })) : []))
    .filter(({ block }) => block.type === "tool_use" && block.name === "SendMessage" && block.input?.to === agentId);
  return calls.map(({ block, index }) => {
    const resultIndex = stream.findIndex((event) => event.type === "user" && !event.parent_tool_use_id && blocks(event).some((part) => part.type === "tool_result" && part.tool_use_id === block.id));
    const result = resultIndex < 0 ? undefined : blocks(stream[resultIndex]).find((part) => part.tool_use_id === block.id);
    return { id: block.id, input: block.input, index, resultIndex, result: result ? resultText(result) : undefined };
  });
}

function assertRecorded(name, files) {
  for (const file of files) assert.ok(exists(`${name}/${file}`), `${name}/${file} is not recorded`);
  const init = frames(name).find((event) => event.type === "system" && event.subtype === "init");
  assert.equal(init?.claude_code_version, "2.1.322", `${name} is not a 2.1.322 recording`);
  for (const line of jsonl(`${name}/stdin.jsonl`).filter((entry) => entry.type === "user")) {
    assert.match(line.uuid, /^[0-9a-f-]{36}$/, `${name}: a user line without its uuid`);
    assert.equal(typeof line.message.content, "string", `${name}: not pi-web's text line`);
  }
  const walk = (dir) => fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => (entry.isDirectory() ? walk(path.join(dir, entry.name)) : [path.join(dir, entry.name)]));
  for (const file of walk(path.join(fixtures, name))) {
    const text = fs.readFileSync(file, "utf8");
    assert.ok(!/sk-ant|ANTHROPIC_(AUTH_TOKEN|API_KEY|BASE_URL)|\/root\/|\/home\/|api-request|[\w.-]+@[\w-]+\.(com|io|net|org)/.test(text), `${path.relative(fixtures, file)} is not scrubbed`);
  }
}

test("the forward recording streams the subagent's text reply under its Agent call", () => {
  assertRecorded("subagent-forward", ["stdin.jsonl", "stream.jsonl"]);
  const { stream, call, agentId } = agentRun("subagent-forward");
  assert.equal(call.input.run_in_background ?? false, false, "the Agent call is not a foreground one");
  const notification = stream.findIndex((event) => event.type === "system" && event.subtype === "task_notification" && event.task_id === agentId);
  assert.ok(notification > 0, "the run has no notification");
  const routed = stream.slice(0, notification).filter((event) => event.parent_tool_use_id === call.id);
  assert.ok(routed.some((event) => event.type === "assistant" && blocks(event).some((block) => block.type === "tool_use")), "the subagent's tool call is not forwarded");
  const text = routed.filter((event) => event.type === "assistant").flatMap(blocks).filter((block) => block.type === "text" && block.text.trim());
  assert.ok(text.length > 0, "the subagent's text reply is not forwarded under its Agent call");
  assert.match(text.map((block) => block.text).join("\n"), /word-1-7/, "the forwarded reply is not the subagent's answer");
});

test("the relay recording backgrounds the running foreground Agent with background_tasks", () => {
  assertRecorded("relay", ["stdin.jsonl", "stream.jsonl", "transcript.jsonl"]);
  const { stream, call, agentId } = agentRun("relay");
  assert.equal(call.input.run_in_background ?? false, false, "the Agent call is not a foreground one");
  const request = jsonl("relay/stdin.jsonl").find((line) => line.type === "control_request");
  assert.deepEqual(request?.request, { subtype: "background_tasks", tool_use_id: call.id }, "the control request does not name the Agent call");
  const response = stream.findIndex((event) => event.type === "control_response" && event.response?.request_id === request.request_id);
  assert.ok(response > 0, "claude did not answer the background_tasks request");
  assert.equal(stream[response].response.subtype, "success");
  assert.deepEqual(stream[response].response.response, { backgrounded: true });
  const firstRound = stream.findIndex((event) => event.type === "assistant" && event.parent_tool_use_id === call.id && blocks(event).some((block) => block.type === "tool_use"));
  const notification = stream.findIndex((event) => event.type === "system" && event.subtype === "task_notification" && event.task_id === agentId);
  assert.ok(firstRound >= 0 && firstRound < response && response < notification, "the request did not reach the subagent while it ran");
  const receipt = stream.find((event) => event.type === "user" && !event.parent_tool_use_id && blocks(event).some((block) => block.type === "tool_result" && block.tool_use_id === call.id));
  assert.match(resultText(blocks(receipt).find((block) => block.tool_use_id === call.id)), /^Async agent launched successfully/, "the Agent call's result is not the async receipt");
});

test("the relay recording queues SendMessage for the running agent's next tool round", () => {
  const { stream, call, agentId } = agentRun("relay");
  const relays = jsonl("relay/stdin.jsonl").filter((line) => line.type === "user").slice(1);
  const text = "COORD-PING: when you read your next file, also say the word PONG in your final reply.";
  assert.equal(relays[0]?.message.content, RELAY(agentId, call.input.description, text), "the first relay line is not the fixed wording");
  const [send] = sendMessages(stream, agentId);
  assert.ok(send, "the main session sent no SendMessage to the agent id");
  assert.equal(send.input.message, text, "SendMessage did not carry the text verbatim");
  assert.match(send.result ?? "", new RegExp(`queued for delivery to ${agentId} at its next tool round`));
  const notification = stream.findIndex((event) => event.type === "system" && event.subtype === "task_notification" && event.task_id === agentId);
  assert.ok(send.resultIndex < notification, "the agent had finished before the message was queued");
  const sidecar = jsonl(`relay/subagents/agent-${agentId}.jsonl`);
  const delivered = sidecar.findIndex((entry) => (entry.type === "attachment" && entry.attachment?.type === "queued_command" && entry.attachment.prompt === text)
    || (entry.type === "user" && typeof entry.message?.content === "string" && entry.message.content.includes(`The coordinator sent a message while you were working:\n${text}`)));
  assert.ok(delivered > 0, "the sidecar does not hold the relayed message");
  assert.ok(sidecar.slice(delivered + 1).some((entry) => entry.type === "assistant" && blocks(entry).some((block) => block.type === "tool_use")),
    "the message did not arrive between the subagent's tool rounds");
});

test("the relay recording resumes the finished agent under its original Agent call", () => {
  const { stream, call, agentId } = agentRun("relay");
  const relays = jsonl("relay/stdin.jsonl").filter((line) => line.type === "user").slice(1);
  const text = "Now read f1.txt once more and reply with its content only.";
  assert.equal(relays[1]?.message.content, RELAY(agentId, call.input.description, text), "the second relay line is not the fixed wording");
  const firstNotification = stream.findIndex((event) => event.type === "system" && event.subtype === "task_notification" && event.task_id === agentId && event.tool_use_id === call.id);
  const resume = sendMessages(stream, agentId).find((send) => send.index > firstNotification);
  assert.ok(firstNotification > 0 && resume, "no SendMessage after the agent finished");
  assert.equal(resume.input.message, text);
  assert.match(resume.result ?? "", /Resum/, "SendMessage did not resume the finished agent");
  const started = stream.findIndex((event) => event.type === "system" && event.subtype === "task_started" && event.tool_use_id === resume.id);
  const notified = stream.findIndex((event) => event.type === "system" && event.subtype === "task_notification" && event.tool_use_id === resume.id);
  assert.ok(started > firstNotification && notified > started, "the resume's task events do not carry the SendMessage call id");
  assert.equal(stream[started].task_id, agentId, "the resume is not the same task");
  assert.equal(stream[notified].task_id, agentId);
  assert.equal(stream[notified].status, "completed");
  const resumed = stream.slice(started, notified).filter((event) => event.parent_tool_use_id);
  assert.ok(resumed.length > 0, "no subagent frame streamed during the resume");
  for (const event of resumed) assert.equal(event.parent_tool_use_id, call.id, "a resumed subagent frame does not carry the original Agent call id");
  assert.ok(resumed.some((event) => event.type === "assistant" && blocks(event).some((block) => block.type === "text" && /word-1-7/.test(block.text))), "the resumed reply is not streamed");
  const sidecars = fs.readdirSync(path.join(fixtures, "relay", "subagents")).filter((file) => file.endsWith(".jsonl"));
  assert.deepEqual(sidecars, [`agent-${agentId}.jsonl`], "the resume did not append to the agent's one sidecar");
  assert.equal(jsonl(`relay/subagents/agent-${agentId}.meta.json`)[0].toolUseId, call.id, "the sidecar's meta does not name the original Agent call");
  const sidecar = jsonl(`relay/subagents/agent-${agentId}.jsonl`);
  const coordinator = sidecar.findIndex((entry) => entry.type === "user" && typeof entry.message?.content === "string" && entry.message.content.includes(`The coordinator sent a message while you were working:\n${text}`));
  const firstReply = sidecar.findIndex((entry) => entry.type === "assistant" && blocks(entry).some((block) => block.type === "text" && /PONG/.test(block.text)));
  assert.ok(firstReply >= 0 && coordinator > firstReply, "the resume's message is not appended after the first run's reply");
  assert.ok(sidecar.slice(coordinator + 1).some((entry) => entry.type === "assistant" && blocks(entry).some((block) => block.type === "text" && /word-1-7/.test(block.text))), "the resumed reply is not in the sidecar");
});

test("the 2.1.322 permission-mode recording: success answers carry the mode, a change adds a status frame, an unknown mode is invalid_mode", () => {
  //, zero credit: control requests only, no user line, so claude ran no turn.
  const stdin = jsonl("permission-mode/stdin.jsonl");
  assert.deepEqual(stdin.map((line) => [line.type, line.request_id, line.request?.subtype, line.request?.mode]), [
    ["control_request", "pm-1", "set_permission_mode", "acceptEdits"],
    ["control_request", "pm-2", "set_permission_mode", "plan"],
    ["control_request", "pm-3", "set_permission_mode", "bogus"],
    ["control_request", "pm-4", "set_permission_mode", "default"],
  ]);
  const stream = jsonl("permission-mode/stream.jsonl");
  assert.ok(!stream.some((event) => event.type === "assistant" || event.type === "result" || event.type === "user"), "a turn ran");
  const answer = (id) => stream.findIndex((event) => event.type === "control_response" && event.response?.request_id === id);
  // Launched with --permission-mode acceptEdits: the same mode is accepted with no status frame.
  for (const [id, mode] of [["pm-1", "acceptEdits"], ["pm-2", "plan"], ["pm-4", "default"]]) {
    assert.deepEqual(stream[answer(id)].response, { subtype: "success", request_id: id, response: { mode } }, id);
  }
  const refused = stream[answer("pm-3")].response;
  assert.equal(refused.subtype, "error");
  assert.equal(refused.error_code, "invalid_mode");
  assert.equal(refused.error, "Cannot set permission mode: must be one of acceptEdits, auto, bypassPermissions, default, dontAsk, plan");
  const statuses = stream.map((event, index) => ({ event, index })).filter(({ event }) => event.type === "system" && event.subtype === "status");
  assert.deepEqual(statuses.map(({ event, index }) => [index, event.status, event.permissionMode]), [
    [answer("pm-2") + 1, null, "plan"],
    [answer("pm-4") + 1, null, "default"],
  ]);
  for (const file of ["stdin.jsonl", "stream.jsonl"]) {
    const text = fs.readFileSync(path.join(fixtures, "permission-mode", file), "utf8");
    assert.ok(!/sk-ant|ANTHROPIC_|\/root\/|\/home\/|[wW][bB][0-9]/.test(text), `permission-mode/${file} is not scrubbed`);
  }
});

test("the 2.1.322 initialize recording: claude's model aliases with their full names, its version and capabilities, at zero credit", () => {
  // one initialize control request, no user line, so claude ran no turn.
  assert.deepEqual(jsonl("initialize/stdin.jsonl"), [{ type: "control_request", request_id: "init-1", request: { subtype: "initialize" } }]);
  const stream = jsonl("initialize/stream.jsonl");
  assert.ok(!stream.some((event) => event.type === "assistant" || event.type === "result" || event.type === "user"), "a turn ran");
  const answers = stream.filter((event) => event.type === "control_response");
  assert.equal(answers.length, 1);
  assert.equal(answers[0].response.subtype, "success");
  assert.equal(answers[0].response.request_id, "init-1");
  const answer = answers[0].response.response;
  assert.equal(answer.claude_code_version, "2.1.322");
  assert.deepEqual(answer.models.map((model) => [model.value, model.resolvedModel]), [
    ["default", "claude-opus-5-5"],
    ["opus", "claude-opus-5-5"],
    ["fable", "claude-fable-5-2"],
    ["sonnet", "claude-sonnet-5-5"],
    ["haiku", "claude-haiku-5-5"],
  ]);
  assert.deepEqual(answer.capabilities, ["ui_surface_v1", "mcp_call_result_not_for_model_v1", "mcp_call_check_permissions_v1"]);
  assert.equal(answer.current_permission_mode, "default");
  for (const file of ["stdin.jsonl", "stream.jsonl"]) {
    const text = fs.readFileSync(path.join(fixtures, "initialize", file), "utf8");
    assert.ok(!/sk-ant|ANTHROPIC_|\/root\/|\/home\/|[wW][bB][0-9]|[\w.-]+@[\w-]+\.(com|io|net|org)/.test(text), `initialize/${file} is not scrubbed`);
  }
});

test("the 2.1.322 hooks recording carries hook_started and hook_response frames for its SessionStart, PreToolUse and PostToolUse hooks, with --include-hook-events", () => {
  // one haiku turn (one user line) under a throwaway config whose hooks are
  // a SessionStart, two PreToolUse (Read) — one of them `exit 1` — and a PostToolUse (Read)
  // `echo`, with pi-web's argv plus --include-hook-events.
  const stdin = jsonl("hooks/stdin.jsonl");
  assert.deepEqual(stdin.map((line) => line.type), ["user"]);
  const stream = jsonl("hooks/stream.jsonl");
  const hooks = stream.filter((event) => event.type === "system" && String(event.subtype).startsWith("hook_"));
  assert.ok(!hooks.some((event) => event.subtype === "hook_progress"), "a hook_progress frame");
  const started = hooks.filter((event) => event.subtype === "hook_started");
  const responses = hooks.filter((event) => event.subtype === "hook_response");
  assert.deepEqual(started.map((event) => event.hook_id).sort(), responses.map((event) => event.hook_id).sort(), "every hook starts and answers");
  for (const event of started) {
    const response = responses.find((candidate) => candidate.hook_id === event.hook_id);
    assert.ok(stream.indexOf(event) < stream.indexOf(response), `${event.hook_name} answers after it starts`);
    assert.equal(response.hook_name, event.hook_name);
    assert.equal(response.hook_event, event.hook_event);
  }
  assert.deepEqual(responses.map((event) => [event.hook_event, event.hook_name, event.outcome, event.exit_code]), [
    ["SessionStart", "SessionStart:startup", "success", 0],
    ["PreToolUse", "PreToolUse:Read", "success", 0],
    ["PreToolUse", "PreToolUse:Read", "error", 1],
    ["PostToolUse", "PostToolUse:Read", "success", 0],
  ]);
  const failed = responses.find((event) => event.outcome === "error");
  assert.equal(failed.stderr, "failing-hook-says-no\n");
  assert.equal(responses[0].stdout, "session-start-hook-ran\n");
  // SessionStart runs as claude starts, before the turn's init; the tool hooks inside the turn.
  const init = stream.findIndex((event) => event.type === "system" && event.subtype === "init");
  const result = stream.findIndex((event) => event.type === "result");
  assert.ok(stream.indexOf(responses[0]) < init);
  for (const event of responses.slice(1)) assert.ok(init < stream.indexOf(event) && stream.indexOf(event) < result);
  assert.equal(stream[result].subtype, "success");
  // A failed (non-blocking, exit 1) PreToolUse hook does not stop the Read.
  const read = stream.find((event) => event.type === "user" && blocks(event).some((block) => block.type === "tool_result"));
  assert.match(resultText(blocks(read).find((block) => block.type === "tool_result")), /word-445-7/);
  for (const file of ["stdin.jsonl", "stream.jsonl"]) {
    const text = fs.readFileSync(path.join(fixtures, "hooks", file), "utf8");
    assert.ok(!/sk-ant|ANTHROPIC_|\/root\/|\/home\/|[wW][bB][0-9]|[\w.-]+@[\w-]+\.(com|io|net|org)/.test(text), `hooks/${file} is not scrubbed`);
  }
});
