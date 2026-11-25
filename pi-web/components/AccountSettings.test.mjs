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
const { AccountClaudeCard, AccountHarnessPane, AccountReadStatus, ClaudeCredentialForm, CredentialWriteResults } = await jiti.import("./AccountSettings.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const h = React.createElement;
const noop = () => {};

const READ = {
  claude: {
    available: true, loggedIn: true, authMethod: "oauth_token", configDirectory: "/home/op/.claude", consoleProfile: "absent",
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
    {
      profile: "gamma", executionDisabled: null, probeError: null,
      harnessOrder: ["pi", "claude"],
      harnesses: {
        pi: { id: "pi", configRoot: "/h/gamma/step-agent", credential: "set", status: "ready", authType: "api_key", checkReason: null, source: "/srv/own", sourceKind: "directory", credentialsFollow: true, keychainHint: false, credentialKinds: null, credentialsFile: null, usable: true, reason: null },
        claude: { id: "claude", configRoot: "/h/gamma/claude-config", credential: "set", status: "ready", authType: null, checkReason: null, source: "/srv/own", sourceKind: "directory", credentialsFollow: true, keychainHint: false, credentialKinds: { oauthToken: true, apiKey: false, gateway: false }, credentialsFile: "absent", usable: true, reason: null },
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
  assert.match(def, /Steps of this Cell run against the shared operator directory \(\/home\/op\/\.pi\/agent/);
  const beta = html.slice(html.indexOf('data-account-cell="beta"'), html.indexOf('data-account-cell="gamma"'));
  assert.match(beta, /data-pi-step-guidance="shared-default"/);
  assert.match(beta, /shared operator directory \(\/home\/op\/\.pi\/agent/);
  const gamma = html.slice(html.indexOf('data-account-cell="gamma"'));
  assert.match(gamma, /data-pi-step-guidance="directory"/);
  assert.match(gamma, /its own source directory \(\/srv\/own/);
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

test("a present Console profile is marked on the card, and a claude row without env credentials carries the fallback hint (review r1#6)", () => {
  const card = renderToStaticMarkup(h(I18nProvider, null, h(AccountClaudeCard, {
    claude: { available: true, loggedIn: true, authMethod: "oauth_token", configDirectory: "/home/op/.claude",
      envCredentials: {}, consoleProfile: "present" },
  })));
  assert.match(card, /Console profile/);
  assert.match(card, /not isolated by CLAUDE_CONFIG_DIR/);
  const absent = renderToStaticMarkup(h(I18nProvider, null, h(AccountClaudeCard, {
    claude: { available: true, loggedIn: true, authMethod: "oauth_token", configDirectory: "/home/op/.claude",
      envCredentials: {}, consoleProfile: "absent" },
  })));
  assert.ok(!absent.includes("Console profile"), "an absent Console profile is not a mark");
  // the claude pane's rows: a row without env credentials carries the
  // fallback hint when the profile is present
  const withProfile = renderToStaticMarkup(h(I18nProvider, null, h(AccountHarnessPane, {
    harness: "claude", capabilities: null, onOpenLoginTerminal: noop, onSaveCredentials: noop,
    read: { ...READ, claude: { ...READ.claude, consoleProfile: "present" } },
  })));
  const beta = withProfile.slice(withProfile.indexOf('data-account-cell="beta"'));
  assert.match(beta, /data-console-fallback="true"/);
  assert.match(beta, /may fall back to it/);
  const withoutProfile = renderToStaticMarkup(h(I18nProvider, null, h(AccountHarnessPane, {
    harness: "claude", capabilities: null, onOpenLoginTerminal: noop, onSaveCredentials: noop,
    read: READ,
  })));
  assert.ok(!withoutProfile.includes("data-console-fallback"), "no hint without the Console profile");
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

// polyforge#256 (T1, B3): the account read's state machine — an explicit
// pending state on the first read and on refresh, the last good data kept
// across a refresh, a visible error the refresh retries, and a late answer
// that never overwrites a newer one. The fetches are controlled promises
// (resolved by the test), never a real 40 s wait; the hook is driven through
// the same hook shim the other component behaviour tests use.
test("account pending, success, empty, failure and refresh are distinct", async () => {
  const shim = createJiti(import.meta.url, {
    jsx: { runtime: "automatic" },
    moduleCache: false,
    // polyforge#259 T4 AC2: the panel imports the display-name mapping through
    // the package's own `@/` alias, which the shim resolves like tsconfig does.
    tsconfigPaths: true,
    alias: {
      react: new URL("../hooks/__fixtures__/react-hook-shim.mjs", import.meta.url).pathname,
      "react/jsx-runtime": new URL("../hooks/__fixtures__/react-jsx-runtime.mjs", import.meta.url).pathname,
      "@/hooks/useI18n": new URL("../hooks/__fixtures__/polyforge-tasks-stubs.mjs", import.meta.url).pathname,
      "./SettingsUi": new URL("../hooks/__fixtures__/polyforge-tasks-stubs.mjs", import.meta.url).pathname,
      "./TerminalPanel": new URL("../hooks/__fixtures__/polyforge-tasks-stubs.mjs", import.meta.url).pathname,
      "./PiAccountLogin": new URL("../hooks/__fixtures__/polyforge-tasks-stubs.mjs", import.meta.url).pathname,
    },
  });
  const { renderHook } = await import(new URL("../hooks/__fixtures__/react-hook-shim.mjs", import.meta.url).href);
  const { useAccountRead } = await shim.import("./AccountSettings.tsx");

  const pending = [];
  globalThis.fetch = (url) => new Promise((resolve) => pending.push({ url, resolve }));
  const take = (url) => {
    const at = pending.findIndex((entry) => entry.url === url);
    assert.ok(at >= 0, `no pending ${url} (pending: ${pending.map((entry) => entry.url).join(", ")})`);
    const [entry] = pending.splice(at, 1);
    return entry;
  };
  const ok = (body) => ({ ok: true, status: 200, json: async () => body });
  const settle = async (hook) => {
    for (let round = 0; round < 16; round++) {
      await new Promise((resolve) => setImmediate(resolve));
      hook.flush();
    }
  };
  const answer = async (hook, body, harnesses = []) => {
    take("/api/settings/account").resolve(ok(body));
    await settle(hook);
    take("/api/harness").resolve(ok({ harnesses }));
    await settle(hook);
  };

  const hook = renderHook(useAccountRead, {});
  assert.equal(hook.result.loading, true, "the first read starts pending");
  assert.equal(hook.result.read, null);

  // success: the data replaces the pending state
  await answer(hook, READ);
  assert.equal(hook.result.loading, false);
  assert.equal(hook.result.readError, null);
  assert.equal(hook.result.read.cells.length, 3, "the read's Cell rows are shown");

  // refresh: pending again, the last good read kept
  hook.result.load();
  await settle(hook);
  assert.equal(hook.result.loading, true, "a refresh shows its own pending state");
  assert.equal(hook.result.read.cells.length, 3, "the last good read stays while refreshing");
  await answer(hook, { ...READ, cells: [], doctorError: null });
  assert.equal(hook.result.loading, false);
  assert.deepEqual(hook.result.read.cells, [], "the refreshed (empty) read replaces the old data");

  // failure: the pending state ends, an error is visible, the data is kept
  hook.result.load();
  await settle(hook);
  take("/api/settings/account").resolve({ ok: false, status: 500, json: async () => ({}) });
  await settle(hook);
  assert.equal(hook.result.loading, false, "a failed read never stays pending");
  assert.equal(hook.result.readError, "settings.accountReadFailed");
  assert.deepEqual(hook.result.read.cells, [], "the last good read is kept through a failure");

  // a late older answer never overwrites a newer one
  hook.result.load(); // request A
  await settle(hook);
  hook.result.load(); // request B, newer
  await settle(hook);
  const [first, second] = pending.filter((entry) => entry.url === "/api/settings/account");
  second.resolve(ok({ ...READ, cells: [] }));
  await settle(hook);
  take("/api/harness").resolve(ok({ harnesses: [] }));
  await settle(hook);
  first.resolve(ok(READ));
  await settle(hook);
  assert.deepEqual(hook.result.read.cells, [], "the newer read wins; the late older answer is dropped");
  hook.unmount();
});

test("B3: the shell renders the pending status and the retry line, and a finished read without claude is its own explanation", () => {
  const t = (key) => key;
  const loading = renderToStaticMarkup(h(I18nProvider, null, h(AccountReadStatus, { loading: true, readError: null, t })));
  assert.match(loading, /role="status"/, "the first read is a live pending status");
  assert.match(loading, /data-account-loading/);
  assert.ok(loading.includes("settings.accountLoading"), "the pending text is the localized key");

  const failed = renderToStaticMarkup(h(I18nProvider, null, h(AccountReadStatus, { loading: false, readError: "settings.accountReadFailed", t })));
  assert.match(failed, /role="alert"/, "a failed read is announced");
  assert.match(failed, /data-account-read-error/);
  assert.ok(failed.includes("settings.accountReadFailed"));
  assert.ok(!failed.includes("data-account-loading"), "a failed read is never still pending");

  const idle = renderToStaticMarkup(h(I18nProvider, null, h(AccountReadStatus, { loading: false, readError: null, t })));
  assert.equal(idle, "", "a settled, successful read has no status line");

  // the claude pane's own state: loading while in flight, absent once the read finished without claude
  const pendingPane = renderToStaticMarkup(h(I18nProvider, null, h(AccountHarnessPane, {
    harness: "claude", read: null, capabilities: null, onOpenLoginTerminal: noop, loading: true,
  })));
  assert.match(pendingPane, /data-claude-status="loading"/, "a read still in flight says it is loading");
  assert.match(pendingPane, /Reading the account facts/, "the loading text is localized");
  const failedPane = renderToStaticMarkup(h(I18nProvider, null, h(AccountHarnessPane, {
    harness: "claude", read: null, capabilities: null, onOpenLoginTerminal: noop, loading: false,
  })));
  assert.match(failedPane, /data-claude-status="absent"/, "a finished read without claude is not still loading");
  assert.ok(!failedPane.includes("Reading the account facts"), "the failed read never claims to be reading");
  assert.match(failedPane, /read finished/, "the finished-but-unavailable explanation is shown");

  // a read that answered claude keeps the data, regardless of the loading flag
  const withClaude = renderToStaticMarkup(h(I18nProvider, null, h(AccountClaudeCard, {
    claude: { available: true, loggedIn: true, authMethod: "oauth_token", configDirectory: "/home/op/.claude", consoleProfile: "absent", envCredentials: {} },
    loaded: true,
  })));
  assert.match(withClaude, /data-claude-status="logged-in"/);
});

// polyforge#259 T4 AC2 (review r3#1, owner 2A: 前端按 code i18n): a refused
// target's row on a Chinese page reads 「拒写」 plus the line its route code
// names; the server's English reason sits only in the 详情 fold. Expected
// texts hand-copied from lib/i18n/messages/zh-CN.ts (settings.accountWriteNo,
// serverError.forbidden, i18n.detailsLabel). Caught change: `entry.reason`
// back outside the fold, or the line not taken from the code.
test("the refusal's own words stay folded, the row reads in Chinese by its code (T4 AC2, review r3#1)", async () => {
  const { zhCNLocale } = await jiti.import("@/lib/i18n/messages/zh-CN.ts");
  const { translateMessage } = await jiti.import("@/lib/i18n/format.ts");
  const t = (key, params) => translateMessage("zh-CN", key, { "zh-CN": zhCNLocale.messages }, params);
  const html = renderToStaticMarkup(h(I18nProvider, null, h(CredentialWriteResults, {
    t,
    results: [{ target: "auth.json", status: "refused", reason: "target file is not writable", code: "account-write-refused" }],
  })));
  const outside = html.replace(/<details[\s\S]*?<\/details>/g, "");
  const folds = (html.match(/<details[\s\S]*?<\/details>/g) ?? []).join("");
  assert.ok(!outside.includes("target file is not writable"), "the refusal's English reason shows outside the fold");
  assert.ok(folds.includes("target file is not writable"), "the refusal's English reason is not in the fold");
  assert.ok(folds.includes("详情"), "the fold is not labelled 详情");
  assert.ok(outside.includes("拒写"), "the refused status line is missing");
  assert.ok(outside.includes("服务端拒绝了这次改动，原文在「详情」里。"), "the row does not read by its code");
});

// polyforge#271 AC1 (A1, Q5 A): a first read with no value shows the shared
// skeleton with a readable loading label; a failed read is a styled alert
// notice with a retry action (never the empty state), and a refresh failure
// keeps the last good read and says it is stale; a successful empty Cell list
// offers its refresh action.
test("account states: skeleton, empty action, alert notice and stale data", () => {
  const t = (key, params) => params ? `${key} ${JSON.stringify(params)}` : key;

  const pending = renderToStaticMarkup(h(I18nProvider, null, h(AccountReadStatus, {
    loading: true, readError: null, hasData: false, t, onRetry: noop,
  })));
  assert.match(pending, /data-account-loading/);
  assert.match(pending, /data-config-skeleton/, "the first read is a shared skeleton");
  assert.match(pending, /role="status"/);
  assert.ok(pending.includes("settings.accountLoading"), "the loading label stays readable");
  assert.ok(!pending.includes("settings.accountNoCells"), "a pending read is never the empty state");

  const failed = renderToStaticMarkup(h(I18nProvider, null, h(AccountReadStatus, {
    loading: false, readError: "settings.accountReadFailed", hasData: false, t, onRetry: noop,
  })));
  assert.match(failed, /role="alert"/, "a failed read is announced");
  assert.match(failed, /data-account-read-error/);
  assert.ok(failed.includes("settings.accountReadFailed"));
  assert.ok(failed.includes(">i18n.retry<"), "the error carries the retry action");
  assert.ok(!failed.includes("settings.accountNoCells"), "a failed read is never the empty state");

  const stale = renderToStaticMarkup(h(I18nProvider, null, h(AccountReadStatus, {
    loading: false, readError: "settings.accountReadFailed", hasData: true, t, onRetry: noop,
  })));
  assert.ok(stale.includes("settings.accountStale"), "a refresh failure says the data is the last good read");
  assert.match(stale, /role="alert"/);

  const emptyCells = renderToStaticMarkup(h(I18nProvider, null, h(AccountHarnessPane, {
    harness: "pi", capabilities: null, onOpenLoginTerminal: noop, onRefresh: noop,
    read: { claude: null, cells: [], doctorError: null },
  })));
  assert.match(emptyCells, /No Cell is configured on this machine/);
  assert.match(emptyCells, /data-account-action="refresh-cells"/, "the empty Cell list offers its refresh action");

  const claudePending = renderToStaticMarkup(h(I18nProvider, null, h(AccountClaudeCard, { claude: null, loaded: false })));
  assert.match(claudePending, /data-claude-status="loading"/);
  assert.match(claudePending, /data-config-skeleton/, "the claude card's first read is a skeleton too");
});

// polyforge#271 round 2 (review r2#5): the retry control is wired to the
// page's read again — clicking it calls the retry the page passed in.
test("account retry control issues the read again", () => {
  const t = (key) => key;
  let retries = 0;
  const tree = AccountReadStatus({ loading: false, readError: "settings.accountReadFailed", hasData: false, t, onRetry: () => { retries += 1; } });
  const walk = (node, out = []) => {
    if (Array.isArray(node)) { for (const child of node) walk(child, out); return out; }
    if (!node || typeof node !== "object" || !("props" in node)) return out;
    if (typeof node.type === "function") return walk(node.type(node.props), out);
    out.push(node);
    walk(node.props.children, out);
    return out;
  };
  const retry = walk(tree).find((node) => node.props?.["data-account-retry"] !== undefined);
  assert.ok(retry, "the retry control is rendered");
  retry.props.onClick();
  assert.equal(retries, 1, "the retry control did not call the read again");
});
