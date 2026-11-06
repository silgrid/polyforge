/**
 * The pure half of Settings › Polyforge's pf install / update entry
 *: the release channel, the wire shapes the route answers,
 * and the judgements both sides share — which versions disagree, which jobs
 * block an upgrade (install.sh's own rule), what install.sh's refusal said.
 * Import-free, so the panel can import it without pulling the server module
 * (`lib/polyforge-install.ts`, which spawns) into the browser bundle.
 */

/** The release channel pf is installed from: the repository whose releases
 *  carry pf and Pi Web alike (the same repository as Pi Web's own shipped
 *  marker, `bin/pi-web-release-channel.json`; a unit test keeps the two and
 *  the copyable install command equal). */
export const POLYFORGE_INSTALL_REPO = "silgrid/polyforge";

/** The ref install.sh is fetched from — the same file the copyable command
 *  pipes into bash. */
export const POLYFORGE_INSTALL_REF = "main";

/** install.sh's exit status for a refusal PF_INSTALL_FORCE=1 lifts (running
 *  jobs, or a job list it could not read); nothing was changed. */
export const POLYFORGE_INSTALL_REFUSED_EXIT = 3;

/** How long a refusal can be answered with a forced update: past it the
 *  jobs it named say nothing about the jobs running now. */
export const POLYFORGE_INSTALL_FORCE_WINDOW_MS = 10 * 60_000;

export type PolyforgeInstallState = "running" | "succeeded" | "failed" | "refused";

/** One job a graceful daemon shutdown would interrupt, as install.sh names it. */
export interface PolyforgeInstallJob {
  slug: string;
  status: string;
}

export interface PolyforgeInstallRefusal {
  /** install.sh's own ERROR line, verbatim (without the `ERROR: ` prefix). */
  reason: string;
  /** The jobs install.sh listed under it (`<slug> (<status>)`), possibly none. */
  jobs: string[];
}

/** One install run as the page sees it — never the api key, never argv. */
export interface PolyforgeInstallRun {
  id: string;
  /** "install" when pf was absent at the start, else "update". */
  mode: "install" | "update";
  force: boolean;
  state: PolyforgeInstallState;
  startedAt: string;
  endedAt: string | null;
  exitCode: number | null;
  /** How many output lines the run has produced (the stream's end cursor). */
  lineCount: number;
  /** A failed run's conclusion: install.sh's last ERROR line, or why it could not run. */
  error: string | null;
  refusal: PolyforgeInstallRefusal | null;
}

export interface PolyforgeInstallVersions {
  repo: string;
  pfPresent: boolean;
  /** `pf --version` of the installed binary; null when absent or unreadable. */
  installed: string | null;
  /** The channel's latest release tag; null with `latestError` when unreadable. */
  latest: string | null;
  latestError: string | null;
  /** The running daemon's own version (`/status` `version`); null when no
   *  daemon answers or it is too old to say. */
  running: string | null;
  /** null = not applicable (pf absent). */
  daemonReachable: boolean | null;
  daemonError: string | null;
  /** The jobs an upgrade would be refused for (install.sh's rule). */
  jobs: PolyforgeInstallJob[];
  /** This Pi Web's own version. */
  piWeb: string;
  /** No cli.toml with an api_key yet (install.sh's `has_config` rule,
   *  `polyforgeConfigHasKey`): install.sh needs the key and the host, and so
   *  does the route — whether pf is installed or not. The card asks for them
   *  exactly when this is true. */
  needsConfig: boolean;
}

export interface PolyforgeVersionFlags {
  /** The channel's latest differs from the installed pf. */
  updateAvailable: boolean;
  /** The daemon answers a version other than the installed binary's. */
  daemonStale: boolean;
  /** Pi Web's version differs from the installed pf's. */
  piWebOutOfSync: boolean;
}

function versionCore(value: string): string {
  return value.trim().replace(/^v/, "");
}

/** Which of the versions disagree (pure). A missing version proves nothing,
 *  so it never raises a flag. */
export function polyforgeVersionFlags(versions: Pick<PolyforgeInstallVersions, "installed" | "latest" | "running" | "piWeb">): PolyforgeVersionFlags {
  const { installed, latest, running, piWeb } = versions;
  return {
    updateAvailable: Boolean(installed && latest && versionCore(installed) !== versionCore(latest)),
    daemonStale: Boolean(installed && running && versionCore(installed) !== versionCore(running)),
    piWebOutOfSync: Boolean(installed && piWeb && versionCore(installed) !== versionCore(piWeb)),
  };
}

