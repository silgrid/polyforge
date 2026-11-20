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

/** A recognized version range: its major is what the gate matches (Q4), the
 * minor/minPatch pair keeping the first version whose recording proves the
 * keys — the floor a concept's key registration names. */
export interface KnownVersionRange {
  major: number;
  minor: number;
  minPatch: number;
  label: string;
}

/** claude's recognized range: any 2.x (85a3's Q4 ruling — the same major is
 * recognized, an unknown major or an unrecognized shape refuses); 2.1.312 is
 * the first recorded version, the floor the model keys register.
 * CLAUDE_RECORDED_VERSIONS keeps its own fixture semantics. */
export const CLAUDE_KNOWN_RANGE: KnownVersionRange = {
  major: 2,
  minor: 1,
  minPatch: 312,
  label: "claude 2.x",
};

/** pi's recognized range for interactive sessions: any 1.x (Q4), the SDK
 * package's version the probe reads; 1.5.0 is the installed schema's
 * version, the floor the model keys register. */
export const PI_KNOWN_RANGE: KnownVersionRange = {
  major: 1,
  minor: 5,
  minPatch: 0,
  label: "pi 1.x",
};

/** `major.minor.patch` of a version string, null when it does not parse. */
export function parseVersion(version: string): [number, number, number] | null {
  const match = /^(\d+)\.(\d+)\.(\d+)/.exec(version.trim());
  if (!match) return null;
  return [Number(match[1]), Number(match[2]), Number(match[3])];
}

/**
 * The gate's recognition (85a3, owner ruling Q4): the same major is known,
 * whatever the minor or patch — an unknown major or an unrecognized file
 * structure is what refuses, never a newer patch or minor. The floors a
 * concept's own key registration names are checked separately by its
 * write (versionAtLeast).
 */
export function versionInKnownRange(version: string, range: KnownVersionRange): boolean {
  const parsed = parseVersion(version);
  if (!parsed) return false;
  return parsed[0] === range.major;
}

/**
 * Whether `version` is at or above `floor` (both `major.minor.patch`), the
 * check a registered key minimum runs against the probed version (85a3 Q4:
 * the gate recognizes the major; the key's floor refuses what came before
 * the version whose recording proves the key). An unparsable side is below.
 */
export function versionAtLeast(version: string, floor: string): boolean {
  const parsed = parseVersion(version);
  const minimum = parseVersion(floor);
  if (!parsed || !minimum) return false;
  for (let index = 0; index < 3; index++) {
    if (parsed[index] !== minimum[index]) return parsed[index] > minimum[index];
  }
  return true;
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
