// carries: lib/settings-navigation.ts, components/SettingsPanel.tsx, components/ModelsConfig.tsx, components/AppShell.tsx, lib/i18n/messages/*
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const {
  getLastSettingsSection,
  getLastSettingsSelection,
  SETTINGS_TABS,
  settingsSectionRequiresProject,
  SETTINGS_SECTION_VALUES,
  setLastSettingsSection,
  setLastSettingsSelection,
} = await jiti.import("./settings-navigation.ts");

function createStorage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    values,
    getItem(key) {
      return values.get(key) ?? null;
    },
    setItem(key, value) {
      values.set(key, value);
    },
  };
}

test("restores the last settings section and falls back without a project", () => {
  const storage = createStorage();

  setLastSettingsSection("models", storage);
  assert.equal(getLastSettingsSection(null, storage), "models");

  setLastSettingsSection("skills", storage);
  assert.equal(getLastSettingsSection("/project", storage), "skills");
  // 85d AC3: skills opens without a project too (the user scope alone).
  assert.equal(getLastSettingsSection(null, storage), "skills");

  setLastSettingsSection("agents", storage);
  assert.equal(getLastSettingsSection("/project", storage), "agents");
});

test("the polyforge section restores and opens without a project (pi#122)", () => {
  const storage = createStorage();

  setLastSettingsSection("polyforge", storage);
  assert.equal(getLastSettingsSection("/project", storage), "polyforge");
  // Not a project section: no project selected keeps it openable/restorable.
  assert.equal(getLastSettingsSection(null, storage), "polyforge");

  setLastSettingsSection("plugins", storage);
  assert.equal(getLastSettingsSection(null, storage), "general", "project sections still fall back without a project");
});

test("keeps project settings selections isolated by cwd", () => {
  const storage = createStorage();

  setLastSettingsSelection("skills", "/one/skill.md", "/project-one", storage);
  setLastSettingsSelection("skills", "/two/skill.md", "/project-two", storage);

  assert.equal(getLastSettingsSelection("skills", "/project-one", storage), "/one/skill.md");
  assert.equal(getLastSettingsSelection("skills", "/project-two", storage), "/two/skill.md");
  assert.equal(getLastSettingsSelection("skills", "/project-three", storage), null);
});

test("shares the legacy Models page's selection globally, under the historical key", () => {
  const storage = createStorage();
  const selection = JSON.stringify({ type: "provider", name: "custom" });

  // The legacy Models page (its temporary id, Q2 A) keeps the global models
  // selection: the storage key stays the historical "models" one.
  setLastSettingsSelection("legacy-models", selection, "/project-one", storage);

  assert.equal(getLastSettingsSelection("legacy-models", "/project-two", storage), selection);
  assert.equal(getLastSettingsSelection("legacy-models", null, storage), selection);
  // A selection stored before the rename still reads.
  const old = createStorage({ "pi-web:settings-navigation": JSON.stringify({ selections: { models: selection } }) });
  assert.equal(getLastSettingsSelection("legacy-models", null, old), selection);
  // The new Models concept page (85a3's) has no selection of its own yet.
  assert.equal(getLastSettingsSelection("models", null, storage), null);
});

test("the settings rail is nine new sections plus the two legacy-marked ones (85a1b AC1)", () => {
  assert.ok(Array.isArray(SETTINGS_TABS), "SETTINGS_TABS is not exported");
  assert.deepEqual(
    SETTINGS_TABS.map((tab) => tab.id),
    ["general", "account", "models", "permission", "skills", "mcp", "agents", "plugins", "polyforge", "legacy-models"],
  );
  // Only the two temporary old pages are legacy-marked (删除条件见 85a3/85a4).
  assert.deepEqual(SETTINGS_TABS.filter((tab) => tab.legacy).map((tab) => tab.id), ["legacy-models"]);
  assert.deepEqual(SETTINGS_SECTION_VALUES, SETTINGS_TABS.map((tab) => tab.id));
  // Every tab names a label key, once.
  const keys = SETTINGS_TABS.map((tab) => tab.labelKey);
  assert.deepEqual([...new Set(keys)], keys);
  for (const key of keys) assert.match(key, /^(settings|common|polyforge)\./, `${key} is not a locale key`);
  // The new concept pages and both legacy pages open without a project; the
  // skills page too (85d AC3: it shows the user scope without a project).
  for (const section of ["general", "account", "models", "permission", "skills", "mcp", "polyforge", "legacy-models"]) {
    assert.equal(settingsSectionRequiresProject(section), false, section);
  }
  for (const section of ["agents", "plugins"]) {
    assert.equal(settingsSectionRequiresProject(section), true, section);
  }
});

