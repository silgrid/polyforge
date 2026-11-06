import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { PrismAsyncLight } from "react-syntax-highlighter";

import { resolveHighlightLanguage } from "../lib/prism-language.ts";

const source = await readFile(new URL("./FileViewer.tsx", import.meta.url), "utf8");

test("FileViewer wires the async full-registry Prism build, not the sync one", () => {
  assert.match(
    source,
    /import\s*\{\s*\n\s*PrismAsyncLight as SyntaxHighlighter,/,
    "FileViewer must import PrismAsyncLight (any refractor language, loaded on demand), not the sync Prism build",
  );
  assert.match(
    source,
    /language=\{language === "text" \? "plaintext" : resolveHighlightLanguage\(language\)\}/,
    "the language prop fed to the highlighter must go through resolveHighlightLanguage so refractor aliases (dockerfile, html, xml) still load",
  );
});

test("the line-number class contract used by SourceCodeRenderer is untouched", () => {
  assert.match(source, /"react-syntax-highlighter-line-number"/);
});

// Real highlighting output for languages that were previously outside the
// bundled ~30-language sync build. Awaiting preload()/loadLanguage() mirrors
// what happens client-side after componentDidMount fires: the AST generator
// (refractor/core) and each language grammar load lazily, and the async
// highlighter is a plain class component (not Suspense-based), so once those
// promises resolve a synchronous render already has real tokens.
for (const [dataLanguage, code] of [
  ["rust", "fn main() {\n    let x: i32 = 1;\n}\n"],
  ["java", "public class Foo {\n    void bar() { int x = 1; }\n}\n"],
  ["toml", "[section]\nkey = \"value\"\n"],
  ["dockerfile", "FROM node:20\nRUN echo hi\n"],
]) {
  test(`${dataLanguage} samples render real Prism token spans via the async build`, async () => {
    const prismLanguage = resolveHighlightLanguage(dataLanguage);
    await PrismAsyncLight.preload();
    await PrismAsyncLight.loadLanguage(prismLanguage);
    assert.ok(PrismAsyncLight.isRegistered(prismLanguage), `${prismLanguage} must be registered after loadLanguage`);

    const html = renderToStaticMarkup(
      React.createElement(PrismAsyncLight, { language: prismLanguage }, code),
    );

    assert.match(html, /class="token/, `${dataLanguage} (-> ${prismLanguage}) must produce token spans, not plain text`);
  });
}
