import "#test-env";
import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { basename, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { createJiti } from "jiti";

// the job card is the flow and the input box — the 交互
// button and the work-item panel it opened are gone, and so is everything
// only they used (the hook / shell plumbing, the helpers, the i18n keys, the
// allowlist entry, the suites' carries globs).
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { PolyforgePanel } = await jiti.import("./PolyforgePanel.tsx");

const ROOT = fileURLToPath(new URL("..", import.meta.url));
const SELF = fileURLToPath(import.meta.url);

const noop = () => {};
const resolved = () => Promise.resolve();

const entry = (id, fields) => ({ id, at: "2026-11-12T01:00:00Z", kind: "work_item", class: "failed", attention: true, resolved: false, dismissed: false, ...fields });

const renderPanel = () => {
  const job = { wi: "wi_T499", slug: "job-t499", status: "running", stepAgeSec: 5, idleAgeSec: 1, candidate: "claude", spawnN: 1 };
  const feed = {
    decisions: [
      entry(2, { wi: "wi_T499", slug: "job-t499", phrase: "the live job's failed entry" }),
      entry(1, { wi: "wi_D499", slug: "dropped-d499", class: "blocked", phrase: "the dropped item's block" }),
    ],
    attention: 2,
  };
  const messagesSurface = {
    pfPresent: true, capable: true, reason: null, conversations: [], contacts: [], groups: [], membershipAdmin: null, users: [],
    onChangeMembership: resolved, error: null, selectedConversation: null, onSelectConversation: noop, selectedContact: null,
    onSelectContact: noop, messages: [], initialPageLoaded: true, blocked: false, loadingOlder: false, onLoadOlder: noop,
    onAcknowledge: noop, onSend: resolved, onAnalyze: noop, analyses: {}, onOpenAnchor: noop, unreadTotal: 0,
    bindPromptTarget: () => null, onPrompt: noop, onPromptBusyChange: noop,
  };
  // the old surface's open state, count source and feed error are still
  // passed: once deleted they are ignored extra props
  const decisionsSurface = {
    feed, onDismiss: resolved, taskRequest: null, onTaskRequestHandled: noop,
    itemPanelOpen: true, onItemPanel: noop, error: "E2E-499 stale feed",
  };
  return renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(PolyforgePanel, {
    opsStatus: { queue: { running: 1, max: 4 }, jobs: [job], tasks: { running: 0, completed: 0 } },
    opsError: null, cards: null, cardsError: null, watchStatus: null,
    selectedWi: "wi_T499", onSelect: noop, onClose: noop, onRefresh: noop,
    activeTab: "work", onActiveTab: noop, workTab: "jobs", onWorkTab: noop,
    workItemsRunning: null, onWorkItemsRunningChange: noop, memoryCwd: null,
    messagesSurface, decisionsSurface, showPin: false, pinned: false, onTogglePin: noop,
  })));
};

test("a selected job's card is the flow and the input box — no 交互 button and no item panel, whatever the old open state", () => {
  const html = renderPanel();
  assert.match(html, /data-pf-chat-input/, "the selected job's input box is rendered");
  assert.match(html, /data-pf-card-attention/, "the dropped item's feed-only attention card is rendered");
  const itemAttrs = html.match(/data-pf-item-[a-z-]*/g) ?? [];
  assert.deepEqual(itemAttrs, [], "no item toggle, count, panel or resume attribute");
  assert.ok(!html.includes(">Interactions<"), "no Interactions label");
});

const SCAN_DIRS = ["app", "components", "hooks", "lib", "e2e", "docs"];
const SCAN_EXT = new Set([".ts", ".tsx", ".mjs", ".js", ".json", ".md"]);
const RETIRED = [
  /PolyforgeItemPanel/, /data-pf-item-/, /itemPanelOpen/, /ItemPanelOpen/, /onItemPanel/, /itemAttentionCount/,
  /resumableNow/, /decisionAction/, /decisionsError/, /ITEM_TOGGLE_CLEARANCE/, /data-pf-feed-stale/,
  /polyforge\.item\./, /polyforge\.blocked\.refile/, /polyforge\.decisions\.actionHint/,
];

function walk(dir, out) {
  if (!existsSync(dir)) return out;
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name.startsWith(".")) continue;
    const path = join(dir, name);
    if (statSync(path).isDirectory()) walk(path, out);
    else if (SCAN_EXT.has(name.slice(name.lastIndexOf("."))) && path !== SELF) out.push(path);
  }
  return out;
}

test("nothing references the deleted item panel — its file, the hook and shell plumbing, its helpers, i18n keys, allowlist entry and carries globs are gone", () => {
  assert.ok(!existsSync(join(ROOT, "components", "PolyforgeItemPanel.tsx")), "components/PolyforgeItemPanel.tsx is deleted");
  const files = SCAN_DIRS.flatMap((dir) => walk(join(ROOT, dir), []));
  files.push(join(ROOT, "AGENTS.md"));
  assert.ok(files.some((file) => basename(file) === "PolyforgePanel.tsx"), "the walk reaches the sources");
  const hits = [];
  for (const file of files) {
    readFileSync(file, "utf8").split("\n").forEach((line, index) => {
      for (const pattern of RETIRED) {
        if (pattern.test(line)) hits.push(`${relative(ROOT, file)}:${index + 1}: ${pattern.source}`);
      }
    });
  }
  assert.deepEqual(hits, [], `retired references remain:\n${hits.join("\n")}`);
});
