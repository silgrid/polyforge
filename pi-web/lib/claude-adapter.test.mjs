import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const adapter = await jiti.import("./claude-adapter.ts");
const { toClientAgentEvent } = await jiti.import("./agent-event-wire.ts");

const fixture = (name) => adapter.parseClaudeTranscript(readFileSync(new URL(`./__fixtures__/claude-2.1.312/${name}`, import.meta.url), "utf8"));

function translateAll(events) {
  const translator = new adapter.ClaudeStreamTranslator(() => 1000);
  const out = [];
  let turnEnd;
  for (const event of events) {
    const translated = translator.push(event);
    out.push(...translated.events);
    if (translated.turnEnd) turnEnd = translated.turnEnd;
  }
  return { translator, events: out, turnEnd };
}

const ended = (events) => events.filter((event) => event.type === "message_end").map((event) => event.message);

test("the recorded stream maps to streamed deltas, a tool call, its result and the turn end", () => {
  const recorded = fixture("stream.jsonl");
  const { translator, events, turnEnd } = translateAll(recorded);
  const init = recorded.find((event) => event.type === "system" && event.subtype === "init");
  assert.equal(translator.sessionId, init.session_id, "the session identity comes from the stream");

  const messages = ended(events);
  assert.deepEqual(messages.map((message) => message.role), ["assistant", "toolResult", "assistant"]);
  const [call, result, reply] = messages;
  assert.deepEqual(call.content, [{ type: "toolCall", id: "toolu_019r4f1eeY8DPjUCUz7UJ7pU", name: "Read", arguments: { file_path: "/tmp/claude-rec/work/notes.txt" } }]);
  assert.equal(call.provider, "claude");
  assert.equal(call.model, "claude-haiku-5-5");
  assert.equal(result.toolCallId, "toolu_019r4f1eeY8DPjUCUz7UJ7pU");
  assert.equal(result.toolName, "Read");
  assert.equal(result.isError, false);
  assert.match(result.content[0].text, /PINEAPPLE/);
  assert.match(reply.content[0].text, /PINEAPPLE/);

  // Token-level streaming: the text deltas, in order, make up the final reply.
  const textDeltas = events.filter((event) => event.type === "message_update" && event.assistantMessageEvent.type === "text_delta");
  assert.ok(textDeltas.length > 3);
  assert.equal(textDeltas.map((event) => event.assistantMessageEvent.delta).join(""), reply.content[0].text);
  // The tool input streams too, and reaches the browser with its id and name.
  const toolStart = events.find((event) => event.type === "message_update" && event.assistantMessageEvent.type === "toolcall_start");
  assert.deepEqual(
    { id: toClientAgentEvent(toolStart).assistantMessageEvent.id, toolName: toClientAgentEvent(toolStart).assistantMessageEvent.toolName },
    { id: "toolu_019r4f1eeY8DPjUCUz7UJ7pU", toolName: "Read" },
  );
  const execution = events.filter((event) => event.type.startsWith("tool_execution_"));
  assert.deepEqual(execution.map((event) => [event.type, event.toolCallId, event.toolName]), [
    ["tool_execution_start", "toolu_019r4f1eeY8DPjUCUz7UJ7pU", "Read"],
    ["tool_execution_end", "toolu_019r4f1eeY8DPjUCUz7UJ7pU", "Read"],
  ]);
  assert.equal(events.at(-1).type, "agent_end");
  assert.equal(turnEnd.error, undefined);
  assert.equal(translator.streamingMessage, null);
});

test("usage is the result aggregate, not the streamed per-message sum", () => {
  const recorded = fixture("stream.jsonl");
  const { turnEnd } = translateAll(recorded);
  const result = recorded.find((event) => event.type === "result");
  assert.deepEqual(turnEnd.usage, {
    input: result.usage.input_tokens,
    output: result.usage.output_tokens,
    cacheRead: result.usage.cache_read_input_tokens,
    cacheWrite: result.usage.cache_creation_input_tokens,
    cost: result.total_cost_usd,
  });
  assert.equal(turnEnd.usage.output, 283);
  const streamedOutput = recorded
    .filter((event) => event.type === "stream_event" && event.event.type === "message_start")
    .reduce((sum, event) => sum + event.event.message.usage.output_tokens, 0);
  assert.notEqual(turnEnd.usage.output, streamedOutput);
  assert.ok(streamedOutput < turnEnd.usage.output);
});

test("the recorded transcript maps to the same conversation the stream showed", () => {
  const { messages, entryIds } = adapter.claudeTranscriptToMessages(fixture("transcript.jsonl"));
  assert.deepEqual(messages.map((message) => message.role), ["user", "assistant", "toolResult", "assistant"]);
  assert.match(messages[0].content, /^Use the Glob tool/);
  const streamed = ended(translateAll(fixture("stream.jsonl")).events);
  assert.deepEqual(messages.slice(1).map((message) => message.content), streamed.map((message) => message.content));
  assert.equal(messages[2].toolName, "Read");
  assert.equal(entryIds.length, messages.length);
  assert.ok(entryIds.every((id) => /^[0-9a-f-]{36}$/.test(id)));
});

// The real claude 2.1.312 image turn (text + one PNG block).
const recordedImageLine = JSON.parse(readFileSync(new URL("./__fixtures__/claude-2.1.312/image/stdin.jsonl", import.meta.url), "utf8"));
const recordedImage = recordedImageLine.message.content.find((block) => block.type === "image");
const recordedText = recordedImageLine.message.content.find((block) => block.type === "text").text;

