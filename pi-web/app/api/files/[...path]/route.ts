import { NextRequest, NextResponse } from "next/server";
import fs from "fs";
import path from "path";
import {
  allowFileRoot,
  getAllowedFileRoots,
  isExistingFilePathAllowed,
  isFilePathAllowed,
} from "@/lib/file-access";
import { checkLinkedDirectoryApproval, withOutsideLinkTargets } from "@/lib/linked-directory";
import { isRegistrableRoot } from "@/lib/root-registration-policy";
import {
  DOCX_PREVIEW_MAX_BYTES,
  IMAGE_PREVIEW_MAX_BYTES,
  documentPreviewKind,
  getAudioMime,
  getDocumentMime,
  getFileExt,
  getImageMime,
  getVideoMime,
} from "@/lib/file-types";
import { resolveDirentIsDirectory } from "@/lib/file-dirent";
import { getFileTreeVisibility, readIgnoredNames } from "@/lib/file-tree-visibility";
import { isFilePathReferencedBySession } from "@/lib/session-file-references";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import {
  inspectUploadTargets,
  parseUploadConflictStrategy,
  validateMkdirFolderName,
  validateNewFileName,
  replaceUploadFile,
  validateUploadFileNames,
} from "@/lib/file-upload";
import { parseFormDataWithinLimit, RequestBodyTooLargeError } from "@/lib/bounded-form-data";
import { filePathFromApiSegments, samePath } from "@/lib/paths";
import { hasParentDirectorySegment } from "@/lib/path-security";
import { fileLooksBinary, readTextPreviewChunk } from "@/lib/text-preview";
import { searchFileText } from "@/lib/file-search";
import { EDIT_MAX_BYTES, saveTextFileSafely } from "@/lib/file-edit";
import { registerFileWatch } from "@/lib/file-watch-registry";
import { pageStreamLabel, registerPageStream } from "@/lib/page-stream-registry";

// Build output directories are hidden from type=list by default, same as
// every other name lib/file-tree-visibility's getFileTreeVisibility excludes
// (it is the shared git-aware source of truth now; see that module), but
// dist/build can be surfaced on demand via ?showBuildOutputs=1|true so users
// can reach build artifacts (e.g. a debug APK under
// build/outputs/apk/debug/) through the explorer. The exemption applies to
// exactly these two names and only in the type=list branch; read/download/
// meta/preview/watch are never gated on visibility to begin with.
const BUILD_OUTPUT_NAMES = new Set(["dist", "build"]);

const FILE_REQUEST_TYPES = ["list", "read", "download", "meta", "preview", "search", "watch"] as const;
type FileRequestType = typeof FILE_REQUEST_TYPES[number];
const FILE_REQUEST_TYPE_SET = new Set<string>(FILE_REQUEST_TYPES);
const MAX_UPLOAD_FILE_BYTES = 25 * 1024 * 1024;
const MAX_UPLOAD_TOTAL_BYTES = 100 * 1024 * 1024;
// Multipart boundaries and headers are not file bytes, but must be bounded too.
const MAX_UPLOAD_REQUEST_BYTES = MAX_UPLOAD_TOTAL_BYTES + 1024 * 1024;

// The source view highlights every language here but text, pdf and word with
// the Prism grammar of that name (refractor/all); a name it lacks renders
// plain. "html" and "markdown" also turn on FileViewer's Preview, so templates
// that are not pages take "markup". `.m` stays plain: Objective-C and MATLAB
// both use it.
const EXT_TO_LANGUAGE: Record<string, string> = {
  ts: "typescript", tsx: "tsx", mts: "typescript", cts: "typescript",
  js: "javascript", jsx: "jsx", mjs: "javascript", cjs: "javascript",
  py: "python", rb: "ruby", php: "php", lua: "lua", pl: "perl", r: "r",
  go: "go", rs: "rust", java: "java", kt: "kotlin", kts: "kotlin", swift: "swift",
  scala: "scala", groovy: "groovy", gradle: "groovy", dart: "dart", zig: "zig",
  c: "c", cpp: "cpp", h: "c", hpp: "cpp", cs: "csharp", mm: "objectivec",
  ex: "elixir", exs: "elixir", hs: "haskell", ml: "ocaml", clj: "clojure", nix: "nix",
  html: "html", htm: "html", vue: "markup", svelte: "markup", astro: "markup",
  css: "css", scss: "scss", less: "less",
  json: "json", jsonl: "json", yaml: "yaml", yml: "yaml",
  toml: "toml", ini: "ini", cfg: "ini", properties: "properties",
  xml: "xml", md: "markdown", mdx: "markdown", tex: "latex",
  sh: "bash", bash: "bash", zsh: "bash", fish: "bash",
  ps1: "powershell", psm1: "powershell", bat: "batch", cmd: "batch",
  sql: "sql", graphql: "graphql", gql: "graphql", proto: "protobuf",
  dockerfile: "dockerfile", tf: "hcl", hcl: "hcl", diff: "diff", patch: "diff",
  env: "bash", gitignore: "bash", txt: "text",
  pdf: "pdf", docx: "word",
  // Expanded coverage (pi#71): extensions upstream's map does not name yet.
  // react-syntax-highlighter's async full-registry build (PrismAsyncLight)
  // loads any of these on demand.
  phtml: "php", pm: "perl", cljs: "clojure", cljc: "clojure", vim: "vim", fs: "fsharp", fsx: "fsharp", elm: "elm",
};

