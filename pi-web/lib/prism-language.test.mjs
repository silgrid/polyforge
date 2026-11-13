import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

async function loadSubject() {
  return import("./prism-language.ts");
}

test("known refractor aliases are translated to their canonical loader id", async () => {
  const { resolveHighlightLanguage } = await loadSubject();

  assert.equal(resolveHighlightLanguage("dockerfile"), "docker");
  assert.equal(resolveHighlightLanguage("html"), "markup");
  assert.equal(resolveHighlightLanguage("xml"), "markup");
});

test("ids that already match their canonical refractor name pass through unchanged", async () => {
  const { resolveHighlightLanguage } = await loadSubject();

  for (const language of [
    "rust", "java", "kotlin", "ruby", "php", "toml", "ini", "csharp",
    "swift", "lua", "typescript", "javascript", "python", "go", "c", "cpp",
    "css", "json", "yaml", "sql", "graphql", "hcl", "bash", "markdown",
    "text", "plaintext",
  ]) {
    assert.equal(resolveHighlightLanguage(language), language);
  }
});
