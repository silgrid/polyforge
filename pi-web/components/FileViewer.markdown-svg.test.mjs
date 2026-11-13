import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import React from "react";
import ts from "typescript";
import { renderToStaticMarkup } from "react-dom/server";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const ReactMarkdown = (await jiti.import("react-markdown")).default;
const { SvgBlock } = await jiti.import("./SvgBlock.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const source = await readFile(new URL("./FileViewer.tsx", import.meta.url), "utf8");
const SVG_NS = "http://www.w3.org/2000/svg";
const svgSrc = `<svg width="200" height="70" xmlns="${SVG_NS}">
  <rect width="194" height="64" fill="#0ea5e9"/>
  <text x="100" y="42">SVG 中文</text>
</svg>`;

/**
 * Stands in for the browser's DOMParser, as in SvgBlock.test.mjs: the first
 * tag is the root, its xmlns its namespace, and a "<parsererror" one that
 * failed to parse.
 */
class FakeDOMParser {
  parseFromString(source, type) {
    assert.equal(type, "image/svg+xml");
    const tag = source.match(/<([a-zA-Z][\w:-]*)([^>]*)>/);
    const xmlns = tag?.[2].match(/\sxmlns="([^"]*)"/)?.[1];
    return {
      getElementsByTagName: (name) => (name === "parsererror" && source.includes("<parsererror") ? [{}] : []),
      documentElement: tag ? {
        localName: tag[1],
        namespaceURI: xmlns || null,
        hasAttribute: (name) => name === "xmlns" && xmlns !== undefined,
      } : null,
    };
  }
}

/**
 * The file preview's markdown renderer is inline in TextFileViewer, which
 * fetches its file on mount. Its real `components` map is taken from the
 * source and rendered through ReactMarkdown, so the fence routing under test
 * is the one the viewer ships — not a copy of it.
 */
function previewComponents() {
  const file = ts.createSourceFile("FileViewer.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const maps = [];
  const visit = (node) => {
    if (ts.isJsxOpeningElement(node) && node.tagName.getText(file) === "ReactMarkdown") {
      for (const attribute of node.attributes.properties) {
        if (ts.isJsxAttribute(attribute) && attribute.name.getText(file) === "components" && attribute.initializer?.expression) {
          maps.push(attribute.initializer.expression.getText(file));
        }
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(file);
  assert.equal(maps.length, 1, "the file preview must declare exactly one markdown components map");

  const { outputText } = ts.transpileModule(`return (${maps[0]});`, {
    compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022 },
    fileName: "FileViewer.tsx",
  });
  // The fence handlers under test only need the diagram/code blocks; the link
  // and image handlers keep their bindings but are never called here.
  const CodeBlock = ({ code, lang }) => React.createElement("pre", { "data-code-block": lang }, code);
  const MermaidBlock = () => null;
  const noop = () => null;
  return new Function(
    "React", "SvgBlock", "MermaidBlock", "CodeBlock", "cwd", "markdownDirectory", "sourceSessionId", "onOpenFile",
    "getFileApiUrl", "resolveLocalFileHref", "isExternalMarkdownHref", "shouldOpenLocalFileInApp", "parsePdfPageFragment", "jumpToMarkdownFragment",
    outputText,
  )(
    React, SvgBlock, MermaidBlock, CodeBlock, "/tmp/project", "/tmp/project", null, noop,
    (path) => path, () => null, () => false, () => true, () => null, () => {},
  );
}

function renderMarkdownFilePreview(markdown) {
  return renderToStaticMarkup(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(ReactMarkdown, { components: previewComponents() }, markdown),
    ),
  );
}

test("a ```svg fence in the markdown file preview renders as an image, not source", () => {
  const original = globalThis.DOMParser;
  globalThis.DOMParser = FakeDOMParser;
  try {
    const html = renderMarkdownFilePreview(`\`\`\`svg\n${svgSrc}\n\`\`\``);
    assert.match(html, /<img src="data:image\/svg\+xml;charset=utf-8,/);
    assert.doesNotMatch(html, /data-code-block|&lt;rect/);
    assert.match(renderMarkdownFilePreview(`\`\`\`SVG\n${svgSrc}\n\`\`\``), /<img src="data:image\/svg\+xml/);
    // Every other fence keeps the source view.
    const other = renderMarkdownFilePreview(`\`\`\`xml\n${svgSrc}\n\`\`\``);
    assert.doesNotMatch(other, /<img/);
    assert.match(other, /data-code-block="xml"/);
  } finally {
    if (original === undefined) delete globalThis.DOMParser;
    else globalThis.DOMParser = original;
  }
});