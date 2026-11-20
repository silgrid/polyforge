"use client";

// The Settings › Skills panel (#85 85d): the unified listing — one row per
// real skill file, grouped 用户 / 项目, every harness that lists it on the
// row. The switch is per harness (AC2): a row's 「应用到」 writes only the
// chosen harness's own store — pi's preference (skillVisibility.pi, hidden
// from the model prompt but still manually invocable, effective only for
// sessions Pi Web starts) and claude's native skillOverrides (user skills in
// $CLAUDE_CONFIG_DIR/settings.json, project skills in the project's
// .claude/settings.local.json) — never the shared SKILL.md. The unified
// 「停用」 writes claude's "user-invocable-only" (owner Q4: 模型不可见、用户
// 仍可手动调用); claude's other native words live in its own partition. A
// group's 「全部启用 / 停用」 acts on pi alone (owner Q6, 85c's conclusion).
// Without a project the panel shows the user scope alone (AC3) and the add
// panel offers the global scope only, still installing for pi alone (Q7 B).

import { useState, useEffect, useCallback, useLayoutEffect, useRef } from "react";
import { useI18n } from "@/hooks/useI18n";
import type {
  SkillContentResponse,
  SkillInfo as Skill,
  SkillInstallScope,
  SkillSearchResult,
  SkillUpdateResult,
  ProjectTrustStatus,
  UnifiedSkillRow,
  UnifiedSkillsResponse,
  SkillApplyResult,
} from "@/lib/api-types";
import { displayPathWithin, shortenPath } from "@/lib/display-path";
import { parseFrontmatter } from "@/lib/frontmatter";
import {
  getLastSettingsSelection,
  setLastSettingsSelection,
} from "@/lib/settings-navigation";
import {
  ConfigAddSourceHeading,
  ConfigButton,
  ConfigDetail,
  ConfigDetailHeader,
  ConfigDetailHeaderInfo,
  ConfigDetailStack,
  ConfigEmptyState,
  ConfigField,
  ConfigFooter,
  ConfigListAction,
  ConfigPanelShell,
  ConfigSaveTarget,
  ConfigScopeTag,
  ConfigSidebar,
  ConfigSidebarGroupLabel,
  ConfigSidebarGroupStatus,
  ConfigSidebarGroupSwitch,
  ConfigSidebarItem,
  ConfigSidebarList,
  ConfigSidebarText,
  ConfigSplitView,
  ConfigStatusDot,
  ConfigTrustNotice,
} from "./SettingsUi";
import { projectTrustReloadKey } from "./settings-ui-helpers";
import { HarnessApplyTo, applyToCanSubmit, applyToTargets, type HarnessApplyToSelection, type HarnessApplyToResult, type HarnessApplyToTarget } from "./HarnessApplyTo";
import { MarkdownBody } from "./MarkdownBody";

type Translate = ReturnType<typeof useI18n>["t"];

/** One switch item as PATCH /api/skills takes it (85d). */
export interface SkillSwitchItemWire {
  skill: string;
  harness: "pi" | "claude";
  state: string;
  scope?: "user" | "project";
}

/** The row's 「应用到」 targets: a harness the row does not list is disabled with its reason (未列出). */
export function rowSwitchTargets(
  row: Pick<UnifiedSkillRow, "pi" | "claude">,
  texts: { pi: string; claude: string; notListed: string },
): HarnessApplyToTarget[] {
  return [
    {
      harness: "pi",
      label: texts.pi,
      ...(row.pi ? {} : { disabled: true, disabledReason: texts.notListed }),
    },
    {
      harness: "claude",
      label: texts.claude,
      ...(row.claude ? {} : { disabled: true, disabledReason: texts.notListed }),
    },
  ];
}

/**
 * The items a row's submission sends (AC2): one per chosen harness, each in
 * that harness's own words — pi's path and enabled/disabled, claude's native
 * skillOverrides key with the unified 停用 mapped to "user-invocable-only"
 * (owner Q4) and the row's scope layer. A harness the row does not list is
 * never reached.
 */
export function rowSwitchItems(
  row: Pick<UnifiedSkillRow, "pi" | "claude" | "scope">,
  harnesses: readonly ("pi" | "claude")[],
  unified: "enabled" | "disabled",
): SkillSwitchItemWire[] {
  const items: SkillSwitchItemWire[] = [];
  for (const harness of harnesses) {
    if (harness === "pi" && row.pi) {
      items.push({ skill: row.pi.filePath, harness: "pi", state: unified });
    } else if (harness === "claude" && row.claude) {
      items.push({
        skill: row.claude.name,
        harness: "claude",
        state: unified === "disabled" ? "user-invocable-only" : "enabled",
        scope: row.scope,
      });
    }
  }
  return items;
}

/**
 * After one round's results: the rows whose items succeeded take the state
 * that was sent, each on its own side; a refused item keeps its row as it
 * was. `sent` carries what the round asked of each harness.
 */
export function applyRowSwitchResults(
  rows: UnifiedSkillRow[],
  results: SkillApplyResult[],
  sent: { pi?: "enabled" | "disabled"; claude?: string },
): UnifiedSkillRow[] {
  const succeeded = new Set(results.filter((result) => !result.error).map((result) => `${result.harness}\0${result.skill}`));
  return rows.map((row) => {
    if (row.pi && sent.pi !== undefined && succeeded.has(`pi\0${row.pi.filePath}`)) {
      return { ...row, pi: { ...row.pi, disableModelInvocation: sent.pi === "disabled" } };
    }
    if (row.claude && sent.claude !== undefined && succeeded.has(`claude\0${row.claude.name}`)) {
      return { ...row, claude: { ...row.claude, ...(sent.claude ? { state: sent.claude } : {}) } };
    }
    return row;
  });
}

/** The group switch's rows: the pi sides whose state would change — currently
 * off when switching on, currently on when switching off (85c's conclusion:
 * the group acts on pi alone). */
