// Per-layer effective values for the unified fields (polyforge#85 85a1, unit
// u4). Every value is computed, never observed: claude applies its settings
// `env` only after spawn (workbench#397) and each harness merges its layers
// its own way, so each value carries `inferred: true` (#85 d-5). Until the
// Pi Web-stored claude settings migrate back to native files (#147, #150),
// the claude side also lists the Pi Web layer (pi-web-claude-settings.json,
// claude-permissions.json) under the `pi-web` scope.
import { join } from "node:path";
import { claudeConfigDir } from "../claude-config-dir";
import { claudePermissionConfigPath, readClaudePermissionConfig } from "../claude-permission-config";
import { claudeSettingsPath, readClaudeSettings } from "../claude-settings";
import { defaultAgentDir } from "../http-dispatcher";
import type { HarnessReadContext } from "./adapter";
import { claudeSettingsLayers } from "./claude-layers";
import type { EffectiveConfigPayload, EffectiveValue } from "./concepts";
import { readJsonRecord } from "./redact";

/** Whether `value` is a plain record. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** The first layer whose record holds `key` as a string (the effective value), highest priority first. */
function effectiveString(layers: { scope: string; path: string; record: Record<string, unknown> | null }[], key: string): EffectiveValue | null {
  for (const layer of layers) {
    if (layer.record && key in layer.record && typeof layer.record[key] === "string") {
      return { value: layer.record[key] as string, sourceScope: layer.scope as EffectiveValue["sourceScope"], sourcePath: layer.path, inferred: true };
    }
  }
  return null;
}

/** pi's layers: the agent's settings.json, overridden by a project's `.pi/settings.json`. */
export function piEffectiveConfig(ctx?: HarnessReadContext): EffectiveConfigPayload {
  const agentDir = ctx?.agentDir ?? defaultAgentDir();
  const user = readJsonRecord(join(agentDir, "settings.json"));
  const project = ctx?.cwd ? readJsonRecord(join(ctx.cwd, ".pi", "settings.json")) : null;
  const projectPath = ctx?.cwd ? join(ctx.cwd, ".pi", "settings.json") : null;
  const layers = [
    ...(project && projectPath ? [{ scope: "project", path: projectPath, record: project }] : []),
    ...(user ? [{ scope: "user", path: join(agentDir, "settings.json"), record: user }] : []),
  ];
  const fields: Record<string, EffectiveValue> = {};
  const provider = effectiveString(layers, "defaultProvider");
  if (provider) fields["provider"] = provider;
  const model = effectiveString(layers, "defaultModel");
  if (model) fields["model"] = model;
  const effort = effectiveString(layers, "defaultThinkingLevel");
  if (effort) fields["reasoning-effort"] = effort;
  return { fields };
}

/** claude's layers (managed > local > project > user) plus the Pi Web-stored layer. */
export function claudeEffectiveConfig(ctx?: HarnessReadContext): EffectiveConfigPayload {
  const configDir = ctx?.claudeConfigDir ?? claudeConfigDir();
  const agentDir = ctx?.agentDir ?? defaultAgentDir();
  const layers = claudeSettingsLayers(configDir, ctx?.cwd);
  const fields: Record<string, EffectiveValue> = {};
  const model = effectiveString(layers, "model");
  if (model) fields["model"] = model;
  const effort = effectiveString(layers, "effortLevel");
  if (effort) fields["reasoning-effort"] = effort;
  for (const layer of layers) {
    const permissions = layer.record?.permissions;
    if (isRecord(permissions) && typeof permissions.defaultMode === "string") {
      fields["approval-mode"] = { value: permissions.defaultMode, sourceScope: layer.scope, sourcePath: layer.path, inferred: true };
      break;
    }
  }
  // The Pi Web-stored layer, until #147/#150 migrate it back to native files.
  fields["pi-web:launch-permission-mode"] = {
    value: readClaudeSettings(agentDir).launchPermissionMode,
    sourceScope: "pi-web",
    sourcePath: claudeSettingsPath(agentDir),
    inferred: true,
  };
  fields["pi-web:tool-permissions"] = {
    value: readClaudePermissionConfig(agentDir).mode,
    sourceScope: "pi-web",
    sourcePath: claudePermissionConfigPath(agentDir),
    inferred: true,
  };
  return { fields };
}
