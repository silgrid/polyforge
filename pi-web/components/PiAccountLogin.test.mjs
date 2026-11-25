// carries: components/PiAccountLogin.tsx, app/api/auth/providers/route.ts, lib/provider-listing.ts
// polyforge#142 (85a2, AC4/AC8): pi's sign-in lives only on Account › pi —
// the moved login flows render their buttons, the stored-but-never-offered
// Anthropic subscription shows status and logout alone (never a login), the
// API-key entry stays for Anthropic, and the picker offers only what the
// (filtered) providers route answers. Expected labels come from the i18n
// tables and the ticket's Q10 ruling, never from the implementation.
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
const { AddProviderPicker, ApiKeyDetail, OAuthDetail, PiAccountLogin } = await jiti.import("./PiAccountLogin.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const h = React.createElement;
const noop = () => {};

test("the pi pane is the one place to sign in: the rows and the Add provider button (AC4)", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(PiAccountLogin)));
  assert.match(html, /data-pi-account-login="true"/);
  assert.match(html, /Add provider/, "the Add provider entry is missing from the account pane");
});

test("a stored Anthropic subscription shows status and logout alone — never a login or a re-login (AC8)", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(OAuthDetail, {
    provider: { id: "anthropic", name: "Anthropic (Claude Pro/Max)", usesCallbackServer: false, loggedIn: true },
    onRefresh: noop,
  })));
  assert.match(html, /Connected/, "the stored credential's status is missing");
  assert.match(html, /Disconnect/, "the logout is missing");
  // no login-class button of any label: the buttons are counted, and the
  // only one may be Disconnect (the login button's label is "Re-login"
  // once logged in — the old assertion on "Login" never saw it)
  const buttons = [...html.matchAll(/<button[^>]*>([^<]*)<\/button>/g)].map((m) => m[1]);
  assert.deepEqual(buttons.filter((label) => label !== "Disconnect"), [], `a login-class button is offered for the stored subscription: ${JSON.stringify(buttons)}`);
  assert.ok(buttons.includes("Disconnect"), "the logout is missing");
});

test("an offered provider keeps its login button; the API-key entry stays for Anthropic (AC4/AC8)", () => {
  const login = renderToStaticMarkup(h(I18nProvider, null, h(OAuthDetail, {
    provider: { id: "openai-codex", name: "ChatGPT Plus/Pro", usesCallbackServer: false, loggedIn: false },
    onRefresh: noop,
  })));
  assert.match(login, /Login/, "an offered provider lost its login button");
  const key = renderToStaticMarkup(h(I18nProvider, null, h(ApiKeyDetail, {
    provider: { id: "anthropic", displayName: "Anthropic", configured: false, modelCount: 12 },
    onRefresh: noop,
  })));
  assert.match(key, /type="password"/, "the API-key entry lost its input");
  assert.match(key, /Anthropic/);
});

test("the picker offers what the filtered route answers — no custom entry, no never-offered login", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(AddProviderPicker, {
    oauthProviders: [
      { id: "openai-codex", name: "ChatGPT Plus/Pro", usesCallbackServer: false, loggedIn: false },
    ],
    apiKeyProviders: [
      { id: "anthropic", displayName: "Anthropic", configured: false, modelCount: 12 },
      { id: "google", displayName: "Google", configured: false, modelCount: 8 },
    ],
    onSelectOAuth: noop,
    onSelectApiKey: noop,
    onClose: noop,
  })));
  assert.match(html, /ChatGPT Plus\/Pro/);
  assert.match(html, /Anthropic/);
  assert.ok(!html.includes("Custom endpoint"), "the custom provider definition is 85a3's, not the account picker's");
});

