// carries: components/AppShell.tsx, components/SettingsPanel.tsx, components/HarnessGeneralSettings.tsx, components/GeneralSettings.tsx, lib/onboarding-flow.ts
// polyforge#167 (91-onboarding) AC1: the wizard's first-open entry lives on
// AppShell's null-session branch (no selected session, a draft tab included)
// and Settings › 常规's reopen entry opens it over a session. The popup
// decision is a pure function with a truth table taken from the ticket; the
// shell's fetch/POST wiring is the e2e's to observe (e2e/onboarding-91.mjs
// OB1–OB9), never a source scan. Expected labels come from the English table.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { GeneralSettings } = await jiti.import("./GeneralSettings.tsx");
const { HarnessGeneralSettings } = await jiti.import("./HarnessGeneralSettings.tsx");
const { onboardingPopupOpen } = await jiti.import("@/lib/onboarding-flow");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const h = React.createElement;
const noop = () => {};
const facts = (overrides = {}) => ({
  imageManaged: true,
  dismissed: false,
  hasSelectedSession: false,
  reopened: false,
  ...overrides,
});

test("the popup opens on the null-session branch only: host mode, a dismissal and an open session each keep it closed", () => {
  // AC1: 第一次打开时出现引导；完成或跳过以后不再弹出；非镜像模式的 Pi Web 上不出现；
  // 工单: 无会话时的入口挂在 AppShell 的 null-session 分支。
  assert.equal(onboardingPopupOpen(facts()), true, "the first open with no session does not show the wizard");
  assert.equal(onboardingPopupOpen(facts({ hasSelectedSession: true })), false, "an open session auto-opens the wizard off the null-session branch");
  assert.equal(onboardingPopupOpen(facts({ dismissed: true })), false, "a dismissed wizard opens again without a reopen");
  assert.equal(onboardingPopupOpen(facts({ imageManaged: false })), false, "a host-mode Pi Web shows the wizard");
  assert.equal(onboardingPopupOpen(facts({ imageManaged: false, reopened: true })), false, "a host-mode reopen shows the wizard");
  // A draft tab (the entry fallback's new session) leaves `selectedSession`
  // null in the shell, so the null-session branch keeps the popup; that
  // wiring is the e2e's OB9 to observe (the pure function only sees the fact).
});

test("Settings › 常规's reopen opens the wizard over a session, and only after the reopen write lands", () => {
  // AC1: 可以从设置 › 常规重新打开. The reopen POST clears `dismissed`; until
  // that answer lands the dismissal still wins, so the popup never flashes open
  // ahead of the state it is showing.
  assert.equal(onboardingPopupOpen(facts({ hasSelectedSession: true, reopened: true })), true, "the reopen does not open over a session");
  assert.equal(onboardingPopupOpen(facts({ reopened: true, dismissed: true })), false, "the reopened popup ignores the not-yet-cleared dismissal");
});

function renderGeneral(onReopenOnboarding) {
  return renderToStaticMarkup(h(I18nProvider, null, h(GeneralSettings, {
    sessionId: null,
    onSessionReloaded: noop,
    quoteSelectionEnabled: false,
    onQuoteSelectionChange: noop,
    onReopenOnboarding,
  })));
}

test("General's 常规 pane offers the reopen entry in the image mode and nothing on a host", () => {
  const image = renderGeneral(noop);
  assert.match(image, /data-onboarding-reopen/);
  assert.match(image, /Reopen first-time setup/);
  assert.match(image, /Run the four-step setup again; completed steps are kept\./);
  assert.match(image, /Reopen<\/button>/);
  const host = renderGeneral(undefined);
  assert.doesNotMatch(host, /data-onboarding-reopen/, "a host-mode Pi Web shows no reopen entry");
  assert.doesNotMatch(host, /Reopen first-time setup/);
});

test("HarnessGeneralSettings forwards the reopen entry to its 界面与对话 pane", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(HarnessGeneralSettings, {
    cwd: null,
    sessionId: null,
    onSessionReloaded: noop,
    quoteSelectionEnabled: false,
    onQuoteSelectionChange: noop,
    onReopenOnboarding: noop,
  })));
  assert.match(html, /data-onboarding-reopen/);
  assert.match(html, /Reopen first-time setup/);
});
