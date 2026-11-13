import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ClaudeSubagentRunningList } = await jiti.import("./ClaudeSubagentRunningList.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { formatTurnDuration } = await jiti.import("@/lib/turn-duration");
const { enLocale } = await jiti.import("@/lib/i18n/messages/en");
const { zhCNLocale } = await jiti.import("@/lib/i18n/messages/zh-CN");
const { zhTWLocale } = await jiti.import("@/lib/i18n/messages/zh-TW");

const NOW = Date.UTC(2026, 10, 10, 12, 0, 0);

function run(fields) {
  return { background: false, status: "running", entries: [], ...fields };
}

function render(runs) {
  return renderToStaticMarkup(
    React.createElement(I18nProvider, null, React.createElement(ClaudeSubagentRunningList, { runs, onOpen: () => {}, now: NOW })),
  );
}

function items(html) {
  return [...html.matchAll(/<button[^>]*data-claude-subagent-item="([^"]+)"[^>]*>([\s\S]*?)<\/button>/g)].map((match) => ({ id: match[1], text: match[2].replace(/<[^>]+>/g, " ") }));
}

test("lists each running subagent with its description, status and elapsed time, and renders nothing once none runs", () => {
  const runs = {
    toolu_fg: run({ toolCallId: "toolu_fg", description: "Read note.txt first line", agentType: "general-purpose", startedAt: NOW - 65_000 }),
    toolu_bg: run({ toolCallId: "toolu_bg", agentType: "Explore", background: true, startedAt: NOW - 5_000 }),
    toolu_bare: run({ toolCallId: "toolu_bare", startedAt: NOW - 3_725_000 }),
    toolu_done: run({ toolCallId: "toolu_done", description: "Finished work", status: "completed", startedAt: NOW - 90_000 }),
    toolu_failed: run({ toolCallId: "toolu_failed", description: "Failed work", status: "failed", startedAt: NOW - 80_000 }),
    toolu_stopped: run({ toolCallId: "toolu_stopped", description: "Stopped work", status: "stopped", startedAt: NOW - 70_000 }),
  };
  const html = render(runs);
  assert.match(html, /data-claude-subagent-list/);
  const listed = items(html);
  // Oldest start first; every settled run is left out.
  assert.deepEqual(listed.map((item) => item.id), ["toolu_bare", "toolu_fg", "toolu_bg"]);
  const [bare, fg, bg] = listed;
  assert.match(fg.text, /Read note\.txt first line/);
  assert.match(fg.text, /Running/);
  assert.ok(fg.text.includes(formatTurnDuration(65_000)), fg.text);
  // No description: the agent type, then the tool name.
  assert.match(bg.text, /Explore/);
  assert.match(bg.text, /Running/);
  assert.ok(bg.text.includes(formatTurnDuration(5_000)), bg.text);
  assert.match(bare.text, /Agent/);
  assert.ok(bare.text.includes(formatTurnDuration(3_725_000)), bare.text);
  assert.doesNotMatch(html, /Finished work|Failed work|Stopped work/);

  assert.equal(render({}), "");
  assert.equal(render({ toolu_done: runs.toolu_done, toolu_failed: runs.toolu_failed, toolu_stopped: runs.toolu_stopped }), "");
});

test("however many run, the buttons scroll inside a box three rows tall, with no count label beside it", () => {
  const many = {};
  for (let i = 1; i <= 60; i += 1) {
    const id = `toolu_${String(i).padStart(2, "0")}`;
    many[id] = run({ toolCallId: id, description: `Audit module ${i}`, startedAt: NOW - i * 1_000 });
  }
  // The list holds the items' box and nothing else: no count before or after it.
  const onlyItems = (html) => {
    const match = /^<div data-claude-subagent-list="true"[^>]*>(<div data-claude-subagent-items="true"[^>]*>[\s\S]*<\/button><\/div>)<\/div>$/.exec(html);
    assert.ok(match, `the list holds something beside its items: ${html.slice(0, 600)}`);
  };
  const html = render(many);
  assert.equal(items(html).length, 60);
  // The buttons' box: three 24px rows and the two 4px gaps between them, scrolling inside.
  const box = /<div data-claude-subagent-items="true" style="([^"]*)"/.exec(html);
  assert.ok(box, html.slice(0, 600));
  assert.match(box[1], /max-height:80px/, box[1]);
  assert.match(box[1], /overflow-y:auto/, box[1]);
  assert.doesNotMatch(html, /data-claude-subagent-count/);
  assert.doesNotMatch(html, /60 running/);
  onlyItems(html);

  const two = render({ toolu_01: many.toolu_01, toolu_02: many.toolu_02 });
  assert.equal(items(two).length, 2);
  assert.doesNotMatch(two, /data-claude-subagent-count/);
  assert.doesNotMatch(two, /2 running/);
  onlyItems(two);
  const one = render({ toolu_01: many.toolu_01 });
  assert.equal(items(one).length, 1);
  assert.doesNotMatch(one, /data-claude-subagent-count/);
  onlyItems(one);
});

test("the list's count string claudeSubagent.runningCount is gone from every locale", () => {
  for (const [locale, plugin] of [["en", enLocale], ["zh-CN", zhCNLocale], ["zh-TW", zhTWLocale]]) {
    assert.equal(Object.hasOwn(plugin.messages, "claudeSubagent.runningCount"), false, `${locale} still has claudeSubagent.runningCount`);
  }
});

test("the running list's buttons sit in an equal-width auto-filling grid, each filling its cell", () => {
  const runs = {
    toolu_short: run({ toolCallId: "toolu_short", description: "Lint", startedAt: NOW - 5_000 }),
    toolu_mid: run({ toolCallId: "toolu_mid", description: "Trace the gateway request path", startedAt: NOW - 65_000 }),
    toolu_long: run({ toolCallId: "toolu_long", description: "Audit every module of the session store for stale lease handling and report each finding with its file and line", startedAt: NOW - 3_725_000 }),
  };
  const html = render(runs);
  const box = /<div data-claude-subagent-items="true" style="([^"]*)"/.exec(html);
  assert.ok(box, html.slice(0, 600));
  // Equal-width columns, as many as fit, none narrower than 260px.
  assert.match(box[1], /(^|;)display:grid(;|$)/, box[1]);
  assert.match(box[1], /grid-template-columns:\s*repeat\(\s*auto-fill\s*,\s*minmax\(\s*260px\s*,\s*1fr\s*\)\s*\)/, box[1]);
  // The three-row box that scrolls inside itself stays.
  assert.match(box[1], /max-height:80px/, box[1]);
  assert.match(box[1], /overflow-y:auto/, box[1]);
  const buttons = [...html.matchAll(/<button[^>]*data-claude-subagent-item="[^"]+"[^>]*style="([^"]*)"/g)].map((match) => match[1]);
  assert.equal(buttons.length, 3);
  for (const style of buttons) {
    // Each button fills its cell.
    assert.match(style, /(^|;)width:100%(;|$)/, style);
    assert.doesNotMatch(style, /display:inline-flex/, style);
  }
});