function getLanguage(filePath: string): string {
  const base = path.basename(filePath).toLowerCase();
  // Special full-name matches
  if (base === "dockerfile" || base.startsWith("dockerfile.")) return "dockerfile";
  if (base === ".env" || base.startsWith(".env.")) return "bash";
  if (base === "makefile" || base === "gnumakefile") return "makefile";
  const ext = base.split(".").pop() ?? "";
  return EXT_TO_LANGUAGE[ext] ?? "text";
}

function parseFileRequestType(value: string): FileRequestType | null {
  return FILE_REQUEST_TYPE_SET.has(value) ? (value as FileRequestType) : null;
}

async function getUploadDirectory(segments: string[]): Promise<
  { directory: string } | { response: NextResponse }
> {
  const directory = filePathFromApiSegments(segments);
  const allowedRoots = await getAllowedFileRoots();

  let stat: fs.Stats;
  try {
    stat = fs.statSync(directory);
  } catch {
    return { response: NextResponse.json({ error: "Upload directory not found" }, { status: 404 }) };
  }
  if (!stat.isDirectory()) {
    return { response: NextResponse.json({ error: "Upload target is not a directory" }, { status: 400 }) };
  }

  // Audit P1: reuse the shared symlink-safe containment helper instead of a
  // private realpath dance — isExistingFilePathAllowed resolves BOTH sides,
  // so a symlink inside an allowed root cannot redirect uploads outside it.
  if (!isExistingFilePathAllowed(directory, allowedRoots)) {
    return { response: NextResponse.json({ error: "Access denied" }, { status: 403 }) };
  }

  return { directory: fs.realpathSync(directory) };
}

// A directory link whose target is outside the allowed roots is listed but not
// browsable. Allowing it is the operator's explicit choice, the same one as
// selecting that directory as a workspace, and lasts until the server restarts.
async function allowLinkedDirectory(
  request: NextRequest,
  segments: string[],
): Promise<NextResponse> {
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  const body = await request.json().catch(() => null) as { target?: unknown } | null;
  if (typeof body?.target !== "string" || !body.target) {
    return NextResponse.json({ error: "target must be the link target shown in the listing" }, { status: 400 });
  }
  const approval = checkLinkedDirectoryApproval(
    filePathFromApiSegments(segments),
    body.target,
    await getAllowedFileRoots(),
  );
  if (!approval.ok) {
    return NextResponse.json({ error: approval.error }, { status: approval.status });
  }
  if (!approval.alreadyAllowed) {
    // Promotion into the process-wide roots follows the same registration
    // policy as /api/cwd/validate and /api/agent/new: the target's realpath
    // must lie inside a registration prefix (home, PI_WEB_ALLOWED_ROOT_PREFIXES,
    // or an already-registered root). Without this guard a repository symlink
    // pointing at / or any directory outside the prefixes could register that
    // entire target process-wide on one operator click (review blocker 1).
    const registrable = isRegistrableRoot(approval.target);
    if (!registrable.ok) {
      return NextResponse.json(
        { error: "Link target is outside the allowed registration prefixes" },
        { status: 403 },
      );
    }
    allowFileRoot(registrable.path);
  }
  return NextResponse.json({ path: approval.target });
}

function parseUploadFileNames(value: unknown): string[] | null {
  if (!Array.isArray(value) || !value.every((item) => typeof item === "string")) return null;
  return value;
}

