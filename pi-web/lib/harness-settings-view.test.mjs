// carries: lib/harness-settings-view.ts, components/HarnessEnvironmentSettings.tsx, lib/harness/claude.ts, lib/harness/pi.ts, lib/harness/concepts.ts
// polyforge#149 (#85 85a1b, AC2): the environment pane lists only variable
// names — the adapter payload already carries names only, and this pins that
// nothing a layer holds as a value can reach the view, through the real
// adapters reading a multi-layer fixture (managed/local/project/user claude
// settings layers, pi's automation env). Expected key sets are derived from
// the fixture layers by hand; the sentinel values exist to be found absent.
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
const { envGroup } = await jiti.import("./harness-settings-view.ts");
const { HarnessEnvironmentGroups } = await jiti.import("../components/HarnessEnvironmentSettings.tsx");
const { createClaudeAdapter } = await jiti.import("./harness/claude.ts");
const { createPiAdapter } = await jiti.import("./harness/pi.ts");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const h = React.createElement;
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-harness-env-view-"));
const claudeConfigDir = path.join(scratch, "claude-config");
const project = path.join(scratch, "project");
fs.mkdirSync(path.join(claudeConfigDir, "skills"), { recursive: true });
fs.mkdirSync(path.join(project, ".claude", "skills"), { recursive: true });

// The multi-layer claude fixture: the same variable name in two layers, a
// distinct one per layer, each holding a sentinel value that must never show.
const CLAUDE_ENV_SENTINELS = ["E2E-ENV-USER-A", "E2E-ENV-USER-B", "E2E-ENV-PROJECT-C", "E2E-ENV-LOCAL-A"];
fs.writeFileSync(path.join(claudeConfigDir, "settings.json"), JSON.stringify({
  env: { ANTHROPIC_PROJECT_KEY: CLAUDE_ENV_SENTINELS[0], CLAUDE_ONLY_USER: CLAUDE_ENV_SENTINELS[1] },
}));
fs.writeFileSync(path.join(project, ".claude", "settings.json"), JSON.stringify({
  env: { CLAUDE_PROJECT_ONLY: CLAUDE_ENV_SENTINELS[2] },
}));
fs.writeFileSync(path.join(project, ".claude", "settings.local.json"), JSON.stringify({
  env: { ANTHROPIC_PROJECT_KEY: "E2E-ENV-LOCAL-A" },
}));
fs.writeFileSync(path.join(claudeConfigDir, ".claude.json"), JSON.stringify({ projects: {} }));

// pi's automation env: set through ctx.env, values planted to stay unseen.
const PI_ENV_SENTINELS = ["E2E-ENV-PI-TOKEN", "E2E-ENV-PI-KEY"];
const piEnv = {
  ANTHROPIC_AUTH_TOKEN: PI_ENV_SENTINELS[0],
  GEMINI_API_KEY: PI_ENV_SENTINELS[1],
  ANTHROPIC_API_KEY: "",
};

test.after(() => fs.rmSync(scratch, { recursive: true, force: true }));

test("the environment view lists only variable names across layers", async () => {
  const claude = createClaudeAdapter();
  const payload = await claude.read("env", { claudeConfigDir, cwd: project, env: {} });
  assert.ok(payload, "the claude adapter read no env payload");
  const group = envGroup("claude", payload);
  assert.ok(group, "the view model dropped the payload");
  // The key set is the fixture layers' union, deduped and sorted — derived
  // from the layers by hand, never from the adapter's own output order.
  assert.deepEqual(group.keys, ["ANTHROPIC_PROJECT_KEY", "CLAUDE_ONLY_USER", "CLAUDE_PROJECT_ONLY"]);
  assert.equal(group.target, "claude");
  assert.equal(group.harness, "claude");
  // No layer's value — sentinel or not — reaches the view.
  for (const sentinel of CLAUDE_ENV_SENTINELS) {
    assert.ok(!JSON.stringify(group).includes(sentinel), `the env view leaks the value ${sentinel}`);
  }

  const pi = createPiAdapter();
  const piPayload = await pi.read("env", { env: piEnv });
  const piGroup = envGroup("pi", piPayload);
  assert.ok(piGroup, "the view model dropped pi's payload");
  // Only the set variables list; the empty one does not.
  assert.deepEqual(piGroup.keys, ["ANTHROPIC_AUTH_TOKEN", "GEMINI_API_KEY"]);
  for (const sentinel of PI_ENV_SENTINELS) {
    assert.ok(!JSON.stringify(piGroup).includes(sentinel), `the pi env view leaks the value ${sentinel}`);
  }
});

test("a null payload or malformed keys never reach the view as rows", () => {
  assert.equal(envGroup("claude", null), null);
  assert.deepEqual(envGroup("claude", { target: "claude", setKeys: ["B", "A", "A"] })?.keys, ["A", "B"]);
});

test("a failed harness-list read is a visible reason, not silence or a wrong empty state (r1#8)", () => {
  // 列表本身读失败时必须说明原因（Q4A），不能渲染成空组，也不能说「没有注册」。
  const html = renderToStaticMarkup(h(I18nProvider, null,
    h(HarnessEnvironmentGroups, { groups: [], listError: "HTTP 500" })));
  assert.match(html, /The harness list could not be read: HTTP 500/, "the list read's failure is not visible text");
  assert.match(html, /role="alert"/, "the failure is not an alert");
  assert.ok(!html.includes("No variables set."), "a failed read masquerades as an empty group");
});

test("the environment pane renders the groups, names only, with its note and empty state", () => {
  const groups = [
    { harness: "pi", target: "pi", keys: ["ANTHROPIC_AUTH_TOKEN", "GEMINI_API_KEY"], error: null },
    { harness: "codex", target: "codex", keys: [], error: null },
    { harness: "claude", target: "claude", keys: [], error: "HTTP 500" },
  ];
  const html = renderToStaticMarkup(h(I18nProvider, null, h(HarnessEnvironmentGroups, { groups })));
  assert.match(html, /Only variable names are shown/, "the names-only note is missing");
  for (const name of ["ANTHROPIC_AUTH_TOKEN", "GEMINI_API_KEY"]) {
    assert.ok(html.includes(name), `the pane does not list ${name}`);
  }
  assert.match(html, /data-harness="pi"/, "the groups are not marked per harness");
  assert.match(html, /data-harness="claude"/, "the groups are not marked per harness");
  assert.match(html, /No variables set/, "the empty state is missing");
  assert.match(html, /HTTP 500/, "a failed harness read hides its error");
  // The failure is that harness's alone; the empty group beside it still shows.
  assert.match(html, /data-harness="codex"[\s\S]*?No variables set/, "an empty group beside a failed one lost its empty state");
  // A group's failure and its emptiness are per-harness, never the whole pane's.
  assert.ok(!/E2E-ENV/.test(html), "a value reached the pane's markup");
});