test("the recorded image transcript maps the image block exactly and hides claude's isMeta companion", () => {
  const entries = fixture("image/transcript.jsonl");
  assert.ok(entries.some((entry) => entry.isMeta === true && /^\[Image: source: /.test(entry.message?.content?.[0]?.text ?? "")), "the recording holds the companion");
  const { messages } = adapter.claudeTranscriptToMessages(entries);
  assert.deepEqual(messages.map((message) => message.role), ["user", "assistant"]);
  assert.deepEqual(messages[0].content, [
    { type: "text", text: recordedText },
    { type: "image", data: recordedImage.source.data, mimeType: recordedImage.source.media_type },
  ]);
  assert.equal(recordedImage.source.media_type, "image/png");
  assert.ok(messages.every((message) => JSON.stringify(message.content).indexOf("[Image: source:") === -1), "the companion renders nothing");
  const streamed = ended(translateAll(fixture("image/stream.jsonl")).events);
  assert.deepEqual(messages.slice(1).map((message) => message.content), streamed.map((message) => message.content));
});

test("the image stdin line is the line claude 2.1.312 accepted, byte for byte; image-only omits the text block", () => {
  const recordedLine = readFileSync(new URL("./__fixtures__/claude-2.1.312/image/stdin.jsonl", import.meta.url), "utf8");
  const images = [{ data: recordedImage.source.data, mimeType: recordedImage.source.media_type }];
  assert.equal(adapter.claudeUserInputLine(recordedText, images), recordedLine);
  assert.deepEqual(JSON.parse(adapter.claudeUserInputLine("", images)).message.content, [recordedImage]);
  assert.equal(adapter.claudeUserInputLine("hi", []), adapter.claudeUserInputLine("hi"));
});

test("the live prompt events carry the image blocks in pi's shape; text alone stays a string", () => {
  const images = [{ data: recordedImage.source.data, mimeType: "image/png" }];
  const [start, end] = adapter.claudeUserPromptEvents("look", images, 7);
  assert.equal(start.type, "message_start");
  assert.equal(end.type, "message_end");
  assert.deepEqual(end.message, { role: "user", content: [{ type: "text", text: "look" }, { type: "image", data: images[0].data, mimeType: "image/png" }], timestamp: 7 });
  assert.deepEqual(adapter.claudeUserPromptEvents("", images, 7)[1].message.content, [{ type: "image", data: images[0].data, mimeType: "image/png" }]);
  assert.equal(adapter.claudeUserPromptEvents("plain", [], 7)[1].message.content, "plain");
});

test("a permission denial is an error tool result naming the denial, and the turn still ends", () => {
  const { events, turnEnd } = translateAll(fixture("writable-bash-deny.jsonl"));
  const results = ended(events).filter((message) => message.role === "toolResult");
  assert.deepEqual(results.map((message) => message.isError), [false, true]);
  assert.equal(results[0].content[0].text, "allowed-ok");
  assert.match(results[1].content[0].text, /Permission for this tool use was denied/);
  assert.equal(results[1].toolName, "Bash");
  assert.equal(events.filter((event) => event.type === "tool_execution_end").at(-1).isError, true);
  assert.equal(events.at(-1).type, "agent_end");
  assert.equal(turnEnd.error, undefined);
});

test("a read-only write request ends with a refusal and no tool call (no write tool exists)", () => {
  const { events } = translateAll(fixture("read-only-deny.jsonl"));
  const messages = ended(events);
  assert.ok(messages.every((message) => message.role === "assistant"));
  assert.ok(messages.every((message) => message.content.every((block) => block.type !== "toolCall")));
  assert.match(messages.at(-1).content[0].text, /can't create/);
});

test("unknown events and junk are ignored, never thrown", () => {
  const translator = new adapter.ClaudeStreamTranslator();
  for (const event of [null, 42, "x", { type: "nope" }, { type: "stream_event", event: { type: "content_block_delta", index: 9, delta: { type: "text_delta", text: "x" } } }, { type: "user", message: { content: "plain" } }]) {
    assert.deepEqual(translator.push(event).events, []);
  }
});

test("a failed turn reports claude's own error", () => {
  const translator = new adapter.ClaudeStreamTranslator();
  const { turnEnd, events } = translator.push({ type: "result", subtype: "success", is_error: true, result: "Invalid API key · Please run /login", usage: {}, total_cost_usd: 0 });
  assert.equal(turnEnd.error, "Invalid API key · Please run /login");
  assert.deepEqual(events, [{ type: "agent_end", messages: [] }]);
});

test("flush keeps a partly streamed reply as a final message, then ends the run", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 5);
  translator.push({ type: "stream_event", event: { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } } });
  translator.push({ type: "stream_event", event: { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "half a rep" } } });
  assert.equal(translator.streamingMessage.content[0].text, "half a rep");
  const events = translator.flush();
  assert.deepEqual(events.map((event) => event.type), ["message_end", "agent_end"]);
  assert.equal(events[0].message.content[0].text, "half a rep");
  assert.equal(translator.streamingMessage, null);
});

test("stdin lines and the line decoder speak stream-json", () => {
  assert.deepEqual(JSON.parse(adapter.claudeUserInputLine("hi")), { type: "user", message: { role: "user", content: "hi" } });
  assert.deepEqual(JSON.parse(adapter.claudeInterruptLine("r1")), { type: "control_request", request_id: "r1", request: { subtype: "interrupt", spare_background_tasks: true } });
  const decoder = new adapter.ClaudeLineDecoder();
  assert.deepEqual(decoder.push('{"a":1}\n{"b"'), [{ a: 1 }]);
  assert.deepEqual(decoder.push(':2}\nnot json\n'), [{ b: 2 }]);
});

// Subagent activity, on the real background-Agent recording.
const AGENT_ID = "toolu_01F1YZd2Gseivs4hckhQVw5s";
const READ_ID = "toolu_012N7brUKegpGiA36sdi1ApH";
const subagentEvents = (events) => events.filter((event) => event.type === adapter.CLAUDE_SUBAGENT_EVENT);

test("a subagent's Read, result and text go to its Agent run, never to a main-conversation event", () => {
  const { events } = translateAll(fixture("subagent/stream.jsonl"));
  const main = events.filter((event) => event.type !== adapter.CLAUDE_SUBAGENT_EVENT);
  assert.ok(!JSON.stringify(main).includes(READ_ID), "the subagent's Read leaked into the main conversation");
  assert.deepEqual(
    ended(main).map((message) => message.role === "custom" ? message.customType : `${message.role}:${message.content[0].type}`),
    ["assistant:toolCall", "toolResult:text", "assistant:text", "claude:task-notification", "assistant:text"],
    "the main conversation keeps the Agent call, its launch receipt, its reply, the follow-up anchor and the follow-up reply",
  );
  const runs = subagentEvents(events);
  assert.ok(runs.every((event) => event.run.toolCallId === AGENT_ID), "every subagent event names the Agent call");
  const entries = runs.flatMap((event) => event.run.entries);
  assert.deepEqual(entries.map((entry) => [entry.id, entry.message.role]), [
    ["989c7aa3-823f-4e39-b826-7a4fa64b76cc", "assistant"],
    ["9e9ed764-c94f-4c81-9581-cf1ff958eb80", "toolResult"],
    ["95f1af84-d81a-4ab5-adfd-e41b2d9a338b", "assistant"],
  ], "each entry arrives once, keyed by claude's uuid, in claude's order");
  assert.equal(entries[0].message.content[0].id, READ_ID);
  assert.equal(entries[1].message.toolName, "Read", "the subagent's tool result is named after its call");
});

