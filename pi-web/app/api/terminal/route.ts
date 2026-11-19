import { stat } from "fs/promises";
import { resolve } from "path";
import { NextResponse } from "next/server";
import { getAllowedFileRoots, isExistingFilePathAllowed } from "@/lib/file-access";
import { resolveLoginCommand } from "@/lib/harness-login-commands";
import { createLoginTerminal, createTerminal } from "@/lib/terminal-manager";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(req: Request) {
  try {
    const body = await req.json() as { id?: unknown; cwd?: unknown; cols?: unknown; rows?: unknown; login?: unknown };
    if (body.id !== undefined && (typeof body.id !== "string" || !/^[a-f0-9]{32}$/.test(body.id))) {
      return NextResponse.json({ error: "Invalid terminal id" }, { status: 400 });
    }
    if (body.login !== undefined) {
      // the account page's one-shot login terminal (85a2 AC3): the client
      // names {harness, id, cell} and nothing else — a body that also
      // carries a command, an env or an argv is refused (the terminal face
      // runs adapter-declared commands only), and so is a cwd next to a
      // login (the server resolves the whole environment)
      if (typeof body.login !== "object" || body.login === null || Array.isArray(body.login)) {
        return NextResponse.json({ error: "login must name a harness, an id and optionally a Cell" }, { status: 400 });
      }
      const login = body.login as Record<string, unknown>;
      if ("cwd" in body || "command" in body || "env" in body || "argv" in body
        || "command" in login || "env" in login || "argv" in login || "cwd" in login) {
        return NextResponse.json({ error: "a login runs the adapter-declared command only — no command, env, argv or cwd from the client" }, { status: 400 });
      }
      const resolved = await resolveLoginCommand({ harness: login.harness, id: login.id, cell: login.cell });
      if (!resolved.ok) {
        return NextResponse.json({ error: resolved.reason }, { status: 400 });
      }
      const id = createLoginTerminal(
        resolved.command.argv,
        resolved.command.env,
        resolved.command.cwd,
        typeof body.cols === "number" ? body.cols : 80,
        typeof body.rows === "number" ? body.rows : 24,
      );
      return NextResponse.json({ id });
    }
    if (typeof body.cwd !== "string" || !body.cwd.trim()) {
      return NextResponse.json({ error: "cwd required" }, { status: 400 });
    }
    const cwd = resolve(body.cwd);
    if (!(await stat(cwd)).isDirectory()) {
      return NextResponse.json({ error: "cwd must be a directory" }, { status: 400 });
    }
    const roots = await getAllowedFileRoots();
    if (!isExistingFilePathAllowed(cwd, roots)) {
      return NextResponse.json({ error: "Access denied" }, { status: 403 });
    }
    const id = createTerminal(
      cwd,
      typeof body.cols === "number" ? body.cols : 80,
      typeof body.rows === "number" ? body.rows : 24,
      body.id as string | undefined,
    );
    return NextResponse.json({ id });
  } catch (error) {
    return NextResponse.json({ error: error instanceof Error ? error.message : String(error) }, { status: 500 });
  }
}
