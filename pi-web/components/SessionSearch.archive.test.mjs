import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// wi pi#103 (sidebar A1: Archive), plan step 6 — sidebar search surfaces
// archived sessions with an archived badge; the click path stays view-only.
// The jiti import below is a parse smoke test; the rest pins the wiring at
// source level (SessionSearch is a small presentational component — the
// meaningful seams are the prop flow and the untouched click path).

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./SessionSearch.tsx");

const source = await readFile(new URL("./SessionSearch.tsx", import.meta.url), "utf8");
const sidebarSource = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");

test("the badge is keyed on the archivedSessionIds prop", () => {
  assert.match(
    source,
    /archivedSessionIds\?: ReadonlySet<string>;/,
    "SessionSearch must accept an optional ReadonlySet prop",
  );
  assert.match(source, /archivedSessionIds\?\.has\(session\.id\)/);
  // Sidebar passes the live archive set down — no new plumbing, no copy.
  assert.match(
    sidebarSource,
    /<SessionSearch open=\{sessionSearchOpen\} query=\{sessionSearchQuery\} selectedSessionId=\{selectedSessionId\} archivedSessionIds=\{archivedSessionIds\} onSelectSession=\{handleSelectSessionFromList\}>/,
  );
});

test("the click path is unchanged — view-only open, no archive mutation", () => {
  const row = source.match(/\{response\?\.results\.map\(\(\{ session, entryId, blockIndex, titleMatch, before, match, after \}\) => \([\s\S]*?\)\)\}/);
  assert.ok(row, "the result row render must be found");
  assert.match(row[0], /onClick=\{\(\) => onSelectSession\(session, entryId, blockIndex\)\}/);
  // The only archive-set interaction in the file is the badge READ (has) —
  // never a mutation.
  assert.match(row[0], /archivedSessionIds\?\.has\(session\.id\)/);
  for (const m of source.matchAll(/archivedSessionIds/g)) {
    const around = source.slice(m.index, m.index + 120);
    assert.match(around, /has\(|ReadonlySet|archivedSessionIds,/, "archivedSessionIds is only read, never mutated");
  }
  assert.doesNotMatch(source, /setItem|removeItem/);
});

test("the badge chip is localized via sidebar.archivedBadge", () => {
  assert.match(source, /t\("sidebar\.archivedBadge"\)/);
  assert.match(source, /title=\{t\("sidebar\.archivedBadge"\)\}/);
});

test("the badge text exists in all three locales, exactly", async () => {
  const expectations = [
    ["zh-CN", "已归档"],
    ["zh-TW", "已封存"],
    ["en", "Archived"],
  ];
  for (const [locale, label] of expectations) {
    const messages = await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    assert.match(messages, new RegExp(`"sidebar\\.archivedBadge": "${label}"`), `${locale} badge must be exactly ${label}`);
  }
});