test("the async launch receipt keeps the run running; claude's task events set its final state and statistics", () => {
  const recorded = fixture("subagent/stream.jsonl");
  const translator = new adapter.ClaudeStreamTranslator(() => 1000);
  const runAfter = [];
  for (const event of recorded) {
    const last = subagentEvents(translator.push(event).events).at(-1);
    if (last) runAfter.push({ cause: event.subtype ?? event.type, run: last.run });
  }
  const receipt = runAfter.find((step) => step.cause === "user" && step.run.agentId && step.run.entries.length === 0);
  assert.equal(receipt.run.status, "running", "async_launched is no completion");
  assert.equal(receipt.run.background, true);
  assert.equal(receipt.run.agentType, "general-purpose");
  assert.equal(receipt.run.description, "Read note.txt first line");
  const progress = runAfter.find((step) => step.cause === "task_progress").run;
  assert.equal(progress.progress, "Reading note.txt");
  assert.deepEqual(progress.entries, [], "a progress update adds no timeline entry");
  const final = runAfter.find((step) => step.cause === "task_notification").run;
  assert.equal(final.status, "completed");
  assert.deepEqual(final.usage, { totalTokens: 10379, toolUses: 1, durationMs: 1906 });
  assert.equal(final.result, "SUBAGENT_PROBE_TOKEN_253");
});

test("claude's follow-up turn opens with a hidden anchor; the session is unsettled until it starts", () => {
  const recorded = fixture("subagent/stream.jsonl");
  const translator = new adapter.ClaudeStreamTranslator(() => 1000);
  const unsettled = [];
  let turnStarts = 0;
  for (const event of recorded) {
    const translated = translator.push(event);
    if (translated.claudeTurnStart) {
      turnStarts += 1;
      assert.equal(event.subtype, "init");
      assert.deepEqual(translated.events.map((out) => [out.type, out.message.customType, out.message.display]), [
        ["message_start", "claude:task-notification", false],
        ["message_end", "claude:task-notification", false],
      ]);
    }
    unsettled.push(translator.hasUnsettledSubagents());
  }
  assert.equal(turnStarts, 1, "only the init after the notification starts claude's own turn");
  assert.equal(unsettled[0], false, "before any Agent call nothing is unsettled");
  const followUp = recorded.findIndex((event, index) => index > 0 && event.subtype === "init");
  assert.ok(unsettled.slice(2, followUp).every(Boolean), "unsettled from the Agent call to the follow-up turn (task_updated included)");
  assert.ok(unsettled.slice(followUp).every((value) => !value), "settled once claude's follow-up turn started");
});

test("a nested Agent call starts its own run, and its entries go to it, not to the parent's", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 1000);
  const events = [
    { type: "assistant", message: { content: [{ type: "tool_use", id: "toolu_outer", name: "Agent", input: { description: "outer", prompt: "p", subagent_type: "general-purpose" } }] }, parent_tool_use_id: null, uuid: "m1" },
    { type: "assistant", message: { content: [{ type: "tool_use", id: "toolu_inner", name: "Task", input: { description: "inner", prompt: "q", subagent_type: "Explore" } }] }, parent_tool_use_id: "toolu_outer", uuid: "s1" },
    { type: "assistant", message: { content: [{ type: "text", text: "deep text" }] }, parent_tool_use_id: "toolu_inner", uuid: "d1" },
    { type: "stream_event", event: { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "partial" } }, parent_tool_use_id: "toolu_inner" },
    { type: "system", subtype: "task_something_new", task_id: "x" },
  ].flatMap((event) => translator.push(event).events);
  const runs = subagentEvents(events);
  assert.deepEqual(runs.map((event) => [event.run.toolCallId, event.run.entries.map((entry) => entry.id)]), [
    ["toolu_outer", []],
    ["toolu_outer", ["s1"]],
    ["toolu_inner", []],
    ["toolu_inner", ["d1"]],
  ]);
  assert.equal(runs[2].run.agentType, "Explore");
  assert.equal(runs[2].run.background, false, "a foreground call is no background run");
  assert.ok(!events.some((event) => event.type === "message_update"), "a subagent's partial stream is not shown in the main reply");
  assert.equal(translator.hasUnsettledSubagents(), false, "foreground runs hold nothing");
});

const SIDECAR_ID = "a9fb2ff3aa8e8ff40";
const sidecarsOf = () => ({
  [SIDECAR_ID]: {
    entries: fixture(`subagent/subagents/agent-${SIDECAR_ID}.jsonl`),
    meta: JSON.parse(readFileSync(new URL(`./__fixtures__/claude-2.1.312/subagent/subagents/agent-${SIDECAR_ID}.meta.json`, import.meta.url), "utf8")),
  },
});
const entryKeys = (entries) => entries.map((entry) => [entry.id, entry.message.role]);

test("history rebuilds the run from the sidecar, keeps its entries out of the main list and its prompt out of the timeline", () => {
  const history = adapter.claudeTranscriptToMessages(fixture("subagent/transcript.jsonl"), sidecarsOf());
  assert.equal(history.subagents.length, 1);
  const [run] = history.subagents;
  assert.equal(run.toolCallId, AGENT_ID);
  assert.equal(run.agentId, SIDECAR_ID, "linked by the receipt's toolUseResult.agentId");
  assert.equal(run.status, "completed");
  assert.equal(run.result, "SUBAGENT_PROBE_TOKEN_253", "the notification's <result>, not its summary line");
  assert.deepEqual(run.usage, { totalTokens: 10379, toolUses: 1, durationMs: 1906 });
  assert.match(run.prompt, /^Read the file note\.txt/);
  assert.ok(!run.entries.some((entry) => entry.message.role === "user"), "the sidecar's opening prompt is not repeated as an entry");
  assert.ok(!JSON.stringify(history.messages).includes(READ_ID), "the sidecar's entries are not main messages");
  assert.ok(JSON.stringify(history.messages).includes(AGENT_ID), "the Agent call stays in the main list");
});

