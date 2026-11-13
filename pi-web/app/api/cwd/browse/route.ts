import { NextRequest, NextResponse } from "next/server";
import { stat } from "fs/promises";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import {
  createDirectory,
  getBrowseStartDirectory,
  getParentDirectory,
  listDirectories,
  listWindowsDrives,
  resolveDirectory,
  shouldShowWindowsDrivePicker,
} from "@/lib/directory-browser";
import { isRegistrableRoot } from "@/lib/root-registration-policy";

// GET /api/cwd/browse?path=...：列出文件系统中的可读子目录。
export async function GET(request: NextRequest) {
  try {
    const requested = request.nextUrl.searchParams.get("path")?.trim();

    if (shouldShowWindowsDrivePicker(requested)) {
      return NextResponse.json({
        path: "",
        parentPath: null,
        drives: await listWindowsDrives(),
        directories: [],
      });
    }

    const candidate = getBrowseStartDirectory(requested);

    let resolved: string;
    try {
      resolved = await resolveDirectory(candidate);
    } catch {
      return NextResponse.json({ error: "Directory does not exist" }, { status: 404 });
    }

    const directoryStat = await stat(resolved);
    if (!directoryStat.isDirectory()) {
      return NextResponse.json({ error: "Path is not a directory" }, { status: 400 });
    }

    // showHidden 在驱动器分支之后才读取：Windows 驱动器列表完全不受该参数
    // 影响。只有字面量 "true" 才显示隐藏目录；缺省或任何其他值一律隐藏。
    const directories = await listDirectories(resolved, {
      showHidden: request.nextUrl.searchParams.get("showHidden") === "true",
    });

    return NextResponse.json({
      path: resolved,
      parentPath: getParentDirectory(resolved),
      directories,
    });
  } catch (error) {
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}

// POST /api/cwd/browse  body: { path: string, name: string }
// Creates one direct child of the currently browsed directory.
export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Untrusted API request" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }

  try {
    const body = await request.json() as { path?: unknown; name?: unknown };
    const parentPath = typeof body.path === "string" ? body.path.trim() : "";
    const name = typeof body.name === "string" ? body.name.trim() : "";

    if (!parentPath) {
      return NextResponse.json({ error: "Parent path is required" }, { status: 400 });
    }
    if (!name) {
      return NextResponse.json({ error: "Directory name is required" }, { status: 400 });
    }

    // Creating a directory is only meaningful where a project root could be
    // registered; enforce the same registration policy as /api/cwd/validate
    // before creating anything, so the picker's mkdir cannot create children
    // in arbitrary server-writable locations — including through directory
    // symlinks — outside the registration prefixes (review blocker:
    // alternative unguarded mkdir endpoint).
    const resolvedParent = await resolveDirectory(parentPath).catch(() => null);
    if (!resolvedParent) {
      return NextResponse.json({ error: "Parent directory does not exist" }, { status: 404 });
    }
    const registrable = isRegistrableRoot(resolvedParent);
    if (!registrable.ok) {
      return NextResponse.json(
        { error: "Parent directory is outside the allowed registration prefixes" },
        { status: 403 },
      );
    }

    const createdPath = await createDirectory(resolvedParent, name);
    return NextResponse.json({ success: true, path: createdPath });
  } catch (error) {
    const code = error && typeof error === "object" && "code" in error
      ? String(error.code)
      : "";
    if (code === "EEXIST") {
      return NextResponse.json({ error: "A file or directory with this name already exists" }, { status: 409 });
    }
    if (code === "ENOENT") {
      return NextResponse.json({ error: "Parent directory does not exist" }, { status: 404 });
    }
    if (code === "EACCES" || code === "EPERM") {
      return NextResponse.json({ error: "Permission denied" }, { status: 403 });
    }
    if (code === "EINVAL" || code === "ENAMETOOLONG") {
      return NextResponse.json({ error: "Directory name is invalid" }, { status: 400 });
    }
    if (error instanceof Error && (
      error.message === "Directory name must be a single folder name"
      || error.message === "Parent path is not a directory"
    )) {
      return NextResponse.json({ error: error.message }, { status: 400 });
    }
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}
