// carries: components/ModelCandidatePicker*, lib/step-tiers*
// polyforge#151 (85b2, u4): the shared candidate picker — grouped by the
// step-tier catalog (default first, then 85b's alphabetical order, then the
//「未设置」group; an invalid reference marked), the manual fill kept beside
// it, and a failed catalog read leaving the manual fill with its reason.
// The parent owns the draft and the write; the picker never fetches (the
// dock's copy rides the watch snapshot — G11) and never saves.
import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { ModelCandidatePicker } = await jiti.import("./ModelCandidatePicker.tsx");

const GROUPS = [
  { tier: "default", candidates: [
    { value: "pi:acme/one", invalid: false },
    { value: "pi:gone/old", invalid: true },
  ] },
  { tier: null, candidates: [{ value: "claude:sonnet", invalid: false }] },
];

const render = (props) => renderToStaticMarkup(React.createElement(
  I18nProvider, null, React.createElement(ModelCandidatePicker, {
    groups: GROUPS,
    value: "",
    onChange: () => {},
    selectLabel: "Pick a candidate",
    inputLabel: "Add candidate",
    ...props,
  }),
));

test("the picker groups by tier with the unset group and the invalid marks, beside the manual fill", () => {
  const html = render({});
  assert.match(html, /<optgroup label="default">/, "a tier group");
  assert.match(html, /<option value="pi:acme\/one">/, "the candidate is its own option value");
  assert.match(html, /<option value="pi:gone\/old">[^<]*no longer defined/, "an invalid reference is marked in its option text");
  assert.match(html, /<optgroup label="No tier">[^]*claude:sonnet/, "the unset group lists the defined models no tier covers");
  assert.match(html, /aria-label="Add candidate"/, "the manual fill keeps its own label");
  assert.match(html, /aria-label="Pick a candidate"/, "the select is labelled for the entry");
  assert.ok(html.includes("default") && html.indexOf('label="default"') < html.indexOf("No tier"), "the default tier precedes the unset group");
});

test("a failed catalog read leaves only the manual fill, with its reason", () => {
  const html = render({ groups: null, reason: "the step-tier catalog needs pf 0.2.4 or newer" });
  assert.ok(!html.includes("<select"), "no select without a catalog");
  assert.match(html, /aria-label="Add candidate"/, "the manual fill stays");
  assert.match(html, /catalog is unavailable[^]*0\.2\.4/, "the reason shows beside it");
});

test("disabled and the manual draft pass through", () => {
  const html = render({ disabled: true, value: "pi:manual/entry", placeholder: "type a candidate" });
  assert.match(html, /<select[^]*disabled/, "the select disables");
  assert.match(html, /<input[^]*disabled[^]*value="pi:manual\/entry"/, "the input carries the draft and disables");
  assert.match(html, /placeholder="type a candidate"/);
});
