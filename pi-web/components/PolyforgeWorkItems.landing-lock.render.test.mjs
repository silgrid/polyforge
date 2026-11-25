// carries: components/PolyforgeWorkItems*, components/PolyforgePanel*, lib/polyforge-landing-lock*
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createJiti } from "jiti";

// The 工单列表 row and the selected job card render the same exported badge;
// this is a real server-render (not a source regex), with the zh-CN catalog.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { getLocalePlugin } = await jiti.import("../lib/i18n/registry.ts");
const { PolyforgeLandingLockBadge } = await jiti.import("./PolyforgeWorkItems.tsx");

const messages = getLocalePlugin("zh-CN").messages;
const t = (key, values = {}) => String(messages[key] ?? key).replace(/\{(\w+)\}/g, (_, name) => values[name] ?? "");
const render = (lock, dim = true) => renderToStaticMarkup(
  React.createElement(PolyforgeLandingLockBadge, { lock, t, dim }),
);

const key = (repo, position) => ({ repo, branch: "epic/245", position });

test("waiting badges render the position, the key title and the shared words", () => {
  const one = render({ state: "waiting", keys: [key("github.com/o/app", 3)] });
  assert.match(one, /data-pf-landing-lock=""/);
  assert.match(one, />等待合入锁 #3</);
  assert.match(one, /title="github\.com\/o\/app \(epic\/245\)"/);
  const many = render({ state: "waiting", keys: [key("github.com/o/a", 1), key("github.com/o/b", 4)] });
  assert.match(many, />等待合入锁 #1,#4</);
  assert.match(many, /title="github\.com\/o\/a \(epic\/245\), github\.com\/o\/b \(epic\/245\)"/);
});

test("stale, unavailable, none and keyless values render truthfully", () => {
  const stale = render({ state: "stale", ageMs: 20000, keys: [key("github.com/o/app", 3)] });
  assert.match(stale, />等待合入锁 #3（陈旧）</);
  const unavailable = render({ state: "unavailable", keys: [] });
  assert.match(unavailable, />锁状态不可用</);
  assert.doesNotMatch(unavailable, /#\d/);
  assert.equal(render({ state: "none", keys: [] }), "", "fresh-none has no badge");
  assert.equal(render({ state: "stale", ageMs: 20000, keys: [] }), "", "a keyless stale value invents no #?");
});

test("the card variant renders the same words, and the two surfaces use this one component", async () => {
  const card = render({ state: "waiting", keys: [key("github.com/o/app", 3)] }, false);
  assert.match(card, />等待合入锁 #3</);
  assert.doesNotMatch(card, /#0|\#\?/);
  // wiring pins: both surfaces render the exported component, not their own copy
  const workItems = await readFile(new URL("./PolyforgeWorkItems.tsx", import.meta.url), "utf8");
  const panel = await readFile(new URL("./PolyforgePanel.tsx", import.meta.url), "utf8");
  assert.match(workItems, /<PolyforgeLandingLockBadge lock=\{row\.landing_lock\}/);
  assert.match(panel, /<PolyforgeLandingLockBadge lock=\{landingLock\}/);
});