export function rowsToSwitch(rows: UnifiedSkillRow[], enabled: boolean): UnifiedSkillRow[] {
  return rows.filter((row) => row.pi && row.pi.disableModelInvocation === enabled);
}

/** A row is dormant when every side that lists it is off (pi hidden, claude's override any word but "enabled"). */
export function orderRowsByDormancy(rows: UnifiedSkillRow[]): UnifiedSkillRow[] {
  const claudeOff = (row: UnifiedSkillRow) => row.claude?.state !== undefined && row.claude.state !== "enabled";
  const dormant = (row: UnifiedSkillRow) => (!row.pi || row.pi.disableModelInvocation) && (!row.claude || claudeOff(row));
  return [
    ...rows.filter((row) => !dormant(row)),
    ...rows.filter(dormant),
  ];
}

/** The author-hidden badge keys on the declared frontmatter alone, never on the effective switch. */
export function skillAuthorDeclaredHidden(skill: Pick<Skill, "declaredDisableModelInvocation">): boolean {
  return skill.declaredDisableModelInvocation === true;
}

/** The sidebar's scope groups (AC1/AC3): 用户 always, 项目 only with a project; empty groups never render. */
export function skillRowGroups(
  rows: UnifiedSkillRow[],
  hasProject: boolean,
): { scope: "user" | "project"; rows: UnifiedSkillRow[] }[] {
  const scopes: ("user" | "project")[] = hasProject ? ["user", "project"] : ["user"];
  return scopes
    .map((scope) => ({ scope, rows: rows.filter((row) => row.scope === scope) }))
    .filter((group) => group.rows.length > 0);
}

/** The add panel's scope choices (Q7 B): without a project the global scope alone. */
export function addScopeChoices(hasProject: boolean): ("global" | "project")[] {
  return hasProject ? ["global", "project"] : ["global"];
}

function updateKey(skill: Pick<Skill, "install">): string | null {
  if (!skill.install) return null;
  return `${skill.install.scope}\0${skill.install.package}`;
}

function shortVersion(version: string | undefined, unknown: string): string {
  return version ? version.slice(0, 8) : unknown;
}

export function skillInstructionsBody(content: string): string {
  return parseFrontmatter(content).rest.trim();
}

/** A row's display state: off when every side that lists it is off (pi hidden, claude's override any word but "enabled"). */
function rowIsOff(row: Pick<UnifiedSkillRow, "pi" | "claude">): boolean {
  const claudeOff = row.claude?.state !== undefined && row.claude.state !== "enabled";
  return (!row.pi || row.pi.disableModelInvocation) && (!row.claude || claudeOff);
}

export function resolveSkillLink(href: string, fromDocument: string): { document: string; anchor: string } {
  const hash = href.indexOf("#");
  const [linkPath, anchor] = [hash < 0 ? href : href.slice(0, hash), hash < 0 ? "" : href.slice(hash + 1)]
    .map((part) => { try { return decodeURIComponent(part); } catch { return part; } });
  if (!linkPath) return { document: fromDocument, anchor };
  if (linkPath.startsWith("/")) return { document: linkPath, anchor };
  const parts = fromDocument.split("/").slice(0, -1);
  for (const part of linkPath.split("/")) {
    if (part === ".." && parts.length && parts[parts.length - 1] !== "..") parts.pop();
    else if (part && part !== ".") parts.push(part);
  }
  return { document: parts.join("/"), anchor };
}

/** Every harness the row lists, as a small chip row. */
function HarnessChips({ row }: { row: Pick<UnifiedSkillRow, "harnesses"> }) {
  return (
    <span className="skill-harness-chips">
      {row.harnesses.map((harness) => (
        <span key={harness} className={`skill-harness-chip is-${harness}`}>{harness}</span>
      ))}
    </span>
  );
}