/** Whether running install.sh is relevant now — what the card's line
 *  naming tools/install.sh explains: pf is absent, the channel has a newer
 *  release, the daemon still runs another binary than the installed one, or
 *  a run is in progress or did not succeed (refused, failed). While pf is
 *  installed, current and its daemon on it, nothing about install.sh is
 *  shown (a healthy page shows no install command). Unknown
 *  versions prove nothing, so they make it relevant only through a run. Pure. */
export function polyforgeInstallRelevant(
  versions: Pick<PolyforgeInstallVersions, "pfPresent" | "installed" | "latest" | "running" | "piWeb"> | null,
  run: Pick<PolyforgeInstallRun, "state"> | null,
): boolean {
  if (run && run.state !== "succeeded") return true;
  if (!versions) return false;
  if (!versions.pfPresent) return true;
  const flags = polyforgeVersionFlags(versions);
  return flags.updateAvailable || flags.daemonStale;
}

const VERSION_TOKEN = /^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$/;

/** A version string as reported (a tag, `pf --version`, the daemon's
 *  build), or null when it is not a plain version token. */
export function sanitizeInstallVersion(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const trimmed = value.trim();
  return VERSION_TOKEN.test(trimmed) ? trimmed : null;
}

const JOB_MAX = 50;
const JOB_TEXT_MAX = 200;

/** The jobs a graceful shutdown would interrupt, by install.sh's own rule:
 *  every job whose status is neither `paused` (operator pause: no agent
 *  tree) nor `end_pending`, named by its slug, else its work item id. null
 *  when the body is not a daemon's `/status`. Pure, never throws. */
export function polyforgeInstallBusyJobs(status: unknown): PolyforgeInstallJob[] | null {
  if (!status || typeof status !== "object" || !("machine_id" in status)) return null;
  const jobs = (status as { jobs?: unknown }).jobs;
  if (!Array.isArray(jobs)) return [];
  const busy: PolyforgeInstallJob[] = [];
  for (const job of jobs) {
    if (!job || typeof job !== "object") continue;
    const j = job as { slug?: unknown; work_item_id?: unknown; status?: unknown };
    const jobStatus = typeof j.status === "string" ? j.status : "";
    if (jobStatus === "paused" || jobStatus === "end_pending") continue;
    const slug = typeof j.slug === "string" && j.slug ? j.slug : typeof j.work_item_id === "string" ? j.work_item_id : "";
    if (!slug) continue;
    busy.push({ slug: slug.slice(0, JOB_TEXT_MAX), status: jobStatus.slice(0, JOB_TEXT_MAX) });
    if (busy.length >= JOB_MAX) break;
  }
  return busy;
}

const ERROR_PREFIX = "ERROR: ";
/** install.sh indents a refusal's details by five spaces. */
const DETAIL_INDENT = "     ";

/** install.sh's last ERROR line (without the prefix), or null. */
export function lastInstallError(lines: string[]): string | null {
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].startsWith(ERROR_PREFIX)) return lines[i].slice(ERROR_PREFIX.length);
  }
  return null;
}

/** What install.sh's refusal said: its ERROR line and the job lines it
 *  printed under it (five-space indented). Pure. */
export function parseInstallRefusal(lines: string[]): PolyforgeInstallRefusal {
  let at = -1;
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].startsWith(ERROR_PREFIX)) { at = i; break; }
  }
  if (at < 0) return { reason: "", jobs: [] };
  const jobs: string[] = [];
  for (let i = at + 1; i < lines.length && lines[i].startsWith(DETAIL_INDENT); i++) {
    const job = lines[i].trim();
    if (job) jobs.push(job);
  }
  return { reason: lines[at].slice(ERROR_PREFIX.length), jobs };
}

/** install.sh's overridable refusal, by its text as well as its exit 3:
 *  the last ERROR line is followed by the remedy naming PF_INSTALL_FORCE=1
 *  (a failure that happens to exit 3 is never offered a force). Pure. */
export function isInstallRefusal(lines: string[]): boolean {
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].startsWith(ERROR_PREFIX)) return lines.slice(i + 1).some((line) => line.includes("PF_INSTALL_FORCE=1"));
  }
  return false;
}

/** The aihub host a first install points cli.toml at: a host name or IPv4
 *  address (install.sh builds `http://<host>:8080`); never a flag. */
export function isInstallHost(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,252})$/.test(value);
}

/** An api key the installer can take on stdin: one line, no whitespace or
 *  control characters, long enough that redacting it from output is exact. */
export function isInstallApiKey(value: unknown): value is string {
  return typeof value === "string" && value.length >= 8 && value.length <= 4096 && /^[\x21-\x7e]+$/.test(value);
}
