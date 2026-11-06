/**
 * Text file save core (pi#81): the file viewer's edit mode writes whole
 * files, guarded three ways — a size cap, a binary sniff on the incoming
 * content, and a lost-update check against the mtime the client read.
 *
 * The write itself is atomic (temp file + rename in the same directory)
 * so a crash mid-save can never leave a half-written file behind.
 */

import fs from "node:fs";
import path from "node:path";
import { EDIT_MAX_BYTES } from "./file-types.ts";

export { EDIT_MAX_BYTES };

/** Same heuristic as lib/text-preview's NUL sniff, on in-memory content. */
export function contentLooksBinary(content: string): boolean {
  const window = Buffer.from(content.slice(0, 8192), "utf8");
  return window.includes(0);
}

export type SaveTextFileResult =
  | { status: "saved"; mtimeMs: number }
  | { status: "conflict"; currentMtimeMs: number }
  | { status: "too-large" }
  | { status: "binary" }
  | { status: "not-found" };

/**
 * Overwrite `filePath` with `content`, refusing when the file changed on
 * disk since `expectedMtimeMs` was read (lost-update guard). Temp file and
 * rename share the file's directory, so the swap is atomic on one volume.
 */
export function saveTextFileSafely(
  filePath: string,
  content: string,
  expectedMtimeMs: number,
): SaveTextFileResult {
  const bytes = Buffer.byteLength(content, "utf8");
  if (bytes > EDIT_MAX_BYTES) return { status: "too-large" };
  if (contentLooksBinary(content)) return { status: "binary" };

  let stat: fs.Stats;
  try {
    stat = fs.statSync(filePath);
  } catch {
    return { status: "not-found" };
  }
  if (!stat.isFile()) return { status: "not-found" };
  if (stat.mtimeMs !== expectedMtimeMs) {
    return { status: "conflict", currentMtimeMs: stat.mtimeMs };
  }

  const directory = path.dirname(filePath);
  const tempPath = path.join(directory, `.${path.basename(filePath)}.pf-edit-${process.pid}-${Date.now()}`);
  try {
    fs.writeFileSync(tempPath, content, { encoding: "utf8", mode: stat.mode });
    // Guard the rename against a mid-air delete of the target.
    try {
      fs.renameSync(tempPath, filePath);
    } catch (renameError) {
      if (!fs.existsSync(tempPath)) throw renameError;
      fs.rmSync(tempPath, { force: true });
      throw renameError;
    }
  } catch (error) {
    try {
      fs.rmSync(tempPath, { force: true });
    } catch {
      // best-effort cleanup; the failure below is the real signal
    }
    throw error;
  }

  return { status: "saved", mtimeMs: fs.statSync(filePath).mtimeMs };
}
