// carries: components/HarnessPermissionSettings*, lib/harness-permissions-view*, components/SettingsPanel*, lib/i18n/messages/*
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// polyforge#150 (85a4) AC1/AC2/AC4: the permission page's model — Q1 A's
// unified approval mapping (询问→default / 自动审查→auto / 不询问即拒绝→
// dontAsk), the unified blocks' per-harness values with pi's own 不做审批 /
// 无沙箱 notes, the trust unify target (统一成当前值 = the first ready
// harness's value written to every differing one), the rules text format
// and the managed read-only rule (Q3 B) — and the page's place: mounted as
// the permission section (the placeholder gone), its panes named by the
// locale tables. Expected values are hand-derived from the ticket's settled
// rulings, never from the implementation's own output.
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const view = await jiti.import("../lib/harness-permissions-view.ts");
const { HarnessPermissionSettings } = await jiti.import("./HarnessPermissionSettings.tsx");
const { SettingsPanel } = await jiti.import("./SettingsPanel.tsx");
const { ClaudeCodeReport } = await jiti.import("./ClaudeCodeReport.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const h = React.createElement;
const noop = () => {};

test("Q1 A: the unified approval values map onto claude's native modes", () => {
  assert.deepEqual(view.UNIFIED_TO_CLAUDE_MODE, {
    ask: "default",
    auto: "auto",
    "deny-unprompted": "dontAsk",
  });
  // A neutral mode outside the unified three (claude's own extra values) maps to null.
  assert.equal(view.unifiedApprovalOf("accept-edits"), null);
  assert.equal(view.unifiedApprovalOf("plan"), null);
  assert.equal(view.unifiedApprovalOf("full"), null);
  assert.equal(view.unifiedApprovalOf("ask"), "ask");
  assert.equal(view.unifiedApprovalOf("auto"), "auto");
  assert.equal(view.unifiedApprovalOf("deny-unprompted"), "deny-unprompted");
  assert.equal(view.unifiedApprovalOf(null), null);
});

test("the unified block's values: pi's own note where it does not support the field, the ready value where it does", () => {
  const approval = view.unifiedValues("approval-mode", {
    field: "approval-mode",
    status: "unsupported",
    values: {
      pi: { supported: false, neutral: null, raw: null },
      claude: { supported: true, neutral: "auto", raw: "auto" },
    },
  }, { pi: "pi does no approval prompting", claude: "" });
  assert.deepEqual(approval, [
    { harness: "pi", status: "unsupported", value: null, reason: "pi does no approval prompting" },
    { harness: "claude", status: "ready", value: "auto" },
  ]);
  const sandbox = view.unifiedValues("sandbox", {
    field: "sandbox",
    status: "unsupported",
    values: {
      pi: { supported: false, neutral: null, raw: null },
      claude: { supported: true, neutral: "on", raw: "on" },
    },
  }, { pi: "pi has no sandbox", claude: "" });
  assert.deepEqual(sandbox[0], { harness: "pi", status: "unsupported", value: null, reason: "pi has no sandbox" });
  assert.deepEqual(sandbox[1], { harness: "claude", status: "ready", value: "on" });
  assert.deepEqual(view.unifiedValues("sandbox", null, { pi: "", claude: "" }), []);
});

// polyforge#256 (T1, B6): a supported-but-unset sandbox is 「未设置」, never the
// 「没有已安装的 harness 支持此项」 state; the UnifiedSetting protocol is
// unchanged (status stays `ready`, the display carries the localized text),
// and pi's own unsupported note stays exactly as it was.
test("supported unset sandbox is not unsupported: ready with a localized 未设置, pi still unsupported", async () => {
  const unsetCompare = {
    field: "sandbox",
    status: "inconsistent",
    values: {
      pi: { supported: false, neutral: null, raw: null },
      claude: { supported: true, neutral: null, raw: null },
    },
  };
  const sandbox = view.unifiedValues("sandbox", unsetCompare, { pi: "pi has no sandbox", claude: "", unset: "Not set" });
  assert.deepEqual(sandbox, [
    { harness: "pi", status: "unsupported", value: null, reason: "pi has no sandbox" },
    { harness: "claude", status: "ready", value: "Not set" },
  ], "claude's unset sandbox is ready with 「未设置」, not unsupported");
  const { unifiedStatus } = await jiti.import("./UnifiedSetting.tsx");
  assert.equal(unifiedStatus(sandbox), "inconsistent", "the page must not claim no harness supports the sandbox");
  // a supported and set sandbox still shows its own value, never the unset text
  const setCompare = { ...unsetCompare, values: { ...unsetCompare.values, claude: { supported: true, neutral: "on", raw: "on" } } };
  assert.equal(view.unifiedValues("sandbox", setCompare, { pi: "pi has no sandbox", claude: "", unset: "Not set" })[1].value, "on");
  // pi alone unsupported (no claude support at all) stays unsupported
  const noneCompare = { ...unsetCompare, values: { pi: { supported: false, neutral: null, raw: null }, claude: { supported: false, neutral: null, raw: null } } };
  assert.equal(unifiedStatus(view.unifiedValues("sandbox", noneCompare, { pi: "pi has no sandbox", claude: "no claude", unset: "Not set" })), "unsupported");
  // without the unset text the helper keeps the old protocol shape (null)
  assert.equal(view.unifiedValues("sandbox", unsetCompare, { pi: "pi has no sandbox", claude: "" })[1].value, null);
  // the page passes the localized unset text to the sandbox projection only
  const source = await jiti.import("node:fs/promises").then((fs) => fs.readFile(new URL("./HarnessPermissionSettings.tsx", import.meta.url), "utf8"));
  assert.match(source, /values=\{unifiedValues\("sandbox", sandboxCompare, \{/, "the sandbox block reads the projection");
  assert.match(source, /unset: t\("settings\.permissionSandboxUnset"\)/, "the sandbox block passes the localized unset text");
  assert.match(source, /onChange=\{\(enabled\) => void saveClaude\(\{ sandboxEnabled: enabled \}\)\}/, "the switch writes only on the user's change");
});

test("the trust unify target: every differing harness set to the first ready value, null when they agree or none answers", () => {
  // pi trusted, claude not: claude is the target, written to pi's value.
  assert.deepEqual(view.trustUnifyTargets({
    pi: { approvalMode: { supported: false, neutral: null, raw: null }, projectTrust: { trusted: true }, sandbox: { supported: false, enabled: null, autoAllowBashIfSandboxed: null, otherKeys: [] }, rules: null, denyInsteadOfPrompting: null, sources: { defaultMode: null, sandbox: null, rules: null, denyInsteadOfPrompting: null } },
    claude: { approvalMode: { supported: true, neutral: null, raw: null }, projectTrust: { trusted: false }, sandbox: { supported: true, enabled: null, autoAllowBashIfSandboxed: null, otherKeys: [] }, rules: null, denyInsteadOfPrompting: null, sources: { defaultMode: null, sandbox: null, rules: null, denyInsteadOfPrompting: null } },
  }), { trusted: true, harnesses: ["claude"] });
  // Both untrusted: nothing to unify.
  assert.equal(view.trustUnifyTargets({
    pi: { approvalMode: { supported: false, neutral: null, raw: null }, projectTrust: { trusted: false }, sandbox: { supported: false, enabled: null, autoAllowBashIfSandboxed: null, otherKeys: [] }, rules: null, denyInsteadOfPrompting: null, sources: { defaultMode: null, sandbox: null, rules: null, denyInsteadOfPrompting: null } },
    claude: { approvalMode: { supported: true, neutral: null, raw: null }, projectTrust: { trusted: false }, sandbox: { supported: true, enabled: null, autoAllowBashIfSandboxed: null, otherKeys: [] }, rules: null, denyInsteadOfPrompting: null, sources: { defaultMode: null, sandbox: null, rules: null, denyInsteadOfPrompting: null } },
  }), null);
  // No project: no harness answers, nothing to unify.
  assert.equal(view.trustUnifyTargets({ pi: null, claude: null }), null);
});

test("the rules text: one rule per line, trimmed, duplicates once; an empty text is an empty list", () => {
  assert.equal(view.rulesToText(["Bash(git:*)", "WebSearch"]), "Bash(git:*)\nWebSearch");
  assert.deepEqual(view.rulesFromText("  Bash(git:*) \n\nWebSearch\nBash(git:*)\n"), ["Bash(git:*)", "WebSearch"]);
  assert.deepEqual(view.rulesFromText(""), []);
  assert.deepEqual(view.rulesFromText("\n  \n"), []);
});

test("a managed source is read-only; a project layer and no layer at all are not (Q3 B)", () => {
  assert.equal(view.sourceIsReadOnly({ scope: "managed", path: "/etc/claude-code/managed-settings.json" }), true);
  assert.equal(view.sourceIsReadOnly({ scope: "project", path: "/p/.claude/settings.json" }), false);
  assert.equal(view.sourceIsReadOnly(null), false);
  assert.equal(view.sourceIsReadOnly({ scope: "user", path: "/u/settings.json" }), false);
});

test("the permission section hosts the page and the 85a1b placeholder is gone (AC4)", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(SettingsPanel, {
    cwd: null,
    sessionId: null,
    initialSection: "permission",
    onClose: noop,
    onSessionReloaded: noop,
    quoteSelectionEnabled: false,
    onQuoteSelectionChange: noop,
  })));
  // The permission section host renders the page's own panes, not the placeholder.
  const host = html.slice(html.indexOf('data-settings-section="permission"'));
  assert.ok(html.includes('data-permission-pane="approval"'), "the permission section does not render the page's approval pane");
  assert.ok(host.includes(">Approval mode<"), "the permission section does not show the page's left column");
  assert.ok(!/Placeholder/i.test(host), `the 85a1b placeholder still renders: ${host.slice(0, 200)}`);
});

test("the page renders the four panes of the ticket's settled left column and the unified approval values", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(HarnessPermissionSettings, { cwd: null, sessionId: null })));
  const items = [...html.matchAll(/class="config-sidebar-item[^"]*"[^>]*>(?:(?!<\/button>).)*?<span class="config-sidebar-text">([^<]*)<\/span>/gs)].map((match) => match[1]);
  assert.deepEqual(items, ["Approval mode", "Sandbox", "Project trust", "Rules"], `the left column (got: ${items.join(", ")})`);
  // The default pane is the approval mode, and only the chosen pane is mounted.
  const panes = [...html.matchAll(/data-permission-pane="([^"]*)"/g)].map((match) => match[1]);
  assert.deepEqual(panes, ["approval"]);
  // The unified three render with their settled words (Q1 A's values).
  for (const word of ["Ask", "Auto review", "Deny without asking"]) {
    assert.ok(html.includes(word), `the unified approval value "${word}" is missing`);
  }
});

