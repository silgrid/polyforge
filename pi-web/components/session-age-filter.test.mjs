import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// Structural wiring tests (pi#82), source-regex style: pin the seams —
// the sidebar composes the age filter after the worker filter, the
// Settings row persists through the shared store, and the i18n keys
// exist in all three dictionaries.

const sidebar = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");
const settings = await readFile(new URL("./SettingsPanel.tsx", import.meta.url), "utf8");
const lib = await readFile(new URL("../lib/session-filter.ts", import.meta.url), "utf8");
const en = await readFile(new URL("../lib/i18n/messages/en.ts", import.meta.url), "utf8");
const zhCN = await readFile(new URL("../lib/i18n/messages/zh-CN.ts", import.meta.url), "utf8");
const zhTW = await readFile(new URL("../lib/i18n/messages/zh-TW.ts", import.meta.url), "utf8");

test("the sidebar folds the age filter into visibleSessions after the worker filter", () => {
  assert.match(sidebar, /filterSessionsByAge\(/);
  assert.match(sidebar, /sessionFilter\.ageFilterDays/);
  // Composition order: worker filter result feeds the age window.
  assert.match(sidebar, /filterSessionsByAge\(\s*showFilteredSessions \|\| sessionFilterPatterns\.length === 0/);
});

test("the age window is stored, clamped, and 0 disables it", () => {
  assert.match(lib, /SESSION_AGE_FILTER_DAYS_STORAGE_KEY = "pi-web:session-age-filter-days"/);
  assert.match(lib, /DEFAULT_SESSION_AGE_FILTER_DAYS = 15;/);
  assert.match(lib, /Math\.max\(SESSION_AGE_FILTER_DAYS_MIN, Math\.min\(SESSION_AGE_FILTER_DAYS_MAX, days\)\)/);
  assert.match(lib, /days <= 0\) return sessions;/);
  // A filter never hides what it cannot judge.
  assert.match(lib, /if \(!Number\.isFinite\(modified\)\) return false;/);
});

test("Settings persists the age window through the shared store", () => {
  assert.match(settings, /setSessionAgeFilterDays\(/);
  assert.match(settings, /getSessionFilterState\(\)\.ageFilterDays/);
  assert.match(settings, /data-testid="session-age-filter-input"/);
  assert.match(settings, /min=\{0\}/);
  assert.match(settings, /max=\{365\}/);
});

test("i18n carries the age-filter strings in all three dictionaries", () => {
  for (const dict of [en, zhCN, zhTW]) {
    assert.match(dict, /"settings\.sessionAgeDays":/);
    assert.match(dict, /"settings\.sessionAgeDaysDescription":/);
  }
  // The zero semantics are stated in the owner's language.
  assert.match(zhCN, /0 表示不过滤/);
});

test("the age-filter description states the default 15 in all three dictionaries", () => {
  const line = (dict) => dict.split("\n").find((l) => l.includes('"settings.sessionAgeDaysDescription":')) ?? "";
  assert.match(line(en), /Default 15\./);
  assert.match(line(zhCN), /默认 15。/);
  assert.match(line(zhTW), /預設 15。/);
  for (const dict of [en, zhCN, zhTW]) {
    assert.doesNotMatch(line(dict), /(Default|默认|預設) 7\b/, "the old 7-day default is gone");
  }
});
