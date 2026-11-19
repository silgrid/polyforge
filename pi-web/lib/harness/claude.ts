// The claude adapter (polyforge#85 85a1): reads claude's native config —
// the settings layers (managed > .claude/settings.local.json >
// .claude/settings.json > $CLAUDE_CONFIG_DIR/settings.json), the state file
// .claude.json, .mcp.json and the skills directories — through the same
// redaction allow-list as pi. The version probe reuses probeClaude()
// (lib/claude-probe.ts), the one probe implementation; CLAUDE_RECORDED_VERSIONS
// keeps its fixture semantics and the write gate uses its own range (Q2 C).
import { existsSync, readdirSync } from "node:fs";
import { basename, join } from "node:path";
import { claudeConfigDir } from "../claude-config-dir";
import { probeClaude } from "../claude-probe";
import { findClaudeBinary } from "../claude-session";
import { readClaudeSettings } from "../claude-settings";
import { defaultAgentDir } from "../http-dispatcher";
import {
  gatedWrite,
  CLAUDE_KNOWN_RANGE,
  versionInKnownRange,
  type HarnessAdapter,
  type HarnessReadContext,
  type VersionProbeResult,
  type WriteChange,
  type WriteResult,
} from "./adapter";
import { claudeSettingsLayers, claudeStateFilePath, managedSettingsPath, type ClaudeSettingsLayer } from "./claude-layers";
import {
  neutralApprovalMode,
  neutralReasoningEffort,
  type AuthPayload,
  type ConceptId,
  type ConceptPayload,
  type ConfigRootPayload,
  type EnvPayload,
  type MaskedMcpServer,
  type McpPayload,
  type ModelPayload,
  type PermissionPayload,
  type ScopeId,
  type SkillRef,
  type SkillsPayload,
} from "./concepts";
import { claudeEffectiveConfig } from "./effective";
import { automationEnvStatus, credentialRef, maskedMcpServer, readJsonRecord } from "./redact";

export { managedSettingsPath };

/** The credential environment variables claude answers, names only. */
export const CLAUDE_AUTOMATION_ENV_KEYS = [
  "ANTHROPIC_API_KEY",
  "ANTHROPIC_AUTH_TOKEN",
  "CLAUDE_CODE_OAUTH_TOKEN",
] as const;

type SettingsLayer = ClaudeSettingsLayer;

/** The first layer whose record holds `key` (the effective value), highest priority first. */
function effectiveLayer(layers: SettingsLayer[], key: string): { layer: SettingsLayer; value: unknown } | null {
  for (const layer of layers) {
    if (layer.record && key in layer.record) return { layer, value: layer.record[key] };
  }
  return null;
}

/** The `mcpServers` records of every source, in the order they are listed. */
function mcpSources(configDir: string, cwd: string | undefined, layers: SettingsLayer[]): { scope: ScopeId; servers: Record<string, unknown> }[] {
  const sources: { scope: ScopeId; servers: Record<string, unknown> }[] = [];
  const state = readJsonRecord(claudeStateFilePath({ claudeConfigDir: configDir }));
  if (state && isRecord(state.mcpServers)) sources.push({ scope: "user", servers: state.mcpServers });
  const projectState = cwd && state && isRecord(state.projects)
    ? (state.projects as Record<string, unknown>)[cwd]
    : undefined;
  if (projectState && isRecord(projectState) && isRecord((projectState as Record<string, unknown>).mcpServers)) {
    sources.push({ scope: "project", servers: (projectState as Record<string, unknown>).mcpServers as Record<string, unknown> });
  }
  for (const layer of [...layers].reverse()) {
    if (layer.record && isRecord(layer.record.mcpServers)) sources.push({ scope: layer.scope, servers: layer.record.mcpServers });
  }
  if (cwd) {
    const dotMcp = readJsonRecord(join(cwd, ".mcp.json"));
    if (dotMcp && isRecord(dotMcp.mcpServers)) sources.push({ scope: "project", servers: dotMcp.mcpServers });
  }
  return sources;
}

