// The account route (polyforge#142, 85a2 AC1/AC6): the account page's one
// read and its one write. GET composes the interactive claude card (the
// safe `claude auth status` run plus the settings env block's credential
// key names) and the per-Cell rows from one `pf --json doctor` read (Q8:
// the page's 刷新 runs the full doctor and caches), with a `pi auth check
// --provider <p> --json --no-refresh` per distinct model-credential source
// dir. POST writes a credential into the native env block of the
// interactive session's ~/.claude/settings.json and of the checked Cells'
// claude-config/settings.json — 沿用交互会话的值 copies the interactive
// env block server-side, so the value never passes through the browser —
// through the claude adapter's gated write. No response, no log line and
// no spawn argv ever carries a value; values live in the native files
// alone. Behind the API guard like every /api route; JSON-only POST,
// validated before any write.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { NextResponse } from "next/server";
import { claudeConfigDir } from "@/lib/claude-config-dir";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import { claudeSessionStatus, piStepStatus } from "@/lib/harness/account";
import { buildAccountCellRow, type AccountCellRow, type DoctorCellFacts } from "@/lib/harness/account-rows";
import type { DoctorHarnessFacts, HarnessCapabilityFacts, PiStepCheck } from "@/lib/harness/account-rows-facts";
import { ensureDefaultHarnesses } from "@/lib/harness/adapters";
import { readDoctorCells } from "@/lib/polyforge-doctor";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

/** The interactive settings env block's credential key names, set/unset
 * only (the card's 凭据类型与来源 beside the auth status). */
const INTERACTIVE_CREDENTIAL_KEYS = ["ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN"] as const;

function interactiveEnvCredentials(configDir: string): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const key of INTERACTIVE_CREDENTIAL_KEYS) out[key] = false;
  try {
    const parsed: unknown = JSON.parse(readFileSync(join(configDir, "settings.json"), "utf8"));
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return out;
    const env = (parsed as Record<string, unknown>).env;
    if (typeof env !== "object" || env === null || Array.isArray(env)) return out;
    for (const key of INTERACTIVE_CREDENTIAL_KEYS) {
      out[key] = typeof (env as Record<string, unknown>)[key] === "string" && ((env as Record<string, unknown>)[key] as string) !== "";
    }
  } catch {
    // absent or torn: all unset, never an error
  }
  return out;
}

/** The adapters' capability slice the rows need. */
function capabilityFacts(): Record<string, HarnessCapabilityFacts> {
  const caps: Record<string, HarnessCapabilityFacts> = {};
  for (const adapter of ensureDefaultHarnesses()) {
    caps[adapter.id] = { id: adapter.id, credentialsFollowConfigRoot: adapter.capabilities.credentialsFollowConfigRoot };
  }
  return caps;
}

/** One Cell's claude config root, as the doctor reported it (the client
 * never sends a path). */
function claudeConfigRootOf(cell: DoctorCellFacts): string | null {
  const facts: DoctorHarnessFacts | undefined = cell.harnesses?.claude;
  return typeof facts?.configRoot === "string" && facts.configRoot !== "" ? facts.configRoot : null;
}

export async function GET(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  const configDir = claudeConfigDir();
  const [claude, doctor] = await Promise.all([claudeSessionStatus(), readDoctorCells()]);
  const cells: AccountCellRow[] = [];
  if (doctor.cells) {
    const caps = capabilityFacts();
    // one pi check per distinct model-credential source dir (a shared-default
    // Cell answers once with the Cell it shares)
    const sources = new Set<string>();
    for (const cell of doctor.cells) {
      for (const facts of Object.values(cell.harnesses ?? {})) {
        if (typeof facts?.source === "string" && facts.source !== "") sources.add(facts.source);
      }
    }
    const checks = new Map<string, PiStepCheck>();
    for (const source of sources) {
      const check = await piStepStatus(source);
      checks.set(source, { sourceDir: source, ...check });
    }
    for (const cell of doctor.cells) {
      cells.push(buildAccountCellRow(cell, caps, checks));
    }
  }
  return NextResponse.json({
    claude: { ...claude, envCredentials: interactiveEnvCredentials(configDir) },
    cells,
    doctorError: doctor.error,
  });
}

/** The env-block key each write kind lands in (Q3). */
const WRITE_KIND_KEYS: Record<string, { valueKey: string; baseUrlKey?: string }> = {
  "oauth-token": { valueKey: "CLAUDE_CODE_OAUTH_TOKEN" },
  "api-key": { valueKey: "ANTHROPIC_API_KEY" },
  gateway: { valueKey: "ANTHROPIC_AUTH_TOKEN", baseUrlKey: "ANTHROPIC_BASE_URL" },
};

/** The interactive env block's value for one kind, copied server-side
 * (the browser never sees it); null when the session lacks the kind. */
