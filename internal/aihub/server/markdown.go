package server

// markdown.go: the one server-side Markdown renderer (
// carried into the console's JSON face by) — goldmark
// over the stored body with raw HTML OFF (a hostile corpus escapes
// into inert text, never executes) and one custom renderer that demotes
// headings one level (the page's own h1 is the object's title),
// renders links as non-clickable text (the anchor text plus the URL)
// and images as their alt text alone. No link is clickable, no remote
// resource is referenced or fetched; the console inserts the rendered
// HTML as already-inert markup.

import (
	"bytes"
	"html/template"
	"strconv"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// consoleMarkdown renders one stored body for a detail answer. Raw HTML
// stays escaped (unsafe is NOT enabled); tables render (the zebra-table
// visual spec); the plainLinkRenderer below wins over the default
// link/image/autolink/heading rendering.
var consoleMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.Table),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(
		util.Prioritized(&plainLinkRenderer{}, 100),
	)),
)

// renderMarkdownBody renders one stored body to the safe HTML the
// detail answers carry as pre-rendered content.
func renderMarkdownBody(body string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := consoleMarkdown.Convert([]byte(body), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// plainLinkRenderer keeps the detail page free of anything clickable or
// remote: links become styled text (anchor text + URL), images become
// their alt text, autolinks become the URL as text, and body headings
// are demoted one level so the memory's title stays the page's only h1.
type plainLinkRenderer struct{}

func (r *plainLinkRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindAutoLink, r.renderAutoLink)
	reg.Register(ast.KindImage, r.renderImage)
	reg.Register(ast.KindHeading, r.renderHeading)
}

// renderLink writes a link as non-clickable text: the anchor text
// (rendered by the default child renderers, escaped) followed by the
// URL — escaped, never an href.
func (r *plainLinkRenderer) renderLink(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		w.WriteString(`<span class="link-text">`)
		return ast.WalkContinue, nil
	}
	w.WriteString(` <span class="link-url">`)
	w.Write(util.EscapeHTML(n.(*ast.Link).Destination))
	w.WriteString(`</span></span>`)
	return ast.WalkContinue, nil
}

// renderAutoLink writes an autolinked URL as the same styled text.
func (r *plainLinkRenderer) renderAutoLink(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		w.WriteString(`<span class="link-text"><span class="link-url">`)
		w.Write(util.EscapeHTML(n.(*ast.AutoLink).URL(source)))
		w.WriteString(`</span></span>`)
	}
	return ast.WalkContinue, nil
}

// renderImage writes an image as its alt text alone (the default child
// renderers output the escaped alt text; no src, no fetch).
func (r *plainLinkRenderer) renderImage(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	return ast.WalkContinue, nil
}

// renderHeading demotes a body heading one level (h1 → h2 … capped at
// h6), so the memory's title in the header card is the page's only h1.
func (r *plainLinkRenderer) renderHeading(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	level := n.(*ast.Heading).Level + 1
	if level > 6 {
		level = 6
	}
	tag := "h" + strconv.Itoa(level)
	if entering {
		w.WriteString("<" + tag + ">")
	} else {
		w.WriteString("</" + tag + ">\n")
	}
	return ast.WalkContinue, nil
}
