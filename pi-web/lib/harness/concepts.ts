// Harness adapter framework (polyforge#85, unit 85a1): the neutral concept
// vocabulary every harness adapter speaks — concept ids, scopes, the neutral
// reasoning-effort and approval-mode enums, the payload types a per-concept
// read returns, and the capability declaration each adapter makes. Client-safe:
// never import a server-only module here; 85a1b's UI reads these types.
//
// The native config files are the single source of truth; the neutral enums are
// the unified model of parent ticket #85. A harness's own spelling maps onto a
// neutral value by neutralReasoningEffort()/neutralApprovalMode(); an unknown
// spelling maps to null and callers keep the raw value for display.

/** The harnesses pi-web has an adapter for. New harness = new adapter. */
export type HarnessId = "pi" | "claude";

/** The unified concepts of the first version (#85 c2 subset). */
export type ConceptId =
  | "auth"
  | "config-root"
  | "model"
  | "permission"
  | "skills"
  | "mcp"
  | "env"
  | "effective-config";

/**
 * The scope vocabulary of #85's unified model. `pi-web` is the pseudo-scope of
 * the settings Pi Web stores itself (pi-web-claude-settings.json,
 * claude-permissions.json) until they migrate back to native files (#147, #150).
 */
export type ScopeId = "managed" | "user" | "project" | "local" | "session" | "pi-web";

/** The neutral reasoning-effort levels, ordered. */
export const REASONING_EFFORT_LEVELS = [
  "off",
  "minimal",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
] as const;
export type ReasoningEffort = (typeof REASONING_EFFORT_LEVELS)[number];

/** The neutral approval modes of the unified model. */
export const APPROVAL_MODES = [
  "ask",
  "accept-edits",
  "plan",
  "auto",
  "deny-unprompted",
  "full",
] as const;
export type ApprovalMode = (typeof APPROVAL_MODES)[number];

/**
 * A harness spelling of a reasoning effort onto the neutral enum; pi's
 * `defaultThinkingLevel` and claude's `effortLevel` both use these spellings
 * verbatim. An unknown spelling maps to null (callers show the raw value).
 */
const REASONING_EFFORT_SET: ReadonlySet<string> = new Set(REASONING_EFFORT_LEVELS);

export function neutralReasoningEffort(raw: unknown): ReasoningEffort | null {
  return typeof raw === "string" && REASONING_EFFORT_SET.has(raw) ? (raw as ReasoningEffort) : null;
}

/** claude's permissions.defaultMode spellings onto the neutral enum. */
const APPROVAL_MODE_SPELLINGS: Readonly<Record<string, ApprovalMode>> = {
  default: "ask",
  acceptEdits: "accept-edits",
  "accept-edits": "accept-edits",
  plan: "plan",
  auto: "auto",
  deny: "deny-unprompted",
  "deny-unprompted": "deny-unprompted",
  bypassPermissions: "full",
  full: "full",
};

export function neutralApprovalMode(raw: unknown): ApprovalMode | null {
  return typeof raw === "string" ? APPROVAL_MODE_SPELLINGS[raw] ?? null : null;
}

/** A credential file's existence, never its contents. */
export interface CredentialRef {
  exists: boolean;
  path: string;
}

/** The auth concept (85a1: only what needs no command; status is unknown, Q4 A). */
export interface AuthPayload {
  status: "unknown";
  credentialStore: CredentialRef | null;
  /** Automation credential variable names and whether each is set — names, never values. */
  automationEnv: Record<string, boolean>;
  /** Whether the credentials live inside the config root directory. */
  credentialsFollow: boolean;
}

/** The config-root concept. */
export interface ConfigRootPayload {
  path: string;
  /** The environment variable that overrides the root, when one exists. */
  env: string | null;
  credentialsFollow: boolean;
}

/** A provider as the model concept lists it: never a key, only whether one is stored. */
export interface ProviderRef {
  name: string;
  hasApiKey: boolean;
}

/** The model concept. */
export interface ModelPayload {
  defaultProvider: string | null;
  defaultModel: string | null;
  /** The default reasoning effort, neutral plus the harness's raw spelling. */
  reasoningEffort: { neutral: ReasoningEffort | null; raw: string | null } | null;
  providers: ProviderRef[];
  /** pi-web-stored custom model names (claude only, until #147/#150 migrate them). */
  customModels: string[];
}

/** The permission concept. */
export interface PermissionPayload {
  approvalMode: {
    supported: boolean;
    neutral: ApprovalMode | null;
    raw: string | null;
  };
  projectTrust: { trusted: boolean | null; note?: string } | null;
}

/** One skill as the skills concept lists it: name/path/source only (#146: state
 * comes from the native config, never from frontmatter). */
export interface SkillRef {
  name: string;
  path: string;
  source: ScopeId;
}

/** The skills concept. */
export interface SkillsPayload {
  entries: SkillRef[];
  /** claude's native skillOverrides (settings layers): name → the harness's own state word, never a value. */
  overrides?: Record<string, string>;
}