test("live and history yield the same entries for the run", () => {
  const live = subagentEvents(translateAll(fixture("subagent/stream.jsonl")).events).flatMap((event) => event.run.entries);
  const [run] = adapter.claudeTranscriptToMessages(fixture("subagent/transcript.jsonl"), sidecarsOf()).subagents;
  assert.deepEqual(entryKeys(run.entries), entryKeys(live));
  assert.deepEqual(run.entries.map((entry) => entry.message.content), live.map((entry) => entry.message.content));
});

test("the task-notification turn is a hidden anchor, not a user bubble; the follow-up reply stays", () => {
  const { messages } = adapter.claudeTranscriptToMessages(fixture("subagent/transcript.jsonl"));
  const anchors = messages.filter((message) => message.role === "custom");
  assert.equal(anchors.length, 1);
  assert.equal(anchors[0].customType, "claude:task-notification");
  assert.equal(anchors[0].display, false);
  assert.ok(!messages.some((message) => message.role === "user" && JSON.stringify(message).includes("<task-notification>")));
  assert.match(messages.at(-1).content[0].text, /^The subagent finished and reported/);
});

test("a missing sidecar leaves the run without entries, and the meta's toolUseId links one without a receipt", () => {
  const [bare] = adapter.claudeTranscriptToMessages(fixture("subagent/transcript.jsonl")).subagents;
  assert.deepEqual(bare.entries, []);
  assert.equal(bare.status, "completed", "the notification still settles it");
  // A transcript cut before the receipt: only the meta names the call.
  const cut = fixture("subagent/transcript.jsonl").slice(0, 2);
  const [linked] = adapter.claudeTranscriptToMessages(cut, sidecarsOf()).subagents;
  assert.equal(linked.agentId, SIDECAR_ID);
  assert.equal(linked.entries.length, 3);
  assert.equal(linked.status, "completed", "the meta's finished flag is claude's own report");
});

test("nested sidecars are followed through the inner Agent call's receipt", () => {
  const main = [
    { type: "assistant", uuid: "m1", isSidechain: false, message: { content: [{ type: "tool_use", id: "toolu_outer", name: "Agent", input: { description: "outer", prompt: "go", subagent_type: "general-purpose" } }] } },
    { type: "user", uuid: "m2", isSidechain: false, message: { content: [{ type: "tool_result", tool_use_id: "toolu_outer", content: "outer done" }] }, toolUseResult: { status: "completed", agentId: "outer1", content: [{ type: "text", text: "outer done" }] } },
  ];
  const outer = [
    { type: "user", uuid: "o0", isSidechain: true, message: { content: "go" } },
    { type: "assistant", uuid: "o1", isSidechain: true, message: { content: [{ type: "tool_use", id: "toolu_inner", name: "Agent", input: { description: "inner", prompt: "deeper", subagent_type: "Explore" } }] } },
    { type: "user", uuid: "o2", isSidechain: true, message: { content: [{ type: "tool_result", tool_use_id: "toolu_inner", content: "inner done" }] }, toolUseResult: { status: "completed", agentId: "inner1" } },
  ];
  const inner = [
    { type: "user", uuid: "i0", isSidechain: true, message: { content: "deeper" } },
    { type: "assistant", uuid: "i1", isSidechain: true, message: { content: [{ type: "text", text: "deep text" }] } },
  ];
  const { subagents, messages } = adapter.claudeTranscriptToMessages(main, { outer1: { entries: outer }, inner1: { entries: inner } });
  assert.deepEqual(subagents.map((run) => [run.toolCallId, run.status, run.result, run.entries.map((entry) => entry.id)]), [
    ["toolu_outer", "completed", "outer done", ["o1", "o2"]],
    ["toolu_inner", "completed", "inner done", ["i1"]],
  ]);
  assert.ok(!JSON.stringify(messages).includes("deep text"));
});

test("the recorded control responses translate into answers, never chat messages; the builders reproduce the accepted requests", () => {
  const stdin = fixture("model-switch/stdin.jsonl");
  assert.equal(adapter.claudeSetModelLine(stdin[2].request_id, "claude-sonnet-5-5"), `${JSON.stringify(stdin[2])}\n`);
  const effortStdin = fixture("effort/stdin.jsonl");
  assert.equal(adapter.claudeSetEffortLine(effortStdin[1].request_id, "max"), `${JSON.stringify(effortStdin[1])}\n`);
  const translator = new adapter.ClaudeStreamTranslator(() => 1);
  const answers = [];
  const assistantModels = [];
  for (const event of fixture("model-switch/stream.jsonl")) {
    const translated = translator.push(event);
    if (translated.control) {
      answers.push(translated.control);
      assert.deepEqual(translated.events, []);
    }
    for (const out of translated.events) if (out.type === "message_end" && out.message.role === "assistant") assistantModels.push(out.message.model);
    for (const out of translated.events) assert.ok(!(out.type === "message_end" && out.message.role === "user"), "a /model bookkeeping event became a user message");
  }
  assert.deepEqual(answers.map((answer) => [answer.requestId, answer.subtype, answer.errorCode ?? null]), [["req_switch_bogus", "error", "check_failed"], ["req_switch_sonnet", "success", null]]);
  assert.match(answers[0].error, /model not changed/);
  assert.deepEqual(answers[1].response, { model: "claude-sonnet-5-5" });
  assert.deepEqual(assistantModels, ["claude-opus-5-5", "claude-sonnet-5-5"]);
  const effortAnswer = fixture("effort/stream.jsonl").map((event) => adapter.claudeControlResponse(event)).find(Boolean);
  assert.deepEqual(effortAnswer, { requestId: "req_effort_max", subtype: "success" });
});