test("the Claude Code report block renders claude's version, capabilities and the three features (owner ruling: the retired page's block is taken over)", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(ClaudeCodeReport, {
    report: {
      available: true,
      version: "2.1.322",
      versionRecorded: true,
      recordedVersions: ["2.1.322"],
      capabilities: ["ui_surface_v1"],
      features: [
        { id: "approvals", available: true, basis: "host-prompts" },
        { id: "backgroundTasks", available: true, basis: "recorded" },
        { id: "subagentText", available: null, basis: "version-unknown" },
      ],
    },
    error: null,
  })));
  assert.ok(html.includes("2.1.322"), "the version is missing");
  assert.ok(html.includes("ui_surface_v1"), "claude's own capabilities are not listed verbatim");
  for (const word of ["Approvals", "Background tasks", "Subagent text forwarding", "Available", "Unknown"]) {
    assert.ok(html.includes(word), `the feature table is missing "${word}"`);
  }
  // A version outside the fixtures answers its visible notice.
  const unrecorded = renderToStaticMarkup(h(I18nProvider, null, h(ClaudeCodeReport, {
    report: { available: true, version: "9.9.9", versionRecorded: false, recordedVersions: ["2.1.322"], capabilities: [], features: [] },
    error: null,
  })));
  assert.ok(unrecorded.includes("9.9.9 is not covered"), "the not-recorded notice is missing");
  // A failed read is its own visible line, never silence.
  const failed = renderToStaticMarkup(h(I18nProvider, null, h(ClaudeCodeReport, { report: null, error: "HTTP 500" })));
  assert.ok(failed.includes("HTTP 500"), "the failed read is not shown");
});

