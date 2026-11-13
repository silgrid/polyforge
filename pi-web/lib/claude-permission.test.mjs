import "#test-env";
// claude's permission prompts as Pi Web dialogs: the broker alone, with
// injected emit / write / clock / timers — no process. claude's frames are synthesized
// from the can_use_tool schema in docs/agents/claude-sessions.md (claude 2.1.322).
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { ClaudePermissionBroker, CLAUDE_PERMISSION_TIMEOUT_MS, claudePermissionDeniedNotice } = await jiti.import("./claude-permission.ts");

const START = 1_000_000;

function harness({ language = "en", timeoutMs } = {}) {
  let clock = START;
  let timerSeq = 0;
  const timers = new Map();
  const events = [];
  const lines = [];
  const opened = [];
  const broker = new ClaudePermissionBroker({
    emit: (event) => events.push(event),
    write: (line) => lines.push(line),
    language,
    now: () => clock,
    ...(timeoutMs === undefined ? {} : { timeoutMs }),
    timers: {
      set: (callback, ms) => {
        timerSeq += 1;
        timers.set(timerSeq, { callback, at: clock + ms });
        return timerSeq;
      },
      clear: (handle) => timers.delete(handle),
    },
    onOpen: (requestId, request) => opened.push({ requestId, tool: request.tool_name }),
  });
  const advance = (ms) => {
    clock += ms;
    for (const [handle, timer] of [...timers]) {
      if (timer.at <= clock && timers.has(handle)) {
        timers.delete(handle);
        timer.callback();
      }
    }
  };
  const dialogs = () => events.filter((event) => event.type === "extension_ui_request");
  const lastDialog = () => dialogs().at(-1);
  const closedIds = () => events.filter((event) => event.type === "extension_ui_closed").map((event) => event.id);
  const answers = () => lines.map((line) => {
    assert.ok(line.endsWith("\n"), "one stdin line per answer");
    const frame = JSON.parse(line);
    assert.equal(frame.type, "control_response");
    assert.equal(frame.response.subtype, "success");
    return { requestId: frame.response.request_id, decision: frame.response.response };
  });
  const reply = (id, value) => broker.respond({ type: "extension_ui_response", id, value });
  const esc = (id) => broker.respond({ type: "extension_ui_response", id, cancelled: true });
  return { broker, events, lines, opened, advance, dialogs, lastDialog, closedIds, answers, reply, esc };
}

const bash = (command, extra = {}) => ({
  subtype: "can_use_tool",
  tool_name: "Bash",
  display_name: "Bash",
  input: { command, description: "List files" },
  tool_use_id: "toolu_1",
  ...extra,
});

const SUGGESTIONS = [
  { type: "addRules", rules: [{ toolName: "Bash", ruleContent: "ls:*" }], behavior: "allow", destination: "localSettings" },
  { type: "addDirectories", directories: ["/tmp/x"], destination: "projectSettings" },
];

test("a can_use_tool request becomes one select naming the tool and the input summary; allow once writes allow with the original input, toolUseID and user_temporary", () => {
  const h = harness();
  h.broker.open("req-1", bash("ls -la"));

  assert.equal(h.dialogs().length, 1);
  const dialog = h.lastDialog();
  assert.equal(dialog.method, "select");
  assert.equal(typeof dialog.id, "string");
  assert.ok(dialog.id.length > 0);
  assert.match(dialog.title, /Bash/);
  assert.match(dialog.title, /ls -la/);
  assert.deepEqual(dialog.options, ["Allow once", "Deny"]);
  assert.equal(dialog.timeout, CLAUDE_PERMISSION_TIMEOUT_MS);
  assert.equal(dialog.expiresAt, START + CLAUDE_PERMISSION_TIMEOUT_MS);
  assert.equal(h.broker.size, 1);
  assert.deepEqual(h.opened, [{ requestId: "req-1", tool: "Bash" }]);
  assert.deepEqual(h.lines, [], "nothing is written before the user answers");

  assert.equal(h.reply(dialog.id, "Allow once"), true);
  assert.deepEqual(h.answers(), [{
    requestId: "req-1",
    decision: {
      behavior: "allow",
      updatedInput: { command: "ls -la", description: "List files" },
      toolUseID: "toolu_1",
      decisionClassification: "user_temporary",
    },
  }]);
  assert.deepEqual(h.closedIds(), [dialog.id]);
  assert.equal(h.broker.size, 0);
  assert.deepEqual(h.broker.pending(), []);
  assert.equal(h.reply(dialog.id, "Allow once"), false, "a second answer is not the broker's");
  assert.equal(h.lines.length, 1);

  // A zh-CN session's dialog is in Chinese; a file tool is summarized by its path.
  const zh = harness({ language: "zh-CN" });
  zh.broker.open("req-2", { subtype: "can_use_tool", tool_name: "Edit", input: { file_path: "/repo/a.ts", old_string: "x", new_string: "y" }, tool_use_id: "toolu_2" });
  assert.match(zh.lastDialog().title, /claude 请求使用 Edit/);
  assert.match(zh.lastDialog().title, /\/repo\/a\.ts/);
  assert.deepEqual(zh.lastDialog().options, ["允许一次", "拒绝"]);
});