test("the model-switch transcript shows the user's words and both replies, not claude's /model bookkeeping", () => {
  const { messages } = adapter.claudeTranscriptToMessages(fixture("model-switch/transcript.jsonl"));
  const users = messages.filter((message) => message.role === "user").map((message) => message.content);
  assert.deepEqual(users, ["Reply with exactly the word: one", "Reply with exactly the word: two"]);
  assert.deepEqual(messages.filter((message) => message.role === "assistant").map((message) => message.model), ["claude-opus-5-5", "claude-sonnet-5-5"]);
});

test("the compact recording: the bare /compact line, compact_boundary's token figure, the transcript marker without a summary", () => {
  assert.equal(adapter.claudeCompactLine(), `${JSON.stringify(fixture("compact/stdin.jsonl").at(-1))}\n`);
  const translator = new adapter.ClaudeStreamTranslator(() => 1);
  const boundaries = fixture("compact/stream.jsonl").map((event) => translator.push(event).compactBoundary).filter(Boolean);
  assert.deepEqual(boundaries, [{ tokensBefore: 16739 }]);
  const { messages, entryIds } = adapter.claudeTranscriptToMessages(fixture("compact/transcript.jsonl"));
  const markers = messages.filter((message) => message.role === "custom" && message.customType === "compaction");
  assert.equal(markers.length, 1);
  assert.deepEqual(markers[0].details, { tokensBefore: 16739 });
  assert.equal(markers[0].content, "", "no summary body");
  assert.equal(markers[0].display, true);
  assert.equal(entryIds[messages.indexOf(markers[0])], fixture("compact/transcript.jsonl").find((entry) => entry.subtype === "compact_boundary").uuid);
  assert.deepEqual(messages.filter((message) => message.role === "user").map((message) => message.content), [
    "Remember the colour teal. Reply with exactly the word: noted",
    "Which colour did I ask you to remember? Reply with one word.",
  ], "every pre-compaction message stays; no summary or /compact bookkeeping bubble");
  assert.equal(messages.filter((message) => message.role === "assistant").length, 2);
  assert.ok(messages.indexOf(markers[0]) > messages.findLastIndex((message) => message.role === "assistant"), "the marker sits where claude compacted");
});

// the context the header's meter shows, counted from claude's own usage reports.
const usageOf = (input, cacheWrite, cacheRead, output) => ({
  input_tokens: input,
  cache_creation_input_tokens: cacheWrite,
  cache_read_input_tokens: cacheRead,
  output_tokens: output,
});

test("a response's context is its input, cache write, cache read and output; a usage that counts nothing is unknown", () => {
  assert.equal(adapter.claudeContextTokens(usageOf(2, 3283, 13449, 5)), 16739);
  assert.equal(adapter.claudeContextTokens({ input_tokens: 7 }), 7, "a missing count is zero");
  assert.equal(adapter.claudeContextTokens(usageOf(0, 0, 0, 0)), null, "claude's synthetic replies carry all-zero usage");
  assert.equal(adapter.claudeContextTokens({}), null);
  assert.equal(adapter.claudeContextTokens(undefined), null);
  assert.equal(adapter.claudeContextTokens("16739"), null);
});

test("the live count is the latest main response's, its final output included; a compaction leaves what claude reported (compact recording)", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 1);
  assert.equal(translator.contextTokens, undefined, "nothing reported yet");
  const counts = [];
  let preTokens;
  for (const event of fixture("compact/stream.jsonl")) {
    const before = translator.contextTokens;
    const { compactBoundary } = translator.push(event);
    if (compactBoundary) preTokens = { reported: compactBoundary.tokensBefore, counted: before };
    if (translator.contextTokens !== counts.at(-1)) counts.push(translator.contextTokens);
  }
  // message_start / assistant carry the streamed output (1, then 4); message_delta the final one (4, then 5).
  assert.deepEqual(counts, [16685, 16688, 16738, 16739, 2978]);
  assert.deepEqual(preTokens, { reported: 16739, counted: 16739 }, "the count before /compact is the one claude compacts from");
});

test("a subagent's responses never move the main count; a zero-usage reply leaves it; a compaction without a size makes it unknown", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 1);
  let subagentReports = 0;
  for (const event of fixture("subagent/stream.jsonl")) {
    const before = translator.contextTokens;
    translator.push(event);
    if (typeof event.parent_tool_use_id === "string" && event.parent_tool_use_id) {
      if (event.type === "assistant" && adapter.claudeContextTokens(event.message.usage) !== null) subagentReports += 1;
      assert.equal(translator.contextTokens, before, `a subagent event moved the main count (${event.type})`);
    }
  }
  assert.ok(subagentReports > 0, "the recording's subagent replies report usage of their own");
  assert.equal(translator.contextTokens, 18294);

  translator.push({
    type: "assistant",
    message: { id: "msg_synthetic", type: "message", role: "assistant", model: "<synthetic>", content: [{ type: "text", text: "No response requested." }], usage: usageOf(0, 0, 0, 0) },
    parent_tool_use_id: null,
    session_id: "s",
  });
  assert.equal(translator.contextTokens, 18294, "a reply that counts nothing changes nothing");

  translator.push({ type: "system", subtype: "compact_boundary", session_id: "s", compact_metadata: { trigger: "manual", pre_tokens: 18294 } });
  assert.equal(translator.contextTokens, null, "a compaction claude gave no size for is unknown, not the old count");

  translator.push({ type: "stream_event", event: { type: "message_start", message: { id: "msg_after", model: "claude-haiku-5-5", usage: usageOf(3, 40, 2900, 1) } }, parent_tool_use_id: null, session_id: "s" });
  assert.equal(translator.contextTokens, 2944);
  translator.push({ type: "stream_event", event: { type: "message_delta", delta: { stop_reason: "end_turn" }, usage: usageOf(3, 40, 2900, 61) }, parent_tool_use_id: null, session_id: "s" });
  assert.equal(translator.contextTokens, 3004, "message_delta carries the message's final output");
});

