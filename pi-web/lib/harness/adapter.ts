// The harness adapter interface, the adapter registry and the shared write
// gate (polyforge#85, unit 85a1). Every write a harness adapter performs goes
// through writeGate() first: an unrecognized version — outside the adapter's
// known major.minor range, or a config file whose top-level structure is not
// recognized — is refused with a reason (owner ruling 2026-11-19 Q2 C); a known
// version with a concept whose write is not implemented yet answers
// "not-implemented" (Q1 A). Reads are open to any version.
import type {
  ConceptId,
  ConceptPayload,
  HarnessCapabilities,
  HarnessId,
} from "./concepts";

/** What an adapter's version probe found. */
export interface VersionProbeResult {
  /** The detected version, null when it could not be detected. */
  version: string | null;
  /** Whether this is a version the adapter recognizes for writes. */
  known: boolean;
  /** The recognized range's label, e.g. "claude 2.1.x (>=2.1.312)". */
  knownRange?: string;
  /** Why the version is not known: outside the range, or an unrecognized file structure. */
  reason?: string;
  /** Where the version came from ("pi SDK package", "claude initialize"). */
  source: string;
}

/** A recognized version range: a major.minor pair with a minimum patch (Q2 C). */
export interface KnownVersionRange {
  major: number;
  minor: number;
  minPatch: number;
  label: string;
}

/** claude's recognized range (2.1.x from the first recorded version on; CLAUDE_RECORDED_VERSIONS keeps its own fixture semantics). */
export const CLAUDE_KNOWN_RANGE: KnownVersionRange = {
  major: 2,
  minor: 1,
  minPatch: 312,
  label: "claude 2.1.x (>=2.1.312)",
};

/** pi's recognized range for interactive sessions: the SDK package's version (Q2 C). */
export const PI_KNOWN_RANGE: KnownVersionRange = {
  major: 1,
  minor: 5,
  minPatch: 0,
  label: "pi 1.5.x",
};

/** `major.minor.patch` of a version string, null when it does not parse. */
export function parseVersion(version: string): [number, number, number] | null {
  const match = /^(\d+)\.(\d+)\.(\d+)/.exec(version.trim());
  if (!match) return null;
  return [Number(match[1]), Number(match[2]), Number(match[3])];
}

export function versionInKnownRange(version: string, range: KnownVersionRange): boolean {
  const parsed = parseVersion(version);
  if (!parsed) return false;
  const [major, minor, patch] = parsed;
  return major === range.major && minor === range.minor && patch >= range.minPatch;
}

/** The context every adapter method takes; the routes fill it in, tests inject fixture paths. */
export interface HarnessReadContext {
  /** A validated project folder, when the caller has one. */
  cwd?: string;
  env?: NodeJS.ProcessEnv;
  /** pi's agent dir override (tests; default defaultAgentDir()). */
  agentDir?: string;
  /** claude's config dir override (tests; default claudeConfigDir()). */
  claudeConfigDir?: string;
  /** pi's version probe override (tests; default: the installed SDK package). */
  piVersion?: string;
  /** claude's binary override (tests; default: findClaudeBinary()). */
  claudeBinary?: string;
  /** claude's managed settings path override (tests; default: managedSettingsPath()). */
  managedSettingsPath?: string;
}

/** A write request's change payload; opaque to the gate, interpreted per concept from 85a2 on. */
export type WriteChange = Record<string, unknown>;

/** Every write's outcome: refused by the gate, not implemented yet, or written. */
export type WriteResult =
  | { status: "refused"; reason: string }
  | { status: "not-implemented"; reason: string }
  | { status: "written"; summary: string };

/** One harness adapter (AC1). `diagnose` is a signature only in 85a1 (Q5 A): 85a1b executes it. */
export interface HarnessAdapter {
  id: HarnessId;
  capabilities: HarnessCapabilities;
  probeVersion(ctx?: HarnessReadContext): Promise<VersionProbeResult>;
  read(concept: ConceptId, ctx?: HarnessReadContext): Promise<ConceptPayload | null>;
  write(concept: ConceptId, change: WriteChange, ctx?: HarnessReadContext): Promise<WriteResult>;
  diagnose?(ctx?: HarnessReadContext): Promise<unknown>;
}

/**
 * The shared write gate every adapter's write() passes first. Refuses an
 * undetected or unrecognized version with its reason; allows a known one.
 */
export function writeGate(probe: VersionProbeResult): { allowed: true } | { allowed: false; reason: string } {
  if (probe.version === null) {
    return { allowed: false, reason: probe.reason ?? "the harness version could not be detected" };
  }
  if (!probe.known) {
    return { allowed: false, reason: probe.reason ?? `version ${probe.version} is not recognized` };
  }
  return { allowed: true };
}

/**
 * The one write path every adapter shares: probe, gate, then the concept's
 * write — which no 85a1 concept has (writableConcepts is empty), so a known
 * version answers "not-implemented" for every concept. Adapters compose this
 * into their write(); no adapter may write without it.
 */
export async function gatedWrite(
  adapter: Pick<HarnessAdapter, "id" | "capabilities" | "probeVersion">,
  concept: ConceptId,
  _change: WriteChange,
  ctx?: HarnessReadContext,
): Promise<WriteResult> {
  const probe = await adapter.probeVersion(ctx);
  const gate = writeGate(probe);
  if (!gate.allowed) return { status: "refused", reason: gate.reason };
  return {
    status: "not-implemented",
    reason: `writing ${concept} on ${adapter.id} is not implemented yet (known version ${probe.version})`,
  };
}

const adapters = new Map<HarnessId, HarnessAdapter>();

/** Registers `adapter` under its id; a second registration of the same id replaces the first. */
export function registerHarness(adapter: HarnessAdapter): void {
  adapters.set(adapter.id, adapter);
}

/** The adapter registered under `id`, undefined when none is. */
export function getHarnessAdapter(id: HarnessId): HarnessAdapter | undefined {
  return adapters.get(id);
}

/** Every registered adapter, in registration order. */
export function listHarnessAdapters(): HarnessAdapter[] {
  return [...adapters.values()];
}
