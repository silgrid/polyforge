// carries: components/PiSubagentPanel.tsx, lib/pi-subagent-panel.ts
// polyforge#255 round 2 #4(b): Jump is offered only when this chat can locate
// the launch call; while the search runs or after it fails the button stays
// disabled and names why (Q6).
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

const NOW = Date.UTC(2026, 10, 23, 12, 0, 0);
const run = { sessionId: "child-1", parentSessionId: "parent-1", parentToolCallId: "toolu-run", profile: "explore", description: "Find", status: "completed", createdAt: new Date(NOW - 12_000).toISOString(), completedAt: new Date(NOW - 3_000).toISOString() };

function jumpButton(props) {
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(PiSubagentPanel, {
    sessionId: "child-1", run, phase: "ready", error: null, entries: [], hasEarlier: false, loadingEarlier: false, steerError: null,
    launchToolCallId: "toolu-run", cwd: "/repo", onLoadEarlier: () => {}, onJump: () => {}, onClose: () => {}, onSteer: async () => true, now: NOW,
    ...props,
  })));
  return [...html.matchAll(/<button([^>]*)>([\s\S]*?)<\/button>/g)].map((match) => ({ attrs: match[1], text: match[2].replace(/<[^>]+>/g, " ") })).find((item) => item.text.includes("Jump to launch"));
}

test("polyforge#255 r2: Jump is enabled only when the launch call is located in this chat", () => {
  const found = jumpButton({ jumpStatus: "found" });
  assert.doesNotMatch(found.attrs, /\sdisabled=""/, "a located launch row enables Jump");
  const missing = jumpButton({ jumpStatus: "missing", launchToolCallId: "toolu-run" });
  assert.match(missing.attrs, /\sdisabled=""/, "no located call disables Jump even when the run names one");
  assert.match(missing.attrs, /title="No launch record in this chat"/, "and says why");
  const searching = jumpButton({ jumpStatus: "searching" });
  assert.match(searching.attrs, /\sdisabled=""/, "while paging history, Jump is disabled");
  assert.match(searching.attrs, /title="Locating the launch record…"/, "the search has its own reason");
});
