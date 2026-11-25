// carries: lib/settings-display-names.ts
// polyforge#259 (T4 AC2): 原始 id 只作次行，主文字是显示名。期望值来自工单
// 「原始 id（acceptEdits、denyInsteadOfPrompting、provider / model id、安装
// 路径）只能作次行等宽灰字，主文字必须是显示名」——一张手写的 id → 显示名对照
// 表，不取自实现的输出；未知 id 不得变成空串（回落到 id 本身）。
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

const {
  claudeApprovalModeName,
  accountTargetName,
  pluginRowName,
  DENY_INSTEAD_OF_PROMPTING,
} = await import("./settings-display-names.ts");

test("every claude approval mode has a display name and keeps its raw id for the second line", () => {
  // claude's own modes (lib/harness/concepts.ts) — the six the permission page offers.
  const modes = ["default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"];
  const keys = new Set();
  for (const mode of modes) {
    const name = claudeApprovalModeName(mode);
    assert.ok(name.labelKey, `${mode} has no display name`);
    assert.equal(name.id, mode, `${mode}'s raw id is not kept for the second line`);
    keys.add(name.labelKey);
  }
  assert.equal(keys.size, modes.length, "two modes share one display name, so the choice is ambiguous");
});

test("an unknown approval mode keeps its id instead of rendering an empty name", () => {
  const name = claudeApprovalModeName("futureMode");
  assert.equal(name.labelKey, null, "an unknown mode claims a translation it does not have");
  assert.equal(name.id, "futureMode");
});

test("denyInsteadOfPrompting has a display name and keeps its raw key", () => {
  assert.ok(DENY_INSTEAD_OF_PROMPTING.labelKey, "the switch shows the raw key as its label");
  assert.equal(DENY_INSTEAD_OF_PROMPTING.id, "denyInsteadOfPrompting");
});

test("an account credential target reads as 交互会话 or Cell <profile>, never the raw profile alone", () => {
  const interactive = accountTargetName("interactive");
  assert.ok(interactive.labelKey, "interactive has no display name");
  assert.equal(interactive.id, "interactive");

  const cell = accountTargetName("default");
  assert.ok(cell.labelKey, "a Cell profile has no display name");
  assert.deepEqual(cell.params, { name: "default" }, "the Cell's own name is not passed to its display name");
  assert.equal(cell.id, "default");
  assert.notEqual(cell.labelKey, interactive.labelKey, "a Cell and the interactive session share one display name");
});

test("a plugin row leads with its package name, never the whole install path", () => {
  assert.equal(pluginRowName({ packageName: "polyforge", source: "/opt/polyforge" }), "polyforge");
  assert.equal(pluginRowName({ packageName: "polyforge", source: "npm:polyforge" }), "polyforge");
  assert.equal(pluginRowName({ packageName: null, source: "/root/.local/share/polyforge/release/current" }), "current");
  assert.equal(pluginRowName({ packageName: null, source: "npm:@scope/pi-plugin" }), "@scope/pi-plugin");
  assert.equal(pluginRowName({ source: "/srv/plugins/" }), "plugins", "a trailing separator leaves an empty name");
});