// polyforge#259 T4 AC2 (u3, review r3#1): claude's own approval modes read by
// their display names, the raw mode id only as the second line. Expected names
// hand-copied from lib/i18n/messages/en.ts (settings.permissionMode*), the ids
// from claude's modes (lib/harness/concepts.ts). Caught change: an option whose
// main text is the raw id again (e.g. `acceptEdits`), or one losing its id line.
test("claude's approval modes read by their display names, the raw id only on the second line (T4 AC2, review r3#1)", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(HarnessPermissionSettings, { cwd: null, sessionId: null })))
    .replace(/&#x27;/g, "'").replace(/&quot;/g, '"').replace(/&amp;/g, "&");
  const zone = html.slice(html.indexOf('data-permission-zone="claude"'));
  assert.ok(html.includes('data-permission-zone="claude"'), "the page renders no Claude zone");
  const group = zone.match(/<div role="radiogroup" aria-label="[^"]*" class="settings-agent-language-options">([\s\S]*?)<\/div>/)?.[1];
  assert.ok(group, "the page renders no radio group for claude's modes");
  const options = [...group.matchAll(/<button[^>]*role="radio"[^>]*>([\s\S]*?)<\/button>/g)].map((match) => match[1]);
  const lines = options.map((option) => {
    const stack = option.match(/<span class="settings-name-stack"><span>([^<]*)<\/span><code class="settings-secondary-id">([^<]*)<\/code><\/span>/);
    return stack ? { name: stack[1], id: stack[2] } : { unstacked: option.replace(/<[^>]+>/g, "") };
  });
  assert.deepEqual(lines, [
    { name: "Default (ask each time)", id: "default" },
    { name: "Accept edits", id: "acceptEdits" },
    { name: "Plan mode (read-only)", id: "plan" },
    { name: "Auto review", id: "auto" },
    { name: "Don't ask, deny", id: "dontAsk" },
    { name: "Bypass permissions", id: "bypassPermissions" },
  ]);
  // `acceptEdits` appears once in the group, and that once is the id line.
  assert.equal(group.split("acceptEdits").length - 1, 1, "acceptEdits shows outside its id line");
  assert.match(group, /<code class="settings-secondary-id">acceptEdits<\/code>/);
});