// polyforge#271 AC2 (B3, Q2 A/Q3 A): the pi pane is a connected list plus an
// add picker. A stored credential is connected; an unconfigured one is only
// offered by the picker; the same provider's OAuth and API-key identities stay
// two rows; an entry with no usable boolean folds as unknown; an empty list,
// an empty picker and a failed read are three different states.
test("connected providers and add choices stay disjoint", async () => {
  const { ProviderAccountView } = await jiti.import("./PiAccountLogin.tsx");
  const { accountProviderRows } = await jiti.import("@/lib/provider-listing.ts");
  const oauthProviders = [
    { id: "alpha", name: "Alpha", usesCallbackServer: false, loggedIn: true },
    { id: "beta", name: "Beta", usesCallbackServer: false, loggedIn: false },
    { id: "mystery", name: "Mystery", usesCallbackServer: false },
  ];
  const apiKeyProviders = [
    { id: "gamma", displayName: "Gamma", configured: true, modelCount: 3 },
    { id: "delta", displayName: "Delta", configured: false, modelCount: 1 },
    { id: "alpha", displayName: "Alpha key", configured: false, modelCount: 0 },
  ];
  const rows = accountProviderRows(oauthProviders, apiKeyProviders);
  const html = renderToStaticMarkup(h(I18nProvider, null, h(ProviderAccountView, {
    rows, loading: false, readError: null, onRetry: noop, onAdd: noop, onSelect: noop, onOpenModels: noop,
  })));
  const connected = [...html.matchAll(/data-connected-provider="([^"]+)" data-provider-kind="([^"]+)"/g)].map((m) => `${m[2]}:${m[1]}`);
  assert.deepEqual(connected, ["oauth:alpha", "apikey:gamma"], "the list is exactly the stored credentials, per auth kind");
  assert.ok(!html.includes("data-connected-provider=\"beta\""), "an unconnected provider is not in the list");
  assert.ok(!html.includes("data-connected-provider=\"delta\""), "an unconfigured API key is not in the list");
  assert.match(html, /data-unknown-provider="mystery"/, "an entry without a usable boolean folds as unknown");
  assert.match(html, /Connection status unknown/);
  assert.ok(html.includes("Connected means a credential is stored here"), "the page says connected means a credential is stored");
  assert.match(html, /data-account-action="open-models"/, "the custom-source scope note navigates to Models");

  const picker = renderToStaticMarkup(h(I18nProvider, null, h(AddProviderPicker, {
    oauthProviders, apiKeyProviders, onSelectOAuth: noop, onSelectApiKey: noop, onClose: noop,
  })));
  const available = [...picker.matchAll(/data-provider-available="([^"]+)" data-provider-kind="([^"]+)"/g)].map((m) => `${m[2]}:${m[1]}`);
  assert.deepEqual(available, ["oauth:beta", "apikey:delta", "apikey:alpha"], "the picker offers only what is not connected");
  assert.ok(!picker.includes("data-provider-available=\"alpha\" data-provider-kind=\"oauth\""), "the connected OAuth identity is not offered again");

  const empty = renderToStaticMarkup(h(I18nProvider, null, h(ProviderAccountView, {
    rows: accountProviderRows([], []), loading: false, readError: null, onRetry: noop, onAdd: noop, onSelect: noop, onOpenModels: noop,
  })));
  assert.ok(empty.includes("No provider is connected"), "an empty list names what is missing");
  assert.match(empty, /data-account-action="add-provider"/, "the empty list offers the add action");
  assert.ok(!empty.includes("No provider matches this search"), "an empty list is not a search miss");

  const pending = renderToStaticMarkup(h(I18nProvider, null, h(ProviderAccountView, {
    rows: accountProviderRows([], []), loading: true, readError: null, onRetry: noop, onAdd: noop, onSelect: noop, onOpenModels: noop,
  })));
  assert.match(pending, /data-config-skeleton/, "the first provider read is a skeleton");
  assert.ok(!pending.includes("No provider is connected"), "a pending read is never the empty list");

  const failed = renderToStaticMarkup(h(I18nProvider, null, h(ProviderAccountView, {
    rows: accountProviderRows([], []), loading: false, readError: "settings.accountReadFailed", onRetry: noop, onAdd: noop, onSelect: noop, onOpenModels: noop,
  })));
  assert.match(failed, /role="alert"/, "a failed provider read is an alert");
  assert.match(failed, /data-account-providers-retry/);
  assert.ok(!failed.includes("No provider is connected"), "a failed read is never the empty list");

  const emptyPicker = renderToStaticMarkup(h(I18nProvider, null, h(AddProviderPicker, {
    oauthProviders: [], apiKeyProviders: [], onSelectOAuth: noop, onSelectApiKey: noop, onClose: noop,
  })));
  assert.ok(emptyPicker.includes("No provider can be added"), "an empty picker says so");
});