test("always-allow is offered only with suggestions and without suppress_always_allow_rule, and sends them back with destination session", () => {
  const h = harness();
  h.broker.open("req-1", bash("ls", { permission_suggestions: SUGGESTIONS }));
  assert.equal(h.dialogs().length, 1);
  const dialog = h.lastDialog();
  assert.deepEqual(dialog.options, ["Allow once", "Always allow in this session", "Deny"]);

  assert.equal(h.reply(dialog.id, "Always allow in this session"), true);
  assert.deepEqual(h.answers(), [{
    requestId: "req-1",
    decision: {
      behavior: "allow",
      updatedInput: { command: "ls", description: "List files" },
      updatedPermissions: [
        { type: "addRules", rules: [{ toolName: "Bash", ruleContent: "ls:*" }], behavior: "allow", destination: "session" },
        { type: "addDirectories", directories: ["/tmp/x"], destination: "session" },
      ],
      toolUseID: "toolu_1",
      decisionClassification: "user_permanent",
    },
  }]);
  assert.equal(SUGGESTIONS[0].destination, "localSettings", "claude's own suggestion objects are left as they came");

  const suppressed = harness();
  suppressed.broker.open("req-2", bash("ls", { permission_suggestions: SUGGESTIONS, suppress_always_allow_rule: true }));
  assert.deepEqual(suppressed.lastDialog().options, ["Allow once", "Deny"]);

  const none = harness();
  none.broker.open("req-3", bash("ls", { permission_suggestions: [] }));
  assert.deepEqual(none.lastDialog().options, ["Allow once", "Deny"]);

  const zh = harness({ language: "zh-CN" });
  zh.broker.open("req-4", bash("ls", { permission_suggestions: SUGGESTIONS }));
  assert.deepEqual(zh.lastDialog().options, ["允许一次", "本会话内总是允许", "拒绝"]);
});

test("deny and Esc write behavior deny with a message and user_reject", () => {
  const h = harness();
  h.broker.open("req-1", bash("rm x"));
  assert.equal(h.dialogs().length, 1);
  const first = h.lastDialog();
  assert.equal(h.reply(first.id, "Deny"), true);

  h.broker.open("req-2", bash("rm y", { tool_use_id: "toolu_2" }));
  const second = h.lastDialog();
  assert.equal(h.esc(second.id), true);

  const answers = h.answers();
  assert.equal(answers.length, 2);
  for (const [index, { requestId, decision }] of answers.entries()) {
    assert.equal(requestId, `req-${index + 1}`);
    assert.equal(decision.behavior, "deny");
    assert.equal(typeof decision.message, "string");
    assert.ok(decision.message.length > 0);
    assert.equal(decision.toolUseID, `toolu_${index + 1}`);
    assert.equal(decision.decisionClassification, "user_reject");
    assert.equal("updatedInput" in decision, false);
  }
  assert.deepEqual(h.closedIds(), [first.id, second.id]);
  assert.equal(h.broker.size, 0);
});