export async function POST(
  request: NextRequest,
  { params }: { params: Promise<{ path: string[] }> }
) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Untrusted API request" }, { status: 403 });
  }

  try {
    const { path: segments } = await params;
    const type = request.nextUrl.searchParams.get("type") ?? "upload";

    if (type === "save") {
      // Edit-mode save (pi#81): overwrite an EXISTING text file with JSON
      // body { content, expectedMtimeMs }. Same trust gate as every POST
      // (isApiRequestAllowed above); the path must resolve to an existing
      // regular file inside an allowed root with BOTH sides realpath-checked
      // (isExistingFilePathAllowed), mirroring the audit-P1 upload chain.
      const filePath = filePathFromApiSegments(segments);
      const allowedRoots = await getAllowedFileRoots();
      let stat: fs.Stats;
      try {
        stat = fs.statSync(filePath);
      } catch {
        return NextResponse.json({ error: "File not found" }, { status: 404 });
      }
      if (!stat.isFile()) {
        return NextResponse.json({ error: "Not a file" }, { status: 400 });
      }
      if (!isExistingFilePathAllowed(filePath, allowedRoots)) {
        return NextResponse.json({ error: "Access denied" }, { status: 403 });
      }
      if (stat.size > EDIT_MAX_BYTES) {
        return NextResponse.json({ error: "File too large to edit (>512KB)" }, { status: 413 });
      }
      const body = await request.json().catch(() => null) as
        | { content?: unknown; expectedMtimeMs?: unknown }
        | null;
      if (typeof body?.content !== "string" || typeof body?.expectedMtimeMs !== "number") {
        return NextResponse.json(
          { error: "Body must be { content: string, expectedMtimeMs: number }" },
          { status: 400 },
        );
      }
      const result = saveTextFileSafely(filePath, body.content, body.expectedMtimeMs);
      if (result.status === "saved") {
        return NextResponse.json({ mtimeMs: result.mtimeMs });
      }
      if (result.status === "conflict") {
        return NextResponse.json(
          { error: "fileChangedOnDisk", currentMtimeMs: result.currentMtimeMs },
          { status: 409 },
        );
      }
      if (result.status === "too-large") {
        return NextResponse.json({ error: "File too large to edit (>512KB)" }, { status: 413 });
      }
      if (result.status === "binary") {
        return NextResponse.json({ error: "binaryContent" }, { status: 415 });
      }
      return NextResponse.json({ error: "File not found" }, { status: 404 });
    }

    if (type === "allow-link") return allowLinkedDirectory(request, segments);

    const uploadDirectory = await getUploadDirectory(segments);
    if ("response" in uploadDirectory) return uploadDirectory.response;
    const { directory } = uploadDirectory;

    if (type === "upload-check") {
      const body = await request.json().catch(() => null) as { fileNames?: unknown } | null;
      const fileNames = parseUploadFileNames(body?.fileNames);
      if (!fileNames) {
        return NextResponse.json({ error: "fileNames must be an array of strings" }, { status: 400 });
      }
      const validationError = validateUploadFileNames(fileNames);
      if (validationError) {
        return NextResponse.json({ error: validationError }, { status: 400 });
      }
      return NextResponse.json(inspectUploadTargets(directory, fileNames));
    }

    if (type === "mkdir") {
      // Create exactly `directory/<name>`: JSON body { name }. The security
      // machinery is the upload path's verbatim (isApiRequestAllowed above,
      // then getUploadDirectory's allowed-roots check against
      // realpathSync-resolved roots, so symlinks cannot redirect the write
      // outside an allowed root). recursive:false means intermediates are
      // never created; an existing target is a typed 409 with nothing
      // overwritten.
      const body = await request.json().catch(() => null) as { name?: unknown } | null;
      const name = typeof body?.name === "string" ? body.name : null;
      const validationError = validateMkdirFolderName(name);
      if (validationError) {
        return NextResponse.json({ error: validationError }, { status: 400 });
      }
      const destination = path.join(directory, name as string);
      try {
        fs.mkdirSync(destination, { recursive: false });
      } catch (error) {
        const code = (error as NodeJS.ErrnoException).code;
        if (code === "EEXIST" || code === "EISDIR" || code === "ENOTEMPTY") {
          return NextResponse.json(
            { error: "Folder already exists", conflict: true, path: destination },
            { status: 409 },
          );
        }
        throw error;
      }
      return NextResponse.json({ path: destination });
    }

    if (type === "create-file") {
      // Create exactly `directory/<name>` as an EMPTY file: JSON body
      // { name }. Same security machinery as mkdir (trust gate above, then
      // getUploadDirectory's allowed-roots + realpath symlink defense). The
      // name is validated BEFORE any path.join so an unsafe name can never
      // reach the filesystem; the write is exclusive (flag "wx") so an
      // existing target is a typed 409 with nothing overwritten.
      const body = await request.json().catch(() => null) as { name?: unknown } | null;
      const name = typeof body?.name === "string" ? body.name : null;
      const validationError = validateNewFileName(name);
      if (validationError) {
        return NextResponse.json({ error: validationError }, { status: 400 });
      }
      const destination = path.join(directory, name as string);
      try {
        fs.writeFileSync(destination, "", { flag: "wx" });
      } catch (error) {
        const code = (error as NodeJS.ErrnoException).code;
        if (code === "EEXIST" || code === "EISDIR" || code === "ENOTEMPTY") {
          return NextResponse.json(
            { error: "File already exists", conflict: true, path: destination },
            { status: 409 },
          );
        }
        throw error;
      }
      return NextResponse.json({ path: destination });
    }

    if (type !== "upload") {
      return NextResponse.json({ error: "Invalid upload request type" }, { status: 400 });
    }

    const strategy = parseUploadConflictStrategy(request.nextUrl.searchParams.get("conflict"));
    if (!strategy) {
      return NextResponse.json({ error: "Invalid conflict strategy" }, { status: 400 });
    }

    let formData: FormData;
    try {
      formData = await parseFormDataWithinLimit(request, MAX_UPLOAD_REQUEST_BYTES);
    } catch (error) {
      if (error instanceof RequestBodyTooLargeError) {
        return NextResponse.json({ error: "Uploads must total 100MB or less" }, { status: 413 });
      }
      throw error;
    }
    const files = formData.getAll("files").filter((entry): entry is File => typeof entry !== "string");
    if (files.some((file) => file.size > MAX_UPLOAD_FILE_BYTES)) {
      return NextResponse.json({ error: "Each upload must be 25MB or smaller" }, { status: 413 });
    }
    if (files.reduce((total, file) => total + file.size, 0) > MAX_UPLOAD_TOTAL_BYTES) {
      return NextResponse.json({ error: "Uploads must total 100MB or less" }, { status: 413 });
    }
    const fileNames = files.map((file) => file.name);
    const validationError = validateUploadFileNames(fileNames);
    if (validationError) {
      return NextResponse.json({ error: validationError }, { status: 400 });
    }

    const inspection = inspectUploadTargets(directory, fileNames);
    if (strategy === "error" && inspection.conflicts.length > 0) {
      return NextResponse.json({
        error: "One or more files already exist",
        conflicts: inspection.conflicts,
        nonReplaceable: inspection.nonReplaceable,
      }, { status: 409 });
    }

    const conflictSet = new Set(inspection.conflicts);
    const nonReplaceableSet = new Set(inspection.nonReplaceable);
    const uploaded: string[] = [];
    const skipped: string[] = [];
    const errors: Array<{ name: string; error: string }> = [];

    for (const file of files) {
      const destination = path.join(directory, file.name);
      if (conflictSet.has(file.name) && strategy === "skip") {
        skipped.push(file.name);
        continue;
      }
      if (conflictSet.has(file.name) && nonReplaceableSet.has(file.name)) {
        errors.push({ name: file.name, error: "Cannot replace a directory or symbolic link" });
        continue;
      }

      let bytes: Buffer;
      try {
        bytes = Buffer.from(await file.arrayBuffer());
      } catch (error) {
        errors.push({ name: file.name, error: error instanceof Error ? error.message : String(error) });
        continue;
      }

      try {
        if (conflictSet.has(file.name)) {
          replaceUploadFile(destination, bytes);
        } else {
          fs.writeFileSync(destination, bytes, { flag: "wx" });
        }
        uploaded.push(file.name);
      } catch (error) {
        errors.push({ name: file.name, error: error instanceof Error ? error.message : String(error) });
      }
    }

    return NextResponse.json(
      { uploaded, skipped, errors },
      { status: errors.length > 0 ? 207 : 200 },
    );
  } catch (error) {
    return NextResponse.json({ error: error instanceof Error ? error.message : String(error) }, { status: 500 });
  }
}

