// carries: components/AppShell.tsx, components/SettingsPanel.tsx, components/HarnessGeneralSettings.tsx, components/GeneralSettings.tsx
// polyforge#167 (91-onboarding) AC1: the wizard's first-open entry lives on
// the null-session (welcome) branch and the reopen entry in Settings › 常规.
// AppShell is a 4,000-line client shell the suite observes through its
// rendered settings entry plus the wiring that only a browser can run (the
// e2e drives the flow itself); expected labels come from the English table.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { GeneralSettings } = await jiti.import("./GeneralSettings.tsx");
const { HarnessGeneralSettings } = await jiti.import("./HarnessGeneralSettings.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const source = await readFile(new URL("./AppShell.tsx", import.meta.url), "utf8");
const settingsPanel = await readFile(new URL("./SettingsPanel.tsx", import.meta.url), "utf8");

const h = React.createElement;
const noop = () => {};

test("AppShell reads the onboarding state once and writes each wizard action", () => {
  assert.match(source, /import \{ OnboardingWizard \} from "\.\/OnboardingWizard";/);
  assert.match(source, /fetch\("\/api\/onboarding"\)/, "the shell never reads the onboarding state");
  assert.match(source, /method: "POST"/);
  assert.match(source, /void writeOnboarding\(\{ action: "complete", step \}\);/, "a completed step writes no completion");
  assert.match(source, /void writeOnboarding\(\{ action: "skip", step \}\);/, "a skip writes no skip record");
  assert.match(source, /void writeOnboarding\(\{ action: "dismiss" \}\);/, "closing the wizard writes no dismissal");
  assert.match(source, /void writeOnboarding\(\{ action: "reopen" \}\);/, "the settings entry writes no reopen");
});

test("the auto entry is the null-session branch: dismissed never opens, a reopen shows it over a session", () => {
  assert.match(source, /const showPlaceholder = initialSessionRestored && !showChat;/);
  assert.match(source, /const onboardingOpen = onboarding\?\.imageManaged === true && !onboarding\.dismissed && \(showPlaceholder \|\| onboardingReopened\);/,
    "the wizard's open condition does not require the null-session branch and the dismissed fact");
  assert.match(source, /onClose=\{handleOnboardingDismiss\}/);
  assert.match(source, /onOpenAccount=\{\(\) => openSettingsSection\("account"\)\}/, "the account step does not deep-link to Settings › 账号");
  assert.match(source, /onWorkspaceChange=\{handleCwdChange\}/, "the workspace step does not go through the existing selection");
});

test("Settings › 常规 gets the reopen entry only in the image mode", () => {
  assert.match(source, /onReopenOnboarding=\{onboarding\?\.imageManaged === true \? handleOnboardingReopen : undefined\}/,
    "the settings panel does not receive the reopen entry behind the image gate");
  assert.match(settingsPanel, /onReopenOnboarding\?: \(\) => void;/, "the panel's prop is not optional");
  assert.match(settingsPanel, /onReopenOnboarding=\{onReopenOnboarding\}/, "the panel does not forward the entry to General");
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