function SkillDetail({
  row,
  piSkill,
  cwd,
  applyResults,
  applyBusy,
  saveError,
  claudeSaveError,
  updateStatus,
  checkingUpdate,
  updating,
  updateError,
  onCheckUpdate,
  onUpdate,
  onCleanupLegacy,
  cleaningLegacy,
  onApplySwitch,
  onWriteClaudeState,
}: {
  row: UnifiedSkillRow;
  /** The pi listing's own record of the row's file, when pi lists it. */
  piSkill: Skill | null;
  cwd: string | null;
  applyResults: HarnessApplyToResult[] | null;
  applyBusy: boolean;
  saveError: string | null;
  claudeSaveError: string | null;
  updateStatus?: SkillUpdateResult;
  checkingUpdate: boolean;
  updating: boolean;
  updateError: string | null;
  onCheckUpdate: () => void;
  onUpdate: () => void;
  onCleanupLegacy: (skill: Skill) => void;
  cleaningLegacy: boolean;
  /** The row's unified switch: the chosen harnesses take the chosen state. */
  onApplySwitch: (row: UnifiedSkillRow, harnesses: ("pi" | "claude")[], state: "enabled" | "disabled") => void;
  /** claude's own partition: one native skillOverrides word. */
  onWriteClaudeState: (row: UnifiedSkillRow, state: string) => void;
}) {
  const { t } = useI18n();
  const [selection, setSelection] = useState<HarnessApplyToSelection>(() => ({ mode: "all", selected: [] }));
  const [claudeState, setClaudeState] = useState<string>(row.claude?.state ?? "enabled");
  useEffect(() => {
    setSelection({ mode: "all", selected: [] });
    setClaudeState(row.claude?.state ?? "enabled");
  }, [row]);
  const [instructions, setInstructions] = useState<string | null>(null);
  const [instructionsError, setInstructionsError] = useState<string | null>(null);
  // Documents opened from the instructions' links; the last one shows.
  const filePath = row.pi?.filePath ?? row.claude!.filePath;
  const rootDocument = filePath.split(/[\\/]/).pop()!;
  const [documents, setDocuments] = useState<string[]>([]);
  const documentPath = documents.at(-1) ?? rootDocument;
  const instructionsRef = useRef<HTMLDivElement>(null);
  // The heading to bring into view once its document shows; "" is the top.
  const pendingAnchor = useRef<string | null>(null);
  const [anchorRequest, setAnchorRequest] = useState(0);
  const baseDir = piSkill?.baseDir ?? filePath.split(/[\\/]/).slice(0, -1).join("/");

  const openLink = useCallback((href: string) => {
    const link = resolveSkillLink(href, documentPath);
    pendingAnchor.current = link.anchor;
    if (link.document === documentPath) setAnchorRequest((request) => request + 1);
    else setDocuments((open) => [...open, link.document]);
  }, [documentPath]);

  useLayoutEffect(() => {
    const anchor = pendingAnchor.current;
    const root = instructionsRef.current;
    if (anchor === null || (instructions === null && !instructionsError) || !root) return;
    pendingAnchor.current = null;
    const heading = anchor ? root.querySelector(`[data-heading-anchor="${CSS.escape(anchor)}"]`) : null;
    (heading ?? root).scrollIntoView({ block: "start" });
  }, [instructions, instructionsError, anchorRequest]);

  useEffect(() => {
    const controller = new AbortController();
    setInstructions(null);
    setInstructionsError(null);

    const load = async () => {
      try {
        const params = new URLSearchParams({ filePath });
        if (cwd) params.set("cwd", cwd);
        if (documentPath !== rootDocument) params.set("document", documentPath);
        const response = await fetch(`/api/skills/content?${params}`, { signal: controller.signal });
        const data = (await response.json()) as Partial<SkillContentResponse> & { error?: string };
        if (!response.ok || data.error || typeof data.content !== "string") {
          throw new Error(data.error ?? `HTTP ${response.status}`);
        }
        setInstructions(skillInstructionsBody(data.content));
      } catch (error) {
        if (controller.signal.aborted) return;
        setInstructionsError(error instanceof Error ? error.message : String(error));
      }
    };

    void load();
    return () => controller.abort();
  }, [cwd, filePath, documentPath, rootDocument]);

  const targets = rowSwitchTargets(row, {
    pi: "pi",
    claude: "claude",
    notListed: t("skills.harnessNotListed"),
  });
  const reachable = applyToTargets(selection, targets).filter((harness) => !targets.find((target) => target.harness === harness)?.disabled) as ("pi" | "claude")[];
  const rowOff = rowIsOff(row);

  return (
    <ConfigDetailStack>
      {/* Path + scope tag + harness chips, with a stable status row below. */}
      <div className="skill-detail-heading">
        <ConfigDetailHeader>
          <ConfigDetailHeaderInfo>
            <ConfigScopeTag scope={row.scope}>{t(row.scope === "user" ? "skills.scope.user" : "skills.scope.project")}</ConfigScopeTag>
            <HarnessChips row={row} />
            <span className="config-detail-path">
              {row.scope === "project" && cwd ? displayPathWithin(filePath, cwd) : shortenPath(filePath)}
            </span>
          </ConfigDetailHeaderInfo>
        </ConfigDetailHeader>
        <div className="skill-detail-status-row">
          {row.sameName.length > 0 && (
            <span style={{ fontSize: 11, color: "var(--text-dim)" }}>
              {t("skills.sameName", { count: row.sameName.length + 1 })}
            </span>
          )}
          {saveError && (
            <span style={{ fontSize: 12, color: "#f87171", overflowWrap: "anywhere" }}>
              {saveError}
            </span>
          )}
        </div>
      </div>

      {/* The unified switch: 「应用到」 + 启用 / 停用, one request per chosen harness. */}
      <ConfigField label={t("skills.switchField")}>
        <HarnessApplyTo
          texts={{
            title: t("skills.applyTo.title"),
            all: t("skills.applyTo.all"),
            specific: t("skills.applyTo.specific"),
            submit: "",
          }}
          targets={targets}
          value={selection}
          busy={applyBusy}
          results={applyResults}
          onChange={setSelection}
        />
        <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
          <ConfigButton
            size="small"
            disabled={applyBusy || reachable.length === 0}
            onClick={() => onApplySwitch(row, reachable, "enabled")}
          >
            {t("skills.stateEnabled")}
          </ConfigButton>
          <ConfigButton
            size="small"
            disabled={applyBusy || reachable.length === 0}
            onClick={() => onApplySwitch(row, reachable, "disabled")}
          >
            {t("skills.stateDisabled")}
          </ConfigButton>
        </div>
        <span style={{ fontSize: 11, color: "var(--text-dim)", display: "block", marginTop: 6 }}>
          {t("skills.switchHint")}
        </span>
      </ConfigField>

      {/* pi's own partition: the effective state and its meaning. */}
      {row.pi && (
        <ConfigField label="pi">
          <span style={{ fontSize: 11, color: "var(--text-dim)" }}>
            {row.pi.disableModelInvocation
              ? t("skills.piHiddenHint")
              : t("skills.piVisibleHint")}
          </span>
          {piSkill && skillAuthorDeclaredHidden(piSkill) && (
            <>
              <span style={{ fontSize: 11, color: "var(--text-dim)", display: "block" }}>
                {t("skills.authorHiddenDefault")}
              </span>
              <ConfigButton
                size="small"
                onClick={() => onCleanupLegacy(piSkill)}
                disabled={cleaningLegacy || applyBusy}
                style={{ marginTop: 6 }}
              >
                {t("skills.cleanupLegacy")}
              </ConfigButton>
            </>
          )}
        </ConfigField>
      )}

      {/* claude's own partition: the native state and every native word (Q4 A+C). */}
      {row.claude && (
        <ConfigField label={t("skills.claudePartition")}>
          <span style={{ fontSize: 11, color: "var(--text-dim)" }}>
            {t("skills.claudeStateHint")}
          </span>
          <div style={{ display: "flex", gap: 8, marginTop: 6, alignItems: "center" }}>
            <select
              value={claudeState}
              onChange={(e) => setClaudeState(e.target.value)}
              aria-label={t("skills.claudeStateLabel")}
              style={{ padding: "5px 8px", fontSize: 12, background: "var(--bg-panel)", border: "1px solid var(--border)", borderRadius: 6, color: "var(--text)" }}
            >
              {(["enabled", "user-invocable-only", "name-only", "disabled"] as const).map((state) => (
                <option key={state} value={state}>{t(`skills.claudeState.${state}`)}</option>
              ))}
            </select>
            <ConfigButton
              size="small"
              disabled={applyBusy}
              onClick={() => onWriteClaudeState(row, claudeState)}
            >
              {t("skills.claudeStateApply")}
            </ConfigButton>
          </div>
          {row.claude.state && (row.claude.state === "enabled" || row.claude.state === "user-invocable-only" || row.claude.state === "name-only" || row.claude.state === "disabled") && (
            <span style={{ fontSize: 11, color: "var(--text-dim)", display: "block", marginTop: 6 }}>
              {t("skills.claudeStateCurrent", { state: t(`skills.claudeState.${row.claude.state}`) })}
            </span>
          )}
          {claudeSaveError && (
            <span style={{ fontSize: 12, color: "#f87171", overflowWrap: "anywhere" }}>
              {claudeSaveError}
            </span>
          )}
          <span style={{ fontSize: 11, color: "var(--text-dim)", display: "block", marginTop: 6 }}>
            {t("skills.claudeNotListedHint")}
          </span>
        </ConfigField>
      )}

      {piSkill?.install?.skillsShUrl && (
        <ConfigField label={t("config.source")}>
          <a
            href={piSkill.install.skillsShUrl}
            target="_blank"
            rel="noreferrer"
            title={piSkill.install.skillsShUrl}
            className="skill-source-link"
          >
            <span className="skill-source-link-text">
              {piSkill.install.skillsShUrl.replace(/^https?:\/\//, "")} ↗
            </span>
          </a>
        </ConfigField>
      )}

      {piSkill?.install && (
        <ConfigField label={t("i18n.version")}>
          <div className="skill-version-row">
            <span className="skill-version-value">
              {shortVersion(updateStatus?.currentVersion ?? piSkill.install.versionHash, t("i18n.unknown"))}
            </span>
            {piSkill.install.canCheckForUpdates && (
              <ConfigButton
                size="small"
                onClick={onCheckUpdate}
                disabled={checkingUpdate || updating}
              >
                 {t("i18n.check")}
              </ConfigButton>
            )}
            {updateStatus?.state === "update-available" && (
              <span className="skill-version-value is-update">
                {shortVersion(updateStatus.latestVersion, t("i18n.unknown"))}
              </span>
            )}
            {(checkingUpdate ||
              (updateStatus && updateStatus.state !== "update-available")) && (
              <span
                className={`skill-update-status ${checkingUpdate
                  ? "is-checking"
                  : updateStatus?.state === "up-to-date"
                    ? "is-success"
                    : updateStatus?.state === "error"
                      ? "is-error"
                      : "is-muted"}`}
              >
                {checkingUpdate
                   ? t("i18n.checking")
                  : updateStatus?.state === "up-to-date"
                     ? t("i18n.upToDate")
                    : updateStatus?.state === "unsupported"
                         ? t("i18n.automaticChecksUnavailable")
                         : updateStatus?.message || t("i18n.checkFailed")}
              </span>
            )}
            {updateStatus?.state === "update-available" && (
              <ConfigButton
                variant="primary"
                size="small"
                onClick={onUpdate}
                disabled={updating || checkingUpdate}
              >
                 {updating ? t("i18n.updating") : t("i18n.update")}
              </ConfigButton>
            )}
          </div>
          {updateError && (
            <span style={{ fontSize: 12, color: "#ef4444" }}>{updateError}</span>
          )}
        </ConfigField>
      )}

      <ConfigField label={t("config.name")}>
        <span className="skill-name-value">
          {row.name}
        </span>
      </ConfigField>

      {piSkill && (
        <ConfigField label={t("i18n.description")}>
          <span className="skill-description">
            {piSkill.description}
          </span>
        </ConfigField>
      )}

      <ConfigField label={t("skills.instructions")} ref={instructionsRef}>
        {documents.length > 0 && (
          <ConfigDetailHeader>
            <ConfigDetailHeaderInfo>
              <ConfigButton size="small" onClick={() => { pendingAnchor.current = ""; setDocuments((open) => open.slice(0, -1)); }}>
                {t("skills.documentBack")}
              </ConfigButton>
              <span className="config-detail-path" title={documentPath}>{documentPath}</span>
            </ConfigDetailHeaderInfo>
          </ConfigDetailHeader>
        )}
        {instructionsError ? (
          <span role="alert" className="skill-instructions-status is-error">{instructionsError}</span>
        ) : instructions === null ? (
          <span role="status" className="skill-instructions-status">{t("i18n.loading")}</span>
        ) : instructions ? (
          <MarkdownBody className="skill-instructions" cwd={[baseDir, ...documentPath.split("/").slice(0, -1)].join("/")} onNavigateLink={openLink}>
            {instructions}
          </MarkdownBody>
        ) : (
          <span className="skill-instructions-status">{t("skills.noInstructions")}</span>
        )}
      </ConfigField>
    </ConfigDetailStack>
  );
}

function AddSkillPanel({
  cwd,
  installedPackages,
  projectResourcesLoaded,
  onInstalled,
}: {
  cwd: string | null;
  installedPackages: Record<SkillInstallScope, ReadonlySet<string>>;
  projectResourcesLoaded: boolean;
  onInstalled: () => void;
}) {
  const { t } = useI18n();
  const hasProject = cwd !== null;
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SkillSearchResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [installing, setInstalling] = useState<string | null>(null);
  const [installError, setInstallError] = useState<string | null>(null);
  const [newlyInstalledPkgs, setNewlyInstalledPkgs] = useState<Set<string>>(
    new Set(),
  );
  const [scope, setScope] = useState<"global" | "project">("global");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const search = useCallback(async (q: string) => {
    if (!q.trim()) return;
    setSearching(true);
    setSearchError(null);
    setResults([]);
    try {
      const res = await fetch("/api/skills/search", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ query: q.trim() }),
      });
      const d = (await res.json()) as {
        results?: SkillSearchResult[];
        error?: string;
      };
      if (d.error) {
        setSearchError(d.error);
        return;
      }
      setResults(d.results ?? []);
      if ((d.results ?? []).length === 0) setSearchError(t("i18n.noSkills"));
    } catch (e) {
      setSearchError(String(e));
    } finally {
      setSearching(false);
    }
  }, [t]);

  const install = useCallback(
    async (pkg: string) => {
      setInstalling(pkg);
      setInstallError(null);
      try {
        const res = await fetch("/api/skills/install", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ package: pkg, scope, ...(cwd ? { cwd } : {}) }),
        });
        const d = (await res.json()) as { success?: boolean; error?: string };
        if (!res.ok || d.error) {
          setInstallError(d.error ?? `HTTP ${res.status}`);
          return;
        }
        setNewlyInstalledPkgs((prev) =>
          new Set(prev).add(`${scope}:${pkg}`),
        );
        onInstalled();
      } catch (e) {
        setInstallError(String(e));
      } finally {
        setInstalling(null);
      }
    },
    [onInstalled, scope, cwd],
  );

  const installPath =
    scope === "global"
      ? "~/.pi/agent/skills/"
      : `${shortenPath(cwd ?? "")}/.pi/skills/`;

  return (
    <ConfigDetailStack className="is-full-height">
      {/* ── Header area ── */}
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 12,
          marginBottom: 20,
        }}
      >
        <ConfigAddSourceHeading
          title={t("i18n.addSkill")}
          catalogs={[{ href: "https://skills.sh", label: "skills.sh" }]}
          target={
            <ConfigSaveTarget
              value={scope}
              label={t("config.saveTo")}
              options={addScopeChoices(hasProject).map((choice) => ({
                value: choice,
                label: t(choice === "global" ? "skills.scope.global" : "skills.scope.project"),
                ...(choice === "project" && !projectResourcesLoaded ? { disabled: true } : {}),
              }))}
              path={installPath}
              disabledReason={t("trust.projectScopeUnavailable")}
              onChange={(next) => setScope(next as "global" | "project")}
            />
          }
        />
        {/* Install stays pi's own (Q7 B): the entry says so. */}
        <span style={{ fontSize: 11, color: "var(--text-dim)" }}>
          {t("skills.installPiOnly")}
        </span>

        {/* Search row */}
        <div style={{ display: "flex", gap: 8 }}>
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") search(query);
            }}
             placeholder={t("i18n.skillSearchPlaceholder")}
            style={{
              flex: 1,
              padding: "7px 10px",
              fontSize: 12,
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: 6,
              color: "var(--text)",
              outline: "none",
            }}
          />
          <ConfigButton
            variant="primary"
            onClick={() => search(query)}
            disabled={searching || !query.trim()}
          >
             {searching ? t("i18n.searching") : t("i18n.search")}
          </ConfigButton>
        </div>

        {/* Errors */}
        {searchError && (
          <div style={{ fontSize: 12, color: "#f87171" }}>{searchError}</div>
        )}
        {installError && (
          <div
            style={{ fontSize: 12, color: "#f87171", wordBreak: "break-word" }}
          >
            {installError}
          </div>
        )}
      </div>

      {/* ── Results list ── */}
      {results.length > 0 && (
        <div style={{ flex: 1, overflowY: "auto" }}>
          {results.map((r) => {
            const isInstalled =
              installedPackages[scope].has(r.package) ||
              newlyInstalledPkgs.has(`${scope}:${r.package}`);
            const isInstalling = installing === r.package;
            // split "owner/repo@skill" for cleaner display
            const atIdx = r.package.indexOf("@");
            const repopart = atIdx > -1 ? r.package.slice(0, atIdx) : r.package;
            const skillpart = atIdx > -1 ? r.package.slice(atIdx + 1) : null;
            return (
              <div
                key={r.package}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 14,
                  padding: "12px 0",
                  borderBottom: "1px solid var(--border)",
                }}
              >
                <div style={{ flex: 1, minWidth: 0 }}>
                  {/* skill name prominent */}
                  <div
                    style={{
                      fontSize: 13,
                      fontWeight: 600,
                      color: "var(--text)",
                      marginBottom: 3,
                    }}
                  >
                    {skillpart ?? repopart}
                  </div>
                  {/* repo + installs + link row */}
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 10,
                      flexWrap: "wrap",
                    }}
                  >
                    <span
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 11,
                        color: "var(--text-dim)",
                      }}
                    >
                      {repopart}
                    </span>
                    <span
                      style={{
                        fontSize: 12,
                        color: "var(--text-muted)",
                        fontWeight: 500,
                      }}
                    >
                      {r.installs}
                    </span>
                    {r.url && (
                      <a
                        href={r.url}
                        target="_blank"
                        rel="noreferrer"
                        style={{
                          fontSize: 12,
                          color: "var(--accent)",
                          textDecoration: "none",
                        }}
                      >
                        skills.sh ↗
                      </a>
                    )}
                  </div>
                </div>
                <ConfigButton
                  size="small"
                  onClick={() =>
                    !isInstalled && !isInstalling && install(r.package)
                  }
                  disabled={isInstalled || isInstalling || installing !== null}
                  style={{
                    flexShrink: 0,
                    background: isInstalled ? "rgba(34,197,94,0.1)" : "none",
                    color: isInstalled
                      ? "#16a34a"
                      : isInstalling
                        ? "var(--accent)"
                        : "var(--text-muted)",
                  }}
                >
                  {isInstalled
                     ? `✓ ${t("i18n.installed")}`
                    : isInstalling
                       ? t("i18n.installing")
                       : t("i18n.install")}
                </ConfigButton>
              </div>
            );
          })}
        </div>
      )}
    </ConfigDetailStack>
  );
}