test("AskUserQuestion asks question by question (single select, Other via input, multi-select as comma input) and answers allow with updatedInput.answers keyed by question text", () => {
  const input = {
    questions: [
      { question: "Which color?", header: "Color", multiSelect: false, options: [{ label: "Red", description: "warm" }, { label: "Blue", description: "cool" }] },
      { question: "Which size?", header: "Size", multiSelect: false, options: [{ label: "Small", description: "s" }, { label: "Large", description: "l" }] },
      { question: "Which toppings?", header: "Toppings", multiSelect: true, options: [{ label: "Cheese", description: "c" }, { label: "Ham", description: "h" }, { label: "Olive", description: "o" }] },
    ],
  };
  const h = harness();
  h.broker.open("req-ask", { subtype: "can_use_tool", tool_name: "AskUserQuestion", input, tool_use_id: "toolu_ask" });

  // Question 1: one select, claude's options plus an Other.
  assert.equal(h.dialogs().length, 1);
  const q1 = h.lastDialog();
  assert.equal(q1.method, "select");
  assert.match(q1.title, /Color/);
  assert.match(q1.title, /Which color\?/);
  assert.equal(q1.options.length, 3);
  assert.ok(q1.options[0].startsWith("Red"));
  assert.ok(q1.options[1].startsWith("Blue"));
  assert.match(q1.options[1], /cool/);
  assert.match(q1.options[2], /Other/);
  assert.equal(h.reply(q1.id, q1.options[1]), true);
  assert.deepEqual(h.lines, [], "nothing is written before the last answer");

  // Question 2: Other opens an input for the user's own words.
  const q2 = h.lastDialog();
  assert.notEqual(q2.id, q1.id);
  assert.equal(q2.method, "select");
  assert.match(q2.title, /Which size\?/);
  assert.equal(h.reply(q2.id, q2.options.at(-1)), true);
  const other = h.lastDialog();
  assert.notEqual(other.id, q2.id);
  assert.equal(other.method, "input");
  assert.match(other.title, /Which size\?/);
  assert.equal(h.reply(other.id, "Medium"), true);

  // Question 3: multi-select is an input listing the options, answered comma-separated.
  const q3 = h.lastDialog();
  assert.equal(q3.method, "input");
  assert.match(q3.title, /Which toppings\?/);
  for (const label of ["Cheese", "Ham", "Olive"]) assert.match(q3.title, new RegExp(label));
  assert.equal(h.broker.size, 1, "one claude request however many dialogs it takes");
  assert.equal(h.reply(q3.id, "Cheese, Olive"), true);

  const answers = h.answers();
  assert.equal(answers.length, 1);
  assert.equal(answers[0].requestId, "req-ask");
  assert.equal(answers[0].decision.behavior, "allow");
  assert.equal(answers[0].decision.toolUseID, "toolu_ask");
  assert.deepEqual(answers[0].decision.updatedInput, {
    ...input,
    answers: { "Which color?": "Blue", "Which size?": "Medium", "Which toppings?": "Cheese, Olive" },
  });
  assert.deepEqual(h.closedIds(), [q1.id, q2.id, other.id, q3.id]);
  assert.equal(h.broker.size, 0);

  // Esc on any question denies the whole request.
  h.broker.open("req-ask-2", { subtype: "can_use_tool", tool_name: "AskUserQuestion", input, tool_use_id: "toolu_ask2" });
  const first = h.lastDialog();
  h.reply(first.id, first.options[0]);
  assert.equal(h.esc(h.lastDialog().id), true);
  const last = h.answers().at(-1);
  assert.equal(last.requestId, "req-ask-2");
  assert.equal(last.decision.behavior, "deny");
  assert.equal(last.decision.decisionClassification, "user_reject");
  assert.equal(h.broker.size, 0);
});

test("an unanswered request is denied at the timeout and its dialog closed; a late response writes nothing", () => {
  assert.equal(CLAUDE_PERMISSION_TIMEOUT_MS, 30 * 60_000);
  const h = harness();
  h.broker.open("req-1", bash("sleep 1"));
  const dialog = h.lastDialog();

  h.advance(CLAUDE_PERMISSION_TIMEOUT_MS - 1);
  assert.deepEqual(h.lines, []);
  assert.equal(h.broker.size, 1);

  h.advance(1);
  const answers = h.answers();
  assert.equal(answers.length, 1);
  assert.equal(answers[0].requestId, "req-1");
  assert.equal(answers[0].decision.behavior, "deny");
  assert.equal(typeof answers[0].decision.message, "string");
  assert.ok(answers[0].decision.message.length > 0);
  assert.deepEqual(h.closedIds(), [dialog.id]);
  assert.equal(h.broker.size, 0);
  assert.deepEqual(h.broker.pending(), []);

  assert.equal(h.reply(dialog.id, "Allow once"), false);
  assert.equal(h.lines.length, 1, "a late answer writes nothing");

  // The deadline belongs to the request: a later question's dialog keeps it.
  const ask = harness();
  ask.broker.open("req-ask", {
    subtype: "can_use_tool",
    tool_name: "AskUserQuestion",
    input: { questions: [
      { question: "A?", header: "A", multiSelect: false, options: [{ label: "x", description: "" }, { label: "y", description: "" }] },
      { question: "B?", header: "B", multiSelect: false, options: [{ label: "x", description: "" }, { label: "y", description: "" }] },
    ] },
    tool_use_id: "toolu_ask",
  });
  const a = ask.lastDialog();
  ask.advance(60_000);
  ask.reply(a.id, a.options[0]);
  const b = ask.lastDialog();
  assert.equal(b.expiresAt, START + CLAUDE_PERMISSION_TIMEOUT_MS);
  assert.equal(b.timeout, CLAUDE_PERMISSION_TIMEOUT_MS - 60_000);
  ask.advance(CLAUDE_PERMISSION_TIMEOUT_MS - 60_000);
  assert.equal(ask.answers().length, 1);
  assert.equal(ask.answers()[0].decision.behavior, "deny");
  assert.deepEqual(ask.closedIds(), [a.id, b.id]);
});

