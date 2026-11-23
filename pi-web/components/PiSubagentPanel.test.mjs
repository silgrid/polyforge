// carries: components/PiSubagentPanel.tsx, components/SubagentPanelFrame.tsx, components/MessageView.tsx, lib/pi-subagent-panel.ts, lib/i18n/messages/en.ts
// polyforge#255 u4: the pi subagent progress panel — its header (profile,
// description, status, elapsed), the child transcript read-only (process and
// result, refreshed from the entries it is given), Jump/Close, and the input
// box that steers while the run runs and is disabled with the settled reason.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { PiSubagentPanel } = await jiti.import("./PiSubagentPanel.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
await jiti.import("@/lib/i18n/messages/en");
const { formatTurnDuration } = await jiti.import("@/lib/turn-duration");

const NOW = Date.UTC(2026, 10, 23, 12, 0, 0);

function run(fields = {}) {
  return {
    sessionId: "child-1",
    parentSessionId: "parent-1",
    parentToolCallId: "toolu-agent-1",
    profile: "explore",
    description: "Find the parser",
    status: "running",
    createdAt: new Date(NOW - 12_000).toISOString(),
    ...fields,
  };
}

const entries = [
  { id: "e-1", message: { role: "user", content: [{ type: "text", text: "Find the parser" }] } },
  { id: "e-2", message: { role: "assistant", content: [{ type: "toolCall", toolCallId: "call-read", toolName: "Read", input: { path: "lib/parser.ts" } }] } },
  { id: "e-3", message: { role: "toolResult", toolCallId: "call-read", toolName: "Read", content: [{ type: "text", text: "export function parse() {}" }] } },
  { id: "e-4", message: { role: "assistant", content: [{ type: "text", text: "Parser is in lib/parser.ts" }] } },
];

function render(props = {}) {
  return renderToStaticMarkup(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(PiSubagentPanel, {
        sessionId: "child-1",
        run: run(),
        phase: "ready",
        error: null,
        entries,
        hasEarlier: false,
        loadingEarlier: false,
        steerError: null,
        launchToolCallId: "toolu-agent-1",
        cwd: "/repo",
        onLoadEarlier: () => {},
        onJump: () => {},
        onClose: () => {},
        onSteer: async () => true,
        now: NOW,
        ...props,
      }),
    ),
  );
}

function textarea(html) {
  return /<textarea([^>]*)>/.exec(html)?.[1] ?? "";
}
function button(html, label) {
  return [...html.matchAll(/<button([^>]*)>([\s\S]*?)<\/button>/g)]
    .map((match) => ({ attrs: match[1], text: `${/aria-label="([^"]*)"/.exec(match[1])?.[1] ?? ""} ${match[2].replace(/<[^>]+>/g, " ")}` }))
    .find((item) => item.text.includes(label));
}

test("polyforge#255 u4: the header names the run and its live status and elapsed time", () => {
  const html = render();
  assert.match(html, /data-pi-subagent-panel="child-1"/, "the panel is named by the child session");
  assert.match(html, /data-pi-subagent-status="running"/, "the panel carries the run's status");
  assert.match(html, /aria-label="Find the parser"/, "the description names the section");
  assert.match(html, />explore</, "the profile badge renders");
  assert.match(html, />Running</, "the status word renders");
  assert.ok(html.includes(formatTurnDuration(12_000)), "the elapsed time ticks from creation");
  assert.ok(button(html, "Close"), "the header closes the panel");
  assert.ok(button(html, "Jump to launch"), "the header offers Jump");
});

test("polyforge#255 u4: the body renders the child transcript (process and result) read-only", () => {
  const html = render();
  assert.match(html, /Find the parser/, "the prompt is part of the process");
  assert.match(html, /lib\/parser\.ts/, "the tool call names the file it read");
  assert.match(html, /export function parse\(\) \{\}/, "the tool result renders");
  assert.match(html, /Parser is in lib\/parser\.ts/, "the final answer is the result");
  assert.doesNotMatch(html, /chat-input-textarea/, "the panel embeds no composer of its own");
});

test("polyforge#255 u4: a running run takes a message; the send box is the main composer's", () => {
  const html = render();
  const box = textarea(html);
  assert.match(box, /placeholder="Message this sub-agent"/, "the box explains what it does");
  assert.doesNotMatch(box, /\sdisabled=""/, "a running run takes a message");
  assert.ok(button(html, "Send"), "the send button renders");
  assert.doesNotMatch(html, /no further messages can be sent/, "no settled reason shows while running");
});

test("polyforge#255 u4: every settled and unread state disables the box with its own visible reason", () => {
  for (const [status, reason] of [
    ["completed", "This run has ended; no further messages can be sent"],
    ["failed", "This run failed; no further messages can be sent"],
    ["aborted", "This run was stopped; no further messages can be sent"],
    ["interrupted", "This run was interrupted; no further messages can be sent"],
    ["queued", "Queued; sending is available once the run starts"],
    ["starting", "Starting; sending is available once the run starts"],
    ["unknown", "The run's state is unknown; sending is disabled"],
  ]) {
    const html = render({ run: run({ status }) });
    assert.match(textarea(html), /\sdisabled=""/, `${status} disables the box`);
    assert.ok(html.includes(reason), `${status} shows its reason: ${reason}`);
  }
  for (const [phase, reason] of [
    ["loading", "Waiting for the run's state"],
    ["missing", "This run could not be found; sending is disabled"],
    ["error", "The run's state could not be read; sending is disabled"],
  ]) {
    const html = render({ phase, run: phase === "ready" ? run() : null });
    assert.match(textarea(html), /\sdisabled=""/, `${phase} disables the box`);
    assert.ok(html.includes(reason), `${phase} shows its reason: ${reason}`);
  }
});

test("polyforge#255 u4: Jump is offered only with a launch record in this chat; earlier history pages from the body", () => {
  const enabled = button(render(), "Jump to launch");
  assert.doesNotMatch(enabled.attrs, /\sdisabled=""/, "a located launch row enables Jump");
  const disabled = button(render({ launchToolCallId: null }), "Jump to launch");
  assert.match(disabled.attrs, /\sdisabled=""/, "without a launch row Jump is disabled");
  assert.match(disabled.attrs, /title="No launch record in this chat"/, "and says why");
  assert.ok(button(render({ hasEarlier: true }), "Load earlier"), "the body pages earlier history when there is more");
});

test("polyforge#255 u4: the recorded result and a steer failure are visible; a settled duration is fixed", () => {
  const html = render({
    run: run({ status: "completed", completedAt: new Date(NOW - 3_000).toISOString(), result: "Parser is in lib/parser.ts" }),
    steerError: "Subagent is not running",
  });
  assert.match(html, />Result</, "the result section is labelled");
  assert.ok(html.includes("Parser is in lib/parser.ts"), "the recorded result renders");
  assert.ok(html.includes("Subagent is not running"), "a refused steer keeps its server message visible");
  assert.ok(html.includes(formatTurnDuration(9_000)), "a settled run shows completedAt − createdAt");
  assert.ok(!html.includes(formatTurnDuration(12_000)), "and stops ticking");
});