import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

async function loadSubject() {
  return import("./image-attachments.ts");
}

const image = { type: "image", mimeType: "image/png", data: "YWJj" };

test("calculates padded base64 byte lengths and rejects invalid data", async () => {
  const { getBase64DecodedByteLength } = await loadSubject();

  assert.equal(getBase64DecodedByteLength("YQ=="), 1);
  assert.equal(getBase64DecodedByteLength("YWI="), 2);
  assert.equal(getBase64DecodedByteLength("YWJj"), 3);
  assert.equal(getBase64DecodedByteLength("not base64!"), null);
});

test("rejects invalid, oversized, and too many image attachments", async () => {
  const { MAX_ATTACHED_IMAGE_BYTES, MAX_ATTACHED_IMAGES, validateAgentImages } = await loadSubject();
  const oversizedData = "AAAA".repeat(Math.ceil((MAX_ATTACHED_IMAGE_BYTES + 1) / 3));

  assert.equal(validateAgentImages([image]), null);
  assert.match(validateAgentImages([{ ...image, mimeType: "text/plain" }]), /valid base64 image/);
  assert.match(validateAgentImages([{ ...image, data: oversizedData }]), /10MB/);
  assert.match(validateAgentImages(Array.from({ length: MAX_ATTACHED_IMAGES + 1 }, () => image)), /at most/);
});

test("claude images: PNG/JPEG/GIF/WebP whitelist plus pi's limits", async () => {
  const { CLAUDE_IMAGE_MIME_TYPES, MAX_ATTACHED_IMAGE_BYTES, MAX_ATTACHED_IMAGES, isClaudeImageMimeType, validateClaudeImages } = await loadSubject();
  assert.deepEqual([...CLAUDE_IMAGE_MIME_TYPES], ["image/png", "image/jpeg", "image/gif", "image/webp"]);
  for (const mimeType of CLAUDE_IMAGE_MIME_TYPES) {
    assert.equal(isClaudeImageMimeType(mimeType), true);
    assert.equal(validateClaudeImages([{ ...image, mimeType }]), null);
  }
  assert.equal(isClaudeImageMimeType("image/svg+xml"), false);
  assert.equal(isClaudeImageMimeType("image/bmp"), false);
  assert.equal(isClaudeImageMimeType(undefined), false);
  assert.equal(validateClaudeImages(undefined), null);
  assert.equal(validateClaudeImages(Array.from({ length: MAX_ATTACHED_IMAGES }, () => image)), null);

  const oversizedData = "AAAA".repeat(Math.ceil((MAX_ATTACHED_IMAGE_BYTES + 1) / 3));
  assert.match(validateClaudeImages(Array.from({ length: MAX_ATTACHED_IMAGES + 1 }, () => image)), /at most 10/);
  assert.match(validateClaudeImages([{ ...image, data: oversizedData }]), /10MB/);
  assert.match(validateClaudeImages([{ ...image, data: "not base64!" }]), /valid base64/);
  assert.match(validateClaudeImages([{ ...image, mimeType: "image/svg+xml" }]), /PNG, JPEG, GIF and WebP/);
  assert.match(validateClaudeImages([{ ...image, mimeType: "image/bmp" }]), /PNG, JPEG, GIF and WebP/);
  assert.match(validateClaudeImages("x"), /must be an array/);
  assert.match(validateClaudeImages([{ mimeType: "image/png", data: "YWJj" }]), /must be an image/);
});

test("pi's image gate is unchanged: any image/* type, including SVG, passes", async () => {
  const { validateAgentImages } = await loadSubject();
  assert.equal(validateAgentImages([{ ...image, mimeType: "image/svg+xml" }]), null);
  assert.equal(validateAgentImages([{ ...image, mimeType: "image/bmp" }]), null);
});