test("the transcript's count is the live one: the last main reply, or what a later compaction left", () => {
  const live = (name) => {
    const translator = new adapter.ClaudeStreamTranslator(() => 1);
    for (const event of fixture(name)) translator.push(event);
    return translator.contextTokens;
  };
  assert.equal(adapter.claudeTranscriptContextTokens(fixture("transcript.jsonl")), 1151);
  assert.equal(live("stream.jsonl"), 1151);
  assert.equal(adapter.claudeTranscriptContextTokens(fixture("model-switch/transcript.jsonl")), 16355);
  assert.equal(live("model-switch/stream.jsonl"), 16355);
  assert.equal(adapter.claudeTranscriptContextTokens(fixture("compact/transcript.jsonl")), 2978);
  assert.equal(live("compact/stream.jsonl"), 2978);

  assert.equal(adapter.claudeTranscriptContextTokens([]), undefined, "no reply and no compaction: nothing to read");
  const reply = (usage, extra = {}) => ({ type: "assistant", isSidechain: false, message: { id: `msg_${JSON.stringify(usage)}`, role: "assistant", content: [], usage }, ...extra });
  const boundary = (compactMetadata) => ({ type: "system", subtype: "compact_boundary", compactMetadata });
  assert.equal(adapter.claudeTranscriptContextTokens([reply(usageOf(4, 100, 0, 20)), reply(usageOf(900, 0, 0, 90), { isSidechain: true })]), 124, "a sidechain entry is another context");
  assert.equal(adapter.claudeTranscriptContextTokens([reply(usageOf(4, 100, 0, 20)), reply(usageOf(0, 0, 0, 0))]), 124, "an all-zero reply changes nothing");
  assert.equal(adapter.claudeTranscriptContextTokens([reply(usageOf(4, 100, 0, 20)), boundary({ trigger: "manual", preTokens: 124 })]), null, "a compaction without postTokens is unknown");
  assert.equal(adapter.claudeTranscriptContextTokens([reply(usageOf(4, 100, 0, 20)), boundary({ preTokens: 124, postTokens: 30 }), reply(usageOf(2, 10, 30, 8))]), 50, "a reply after the compaction counts again");
});

// the window the meter divides by is the one claude reports for the model.
test("a result's modelUsage reports claude's own window per model; a missing or non-positive one is left out", () => {
  const results = (name) => fixture(name).filter((event) => event.type === "result");
  for (const result of results("compact/stream.jsonl")) {
    assert.deepEqual([...adapter.claudeReportedContextWindows(result)], [["claude-haiku-5-5", 1_000_000]]);
  }
  // The model-switch recording's second result reports both models the child used.
  assert.deepEqual([...adapter.claudeReportedContextWindows(results("model-switch/stream.jsonl").at(-1))], [
    ["claude-opus-5-5", 1_000_000],
    ["claude-sonnet-5-5", 1_000_000],
  ]);
  const entry = (contextWindow) => ({ inputTokens: 2, outputTokens: 3, contextWindow });
  assert.deepEqual([...adapter.claudeReportedContextWindows({ type: "result", modelUsage: { a: entry(200_000), b: entry(0), c: entry(-5), d: entry("200000"), e: entry(Number.NaN), f: {}, g: null } })], [["a", 200_000]]);
  assert.deepEqual([...adapter.claudeReportedContextWindows({ type: "result" })], [], "a result without modelUsage reports no window");
  assert.deepEqual([...adapter.claudeReportedContextWindows(undefined)], []);
});

test("the translator keeps the window each result reported, per model, and nothing before the first result", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 1);
  const events = fixture("model-switch/stream.jsonl");
  const firstResult = events.findIndex((event) => event.type === "result");
  for (const event of events.slice(0, firstResult)) translator.push(event);
  assert.equal(translator.contextWindow("claude-opus-5-5"), undefined, "no result yet: no reported window");
  translator.push(events[firstResult]);
  assert.equal(translator.contextWindow("claude-opus-5-5"), 1_000_000);
  assert.equal(translator.contextWindow("claude-sonnet-5-5"), undefined, "a model the child has not used yet has no reported window");
  for (const event of events.slice(firstResult + 1)) translator.push(event);
  assert.equal(translator.contextWindow("claude-sonnet-5-5"), 1_000_000);

  // An account without the long context: claude reports a smaller window, and a later result replaces it.
  const result = (contextWindow) => ({ type: "result", subtype: "success", is_error: false, session_id: "s", usage: {}, modelUsage: { "claude-sonnet-5-5": { contextWindow } } });
  translator.push(result(200_000));
  assert.equal(translator.contextWindow("claude-sonnet-5-5"), 200_000);
  assert.equal(translator.contextWindow("claude-opus-5-5"), 1_000_000, "another model's window is kept");
  translator.push({ type: "result", subtype: "success", is_error: false, session_id: "s", usage: {} });
  assert.equal(translator.contextWindow("claude-sonnet-5-5"), 200_000, "a result without modelUsage keeps the last window");
  // A subagent's activity is routed by parent_tool_use_id and never reaches the main windows.
  translator.push({ ...result(64_000), parent_tool_use_id: "toolu_sub" });
  assert.equal(translator.contextWindow("claude-sonnet-5-5"), 200_000);
});

// The real claude 2.1.322 Edit / replace_all Edit / Write-update turn (lib/__fixtures__/claude-2.1.322/README.md, edit/).
const { parseUnifiedPatch } = await jiti.import("./patch.ts");
const edit322 = (name) => adapter.parseClaudeTranscript(readFileSync(new URL(`./__fixtures__/claude-2.1.322/edit/${name}`, import.meta.url), "utf8"));
const EDIT_RUN = "/tmp/claude-rec-444-edit";

/** A tool result's diff as file, hunk headers and the removed / added cells with their line numbers. */
function recordedDiff(message) {
  assert.equal(typeof message.details?.diff, "string", `${message.toolName}: no details.diff`);
  const files = parseUnifiedPatch(message.details.diff);
  assert.equal(files?.length, 1, `${message.toolName}: the diff is not one file`);
  const [file] = files;
  const cells = (side, type) => file.rows.flatMap((row) => (row.type === "line" && row[side].type === type ? [[row[side].lineNo, row[side].text]] : []));
  return {
    path: file.newPath,
    oldPath: file.oldPath,
    hunks: file.rows.filter((row) => row.type === "hunk").map((row) => row.text),
    removed: cells("left", "removed"),
    added: cells("right", "added"),
  };
}

