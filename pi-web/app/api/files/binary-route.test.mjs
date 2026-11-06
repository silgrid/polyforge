import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

const routeSource = await import("node:fs/promises").then(({ readFile }) =>
  readFile(new URL("./[...path]/route.ts", import.meta.url), "utf8"));

test("the read branch sniffs binaries at offset 0 and answers a typed 415 before reading text", () => {
  const readStart = routeSource.indexOf('if (type === "read")');
  const downloadStart = routeSource.indexOf('if (type === "download")');
  assert.ok(readStart !== -1 && downloadStart > readStart);
  const readBranch = routeSource.slice(readStart, downloadStart);

  // The sniff is gated to the first window (offset reads skip it).
  assert.match(readBranch, /if \(offset === 0 && fileLooksBinary\(filePath\)\)/);
  // Typed refusal carries everything the client panel needs: code, name, size.
  assert.match(readBranch, /error: "binaryFile", name: path\.basename\(filePath\), size: stat\.size/);
  assert.match(readBranch, /status: 415/);
  // The sniff precedes the text chunk read, so no garbage is ever parsed.
  assert.ok(
    readBranch.indexOf("fileLooksBinary(filePath)") < readBranch.indexOf("readTextPreviewChunk(filePath"),
    "binary sniff must run before the text chunk read",
  );
  // Shared helper comes from the text-preview module (single implementation).
  assert.match(routeSource, /import \{ fileLooksBinary, readTextPreviewChunk \} from "@\/lib\/text-preview";/);
});
