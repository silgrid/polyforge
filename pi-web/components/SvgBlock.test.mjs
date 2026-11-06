import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { SvgBlock, svgDataUrl, svgImageSource } = await jiti.import("./SvgBlock.tsx");
const { MarkdownBody } = await jiti.import("./MarkdownBody.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const SVG_NS = "http://www.w3.org/2000/svg";
const svgSrc = `<svg width="200" height="70" xmlns="${SVG_NS}">
  <rect width="194" height="64" fill="#0ea5e9" onclick="alert(1)"/>
  <text x="100" y="42">SVG 中文</text>
</svg>`;

/**
 * Stands in for the browser's DOMParser (headless Chrome gave the same answers
 * for these inputs): the first tag is the root, its xmlns its namespace, and a
 * source with "<parsererror" one that failed to parse.
 */
class FakeDOMParser {
  constructor() { this.parsed = []; }
  parseFromString(source, type) {
    assert.equal(type, "image/svg+xml");
    this.parsed.push(source);
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

function render(props) {
  return renderToStaticMarkup(
    React.createElement(I18nProvider, null, React.createElement(SvgBlock, props)),
  );
}

function withDOMParser(fn) {
  const original = globalThis.DOMParser;
  globalThis.DOMParser = FakeDOMParser;
  try {
    return fn();
  } finally {
    if (original === undefined) delete globalThis.DOMParser;
    else globalThis.DOMParser = original;
  }
}

test("svgImageSource keeps an SVG document as it is", () => {
  const parser = new FakeDOMParser();
  assert.equal(svgImageSource(svgSrc, parser), svgSrc);
  assert.equal(parser.parsed.length, 1);
});

test("svgImageSource adds the xmlns an <svg> written for HTML leaves out", () => {
  const parser = new FakeDOMParser();
  const source = svgImageSource('<?xml version="1.0"?>\n<svg width="10" height="10"><rect/></svg>', parser);
  assert.equal(source, `<?xml version="1.0"?>\n<svg xmlns="${SVG_NS}" width="10" height="10"><rect/></svg>`);
  assert.equal(parser.parsed.length, 2);
});

test("svgImageSource refuses what is not an SVG document", () => {
  const parser = new FakeDOMParser();
  assert.equal(svgImageSource(`<svg xmlns="${SVG_NS}"><rect><parsererror/></svg>`, parser), null);
  assert.equal(svgImageSource("<div>hi</div>", parser), null);
  assert.equal(svgImageSource('<svg xmlns="http://example.com/x"/>', parser), null);
  assert.equal(svgImageSource("just text", parser), null);
});

test("svgDataUrl percent-encodes the source, # and quotes included", () => {
  const url = svgDataUrl('<svg fill="#fff">中</svg>');
  assert.equal(url, `data:image/svg+xml;charset=utf-8,${encodeURIComponent('<svg fill="#fff">中</svg>')}`);
  assert.doesNotMatch(url.slice(url.indexOf(",")), /[#"<]/);
  assert.equal(decodeURIComponent(url.slice(url.indexOf(",") + 1)), '<svg fill="#fff">中</svg>');
});

test("SvgBlock shows the source while streaming", () => {
  const html = withDOMParser(() => render({ code: svgSrc, isStreaming: true }));
  assert.match(html, /disabled/);
  assert.match(html, />Preview</);
  assert.match(html, /&lt;rect/);
  assert.doesNotMatch(html, /<img/);
});

test("SvgBlock previews the fence only as an <img>, never as markup", () => {
  const html = withDOMParser(() => render({ code: svgSrc }));
  assert.match(html, />Source</);
  assert.match(html, />SVG</);
  const src = html.match(/<img src="([^"]*)"/)[1];
  assert.equal(src, svgDataUrl(svgSrc));
  assert.doesNotMatch(html.replace(src, ""), /<svg|<rect|onclick/);
});

test("SvgBlock shows a notice for a fence that is not an SVG", () => {
  const html = withDOMParser(() => render({ code: "<div>not svg</div>" }));
  assert.match(html, /mermaid-block-error/);
  assert.match(html, /Invalid SVG image/);
  assert.doesNotMatch(html, /<img|>SVG</);
});

test("a ```svg fence in markdown renders through SvgBlock; other fences do not", () => {
  const markdown = (lang) => renderToStaticMarkup(
    React.createElement(I18nProvider, null, React.createElement(MarkdownBody, null, `\`\`\`${lang}\n${svgSrc}\n\`\`\``)),
  );
  const html = withDOMParser(() => markdown("svg"));
  assert.match(html, /<img src="data:image\/svg\+xml;charset=utf-8,/);
  assert.match(withDOMParser(() => markdown("SVG")), /<img src="data:image\/svg\+xml/);
  assert.doesNotMatch(withDOMParser(() => markdown("xml")), /<img/);
});

test("SvgBlock waits for the browser when there is no DOMParser", () => {
  const html = render({ code: svgSrc });
  assert.match(html, /mermaid-block-loading/);
  assert.doesNotMatch(html, /<img/);
});