const EDIT_DIFFS = [
  { toolName: "Edit", path: `${EDIT_RUN}/notes.txt`, hunks: ["@@ -5,7 +5,7 @@"], removed: [[8, "the middle line"]], added: [[8, "the MIDDLE line"]] },
  {
    toolName: "Edit",
    path: `${EDIT_RUN}/notes.txt`,
    hunks: ["@@ -1,4 +1,4 @@", "@@ -12,4 +12,4 @@"],
    removed: [[1, "apple at the top"], [15, "apple at the bottom"]],
    added: [[1, "pear at the top"], [15, "pear at the bottom"]],
  },
  { toolName: "Write", path: `${EDIT_RUN}/other.txt`, hunks: ["@@ -1,2 +1,2 @@"], removed: [[1, "old other text"]], added: [[1, "new other text"]] },
];

test("a recorded Edit, a replace_all Edit and a Write update carry claude's structuredPatch as the tool result's diff, live and from the transcript", () => {
  const live = ended(translateAll(edit322("stream.jsonl")).events).filter((message) => message.role === "toolResult" && message.toolName !== "Read");
  assert.deepEqual(live.map((message) => message.toolName), ["Edit", "Edit", "Write"]);
  live.forEach((message, index) => {
    const { toolName, ...expected } = EDIT_DIFFS[index];
    assert.equal(message.toolName, toolName);
    assert.deepEqual(recordedDiff(message), { ...expected, oldPath: expected.path }, `live ${toolName} #${index + 1}`);
  });
  const reads = ended(translateAll(edit322("stream.jsonl")).events).filter((message) => message.role === "toolResult" && message.toolName === "Read");
  assert.ok(reads.length > 0 && reads.every((message) => message.details === undefined), "a result without a structuredPatch gets no details");

  const { messages } = adapter.claudeTranscriptToMessages(edit322("transcript.jsonl"));
  const history = messages.filter((message) => message.role === "toolResult" && message.toolName !== "Read");
  assert.deepEqual(history.map((message) => message.toolName), ["Edit", "Edit", "Write"]);
  assert.deepEqual(history.map((message) => message.details), live.map((message) => message.details), "the transcript yields the same diff");
});

test("claude's api_retry is pi's retry banner until the retried request streams, and its auto-compaction is pi's auto_compaction start and end", () => {
  const live = (types) => (translated) => translated.events.filter((event) => types.includes(event.type));
  const retryEvents = live(["auto_retry_start", "auto_retry_end"]);
  const compactionEvents = live(["auto_compaction_start", "auto_compaction_end"]);
  // claude 2.1.322's frame for a rate-limited request it retries (the bundle's schema shape).
  const apiRetry = (attempt, extra = {}) => ({ type: "system", subtype: "api_retry", attempt, max_retries: 10, retry_delay_ms: 1500, error_status: 429, error: "rate_limit", uuid: `retry-${attempt}`, session_id: "s", ...extra });
  const messageStart = { type: "stream_event", event: { type: "message_start", message: { id: "msg_1", model: "claude-haiku-5-5", usage: {} } }, session_id: "s", parent_tool_use_id: null };
  const result = (extra = {}) => ({ type: "result", subtype: "success", is_error: false, session_id: "s", usage: {}, ...extra });

  // The retry's wait is the banner; claude's next retry updates it; the retried request's stream ends it.
  let translator = new adapter.ClaudeStreamTranslator(() => 1000);
  assert.deepEqual(retryEvents(translator.push(apiRetry(1))), [{ type: "auto_retry_start", attempt: 1, maxAttempts: 10, delayMs: 1500, errorMessage: "rate_limit (429)" }]);
  assert.deepEqual(retryEvents(translator.push(apiRetry(2, { error_status: null, error: "server_error" }))), [{ type: "auto_retry_start", attempt: 2, maxAttempts: 10, delayMs: 1500, errorMessage: "server_error" }]);
  assert.deepEqual(retryEvents(translator.push({ ...messageStart, parent_tool_use_id: "toolu_sub" })), [], "a subagent's stream is not the retried request");
  assert.deepEqual(retryEvents(translator.push(messageStart)), [{ type: "auto_retry_end", success: true, attempt: 2 }]);
  assert.deepEqual(retryEvents(translator.push(messageStart)), [], "the banner ends once");
  assert.deepEqual(retryEvents(translator.push(result())), [], "no retry was open at the turn's end");

  // A turn that ends while claude still waits to retry (it gave up, or was stopped): the banner ends unsuccessful, before agent_end.
  translator = new adapter.ClaudeStreamTranslator(() => 1000);
  translator.push(apiRetry(1));
  const gaveUp = translator.push(result({ subtype: "error_during_execution", is_error: true }));
  assert.deepEqual(retryEvents(gaveUp), [{ type: "auto_retry_end", success: false, attempt: 1 }]);
  assert.ok(gaveUp.events.findIndex((event) => event.type === "auto_retry_end") < gaveUp.events.findIndex((event) => event.type === "agent_end"), "the banner ends before the run");

  // The recorded compaction (compact/stream.jsonl lines 22–23 and its compact_boundary): start once, end at the boundary.
  const recorded = fixture("compact/stream.jsonl");
  const [compacting, compacted] = [recorded[21], recorded[22]];
  assert.equal(compacting.status, "compacting");
  assert.equal(compacted.compact_result, "success");
  const boundary = recorded.find((event) => event.type === "system" && event.subtype === "compact_boundary");
  translator = new adapter.ClaudeStreamTranslator(() => 1000);
  assert.deepEqual(compactionEvents(translator.push(compacting)), [{ type: "auto_compaction_start", reason: "threshold" }]);
  assert.deepEqual(compactionEvents(translator.push(compacting)), [], "claude's 30 s re-sends of compacting start nothing new");
  assert.deepEqual(compactionEvents(translator.push(compacted)), [], "a successful compaction ends at its boundary");
  const ended = translator.push(boundary);
  assert.deepEqual(compactionEvents(ended), [{ type: "auto_compaction_end", reason: "threshold", result: { tokensBefore: 16739 }, aborted: false, willRetry: false }]);
  assert.deepEqual(ended.compactBoundary, { tokensBefore: 16739 }, "the boundary still reports to a manual /compact");
  assert.deepEqual(compactionEvents(translator.push(boundary)), [], "a boundary with no compaction open ends nothing");
  assert.deepEqual(compactionEvents(translator.push(result())), []);

  // A failed compaction ends with claude's error; one still open at the turn's end is aborted.
  translator = new adapter.ClaudeStreamTranslator(() => 1000);
  translator.push(compacting);
  assert.deepEqual(compactionEvents(translator.push({ ...compacted, compact_result: "failed", compact_error: "Not enough messages to compact." })), [
    { type: "auto_compaction_end", reason: "threshold", aborted: false, willRetry: false, errorMessage: "Not enough messages to compact." },
  ]);
  assert.deepEqual(compactionEvents(translator.push(boundary)), [], "the failed compaction is already over");
  translator.push(compacting);
  const stopped = translator.push(result({ subtype: "error_during_execution", is_error: true }));
  assert.deepEqual(compactionEvents(stopped), [{ type: "auto_compaction_end", reason: "threshold", aborted: true, willRetry: false }]);
  assert.ok(stopped.events.findIndex((event) => event.type === "auto_compaction_end") < stopped.events.findIndex((event) => event.type === "agent_end"));

  // claude's quota report is no waiting state: nothing (see docs/agents/claude-sessions.md).
  translator = new adapter.ClaudeStreamTranslator(() => 1000);
  assert.deepEqual(translator.push({ type: "rate_limit_event", rate_limit_info: { status: "allowed_warning", resetsAt: 1 }, session_id: "s" }).events, []);
});