/** One MCP server as the mcp concept lists it — masked (lib/mcp-secrets.ts), url and args only as maskUrl/maskArgs give them, header/env names without values. */
export interface MaskedMcpServer {
  name: string;
  scope: ScopeId;
  url?: { masked: string; hadSecret: boolean };
  command?: string;
  args?: { args: string[]; masked: boolean };
  headerNames?: string[];
  envNames?: string[];
  disabled?: boolean;
}

/** The mcp concept. */
export interface McpPayload {
  servers: MaskedMcpServer[];
}

/** The env concept: read-only; key names only, never values. */
export interface EnvPayload {
  /** Which environment the keys belong to (the harness's own settings env block or its automation variables). */
  target: string;
  setKeys: string[];
}

/** One effective value with its provenance (#85 d-5: self-computed, always `inferred`). */
export interface EffectiveValue {
  value: unknown;
  sourceScope: ScopeId;
  sourcePath: string;
  overriddenBy?: string;
  inferred: true;
}

/** The effective-config concept (#85 c2): per-field effective values and sources. */
export interface EffectiveConfigPayload {
  fields: Record<string, EffectiveValue>;
}

/** What any adapter's per-concept read returns (the payload shapes above). */
export type ConceptPayload =
  | AuthPayload
  | ConfigRootPayload
  | ModelPayload
  | PermissionPayload
  | SkillsPayload
  | McpPayload
  | EnvPayload
  | EffectiveConfigPayload;

/** The transports an MCP entry can use; pi holds no sse or ws (#85 85c, Q4 A). */
export const MCP_TRANSPORTS = ["stdio", "http", "sse", "ws"] as const;
export type McpTransport = (typeof MCP_TRANSPORTS)[number];

/**
 * The mcp concept's capability declaration (#85 c2, 85c): what a harness's
 * MCP support is. `enableToggle` false means the harness has no native
 * enable/disable of an entry — claude's apply-to is having it or not, so
 * unapplying removes it (owner ruling Q1 A) — and `cliAdd` true means every
 * write goes through the harness's own CLI, never a file Pi Web wrote.
 */
export interface McpCapability {
  /** The harness scopes this harness holds MCP entries in (claude adds local). */
  scopes: ScopeId[];
  /** The transports this harness accepts in an MCP entry. */
  transports: readonly McpTransport[];
  /** Writes go through the harness's own CLI (claude mcp add-json); false writes the native file itself. */
  cliAdd: boolean;
  /** The harness has a native enable/disable of an MCP entry (pi only, Q1 A). */
  enableToggle: boolean;
  /** The harness can filter which tools a server exposes (pi's exposure and toolExposure). */
  toolFilter: boolean;
}

/** The capability declaration each adapter makes (AC1). */
export interface HarnessCapabilities {
  id: HarnessId;
  /** The scopes this harness's config layers use. */
  scopes: ScopeId[];
  /** The mcp concept's own capabilities (#85 c2, declared from 85c on). */
  mcp?: McpCapability;
  /** The concepts this adapter reads. */
  concepts: ConceptId[];
  /** The concepts this adapter writes; empty in 85a1 (the writes land in 85a2–85d). */
  writableConcepts: readonly ConceptId[];
  /** Read-only concepts a caller must never be offered a write for. */
  readOnlyConcepts: readonly ConceptId[];
  /** The environment variable that overrides the config root, when one exists. */
  configRootEnv: string | null;
  /** Whether the credentials live inside the config root directory. */
  credentialsFollowConfigRoot: boolean;
  /** Native diagnostics: declared per Q5 A, executed only from 85a1b (#149). */
  nativeDiagnostics: { declared: true; implemented: boolean };
  /** The terminal login commands the account page may run (85a2 AC3);
   * empty = this harness logs in another way (pi: the SDK routes, Q4). */
  loginCommands: readonly HarnessLoginCommand[];
}

/**
 * A login command the account page may run in its one-shot terminal
 * (polyforge#142, 85a2 AC3): the adapter's own declaration — the terminal
 * runs exactly this argv, never anything a client sends. `cell` marks a
 * command that may run against a Cell's own config root (the terminal route
 * then injects the server-computed config-root env); false keeps it on the
 * operator's interactive environment (claude's `claude auth login` and
 * `claude setup-token`: the interactive session's own credentials are the
 * source, the 2026-11-19 Q3 revision).
 */
export interface HarnessLoginCommand {
  id: string;
  argv: string[];
  cell: boolean;
}

/**
 * What an adapter's native diagnostics answer (polyforge#149, AC6): the fixed
 * safe command's filtered output, or the visible reason it is unavailable.
 * Client-safe: the 生效值与来源 pane renders this shape from the route.
 */
export type NativeDiagnosticsResult =
  | { available: true; command: string[]; lines: string[]; truncated: boolean; timedOut: boolean }
  | { available: false; reason: string };
