import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const { MachineLoadSection } = await jiti.import("./MachineLoadSection.tsx");
const { enLocale } = await jiti.import("../lib/i18n/messages/en.ts");
const { zhCNLocale } = await jiti.import("../lib/i18n/messages/zh-CN.ts");
const { zhTWLocale } = await jiti.import("../lib/i18n/messages/zh-TW.ts");

const appShell = await readFile(new URL("./AppShell.tsx", import.meta.url), "utf8");
const agentPanel = await readFile(new URL("./AgentSessionPanel.tsx", import.meta.url), "utf8");
const debugPanel = await readFile(new URL("./DebugPanel.tsx", import.meta.url), "utf8");

const KEYS = [
  "session.machineLoad",
  "session.machineHost",
  "session.machineCpu",
  "session.machineCores",
  "session.machineLoadavg",
  "session.machineMemory",
  "session.machineUnavailable",
];

function render(messages) {
  const translate = (key) => messages[key] ?? key;
  return renderToStaticMarkup(React.createElement(MachineLoadSection, { translate }));
}

test("first open (no good value yet) renders the heading, the host badge and 不可用 in all four rows", () => {
  const html = render(zhCNLocale.messages);
  assert.match(html, /class="machine-load-section"/);
  assert.match(html, />机器负载</);
  assert.match(html, />宿主机</);
  assert.equal((html.match(/>不可用</g) || []).length, 4);
  assert.doesNotMatch(html, /\d%|0\.00|GiB/, "no fabricated value before the first success");
  assert.doesNotMatch(html, /<input|<select|<textarea/, "no interval control");
  // The rows wrap inside the panel instead of widening it: the label may shrink (its longest is
  // "Memory (used / total)"), while the value keeps its own width, so a narrow column wraps the
  // label rather than the reading apart (owner-intended change: the load is a narrow column
  // in the wide-panel grid, so values must not be the part that breaks).
  assert.match(html, /grid-template-columns:minmax\(0, max-content\) minmax\(max-content, 1fr\)/, "the label shrinks before the value does");
});

test("all three locales resolve every section key", () => {
  assert.equal(zhCNLocale.messages["session.machineLoad"], "机器负载");
  assert.equal(zhCNLocale.messages["session.machineHost"], "宿主机");
  assert.equal(zhCNLocale.messages["session.machineUnavailable"], "不可用");
  for (const locale of [enLocale, zhCNLocale, zhTWLocale]) {
    for (const key of KEYS) assert.equal(typeof locale.messages[key], "string", `${locale.id}: ${key}`);
    const html = render(locale.messages);
    assert.doesNotMatch(html, /session\.machine/, `${locale.id}: no raw key fallback`);
  }
  assert.doesNotMatch(render(enLocale.messages), /[一-鿿]/, "en renders no CJK");
  assert.notEqual(zhTWLocale.messages["session.machineLoad"], zhCNLocale.messages["session.machineLoad"]);
});

test("only the Debug panel mounts it: not the Session-info popover, not the Agents dropdown", () => {
  assert.doesNotMatch(appShell, /MachineLoadSection|machine-load/, "AppShell (the Session-info popover) mounts no section and polls nothing");
  assert.equal((debugPanel.match(/<MachineLoadSection /g) || []).length, 1);
  assert.doesNotMatch(agentPanel, /MachineLoad|machine-load/);
});
