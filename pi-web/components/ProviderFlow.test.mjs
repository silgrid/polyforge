// carries: components/PiAccountLogin*, components/AccountSettings*, components/SettingsDrafts*
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ApiKeyDetail } = await jiti.import("./PiAccountLogin.tsx");
const { ClaudeCredentialForm } = await jiti.import("./AccountSettings.tsx");
const { SettingsDraftProvider } = await jiti.import("./SettingsDrafts.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const h = React.createElement;
const noop = () => {};
const render = (child, managed) => renderToStaticMarkup(h(I18nProvider, null, managed ? h(SettingsDraftProvider, { onDirtyChange: noop, onNavigate: noop }, child) : child));
for (const managed of [true, false]) {
  test(`API key has a labelled password input and ${managed ? "no local" : "one fallback"} save`, () => {
    const html = render(h(ApiKeyDetail, { provider: { id: "fixture", displayName: "Fixture", configured: false, modelCount: 1 }, onRefresh: noop }), managed);
    assert.equal((html.match(/>Save<\/button>/g) ?? []).length, managed ? 0 : 1);
    assert.match(html, /aria-label="API Key"/);
    assert.match(html, /type="password"/);
    assert.doesNotMatch(html, /<h\d[^>]*>API Key</);
  });
  test(`Claude credential form ${managed ? "delegates submission to Settings" : "keeps its fallback"}`, () => {
    const html = render(h(ClaudeCredentialForm, { cells: [], purpose: "interactive", onSave: noop }), managed);
    assert.equal(html.includes('data-account-action="save"'), !managed);
  });
}
