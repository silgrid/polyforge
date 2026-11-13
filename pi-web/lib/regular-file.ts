import { closeSync, constants, fstatSync, openSync, readFileSync, readSync } from "node:fs";
import { open, type FileHandle } from "node:fs/promises";

/** Windows defines no O_NONBLOCK; it has no FIFOs either. */
const OPEN_FLAGS = constants.O_RDONLY | (constants.O_NONBLOCK || 0);

/**
 * The largest trusted project `.pi/settings.json` a read path such as
 * `GET /api/mcp` parses, as for the project's `.pi/mcp.json`. pi itself has no
 * limit; a settings file anywhere near this size is not one.
 */
export const PROJECT_SETTINGS_MAX_BYTES = 1024 * 1024;

function isMissing(error: unknown): boolean {
  const code = (error as NodeJS.ErrnoException | undefined)?.code;
  return code === "ENOENT" || code === "ENOTDIR";
}

/**
 * The text of the regular file at `path`, or undefined when nothing is there
 * (as `existsSync()` would say). A link is followed, as pi follows it. The
 * path is opened once without blocking and checked through what was opened,
 * so a FIFO, which a plain `readFileSync()` waits on until something writes
 * to it, or a device such as `/dev/zero` is never read: it throws instead, as
 * a file that cannot be read does. With `maxBytes`, a larger file throws too,
 * including one still growing past the limit while it is read.
 *
 * For a settings file a GET reads beside a session, such as a trusted
 * project's `.pi/settings.json`: anything there would otherwise stall the
 * whole server, since the read is synchronous. This synchronous form serves
 * the settings, skill and extension reads; a read path polled often enough
 * that blocking the event loop matters (the Polyforge cards/progress reader,
 *) uses `readRegularFileTextAsync`, the same rules on
 * `node:fs/promises`.
 */
export function readRegularFileText(path: string, maxBytes?: number): string | undefined {
  let fd: number;
  try {
    fd = openSync(path, OPEN_FLAGS);
  } catch (error) {
    if (isMissing(error)) return undefined;
    throw error;
  }
  try {
    const stats = fstatSync(fd);
    if (!stats.isFile()) throw new Error(`${path} is not a regular file`);
    if (maxBytes === undefined) return readFileSync(fd, "utf8");
    if (stats.size > maxBytes) throw new Error(`${path} is larger than ${maxBytes} bytes`);
    // One byte past the limit: a file still growing past it is refused rather than cut short.
    const limit = maxBytes + 1;
    const chunks: Buffer[] = [];
    let length = 0;
    while (length < limit) {
      const chunk = Buffer.alloc(Math.min(limit - length, 64 * 1024));
      const read = readSync(fd, chunk, 0, chunk.length, null);
      if (read === 0) break;
      chunks.push(chunk.subarray(0, read));
      length += read;
    }
    if (length > maxBytes) throw new Error(`${path} is larger than ${maxBytes} bytes`);
    return Buffer.concat(chunks, length).toString("utf8");
  } finally {
    closeSync(fd);
  }
}

/**
 * `readRegularFileText` on `node:fs/promises`: the same non-blocking open, the
 * same refusal of anything but a regular file, the same `maxBytes` cap and
 * one-byte-past-the-limit rule, without blocking the event loop while the
 * bytes are read.
 */
export async function readRegularFileTextAsync(path: string, maxBytes?: number): Promise<string | undefined> {
  let handle: FileHandle;
  try {
    handle = await open(path, OPEN_FLAGS);
  } catch (error) {
    if (isMissing(error)) return undefined;
    throw error;
  }
  try {
    const stats = await handle.stat();
    if (!stats.isFile()) throw new Error(`${path} is not a regular file`);
    if (maxBytes === undefined) return await handle.readFile("utf8");
    if (stats.size > maxBytes) throw new Error(`${path} is larger than ${maxBytes} bytes`);
    // One byte past the limit: a file still growing past it is refused rather than cut short.
    const limit = maxBytes + 1;
    const chunks: Buffer[] = [];
    let length = 0;
    while (length < limit) {
      const chunk = Buffer.alloc(Math.min(limit - length, 64 * 1024));
      const { bytesRead } = await handle.read(chunk, 0, chunk.length, null);
      if (bytesRead === 0) break;
      chunks.push(chunk.subarray(0, bytesRead));
      length += bytesRead;
    }
    if (length > maxBytes) throw new Error(`${path} is larger than ${maxBytes} bytes`);
    return Buffer.concat(chunks, length).toString("utf8");
  } finally {
    await handle.close();
  }
}