function createFileBodyStream(filePath: string, range?: { start: number; end: number }): ReadableStream<Uint8Array> {
  const fileStream = fs.createReadStream(filePath, range);
  let closed = false;

  return new ReadableStream<Uint8Array>({
    start(controller) {
      fileStream.on("data", (chunk: Buffer) => {
        if (closed) return;
        try {
          controller.enqueue(new Uint8Array(chunk));
        } catch {
          closed = true;
          fileStream.destroy();
        }
      });
      fileStream.once("end", () => {
        if (closed) return;
        closed = true;
        try {
          controller.close();
        } catch {
          // The browser may cancel media probes before the file stream ends.
        }
      });
      fileStream.once("error", (error) => {
        if (closed) return;
        closed = true;
        try {
          controller.error(error);
        } catch {
          // The response was already abandoned by the client.
        }
      });
    },
    cancel() {
      closed = true;
      fileStream.destroy();
    },
  });
}

function encodeHeaderValue(value: string): string {
  return encodeURIComponent(value).replace(/[!'()*]/g, (ch) =>
    `%${ch.charCodeAt(0).toString(16).toUpperCase()}`
  );
}

function getContentDisposition(filePath: string, asDownload = false): string {
  const disposition = asDownload ? "attachment" : "inline";
  const fileName = path.basename(filePath);
  const fallback = fileName.replace(/[^\x20-\x7E]|["\\;\r\n]/g, "_") || "download";
  return `${disposition}; filename="${fallback}"; filename*=UTF-8''${encodeHeaderValue(fileName)}`;
}

function streamFile(filePath: string, stat: fs.Stats, contentType: string, rangeHeader: string | null, asDownload = false): Response {
  const headers: Record<string, string> = {
    "Content-Type": contentType,
    "Cache-Control": "no-cache",
    "Accept-Ranges": "bytes",
    "Content-Disposition": getContentDisposition(filePath, asDownload),
    "X-Content-Type-Options": "nosniff",
  };
  // SVG is the only preview type a browser executes as a document. A
  // repo-controlled SVG navigated to directly (for example through a link in
  // a transcript) would otherwise run script in the Pi Web origin, where it
  // can call any /api route. These headers only affect document rendering;
  // <img> preview embedding ignores them.
  if (contentType === "image/svg+xml") {
    headers["Content-Security-Policy"] =
      "default-src 'none'; img-src data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'";
    headers["Referrer-Policy"] = "no-referrer";
  }

  if (!rangeHeader) {
    return new Response(createFileBodyStream(filePath), {
      headers: {
        ...headers,
        "Content-Length": String(stat.size),
      },
    });
  }

  const match = /^bytes=(\d*)-(\d*)$/.exec(rangeHeader);
  if (!match) {
    return new Response(null, {
      status: 416,
      headers: {
        ...headers,
        "Content-Range": `bytes */${stat.size}`,
      },
    });
  }

  let start = match[1] ? Number(match[1]) : 0;
  let end = match[2] ? Number(match[2]) : stat.size - 1;
  if (!match[1] && match[2]) {
    const suffixLength = Number(match[2]);
    start = Math.max(stat.size - suffixLength, 0);
    end = stat.size - 1;
  }

  if (!Number.isFinite(start) || !Number.isFinite(end) || start < 0 || end < start || start >= stat.size) {
    return new Response(null, {
      status: 416,
      headers: {
        ...headers,
        "Content-Range": `bytes */${stat.size}`,
      },
    });
  }

  end = Math.min(end, stat.size - 1);
  const chunkSize = end - start + 1;
  return new Response(createFileBodyStream(filePath, { start, end }), {
    status: 206,
    headers: {
      ...headers,
      "Content-Length": String(chunkSize),
      "Content-Range": `bytes ${start}-${end}/${stat.size}`,
    },
  });
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function wrapDocxPreviewHtml(bodyHtml: string, fileName: string): string {
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  :root { color-scheme: light; }
  html, body { margin: 0; min-height: 100%; background: #eef1f5; color: #171717; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; padding: 28px; }
  main {
    box-sizing: border-box;
    max-width: 840px;
    min-height: calc(100vh - 56px);
    margin: 0 auto;
    padding: 56px 64px;
    background: #fff;
    box-shadow: 0 8px 28px rgba(15, 23, 42, 0.14);
  }
  .file-title {
    margin: 0 0 28px;
    padding-bottom: 10px;
    border-bottom: 1px solid #e5e7eb;
    color: #6b7280;
    font: 12px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    word-break: break-word;
  }
  h1, h2, h3, h4, h5, h6 { line-height: 1.3; margin: 1.1em 0 0.45em; color: #111827; }
  p { margin: 0.65em 0; line-height: 1.7; }
  table { border-collapse: collapse; max-width: 100%; margin: 1em 0; }
  th, td { border: 1px solid #d1d5db; padding: 6px 9px; vertical-align: top; }
  img { max-width: 100%; height: auto; }
  pre { white-space: pre-wrap; overflow-wrap: anywhere; }
  a { color: #2563eb; }
  @media (max-width: 720px) {
    body { padding: 0; background: #fff; }
    main { min-height: 100vh; padding: 28px 22px; box-shadow: none; }
  }
</style>
</head>
<body>
<main>
<div class="file-title">${escapeHtml(fileName)}</div>
${bodyHtml}
</main>
</body>
</html>`;
}

export async function GET(
  request: NextRequest,
  { params }: { params: Promise<{ path: string[] }> }
) {
  try {
    const { path: segments } = await params;
    const filePath = filePathFromApiSegments(segments);
    // Authorization collapses `..` lexically, but the filesystem applies it
    // after following links, so `link/../x` names a file beside the link's
    // target, outside the roots. URL parsing already drops real `..` segments;
    // only an encoded slash inside a segment still carries one here. The
    // existing-path check refuses them too, but a file referenced by the
    // session skips that check.
    if (hasParentDirectorySegment(filePath)) {
      return NextResponse.json({ error: "Access denied" }, { status: 403 });
    }
    const rawType = request.nextUrl.searchParams.get("type") ?? "list";
    const type = parseFileRequestType(rawType);
    if (!type) {
      return NextResponse.json({ error: "Invalid file request type" }, { status: 400 });
    }
    const sessionId = request.nextUrl.searchParams.get("sessionId");

    const allowedRoots = await getAllowedFileRoots();
    const allowedByRoot = isFilePathAllowed(filePath, allowedRoots);
    const allowedBySessionReference =
      !allowedByRoot &&
      type !== "list" &&
      await isFilePathReferencedBySession(filePath, sessionId);
    if (!allowedByRoot && !allowedBySessionReference) {
      return NextResponse.json({ error: "Access denied" }, { status: 403 });
    }

    let stat: fs.Stats | undefined;
    try {
      stat = fs.statSync(filePath);
    } catch {
      if (type !== "watch") {
        return NextResponse.json({ error: "Not found" }, { status: 404 });
      }
    }

    const existingAuthorizationPath = stat ? filePath : path.dirname(filePath);
    if (
      !allowedBySessionReference
      && !isExistingFilePathAllowed(existingAuthorizationPath, allowedRoots)
    ) {
      return NextResponse.json({ error: "Access denied" }, { status: 403 });
    }

    if (type === "read") {
      if (!stat?.isFile()) {
        return NextResponse.json({ error: "Not a file" }, { status: 400 });
      }
      const imageMime = getImageMime(filePath);
      if (imageMime) {
        if (stat.size > IMAGE_PREVIEW_MAX_BYTES) {
          return NextResponse.json({ error: "Image too large (>10MB)" }, { status: 413 });
        }
        return streamFile(filePath, stat, imageMime, request.headers.get("range"));
      }
      const audioMime = getAudioMime(filePath);
      if (audioMime) {
        return streamFile(filePath, stat, audioMime, request.headers.get("range"));
      }
      const videoMime = getVideoMime(filePath);
      if (videoMime) {
        return streamFile(filePath, stat, videoMime, request.headers.get("range"));
      }
      const documentMime = getDocumentMime(filePath);
      if (documentMime) {
        return streamFile(filePath, stat, documentMime, request.headers.get("range"));
      }
      const rawOffset = request.nextUrl.searchParams.get("offset");
      if (rawOffset !== null && !/^\d+$/.test(rawOffset)) {
        return NextResponse.json({ error: "Invalid text preview offset" }, { status: 400 });
      }
      const offset = Number(rawOffset ?? 0);
      if (!Number.isSafeInteger(offset) || offset > stat.size) {
        return NextResponse.json({ error: "Invalid text preview offset" }, { status: 400 });
      }
      // Unrecognized-type binaries would render as UTF-8 mojibake in the text
      // viewer (owner report, pi#75): sniff the leading window for a NUL byte
      // and answer a typed refusal carrying the download pointer instead.
      // Offset reads skip the sniff — the client panel replaces the whole view.
      if (offset === 0 && fileLooksBinary(filePath)) {
        return NextResponse.json(
          { error: "binaryFile", name: path.basename(filePath), size: stat.size },
          { status: 415 },
        );
      }
      const chunk = readTextPreviewChunk(filePath, stat.size, offset);
      const language = getLanguage(filePath);
      // mtimeMs anchors the edit mode's lost-update guard (pi#81).
      return NextResponse.json({ ...chunk, language, size: stat.size, mtimeMs: stat.mtimeMs, mtime: stat.mtime.toISOString() });
    }

    if (type === "search") {
      if (!stat?.isFile()) {
        return NextResponse.json({ error: "Not a file" }, { status: 400 });
      }
      const query = (request.nextUrl.searchParams.get("q") ?? "").trim();
      if (!query) {
        return NextResponse.json({ matches: [], truncated: false });
      }
      if (query.length > 200) {
        return NextResponse.json({ error: "Search query exceeds 200 characters" }, { status: 400 });
      }
      if (fileLooksBinary(filePath)) {
        // Same typed refusal shape the read branch answers with (pi#75):
        // binary files have no text to search.
        return NextResponse.json(
          { error: "binaryFile", name: path.basename(filePath), size: stat.size },
          { status: 415 },
        );
      }
      const rawOffset = request.nextUrl.searchParams.get("offset");
      if (rawOffset !== null && !/^\d+$/.test(rawOffset)) {
        return NextResponse.json({ error: "Invalid search offset" }, { status: 400 });
      }
      const offset = Number(rawOffset ?? 0);
      if (!Number.isSafeInteger(offset) || offset > stat.size) {
        return NextResponse.json({ error: "Invalid search offset" }, { status: 400 });
      }
      const response = await searchFileText(filePath, query, {
        offset,
        caseSensitive: request.nextUrl.searchParams.has("case"),
        regex: request.nextUrl.searchParams.has("regex"),
        signal: request.signal,
      });
      return NextResponse.json(response);
    }

    if (type === "download") {
      if (!stat?.isFile()) {
        return NextResponse.json({ error: "Not a file" }, { status: 400 });
      }
      const mime = getImageMime(filePath) || getAudioMime(filePath) || getVideoMime(filePath) || getDocumentMime(filePath) || "application/octet-stream";
      return streamFile(filePath, stat, mime, request.headers.get("range"), true);
    }

    if (type === "meta") {
      if (!stat?.isFile()) {
        return NextResponse.json({ error: "Not a file" }, { status: 400 });
      }
      const imageMime = getImageMime(filePath);
      const audioMime = getAudioMime(filePath);
      const videoMime = getVideoMime(filePath);
      const documentMime = getDocumentMime(filePath);
      return NextResponse.json({
        size: stat.size,
        // The watch's `change` events carry the same pair (lib/file-version.ts).
        mtime: stat.mtime.toISOString(),
        language: getLanguage(filePath),
        mime: imageMime || audioMime || videoMime || documentMime || "text/plain",
        previewKind: documentPreviewKind(filePath),
      });
    }

    if (type === "preview") {
      if (!stat?.isFile()) {
        return NextResponse.json({ error: "Not a file" }, { status: 400 });
      }
      if (getFileExt(filePath) !== "docx") {
        return NextResponse.json({ error: "Preview not available for this file type" }, { status: 400 });
      }
      if (stat.size > DOCX_PREVIEW_MAX_BYTES) {
        return NextResponse.json({ error: "DOCX too large for preview (>10MB)" }, { status: 413 });
      }

      const mammoth = await import("mammoth");
      const result = await mammoth.convertToHtml(
        { path: filePath },
        {
          externalFileAccess: false,
          convertImage: mammoth.images.dataUri,
        }
      );
      const html = wrapDocxPreviewHtml(result.value, path.basename(filePath));
      return new Response(html, {
        headers: {
          "Content-Type": "text/html; charset=utf-8",
          "Cache-Control": "no-cache",
          "Content-Security-Policy": "default-src 'none'; img-src data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'",
          "Referrer-Policy": "no-referrer",
          "X-Content-Type-Options": "nosniff",
        },
      });
    }

    if (type === "watch") {
      if (stat && !stat.isFile()) {
        return NextResponse.json({ error: "Not a file" }, { status: 400 });
      }
      let watcher: fs.FSWatcher | null = null;
      // The debug panel's count of open watches; released when the watcher fails or the client cancels.
      let releaseWatch: (() => void) | null = null;
      let lastMtimeMs = stat?.mtimeMs ?? 0;
      let lastCtimeMs = stat?.ctimeMs ?? 0;
      let lastIno = stat?.ino ?? 0;
      let lastSize = stat?.size ?? 0;
      let lastExists = stat !== undefined;
      const stream = new ReadableStream({
        start(controller) {
          const send = (eventName: string, data: Record<string, unknown>) => {
            const payload = `event: ${eventName}\ndata: ${JSON.stringify(data)}\n\n`;
            try {
              controller.enqueue(new TextEncoder().encode(payload));
            } catch {
              // client disconnected
            }
          };
          try {
            const watchedDirectory = path.dirname(filePath);
            watcher = fs.watch(watchedDirectory, (_eventType, changedName) => {
              if (
                changedName != null
                && !samePath(path.join(watchedDirectory, changedName.toString()), filePath)
              ) return;
              try {
                const s = fs.statSync(filePath);
                // Some platforms emit watch events for file reads/attribute
                // access. Ignore those or the client's refresh read loops.
                if (
                  lastExists
                  && s.mtimeMs === lastMtimeMs
                  && s.ctimeMs === lastCtimeMs
                  && s.ino === lastIno
                  && s.size === lastSize
                ) return;
                lastExists = true;
                lastMtimeMs = s.mtimeMs;
                lastCtimeMs = s.ctimeMs;
                lastIno = s.ino;
                lastSize = s.size;
                send("change", { mtime: s.mtime.toISOString(), size: s.size });
              } catch {
                if (!lastExists) return;
                lastExists = false;
                send("change", { mtime: new Date().toISOString(), size: 0 });
              }
            });
            watcher.on("error", () => {
              try { watcher?.close(); } catch { /* ignore */ }
              watcher = null;
              releaseWatch?.();
              try { controller.close(); } catch { /* ignore */ }
            });
            // Counted, and listed under the page that opened it (`?holder=`).
            const releaseCount = registerFileWatch();
            const releasePage = registerPageStream(pageStreamLabel("file-watch", request));
            releaseWatch = () => {
              releaseCount();
              releasePage();
            };
            // A client gone without a cancel (a phone off the network) would keep
            // the watcher, and its place in the debug panel's count, forever.
            const onAbort = () => {
              try { watcher?.close(); } catch { /* ignore */ }
              watcher = null;
              releaseWatch?.();
              try { controller.close(); } catch { /* ignore */ }
            };
            if (request.signal.aborted) {
              onAbort();
              return;
            }
            request.signal.addEventListener("abort", onAbort, { once: true });
            // The client snapshots only after this event, so emit it after the
            // watcher exists to avoid dropping changes between those steps.
            send("connected", { filePath });
          } catch {
            send("error", { message: "Failed to watch file" });
            controller.close();
          }
        },
        cancel() {
          try { watcher?.close(); } catch { /* ignore */ }
          releaseWatch?.();
        },
      });
      return new Response(stream, {
        headers: {
          "Content-Type": "text/event-stream",
          "Cache-Control": "no-cache, no-transform",
          Connection: "keep-alive",
          "X-Accel-Buffering": "no",
        },
      });
    }

    // type === "list"
    if (!stat?.isDirectory()) {
      return NextResponse.json({ error: "Not a directory" }, { status: 400 });
    }

    // Opt-in surfacing of build output directories (see BUILD_OUTPUT_NAMES).
    // Read only here: the parameter never affects any other request type.
    const rawShowBuildOutputs = request.nextUrl.searchParams.get("showBuildOutputs");
    const showBuildOutputs = rawShowBuildOutputs === "1" || rawShowBuildOutputs === "true";
    // Opt-in surfacing of git-ignored entries (same read-only contract): the
    // ignored entries are included and marked additively (`ignored: true`) so
    // the client can dim them. Nothing about the default listing changes.
    const rawShowIgnored = request.nextUrl.searchParams.get("showIgnored");
    const showIgnored = rawShowIgnored === "1" || rawShowIgnored === "true";

    // Avoid per-entry stat calls for normal files and directories. Symlinks and
    // filesystems without directory type information use the stat fallback.
    const dirents = fs.readdirSync(filePath, { withFileTypes: true });
    const isVisible = await getFileTreeVisibility(filePath, dirents.map((d) => d.name));
    // Computed only under the opt-in: the extra git call is never paid by a
    // default listing. An empty set wherever git has no view (outside a work
    // tree, git failure) — nothing extra surfaces there.
    const ignoredNames = showIgnored
      ? await readIgnoredNames(filePath, dirents.map((d) => d.name))
      : new Set<string>();
    const entries = dirents
      .filter((d) =>
        isVisible(d.name)
        || (BUILD_OUTPUT_NAMES.has(d.name) && showBuildOutputs)
        || (showIgnored && ignoredNames.has(d.name)),
      )
      .flatMap((d) => {
        const isDir = resolveDirentIsDirectory(d, path.join(filePath, d.name));
        if (isDir === null) return [];
        const entry = { name: d.name, isDir, size: 0, modified: "" };
        // Additive marker, present only on ignored entries under the opt-in,
        // so the default entry shape is byte-identical to before.
        if (showIgnored && ignoredNames.has(d.name)) return [{ ...entry, ignored: true }];
        return [entry];
      })
      .sort((a, b) => {
        // Dirs first, then files, both alphabetically
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        return a.name.localeCompare(b.name);
      });

    return NextResponse.json({
      entries: withOutsideLinkTargets(filePath, entries, dirents, allowedRoots),
      path: filePath,
    });
  } catch (error) {
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}
