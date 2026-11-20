// carries: components/ChatWindow*, components/ChatInput*, components/MessageView*, hooks/useAgentSession*, lib/i18n/messages/*
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const chatWindow = await readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8");
const chatInputSource = await readFile(new URL("./ChatInput.tsx", import.meta.url), "utf8");
const hook = await readFile(new URL("../hooks/useAgentSession.ts", import.meta.url), "utf8");

// #128 r1#1: AC3 is behaviour, so the u6 assertions render the components
// (renderToStaticMarkup, the same technique as components/ChatInput.test.mjs and
// components/MessageView.test.mjs) and assert the markup; the expected copy is
// AC3's wording and the i18n catalogue's, written out verbatim, never imported.
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ChatInput } = await jiti.import("./ChatInput.tsx");
const { MessageView } = await jiti.import("./MessageView.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const render = (element) => renderToStaticMarkup(React.createElement(I18nProvider, null, element));

test("a claude session offers no ask-in-new-chat or compaction recovery", () => {
  assert.match(chatWindow, /onAskInNewChat && !isClaudeSession && quotedSelection\.sourceEntryId/);
  assert.match(chatWindow, /onCompact=\{options\.recoverTruncation && !isClaudeSession \? handleCompact : undefined\}/);
});

// rewrote #219's "no fork, edit-from-here": a claude user message forks
// ("New session") and edits in place like pi's ("Edit from here", sent as one prompt naming
// the message, which the server turns into a truncating resume of the same session); neither
// while a claude turn or compaction runs, the edit only where claude can resume before the message.
test("a claude session forks at a user message and edits it in place, never while a turn runs", () => {
  assert.match(chatWindow, /const claudeActionsLocked = isClaudeSession && \(agentRunning \|\| isCompacting\);/);
  assert.match(chatWindow, /onFork=\{bashRunning \|\| isNew \|\| claudeActionsLocked \? undefined : handleFork\}/);
  assert.match(chatWindow, /onEditContent=\{sessionBusy \|\| claudeActionsLocked \|\| \(isClaudeSession && !claudeEditable\.has\(entryIds\[idx\]\)\) \? undefined : handleEditContent\}/);
  // One edit handler for both harnesses; no fork-and-prefill path is left.
  assert.match(hook, /^ {4}handleEditContent,$/m);
  assert.doesNotMatch(chatWindow + hook, /handleClaudeEditContent|editOpensSession|setDraft\(newSessionId/);
  assert.match(hook, /\.\.\.\(claudeEditEntryId \? \{ entryId: claudeEditEntryId \} : \{\}\),/);
});

// rewrote #250's "hides the pi model, reasoning and compact controls": claude gets
// its own model / effort handlers (through `claude`) and compacts in an open session.
test("a claude session gets claude's model, effort and compact handlers and pi's Steer; still no pi model, reasoning, tools, follow-up, queue recall or built-in commands", () => {
  for (const pattern of [
    /onModelChange=\{isClaudeSession \? undefined : handleModelChange\}/,
    // polyforge#71 (AC5): an open session of either harness compacts; a fresh
    // composer of either harness has no compact control (there is nothing to
    // compact yet) — the earlier (session || isNew) rule showed a dead button.
    /onCompact=\{session \? handleCompact : undefined\}/,
    /onAbortCompaction=\{isClaudeSession \? handleAbort : handleAbortCompaction\}/,
    /onToolPresetChange=\{\(session \|\| isNew\) && !isClaudeSession \? handleToolPresetChange : undefined\}/,
    /onThinkingLevelChange=\{\(session \|\| isNew\) && !isClaudeSession \? handleThinkingLevelChange : undefined\}/,
    /onSteer=\{isActivePane && agentRunning \? handleSteer : undefined\}/,
    /onRecallQueue=\{isClaudeSession \? undefined : handleRecallQueue\}/,
    /onFollowUp=\{isActivePane && agentRunning && !isClaudeSession \? handleFollowUp : undefined\}/,
    // polyforge#71 (AC4): a `/` message sent while a turn runs takes the same
    // entry on both harnesses — ChatWindow no longer branches on the harness here.
    /onPromptWithStreamingBehavior=\{isActivePane && agentRunning \? handlePromptWithStreamingBehavior : undefined\}/,
    /onBuiltinCommand=\{handleBuiltinSlashCommand\}/,
    /claude=\{claudeComposer\}/,
  ]) assert.match(chatWindow, pattern);
  // polyforge#71 r1#3: the claude routing gate (only /compact and /session reach pi's
  // handler) is pinned as behaviour in hooks/mcp-slash-command.test.mjs — no source pin here.
});

test("a fresh composer passes the harness switch (with its model reset), the claude model and effort; an open claude session switches model and effort, never the harness", () => {
  const block = chatWindow.slice(chatWindow.indexOf("const claudeComposer = useMemo"), chatWindow.indexOf("// External-write signal"));
  const fresh = block.slice(block.indexOf("if (isNew)"), block.indexOf("if (!isClaudeSession)"));
  assert.match(fresh, /choosing: true/);
  assert.match(fresh, /model: newSessionClaudeModel/);
  assert.match(fresh, /onHarnessChange: chooseNewSessionHarness/);
  assert.match(fresh, /onModelChange: pickNewSessionClaudeModel/);
  // 85a3-1: the star's filled marker and its save handler ride along
  assert.match(fresh, /defaultModel: claudeDefaultModel/);
  assert.match(fresh, /onSetDefaultModel: handleSetDefaultClaudeModel/);
  assert.match(fresh, /effort: newSessionClaudeEffort/);
  assert.match(fresh, /onEffortChange: handleClaudeEffortChange/);
  const open = block.slice(block.indexOf("if (!isClaudeSession)"));
  assert.match(open, /choosing: false/);
  assert.match(open, /model: normalizeClaudeModel\(claudeSessionChoices\?\.model\)/);
  assert.match(open, /effort: normalizeClaudeEffort\(claudeSessionChoices\?\.effort\)/);
  assert.match(open, /onModelChange: handleClaudeModelChange/);
  assert.match(open, /onEffortChange: handleClaudeEffortChange/);
  assert.match(open, /switchError: claudeSwitchError/);
  assert.doesNotMatch(open, /onHarnessChange/, "an open claude session gets no harness switch");
  assert.match(hook, /const chooseNewSessionHarness = useCallback/);
});

test("the chat hands a claude session's subagent runs to every Agent row, listed or streamed", () => {
  // #128 u6 added the restart-refusal answers' destructure line just before the
  // hook call's closing line, so the pin allows the lines between the claude
  // availability fields and that closing line to change.
  assert.match(chatWindow, /claudeAvailability, claudeSubagents,\n(?:[^\n]*\n)*  \} = useAgentSession\(/);
  assert.match(chatWindow, /<ClaudeSubagentContext\.Provider value=\{claudeSubagents\}>/);
  const provider = chatWindow.indexOf("<ClaudeSubagentContext.Provider");
  assert.ok(provider < chatWindow.indexOf("<StreamingReply store="), "the streaming reply is inside the provider");
  assert.ok(provider < chatWindow.indexOf("key={`${keyPrefix}-view-${messageKey}`}"), "the listed messages are inside the provider");
  assert.match(hook, /claudeSubagents: claudeSubagentState\.sessionId !== null/);
});

test("a running background Agent step renders outside the folded process details, after them, and nothing else leaves the fold", () => {
  assert.match(chatWindow, /if \(processIdx !== finalAssistantIdx && isRunningBackgroundAgentMessage\(blocks, claudeSubagents\)\) \{\n\s*runningSubagentViews\.push\(renderMessage\(processIdx, \{ attachRef: false, keyPrefix: "subagent", showTimestamp: false \}\)\);\n\s*continue;\n\s*\}/);
  const group = chatWindow.indexOf("<ProcessDetailsGroup key={answered");
  const outside = chatWindow.indexOf("rendered.push(...runningSubagentViews);");
  assert.ok(group > 0 && outside > group, "the running row follows the fold, at the turn's top level");
  assert.ok(outside < chatWindow.indexOf("const writtenFiles = keepWrittenFiles"), "before the final answer");
  assert.equal(chatWindow.split("isRunningBackgroundAgentMessage(").length - 1, 1, "one fold exception");
});

// #128 (AC3 + AC1's prompt): a staged edit is a visible, cancellable mode of the
// composer — the edit bar with its Cancel — and a restart-refused edit adds the
// two-choice prompt («作为新消息发送» submits the composer's own text once without
// the entryId, «保留编辑» only closes the prompt); the history edit entry stays
// offered while background work runs, annotated that submitting must wait for it.
// r1#1: behaviour asserted on the rendered markup; the hook-side behaviour is pinned
// in hooks/useAgentSession.claude.test.mjs and ChatWindow's wiring stays a source pin.
test("128 u6 the composer shows the pending-edit bar and the restart-refused two-choice prompt; history edit stays offered while background work runs", () => {
  // AC3: the edit bar, only while an edit is staged, with its one-click Cancel.
  const editing = render(React.createElement(ChatInput, {
    onSend() {}, onAbort() {},
    editPending: true, onCancelEdit() {},
  }));
  assert.ok(editing.includes("Editing a history message — Send replaces it and everything after it."), "no edit-state bar over the composer");
  assert.ok(editing.includes(">Cancel edit</button>"), "the edit bar offers no Cancel");
  const notEditing = render(React.createElement(ChatInput, { onSend() {}, onAbort() {} }));
  assert.ok(!notEditing.includes("Editing a history message"), "an edit bar without a staged edit");
  assert.ok(!notEditing.includes(">Cancel edit</button>"), "a Cancel without a staged edit");

  // AC1's prompt: the refusal's own text and the two answers, on the structured code alone.
  const refused = render(React.createElement(ChatInput, {
    onSend() {}, onAbort() {},
    claudeEditRestartRefusal: 'A claude background task is still running ("npm run dev"). Editing a message restarts claude, which would end it; try again when it is done.',
    onSendClaudeEditAsNewMessage() {}, onKeepClaudeEditPending() {},
  }));
  assert.ok(refused.includes("A claude background task is still running"), "the refusal prompt shows no refusal text");
  assert.ok(refused.includes("npm run dev"), "the refusal prompt does not name the task's command");
  assert.ok(refused.includes(">Send as a new message</button>"), "no「作为新消息发送」on the refusal prompt");
  assert.ok(refused.includes(">Keep the edit, try later</button>"), "no「保留编辑」on the refusal prompt");
  assert.ok(!refused.includes("Editing a history message — Send replaces it"), "the refusal prompt is the edit bar");
  const unrefused = render(React.createElement(ChatInput, { onSend() {}, onAbort() {} }));
  assert.ok(!unrefused.includes("Send as a new message"), "a refusal prompt without a refusal");

  // AC3: the history edit entry stays offered while background work runs, annotated
  // on both the label and the title; without the work the entry is exactly as before.
  const message = { role: "user", content: "Hi", timestamp: 1 };
  const waiting = render(React.createElement(MessageView, {
    message, entryId: "entry", onEditContent: () => {}, editWaitsForBackgroundWork: true,
  }));
  assert.ok(waiting.includes('title="Edit from here — branches within this session — waits for the background task to end"'), "the waiting annotation is not on the edit title");
  assert.match(waiting, /Edit from here(?:<!-- -->)? · waits for the background task to end/, "the waiting annotation is not on the edit label");
  const free = render(React.createElement(MessageView, { message, entryId: "entry", onEditContent: () => {} }));
  assert.ok(free.includes('title="Edit from here — branches within this session"'), "the plain edit title changed");
  assert.ok(!free.includes("waits for the background task to end"), "a waiting annotation without background work");
  assert.match(free, />Edit from here(?:<!-- -->)?</, "the edit entry itself went missing");

  // ChatWindow's wiring stays a source pin (the supplement review r1#1 allows):
  // the hook's edit state and both answers reach the session composer, and
  // background work annotates the entry instead of hiding it.
  assert.match(chatWindow, /editPending=\{editEntryId !== null\}/);
  assert.match(chatWindow, /onCancelEdit=\{cancelEdit\}/);
  assert.match(chatWindow, /claudeEditRestartRefusal=\{claudeEditRestartRefusal\}/);
  assert.match(chatWindow, /onSendClaudeEditAsNewMessage=\{handleSendClaudeEditAsNewMessage\}/);
  assert.match(chatWindow, /onKeepClaudeEditPending=\{dismissClaudeEditRestartRefusal\}/);
  assert.match(
    chatWindow,
    /const claudeBackgroundWorkRunning = isClaudeSession\s*\n\s*&& \(Object\.values\(claudeBackgroundTasks\)\.some\(\(task\) => task\.status === "running"\)\s*\n\s*\|\| Object\.values\(claudeSubagents\)\.some\(\(run\) => run\.status === "running"\)\);/,
    "background work must not hide the edit entry — it annotates it instead",
  );
  assert.match(chatWindow, /editWaitsForBackgroundWork=\{claudeBackgroundWorkRunning\}/);
});
