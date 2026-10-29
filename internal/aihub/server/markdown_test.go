package server

// markdown_test.go (carried to /web/ by):
// the one server-side Markdown renderer — the hostile corpus produces
// no executable markup and no clickable external link (raw HTML is
// omitted outright, links render as text, images as alt text), body
// headings demote one level, and the rendered table/code visual shapes
// are present. (The HTTP-face half — the JSON detail carrying this
// rendered html — lives in web_api_test.go.)

import (
	"regexp"
	"strings"
	"testing"
)

// hostileCorpus is the spec's XSS fixture set: raw HTML payloads
// (script, img with an event handler, iframe, object, svg, form, a raw
// div with an onclick) and markdown links/images with remote,
// javascript: and data: destinations.
func hostileCorpus() string {
	return "# Hostile corpus\n\n" +
		"<script>alert('x')</script>\n\n" +
		"<img src=\"https://evil.example/pixel.png\" onerror=\"alert(1)\">\n\n" +
		"<iframe src=\"https://evil.example/frame\"></iframe>\n\n" +
		"<object data=\"https://evil.example/o\"></object>\n\n" +
		"<svg onload=\"alert(1)\"></svg>\n\n" +
		"<form action=\"https://evil.example/post\"><input name=\"x\"></form>\n\n" +
		"<div onclick=\"alert(1)\">raw html block</div>\n\n" +
		"[click me](https://evil.example/x)\n\n" +
		"[bad](javascript:alert(1))\n\n" +
		"![alt text](https://evil.example/i.png)\n\n" +
		"![badimg](data:text/html;base64,PHNjcmlwdD4=)\n\n" +
		"<https://evil.example/auto>\n"
}

func TestMarkdownHostileCorpusIsInert(t *testing.T) {
	out, err := renderMarkdownBody(hostileCorpus())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	lower := strings.ToLower(body)
	for _, bad := range []string{"<script", "<iframe", "<object", "<svg", "<form", "<img"} {
		if strings.Contains(lower, bad) {
			t.Fatalf("hostile body produced %q: %.1200s", bad, body)
		}
	}
	if m := regexp.MustCompile(`(?is)<[^>]*\son[a-z]+\s*=`).FindString(body); m != "" {
		t.Fatalf("hostile body produced an event-handler attribute %q: %.1200s", m, body)
	}
	if m := regexp.MustCompile(`(?is)<[^>]*(href|src)\s*=\s*["']\s*((https?:)?//|javascript:|data:)`).
		FindString(body); m != "" {
		t.Fatalf("hostile body produced an external/executable URL attribute %q: %.1200s", m, body)
	}
	for _, want := range []string{"click me", "https://evil.example/x", "alt text", "badimg"} {
		if !strings.Contains(body, want) {
			t.Fatalf("hostile body does not render %q as text: %.1200s", want, body)
		}
	}
}

func TestMarkdownDemotesHeadingsAndKeepsShapes(t *testing.T) {
	out, err := renderMarkdownBody("# Big\n\ntext\n\n## Sub\n\nmore\n\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n    code line\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(out)
	for _, want := range []string{"<h2>Big</h2>", "<h3>Sub</h3>", "<table>", "<th>", "<pre><code>"} {
		if !strings.Contains(s, want) {
			t.Fatalf("rendered body misses %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "<h1") {
		t.Fatalf("a body heading stayed h1 (the memory title owns h1):\n%s", s)
	}
	// H7-style deep headings cap at h6.
	out, err = renderMarkdownBody(strings.Repeat("#", 6) + " deep\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(out), "<h6>deep</h6>") {
		t.Fatalf("a level-6 heading did not cap at h6:\n%s", string(out))
	}
}
