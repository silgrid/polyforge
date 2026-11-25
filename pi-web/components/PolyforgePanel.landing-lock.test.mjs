// carries: components/PolyforgePanel.tsx, lib/polyforge-landing-lock.ts, lib/i18n/messages/en.ts, lib/i18n/messages/zh-CN.ts, lib/i18n/messages/zh-TW.ts
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./PolyforgePanel.tsx", import.meta.url), "utf8");
const locales = Object.fromEntries(await Promise.all(["en", "zh-CN", "zh-TW"].map(async (id) => [
  id, await readFile(new URL(`../lib/i18n/messages/${id}.ts`, import.meta.url), "utf8"),
])));

test("waiting lock visible in the selected job card, from the same watch snapshot", () => {
  assert.match(source, /landingLock=\{isSelected\(card\.job\) \? watchStatus\?\.landingLock : undefined\}/,
    "only the selected job reads the watch projection");
  const card = source.slice(source.indexOf("const PolyforgeJobCard = memo("), source.indexOf("export function PolyforgePanel("));
  assert.match(card, /landingLock\?: PolyforgeLandingLock;/);
  assert.match(card, /<PolyforgeLandingLockBadge lock=\{landingLock\} t=\{t\} dim=\{false\} \/>/);
  for (const [id, text] of Object.entries(locales)) {
    assert.match(text, /"polyforge\.landingLock\.waiting":/, `${id} waiting`);
    assert.match(text, /"polyforge\.landingLock\.stale":/, `${id} stale`);
    assert.match(text, /"polyforge\.landingLock\.unavailable":/, `${id} unavailable`);
  }
  assert.match(locales["zh-CN"], /等待合入锁 \{positions\}/);
  assert.match(locales["zh-CN"], /（陈旧）/);
});