function interactiveCredential(kind: string, configDir: string): { value: string; baseUrl?: string } | null {
  const shape = WRITE_KIND_KEYS[kind];
  if (!shape) return null;
  try {
    const parsed: unknown = JSON.parse(readFileSync(join(configDir, "settings.json"), "utf8"));
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return null;
    const env = (parsed as Record<string, unknown>).env;
    if (typeof env !== "object" || env === null || Array.isArray(env)) return null;
    const value = (env as Record<string, unknown>)[shape.valueKey];
    if (typeof value !== "string" || value === "") return null;
    if (!shape.baseUrlKey) return { value };
    const baseUrl = (env as Record<string, unknown>)[shape.baseUrlKey];
    if (typeof baseUrl !== "string" || baseUrl === "") return null;
    return { value, baseUrl };
  } catch {
    return null;
  }
}

interface WriteOutcome {
  target: string;
  status: "written" | "refused";
  reason?: string;
}

export async function POST(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return NextResponse.json({ error: "request-denied", message: "Untrusted API request" }, { status: 403 });
  }
  if (!hasJsonContentType(req)) {
    return NextResponse.json({ error: "json-required", message: "POST needs a JSON body" }, { status: 415 });
  }
  let body: Record<string, unknown>;
  try {
    body = await req.json() as Record<string, unknown>;
  } catch {
    return NextResponse.json({ error: "json-invalid", message: "the body is not valid JSON" }, { status: 400 });
  }
  const bad = (message: string) => NextResponse.json({ error: "invalid-request", message }, { status: 400 });
  const kind = typeof body.kind === "string" ? body.kind : "";
  const shape = WRITE_KIND_KEYS[kind];
  if (!shape) return bad("kind must be one of oauth-token, api-key, gateway");
  if (!Array.isArray(body.targets) || body.targets.length === 0 || !body.targets.every((t) => typeof t === "string" && t !== "")) {
    return bad("targets must name the interactive session and/or the Cells to write (default: every Cell)");
  }
  const configDir = claudeConfigDir();
  let value: string | null = null;
  let baseUrl: string | undefined;
  if (body.useInteractive === true) {
    const copied = interactiveCredential(kind, configDir);
    if (copied === null) {
      return bad(`the interactive session's settings.json env block holds no ${kind} credential to reuse`);
    }
    value = copied.value;
    baseUrl = copied.baseUrl;
  } else if (typeof body.value === "string" && body.value !== "") {
    value = body.value;
  } else {
    return bad("value is required (or useInteractive: true to copy the interactive session's)");
  }
  if (shape.baseUrlKey && body.useInteractive !== true) {
    if (typeof body.baseUrl === "string" && body.baseUrl !== "") {
      baseUrl = body.baseUrl;
    } else {
      return bad("a gateway credential needs its base URL");
    }
  }
  // the Cell targets resolve server-side from a fresh doctor read — the
  // client names profiles only, never paths. Every target is validated
  // before any write: an unknown name, or a Cell that cannot be resolved
  // because the doctor read failed, refuses the whole request.
  const doctor = await readDoctorCells();
  const byProfile = new Map<string, DoctorCellFacts>();
  for (const cell of doctor.cells ?? []) byProfile.set(cell.profile, cell);
  const writeTargets = body.targets as string[];
  for (const target of writeTargets) {
    if (target === "interactive") continue;
    if (!byProfile.has(target)) {
      return bad(doctor.error !== null
        ? `the Cells cannot be resolved: ${doctor.error}`
        : `no Cell named ${target} is on this machine`);
    }
  }
  const results: WriteOutcome[] = [];
  const adapter = ensureDefaultHarnesses().find((h) => h.id === "claude");
  for (const target of writeTargets) {
    if (target === "interactive") {
      results.push({ target, ...(await writeOne(adapter, { kind, value, baseUrl }, configDir)) });
      continue;
    }
    const cell = byProfile.get(target);
    const root = cell ? claudeConfigRootOf(cell) : null;
    if (root === null) {
      results.push({
        target,
        status: "refused",
        reason: "the doctor reports no claude config root for this Cell (its daemon did not answer or is too old)",
      });
      continue;
    }
    results.push({ target, ...(await writeOne(adapter, { kind, value, baseUrl }, root)) });
  }
  return NextResponse.json({ results });
}

async function writeOne(
  adapter: { write: (concept: "auth", change: Record<string, unknown>, ctx: { claudeConfigDir: string; env: NodeJS.ProcessEnv }) => Promise<{ status: string; reason?: string; summary?: string }> } | undefined,
  change: Record<string, unknown>,
  configDir: string,
): Promise<Omit<WriteOutcome, "target">> {
  if (!adapter) return { status: "refused", reason: "the claude adapter is not registered" };
  const outcome = await adapter.write("auth", change, { claudeConfigDir: configDir, env: process.env });
  if (outcome.status === "written") return { status: "written" };
  return { status: "refused", reason: outcome.reason ?? "the write was refused" };
}