test("the deleted Claude page's stored `claude` value reads as unknown and lands on general (review r1#7: the migration map went with the page)", () => {
  const storage = createStorage({ "pi-web:settings-navigation": JSON.stringify({ section: "claude" }) });
  assert.equal(getLastSettingsSection("/project", storage), "general");
  // Other selections survive the unknown value untouched.
  const both = createStorage({
    "pi-web:settings-navigation": JSON.stringify({
      section: "claude",
      selections: { [JSON.stringify(["skills", "/p"])]: "/skill.md", models: "{}" },
    }),
  });
  assert.equal(getLastSettingsSection("/p", both), "general");
  assert.equal(getLastSettingsSelection("skills", "/p", both), "/skill.md");
  assert.equal(getLastSettingsSelection("legacy-models", null, both), "{}");
});

test("a stored models section value lands on the new Models concept page (85a1b AC4)", () => {
  const storage = createStorage({ "pi-web:settings-navigation": JSON.stringify({ section: "models" }) });
  assert.equal(getLastSettingsSection("/project", storage), "models");
  assert.equal(getLastSettingsSection(null, storage), "models");
  // The raw value keeps its spelling; the destination changed meaning.
  assert.equal(JSON.parse(storage.values.get("pi-web:settings-navigation")).section, "models");
});

test("restores Settings › MCP without a project, since it lists the global file alone", () => {
  const storage = createStorage();

  setLastSettingsSection("mcp", storage);
  assert.equal(getLastSettingsSection(null, storage), "mcp");
  assert.equal(getLastSettingsSection("/project", storage), "mcp");
  assert.ok(SETTINGS_SECTION_VALUES.includes("mcp"));
  assert.equal(settingsSectionRequiresProject("mcp"), false);
  assert.equal(settingsSectionRequiresProject("models"), false);
  assert.equal(settingsSectionRequiresProject("general"), false);
  assert.equal(settingsSectionRequiresProject("skills"), false, "85d AC3: skills opens without a project");
  for (const section of ["agents", "plugins"]) {
    assert.equal(settingsSectionRequiresProject(section), true, section);
  }
});

test("keeps the MCP selection per project and once more for no project", () => {
  const storage = createStorage();

  setLastSettingsSelection("mcp", "global\0github", null, storage);
  setLastSettingsSelection("mcp", "project\0lint", "/project-one", storage);
  setLastSettingsSelection("mcp", "codemode", "/project-two", storage);

  assert.equal(getLastSettingsSelection("mcp", null, storage), "global\0github");
  assert.equal(getLastSettingsSelection("mcp", undefined, storage), "global\0github");
  assert.equal(getLastSettingsSelection("mcp", "/project-one", storage), "project\0lint");
  assert.equal(getLastSettingsSelection("mcp", "/project-two", storage), "codemode");
  assert.equal(getLastSettingsSelection("mcp", "/project-three", storage), null);
  // A project section still remembers nothing without a project.
  setLastSettingsSelection("plugins", "key", null, storage);
  assert.equal(getLastSettingsSelection("plugins", null, storage), null);
  // A folder literally named "mcp" cannot reach the no-project key.
  assert.equal(getLastSettingsSelection("mcp", "mcp", storage), null);
});

test("ignores malformed and unavailable browser storage", () => {
  const malformed = createStorage({ "pi-web:settings-navigation": "{" });
  const unavailable = {
    getItem() { throw new Error("blocked"); },
    setItem() { throw new Error("blocked"); },
  };

  assert.equal(getLastSettingsSection("/project", malformed), "general");
  assert.equal(getLastSettingsSelection("plugins", "/project", malformed), null);
  assert.equal(getLastSettingsSection("/project", unavailable), "general");
  assert.doesNotThrow(() => setLastSettingsSection("plugins", unavailable));
  assert.doesNotThrow(() => setLastSettingsSelection("plugins", "key", "/project", unavailable));
});

test("the skills section works without a project: no redirect, and its selection lives under the bare name (85d AC3)", () => {
  // A stored skills section survives a null cwd instead of falling back to general.
  const stored = createStorage({ "pi-web:settings-navigation": JSON.stringify({ section: "skills" }) });
  assert.equal(getLastSettingsSection(null, stored), "skills");
  assert.equal(getLastSettingsSection("/project", stored), "skills");

  // The no-project selection is its own key, like MCP's: no project key
  // (always an array) can collide with it, and each side keeps its own.
  const storage = createStorage();
  setLastSettingsSelection("skills", "/agent/skills/a/SKILL.md", null, storage);
  setLastSettingsSelection("skills", "/agent/skills/b/SKILL.md", "/project", storage);
  assert.equal(getLastSettingsSelection("skills", null, storage), "/agent/skills/a/SKILL.md");
  assert.equal(getLastSettingsSelection("skills", "/project", storage), "/agent/skills/b/SKILL.md");
  // A remembered project selection never leaks into the no-project listing.
  const projectOnly = createStorage();
  setLastSettingsSelection("skills", "/project/.pi/skills/x/SKILL.md", "/project", projectOnly);
  assert.equal(getLastSettingsSelection("skills", null, projectOnly), null);
});