test("control_cancel_request closes the dialog and writes nothing; closeAll closes every pending one", () => {
  const h = harness();
  h.broker.open("req-1", bash("a"));
  const first = h.lastDialog();
  h.broker.open("req-2", bash("b", { tool_use_id: "toolu_2" }));
  const second = h.lastDialog();
  h.broker.open("req-3", bash("c", { tool_use_id: "toolu_3" }));
  const third = h.lastDialog();
  assert.equal(h.broker.size, 3);

  h.broker.cancel("req-2");
  assert.deepEqual(h.closedIds(), [second.id]);
  assert.deepEqual(h.lines, []);
  assert.equal(h.broker.size, 2);
  assert.equal(h.reply(second.id, "Allow once"), false);
  assert.deepEqual(h.lines, []);
  h.broker.cancel("req-unknown");
  assert.deepEqual(h.closedIds(), [second.id]);

  h.broker.closeAll();
  assert.deepEqual(h.closedIds(), [second.id, first.id, third.id]);
  assert.deepEqual(h.lines, []);
  assert.equal(h.broker.size, 0);
  assert.deepEqual(h.broker.pending(), []);
  assert.equal(h.reply(first.id, "Allow once"), false);

  h.advance(CLAUDE_PERMISSION_TIMEOUT_MS * 2);
  assert.deepEqual(h.lines, [], "no timer outlives a closed request");
});

test("pending() lists the open dialogs in arrival order for a new listener", () => {
  const h = harness();
  h.broker.open("req-a", bash("a"));
  h.broker.open("req-b", bash("b", { tool_use_id: "toolu_b" }));
  h.broker.open("req-c", bash("c", { tool_use_id: "toolu_c" }));
  const [a, b, c] = h.dialogs();
  assert.deepEqual(h.broker.pending(), [a, b, c]);

  h.reply(b.id, "Deny");
  assert.deepEqual(h.broker.pending(), [a, c]);

  h.broker.open("req-a", bash("a again"));
  assert.equal(h.dialogs().length, 3, "a request id already pending opens nothing new");
  assert.deepEqual(h.broker.pending(), [a, c]);
});

test("a permission_denied frame becomes a warning notify naming the tool and the reason", () => {
  const notice = claudePermissionDeniedNotice({ toolName: "Bash", toolUseId: "toolu_9", reason: "mode/dontAsk" }, "en");
  assert.equal(notice.type, "extension_ui_request");
  assert.equal(notice.method, "notify");
  assert.equal(notice.notifyType, "warning");
  assert.equal(typeof notice.id, "string");
  assert.ok(notice.id.length > 0);
  assert.match(notice.message, /Bash/);
  assert.match(notice.message, /mode\/dontAsk/);

  const zh = claudePermissionDeniedNotice({ toolName: "Write", toolUseId: "toolu_10", reason: "rule" }, "zh-CN");
  assert.match(zh.message, /claude 拒绝了 Write/);
  assert.match(zh.message, /rule/);

  const bare = claudePermissionDeniedNotice({ toolName: "Read", toolUseId: "toolu_11" }, "en");
  assert.equal(bare.notifyType, "warning");
  assert.match(bare.message, /Read/);
  assert.notEqual(bare.id, notice.id);
});
