import "#test-env";
import assert from "node:assert/strict";
import React from "react";
import { renderToString } from "react-dom/server";
import test from "node:test";

import { createJiti } from "jiti";
const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const { PaneHeader } = await jiti.import("./PaneHeader.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const base = {
  id: "pane-1",
  label: "session one",
  hasBadge: false,
  focused: false,
  onClick: () => {},
  onClose: () => {},
};

test("a running pane header renders the animated spinner indicator (pi#79)", () => {
  const html = renderToString(React.createElement(I18nProvider, null, React.createElement(PaneHeader, { ...base, running: true })));
  assert.match(html, /animateTransform/);
  assert.match(html, /aria-label="[^"]+"/);
});

test("a non-running pane header renders no spinner and no static dot", () => {
  const html = renderToString(React.createElement(I18nProvider, null, React.createElement(PaneHeader, { ...base, running: false })));
  assert.doesNotMatch(html, /animateTransform/);
  assert.doesNotMatch(html, /border-radius:\s*50%[^"]*background/);
});
