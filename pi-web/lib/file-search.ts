/**
 * Server-side text file search (pi#80) for the file viewer's
 * "search the rest of the file" affordance: the viewer renders a preview
 * chunk (head of the file), so matches beyond it need a server scan.
 *
 * Streams from a byte offset (the viewer passes the nextOffset it has
 * already loaded, so local matches are never double-reported), bounded by
 * a scan budget and a match cap like lib/session-search.
 */

import { createReadStream } from "node:fs";
import { createInterface } from "node:readline";

export interface FileSearchMatch {
  /** 1-based line number within the WHOLE file. */
  line: number;
  /** Byte offset of the start of the matching line. */
  byteOffset: number;
  /** ±context snippet around the match (single line, ellipsized). */
  snippet: string;
}

export interface FileSearchResponse {
  matches: FileSearchMatch[];
  truncated: boolean;
}

const MAX_MATCHES = 50;
const MAX_SCAN_BYTES = 16 * 1024 * 1024;
const MAX_LINE_CHARS = 1024 * 1024;
const SNIPPET_RADIUS = 60;
const TIME_BUDGET_MS = 3000;

function buildRegex(query: string, caseSensitive: boolean, regex: boolean): RegExp {
  const source = regex ? query : query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(source, caseSensitive ? "g" : "gi");
}

function snippetAround(line: string, index: number, length: number): string {
  const start = Math.max(0, index - SNIPPET_RADIUS);
  const end = Math.min(line.length, index + length + SNIPPET_RADIUS);
  const prefix = start > 0 ? "…" : "";
  const suffix = end < line.length ? "…" : "";
  return `${prefix}${line.slice(start, end)}${suffix}`.replace(/\s+/g, " ").trim();
}

/**
 * Search a text file from `offset`. `startLine` is the 1-based line the
 * offset lands on (callers that begin mid-file must count what they
 * skipped; the viewer path always passes whole-line boundaries, so the
 * first streamed line IS a full line).
 */
export async function searchFileText(
  filePath: string,
  query: string,
  options: {
    offset?: number;
    caseSensitive?: boolean;
    regex?: boolean;
    startLine?: number;
    signal?: AbortSignal;
  } = {},
): Promise<FileSearchResponse> {
  const needle = query.trim();
  if (!needle) return { matches: [], truncated: false };
  if (needle.length > 200) throw new RangeError("Search query exceeds 200 characters");
  const regexp = buildRegex(needle, Boolean(options.caseSensitive), Boolean(options.regex));
  const offset = Math.max(0, options.offset ?? 0);
  const deadline = Date.now() + TIME_BUDGET_MS;
  const timeout = AbortSignal.timeout(TIME_BUDGET_MS);
  const signal = options.signal ? AbortSignal.any([options.signal, timeout]) : timeout;

  const matches: FileSearchMatch[] = [];
  let truncated = false;
  const stream = createReadStream(filePath, {
    encoding: "utf8",
    start: offset,
    end: offset + MAX_SCAN_BYTES - 1,
    signal,
  });
  const lines = createInterface({ input: stream, crlfDelay: Infinity });
  let byteOffset = offset;
  let lineNumber = Math.max(1, options.startLine ?? 1);

  try {
    for await (const line of lines) {
      if (signal.aborted || Date.now() >= deadline || matches.length >= MAX_MATCHES) {
        truncated = true;
        break;
      }
      if (line.length > MAX_LINE_CHARS) {
        byteOffset += Buffer.byteLength(line, "utf8") + 1;
        lineNumber++;
        continue;
      }
      if (line.length > 0) {
        regexp.lastIndex = 0;
        let match: RegExpExecArray | null;
        while ((match = regexp.exec(line)) !== null) {
          matches.push({
            line: lineNumber,
            byteOffset,
            snippet: snippetAround(line, match.index, match[0].length),
          });
          if (match[0].length === 0) regexp.lastIndex++;
          if (matches.length >= MAX_MATCHES) break;
        }
      }
      byteOffset += Buffer.byteLength(line, "utf8") + 1;
      lineNumber++;
    }
  } finally {
    lines.close();
    stream.destroy();
  }

  return { matches, truncated };
}