export function SkillsConfig({
  cwd,
  onClose,
  embedded = false,
  trust,
}: {
  cwd: string | null;
  onClose: () => void;
  embedded?: boolean;
  /** The page's trust status for `cwd`; a new decision loads the list again. */
  trust?: ProjectTrustStatus | null;
}) {
  const { t } = useI18n();
  const hasProject = cwd !== null;
  const [rows, setRows] = useState<UnifiedSkillRow[]>([]);
  // The pi listing's own records (description, baseDir, install details).
  const [piSkills, setPiSkills] = useState<Skill[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(() => getLastSettingsSelection("skills", cwd));
  const [toggling, setToggling] = useState<Set<string>>(new Set());
  const [saveError, setSaveError] = useState<string | null>(null);
  const [claudeSaveError, setClaudeSaveError] = useState<string | null>(null);
  const [applyResults, setApplyResults] = useState<HarnessApplyToResult[] | null>(null);
  // The group switch that is running, and what the last one left undone.
  const [bulkGroup, setBulkGroup] = useState<string | null>(null);
  const [groupStatus, setGroupStatus] = useState<{ group: string; error: string } | null>(null);
  const [addMode, setAddMode] = useState(false);
  const [updateStatuses, setUpdateStatuses] = useState<Record<string, SkillUpdateResult>>({});
  const [checkingUpdates, setCheckingUpdates] = useState<Set<string>>(new Set());
  const [checkingAll, setCheckingAll] = useState(false);
  const [updatingSkill, setUpdatingSkill] = useState<string | null>(null);
  const [updateError, setUpdateError] = useState<string | null>(null);
  const [projectResourcesLoaded, setProjectResourcesLoaded] = useState(true);

  const loadSkills = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const query = cwd ? `?cwd=${encodeURIComponent(cwd)}` : "";
      const res = await fetch(`/api/skills${query}`);
      const d = (await res.json()) as Partial<UnifiedSkillsResponse> & { error?: string };
      if (!res.ok || d.error) throw new Error(d.error ?? `HTTP ${res.status}`);
      const list = d.rows ?? [];
      const piList = d.skills ?? [];
      setRows(list);
      setPiSkills(piList);
      setProjectResourcesLoaded(d.projectResourcesLoaded ?? true);
      setSelected((current) => {
        if (current && list.some((row) => row.key === current)) return current;
        const initialRow = list.find((row) => !row.pi?.disableModelInvocation) ?? list[0];
        return initialRow?.key ?? null;
      });
      return list;
    } catch (e) {
      setError(String(e));
      return [];
    } finally {
      setLoading(false);
    }
  }, [cwd]);

  useEffect(() => {
    setUpdateStatuses({});
    setUpdateError(null);
    void loadSkills();
  }, [cwd]); // eslint-disable-line react-hooks/exhaustive-deps

  // Whether the project's skills load follows its trust, which can change while
  // this section stays mounted (hidden) in Settings: trusting from Settings ›
  // MCP. A new decision loads the list again in place, keeping the selection and
  // any update checks; the first load is the effect above.
  const trustKey = projectTrustReloadKey(trust);
  const loadedTrustKeyRef = useRef(trustKey);
  useEffect(() => {
    if (loadedTrustKeyRef.current === trustKey) return;
    loadedTrustKeyRef.current = trustKey;
    void loadSkills();
  }, [trustKey, loadSkills]);

  useEffect(() => {
    if (selected) setLastSettingsSelection("skills", selected, cwd);
  }, [cwd, selected]);

  const checkForUpdates = useCallback(async (skill?: Skill) => {
    const targets = skill
      ? [skill]
      : piSkills.filter((item) => Boolean(item.install));
    const keys = targets
      .map(updateKey)
      .filter((key): key is string => Boolean(key));
    if (keys.length === 0) return;

    setUpdateError(null);
    setCheckingUpdates((current) => new Set([...current, ...keys]));
    if (!skill) setCheckingAll(true);
    try {
      const res = await fetch("/api/skills/check", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          ...(cwd ? { cwd } : {}),
          package: skill?.install?.package,
          scope: skill?.install?.scope,
        }),
      });
      const data = (await res.json()) as {
        updates?: SkillUpdateResult[];
        error?: string;
      };
      if (!res.ok || data.error) throw new Error(data.error ?? `HTTP ${res.status}`);
      setUpdateStatuses((current) => {
        const next = { ...current };
        for (const update of data.updates ?? []) {
          next[`${update.scope}\0${update.package}`] = update;
        }
        return next;
      });
    } catch (e) {
      setUpdateError(e instanceof Error ? e.message : String(e));
    } finally {
      setCheckingUpdates((current) => {
        const next = new Set(current);
        for (const key of keys) next.delete(key);
        return next;
      });
      if (!skill) setCheckingAll(false);
    }
  }, [cwd, piSkills]);

  const updateInstalledSkill = useCallback(async (skill: Skill) => {
    if (!skill.install) return;
    const key = updateKey(skill)!;
    setUpdatingSkill(key);
    setUpdateError(null);
    try {
      const res = await fetch("/api/skills/update", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          ...(cwd ? { cwd } : {}),
          package: skill.install.package,
          scope: skill.install.scope,
        }),
      });
      const data = (await res.json()) as {
        success?: boolean;
        skill?: Skill;
        error?: string;
      };
      if (!res.ok || data.error || !data.success) {
        throw new Error(data.error ?? `HTTP ${res.status}`);
      }
      await loadSkills();
      const versionHash = data.skill?.install?.versionHash;
      setUpdateStatuses((current) => ({
        ...current,
        [key]: {
          package: skill.install!.package,
          scope: skill.install!.scope,
          state: "up-to-date",
          currentVersion: versionHash,
          latestVersion: versionHash,
        },
      }));
    } catch (e) {
      setUpdateError(e instanceof Error ? e.message : String(e));
    } finally {
      setUpdatingSkill(null);
    }
  }, [cwd, loadSkills]);

  /** One switch round: the items go in one PATCH, the results come back per item. */
  const switchRound = useCallback(async (
    rowKeys: string[],
    items: SkillSwitchItemWire[],
  ): Promise<SkillApplyResult[] | null> => {
    if (items.length === 0) return [];
    setToggling((current) => new Set([...current, ...rowKeys]));
    setSaveError(null);
    try {
      const res = await fetch("/api/skills", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ items }),
      });
      const d = (await res.json()) as { results?: SkillApplyResult[]; error?: string };
      if (!res.ok || d.error || !d.results) throw new Error(d.error ?? `HTTP ${res.status}`);
      return d.results;
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : String(e));
      return null;
    } finally {
      setToggling((current) => {
        const next = new Set(current);
        for (const key of rowKeys) next.delete(key);
        return next;
      });
    }
  }, []);

  /** The row's unified switch (AC2): only the chosen harnesses' own stores. */
  const applySwitch = useCallback(async (
    row: UnifiedSkillRow,
    harnesses: ("pi" | "claude")[],
    state: "enabled" | "disabled",
  ) => {
    setApplyResults(null);
    setGroupStatus(null);
    const items = rowSwitchItems(row, harnesses, state);
    const results = await switchRound([row.key], items);
    if (results === null) return;
    setRows((prev) => applyRowSwitchResults(prev, results, {
      ...(row.pi && harnesses.includes("pi") ? { pi: state } : {}),
      ...(row.claude && harnesses.includes("claude") ? { claude: state === "disabled" ? "user-invocable-only" : "enabled" } : {}),
    }));
    setApplyResults(results.map((result) => ({
      harness: result.harness,
      ok: !result.error,
      text: result.error ?? t(result.harness === "pi" ? "skills.piWritten" : "skills.claudeWritten"),
    })));
  }, [switchRound, t]);

  /** claude's own partition: one native word through the same gated write. */
  const writeClaudeState = useCallback(async (row: UnifiedSkillRow, state: string) => {
    setClaudeSaveError(null);
    const results = await switchRound([row.key], [
      { skill: row.claude!.name, harness: "claude", state, scope: row.scope },
    ]);
    if (results === null) return;
    const failed = results.find((result) => result.error);
    if (failed) {
      setClaudeSaveError(failed.error ?? "the write was not made");
      return;
    }
    setRows((prev) => applyRowSwitchResults(prev, results, { claude: state }));
  }, [switchRound]);

  // One PATCH for the whole group: each side is written on its own and
  // reported separately, so a harness the route refuses keeps its state and
  // is named under the group heading while the rest switch. The group acts
  // on pi alone (85c's conclusion, owner Q6).
  const setGroupRows = useCallback(async (group: string, groupRows: UnifiedSkillRow[], enabled: boolean) => {
    const targets = rowsToSwitch(groupRows, enabled);
    if (targets.length === 0) return;
    const items = targets.flatMap((row) => rowSwitchItems(row, ["pi"], enabled ? "enabled" : "disabled"));
    setBulkGroup(group);
    setGroupStatus(null);
    try {
      const results = await switchRound(targets.map((row) => row.key), items);
      if (results === null) return;
      setRows((prev) => applyRowSwitchResults(prev, results, { pi: enabled ? "enabled" : "disabled" }));
      const failures = results.filter((result) => result.error);
      if (failures.length > 0) {
        const names = new Map(items.map((item) => [item.skill, item.skill]));
        setGroupStatus({
          group,
          error: [
            t("skills.bulkFailed", { count: failures.length, total: results.length }),
            ...failures.map((failure) => `${names.get(failure.skill) ?? failure.skill}: ${failure.error}`),
          ].join("\n"),
        });
      }
    } finally {
      setBulkGroup(null);
    }
  }, [switchRound, t]);

  const [cleaningLegacy, setCleaningLegacy] = useState<string | null>(null);
  const cleanupLegacy = useCallback(async (skill: Skill) => {
    // One confirmed skill at a time (owner Q3 B): the dialog names what the
    // route does — the hidden preference first, then the line's removal.
    if (!window.confirm(t("skills.cleanupLegacyConfirm", { name: skill.name }))) return;
    setCleaningLegacy(skill.filePath);
    setSaveError(null);
    try {
      const res = await fetch("/api/skills/cleanup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ filePath: skill.filePath }),
      });
      const d = (await res.json()) as { success?: boolean; error?: string };
      if (!res.ok || d.error) throw new Error(d.error ?? `HTTP ${res.status}`);
      await loadSkills();
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : String(e));
    } finally {
      setCleaningLegacy(null);
    }
  }, [t, loadSkills]);

  const selectedRow = rows.find((row) => row.key === selected) ?? null;
  const selectedPiSkill = selectedRow?.pi
    ? piSkills.find((skill) => skill.filePath === selectedRow.pi?.filePath) ?? null
    : null;
  const bulkBusy = loading || toggling.size > 0 || updatingSkill !== null;
  const groups = skillRowGroups(rows, hasProject);

  return (
    <ConfigPanelShell
      embedded={embedded}
      title={t("common.skills")}
      subtitle={cwd ? shortenPath(cwd) : t("skills.noProjectSubtitle")}
      closeLabel={t("i18n.close")}
      onClose={onClose}
    >

        {!projectResourcesLoaded && <ConfigTrustNotice message={t("trust.skillsNotLoaded")} />}

        {/* Body */}
        <ConfigSplitView>
          {/* Left: skill rows, grouped 用户 / 项目 */}
          <ConfigSidebar>
            <ConfigSidebarList>
              {loading ? (
                <div className="config-sidebar-message">
                   {t("i18n.loading")}
                </div>
              ) : error ? (
                <div className="config-sidebar-message is-error">
                  {error}
                </div>
              ) : groups.length === 0 ? (
                <div className="config-sidebar-message is-empty">
                   {t("i18n.noSkills")}
                </div>
              ) : (
                groups.map(({ scope: groupScope, rows: groupRows }) => {
                  const groupLabel = t(groupScope === "user" ? "skills.scope.user" : "skills.scope.project");
                  const piRows = groupRows.filter((row) => row.pi);
                  const visible = groupRows.filter((row) => !rowIsOff(row)).length;
                  const renderRow = (row: UnifiedSkillRow) => {
                    const isSelected = !addMode && selected === row.key;
                    const rowOff = rowIsOff(row);
                    return (
                      <ConfigSidebarItem
                        key={row.key}
                        active={isSelected}
                        onClick={() => {
                          setSelected(row.key);
                          setAddMode(false);
                        }}
                      >
                        <ConfigStatusDot active={!rowOff} />
                        <ConfigSidebarText className={`is-grow${rowOff ? " is-muted" : ""}`}>
                          {row.name}
                        </ConfigSidebarText>
                        <HarnessChips row={row} />
                        {(() => {
                          const key = row.pi ? updateKey({ install: row.pi.install }) : null;
                          const status = key ? updateStatuses[key] : undefined;
                          if (status?.state !== "update-available") return null;
                          return (
                            <span title={t("i18n.updateAvailable")} className="skill-update-indicator">
                              ↑
                            </span>
                          );
                        })()}
                      </ConfigSidebarItem>
                    );
                  };
                  return (
                    <div key={groupLabel} className="config-sidebar-group">
                      <ConfigSidebarGroupLabel
                        aside={piRows.length > 0 ? (
                          <ConfigSidebarGroupSwitch
                            enabled={visible}
                            total={groupRows.length}
                            disabled={bulkBusy}
                            loading={bulkGroup === groupLabel}
                            label={t(visible === groupRows.length ? "skills.groupSwitchOn" : "skills.groupSwitchOff", { group: groupLabel })}
                            onChange={(enabled) => void setGroupRows(groupLabel, groupRows, enabled)}
                          />
                        ) : undefined}
                      >
                        {groupLabel}
                      </ConfigSidebarGroupLabel>
                      {groupStatus?.group === groupLabel && <ConfigSidebarGroupStatus error={groupStatus.error} />}
                      {orderRowsByDormancy(groupRows).map(renderRow)}
                    </div>
                  );
                })
              )}
            </ConfigSidebarList>
            {/* Add skill button */}
            <ConfigListAction
                onClick={() => setAddMode(true)}
                active={addMode}
              >
                 {t("i18n.addSkill")}
            </ConfigListAction>
          </ConfigSidebar>

          {/* Right: detail or add panel */}
          <ConfigDetail>
            <ConfigDetailStack className="is-fill">
              {addMode ? (
              <AddSkillPanel
                cwd={cwd}
                projectResourcesLoaded={projectResourcesLoaded}
                installedPackages={{
                  global: new Set(
                    piSkills
                      .filter((skill) => skill.install?.scope === "global")
                      .map((skill) => skill.install!.package),
                  ),
                  project: new Set(
                    piSkills
                      .filter((skill) => skill.install?.scope === "project")
                      .map((skill) => skill.install!.package),
                  ),
                }}
                onInstalled={() => {
                  void loadSkills();
                }}
              />
            ) : loading ? null : selectedRow ? (
              <SkillDetail
                key={selectedRow.key}
                row={selectedRow}
                piSkill={selectedPiSkill}
                cwd={cwd}
                applyResults={applyResults}
                applyBusy={toggling.has(selectedRow.key)}
                saveError={saveError}
                claudeSaveError={claudeSaveError}
                updateStatus={
                  selectedPiSkill && updateKey(selectedPiSkill)
                    ? updateStatuses[updateKey(selectedPiSkill)!]
                    : undefined
                }
                checkingUpdate={
                  selectedPiSkill && updateKey(selectedPiSkill)
                    ? checkingUpdates.has(updateKey(selectedPiSkill)!)
                    : false
                }
                updating={selectedPiSkill ? updatingSkill === updateKey(selectedPiSkill) : false}
                updateError={updateError}
                onCheckUpdate={() => selectedPiSkill && void checkForUpdates(selectedPiSkill)}
                onUpdate={() => selectedPiSkill && void updateInstalledSkill(selectedPiSkill)}
                onCleanupLegacy={(skill) => void cleanupLegacy(skill)}
                cleaningLegacy={selectedPiSkill ? cleaningLegacy === selectedPiSkill.filePath : false}
                onApplySwitch={(row, harnesses, state) => void applySwitch(row, harnesses, state)}
                onWriteClaudeState={(row, state) => void writeClaudeState(row, state)}
              />
              ) : (
                <ConfigEmptyState>{t("i18n.selectSkill")}</ConfigEmptyState>
              )}
            </ConfigDetailStack>
          </ConfigDetail>
        </ConfigSplitView>

        {/* Footer */}
        <ConfigFooter status={
            Object.values(updateStatuses).filter(
              (status) => status.state === "update-available",
            ).length > 0 && (
              <span style={{ fontSize: 12, color: "#d97706" }}>
                {
                  Object.values(updateStatuses).filter(
                    (status) => status.state === "update-available",
                  ).length
                }{" "}
                {Object.values(updateStatuses).filter(
                  (status) => status.state === "update-available",
                ).length === 1
                   ? t("i18n.update")
                   : t("i18n.updates")}
              </span>
            )}
        >
          {!embedded && <ConfigButton onClick={onClose}>{t("i18n.close")}</ConfigButton>}
          {piSkills.some((skill) => Boolean(skill.install)) && (
            <ConfigButton variant="secondary" onClick={() => void checkForUpdates()} disabled={checkingAll || updatingSkill !== null}>
              {checkingAll ? t("i18n.checking") : t("i18n.checkUpdates")}
            </ConfigButton>
          )}
        </ConfigFooter>
    </ConfigPanelShell>
  );
}
