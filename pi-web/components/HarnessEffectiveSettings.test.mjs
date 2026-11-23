// carries: components/HarnessEffectiveSettings.tsx, lib/harness-settings-view.ts, components/HarnessGeneralSettings.tsx
// polyforge#149 (#85 85a1b, AC2/AC6): the 生效值与来源 pane — every effective
// value carries its 推算 (inferred) mark and its source, and the native
// diagnostics are one collapsed block per harness. Expected rows come from
// the multi-layer fixture derived by hand (project over user), and the
// adapter's own contract (#85 d-5: every value is inferred); the collapsed
// block's look is pinned from the ticket's 可折叠 wording.
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { effectiveRows } = await jiti.import("../lib/harness-settings-view.ts");
const { HarnessEffectiveValues, HarnessDiagnosticsBlock } = await jiti.import("./HarnessEffectiveSettings.tsx");
const { claudeEffectiveConfig } = await jiti.import("../lib/harness/effective.ts");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const h = React.createElement;
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-harness-effective-view-"));
const claudeConfigDir = path.join(scratch, "claude-config");
const agentDir = path.join(scratch, "agent");
const project = path.join(scratch, "project");
fs.mkdirSync(claudeConfigDir, { recursive: true });
fs.mkdirSync(agentDir, { recursive: true });
fs.mkdirSync(path.join(project, ".claude"), { recursive: true });

// The multi-layer fixture: a project layer over a user layer, each naming a
// different model; the project layer wins, and the row must say so.
fs.writeFileSync(path.join(claudeConfigDir, "settings.json"), JSON.stringify({ model: "claude-user-model" }));
fs.writeFileSync(path.join(project, ".claude", "settings.json"), JSON.stringify({ model: "claude-project-model", effortLevel: "low" }));
fs.writeFileSync(path.join(agentDir, "pi-web-settings.json"), JSON.stringify({ version: 1, claude: { approvalChannel: "ask", pushReminders: true, includeHookEvents: false } }));

test.after(() => fs.rmSync(scratch, { recursive: true, force: true }));

test("the effective rows carry the value, the inferred mark's data, and the winning source", () => {
  const payload = claudeEffectiveConfig({ claudeConfigDir, agentDir, cwd: project });
  const rows = effectiveRows(payload);
  assert.ok(rows.length > 0, "no rows came out of the payload");
  const model = rows.find((row) => row.field === "model");
  assert.ok(model, "the model field is missing");
  assert.equal(model.value, "claude-project-model", "the project layer did not win");
  assert.equal(model.sourceScope, "project");
  assert.equal(model.sourcePath, path.join(project, ".claude", "settings.json"));
  // Rows are sorted by field, and a non-string value renders as JSON.
  assert.deepEqual(rows.map((row) => row.field), [...rows.map((row) => row.field)].sort(), "the rows are not in a stable order");
  const shaped = effectiveRows({ fields: { "a-non-string": { value: { nested: true }, sourceScope: "user", sourcePath: "/x", inferred: true } } });
  assert.equal(shaped[0].value, JSON.stringify({ nested: true }));
});

test("the effective pane shows every value with its Inferred mark and its source", () => {
  const rows = [
    { field: "model", value: "claude-project-model", sourceScope: "project", sourcePath: "/project/.claude/settings.json" },
    { field: "pi-web:approval-channel", value: "ask", sourceScope: "pi-web", sourcePath: "/agent/pi-web-settings.json" },
  ];
  const html = renderToStaticMarkup(h(I18nProvider, null, h(HarnessEffectiveValues, { harness: "claude", rows })));
  assert.ok(html.includes("claude-project-model"), "the value is missing");
  assert.match(html, /Inferred/, "the Inferred mark is missing");
  assert.ok(html.includes("/agent/pi-web-settings.json"), "the source path is missing");
  assert.match(html, /data-harness="claude"/, "the group is not marked per harness");
  assert.match(html, /Computed from the config layers/, "the pane's inferred note is missing");
});

test("the native diagnostics are one collapsed block per harness, loading on open", () => {
  const idle = renderToStaticMarkup(h(I18nProvider, null,
    h(HarnessDiagnosticsBlock, { harness: "claude", state: { status: "loading" }, result: null, onOpen: () => {} })));
  assert.match(idle, /<details[^>]*class="settings-harness-diagnostics[^"]*"[^>]*data-harness="claude"/, "the block is not a per-harness details element");
  assert.ok(!/<details[^>]*open/.test(idle), "the block starts open");
  assert.match(idle, /Native diagnostics/, "the block's summary label is missing");

  const done = renderToStaticMarkup(h(I18nProvider, null,
    h(HarnessDiagnosticsBlock, { harness: "claude", state: { status: "done" }, result: { available: true, command: ["claude", "doctor"], lines: ["doctor: healthy"], truncated: false, timedOut: false }, onOpen: () => {} })));
  assert.ok(done.includes("doctor: healthy"), "the loaded output is missing");
  assert.match(done, /claude doctor/, "the run command is not named");

  const timedOut = renderToStaticMarkup(h(I18nProvider, null,
    h(HarnessDiagnosticsBlock, { harness: "claude", state: { status: "done" }, result: { available: true, command: ["claude", "doctor"], lines: [], truncated: false, timedOut: true }, onOpen: () => {} })));
  assert.match(timedOut, /timed out/, "a timed-out run does not say so");

  const unavailable = renderToStaticMarkup(h(I18nProvider, null,
    h(HarnessDiagnosticsBlock, { harness: "pi", state: { status: "done" }, result: { available: false, reason: "pi has no fixed safe diagnostic command yet" }, onOpen: () => {} })));
  assert.match(unavailable, /pi has no fixed safe diagnostic command yet/, "the unavailable reason is not shown");
});
