// carries: components/HarnessModelSettings*, lib/harness/model-view*
import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { HarnessModelSettings, HarnessModelTable } = await jiti.import("./HarnessModelSettings.tsx");

test("模型页包含默认模型、三列清单和 pi 编辑区", () => {
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelSettings, { cwd: null })));
  assert.match(html, /data-model-page="unified"/);
  assert.match(html, /默认模型/);
  assert.match(html, /模型清单/);
  assert.match(html, /Chat availability/);
  assert.match(html, /pi/);
});

test("unified table renders both harnesses, exact provider identity and unsupported Claude chat", () => {
  const rows = [
    { provider: "anthropic", id: "opus", name: "Opus", harnesses: ["pi", "claude"], chatAvailable: true, reasoning: ["high"], isDefault: true },
    { provider: "gateway", id: "opus", name: "Gateway Opus", harnesses: ["pi"], chatAvailable: false, reasoning: ["minimal"], isDefault: false },
    { provider: "claude", id: "sonnet", name: "Sonnet", harnesses: ["claude"], chatAvailable: false, reasoning: ["adaptive"], isDefault: false },
  ];
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(HarnessModelTable, { rows })));
  assert.match(html, /anthropic/);
  assert.match(html, /Gateway Opus/);
  assert.match(html, /Sonnet/);
  assert.match(html, /Not supported/);
  assert.match(html, /high/);
  assert.match(html, /minimal/);
  assert.match(html, /adaptive/);
  assert.match(html, /Available for/);
  assert.match(html, /Chat availability/);
});

