// carries: components/AccountSettings.tsx, components/SettingsPanel.tsx, lib/settings-navigation.ts
// polyforge#142 (85a2, AC1/AC6/AC7): the account tab's panes — the left
// column's two harnesses, the interactive claude card, the per-Cell rows
// with every field of the ticket's list (状态、来源含「共享 default」、配置根、
// 凭据是否跟随配置目录、步骤能否使用及原因), the keychain hint (AC7) when the
// capability says the credentials do not follow the config root, and the
// credential form (AC6: three kinds, the gateway base URL, the write
// targets defaulting to every Cell, 沿用交互会话的值). Expected labels come
// from the ticket's field list and the locale tables, never from the
// implementation's own output; the sentinel in the fixture must not
// render anywhere.
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
const { AccountClaudeCard, AccountHarnessPane, ClaudeCredentialForm, CredentialWriteResults } = await jiti.import("./AccountSettings.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const h = React.createElement;
const noop = () => {};

const READ = {
  claude: {
    available: true, loggedIn: true, authMethod: "oauth_token", configDirectory: "/home/op/.claude",
    envCredentials: { ANTHROPIC_AUTH_TOKEN: true, ANTHROPIC_BASE_URL: true, ANTHROPIC_API_KEY: false, CLAUDE_CODE_OAUTH_TOKEN: true },
  },
  cells: [
    {
      profile: "default", executionDisabled: null, probeError: null,
      harnessOrder: ["pi", "claude"],
      harnesses: {
        pi: { id: "pi", configRoot: "/h/default/step-agent", credential: "set", status: "ready", authType: "api_key", checkReason: null, source: "/home/op/.pi/agent", sourceKind: "operator", credentialsFollow: true, keychainHint: false, credentialKinds: null, credentialsFile: null, usable: true, reason: null },
        claude: { id: "claude", configRoot: "/h/default/claude-config", credential: "set", status: "ready", authType: null, checkReason: null, source: "/home/op/.pi/agent", sourceKind: "operator", credentialsFollow: true, keychainHint: false, credentialKinds: { oauthToken: false, apiKey: false, gateway: true }, credentialsFile: "absent", usable: true, reason: null },
      },
    },
    {
      profile: "beta", executionDisabled: null, probeError: null,
      harnessOrder: ["pi", "claude"],
      harnesses: {
        pi: { id: "pi", configRoot: "/h/beta/step-agent", credential: "unset", status: "not_ready", authType: null, checkReason: "credentials_not_configured", source: "/home/op/.pi/agent", sourceKind: "shared-default", credentialsFollow: true, keychainHint: false, credentialKinds: null, credentialsFile: null, usable: false, reason: "credentials_not_configured" },
        claude: { id: "claude", configRoot: "/h/beta/claude-config", credential: "unset", status: "not_ready", authType: null, checkReason: null, source: "/home/op/.pi/agent", sourceKind: "shared-default", credentialsFollow: false, keychainHint: true, credentialKinds: null, credentialsFile: "present", usable: true, reason: null },
      },
    },
  ],
  doctorError: null,
};

const CAPS = {
  id: "claude",
  loginCommands: [
    { id: "auth-login", argv: ["claude", "auth", "login"], cell: false },
    { id: "setup-token", argv: ["claude", "setup-token"], cell: false },
  ],
  credentialsFollowConfigRoot: false,
};

function renderPane(harness, caps = null) {
  return renderToStaticMarkup(h(I18nProvider, null, h(AccountHarnessPane, {
    harness, read: READ, capabilities: caps, onOpenLoginTerminal: noop,
  })));
}

test("the claude pane shows the interactive card and both declared login commands (AC1/AC3)", () => {
  const html = renderPane("claude", CAPS);
  assert.match(html, /data-account-pane="claude"/);
  assert.match(html, /data-claude-status="logged-in"/);
  assert.match(html, /oauth_token/, "the auth method is missing");
  assert.match(html, /CLAUDE_CODE_OAUTH_TOKEN/, "the credential key names are missing");
  assert.match(html, /data-login-command="auth-login"/);
  assert.match(html, /data-login-command="setup-token"/);
  assert.match(html, /Log in with Claude \(OAuth\)/);
  assert.ok(!html.includes("sk-SENTINEL"), "a value rendered");
});

test("the Cell rows carry every field of the ticket's list, per Cell (AC1)", () => {
  const html = renderPane("claude", null);
  assert.match(html, /data-account-cell="default"/);
  assert.match(html, /data-account-cell="beta"/);
  // 来源含「共享 default」: the shared-default Cell's row names the kind and the dir
  const beta = html.slice(html.indexOf('data-account-cell="beta"'));
  assert.match(beta, /data-source-kind="shared-default"/);
  assert.match(beta, /shared default/);
  assert.match(beta, /\/home\/op\/\.pi\/agent/);
  // 状态 / 配置根 / 步骤能否使用：the beta claude row is usable through its
  // residual OAuth login (the daemon still uses it) although no env credential
  // is set — and the row says so
  assert.match(beta, /data-account-field="config-root">\/h\/beta\/claude-config</);
  assert.match(beta, /data-account-field="status">not set</);
  assert.match(beta, /data-account-field="usable">yes</);
  // the keychain hint (AC7): the beta row's capability says the credentials
  // do not follow the config root, so the row shows the hint
  assert.match(beta, /data-keychain-hint="true"/);
  assert.match(beta, /Keychain/);
  // the residual OAuth login is marked (Q3)
  assert.match(beta, /Residual OAuth login/);
  // the default row: gateway kinds, operator source, no hint
  const def = html.slice(html.indexOf('data-account-cell="default"'), html.indexOf('data-account-cell="beta"'));
  assert.match(def, /data-source-kind="operator"/);
  assert.ok(!def.includes("data-keychain-hint"), "the following row shows no hint");
});

test("the pi pane shows its rows without the claude-only parts", () => {
  const html = renderPane("pi", { id: "pi", loginCommands: [], credentialsFollowConfigRoot: true });
  assert.match(html, /data-account-pane="pi"/);
  assert.ok(!html.includes("data-login-command"), "pi declares no terminal login (Q4)");
  assert.match(html, /data-account-cell="default"/);
  assert.match(html, /api_key/);
  // the beta pi row carries the unusable verdict and its reason
  const beta = html.slice(html.indexOf('data-account-cell="beta"'));
  assert.match(beta, /data-account-field="usable">no</);
  assert.match(beta, /credentials_not_configured/);
  assert.ok(!html.includes("data-account-card=" + "credential-form"), "the pi pane has no claude form");
});

test("the credential form: three kinds, the gateway base URL, targets defaulting to every Cell, 沿用交互会话的值 (AC6)", () => {
  const bodies = [];
  const html = renderToStaticMarkup(h(I18nProvider, null, h(ClaudeCredentialForm, {
    cells: READ.cells,
    onSave: (body) => bodies.push(body),
  })));
  for (const kind of ["oauth-token", "api-key", "gateway"]) {
    assert.match(html, new RegExp(`value="${kind}"`), `the ${kind} kind is missing`);
  }
  // the gateway kind names its base URL in its label (the field itself renders
  // only once the kind is chosen)
  assert.match(html, /ANTHROPIC_BASE_URL/);
  assert.match(html, /data-account-field="use-interactive"/);
  assert.match(html, /copied server-side/);
  // every target checkbox is on by default: interactive + both Cells
  for (const target of ["interactive", "default", "beta"]) {
    assert.match(html, new RegExp(`data-account-target="${target}" [^>]*checked`), `the ${target} target is not checked by default`);
  }
  assert.match(html, /next Cell build/, "the shared-default note is missing");
});

test("the pi pane renders the per-Cell step guidance of Q4: shared operator directory or the Cell's own source (review r1#5)", () => {
  const html = renderPane("pi", { id: "pi", loginCommands: [], credentialsFollowConfigRoot: true });
  const def = html.slice(html.indexOf('data-account-cell="default"'), html.indexOf('data-account-cell="beta"'));
  assert.match(def, /data-pi-step-guidance="operator"/);
  assert.match(def, /Steps of this Cell run against the shared operator directory \/home\/op\/\.pi\/agent/);
  const beta = html.slice(html.indexOf('data-account-cell="beta"'), html.indexOf('data-account-cell="gamma"'));
  assert.match(beta, /data-pi-step-guidance="shared-default"/);
  assert.match(beta, /shared operator directory \/home\/op\/\.pi\/agent/);
  const gamma = html.slice(html.indexOf('data-account-cell="gamma"'));
  assert.match(gamma, /data-pi-step-guidance="directory"/);
  assert.match(gamma, /its own source directory \/srv\/own/);
});

test("a refused write shows its target and its reason — the reason reaches the user (review r1#10)", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(CredentialWriteResults, {
    results: [
      { target: "interactive", status: "written" },
      { target: "gamma", status: "refused", reason: "settings.json has an unrecognized format (not JSON) — refusing to overwrite it" },
    ],
  })));
  assert.match(html, /interactive/);
  assert.match(html, /gamma/);
  assert.match(html, /unrecognized format/);
  assert.ok(!html.includes("SENTINEL"));
  // the refused target is distinguishable from the written one
  assert.match(html, /data-write-target="gamma"[^>]*data-write-status="refused"/);
});

test("an unavailable claude status degrades with its reason, and the doctor error shows (AC1)", () => {
  const card = renderToStaticMarkup(h(I18nProvider, null, h(AccountClaudeCard, {
    claude: { available: false, loggedIn: null, authMethod: null, configDirectory: null, reason: "claude was not found on PATH SENTINEL-FREE" },
  })));
  assert.match(card, /data-claude-status="unavailable"/);
  assert.match(card, /claude(?:&#x27;|')s status is unavailable/);
  const withError = renderToStaticMarkup(h(I18nProvider, null, h(AccountHarnessPane, {
    harness: "pi", capabilities: null, onOpenLoginTerminal: noop,
    read: { claude: null, cells: [], doctorError: "pf doctor did not answer in time" },
  })));
  assert.match(withError, /The Cell facts could not be read: pf doctor did not answer in time/);
  assert.ok(!withError.includes("No Cell is configured"), "a failed read masquerades as an empty Cell list");
  const empty = renderToStaticMarkup(h(I18nProvider, null, h(AccountHarnessPane, {
    harness: "pi", capabilities: null, onOpenLoginTerminal: noop,
    read: { claude: null, cells: [], doctorError: null },
  })));
  assert.match(empty, /No Cell is configured on this machine/);
});
