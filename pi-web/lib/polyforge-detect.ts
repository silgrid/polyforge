import { createHash } from "node:crypto";
import { hasPolyforgeCellCapabilities } from "./polyforge-cells-pure";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { homedir } from "node:os";
import { type PackageSource } from "@earendil-works/pi-coding-agent";
import { execFileAsync } from "./exec-file";
import { getGlobalSettingsPath, readGlobalSettings } from "./global-settings-file";
import { getPackageSource, isDisabledPackage } from "./package-settings";
import { probePolyforgeVersion, readPolyforgeOpsStatus } from "./polyforge-status";

/**
 * Server-side machine-level detection for the Settings Polyforge section
 * (pi#122). The presence/version probe is the dock's own single
 * implementation (`probePolyforgeVersion` in `lib/polyforge-status.ts` — the
 * only presence spawn site, whose machine line also carries the protocol
 * capabilities); this module assembles the flat
 * booleans/labels the section renders and nothing else: no `api_key`, no
 * server address, no `user_id`, no `cli.toml` contents, no absolute home
 * paths, never `process.env` values (R8). The resolved clone root, the
 * package source and the Claude hook command text all stay server-side.
 *
 * Field independence: line, pi-skill and Claude Code state are machine facts
 * read independently of pf presence; the pf-dependent fields degrade when pf
 * is absent — version absent, daemon 「不适用」 (null, no probe attempted).
 * Every read degrades on its own (a missing or unparsable file, a failed
 * spawn, a broken link): never an error, never a guess.
 */

import { isPolyforgeVersionBelowMinimum } from "./polyforge-version";

// The browser-safe comparison lives in lib/polyforge-version.ts (the install
// card imports that module without the Node/SDK chain this one carries); keep
// the old names exported from here so every server caller stays put.
export { POLYFORGE_MINIMUM_PF_VERSION, isPolyforgeVersionBelowMinimum } from "./polyforge-version";

/** The sha256 (hex) of the internal line's origin owner/repo pair — the
 *  only form the pair is stored in (never the plaintext pair, never a URL,
 *  never a path). */
const INTERNAL_ORIGIN_OWNER_REPO_SHA256 =
  "6d6a1a5b62f974b5f5ae9fb72397a5a021d5f779cb1dcdd491885238033700d6";

/** The harness package's package.json name (the one identification fact). */
const POLYFORGE_PACKAGE_NAME = "polyforge";

const GIT_TIMEOUT_MS = 3000;

import { workstationImage } from "./workstation-mode";

export type PolyforgeLine = "internal" | "public" | "unknown";

export interface PolyforgeDaemonStatus {
  reachable: boolean;
  /** pf's own stderr, capped at 512 bytes (PolyforgeError); present only when unreachable. */
  error?: string;
}

export interface PolyforgeDetectStatus {
  imageManaged?: boolean;
  pfPresent: boolean;
  pfVersion: string | null;
  pfVersionBelowMinimum: boolean;
  line: PolyforgeLine;
  piSkillInstalled: boolean;
  piSkillEnabled: boolean;
  claudeSkillsSymlink: boolean;
  claudeSessionStartHook: boolean;
  /** null = 「不适用」 — pf absent, no probe attempted. */
  daemon: PolyforgeDaemonStatus | null;
  /** the protocol capabilities the presence probe reports
   *  ([] for an older pf or pf absent); multi-Cell writes gate on them. */
  capabilities: string[];
  cellsSupported: boolean;
}

// ---- pure helpers ----

/** The owner/repo pair of a git origin URL (`git@host:O/R.git` and
 *  `https://host/O/R.git` forms, `.git` stripped) — null when the URL carries
 *  no parseable owner/repo pair. Pure, no lookbehind, never throws. */
export function polyforgeOriginOwnerRepo(originUrl: string): string | null {
  const value = originUrl.trim();
  if (!value || /\s/.test(value)) return null;
  let path: string | null = null;
  if (value.startsWith("git@")) {
    const colon = value.indexOf(":");
    if (colon < 0) return null;
    path = value.slice(colon + 1);
  } else {
    try {
      path = new URL(value).pathname;
    } catch {
      return null;
    }
  }
  const pair = path.replace(/^\/+/, "").replace(/\.git$/, "").replace(/\/+$/, "");
  const segments = pair.split("/");
  if (segments.length !== 2 || segments.some((segment) => !segment)) return null;
  return pair;
}

