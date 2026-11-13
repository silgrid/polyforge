import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./route.ts", import.meta.url), "utf8");

function sliceBetween(startMarker, endMarker) {
  const start = source.indexOf(startMarker);
  assert.ok(start !== -1, `marker not found: ${startMarker}`);
  const end = source.indexOf(endMarker, start);
  assert.ok(end !== -1, `end marker not found after ${startMarker}: ${endMarker}`);
  return source.slice(start, end + endMarker.length);
}

test("EXT_TO_LANGUAGE covers the pi#71 expanded language set", () => {
  const map = sliceBetween("const EXT_TO_LANGUAGE", "function getLanguage(");

  // Named explicitly in pi#71 as previously unhighlighted.
  const expected = {
    rs: "rust",
    java: "java",
    kt: "kotlin",
    rb: "ruby",
    php: "php",
    toml: "toml",
    ini: "ini",
    cs: "csharp",
    swift: "swift",
    lua: "lua",
  };
  for (const [ext, language] of Object.entries(expected)) {
    assert.match(map, new RegExp(`\\b${ext}: "${language}"`), `${ext} -> ${language} missing from EXT_TO_LANGUAGE`);
  }

  // dockerfile detection stays on the existing special full-name-match path,
  // not the extension map.
  assert.match(source, /base === "dockerfile" \|\| base\.startsWith\("dockerfile\."\)\) return "dockerfile"/);

  // A representative sample of the broader "common language" additions.
  for (const [ext, language] of [
    ["scala", "scala"], ["groovy", "groovy"], ["hs", "haskell"],
    ["ex", "elixir"], ["clj", "clojure"], ["ps1", "powershell"],
    ["dart", "dart"], ["fs", "fsharp"], ["zig", "zig"], ["elm", "elm"],
  ]) {
    assert.match(map, new RegExp(`\\b${ext}: "${language}"`), `${ext} -> ${language} missing from EXT_TO_LANGUAGE`);
  }
});

test("previously-covered extensions are untouched", () => {
  const map = sliceBetween("const EXT_TO_LANGUAGE", "function getLanguage(");
  for (const [ext, language] of [
    ["ts", "typescript"], ["py", "python"], ["go", "go"], ["html", "html"],
    ["json", "json"], ["yaml", "yaml"], ["md", "markdown"], ["sh", "bash"],
    ["sql", "sql"], ["dockerfile", "dockerfile"], ["tf", "hcl"],
  ]) {
    assert.match(map, new RegExp(`\\b${ext}: "${language}"`), `${ext} -> ${language} regressed`);
  }
});
