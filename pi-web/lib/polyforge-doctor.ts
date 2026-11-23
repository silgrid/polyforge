// The doctor wrapper (polyforge#142, 85a2 AC1): the account page's one
// `pf --json doctor` read (Q8: the page's 刷新 runs the full doctor and
// caches the answer). The spawn follows lib/polyforge-cells.ts's shape
// (argv only, no shell, a bound, pf's stderr as the reason), and the
// answer is projected through a sanitizer that copies known keys with
// closed vocabularies only — no value, no undeclared field, ever reaches
// the page. Server-side; the sanitized rows are the client-safe wire type
// of lib/harness/account-rows-facts.ts.
import { spawn } from "node:child_process";
import { polyforgePfBin } from "./polyforge-pf-bin";
import type { DoctorCellFacts, DoctorHarnessFacts } from "./harness/account-rows-facts";

/** How long the one doctor read may take (the doctor probes every Cell). */
const DOCTOR_TIMEOUT_MS = 120_000;

/** The one argv the wrapper ever runs. */
export const DOCTOR_ARGV: readonly string[] = ["--json", "doctor"];

/** The sanitized read: the Cell rows (default first), or the reason it is
 * unavailable (pf missing, a non-zero exit, an unparsable or cells-less
 * answer). Never throws. */
export interface DoctorCellsRead {
  cells: DoctorCellFacts[] | null;
  error: string | null;
}

/** The closed vocabularies of the doctor's facts (u1's contract). */
const SOURCE_KINDS = new Set(["operator", "shared-default", "directory", "none"]);
const SET_UNSET = new Set(["set", "unset"]);
const FILE_PRESENCE = new Set(["present", "absent"]);

function str(value: unknown): string | null {
  return typeof value === "string" && value !== "" ? value : null;
}

/** One harness's facts: the known keys only, each through its vocabulary. */
function sanitizeHarness(raw: unknown): DoctorHarnessFacts {
  const facts: DoctorHarnessFacts = {};
  if (typeof raw !== "object" || raw === null) return facts;
  const entry = raw as Record<string, unknown>;
  const configRoot = str(entry.config_root);
  if (configRoot !== null) facts.configRoot = configRoot;
  const credential = str(entry.credential);
  if (credential !== null && SET_UNSET.has(credential)) facts.credential = credential as "set" | "unset";
  const source = str(entry.source);
  if (source !== null) facts.source = source;
  const sourceKind = str(entry.source_kind);
  if (sourceKind !== null && SOURCE_KINDS.has(sourceKind)) facts.sourceKind = sourceKind;
  const credentialsFile = str(entry.credentials_file);
  if (credentialsFile !== null && FILE_PRESENCE.has(credentialsFile)) facts.credentialsFile = credentialsFile as "present" | "absent";
  if (typeof entry.settings_env === "object" && entry.settings_env !== null) {
    const env: Record<string, string> = {};
    for (const [key, value] of Object.entries(entry.settings_env as Record<string, unknown>)) {
      if (typeof value === "string" && SET_UNSET.has(value)) env[key] = value;
    }
    facts.settingsEnv = env;
  }
  return facts;
}

/**
 * The doctor's `cells` object as the account page's wire rows: the profile
 * and the closed-vocabulary facts per Cell (default first, then the named
 * Cells in pf's own sorted order). Anything else — an undeclared key, a
 * value outside its vocabulary — is dropped. Null when the answer carries
 * no cells object.
 */
export function sanitizeDoctorCells(answer: unknown): DoctorCellFacts[] | null {
  if (typeof answer !== "object" || answer === null) return null;
  const cells = (answer as Record<string, unknown>).cells;
  if (typeof cells !== "object" || cells === null || Array.isArray(cells)) return null;
  const names = Object.keys(cells as Record<string, unknown>).sort((a, b) =>
    a === "default" ? -1 : b === "default" ? 1 : a < b ? -1 : 1);
  const rows: DoctorCellFacts[] = [];
  for (const profile of names) {
    const raw = (cells as Record<string, unknown>)[profile];
    if (typeof raw !== "object" || raw === null) continue;
    const entry = raw as Record<string, unknown>;
    const row: DoctorCellFacts = { profile };
    const executionDisabled = str(entry.execution_disabled);
    if (executionDisabled !== null) row.executionDisabled = executionDisabled;
    const probeError = str(entry.probe_error);
    if (probeError !== null) row.probeError = probeError;
    if (typeof entry.harnesses === "object" && entry.harnesses !== null) {
      const harnesses: Record<string, DoctorHarnessFacts> = {};
      for (const [id, facts] of Object.entries(entry.harnesses as Record<string, unknown>)) {
        harnesses[id] = sanitizeHarness(facts);
      }
      row.harnesses = harnesses;
    }
    rows.push(row);
  }
  return rows;
}

/**
 * The account page's one doctor read: `pf --json doctor`, sanitized. pf's
 * own exit code is not an error — the doctor exits 1 whenever any check
 * failed, and the cells facts are still the answer.
 */
export async function readDoctorCells(options: { binary?: string; timeoutMs?: number } = {}): Promise<DoctorCellsRead> {
  const binary = options.binary ?? polyforgePfBin();
  return new Promise<DoctorCellsRead>((resolve) => {
    let stdout = "";
    let stderr = "";
    let settled = false;
    const finish = (read: DoctorCellsRead) => {
      if (settled) return;
      settled = true;
      clearTimeout(deadline);
      resolve(read);
    };
    const child = spawn(binary, [...DOCTOR_ARGV], { shell: false, stdio: ["ignore", "pipe", "pipe"] });
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => { stdout += chunk; });
    child.stderr.on("data", (chunk: string) => { stderr += chunk; });
    child.on("error", (error) => finish({ cells: null, error: `polyforge is unavailable: ${error.message}` }));
    child.on("close", () => {
      if (stdout.trim() === "") {
        finish({ cells: null, error: stderr.trim() !== "" ? `pf doctor answered no report: ${stderr.trim().slice(0, 200)}` : "pf doctor answered no report" });
        return;
      }
      let parsed: unknown;
      try {
        parsed = JSON.parse(stdout);
      } catch {
        finish({ cells: null, error: "pf doctor answered an unparsable report" });
        return;
      }
      const cells = sanitizeDoctorCells(parsed);
      finish(cells === null
        ? { cells: null, error: "pf doctor answered no cells object (an older pf)" }
        : { cells, error: null });
    });
    const deadline = setTimeout(() => {
      child.kill("SIGKILL");
      finish({ cells: null, error: "pf doctor did not answer in time" });
    }, options.timeoutMs ?? DOCTOR_TIMEOUT_MS);
    deadline.unref();
  });
}
