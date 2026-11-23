// carries: components/ChatWindow.tsx, lib/i18n/messages/*, docs/agents/claude-sessions.md, AGENTS.md
// polyforge#255 round 2 #8/#7: the right panel's subagent-chat path leaves no
// residue (the `panel` prop, the `sessionPanel.*` copy, the claude-sessions
// "side panel" wording), and the new pi-web files are in the File Map.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const [chatWindow, en, zhCN, zhTW, claudeSessions, agents] = await Promise.all([
  readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8"),
  readFile(new URL("../lib/i18n/messages/en.ts", import.meta.url), "utf8"),
  readFile(new URL("../lib/i18n/messages/zh-CN.ts", import.meta.url), "utf8"),
  readFile(new URL("../lib/i18n/messages/zh-TW.ts", import.meta.url), "utf8"),
  readFile(new URL("../docs/agents/claude-sessions.md", import.meta.url), "utf8"),
  readFile(new URL("../AGENTS.md", import.meta.url), "utf8"),
]);

test("polyforge#255 r2: the deleted right-panel chat leaves no prop or copy behind", () => {
  assert.doesNotMatch(chatWindow, /panel\?: boolean/, "the panel prop is deleted");
  assert.doesNotMatch(chatWindow, /panel = false/, "no panel default remains");
  assert.doesNotMatch(chatWindow, /if \(!panel\)/, "the abort registration is unconditional again");
  assert.match(chatWindow, /registerAbortHandler\(sessionBusy \? handleAbort : null\)/, "the normal chat still registers its abort handler");
  for (const locale of [en, zhCN, zhTW]) assert.doesNotMatch(locale, /"sessionPanel\./, "the deleted chat's copy is gone");
  assert.doesNotMatch(claudeSessions, /pi subagent switcher and side panel/, "claude-sessions no longer claims a pi side panel");
});

test("polyforge#255 r2: the new pi-web files are in the File Map", () => {
  assert.match(agents, /usePiSubagentPanel\.ts/, "the hook is mapped");
  assert.match(agents, /pi-subagent-panel\.ts/, "the lib module is mapped");
});
