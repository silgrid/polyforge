// SanitizedHtml is the ONE place inert HTML enters the DOM: the
// server's goldmark renderer already produced it with raw HTML off,
// links as text and images as alt text. Everything else in the console
// renders as React text (escaped by construction).
export default function SanitizedHtml({ html }: { html: string }) {
  return <div className="prose-body" data-testid="sanitized-body" dangerouslySetInnerHTML={{ __html: html }} />
}