// claude 2.1.322's permission frames, synthesized from the schema the plan's
// Protocol facts quote: `can_use_tool`, its withdrawal and claude's own denial.
const CAN_USE_TOOL = {
  type: "control_request",
  request_id: "perm-1",
  request: {
    subtype: "can_use_tool",
    tool_name: "Bash",
    display_name: "Bash",
    input: { command: "ls -la", description: "List files" },
    tool_use_id: "toolu_perm_1",
    permission_suggestions: [{ type: "addRules", rules: [{ toolName: "Bash", ruleContent: "ls:*" }], behavior: "allow", destination: "localSettings" }],
    decision_reason: "No rule allows this command",
  },
};

test("can_use_tool and control_cancel_request reach the session as permission frames, never chat events", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 1000);
  translator.push({ type: "system", subtype: "init", session_id: "s-perm", model: "claude-haiku-5-5" });
  const asked = translator.push(CAN_USE_TOOL);
  assert.deepEqual(asked.events, []);
  assert.deepEqual(asked.permissionRequest, { requestId: "perm-1", request: CAN_USE_TOOL.request });
  assert.equal(asked.permissionCancel, undefined);
  assert.equal(asked.control, undefined);

  const withdrawn = translator.push({ type: "control_cancel_request", request_id: "perm-1" });
  assert.deepEqual(withdrawn.events, []);
  assert.equal(withdrawn.permissionCancel, "perm-1");
  assert.equal(withdrawn.permissionRequest, undefined);

  // Only can_use_tool is a permission request; another control request or a malformed one is ignored.
  for (const frame of [
    { type: "control_request", request_id: "x-1", request: { subtype: "hook_callback", callback_id: "c" } },
    { type: "control_request", request_id: "x-2", request: { subtype: "can_use_tool", input: {} } },
    { type: "control_request", request: CAN_USE_TOOL.request },
    { type: "control_cancel_request" },
  ]) {
    const translated = translator.push(frame);
    assert.deepEqual(translated.events, [], JSON.stringify(frame));
    assert.equal(translated.permissionRequest, undefined, JSON.stringify(frame));
    assert.equal(translated.permissionCancel, undefined, JSON.stringify(frame));
  }
  assert.equal(translator.streamingMessage, null);
});

test("system/permission_denied is reported with its tool, call id and reason", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => 1000);
  const denied = translator.push({
    type: "system",
    subtype: "permission_denied",
    session_id: "s-perm",
    tool_name: "Bash",
    tool_use_id: "toolu_denied_1",
    decision_reason_type: "mode",
    decision_reason_code: "prompts_none",
  });
  assert.deepEqual(denied.events, []);
  assert.deepEqual(denied.permissionDenied, { toolName: "Bash", toolUseId: "toolu_denied_1", reason: "mode/prompts_none" });

  const typeOnly = translator.push({ type: "system", subtype: "permission_denied", tool_name: "Write", tool_use_id: "toolu_denied_2", decision_reason_type: "rule" });
  assert.deepEqual(typeOnly.permissionDenied, { toolName: "Write", toolUseId: "toolu_denied_2", reason: "rule" });
  const bare = translator.push({ type: "system", subtype: "permission_denied", tool_name: "Edit", tool_use_id: "toolu_denied_3" });
  assert.deepEqual(bare.permissionDenied, { toolName: "Edit", toolUseId: "toolu_denied_3" });
  assert.equal(translator.push({ type: "system", subtype: "permission_denied", tool_use_id: "toolu_x" }).permissionDenied, undefined);
});

test("claudeControlSuccessLine writes claude's control_response envelope with the decision", () => {
  const allow = { behavior: "allow", updatedInput: { command: "ls -la" }, toolUseID: "toolu_perm_1", decisionClassification: "user_temporary" };
  assert.equal(
    adapter.claudeControlSuccessLine("perm-1", allow),
    '{"type":"control_response","response":{"subtype":"success","request_id":"perm-1","response":{"behavior":"allow","updatedInput":{"command":"ls -la"},"toolUseID":"toolu_perm_1","decisionClassification":"user_temporary"}}}\n',
  );
  const deny = { behavior: "deny", message: "The user denied this in Pi Web", toolUseID: "toolu_perm_1", decisionClassification: "user_reject" };
  assert.equal(
    adapter.claudeControlSuccessLine("perm-2", deny),
    '{"type":"control_response","response":{"subtype":"success","request_id":"perm-2","response":{"behavior":"deny","message":"The user denied this in Pi Web","toolUseID":"toolu_perm_1","decisionClassification":"user_reject"}}}\n',
  );
  // The line is one stream-json input line claude's own answer parser reads back.
  const line = adapter.claudeControlSuccessLine("perm-3", { behavior: "allow" });
  assert.equal(line.split("\n").length, 2);
  assert.deepEqual(JSON.parse(line), { type: "control_response", response: { subtype: "success", request_id: "perm-3", response: { behavior: "allow" } } });
});
