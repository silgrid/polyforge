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

test("a stored Anthropic subscription shows status and logout alone — never a login (AC8)", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(OAuthDetail, {
    provider: { id: "anthropic", name: "Anthropic (Claude Pro/Max)", usesCallbackServer: false, loggedIn: true },
    onRefresh: noop,
  })));
  assert.match(html, /Connected/, "the stored credential's status is missing");
  assert.match(html, /Disconnect/, "the logout is missing");
  assert.ok(!html.includes(">Log in<") && !html.match(/<button[^>]*>Log in/), "a login is offered for the stored subscription");
});

test("an offered provider keeps its login button; the API-key entry stays for Anthropic (AC4/AC8)", () => {
  const login = renderToStaticMarkup(h(I18nProvider, null, h(OAuthDetail, {
    provider: { id: "openai-codex", name: "ChatGPT Plus/Pro", usesCallbackServer: false, loggedIn: false },
    onRefresh: noop,
  })));
  assert.match(login, /Log in/, "an offered provider lost its login button");
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
