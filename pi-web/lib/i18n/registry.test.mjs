import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const {
  getLocalePlugin,
  getSupportedLocales,
  resolveBrowserLocale,
} = await jiti.import("./registry.ts");

test("uses the first supported browser language and falls back to English", () => {
  assert.equal(resolveBrowserLocale(["zh-CN", "en-US"]), "zh-CN");
  assert.equal(resolveBrowserLocale(["zh", "en-US"]), "zh-CN");
  assert.equal(resolveBrowserLocale(["zh-Hans", "zh-TW"]), "zh-CN");
  assert.equal(resolveBrowserLocale(["zh-Hans-HK", "en-US"]), "zh-CN");
  assert.equal(resolveBrowserLocale(["zh-SG", "en-US"]), "zh-CN");
  assert.equal(resolveBrowserLocale(["zh-MY", "en-US"]), "zh-CN");
  assert.equal(resolveBrowserLocale(["zh-TW", "en-US"]), "zh-TW");
  assert.equal(resolveBrowserLocale(["ZH-tW", "en-US"]), "zh-TW");
  assert.equal(resolveBrowserLocale(["zh-Hant", "en-US"]), "zh-TW");
  assert.equal(resolveBrowserLocale(["zh-Hant-HK", "en-US"]), "zh-TW");
  assert.equal(resolveBrowserLocale(["zh-HK", "en-US"]), "zh-TW");
  assert.equal(resolveBrowserLocale(["zh-MO", "en-US"]), "zh-TW");
  assert.equal(resolveBrowserLocale(["en-US", "zh-CN"]), "en");
  assert.equal(resolveBrowserLocale(["fr-FR", "zh-CN"]), "zh-CN");
  assert.equal(resolveBrowserLocale(["fr-FR"]), "en");
  assert.equal(resolveBrowserLocale([]), "en");
});

test("returns only registered locales", () => {
  assert.deepEqual(getSupportedLocales(), ["en", "zh-CN", "zh-TW"]);
  assert.equal(getLocalePlugin("en").id, "en");
  assert.equal(getLocalePlugin("zh-TW")?.label, "繁體中文");
  assert.equal(getLocalePlugin("zh-TW")?.messages["common.language"], "語言");
  assert.equal(getLocalePlugin("missing"), undefined);
});

// Cross-locale parity alone cannot catch a key lost from every translation in
// a merge. Keep the upstream sidebar vocabulary even where the fork's directory
// picker currently uses different labels; syncing must preserve both key sets.
test("preserves upstream sidebar keys alongside the fork directory picker", () => {
  const upstreamKeys = [
    "sidebar.selectProject",
    "sidebar.filterProjects",
    "sidebar.noMatchingProjects",
    "sidebar.useDefaultDirectory",
    "sidebar.customPath",
    "sidebar.uploadFiles",
    "sidebar.worktreePath",
    "sidebar.removeWorktreeTitle",
  ];
  for (const locale of getSupportedLocales()) {
    const messages = getLocalePlugin(locale).messages;
    for (const key of upstreamKeys) {
      assert.equal(typeof messages[key], "string", `${locale}.${key} must survive upstream sync`);
      assert.ok(messages[key].trim(), `${locale}.${key} must not be empty`);
    }
    assert.ok(messages["directoryPicker.entriesTitle"]);
    assert.ok(messages["sidebar.renameDirectory"]);
  }
});

test("built-in locale packages have the complete English key and required placeholder sets", () => {
  const englishMessages = getLocalePlugin("en").messages;
  const englishKeys = Object.keys(englishMessages).sort();
  const placeholders = (message) => [...message.matchAll(/\{([\w.-]+)\}/g)].map((match) => match[1]).sort();
  const optionalPlaceholders = { "files.conflictSummary": ["countSuffix"] };

  for (const locale of getSupportedLocales().filter((id) => id !== "en")) {
    const messages = getLocalePlugin(locale).messages;
    assert.deepEqual(Object.keys(messages).sort(), englishKeys, `${locale} keys must match English`);
    for (const key of englishKeys) {
      const optional = optionalPlaceholders[key] ?? [];
      const required = placeholders(englishMessages[key]).filter((name) => !optional.includes(name));
      const translated = placeholders(messages[key]).filter((name) => !optional.includes(name));
      assert.deepEqual(translated, required, `${locale}.${key} placeholders must match English`);
    }
  }
});

// the per-feature "exists in all three locales" tests also
// required each translation to be non-empty; key parity alone passes an
// emptied value. Every key English fills is filled in every locale.
test("built-in locale packages fill every key English fills", () => {
  const englishMessages = getLocalePlugin("en").messages;
  for (const locale of getSupportedLocales().filter((id) => id !== "en")) {
    const messages = getLocalePlugin(locale).messages;
    const empty = Object.keys(englishMessages)
      .filter((key) => englishMessages[key].trim() !== "" && typeof messages[key] === "string" && messages[key].trim() === "");
    assert.deepEqual(empty, [], `${locale} leaves keys empty that English fills`);
  }
});

// the workflow card's strings ride every locale (the card
// renders no hardcoded user-visible English), and the source text keeps
// the fact it states.
test("the workflow card's keys exist in every locale with their fixed facts", () => {
  const keys = ["workflowTitle", "workflowHint", "workflowProject", "workflowInherit",
    "workflowDeclared", "workflowEffective", "workflowSourceLabel", "workflowSubmitNote", "workflowCapabilityHint",
    "workflowBundleError", "workflowChainAdd", "workflowChainClear", "workflowDefaultRow",
    "workflowTaskRow", "workflowEditToggle", "classLiveNote"];
  for (const locale of getSupportedLocales()) {
    const messages = getLocalePlugin(locale).messages;
    for (const key of keys) assert.ok(messages[`polyforge.settings.${key}`], `${locale} lacks polyforge.settings.${key}`);
    assert.match(messages["polyforge.settings.workflowSubmitNote"], /pf submit --models/, `${locale}'s note names the manual override`);
  }
});