/** Whether `value` is a plain record. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** The key names of an entry's headers/env blocks, values never read. */
function keyNames(value: unknown): string[] {
  return isRecord(value) ? Object.keys(value).sort() : [];
}

/** One skills directory's SKILL.md files as entries. */
function skillDirEntries(dir: string, source: ScopeId): SkillRef[] {
  let dirents;
  try {
    dirents = readdirSync(dir, { withFileTypes: true });
  } catch {
    return [];
  }
  return dirents
    .filter((entry) => entry.isDirectory())
    .map((entry) => ({ name: entry.name, path: join(dir, entry.name, "SKILL.md"), source }))
    .filter((entry) => existsSync(entry.path));
}

export function createClaudeAdapter(): HarnessAdapter {
  const configDirOf = (ctx?: HarnessReadContext): string => ctx?.claudeConfigDir ?? claudeConfigDir();
  const envOf = (ctx?: HarnessReadContext): NodeJS.ProcessEnv => ctx?.env ?? process.env;
  const agentDirOf = (ctx?: HarnessReadContext): string => ctx?.agentDir ?? defaultAgentDir();

  async function probeVersion(ctx?: HarnessReadContext): Promise<VersionProbeResult> {
    const configDir = configDirOf(ctx);
    const binary = ctx?.claudeBinary ?? findClaudeBinary();
    const source = "claude initialize";
    if (!binary) return { version: null, known: false, reason: "claude was not found on PATH", source };
    const probed = await probeClaude(binary);
    if ("error" in probed) return { version: null, known: false, reason: probed.error, source };
    const version = probed.version;
    if (version === null) return { version: null, known: false, reason: "claude did not report a version", source };
    if (!versionInKnownRange(version, CLAUDE_KNOWN_RANGE)) {
      return { version, known: false, knownRange: CLAUDE_KNOWN_RANGE.label, source, reason: `version ${version} is outside the known range ${CLAUDE_KNOWN_RANGE.label}` };
    }
    // The structure probe of the write gate (Q2 C): a settings layer or the
    // state file that exists but is not a JSON object refuses the write.
    for (const layer of claudeSettingsLayers(configDir, ctx?.cwd)) {
      if (existsSync(layer.path) && layer.record === null) {
        return { version, known: false, knownRange: CLAUDE_KNOWN_RANGE.label, source, reason: `${basename(layer.path)} has an unrecognized top-level structure (expected a JSON object)` };
      }
    }
    const statePath = claudeStateFilePath(ctx);
    if (existsSync(statePath) && readJsonRecord(statePath) === null) {
      return { version, known: false, knownRange: CLAUDE_KNOWN_RANGE.label, source, reason: `${basename(statePath)} has an unrecognized top-level structure (expected a JSON object)` };
    }
    return { version, known: true, knownRange: CLAUDE_KNOWN_RANGE.label, source };
  }

  return {
    id: "claude",
    capabilities: {
      id: "claude",
      scopes: ["managed", "user", "project", "local"],
      concepts: ["auth", "config-root", "model", "permission", "skills", "mcp", "env", "effective-config"],
      writableConcepts: [],
      readOnlyConcepts: ["env", "effective-config"],
      configRootEnv: "CLAUDE_CONFIG_DIR",
      credentialsFollowConfigRoot: true,
      nativeDiagnostics: { declared: true, implemented: false },
    },
    probeVersion,
    async read(concept: ConceptId, ctx): Promise<ConceptPayload | null> {
      const configDir = configDirOf(ctx);
      const env = envOf(ctx);
      const cwd = ctx?.cwd;
      const layers = claudeSettingsLayers(configDir, cwd);
      switch (concept) {
        case "auth": {
          const payload: AuthPayload = {
            status: "unknown",
            credentialStore: credentialRef(join(configDir, ".credentials.json")),
            automationEnv: automationEnvStatus(env, CLAUDE_AUTOMATION_ENV_KEYS),
            credentialsFollow: true,
          };
          return payload;
        }
        case "config-root": {
          const payload: ConfigRootPayload = { path: configDir, env: "CLAUDE_CONFIG_DIR", credentialsFollow: true };
          return payload;
        }
        case "model": {
          const model = effectiveLayer(layers, "model");
          const effort = effectiveLayer(layers, "effortLevel");
          const rawEffort = effort && typeof effort.value === "string" ? effort.value : null;
          const payload: ModelPayload = {
            defaultProvider: null,
            defaultModel: model && typeof model.value === "string" ? model.value : null,
            reasoningEffort: rawEffort === null ? null : { neutral: neutralReasoningEffort(rawEffort), raw: rawEffort },
            providers: [],
            customModels: [...readClaudeSettings(agentDirOf(ctx)).customModels],
          };
          return payload;
        }
        case "permission": {
          const mode = effectiveLayer(layers, "permissions");
          const raw = mode && isRecord(mode.value) && typeof (mode.value as Record<string, unknown>).defaultMode === "string"
            ? (mode.value as Record<string, unknown>).defaultMode as string
            : null;
          let projectTrust: PermissionPayload["projectTrust"] = null;
          if (cwd) {
            const state = readJsonRecord(claudeStateFilePath({ ...ctx, claudeConfigDir: configDir }));
            const entry = state && isRecord(state.projects) ? (state.projects as Record<string, unknown>)[cwd] : undefined;
            const accepted = isRecord(entry) && (entry as Record<string, unknown>).hasTrustDialogAccepted === true;
            projectTrust = { trusted: accepted };
          }
          const payload: PermissionPayload = {
            approvalMode: { supported: true, neutral: raw === null ? null : neutralApprovalMode(raw), raw },
            projectTrust,
          };
          return payload;
        }
        case "skills": {
          const entries = [
            ...skillDirEntries(join(configDir, "skills"), "user"),
            ...(cwd ? skillDirEntries(join(cwd, ".claude", "skills"), "project") : []),
          ];
          const overrides: Record<string, string> = {};
          for (const layer of [...layers].reverse()) {
            if (!layer.record || !isRecord(layer.record.skillOverrides)) continue;
            for (const [name, state] of Object.entries(layer.record.skillOverrides as Record<string, unknown>)) {
              if (typeof state === "string") overrides[name] = state;
            }
          }
          const payload: SkillsPayload = Object.keys(overrides).length > 0 ? { entries, overrides } : { entries };
          return payload;
        }
        case "mcp": {
          const servers: MaskedMcpServer[] = [];
          for (const { scope, servers: entries } of mcpSources(configDir, cwd, layers)) {
            for (const [name, entry] of Object.entries(entries)) {
              if (!isRecord(entry)) continue;
              servers.push(maskedMcpServer(scope, {
                name,
                url: typeof entry.url === "string" ? entry.url : undefined,
                command: typeof entry.command === "string" ? entry.command : undefined,
                args: Array.isArray(entry.args) && entry.args.every((arg) => typeof arg === "string") ? (entry.args as string[]) : undefined,
                headerNames: keyNames(entry.headers),
                envNames: keyNames(entry.env),
              }));
            }
          }
          const payload: McpPayload = { servers };
          return payload;
        }
        case "env": {
          const keys = new Set<string>();
          for (const layer of layers) {
            if (layer.record && isRecord(layer.record.env)) {
              for (const key of Object.keys(layer.record.env as Record<string, unknown>)) keys.add(key);
            }
          }
          const payload: EnvPayload = { target: "claude", setKeys: [...keys].sort() };
          return payload;
        }
        case "effective-config":
          return claudeEffectiveConfig(ctx);
        default:
          return null;
      }
    },
    async write(concept: ConceptId, change: WriteChange, ctx): Promise<WriteResult> {
      return gatedWrite(this, concept, change, ctx);
    },
  };
}
