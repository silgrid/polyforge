import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { I18nProvider } from "../hooks/useI18n";
import { HarnessModelSettings } from "./HarnessModelSettings.tsx";

test("模型页包含默认模型、三列清单和 pi 编辑区", () => {
  const html = renderToStaticMarkup(h(I18nProvider, null, h(HarnessModelSettings, { cwd: null })));
  assert.match(html, /默认模型/);
  assert.match(html, /模型清单/);
  assert.match(html, /聊天可用/);
  assert.match(html, /pi/);
});
