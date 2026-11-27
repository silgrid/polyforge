/** Native Claude API endpoints must be absolute HTTP(S) URLs; never execute or resolve expressions here. */
export function isClaudeGatewayUrl(value: unknown): value is string {
  if (typeof value !== "string" || !value.trim()) return false;
  try {
    const url = new URL(value.trim());
    return (url.protocol === "http:" || url.protocol === "https:") && Boolean(url.hostname);
  } catch {
    return false;
  }
}