function sha256Hex(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

/** Line from a git origin URL: 内部线 when the parsed owner/repo pair's
 *  sha256 equals `digest` (the built-in internal-line digest constant by
 *  default — the pair is never compared in plaintext), 公开线 for any other
 *  parseable origin, 未知 when undeterminable — never a guess. */
export function polyforgeLineFromOrigin(
  originUrl: string,
  digest: string = INTERNAL_ORIGIN_OWNER_REPO_SHA256,
): PolyforgeLine {
  const pair = polyforgeOriginOwnerRepo(originUrl);
  if (pair === null) return "unknown";
  return sha256Hex(pair) === digest ? "internal" : "public";
}

/** Resolve a package source to the polyforge harness package root: an
 *  existing path whose `package.json` `name` is `polyforge` (install.sh's
 *  `release/current` package root). Null when the source is not such a path —
 *  never an error. */
export function polyforgePackageRoot(source: string): string | null {
  try {
    const stats = statSync(source);
    const root = stats.isDirectory() ? source : dirname(source);
    const packageJsonPath = join(root, "package.json");
    if (!existsSync(packageJsonPath)) return null;
    const parsed = JSON.parse(readFileSync(packageJsonPath, "utf8")) as { name?: unknown };
    return parsed.name === POLYFORGE_PACKAGE_NAME ? root : null;
  } catch {
    return null;
  }
}

/** Enabled = a plain string entry, or an object entry the plugins route's
 *  disabled-shape rule (`lib/package-settings.ts`) does not count disabled —
 *  one fact for the write path and the detection. */
export function isPolyforgePackageEnabled(entry: PackageSource): boolean {
  return typeof entry === "string" || !isDisabledPackage(entry);
}

/** A parsed `~/.claude/settings.json` carries a SessionStart hook whose
 *  command references polyforge (pure, never throws; the command text itself
 *  never leaves the server — only the boolean does). Both the matcher form
 *  (`[{hooks: [...]}]`) and a bare hook list are accepted. */
export function claudeSessionStartHookPresent(settings: unknown): boolean {
  if (!settings || typeof settings !== "object") return false;
  const hooks = (settings as { hooks?: unknown }).hooks;
  if (!hooks || typeof hooks !== "object") return false;
  const sessionStart = (hooks as { SessionStart?: unknown }).SessionStart;
  if (!Array.isArray(sessionStart)) return false;
  for (const matcher of sessionStart) {
    if (!matcher || typeof matcher !== "object") continue;
    const hookList = Array.isArray((matcher as { hooks?: unknown }).hooks)
      ? (matcher as { hooks: unknown[] }).hooks
      : [matcher];
    if (!Array.isArray(hookList)) continue;
    for (const hook of hookList) {
      if (!hook || typeof hook !== "object") continue;
      const command = (hook as { command?: unknown }).command;
      if (typeof command === "string" && command.toLowerCase().includes(POLYFORGE_PACKAGE_NAME)) {
        return true;
      }
    }
  }
  return false;
}

// ---- machine reads (each degrades on its own) ----

/** The global settings' package entries (project scope is never consulted —
 *  pi#122 R5). A missing or unparsable file degrades to no entries, never an
 *  error. Reads through the same lock SettingsManager takes. */
export async function readGlobalPackageEntries(): Promise<PackageSource[]> {
  try {
    return await readGlobalSettings(getGlobalSettingsPath(), (settings) => {
      const packages = settings.packages;
      if (!Array.isArray(packages)) return [];
      return packages.filter((entry): entry is PackageSource =>
        typeof entry === "string"
          || (typeof entry === "object" && entry !== null && typeof (entry as { source?: unknown }).source === "string"));
    });
  } catch {
    return [];
  }
}

/** The workstation image's read-only seed root — the one fact the image
 * judgment reads (polyforge#175, owner 2026-11-19: the seed manifest the
 * image's final layer carries; the same convention the Go side states,
 * never a probe of /components). */
const WORKSTATION_SEED_ROOT = "/opt/polyforge/seed";

/** The workstation's components volume root: the volume whose current/
 * symlink every consumer inside the image resolves. */
const WORKSTATION_COMPONENTS_ROOT = "/components";

/** The image judgment: the seed (with its manifest) the image's final
 * layer carries is under the seed root — one manifest under
 * releases/<ver>/ is the fact, a bare releases/ directory is not. Every
 * read degrades on its own (a missing or unreadable root reads as a
 * host, never an error, never a guess). */

/** The one image-mode judgment every entry reads (polyforge#177, owner
 *  2026-11-19: the seed manifest is the only fact — pf absent must still
 *  identify the image, and nothing probes /components). The seed root
 *  parameter is the seam: an explicit root (the tests' fixture) wins, and
 *  the `__piWebTestSeedRoot` global points the DEFAULT read at a fixture —
 *  a test-only global (never an environment variable: this module never
 *  reads process.env, and the detect contract pins that), unset in
 *  production, so the default reads the constant. */
declare global {
  // eslint-disable-next-line no-var
  var __piWebTestSeedRoot: string | undefined;
}

export function isImageManaged(seedRoot: string = globalThis.__piWebTestSeedRoot ?? WORKSTATION_SEED_ROOT): boolean {
  return workstationImage(seedRoot);
}

/** install.sh's conventional clone path (a fixed
 *  `$HOME/.local/share/polyforge` root, verified in its source) on a
 * host; inside the workstation image (judged by the seed manifest,
 * polyforge#175) the components volume's current instead. The seed root
 * is a parameter so the tests inject a fixture; production reads the
 * constant. */
export function conventionalPolyforgeRoot(home: string = homedir(), seedRoot: string = WORKSTATION_SEED_ROOT): string {
  if (workstationImage(seedRoot)) {
    return join(WORKSTATION_COMPONENTS_ROOT, "current");
  }
  return join(home, ".local", "share", "polyforge", "release", "current");
}

/** `~/.claude/skills/polyforge` resolves through the symlink (stat follows
 *  links; a broken or missing link reads absent). */
export function claudeSkillsSymlinkExists(claudeDir: string): boolean {
  try {
    statSync(join(claudeDir, "skills", "polyforge"));
    return true;
  } catch {
    return false;
  }
}

function claudeSettingsHookExists(claudeDir: string): boolean {
  try {
    return claudeSessionStartHookPresent(JSON.parse(readFileSync(join(claudeDir, "settings.json"), "utf8")));
  } catch {
    return false;
  }
}

/** The line from a git origin URL read off the resolved clone root: no
 *  root (or a failed git read) → 未知, never a guess. The root itself never
 *  leaves the server. */
async function detectPolyforgeLine(cloneRoot: string | null): Promise<PolyforgeLine> {
  if (!cloneRoot) return "unknown";
  try {
    const { stdout } = await execFileAsync("git", ["-C", cloneRoot, "remote", "get-url", "origin"], {
      timeout: GIT_TIMEOUT_MS,
    });
    return polyforgeLineFromOrigin(stdout);
  } catch {
    return "unknown";
  }
}

// ---- assembly ----

/** Assemble the Settings Polyforge section's machine-level detection
 *  (pi#122, R8: flat booleans/labels only). `force` bypasses the 60 s
 *  presence cache read (the 「重新检测」 control). */
export async function detectPolyforge(force = false): Promise<PolyforgeDetectStatus> {
  const entries = await readGlobalPackageEntries();
  // The clone root the line is read from: the global polyforge package
  // entry's source when that entry exists, else install.sh's conventional
  // path when it does (null → line 未知, no git spawn). The resolved
  // home-path root never leaves the server.
  let cloneRoot: string | null = null;
  let piSkillInstalled = false;
  let piSkillEnabled = false;
  for (const entry of entries) {
    const root = polyforgePackageRoot(getPackageSource(entry));
    if (!root) continue;
    cloneRoot = root;
    piSkillInstalled = true;
    piSkillEnabled = isPolyforgePackageEnabled(entry);
    break;
  }
  if (!cloneRoot) {
    const conventional = conventionalPolyforgeRoot();
    if (existsSync(conventional)) cloneRoot = conventional;
  }
  const line = await detectPolyforgeLine(cloneRoot);
  const claudeDir = join(homedir(), ".claude");
  const claudeSkillsSymlink = claudeSkillsSymlinkExists(claudeDir);
  const claudeSessionStartHook = claudeSettingsHookExists(claudeDir);
  const { present, version, capabilities } = await probePolyforgeVersion(force);
  const pfVersion = present ? version : null;
  let daemon: PolyforgeDaemonStatus | null = null;
  if (present) {
    try {
      await readPolyforgeOpsStatus();
      daemon = { reachable: true };
    } catch (error) {
      // Only pf's own bounded stderr surfaces (PolyforgeError caps it at
      // 512); never argv, never `process.env` values.
      daemon = { reachable: false, error: error instanceof Error ? error.message : "polyforge is unavailable" };
    }
  }
  return {
    ...(workstationImage() ? { imageManaged: true } : {}),
    pfPresent: present,
    pfVersion,
    pfVersionBelowMinimum: isPolyforgeVersionBelowMinimum(pfVersion),
    line,
    piSkillInstalled,
    piSkillEnabled,
    claudeSkillsSymlink,
    claudeSessionStartHook,
    daemon,
    capabilities,
    cellsSupported: hasPolyforgeCellCapabilities(capabilities),
  };
}