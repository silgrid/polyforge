// The terminal login whitelist (polyforge#142, 85a2 AC3): the resolver
// that turns the account page's {harness, id, cell} into the argv, env and
// cwd a one-shot login terminal runs. The commands are the adapters' own
// loginCommands declarations (Q3/Q4) — the browser names one, it can send
// nothing else; a Cell-scoped declaration gets its config root from the
// server's own `pf --json doctor` read, never from the client. The cwd is
// the operator's home (a login command's own environment).
import { homedir } from "node:os";
import type { HarnessAdapter } from "./harness/adapter";
import type { DoctorCellFacts } from "./harness/account-rows-facts";
import { ensureDefaultHarnesses } from "./harness/adapters";
import { readDoctorCells, type DoctorCellsRead } from "./polyforge-doctor";

/** What the resolver answers: the command to run, or why it refuses. */
export type ResolvedLogin =
  | { ok: true; command: { argv: string[]; env: Record<string, string>; cwd: string } }
  | { ok: false; status: 400; reason: string };

/**
 * Resolves one login request. `login.harness` and `login.id` name an
 * adapter and one of its declared commands; `login.cell` (optional) names
 * the Cell a cell-scoped command runs against. Anything else — an unknown
 * harness or id, a cell on a command declared for the interactive session,
 * a Cell the doctor does not know — refuses with its reason.
 */
export async function resolveLoginCommand(
  login: { harness: unknown; id: unknown; cell?: unknown },
  options: {
    adapters?: HarnessAdapter[];
    doctorRead?: () => Promise<DoctorCellsRead>;
  } = {},
): Promise<ResolvedLogin> {
  const refuse = (reason: string): ResolvedLogin => ({ ok: false, status: 400, reason });
  if (typeof login !== "object" || login === null) return refuse("login must name a harness, an id and optionally a Cell");
  const harness = login.harness;
  const id = login.id;
  if (typeof harness !== "string" || harness === "" || typeof id !== "string" || id === "") {
    return refuse("login must name a harness and one of its declared command ids");
  }
  const adapters = options.adapters ?? ensureDefaultHarnesses();
  const adapter = adapters.find((candidate) => candidate.id === harness);
  if (!adapter) return refuse(`no harness named ${harness} has a login command`);
  const declared = adapter.capabilities.loginCommands.find((command) => command.id === id);
  if (!declared) return refuse(`${harness} declares no login command named ${id}`);
  const cell = login.cell;
  if (cell !== undefined && cell !== null && cell !== "") {
    if (typeof cell !== "string") return refuse("the Cell of a login command must be its profile name");
    if (!declared.cell) {
      return refuse(`${harness}'s ${id} runs on the interactive session's own environment — it is not a Cell command`);
    }
    const read = await (options.doctorRead ?? readDoctorCells)();
    const facts = read.cells?.find((candidate: DoctorCellFacts) => candidate.profile === cell);
    if (!facts) {
      return refuse(read.error !== null
        ? `the Cell cannot be resolved: ${read.error}`
        : `no Cell named ${cell} is on this machine`);
    }
    const configRootEnv = adapter.capabilities.configRootEnv;
    const configRoot = facts.harnesses?.[adapter.id]?.configRoot;
    if (!configRootEnv || typeof configRoot !== "string" || configRoot === "") {
      return refuse(`the doctor reports no ${harness} config root for the Cell ${cell}`);
    }
    return { ok: true, command: { argv: [...declared.argv], env: { [configRootEnv]: configRoot }, cwd: homedir() } };
  }
  return { ok: true, command: { argv: [...declared.argv], env: {}, cwd: homedir() } };
}
