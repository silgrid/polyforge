import { spawn } from "node:child_process";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { ConfigWriteError, parsePfError } from "./polyforge-config-write";
import { readPolyforgeCapabilities } from "./polyforge-config-scope";
import {
  POLYFORGE_CELLS_UPGRADE_HINT,
  hasPolyforgeCellCapabilities,
  isNewCellName,
  isPolyforgeProfileName,
  sanitizePolyforgeCells,
  type PolyforgeCell,
} from "./polyforge-cells-pure";

/**
 * Server-side Cell reads and lifecycle writes for Settings › Polyforge.
 * Every read goes through pf (`pf --json version`,
 * `pf --json cell list`), never through the config files; every write is a
 * `pf cell …` spawn (argv only, no shell) gated on the multi-Cell
 * capabilities — a pf without them is refused with the upgrade hint and
 * nothing is spawned. The api key is passed on stdin only and never logged,
 * echoed or returned. Permanent cleanup has no web path (CLI-only).
 * these are Host commands — `pf cell` talks to the Host
 * (`GET /profiles`, the lifecycle routes) and names its Cell positionally,
 * so no `--profile` is passed and none of pf's resolver inputs apply.
 */

const CELL_TIMEOUT_MS = 30000;

interface PfRun {
  code: number;
  stdout: string;
  stderr: string;
}

function runPf(argv: string[], stdin?: string): Promise<PfRun> {
  return new Promise((resolve, reject) => {
    const child = spawn(polyforgePfBin(), argv, { shell: false, stdio: ["pipe", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      reject(new ConfigWriteError("pf did not answer in time", 504));
    }, CELL_TIMEOUT_MS);
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => { stdout += chunk; });
    child.stderr.on("data", (chunk: string) => { stderr += chunk; });
    child.on("error", (error) => {
      clearTimeout(timer);
      reject(new ConfigWriteError(error instanceof Error ? error.message : "polyforge is unavailable", 503));
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      resolve({ code: code ?? 1, stdout, stderr });
    });
    if (stdin !== undefined) child.stdin.write(stdin);
    child.stdin.end();
  });
}

function parseJson(text: string): unknown {
  try { return JSON.parse(text); } catch { return null; }
}

export interface PolyforgeCellsRead {
  capabilities: string[];
  /** false → the multi-Cell surface shows the upgrade hint, no write control. */
  supported: boolean;
  upgradeHint?: string;
  cells: PolyforgeCell[] | null;
  error?: string;
}

/** The Cell registry; degradation as fields (never throws). */
export async function readPolyforgeCells(): Promise<PolyforgeCellsRead> {
  const capabilities = await readPolyforgeCapabilities();
  const supported = hasPolyforgeCellCapabilities(capabilities);
  if (!supported) return { capabilities, supported, upgradeHint: POLYFORGE_CELLS_UPGRADE_HINT, cells: null };
  try {
    const run = await runPf(["--json", "cell", "list"]);
    if (run.code !== 0) return { capabilities, supported, cells: null, error: parsePfError(run.stderr).message };
    const cells = sanitizePolyforgeCells(parseJson(run.stdout));
    return cells ? { capabilities, supported, cells } : { capabilities, supported, cells: null, error: "pf answered an unreadable Cell list" };
  } catch (error) {
    return { capabilities, supported, cells: null, error: error instanceof Error ? error.message : "polyforge is unavailable" };
  }
}

/** Refuse a multi-Cell write on a pf without the capabilities (nothing spawned). */
export async function assertPolyforgeCellCapabilities(): Promise<void> {
  if (!hasPolyforgeCellCapabilities(await readPolyforgeCapabilities())) {
    throw new ConfigWriteError(POLYFORGE_CELLS_UPGRADE_HINT, 400);
  }
}

export interface PolyforgeCellAddRequest {
  profile?: unknown;
  server?: unknown;
  apiKey?: unknown;
  modelSource?: unknown;
}

function asOutcome(run: PfRun): unknown {
  if (run.code !== 0) {
    const failure = parsePfError(run.stderr);
    throw new ConfigWriteError(failure.message, failure.status, failure.diff);
  }
  return parseJson(run.stdout);
}

/** `pf cell add` (validated before any spawn; the key on stdin only). */
export async function addPolyforgeCell(body: PolyforgeCellAddRequest): Promise<unknown> {
  if (!isNewCellName(body.profile)) throw new ConfigWriteError("profile must match [a-z0-9][a-z0-9-]{0,31} and not be default", 400);
  if (typeof body.server !== "string" || !/^https?:\/\/\S+$/.test(body.server)) throw new ConfigWriteError("server must be an http(s) URL", 400);
  if (typeof body.apiKey !== "string" || body.apiKey === "" || /\s/.test(body.apiKey)) throw new ConfigWriteError("apiKey must be a non-empty string without whitespace", 400);
  const argv = ["--json", "cell", "add", body.profile, "--server", body.server];
  if (body.modelSource !== undefined && body.modelSource !== "") {
    if (typeof body.modelSource !== "string" || (body.modelSource !== "shared-default" && !body.modelSource.startsWith("/"))) {
      throw new ConfigWriteError("modelSource must be shared-default or an absolute directory", 400);
    }
    argv.push("--model-source", body.modelSource);
  }
  await assertPolyforgeCellCapabilities();
  return asOutcome(await runPf(argv, `${body.apiKey}\n`));
}

/** `pf cell update|repair|remove <profile>`. */
export async function changePolyforgeCell(profile: unknown, action: "update" | "repair" | "remove"): Promise<unknown> {
  if (!isPolyforgeProfileName(profile)) throw new ConfigWriteError("profile must match [a-z0-9][a-z0-9-]{0,31}", 400);
  if (action === "remove" && profile === "default") throw new ConfigWriteError("the default Cell cannot be removed", 400);
  await assertPolyforgeCellCapabilities();
  return asOutcome(await runPf(["--json", "cell", action, profile]));
}

/** The Cell the CLI resolves for a tab's cwd (`pf --json profile`, run in
 *  that cwd — local, no daemon needed), so the Memory, Tasks and Messages
 *  tabs can name the Cell they read. null when pf cannot
 *  answer (an older pf, an invalid cwd); never throws. */
export async function readPolyforgeResolvedCell(cwd?: string): Promise<string | null> {
  try {
    const run = await new Promise<PfRun>((resolve, reject) => {
      const child = spawn(polyforgePfBin(), ["--json", "profile"], {
        shell: false,
        stdio: ["ignore", "pipe", "pipe"],
        ...(cwd && cwd.startsWith("/") ? { cwd } : {}),
      });
      let stdout = "";
      const timer = setTimeout(() => { child.kill("SIGKILL"); reject(new Error("timeout")); }, 5000);
      child.stdout.setEncoding("utf8");
      child.stdout.on("data", (chunk: string) => { stdout += chunk; });
      child.on("error", (error) => { clearTimeout(timer); reject(error); });
      child.on("close", (code) => { clearTimeout(timer); resolve({ code: code ?? 1, stdout, stderr: "" }); });
    });
    if (run.code !== 0) return null;
    const profile = (parseJson(run.stdout) as { profile?: unknown } | null)?.profile;
    return isPolyforgeProfileName(profile) ? profile : null;
  } catch {
    return null;
  }
}
