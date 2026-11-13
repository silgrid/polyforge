import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./PolyforgeAnalysisCard.tsx", import.meta.url), "utf8");
const locales = Object.fromEntries(await Promise.all(["en", "zh-CN", "zh-TW"].map(async (locale) => [
  locale, await readFile(new URL(`../lib/i18n/messages/${locale}.ts`, import.meta.url), "utf8"),
])));

test("the header (badge, model · duration, expand control) is always shown; the body only expanded", () => {
  assert.match(source, /t\(`polyforge\.messages\.analysisType\.\$\{analysis\.type\}`\)/, "the badge is the localized type, never the raw enum");
  assert.match(source, /\[analysis\.model, analysis\.duration\]\.filter\(\(fact\) => fact !== ""\)\.join\(" · "\)/);
  assert.match(source, /aria-expanded=\{expanded\}/);
  assert.match(source, /expanded \? t\("polyforge\.messages\.analysisCollapse"\) : t\("polyforge\.messages\.analysisExpand"\)/);
  assert.match(source, /hidden=\{!expanded\}[^>]*>\s*\{analysis\.summary\}/, "collapsing hides the summary");
  assert.doesNotMatch(source, /localStorage|sessionStorage|fetch\(|conclusion/, "the card writes and reads nothing");
});

test("every closed type has its badge word in en / zh-CN / zh-TW", () => {
  const words = {
    en: ["Requirement", "Bug", "Ruling", "Discussion", "Clarification", "Outreach", "Other"],
    "zh-CN": ["需求", "缺陷", "裁定", "讨论", "消歧", "外联", "其他"],
    "zh-TW": ["需求", "缺陷", "裁定", "討論", "消歧", "外聯", "其他"],
  };
  const types = ["requirement", "bug", "ruling", "discussion", "clarification", "outreach", "other"];
  for (const [locale, list] of Object.entries(words)) {
    types.forEach((type, index) => {
      assert.ok(locales[locale].includes(`"polyforge.messages.analysisType.${type}": "${list[index]}"`), `${locale} ${type}`);
    });
  }
});

test("no card control name collides with the Analyze entry's name", () => {
  for (const locale of Object.values(locales)) {
    for (const key of ["analysisCollapse", "analysisExpand"]) {
      const line = locale.split("\n").find((row) => row.includes(`"polyforge.messages.${key}"`)) ?? "";
      assert.ok(line, key);
      assert.doesNotMatch(line.split(":").slice(1).join(":"), /分析|Analyze/i, `${key} must not read as the Analyze entry`);
    }
  }
});

test("both actions always render; the suggested one is primary, note_only/none leave both secondary", () => {
  assert.match(source, /const primary = primarySuggestedAction\(analysis\.suggested_actions\);/);
  assert.match(source, /\.\.\.\(primary === kind \? PRIMARY : SECONDARY\)/);
  assert.match(source, /\{action\("file_ticket", t\("polyforge\.messages\.actionFileTicket"\), onFileTicket\)\}/);
  assert.match(source, /\{action\("reply", t\("polyforge\.messages\.reply"\), onReply\)\}/);
  assert.notDeepEqual(
    /const PRIMARY = (\{[^}]*\})/.exec(source)?.[1],
    /const SECONDARY = (\{[^}]*\})/.exec(source)?.[1],
    "the primary and secondary styles differ",
  );
});

test("the task link opens the task through the panel and hides with the body", () => {
  assert.match(source, /hidden=\{!expanded\}\s*onClick=\{\(event\) => \{ event\.stopPropagation\(\); onOpenTask\(analysis\.task_id\); \}\}/);
  assert.match(source, /t\("polyforge\.messages\.analysisTask", \{ id: analysis\.task_id \}\)/);
});
