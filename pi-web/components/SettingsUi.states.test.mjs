// carries: components/SettingsUi.tsx, app/settings.css
// polyforge#271 AC1: the settings pages' three states share one vocabulary —
// a skeleton for a first read still in flight, a localized error notice with
// a retry action, and an empty state that names what is missing and offers the
// action that fixes it. Ordinary notices stay role=status; only the error
// variant is role=alert, so a non-error banner is not announced repeatedly.
// Expected texts come from the locale tables, never from the implementation.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const states = await jiti.import("./SettingsUi.tsx");

const h = React.createElement;
const wrap = (node) => renderToStaticMarkup(h(I18nProvider, null, node));

test("state blocks preserve status and alert semantics", () => {
  assert.equal(typeof states.ConfigSkeleton, "function", "ConfigSkeleton is one shared block");

  const skeleton = wrap(h(states.ConfigSkeleton, { id: "read-skeleton", label: "Reading…", lines: 3 }));
  assert.match(skeleton, /data-config-skeleton=""/, "the skeleton names itself for the pages' tests");
  assert.match(skeleton, /id="read-skeleton"/);
  assert.match(skeleton, /role="status"/, "a pending read is a live status");
  assert.match(skeleton, /Reading…/, "the loading label stays readable");
  assert.equal((skeleton.match(/data-config-skeleton-line/g) ?? []).length, 3, "one line per requested row");

  const error = wrap(h(states.ConfigErrorNotice, {
    summary: "The read failed.",
    action: h("button", { type: "button" }, "Retry"),
  }));
  assert.match(error, /role="alert"/, "an error is announced");
  assert.match(error, /The read failed\./);
  assert.match(error, /<button type="button">Retry<\/button>/, "the error carries the retry action");

  const empty = wrap(h(states.ConfigEmptyState, {
    action: h("button", { type: "button" }, "Add"),
  }, "No provider is connected."));
  assert.match(empty, /No provider is connected\./);
  assert.match(empty, /<button type="button">Add<\/button>/, "the empty state offers its action");

  const notice = wrap(h(states.ConfigNotice, null, "Saved."));
  assert.match(notice, /role="status"/, "an ordinary notice is not an alert");
  assert.ok(!notice.includes('role="alert"'), "an ordinary notice is never an alert");
});