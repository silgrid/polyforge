"use client";

import { Fragment, useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useI18n } from "@/hooks/useI18n";
import type { TranslationParams } from "@/lib/i18n/types";
import type { PolyforgeDetectStatus } from "@/lib/polyforge-detect";
import type { PolyforgeConfigHistoryEntry } from "@/lib/polyforge-config-history";
import type { PolyforgeConfigItem, PolyforgeConfigRead, PolyforgeMemoryPolicyEntry } from "@/lib/polyforge-config";
import {
  CELL_KEYS,
  CLI_COMMON_KEYS,
  CLI_WRITABLE_KEYS,
  CONNECTION_KEYS,
  configValueError,
  HOST_ONLY_KEYS,
  IDENTITY_KEYS,
  parseWorkspaceWriteKey,
  POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION,
  skipWriteKey,
  stepValueError,
  stepWriteKey,
  SUMMARIZE_KEYS,
  workspaceProjectsValue,
  workspaceWriteKey,
} from "@/lib/polyforge-config-schema";
import type { PolyforgeWorkflowsRead } from "@/lib/polyforge-workflows";
import {
  addCandidate,
  buildWorkflowRows,
  defaultProject,
  moveCandidate,
  projectOwner,
  removeCandidate,
  workflowUnion,
  type WorkflowRow,
} from "@/lib/polyforge-workflows-pure";
import type { PluginPackageInfo } from "@/lib/api-types";
import {
  POLYFORGE_DEFAULT_PROFILE,
  polyforgeCellFileName,
  polyforgeCellListState,
  polyforgeCellNotice,
  type PolyforgeCell,
  type PolyforgeCellListState,
} from "@/lib/polyforge-cells-pure";
import type { PolyforgeCellsRead } from "@/lib/polyforge-cells";
import { shortenPath } from "@/lib/display-path";
import {
  POLYFORGE_POLL_INTERVAL,
  POLYFORGE_TITLE_LENGTH,
  writePolyforgePreference,
  type PolyforgePreferenceSpec,
} from "@/lib/polyforge-panel-preferences";
import { usePolyforgePanelPreferences } from "@/hooks/usePolyforgePanelPreferences";
import { usePolyforgeDisclosure } from "@/hooks/usePolyforgeDisclosure";
import type { PolyforgeDisclosureRegion } from "@/lib/polyforge-disclosure";
import {
  ConfigButton,
  ConfigDetail,
  ConfigDetailActions,
  ConfigDetailNavigationContext,
  ConfigDetailGrid,
  ConfigDetailGridRow,
  ConfigDetailHeader,
  ConfigDetailHeaderInfo,
  ConfigDetailStack,
  ConfigDetailTitle,
  ConfigFooter,
  ConfigFooterStatus,
  ConfigListAction,
  ConfigPanelShell,
  ConfigSectionTitle,
  ConfigSidebar,
  ConfigSidebarGroupLabel,
  ConfigSidebarGroupStatus,
  ConfigSidebarItem,
  ConfigSidebarList,
  ConfigSidebarText,
  ConfigSplitView,
  ConfigStatusDot,
  ConfigSwitch,
} from "./SettingsUi";
import {
  cellAddDraftError,
  type CellAddDraft,
  groupConfigItems,
  HOST_REGION_READ_URL,
  hostRegionWriteBody,
  isUnsetValue,
  policyEntryNames,
  regionShownValue,
  workspaceProjects,
  workspaceSectionPath,
  type ConfigGroupId,
} from "./polyforge-config-helpers";
import { PolyforgeInstallCard } from "./PolyforgeInstallCard";

/**
 * Settings › Polyforge: machine-level detection (pi#122) plus the
 * configuration panel. Every read and write goes through
 * `/api/polyforge/**`, which in turn calls `pf`; this component never reads
 * or writes cli.toml / `.polyforge.yaml`. Why a control is unavailable is
 * visible text, never only a tooltip, and an api_key value is write-only.
 * The Jobs-tab card preferences are the one exception to
 * "through pf": two numbers kept in this browser's localStorage only.
 *
 * the page is master-detail, the shared split view of the
 * other settings panels. The list holds the Host, then every Cell with its
 * state, then Add Cell; the detail shows the selected entry only. The Host
 * pane is the one home of the Host-level settings (status, Host-only keys,
 * the package switch, the card preferences, the config history, the pf
 * install / update entry and the install command); a Cell
 * pane holds that Cell's own settings. A pane mounts on its
 * first selection and stays mounted (hidden) while the section is, so a
 * draft survives a switch.
 *
 * every Cell's settings are one component, `CellSettings` —
 * the file card of that Cell's own file (cli.toml / cli-<profile>.toml), the
 * editable 工作区 card, Reload and the workflow card, every call carrying the
 * Cell's profile. The default and a named Cell differ only where pf's facts
 * do (the file name, a named Cell's [model] zone, the Cell head).
 */

/** what the detail shows — the Host, one Cell, or Add Cell. */
type PaneSelection = { kind: "host" } | { kind: "cell"; profile: string } | { kind: "add" };

function paneKey(selection: PaneSelection): string {
  return selection.kind === "cell" ? `cell:${selection.profile}` : selection.kind;
}

/** A list entry's tone: its status dot and badge color. */
type EntryTone = "on" | "off" | "warning" | "error";

const CELL_STATE_TONE: Record<PolyforgeCellListState, EntryTone> = {
  serving: "on",
  "pending-restart": "warning",
  degraded: "error",
  "execution-disabled": "warning",
  removed: "off",
};

function entryDot(tone: EntryTone): { active?: boolean; color?: string } {
  if (tone === "on") return { active: true };
  if (tone === "off") return { active: false };
  return { color: tone === "error" ? "#ef4444" : "#f59e0b" };
}

interface Props {
  cwd: string | null;
  /** True while the section is the settings dialog's active section. */
  active: boolean;
}

type RowFeedback = { message: string; tone: "default" | "error" };

type SourceState = { status: "pending" | "found" | "missing"; source: string | null };

/** Every key the panel writes: the cli.toml keys and the
 *  cli.toml [summarize] keys the write route's own
 *  allowlist constants. */
const WRITABLE = new Set([...CLI_WRITABLE_KEYS, ...SUMMARIZE_KEYS]);

function isNumericKey(key: string): boolean {
  return key === "max_concurrent_jobs" || key === "summarize.interval_sec";
}

/** pf's reload classes, localized. An unknown class still
 *  renders pf's own name through the fallback, never as a bare raw token. */
const RELOAD_CLASS_KEYS: Record<string, string> = {
  "restart-only": "polyforge.settings.classRestartOnly",
  "cli-only": "polyforge.settings.classCliOnly",
  live: "polyforge.settings.classLive",
  "next-claim": "polyforge.settings.classNextClaim",
};

/** A list-valued key (summarize.models) renders as chips, never one comma string. */
function isModelListKey(item: PolyforgeConfigItem): boolean {
  return item.schema?.kind === "model-list" || item.key === "summarize.models";
}

function modelListValue(value: unknown): string[] {
  const list = Array.isArray(value)
    ? value.filter((entry): entry is string => typeof entry === "string" && entry.trim() !== "")
    : typeof value === "string"
      ? value.split(",").map((part) => part.trim()).filter(Boolean)
      : [];
  // Chips key on the model name, so a duplicate from pf must not collide.
  return [...new Set(list)];
}

/** The three keys the spec gives a daemon column (claude_bin does not). */
function isDaemonColumnKey(key: string): boolean {
  return key === "max_concurrent_jobs" || key === "step_timeout" || key === "stall_window";
}

/** rendering a data-driven Latin token so a line breaks only
 *  after one of these separators (or beside CJK), never between two letters.
 *  A <wbr> adds the break opportunity without changing the text (copy, search,
 *  String(item.value) all stay identical); overflow-wrap stays the fallback
 *  only for a segment that is itself wider than its box. */
const BREAK_AFTER = "-_/.:,;|";

function breakableParts(text: string): string[] {
  const parts: string[] = [];
  let start = 0;
  for (let index = 0; index < text.length; index += 1) {
    if (BREAK_AFTER.includes(text[index]) && index + 1 < text.length) {
      parts.push(text.slice(start, index + 1));
      start = index + 1;
    }
  }
  if (start < text.length) parts.push(text.slice(start));
  return parts;
}

function BreakableToken({ text }: { text: string }) {
  const parts = breakableParts(text);
  return (
    <>
      {parts.map((part, index) => (
        <Fragment key={index}>
          {part}
          {index < parts.length - 1 ? <wbr /> : null}
        </Fragment>
      ))}
    </>
  );
}

/** One Cell's file card holds its file zones —
 *  the top-level keys, the `[daemon]` table, the `[model]` table (a named
 *  Cell's model.source,) and the `[summarize]` table — plus
 *  the read-only `[memory_policy]` blocks grouped per workspace path (added
 *  at render); the 工作区 card holds the file's
 *  `[workspace]` mapping rows only. */
type ConfigZoneId = "flat" | "daemon" | "model" | "summarize";
type ConfigRowZone = ConfigZoneId | "workspace";

/** Each zone header names the literal file section it writes, after the
 *  Cell's own file name (never translated): `cli-public.toml [daemon]`. */
const ZONE_SECTION: Record<ConfigZoneId, string> = {
  flat: "",
  daemon: " [daemon]",
  model: " [model]",
  summarize: " [summarize]",
};
/** The disclosure region that folds each zone's advanced rows (the #212
 *  regions, reused: no new stored field). The model and summarize zones
 *  have no advanced row, so their region never renders a fold. */
const ZONE_REGION: Record<ConfigZoneId, PolyforgeDisclosureRegion> = {
  flat: "cliToml",
  daemon: "daemon",
  model: "cliToml",
  summarize: "cliToml",
};

function configCards<T extends { key: string; section?: boolean }>(groups: { id: ConfigGroupId; items: T[] }[]): { cli: { id: ConfigZoneId; items: T[] }[]; workspace: T[] } {
  const itemsOf = (id: ConfigGroupId) => groups.find((group) => group.id === id)?.items ?? [];
  return {
    cli: (["flat", "daemon", "model", "summarize"] as const).map((id) => ({ id, items: itemsOf(id) })).filter((zone) => zone.items.length > 0),
    workspace: itemsOf("workspace"),
  };
}

/** The read snapshot the CAS compares: only the cli.toml keys the file
 *  actually declares (source `file`), with api_key reduced to {set}. */
function expectedValues(items: PolyforgeConfigItem[]): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  for (const item of items) {
    if (item.section) continue;
    if (item.source !== "file") continue;
    if (item.key === "api_key") { values[item.key] = { set: item.set === true }; continue; }
    if (item.value !== undefined) values[item.key] = item.value;
  }
  return values;
}

function itemValue(item: PolyforgeConfigItem): unknown {
  if (item.key === "api_key") return "";
  return item.value ?? "";
}

function formValue(key: string, raw: string): unknown {
  if (key === "api_key") return raw;
  if (isNumericKey(key)) return Number(raw);
  if (key === "summarize.models") return raw.split(",").map((part) => part.trim()).filter(Boolean);
  return raw;
}

type Translate = (key: string, params?: TranslationParams) => string;

/** pf's reload class, localized (an unknown class keeps pf's own name). */
function reloadClassLabelOf(t: Translate, reloadClass: string): string {
  const key = RELOAD_CLASS_KEYS[reloadClass];
  return key ? t(key) : t("polyforge.settings.classUnknown", { class: reloadClass });
}

/** A region's toggle state: open = locked || stored; while
 *  locked (a draft or an error, `marker`) a toggle attempt writes nothing. */
function regionToggles(disclosure: Record<PolyforgeDisclosureRegion, boolean>, setRegion: (region: PolyforgeDisclosureRegion, open: boolean) => void) {
  return (region: PolyforgeDisclosureRegion, marker: string | null) => ({
    open: marker !== null || disclosure[region],
    marker,
    // While locked a toggle attempt keeps the region open and writes nothing.
    onToggle: () => { if (marker === null) setRegion(region, !disclosure[region]); },
  });
}

/** One row's (or history entry's) inline result; an error is an alert. */
function RowFeedbackLine({ feedback }: { feedback: RowFeedback | undefined }) {
  if (!feedback) return null;
  return (
    <p
      className={`polyforge-config-row-feedback${feedback.tone === "error" ? " is-error" : ""}`}
      role={feedback.tone === "error" ? "alert" : undefined}
    >
      {feedback.message}
    </p>
  );
}

/** One rollback (a history entry, or a saved connection row's notice): the
 *  history route's fresh read + the same CAS write, into the entry's own
 *  file (its recorded scope). Resolves to pf's message; throws its refusal. */
async function postRollback(entry: PolyforgeConfigHistoryEntry, cwd: string | null): Promise<string | undefined> {
  const response = await fetch("/api/polyforge/config/history", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: entry.id, key: entry.key, old: entry.old, entry, cwd: cwd ?? "" }),
  });
  const data = await response.json() as { error?: string; message?: string };
  if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
  return data.message;
}

/** One browser-stored numeric preference: the input, its Save beside it,
 *  and its visible hint. Saving clamps, stores and notifies the open panel;
 *  no request is made. */
function PanelPreferenceRow({ spec, value, label, hint, saveLabel }: {
  spec: PolyforgePreferenceSpec;
  value: number;
  label: string;
  hint: string;
  saveLabel: string;
}) {
  // null: untouched, the input shows the stored value.
  const [draft, setDraft] = useState<string | null>(null);
  const save = () => {
    writePolyforgePreference(spec, draft ?? value);
    setDraft(null);
  };
  return (
    <ConfigDetailGridRow label={label} tone="plain">
      <span className="polyforge-config-write">
        <input
          type="number"
          aria-label={label}
          min={spec.min}
          max={spec.max}
          step={1}
          value={draft ?? String(value)}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") { event.preventDefault(); save(); }
          }}
        />
        <ConfigButton size="small" onClick={save}>{saveLabel}</ConfigButton>
      </span>
      <span className="config-scope-switch-reason">{hint}</span>
    </ConfigDetailGridRow>
  );
}

/** One region's open/closed toggle: a real button carrying
 *  aria-expanded/aria-controls; the chevron is CSS, the label is the children. */
function DisclosureToggle({ open, controls, onToggle, marker, children }: {
  open: boolean;
  controls: string;
  onToggle: () => void;
  /** Visible runtime note (an unsaved draft or an error keeps the region open). */
  marker?: string | null;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      className="polyforge-disclosure-toggle"
      aria-expanded={open}
      aria-controls={controls}
      onClick={onToggle}
    >
      {children}
      {marker ? <span className="polyforge-disclosure-marker">{marker}</span> : null}
    </button>
  );
}

export function PolyforgeConfig({ cwd, active }: Props) {
  const { t } = useI18n();
  const [detect, setDetect] = useState<PolyforgeDetectStatus | null>(null);
  const [detectError, setDetectError] = useState<string | null>(null);
  const [sourceState, setSourceState] = useState<SourceState>({ status: "pending", source: null });
  const [toggleBusy, setToggleBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionMessage, setActionMessage] = useState<string | null>(null);
  // a history rollback's result is keyed by entry.id and
  // rendered under that entry; the footer keeps only page-level status
  // (detection, package switch, a Cell's Reload).
  const [rowFeedback, setRowFeedback] = useState<Record<string, RowFeedback>>({});
  const [installMessage, setInstallMessage] = useState<string | null>(null);
  const [history, setHistory] = useState<PolyforgeConfigHistoryEntry[]>([]);
  const [writeBusy, setWriteBusy] = useState<string | null>(null);
  // bumped after a history rollback, so every mounted Cell
  // pane re-reads the file the rollback wrote.
  const [configEpoch, setConfigEpoch] = useState(0);
  const detectRanForActive = useRef(false);
  const { titleLength, pollIntervalSec } = usePolyforgePanelPreferences();
  // each region's open/closed state, this browser only.
  const { disclosure, setRegion } = usePolyforgeDisclosure();
  const regionToggle = regionToggles(disclosure, setRegion);
  const regionId = useId();
  // the list's Cell registry (the Cells card's read, moved up:
  // the list, the Cell panes and Add Cell share it) and the selected entry.
  const [cellsRead, setCellsRead] = useState<PolyforgeCellsRead | null>(null);
  const [cellsError, setCellsError] = useState<string | null>(null);
  const [cellBusy, setCellBusy] = useState<string | null>(null);
  // A lifecycle result renders in the pane it came from (a profile, or "add").
  const [cellMessage, setCellMessage] = useState<{ pane: string; tone: "ok" | "error"; text: string } | null>(null);
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);
  const [selection, setSelection] = useState<PaneSelection>({ kind: "host" });
  const [visited, setVisited] = useState<ReadonlySet<string>>(() => new Set(["host"]));
  const select = useCallback((next: PaneSelection) => {
    setSelection(next);
    setVisited((current) => (current.has(paneKey(next)) ? current : new Set(current).add(paneKey(next))));
  }, []);

  const runDetect = useCallback(async (force: boolean) => {
    try {
      const response = await fetch(`/api/polyforge/detect${force ? "?force=1" : ""}`);
      const data = await response.json() as PolyforgeDetectStatus & { error?: string };
      if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
      setDetect(data);
      setDetectError(null);
    } catch (error) {
      setDetectError(error instanceof Error ? error.message : String(error));
    }
  }, []);

  useEffect(() => {
    if (!active) {
      detectRanForActive.current = false;
      return;
    }
    if (detectRanForActive.current) return;
    detectRanForActive.current = true;
    void runDetect(false);
  }, [active, runDetect]);

  const runHistory = useCallback(async () => {
    try {
      const response = await fetch("/api/polyforge/config/history");
      const data = await response.json() as { entries?: PolyforgeConfigHistoryEntry[] };
      if (response.ok) setHistory(Array.isArray(data.entries) ? data.entries : []);
    } catch {
      // An unreadable history is simply empty; the write paths still work.
    }
  }, []);

  // the Host's Cells (`GET /api/polyforge/cells`); a pf
  // without the multi-Cell capabilities answers supported:false and the page
  // shows the upgrade hint with no lifecycle control.
  const runCellsRead = useCallback(async (): Promise<PolyforgeCellsRead | null> => {
    try {
      const response = await fetch("/api/polyforge/cells");
      const body = await response.json() as PolyforgeCellsRead;
      setCellsRead(body);
      setCellsError(body.error ?? null);
      return body;
    } catch (error) {
      setCellsError(error instanceof Error ? error.message : String(error));
      return null;
    }
  }, []);

  useEffect(() => {
    if (active) void runCellsRead();
  }, [active, runCellsRead]);

  // One Cell lifecycle write (add / repair / remove) through its existing
  // route, then a fresh registry read; the outcome lands in its pane.
  // Resolves to whether pf accepted it, plus the registry read after it.
  const cellAct = useCallback(async (pane: string, key: string, url: string, init: RequestInit, done: string) => {
    setCellBusy(key);
    setCellMessage(null);
    try {
      const response = await fetch(url, init);
      const body = await response.json() as { ok?: boolean; error?: string };
      if (!response.ok || body.error) throw new Error(body.error ?? `HTTP ${response.status}`);
      setCellMessage({ pane, tone: "ok", text: done });
      return { ok: true, read: await runCellsRead() };
    } catch (error) {
      setCellMessage({ pane, tone: "error", text: error instanceof Error ? error.message : String(error) });
      return { ok: false, read: null };
    } finally {
      setCellBusy(null);
    }
  }, [runCellsRead]);

  const repairCell = (profile: string) => void cellAct(profile, `repair:${profile}`, `/api/polyforge/cells/${encodeURIComponent(profile)}`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ action: "repair" }),
  }, t("polyforge.cells.repaired", { profile }));

  // A removed Cell pf still lists (state removed) keeps its pane, which
  // carries the outcome; one pf no longer lists has no pane left, so the page
  // returns to the Host with the outcome. A refusal stays in the Cell's pane.
  const removeCell = async (profile: string) => {
    setConfirmRemove(null);
    const done = t("polyforge.cells.removed", { profile });
    const { ok, read } = await cellAct(profile, `remove:${profile}`, `/api/polyforge/cells/${encodeURIComponent(profile)}`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ action: "remove" }),
    }, done);
    if (ok && !read?.cells?.some((cell) => cell.profile === profile)) {
      setCellMessage({ pane: "host", tone: "ok", text: done });
      select({ kind: "host" });
    }
  };

  // An added Cell pf lists is selected at once: the page lands on its own
  // settings with the outcome; otherwise the outcome stays in Add Cell.
  const addCell = async (posted: CellAddDraft) => {
    const done = t("polyforge.cells.added", { profile: posted.profile });
    const { ok, read } = await cellAct("add", "add", "/api/polyforge/cells", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(posted),
    }, done);
    if (ok && read?.cells?.some((cell) => cell.profile === posted.profile)) {
      setCellMessage({ pane: posted.profile, tone: "ok", text: done });
      select({ kind: "cell", profile: posted.profile });
    }
    return ok;
  };

  // A cwd change is a NEW snapshot: the history's row feedback never carries
  // over (each Cell pane drops its own drafts the same way).
  useEffect(() => {
    if (!active) return;
    setRowFeedback({});
    void runHistory();
  }, [active, cwd, runHistory]);

  useEffect(() => {
    if (!active) return;
    if (!cwd || !detect?.piSkillInstalled) {
      setSourceState({ status: "missing", source: null });
      return;
    }
    let cancelled = false;
    setSourceState({ status: "pending", source: null });
    void fetch(`/api/plugins?cwd=${encodeURIComponent(cwd)}`)
      .then(async (response) => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return (await response.json()) as { packages?: PluginPackageInfo[] };
      })
      .then((data) => {
        if (cancelled) return;
        const row = data?.packages?.find((pkg) => pkg.scope === "global" && pkg.packageName === "polyforge") ?? null;
        setSourceState({ status: row ? "found" : "missing", source: row?.source ?? null });
      })
      .catch(() => {
        if (!cancelled) setSourceState({ status: "missing", source: null });
      });
    return () => { cancelled = true; };
  }, [active, cwd, detect?.piSkillInstalled]);

  const togglePackage = useCallback(async (enabled: boolean) => {
    if (!cwd || !sourceState.source) return;
    setToggleBusy(true);
    setActionError(null);
    setActionMessage(null);
    try {
      const response = await fetch("/api/plugins", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action: enabled ? "enable" : "disable", source: sourceState.source, scope: "global", cwd }),
      });
      const data = await response.json() as { error?: string };
      if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
      setActionMessage(t("polyforge.settings.packageToggled"));
      await runDetect(false);
    } catch (error) {
      setActionError(error instanceof Error ? error.message : String(error));
    } finally {
      setToggleBusy(false);
    }
  }, [cwd, sourceState.source, runDetect, t]);

  // A rollback from the history: the same CAS write into the entry's own
  // file, its result under its entry; then every mounted Cell pane re-reads.
  const rollback = useCallback(async (entry: PolyforgeConfigHistoryEntry, feedbackId: string = entry.id) => {
    if (entry.key === "api_key" || entry.rollbackable === false) return;
    setWriteBusy(entry.id);
    setRowFeedback({});
    try {
      const message = await postRollback(entry, cwd);
      setRowFeedback({ [feedbackId]: { message: message ?? t("polyforge.settings.configRolledBack"), tone: "default" } });
      setConfigEpoch((epoch) => epoch + 1);
      await runHistory();
    } catch (error) {
      setRowFeedback({ [feedbackId]: { message: error instanceof Error ? error.message : String(error), tone: "error" } });
    } finally {
      setWriteBusy(null);
    }
  }, [cwd, runHistory, t]);

  // A Cell pane's write is a config write: the page re-reads the history and
  // the Cell registry (a mapping write reloads its Cell: its state and
  // workspaces are the registry's).
  const onCellWritten = useCallback(() => {
    void runHistory();
    void runCellsRead();
  }, [runCellsRead, runHistory]);
  // A Cell's Reload outcome is page-level status: the footer's.
  const onPageStatus = useCallback((error: string | null, message: string | null) => {
    setActionError(error);
    if (message !== null) setActionMessage(message);
  }, []);

  // 重新检测 also re-reads the install card's versions.
  const [installRefresh, setInstallRefresh] = useState(0);
  const onInstallFinished = useCallback(() => {
    void runDetect(true);
    void runCellsRead();
  }, [runDetect, runCellsRead]);

  const installCommand = t("polyforge.settings.installCommand");
  const copyInstallCommand = useCallback(async () => {
    setInstallMessage(null);
    try {
      await navigator.clipboard.writeText(installCommand);
      setInstallMessage(t("polyforge.settings.installCopied"));
    } catch {
      // Clipboard unavailable: the command text stays copyable by hand.
    }
  }, [installCommand, t]);

  const lineLabel = detect?.line === "internal"
    ? t("polyforge.settings.line.internal")
    : detect?.line === "public"
      ? t("polyforge.settings.line.public")
      : t("polyforge.settings.line.unknown");
  const switchReason = !cwd
    ? t("polyforge.settings.switchNeedsProject")
    : sourceState.status === "pending"
      ? t("polyforge.settings.switchResolving")
      : !sourceState.source
        ? t("polyforge.settings.switchNoPackage")
        : null;
  const switchReasonId = useId();
  const daemonState = !detect || detect.daemon === null
    ? t("polyforge.settings.daemonNotApplicable")
    : detect.daemon.reachable
      ? t("polyforge.settings.daemonReachable")
      : `${t("polyforge.settings.daemonUnreachable")}${detect.daemon.error ? `: ${detect.daemon.error}` : ""}`;
  // The one visible detection line: abnormal states are named
  // here as text, so a collapsed region never hides them.
  const detectSummary = !detect
    ? null
    : detect.pfPresent === false
      ? t("polyforge.settings.notInstalled")
      : `${t("polyforge.settings.configSummary", {
        version: detect.pfVersion ?? t("polyforge.settings.line.unknown"),
        line: lineLabel,
        daemon: daemonState,
      })}${detect.pfVersionBelowMinimum ? ` · ${t("polyforge.settings.belowMinimum")}` : ""}`;
  // Until detection proves pf present and new enough, the install remedy
  // stays open whatever the stored state; the forced state is never stored.
  const installForced = !detect || detect.pfPresent === false || detect.pfVersionBelowMinimum === true;
  const detectAbnormal = Boolean(detect && (
    detect.pfPresent === false || detect.pfVersionBelowMinimum || (detect.daemon !== null && !detect.daemon.reachable)
  ));

  const renderFeedback = (id: string) => <RowFeedbackLine feedback={rowFeedback[id]} />;
  // The history region locks open on a rollback error.
  const lockMarker = (ids: string[]): string | null => (ids.some((id) => rowFeedback[id]?.tone === "error") ? t("polyforge.settings.configErrorMarker") : null);
  const historyRegion = regionToggle("history", lockMarker(history.map((entry) => entry.id)));

  // the list. With the registry read the Cells are pf's own
  // rows (default first); without it (an old pf, a failed read) the list
  // still holds the default Cell, whose settings — the cli.toml card — never
  // needed the Cell protocol.
  const cells: PolyforgeCell[] | null = cellsRead?.cells ?? null;
  const cellsSupported = cellsRead?.supported === true;
  const cellOf = (profile: string): PolyforgeCell | null => cells?.find((cell) => cell.profile === profile) ?? null;
  const profiles = [POLYFORGE_DEFAULT_PROFILE, ...(cells ?? []).map((cell) => cell.profile).filter((profile) => profile !== POLYFORGE_DEFAULT_PROFILE)];
  // A selected Cell that left the registry falls back to the Host.
  const shown: PaneSelection = selection.kind === "cell" && !profiles.includes(selection.profile) ? { kind: "host" } : selection;
  const shownKey = paneKey(shown);
  const stateLabel = (state: PolyforgeCellListState) => t(`polyforge.cells.state.${state}`);
  // The Host entry's dot and badge: the machine detection, in one word.
  const hostTone: EntryTone = !detect
    ? "off"
    : detect.pfPresent === false || (detect.daemon !== null && !detect.daemon.reachable)
      ? "error"
      : detect.pfVersionBelowMinimum ? "warning" : "on";
  const hostBadge = !detect
    ? null
    : detect.pfPresent === false
      ? t("polyforge.settings.state.notInstalled")
      : detect.daemon !== null && !detect.daemon.reachable
        ? t("polyforge.cells.hostDaemonDown")
        : detect.pfVersion;

  // A lifecycle outcome, in the pane it belongs to.
  const paneMessage = (pane: string) => cellMessage?.pane === pane ? (
    <p className={cellMessage.tone === "error" ? "config-add-source-error" : "polyforge-config-card-hint"} role="status">{cellMessage.text}</p>
  ) : null;

  // One Cell's head: its name and state, AIHub, counts, pf's notice, and the
  // lifecycle — repair, and remove behind a confirmation (never for default;
  // no permanent cleanup, which stays CLI-only).
  const renderCellHead = (profile: string) => {
    const cell = cellOf(profile);
    const state = cell ? polyforgeCellListState(cell) : null;
    const notice = cell ? polyforgeCellNotice(cell) : null;
    return (
      <div className="polyforge-cell-head" data-pf-cell={profile}>
        <ConfigDetailHeader>
          <ConfigDetailHeaderInfo>
            <ConfigDetailTitle>{profile}</ConfigDetailTitle>
            {state && <span className={`polyforge-cell-badge is-${CELL_STATE_TONE[state]}`} data-pf-cell-state={state}>{stateLabel(state)}</span>}
          </ConfigDetailHeaderInfo>
          {cell && cellsSupported && (
            <ConfigDetailActions>
              <ConfigButton size="small" disabled={cellBusy !== null} onClick={() => repairCell(profile)}>{t("polyforge.cells.repair")}</ConfigButton>
              {profile !== POLYFORGE_DEFAULT_PROFILE && (confirmRemove === profile ? (
                <>
                  <ConfigButton size="small" variant="danger" disabled={cellBusy !== null} onClick={() => void removeCell(profile)}>{t("polyforge.cells.removeConfirm")}</ConfigButton>
                  <ConfigButton size="small" onClick={() => setConfirmRemove(null)}>{t("polyforge.cells.cancel")}</ConfigButton>
                </>
              ) : (
                <ConfigButton size="small" disabled={cellBusy !== null} onClick={() => setConfirmRemove(profile)}>{t("polyforge.cells.remove")}</ConfigButton>
              ))}
            </ConfigDetailActions>
          )}
        </ConfigDetailHeader>
        {cell && (
          <p className="polyforge-cell-facts">
            {cell.server && <><BreakableToken text={cell.server} />{" · "}</>}
            {t("polyforge.cells.counts", { running: String(cell.running), pending: String(cell.pending), max: String(cell.maxConcurrent ?? "-") })}
            {cell.cellMaxConcurrentJobs !== undefined && ` · ${t("polyforge.cells.cellCeiling", { value: String(cell.cellMaxConcurrentJobs) })}`}
          </p>
        )}
        {notice && <p className="polyforge-cell-notice" role="status">{notice}</p>}
        <p className="polyforge-config-card-hint">{t("polyforge.cells.cellHint")}</p>
        {profile === POLYFORGE_DEFAULT_PROFILE && (
          // pf lists no model.source for the default Cell: its credentials are the Host's.
          <p className="polyforge-config-card-hint" data-pf-model-source={profile}>{t("polyforge.cells.defaultModelSource")}</p>
        )}
        {paneMessage(profile)}
      </div>
    );
  };

  // The Host pane: the one home of the Host-level settings.
  const renderHostPane = () => (
    <>
      <ConfigDetailHeader>
        <ConfigDetailHeaderInfo>
          <ConfigDetailTitle>{t("polyforge.cells.host")}</ConfigDetailTitle>
        </ConfigDetailHeaderInfo>
      </ConfigDetailHeader>
      <p className="polyforge-config-card-hint">{t("polyforge.cells.hint")}</p>
      {paneMessage("host")}

      {detectError && (
        <p role="alert" className="config-add-source-error">{detectError}</p>
      )}

      {detect && (
        <section className="polyforge-config-card polyforge-config-status" aria-label={t("polyforge.settings.configStatusTitle")}>
          <p className="polyforge-config-card-title">{t("polyforge.settings.configStatusTitle")}</p>
          <DisclosureToggle
            open={disclosure.detect}
            controls={`${regionId}-detect`}
            onToggle={() => setRegion("detect", !disclosure.detect)}
          >
            <span className={`polyforge-config-summary${detectAbnormal ? " is-warning" : ""}`}>{detectSummary}</span>
          </DisclosureToggle>
          <div id={`${regionId}-detect`} className="polyforge-disclosure-body" hidden={!disclosure.detect}>
            <ConfigDetailGrid>
              <ConfigDetailGridRow label={t("polyforge.settings.version")} tone="plain" mono>
                {detect.pfVersion ?? t("polyforge.settings.line.unknown")}
                {detect.pfVersionBelowMinimum && (
                  <span style={{ color: "#d97706", marginLeft: 6 }}>{t("polyforge.settings.belowMinimum")}</span>
                )}
              </ConfigDetailGridRow>
              <ConfigDetailGridRow label={t("polyforge.settings.line")} tone="plain">
                {lineLabel}
              </ConfigDetailGridRow>
              <ConfigDetailGridRow label={t("polyforge.settings.piSkill")} tone="plain">
                {detect.piSkillInstalled
                  ? detect.piSkillEnabled
                    ? t("polyforge.settings.state.enabled")
                    : t("polyforge.settings.state.disabled")
                  : t("polyforge.settings.state.notInstalled")}
              </ConfigDetailGridRow>
              <ConfigDetailGridRow label={t("polyforge.settings.claudeSymlink")} tone="plain">
                {detect.claudeSkillsSymlink ? t("polyforge.settings.state.present") : t("polyforge.settings.state.absent")}
              </ConfigDetailGridRow>
              <ConfigDetailGridRow label={t("polyforge.settings.claudeHook")} tone="plain">
                {detect.claudeSessionStartHook ? t("polyforge.settings.state.present") : t("polyforge.settings.state.absent")}
              </ConfigDetailGridRow>
              <ConfigDetailGridRow label={t("polyforge.settings.daemon")} tone="plain">
                {daemonState}
              </ConfigDetailGridRow>
            </ConfigDetailGrid>
          </div>
        </section>
      )}

      {/* the Host-only keys (the Host ceiling among them)
          through the scoped writer; a pf without the multi-Cell
          capabilities shows the upgrade hint and no control. */}
      {cellsRead && (
        <div className="polyforge-config-card" data-pf-host-region>
          {cellsSupported ? (
            <HostSettingsRegion title={t("polyforge.cells.hostRegion")} />
          ) : (
            <>
              <p className="polyforge-config-card-title">{t("polyforge.cells.hostRegion")}</p>
              <p className="polyforge-config-card-hint" data-pf-cells-upgrade>{t("polyforge.cells.upgrade")}</p>
            </>
          )}
        </div>
      )}

      <section className="polyforge-config-card polyforge-config-package" aria-label={t("polyforge.settings.packageSwitch")}>
        <p className="polyforge-config-card-title">{t("polyforge.settings.packageSwitch")}</p>
        <p className="polyforge-config-card-hint">{t("polyforge.settings.configPackageHint")}</p>
        <div className="polyforge-config-switch-row">
          {detect?.piSkillInstalled && (
            <ConfigSwitch
              checked={detect.piSkillEnabled}
              disabled={!cwd || !sourceState.source}
              loading={toggleBusy}
              label={t("polyforge.settings.packageSwitch")}
              describedBy={switchReason ? switchReasonId : undefined}
              onChange={(enabled) => void togglePackage(enabled)}
            />
          )}
          {switchReason && detect?.piSkillInstalled && (
            <span id={switchReasonId} className="config-scope-switch-reason">{switchReason}</span>
          )}
        </div>
      </section>

      {/* the Jobs-tab card preferences — browser-only, so
          shown whether or not pf is detected. Exactly these two controls. */}
      <section className="polyforge-config-card polyforge-config-cards" aria-label={t("polyforge.settings.cardsTitle")}>
        <p className="polyforge-config-card-title">{t("polyforge.settings.cardsTitle")}</p>
        <p className="polyforge-config-card-hint">{t("polyforge.settings.cardsHint")}</p>
        <ConfigDetailGrid>
          <PanelPreferenceRow
            spec={POLYFORGE_TITLE_LENGTH}
            value={titleLength}
            label={t("polyforge.settings.cardsTitleLength")}
            hint={t("polyforge.settings.cardsTitleLengthHint")}
            saveLabel={t("polyforge.settings.configSave")}
          />
          <PanelPreferenceRow
            spec={POLYFORGE_POLL_INTERVAL}
            value={pollIntervalSec}
            label={t("polyforge.settings.cardsPollInterval")}
            hint={t("polyforge.settings.cardsPollIntervalHint")}
            saveLabel={t("polyforge.settings.configSave")}
          />
        </ConfigDetailGrid>
      </section>

      {/* the read-outs come after the settings, each
          collapsed by default with its count in the title.:
          the read-only declared-candidates card is gone — the workflow
          card carries those step facts. */}
      {history.length > 0 && (
        <section className="polyforge-config-card" aria-label={t("polyforge.settings.configHistory")}>
          <p className="polyforge-config-card-title">
            <DisclosureToggle
              open={historyRegion.open}
              controls={`${regionId}-history`}
              onToggle={historyRegion.onToggle}
              marker={historyRegion.marker}
            >
              {t("polyforge.settings.configHistory")}{" "}
              <span className="polyforge-config-candidate-count">{t("polyforge.settings.configHistoryCount", { count: history.length })}</span>
            </DisclosureToggle>
          </p>
          <div id={`${regionId}-history`} className="polyforge-disclosure-body polyforge-config-history" hidden={!historyRegion.open}>
            {history.map((entry) => (
              <div key={entry.id} className="polyforge-config-history-row">
                <span className="polyforge-config-history-key"><BreakableToken text={entry.key} /></span>
                <span className="polyforge-config-history-change"><BreakableToken text={`${JSON.stringify(entry.old)} → ${JSON.stringify(entry.new)}`} /></span>
                {entry.rollbackable && entry.key !== "api_key" && (
                  <ConfigButton size="small" disabled={writeBusy !== null} onClick={() => void rollback(entry)}>
                    {t("polyforge.settings.configRollback")}
                  </ConfigButton>
                )}
                {renderFeedback(entry.id)}
              </div>
            ))}
          </div>
        </section>
      )}

      {/* install or update pf from the release channel, the
          output line by line; a finished run re-detects the machine. */}
      <PolyforgeInstallCard active={active} refreshKey={installRefresh} onFinished={onInstallFinished} />

      {/* The install remedy is forced open, with no extra disclosure, until
          detection proves pf present and new enough. */}
      <section className="polyforge-config-card polyforge-config-install" aria-label={t("polyforge.settings.installCommandTitle")}>
        {/* The copy control sits on the title line, reachable without
            expanding; opening the region reveals only the command text. */}
        <div className="polyforge-config-install-head">
          {installForced ? (
            <p className="polyforge-config-card-title">{t("polyforge.settings.installCommandTitle")}</p>
          ) : (
            <p className="polyforge-config-card-title">
              <DisclosureToggle
                open={disclosure.install}
                controls={`${regionId}-install`}
                onToggle={() => setRegion("install", !disclosure.install)}
              >
                {t("polyforge.settings.installCommandTitle")}
              </DisclosureToggle>
            </p>
          )}
          <ConfigButton size="small" onClick={() => void copyInstallCommand()}>
            {t("polyforge.settings.copyInstall")}
          </ConfigButton>
          {installMessage && <span className="config-scope-switch-reason">{installMessage}</span>}
        </div>
        <div id={`${regionId}-install`} className="polyforge-disclosure-body polyforge-config-fold" hidden={!installForced && !disclosure.install}>
          <p className="polyforge-config-install-command">
            <BreakableToken text={installCommand} />
          </p>
        </div>
      </section>
    </>
  );

  // A Cell's pane (the default and every named Cell alike):
  // its head, then its own settings — the file card, the 工作区 card, Reload
  // and the workflow card, every call carrying its profile. A removed named
  // Cell has no file left to edit: its head only.
  const renderCellPane = (profile: string) => {
    const removed = profile !== POLYFORGE_DEFAULT_PROFILE && cellOf(profile)?.state === "removed";
    return (
      <>
        {renderCellHead(profile)}
        {!removed && (
          <CellSettings
            profile={profile}
            cwd={cwd}
            active={active}
            epoch={configEpoch}
            onWritten={onCellWritten}
            onPageStatus={onPageStatus}
          />
        )}
      </>
    );
  };

  // A pane mounts on its first selection and then stays mounted, hidden
  // while another entry is selected.
  const pane = (key: string, render: () => ReactNode) => visited.has(key) ? (
    <div key={key} className="polyforge-pane" data-pf-pane={key} hidden={shownKey !== key}>{render()}</div>
  ) : null;

  // The embedded shell gives the split view the section host's height: the
  // list and the detail each scroll on their own, the footer stays below.
  // On a phone the list sits above the detail, both on one screen: the
  // page opts out of Settings' one-pane drill-in (a null navigation), so a
  // row selects in place and the shared ≤640px split-view rules stack the
  // 190px list over the detail.
  return (
    <ConfigPanelShell embedded title={t("polyforge.title")} onClose={() => {}}>
      <ConfigDetailNavigationContext.Provider value={null}>
      <ConfigSplitView>
        <ConfigSidebar>
          <ConfigSidebarList>
            <div className="config-sidebar-group">
              <ConfigSidebarItem active={shownKey === "host"} data-pf-host-entry onClick={() => select({ kind: "host" })}>
                <ConfigStatusDot {...entryDot(hostTone)} />
                <ConfigSidebarText className="is-grow">{t("polyforge.cells.host")}</ConfigSidebarText>
                {hostBadge && <span className={`polyforge-cell-badge is-${hostTone}`}>{hostBadge}</span>}
              </ConfigSidebarItem>
            </div>
            <div className="config-sidebar-group" role="group" aria-label={t("polyforge.cells.cellsGroup")} data-pf-cells>
              <ConfigSidebarGroupLabel aside={<span className="config-sidebar-group-count">{profiles.length}</span>}>
                {t("polyforge.cells.cellsGroup")}
              </ConfigSidebarGroupLabel>
              {(cellsError || (cellsRead && !cellsSupported)) && (
                <ConfigSidebarGroupStatus note={cellsRead && !cellsSupported ? t("polyforge.cells.upgrade") : null} error={cellsError} />
              )}
              {profiles.map((profile) => {
                const cell = cellOf(profile);
                const state = cell ? polyforgeCellListState(cell) : null;
                const tone: EntryTone = state ? CELL_STATE_TONE[state] : "off";
                return (
                  <ConfigSidebarItem
                    key={profile}
                    active={shownKey === `cell:${profile}`}
                    data-pf-cell-entry={profile}
                    data-pf-cell-state={state ?? undefined}
                    onClick={() => select({ kind: "cell", profile })}
                  >
                    <ConfigStatusDot {...entryDot(tone)} />
                    <ConfigSidebarText className="is-grow">{profile}</ConfigSidebarText>
                    {state && <span className={`polyforge-cell-badge is-${tone}`}>{stateLabel(state)}</span>}
                  </ConfigSidebarItem>
                );
              })}
            </div>
          </ConfigSidebarList>
          <ConfigListAction active={shownKey === "add"} data-pf-add-entry onClick={() => select({ kind: "add" })}>
            {t("polyforge.cells.add")}
          </ConfigListAction>
        </ConfigSidebar>

        <ConfigDetail>
          <ConfigDetailStack className="settings-polyforge">
            {pane("host", renderHostPane)}
            {profiles.map((profile) => pane(`cell:${profile}`, () => renderCellPane(profile)))}
            {pane("add", () => (
              <AddCellPane
                read={cellsRead !== null}
                supported={cellsSupported}
                busy={cellBusy !== null}
                message={paneMessage("add")}
                onAdd={addCell}
              />
            ))}
          </ConfigDetailStack>
        </ConfigDetail>
      </ConfigSplitView>
      </ConfigDetailNavigationContext.Provider>

      <ConfigFooter
        status={
          <ConfigFooterStatus
            summary={actionError ?? actionMessage ?? (detect ? t("polyforge.settings.detected") : t("polyforge.loading"))}
            tone={actionError ? "error" : "default"}
          />
        }
      >
        {/* Re-detect refreshes the machine detection, the Cell list and the
            install card's versions. */}
        <ConfigButton size="small" disabled={!detect} onClick={() => { void runDetect(true); void runCellsRead(); setInstallRefresh((n) => n + 1); }}>
          {t("polyforge.settings.redetect")}
        </ConfigButton>
      </ConfigFooter>
    </ConfigPanelShell>
  );
}

/** The feedback id of a Cell's 工作区 add row (a mapping row's is its key). */
const WORKSPACE_ADD = "__workspace_add__";

/** One pending connection-key write, held for its confirmation. */
type PendingWrite = { item: PolyforgeConfigItem; action: "set" | "unset"; value?: unknown };

/**
 * one Cell's settings — the default Cell's and every named
 * Cell's alike. The file card of the Cell's own file (`cli.toml` for default,
 * `cli-<profile>.toml` for a named Cell: its zones, common keys and advanced
 * folds, per-row save and unset with inline feedback), the connection-key
 * confirmation, the editable 工作区 card, Reload and the workflow card. Every
 * read, write and reload carries `profile`, which the routes pin to
 * `--profile <name>`. The Host-only keys never render here
 * (their one home is the Host pane); what pf lists differently per Cell
 * (a named Cell's [model] source) is its only content difference.
 */
function CellSettings({ profile, cwd, active, epoch, onWritten, onPageStatus }: {
  profile: string;
  cwd: string | null;
  active: boolean;
  /** Bumped by the page after a history rollback: re-read the file. */
  epoch: number;
  /** A write landed (or was refused after pf touched the file): the page re-reads its history and the registry. */
  onWritten: () => void;
  /** Reload's outcome, page-level status (the footer's). */
  onPageStatus: (error: string | null, message: string | null) => void;
}) {
  const { t } = useI18n();
  const file = polyforgeCellFileName(profile);
  const named = profile !== POLYFORGE_DEFAULT_PROFILE;
  // per-key save/validation results keyed by item.key (a
  // mapping row's by its section key, the add row's by WORKSPACE_ADD), a
  // notice rollback's by the key, rendered under that row.
  const [rowFeedback, setRowFeedback] = useState<Record<string, RowFeedback>>({});
  const [config, setConfig] = useState<PolyforgeConfigRead | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [modelEdits, setModelEdits] = useState<Record<string, string[]>>({});
  const [modelAdds, setModelAdds] = useState<Record<string, string>>({});
  // the 工作区 add row's two inputs.
  const [addPath, setAddPath] = useState("");
  const [addProjects, setAddProjects] = useState("");
  const [writeBusy, setWriteBusy] = useState<string | null>(null);
  // (review s2#4): bumped after a 工作区 mapping write, so the
  // workflow card re-reads the Cell's (now different) workspaces.
  const [workflowEpoch, setWorkflowEpoch] = useState(0);
  // a connection-key save (or unset) waits here for its
  // second confirmation (nothing is posted until then), and a saved
  // connection key keeps its history entry for the row's notice and
  // one-click rollback.
  const [confirming, setConfirming] = useState<PendingWrite | null>(null);
  const [notices, setNotices] = useState<Record<string, PolyforgeConfigHistoryEntry>>({});
  // each region's open/closed state, this browser only.
  const { disclosure, setRegion } = usePolyforgeDisclosure();
  const regionToggle = regionToggles(disclosure, setRegion);
  const regionId = useId();
  const reloadClassLabel = useCallback((reloadClass: string) => reloadClassLabelOf(t, reloadClass), [t]);

  const runConfigRead = useCallback(async () => {
    try {
      const response = await fetch(`/api/polyforge/config?cwd=${encodeURIComponent(cwd ?? "")}&profile=${encodeURIComponent(profile)}`);
      const data = await response.json() as PolyforgeConfigRead & { error?: string };
      if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
      setConfig(data);
      setConfigError(null);
    } catch (error) {
      setConfigError(error instanceof Error ? error.message : String(error));
    }
  }, [cwd, profile]);

  // A cwd change is a NEW snapshot: drafts and row feedback never carry over
  // from another cwd's read.
  useEffect(() => {
    if (!active) return;
    setDrafts({});
    setModelEdits({});
    setModelAdds({});
    setRowFeedback({});
    setConfirming(null);
    setNotices({});
    void runConfigRead();
  }, [active, cwd, runConfigRead]);

  // A history rollback on the Host pane wrote some Cell's file: re-read.
  const seenEpoch = useRef(epoch);
  useEffect(() => {
    if (seenEpoch.current === epoch) return;
    seenEpoch.current = epoch;
    void runConfigRead();
  }, [epoch, runConfigRead]);

  // The one POST of a Cell-file key (summarize included: no
  // workspace; a 工作区 mapping section the same way): pf's
  // outcome lands under the row, then a fresh read (the draft is dropped once
  // the read shows the value). A refusal re-reads too — pf may have written
  // the file before the Host refused it, so the CAS hash must be fresh. Never
  // a reload, shutdown or ensure: Reload stays its own control.
  const commit = useCallback(async (key: string, action: "set" | "unset", value: unknown, feedbackId: string = key) => {
    if (!config) return false;
    setWriteBusy(key);
    setRowFeedback({});
    try {
      const expectState = { file_sha256: config.file?.sha256 ?? null, values: expectedValues(config.items) };
      const response = await fetch("/api/polyforge/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          action,
          key,
          ...(action === "set" ? { value } : {}),
          cwd: cwd ?? "",
          profile,
          expect_state: expectState,
        }),
      });
      const data = await response.json() as { error?: string; message?: string; diff?: unknown; entry?: PolyforgeConfigHistoryEntry | null };
      // the summarize floor refusal (400 naming the floor) is
      // shown in the page's language, with the version the read found.
      if (response.status === 400 && key.startsWith("summarize.") && data.error?.includes(POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION)) {
        throw new Error(t("polyforge.settings.summarizeWriteRefused", {
          required: POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION,
          found: config.pfVersion ?? t("polyforge.settings.versionUnparseable"),
        }));
      }
      if (!response.ok || data.error) {
        throw new Error(data.diff ? `${data.error ?? "refused"} (${JSON.stringify(data.diff)})` : data.error ?? `HTTP ${response.status}`);
      }
      setRowFeedback({ [feedbackId]: { message: data.message ?? t("polyforge.settings.configSaved"), tone: "default" } });
      const entry = data.entry;
      if (CONNECTION_KEYS.includes(key) && entry) setNotices((current) => ({ ...current, [key]: entry }));
      await runConfigRead();
      onWritten();
      if (parseWorkspaceWriteKey(key) !== null) setWorkflowEpoch((epoch) => epoch + 1);
      setDrafts((current) => { const next = { ...current }; delete next[key]; return next; });
      setModelEdits((current) => { const next = { ...current }; delete next[key]; return next; });
      setModelAdds((current) => { const next = { ...current }; delete next[key]; return next; });
      return true;
    } catch (error) {
      setRowFeedback({ [feedbackId]: { message: error instanceof Error ? error.message : String(error), tone: "error" } });
      await runConfigRead();
      onWritten();
      // a refused mapping may still have landed in the file (pf writes, then the Host refuses)
      if (parseWorkspaceWriteKey(key) !== null) setWorkflowEpoch((epoch) => epoch + 1);
      return false;
    } finally {
      setWriteBusy(null);
    }
  }, [config, cwd, onWritten, profile, runConfigRead, t]);

  const save = useCallback(async (item: PolyforgeConfigItem) => {
    if (!config) return;
    const raw = isModelListKey(item)
      ? [
          ...(modelEdits[item.key] ?? modelListValue(item.value)),
          ...((modelAdds[item.key] ?? "").trim() ? [(modelAdds[item.key] ?? "").trim()] : []),
        ].filter((model, index, all) => all.indexOf(model) === index).join(",")
      : drafts[item.key] ?? String(itemValue(item));
    const value = formValue(item.key, raw);
    // pf's schema is enforced HERE so the form cannot submit 0 or 9; the
    // route still hands the value to pf, which stays the authority.
    const invalid = configValueError(item.key, value, item.schema);
    if (invalid) {
      setRowFeedback({ [item.key]: { message: invalid, tone: "error" } });
      return;
    }
    // a connection key posts only after its confirmation.
    if (CONNECTION_KEYS.includes(item.key)) {
      setRowFeedback({});
      setConfirming({ item, action: "set", value });
      return;
    }
    await commit(item.key, "set", value);
  }, [commit, config, drafts, modelAdds, modelEdits]);

  // Unset a key the file declares (its default applies
  // again); a connection key asks first, like its save.
  const unsetKey = useCallback(async (item: PolyforgeConfigItem) => {
    if (CONNECTION_KEYS.includes(item.key)) {
      setRowFeedback({});
      setConfirming({ item, action: "unset" });
      return;
    }
    await commit(item.key, "unset", undefined);
  }, [commit]);

  // The api_key never stays in the form: a confirmation's outcome or its
  // cancel clears the draft.
  const dropKeyDraft = (key: string) => {
    if (key === "api_key") setDrafts((current) => { const next = { ...current }; delete next.api_key; return next; });
  };
  // Confirm: exactly one write. Cancel: no write, the draft stays (never the key).
  const confirmSave = useCallback(async () => {
    if (!confirming) return;
    setConfirming(null);
    dropKeyDraft(confirming.item.key);
    await commit(confirming.item.key, confirming.action, confirming.value);
  }, [commit, confirming]);
  const cancelConfirm = () => {
    if (confirming) dropKeyDraft(confirming.item.key);
    setConfirming(null);
  };

  // one 工作区 mapping row of this Cell's file — add (the add
  // row), edit its projects, or remove it — through the same writer.
  const writeMapping = async (path: string, action: "set" | "unset", projectsText: string, feedbackId: string) => {
    const key = workspaceWriteKey(path.trim());
    if (parseWorkspaceWriteKey(key) === null) {
      setRowFeedback({ [feedbackId]: { message: t("polyforge.settings.workspacePathInvalid"), tone: "error" } });
      return;
    }
    const projects = action === "set" ? workspaceProjectsValue(projectsText) : null;
    if (action === "set" && !projects) {
      setRowFeedback({ [feedbackId]: { message: t("polyforge.settings.workspaceProjectsInvalid"), tone: "error" } });
      return;
    }
    const ok = await commit(key, action, projects ?? undefined, feedbackId);
    if (ok && feedbackId === WORKSPACE_ADD) {
      setAddPath("");
      setAddProjects("");
    }
  };

  const reload = useCallback(async () => {
    setWriteBusy("__reload__");
    onPageStatus(null, null);
    try {
      const response = await fetch("/api/polyforge/config/reload", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ cwd: cwd ?? "", profile }),
      });
      const data = await response.json() as { error?: string };
      if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
      onPageStatus(null, t("polyforge.settings.configReloaded"));
      await runConfigRead();
    } catch (error) {
      onPageStatus(error instanceof Error ? error.message : String(error), null);
    } finally {
      setWriteBusy(null);
    }
  }, [cwd, onPageStatus, profile, runConfigRead, t]);

  // A rollback from a saved connection row's notice (feedback under that
  // row): the history's same CAS write into this Cell's file.
  const rollback = useCallback(async (entry: PolyforgeConfigHistoryEntry, feedbackId: string) => {
    if (entry.key === "api_key" || entry.rollbackable === false) return;
    setWriteBusy(entry.id);
    setRowFeedback({});
    try {
      const message = await postRollback(entry, cwd);
      setRowFeedback({ [feedbackId]: { message: message ?? t("polyforge.settings.configRolledBack"), tone: "default" } });
      setNotices((current) => { const next = { ...current }; delete next[entry.key]; return next; });
      await runConfigRead();
      onWritten();
      setDrafts((current) => { const next = { ...current }; delete next[entry.key]; return next; });
    } catch (error) {
      setRowFeedback({ [feedbackId]: { message: error instanceof Error ? error.message : String(error), tone: "error" } });
    } finally {
      setWriteBusy(null);
    }
  }, [cwd, onWritten, runConfigRead, t]);

  // A workflow-card write is a config write: the pane re-reads its file and
  // the page its history (the card re-reads its own facts).
  const onWorkflowWritten = useCallback(() => {
    void runConfigRead();
    onWritten();
  }, [onWritten, runConfigRead]);

  const unset = t("polyforge.settings.configUnset");
  // (review s2#1): no "not mapped … read-only" line — nothing in
  // a Cell's pane depends on whether the settings cwd lies in that Cell's
  // mapping (a named Cell's never does), and its 工作区 card is editable.
  const configReason = !cwd
    ? t("polyforge.settings.configNeedsProject")
    : config?.capable === false
      ? config.reason ?? t("polyforge.settings.configUnavailable")
      : null;

  const addModel = (item: PolyforgeConfigItem) => {
    const addition = (modelAdds[item.key] ?? "").trim();
    if (!addition) return;
    setModelEdits((current) => {
      const base = current[item.key] ?? modelListValue(item.value);
      return { ...current, [item.key]: base.includes(addition) ? base : [...base, addition] };
    });
    setModelAdds((current) => ({ ...current, [item.key]: "" }));
  };

  const removeModel = (item: PolyforgeConfigItem, model: string) => {
    setModelEdits((current) => ({
      ...current,
      [item.key]: (current[item.key] ?? modelListValue(item.value)).filter((entry) => entry !== model),
    }));
  };

  // a row the file declares offers Unset (api_key: when a key is set).
  const renderUnset = (item: PolyforgeConfigItem) => {
    const declared = item.key === "api_key" ? item.set === true : item.source === "file";
    if (!declared) return null;
    return (
      <ConfigButton size="small" disabled={writeBusy !== null || confirming !== null} onClick={() => void unsetKey(item)}>
        {t("polyforge.cells.unset")}
      </ConfigButton>
    );
  };

  const renderValue = (item: PolyforgeConfigItem, writable: boolean) => {
    if (item.section) {
      const workspacePath = parseWorkspaceWriteKey(item.key);
      // a mapping row edits its projects and can be removed;
      // only a header the panel cannot write back stays read-only chips.
      if (workspacePath !== null) {
        return (
          <span className="polyforge-config-write">
            <input
              type="text"
              aria-label={`${t("polyforge.settings.workspaceProjects")} · ${workspacePath}`}
              value={drafts[item.key] ?? workspaceProjects(item.value).join(", ")}
              onChange={(event) => setDrafts((current) => ({ ...current, [item.key]: event.target.value }))}
            />
            <ConfigButton size="small" disabled={writeBusy !== null} onClick={() => void writeMapping(workspacePath, "set", drafts[item.key] ?? workspaceProjects(item.value).join(","), item.key)}>
              {t("polyforge.settings.configSave")}
            </ConfigButton>
            <ConfigButton size="small" disabled={writeBusy !== null} onClick={() => void writeMapping(workspacePath, "unset", "", item.key)}>
              {t("polyforge.settings.workspaceRemove")}
            </ConfigButton>
          </span>
        );
      }
      const projects = workspaceProjects(item.value);
      return projects.length > 0 ? (
        <span className="polyforge-config-chips" aria-label={t("polyforge.settings.configProjects")}>
          {projects.map((project) => (
            <span key={project} className="polyforge-config-chip"><BreakableToken text={project} /></span>
          ))}
        </span>
      ) : <span className="polyforge-config-empty">{unset}</span>;
    }

    if (isModelListKey(item)) {
      const models = modelEdits[item.key] ?? modelListValue(item.value);
      const chips = models.length > 0 ? (
        <span className="polyforge-config-chips" aria-label={t("polyforge.settings.configModels")}>
          {models.map((model) => (
            <span key={model} className="polyforge-config-chip">
              <span className="polyforge-config-chip-label"><BreakableToken text={model} /></span>
              {writable && (
                <button
                  type="button"
                  className="polyforge-config-chip-remove"
                  aria-label={`${t("polyforge.settings.configRemoveModel")} ${model}`}
                  onClick={() => removeModel(item, model)}
                >×</button>
              )}
            </span>
          ))}
        </span>
      ) : <span className="polyforge-config-empty">{unset}</span>;
      if (!writable) return chips;
      return (
        <span className="polyforge-config-write is-multiline">
          {chips}
          <span className="polyforge-config-write-row">
            <input
              type="text"
              aria-label={item.key}
              placeholder={t("polyforge.settings.configAddModel")}
              value={modelAdds[item.key] ?? ""}
              onChange={(event) => setModelAdds((current) => ({ ...current, [item.key]: event.target.value }))}
              onKeyDown={(event) => {
                if (event.key === "Enter") { event.preventDefault(); addModel(item); }
              }}
            />
            <ConfigButton size="small" disabled={writeBusy !== null} onClick={() => void save(item)}>
              {t("polyforge.settings.configSave")}
            </ConfigButton>
            {renderUnset(item)}
          </span>
        </span>
      );
    }

    if (writable) {
      return (
        <span className="polyforge-config-write">
          <input
            type={item.key === "api_key" ? "password" : isNumericKey(item.key) ? "number" : "text"}
            aria-label={item.key}
            autoComplete={item.key === "api_key" ? "off" : undefined}
            placeholder={item.key === "api_key" ? t("polyforge.settings.configApiKeyWriteOnly") : undefined}
            value={drafts[item.key] ?? String(itemValue(item))}
            min={item.schema?.min}
            max={item.schema?.max}
            step={item.schema?.kind === "int" ? 1 : undefined}
            onChange={(event) => setDrafts((current) => ({ ...current, [item.key]: event.target.value }))}
          />
          <ConfigButton size="small" disabled={writeBusy !== null} onClick={() => void save(item)}>
            {t("polyforge.settings.configSave")}
          </ConfigButton>
          {renderUnset(item)}
          {item.key === "api_key" && (
            // write-only: whether a key is set, never its value
            <span className="config-scope-switch-reason" data-pf-api-key-state={item.set ? "set" : "unset"}>
              {item.set ? t("polyforge.cells.apiKeyIsSet") : t("polyforge.cells.apiKeyNotSet")}
            </span>
          )}
        </span>
      );
    }

    if (item.key !== "api_key" && isUnsetValue(item.value)) {
      return <span className="polyforge-config-empty">{unset}</span>;
    }
    const text = item.key === "api_key" ? (item.set ? "<set>" : "<unset>") : JSON.stringify(item.value ?? null);
    return (
      <span className="polyforge-config-value">
        <BreakableToken text={text} />
      </span>
    );
  };

  const renderFeedback = (id: string) => <RowFeedbackLine feedback={rowFeedback[id]} />;

  // a saved connection key's inline notice — the running
  // daemon keeps the old value until a restart — with a one-click rollback
  // (never for api_key: its history is set/unset only).
  const renderNotice = (key: string) => {
    const entry = notices[key];
    if (!entry) return null;
    return (
      <div className="polyforge-config-row-notice" role="status">
        <span>{t("polyforge.settings.configConnectionNotice")}</span>
        {entry.rollbackable && entry.key !== "api_key" && (
          <ConfigButton size="small" disabled={writeBusy !== null} onClick={() => void rollback(entry, entry.key)}>
            {t("polyforge.settings.configRollback")}
          </ConfigButton>
        )}
      </div>
    );
  };

  // A region holding an unsaved draft or a row error stays open and says so
  //; the lock is runtime state, never written to storage.
  const lockMarker = (ids: string[]): string | null => {
    if (ids.some((id) => rowFeedback[id]?.tone === "error")) return t("polyforge.settings.configErrorMarker");
    if (ids.some((id) => drafts[id] !== undefined || modelEdits[id] !== undefined || Boolean(modelAdds[id]?.trim()) || confirming?.item.key === id)) {
      return t("polyforge.settings.configDraftMarker");
    }
    return null;
  };

  // the Host-only keys have one surface, the Host region, so
  // no Host key renders in a Cell's pane.
  const cards = config?.capable ? configCards(groupConfigItems((config.items ?? []).filter((item) => !HOST_ONLY_KEYS.includes(item.key)))) : null;
  // the read-only memory_policy blocks of `pf config list`,
  // one group per mapped workspace path (sorted; pf builds them from a map).
  const memoryPolicyPaths = config?.capable && config.memoryPolicy
    ? Object.entries(config.memoryPolicy).sort(([a], [b]) => a.localeCompare(b))
    : [];
  // A memory_policy row: read-only, no write control, its class badge.
  const renderMemoryRow = (name: string, entry: PolyforgeMemoryPolicyEntry) => (
    <div key={name} className="polyforge-config-row is-readonly">
      <span className="polyforge-config-key">
        <span className="polyforge-config-key-name"><BreakableToken text={`memory_policy.${name}`} /></span>
      </span>
      <span className="polyforge-config-class">
        <span className="polyforge-config-badge" title="next-claim">{reloadClassLabel("next-claim")}</span>
      </span>
      <span className="polyforge-config-file">
        {entry.record.length === 0 && entry.skip.length === 0
          ? <span className="polyforge-config-empty">{unset}</span>
          : (["record", "skip"] as const).map((list) => entry[list].length > 0 && (
            <span key={list} className="polyforge-config-chips" aria-label={list}>
              <span className="polyforge-config-empty">{list}:</span>
              {entry[list].map((value, index) => <span key={`${index}:${value}`} className="polyforge-config-chip"><BreakableToken text={value} /></span>)}
            </span>
          ))}
      </span>
    </div>
  );
  const classesOf = (items: { class?: string }[]): string[] => [...new Set(items.map((item) => item.class)
    .filter((value): value is string => Boolean(value)))];
  const legendOf = (classes: string[]) => classes.length > 0 && (
    <p className="polyforge-config-legend">
      <span>{t("polyforge.settings.configLegend")}</span>
      {classes.map((reloadClass) => (
        <span key={reloadClass} className="polyforge-config-badge" title={reloadClass}>
          {reloadClassLabel(reloadClass)}
        </span>
      ))}
    </p>
  );
  // A zone/card declares only its effective columns (#126): the daemon
  // column header exists in the [daemon] zone alone.
  const gridHeader = (hasDaemon: boolean) => (
    <div className="polyforge-config-grid-header" aria-hidden="true">
      <span>{t("polyforge.settings.configColKey")}</span>
      <span>{t("polyforge.settings.configColClass")}</span>
      <span>{t("polyforge.settings.configColFile")}</span>
      {hasDaemon && <span>{t("polyforge.settings.configColDaemon")}</span>}
    </div>
  );
  const renderRow = (item: PolyforgeConfigItem, zone: ConfigRowZone) => {
    const hasDaemon = zone === "daemon";
    const isWorkspace = zone === "workspace";
    const writable = WRITABLE.has(item.key) && !item.section;
    const workspacePath = item.section ? workspaceSectionPath(item.key) : null;
    // a mapping row the panel can write back is editable too
    const editable = writable || (item.section === true && parseWorkspaceWriteKey(item.key) !== null);
    const daemonCell = hasDaemon && isDaemonColumnKey(item.key);
    return (
      <div key={item.key} className={`polyforge-config-row${editable ? "" : " is-readonly"}${daemonCell ? "" : hasDaemon ? " no-daemon" : ""}`} data-pf-workspace-row={workspacePath ?? undefined}>
        <span className="polyforge-config-key">
          {workspacePath !== null
            ? <span className="polyforge-config-workspace-path"><BreakableToken text={shortenPath(workspacePath)} /></span>
            : isWorkspace
              ? <span className="polyforge-config-key-name"><BreakableToken text={item.key} /></span>
              : <BreakableToken text={item.key} />}
          {isWorkspace
            ? <span className="polyforge-config-file-tag" title={t("polyforge.settings.workspaceSourceFile")}>{file}</span>
            : null}
        </span>
        <span className="polyforge-config-class">
          {item.class
            ? <span className="polyforge-config-badge" title={item.class}>{reloadClassLabel(item.class)}</span>
            : null}
        </span>
        <span className="polyforge-config-file">{renderValue(item, writable)}</span>
        {daemonCell && (
          <span className="polyforge-config-daemon">
            {config?.daemonReachable === false
              ? t("polyforge.settings.configDaemonDown")
              : item.daemon
                ? <>{t("polyforge.settings.configDaemonValue")}: <BreakableToken text={`${String(item.daemon.value)}${item.stale_reason ? ` (${item.stale_reason})` : ""}`} /></>
                : <span className="polyforge-config-empty">{t("polyforge.settings.configUnset")}</span>}
          </span>
        )}
        {IDENTITY_KEYS.includes(item.key) && (
          // an identity key is never edited in place
          <span className="polyforge-config-card-hint" data-pf-identity-hint={item.key}>{t("polyforge.cells.identityNewProfile")}</span>
        )}
        {renderFeedback(item.key)}
        {renderNotice(item.key)}
      </div>
    );
  };

  const machineScope = t("polyforge.settings.configMachineScope", { file });
  return (
    <>
      <div className="polyforge-config-panel">
        <ConfigSectionTitle>{t("polyforge.settings.configTitle")}</ConfigSectionTitle>
        {/* its own class — one ellipsised line with the full text in the title at desktop, whole and balanced on phones. */}
        <p className="config-machine-scope-hint" title={machineScope}>{machineScope}</p>
        {configError && <p role="alert" className="config-add-source-error">{configError}</p>}
        {configReason && <p className="config-scope-switch-reason">{configReason}</p>}
        {config && (
          <>
            {cards && cards.cli.length > 0 && (
              <section className="polyforge-config-card polyforge-config-cli" aria-label={file}>
                <p className="polyforge-config-card-title">{file}</p>
                <p className="polyforge-config-card-hint">{t("polyforge.settings.configGroupFlatHint", { file })}</p>
                {legendOf(classesOf(cards.cli.flatMap((zone) => zone.items)))}
                {cards.cli.map((zone) => {
                  // each file zone shows its common keys and
                  // folds the rest (the advanced keys, plus any key pf adds
                  // later, read-only) behind one counted control; the daemon
                  // column lives only in the [daemon] zone (the #126 ruling).
                  const hasDaemon = zone.id === "daemon";
                  const region = ZONE_REGION[zone.id];
                  // the Cell ceiling is one of a Cell's own
                  // visible settings (the Host ceiling moved to the Host
                  // pane); so is a named Cell's model.source.
                  const common = zone.items.filter((item) => (CLI_COMMON_KEYS.includes(item.key) || SUMMARIZE_KEYS.includes(item.key) || CELL_KEYS.includes(item.key)) && !item.section);
                  const advanced = zone.items.filter((item) => !common.includes(item));
                  const fold = regionToggle(region, lockMarker(advanced.map((item) => item.key)));
                  return (
                    <div key={zone.id} className={`polyforge-config-zone is-${zone.id}`}>
                      <p className="polyforge-config-zone-title">{`${file}${ZONE_SECTION[zone.id]}`}</p>
                      {hasDaemon && <p className="polyforge-config-card-hint">{t("polyforge.settings.configGroupDaemonHint")}</p>}
                      <div className={`polyforge-config-grid${hasDaemon ? " has-daemon" : ""}`}>
                        {gridHeader(hasDaemon)}
                        {common.map((item) => renderRow(item, zone.id))}
                      </div>
                      {advanced.length > 0 && (
                        <>
                          <DisclosureToggle
                            open={fold.open}
                            controls={`${regionId}-${region}`}
                            onToggle={fold.onToggle}
                            marker={fold.marker}
                          >
                            {t("polyforge.settings.configAdvancedCount", { count: advanced.length })}
                          </DisclosureToggle>
                          <div id={`${regionId}-${region}`} className="polyforge-disclosure-body polyforge-config-fold" hidden={!fold.open}>
                            <div className={`polyforge-config-grid${hasDaemon ? " has-daemon" : ""}`}>
                              {advanced.map((item) => renderRow(item, zone.id))}
                            </div>
                          </div>
                        </>
                      )}
                    </div>
                  );
                })}
                {/* below the summarize floor the [summarize]
                    zone and the [memory_policy] blocks are hidden (the read
                    drops them) and this one localized hint stands in. */}
                {config.summarizeReason && (
                  <p className="config-scope-switch-reason">
                    {t("polyforge.settings.summarizeFloorHint", {
                      required: POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION,
                      found: config.pfVersion ?? t("polyforge.settings.versionUnparseable"),
                    })}
                  </p>
                )}
                {memoryPolicyPaths.length > 0 && (
                  <div className="polyforge-config-zone is-memory-policy">
                    <p className="polyforge-config-zone-title">{`${file} [memory_policy]`}</p>
                    <p className="polyforge-config-subtitle">{t("polyforge.settings.memoryPolicyTitle")}</p>
                    <p className="config-scope-switch-reason">{t("polyforge.settings.memoryPolicyAppendNote")}</p>
                    {memoryPolicyPaths.map(([path, entries]) => (
                      <div key={path} className="polyforge-config-policy-path">
                        <p className="polyforge-config-policy-path-title" title={path}>
                          {t("polyforge.settings.memoryPolicyPath", { path: shortenPath(path) })}
                        </p>
                        <div className="polyforge-config-grid">
                          {policyEntryNames(entries).map((name) => renderMemoryRow(name, entries[name]))}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </section>
            )}
            {/* the connection-key confirmation renders outside
                every folded region, so a collapsed zone never hides it. */}
            {confirming && (
              <div role="alertdialog" aria-labelledby={`${regionId}-confirm`} className="polyforge-config-confirm" data-pf-confirm={confirming.item.key}>
                <p id={`${regionId}-confirm`} className="polyforge-config-confirm-text">
                  {named && confirming.item.key === "api_key" && confirming.action === "unset"
                    // a named Cell needs its key: its file would turn invalid
                    ? t("polyforge.cells.confirmApiKeyUnset")
                    : t("polyforge.settings.configConfirmConnection", { key: confirming.item.key })}
                </p>
                <div className="polyforge-config-confirm-actions">
                  <ConfigButton size="small" variant="primary" disabled={writeBusy !== null} onClick={() => void confirmSave()}>
                    {t("polyforge.settings.configConfirmSave")}
                  </ConfigButton>
                  <ConfigButton size="small" onClick={cancelConfirm}>
                    {t("polyforge.settings.configConfirmCancel")}
                  </ConfigButton>
                </div>
              </div>
            )}
            {cards && (() => {
              // The 工作区 card holds the file's
              // [workspace] mapping rows only, folded under its counted
              // disclosure; each row edits its projects or is
              // removed, and the add row below the fold maps a new path.
              const items = cards.workspace;
              const fold = regionToggle("workspace", lockMarker(items.map((item) => item.key)));
              // The legend and the column header sit above the fold, so
              // collapsing the card hides rows only.
              return (
                <section className="polyforge-config-card" aria-label={t("polyforge.settings.workspaceTitle")} data-pf-cell-workspaces={profile}>
                  <p className="polyforge-config-card-title">{t("polyforge.settings.workspaceTitle")}</p>
                  <p className="polyforge-config-card-hint">{t("polyforge.settings.configGroupWorkspaceHint", { file })}</p>
                  {legendOf(classesOf(items))}
                  <div className="polyforge-config-grid">
                    {gridHeader(false)}
                  </div>
                  {items.length > 0 ? (
                    <>
                      <DisclosureToggle
                        open={fold.open}
                        controls={`${regionId}-workspace`}
                        onToggle={fold.onToggle}
                        marker={fold.marker}
                      >
                        {t("polyforge.settings.configRowCount", { count: items.length })}
                      </DisclosureToggle>
                      <div id={`${regionId}-workspace`} className="polyforge-disclosure-body polyforge-config-fold" hidden={!fold.open}>
                        <div className="polyforge-config-grid">
                          {items.map((item) => renderRow(item, "workspace"))}
                        </div>
                      </div>
                    </>
                  ) : (
                    <p className="config-scope-switch-reason" data-pf-cell-no-workspace>{t("polyforge.cells.noWorkspace")}</p>
                  )}
                  <div className="polyforge-config-workspace-add" data-pf-workspace-add>
                    <input
                      type="text"
                      aria-label={t("polyforge.settings.workspacePath")}
                      placeholder="/abs/workspace/path"
                      value={addPath}
                      onChange={(event) => setAddPath(event.target.value)}
                    />
                    <input
                      type="text"
                      aria-label={t("polyforge.settings.workspaceProjects")}
                      placeholder="project-a, project-b"
                      value={addProjects}
                      onChange={(event) => setAddProjects(event.target.value)}
                    />
                    <ConfigButton size="small" disabled={writeBusy !== null} onClick={() => void writeMapping(addPath, "set", addProjects, WORKSPACE_ADD)}>
                      {t("polyforge.settings.workspaceAdd")}
                    </ConfigButton>
                    {renderFeedback(WORKSPACE_ADD)}
                  </div>
                </section>
              );
            })()}
            <div>
              <ConfigButton size="small" disabled={writeBusy !== null || !config.capable} onClick={() => void reload()}>
                {t("polyforge.settings.configReload")}
              </ConfigButton>
            </div>
          </>
        )}
      </div>

      {/* the workflow card, right after the configuration
          cards: the step chains per project of this Cell's mapped
          workspaces. */}
      <WorkflowCard
        cwd={cwd}
        profile={profile}
        active={active}
        refreshKey={workflowEpoch}
        regionId={regionId}
        reloadClassLabel={reloadClassLabel}
        onWritten={onWorkflowWritten}
      />
    </>
  );
}

/**
 * the Host's layered settings region — its Host-only keys,
 * read through the scoped config read (`--host`, never the cwd's Cell) and
 * written through the existing scoped writer (`POST /api/polyforge/config`,
 * pf's CAS on cli.toml's sha256, the same validation and history). A
 * connection key (the Host's daemon_socket) is written only after the same
 * confirmation as a Cell's file card, and a saved one shows the notice with
 * its one-click rollback. Each key has one surface: the Host-only keys live
 * here only (a Cell's file card drops them). every Cell's
 * keys are its file card, so the Host is the one region left.
 */
/** One pending region write (held for a connection key's confirmation). */
type RegionWrite = { action: "set" | "unset"; key: string; value?: unknown };

/** One region row: key, value input and actions, wrapping on a narrow sheet. */
const REGION_ROW_STYLE = { display: "flex", flexWrap: "wrap", alignItems: "center", gap: 6, minWidth: 0 } as const;

function HostSettingsRegion({ title }: { title: string }) {
  const { t } = useI18n();
  const [read, setRead] = useState<PolyforgeConfigRead | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [message, setMessage] = useState<{ tone: "ok" | "error"; text: string } | null>(null);
  ///#348: a connection-key write (daemon_socket) waits here for
  // its confirmation — nothing is posted until then — and a saved one keeps
  // its history entry for the row's notice and one-click rollback.
  const [confirming, setConfirming] = useState<RegionWrite | null>(null);
  const [notices, setNotices] = useState<Record<string, PolyforgeConfigHistoryEntry>>({});

  const runRead = useCallback(async () => {
    try {
      const response = await fetch(HOST_REGION_READ_URL);
      const body = await response.json() as PolyforgeConfigRead & { error?: string };
      if (!response.ok || body.error) throw new Error(body.error ?? `HTTP ${response.status}`);
      setRead(body);
    } catch (error) {
      setMessage({ tone: "error", text: error instanceof Error ? error.message : String(error) });
    }
  }, []);

  useEffect(() => { void runRead(); }, [runRead]);

  const post = async ({ action, key, value }: RegionWrite) => {
    setBusy(key);
    setMessage(null);
    try {
      const response = await fetch("/api/polyforge/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(hostRegionWriteBody(action, key, value, read?.file?.sha256 ?? null)),
      });
      const body = await response.json() as { error?: string; message?: string; entry?: PolyforgeConfigHistoryEntry | null };
      if (!response.ok || body.error) throw new Error(body.error ?? `HTTP ${response.status}`);
      setMessage({ tone: "ok", text: body.message ?? t("polyforge.settings.configSaved") });
      const entry = body.entry;
      if (CONNECTION_KEYS.includes(key) && entry) setNotices((current) => ({ ...current, [key]: entry }));
      setDrafts((current) => { const next = { ...current }; delete next[key]; return next; });
      await runRead();
    } catch (error) {
      setMessage({ tone: "error", text: error instanceof Error ? error.message : String(error) });
    } finally {
      setBusy(null);
    }
  };
  // Every region write goes through here: a connection key asks first.
  const write = (pending: RegionWrite) => {
    if (CONNECTION_KEYS.includes(pending.key)) {
      setMessage(null);
      setConfirming(pending);
      return;
    }
    void post(pending);
  };
  const confirmWrite = () => {
    if (!confirming) return;
    const pending = confirming;
    setConfirming(null);
    void post(pending);
  };
  // The same rollback as a Cell's file card: the history route writes the
  // entry's own file (its scope) back with a fresh CAS read.
  const rollback = async (entry: PolyforgeConfigHistoryEntry) => {
    if (entry.key === "api_key" || entry.rollbackable === false) return;
    setBusy(entry.id);
    setMessage(null);
    try {
      const text = await postRollback(entry, "");
      setMessage({ tone: "ok", text: text ?? t("polyforge.settings.configRolledBack") });
      setNotices((current) => { const next = { ...current }; delete next[entry.key]; return next; });
      setDrafts((current) => { const next = { ...current }; delete next[entry.key]; return next; });
      await runRead();
    } catch (error) {
      setMessage({ tone: "error", text: error instanceof Error ? error.message : String(error) });
    } finally {
      setBusy(null);
    }
  };

  const items = read?.items ?? [];
  const itemOf = (key: string) => items.find((item) => item.key === key);
  const save = (key: string) => {
    const raw = drafts[key] ?? regionShownValue(itemOf(key));
    const value = formValue(key, raw);
    const invalid = configValueError(key, value, itemOf(key)?.schema);
    if (invalid) {
      setMessage({ tone: "error", text: `${key}: ${invalid}` });
      return;
    }
    write({ action: "set", key, value });
  };
  const renderNotice = (key: string) => {
    const entry = notices[key];
    if (!entry) return null;
    return (
      <span className="polyforge-config-row-notice" role="status" data-pf-region-notice={key}>
        <span>{t("polyforge.cells.connectionNotice")}</span>
        {entry.rollbackable && entry.key !== "api_key" && (
          <ConfigButton size="small" disabled={busy !== null} onClick={() => void rollback(entry)}>{t("polyforge.settings.configRollback")}</ConfigButton>
        )}
      </span>
    );
  };
  return (
    <div className="polyforge-config-region" data-pf-settings-region="host" role="group" aria-label={title} style={{ display: "flex", flexDirection: "column", gap: 6, minWidth: 0 }}>
      <p className="polyforge-config-card-title">{title}</p>
      {HOST_ONLY_KEYS.map((key) => (
        <div key={key} className="polyforge-config-region-row" data-pf-region-key={key} style={REGION_ROW_STYLE}>
          <span className="polyforge-config-key"><BreakableToken text={key} /></span>
          <input aria-label={`${key} (host)`} value={drafts[key] ?? regionShownValue(itemOf(key))} onChange={(event) => setDrafts((current) => ({ ...current, [key]: event.target.value }))} />
          <ConfigButton size="small" disabled={busy !== null || read === null || confirming !== null} onClick={() => save(key)}>{t("polyforge.settings.configSave")}</ConfigButton>
          <ConfigButton size="small" disabled={busy !== null || read === null || confirming !== null || itemOf(key)?.source !== "file"} onClick={() => write({ action: "unset", key })}>{t("polyforge.cells.unset")}</ConfigButton>
          {renderNotice(key)}
        </div>
      ))}
      {confirming && (
        <div role="alertdialog" aria-label={confirming.key} className="polyforge-config-confirm" data-pf-region-confirm={confirming.key}>
          <p className="polyforge-config-confirm-text">{t("polyforge.settings.configConfirmConnection", { key: confirming.key })}</p>
          <div className="polyforge-config-confirm-actions">
            <ConfigButton size="small" variant="primary" disabled={busy !== null} onClick={confirmWrite}>{t("polyforge.settings.configConfirmSave")}</ConfigButton>
            <ConfigButton size="small" onClick={() => setConfirming(null)}>{t("polyforge.settings.configConfirmCancel")}</ConfigButton>
          </div>
        </div>
      )}
      {message && <p className={message.tone === "error" ? "config-add-source-error" : "polyforge-config-card-hint"} role="status">{message.text}</p>}
    </div>
  );
}

const EMPTY_CELL_DRAFT: CellAddDraft = { profile: "", server: "", apiKey: "", modelSource: "" };

/** the Add Cell pane (the Cells card's add form, moved). A pf
 *  without the multi-Cell capabilities shows the upgrade hint and no form.
 *  The api key input is write-only: it is cleared after every submit and
 *  never read back; pf receives it on stdin only (the route's contract). */
function AddCellPane({ read, supported, busy, message, onAdd }: {
  read: boolean;
  supported: boolean;
  busy: boolean;
  message: ReactNode;
  onAdd: (posted: CellAddDraft) => Promise<boolean>;
}) {
  const { t } = useI18n();
  const [draft, setDraft] = useState<CellAddDraft>(EMPTY_CELL_DRAFT);
  const [problem, setProblem] = useState<string | null>(null);

  const submit = async () => {
    const invalid = cellAddDraftError(draft);
    if (invalid) {
      setProblem(t(invalid));
      return;
    }
    setProblem(null);
    const posted = { profile: draft.profile.trim(), server: draft.server.trim(), apiKey: draft.apiKey, modelSource: draft.modelSource.trim() };
    // the key never stays in the form, whatever the outcome
    setDraft((current) => ({ ...current, apiKey: "" }));
    if (await onAdd(posted)) setDraft(EMPTY_CELL_DRAFT);
  };

  return (
    <>
      <ConfigDetailHeader>
        <ConfigDetailHeaderInfo>
          <ConfigDetailTitle>{t("polyforge.cells.add")}</ConfigDetailTitle>
        </ConfigDetailHeaderInfo>
      </ConfigDetailHeader>
      <p className="polyforge-config-card-hint">{t("polyforge.cells.addHint")}</p>
      {read && !supported && (
        <p className="polyforge-config-card-hint" data-pf-cells-upgrade>{t("polyforge.cells.upgrade")}</p>
      )}
      {supported && (
        <section className="polyforge-config-card" aria-label={t("polyforge.cells.add")}>
          <div className="polyforge-cell-add" data-pf-cell-add>
            <input aria-label={t("polyforge.cells.profile")} placeholder={t("polyforge.cells.profile")} value={draft.profile} onChange={(event) => setDraft({ ...draft, profile: event.target.value })} />
            <input aria-label={t("polyforge.cells.server")} placeholder="https://aihub.example" value={draft.server} onChange={(event) => setDraft({ ...draft, server: event.target.value })} />
            <input type="password" autoComplete="off" aria-label={t("polyforge.cells.apiKey")} placeholder={t("polyforge.cells.apiKey")} value={draft.apiKey} onChange={(event) => setDraft({ ...draft, apiKey: event.target.value })} />
            <input aria-label={t("polyforge.cells.modelSource")} placeholder="shared-default" value={draft.modelSource} onChange={(event) => setDraft({ ...draft, modelSource: event.target.value })} />
            <ConfigButton variant="primary" disabled={busy} onClick={() => void submit()}>{t("polyforge.cells.add")}</ConfigButton>
          </div>
        </section>
      )}
      {problem && <p className="config-add-source-error" role="status">{problem}</p>}
      {message}
    </>
  );
}

/** One workflow-card row's pending edit state. */
type WorkflowRowFeedback = { message: string; tone: "default" | "error" };

/**
 * The workflow card: the installed bundles' steps for one
 * mapped project — `default` first, one row per step id across bundles, then
 * `task`. Each row is one summary line with its declared chain (or Inherited),
 * the effective chain and its source level, and the bundles it appears in.
 * the live badge and the
 * next-dispatch note sit once at card level. Reads `GET
 * /api/polyforge/workflows` (fresh per ↻ click, no cache: only the newest read
 * may write the card, a failed one keeps the last good list plus an error);
 * writes go through `POST /api/polyforge/config` with the yaml sha as the
 * CAS. Write controls fold under the `workflow` disclosure region and are
 * disabled, with pf's reason as visible text, while the running daemon cannot
 * check the step keys. `pf submit --models` is only noted, never edited.
 */
function WorkflowCard({ cwd, profile, active, refreshKey, regionId, reloadClassLabel, onWritten }: {
  cwd: string | null;
  /** the pane's Cell — its mapping's workspaces, its --profile. */
  profile: string;
  active: boolean;
  /** bumped by the pane after a mapping write — one fresh read. */
  refreshKey: number;
  regionId: string;
  reloadClassLabel: (reloadClass: string) => string;
  onWritten: () => void;
}) {
  const { t } = useI18n();
  const { disclosure, setRegion } = usePolyforgeDisclosure();
  // The last good read: a failed refresh never clears it.
  const [read, setRead] = useState<PolyforgeWorkflowsRead | null>(null);
  const [readError, setReadError] = useState<string | null>(null);
  // The ↻ pattern: busy while the newest read is in flight, and
  // only that newest read may write the card or end the spin.
  const [refreshing, setRefreshing] = useState(false);
  const readSeqRef = useRef(0);
  const [selectedProject, setSelectedProject] = useState<string | null>(null);
  const [chainEdits, setChainEdits] = useState<Record<string, string[]>>({});
  const [chainAdds, setChainAdds] = useState<Record<string, string>>({});
  const [feedback, setFeedback] = useState<Record<string, WorkflowRowFeedback>>({});
  const [writing, setWriting] = useState<string | null>(null);

  const runRead = useCallback(() => {
    const seq = ++readSeqRef.current;
    setRefreshing(true);
    fetch(`/api/polyforge/workflows?cwd=${encodeURIComponent(cwd ?? "")}&profile=${encodeURIComponent(profile)}`)
      .then(async (response) => {
        const body = await response.json() as PolyforgeWorkflowsRead & { error?: string };
        if (seq !== readSeqRef.current) return;
        setRefreshing(false);
        // Good = at least one workspace answered with a reachable daemon; a
        // single failed workspace is its own line inside the card.
        const good = response.ok && body.capable && workflowUnion(body).sources.some((entry) => entry.workflows?.daemon_reachable === true);
        if (!good) {
          // Keep the list on screen; the error sits above it.
          setReadError(body.reason ?? body.error ?? `HTTP ${response.status}`);
          return;
        }
        setRead(body);
        setReadError(body.reason ?? null);
      })
      .catch((error: unknown) => {
        if (seq !== readSeqRef.current) return;
        setRefreshing(false);
        setReadError(error instanceof Error ? error.message : String(error));
      });
  }, [cwd, profile]);

  useEffect(() => {
    if (!active) return;
    setChainEdits({});
    setChainAdds({});
    setFeedback({});
    runRead();
  }, [active, runRead]);

  // A mapping write changed the Cell's workspaces: one fresh read (never a timer).
  const seenRefresh = useRef(refreshKey);
  useEffect(() => {
    if (seenRefresh.current === refreshKey) return;
    seenRefresh.current = refreshKey;
    runRead();
  }, [refreshKey, runRead]);

  // the union of every mapped workspace's projects; a
  // project's rows, capability and writes come from the workspace owning it.
  const union = workflowUnion(read);
  const projects = union.projects.map((entry) => entry.project);
  const ownProjects = (read?.workflows?.projects ?? []).filter((name) => projects.includes(name));
  const project = selectedProject !== null && projects.includes(selectedProject) ? selectedProject : defaultProject([...ownProjects, ...projects]);
  const owner = projectOwner(union, project);
  // With no project at all the first readable workspace still shows its
  // bundle facts (no rows, no write).
  const workflows = owner?.workflows ?? union.sources[0]?.workflows ?? null;
  const rows = buildWorkflowRows(workflows, owner?.declared ?? null, project);
  const writeCapable = workflows?.write_capable === true && owner?.declared != null && project !== null;
  const editorId = `${regionId}-workflow`;

  const draftIds = [
    ...Object.keys(chainEdits),
    ...Object.entries(chainAdds).filter(([, value]) => value.trim() !== "").map(([id]) => id),
  ];
  const marker = Object.values(feedback).some((entry) => entry.tone === "error")
    ? t("polyforge.settings.configErrorMarker")
    : draftIds.length > 0 ? t("polyforge.settings.configDraftMarker") : null;
  const foldOpen = marker !== null || disclosure.workflow;

  const write = useCallback(async (rowId: string, key: string, action: "set" | "unset", value?: unknown) => {
    if (!project || !owner) return;
    setWriting(key);
    setFeedback({});
    try {
      const response = await fetch("/api/polyforge/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          action,
          key,
          ...(action === "set" ? { value } : {}),
          // The owning workspace's --cwd and yaml CAS, never the settings cwd;
          // pf resolves it in this Cell's mapping.
          cwd: owner.workspace,
          profile,
          expect_state: { file_sha256: owner.declared?.yaml.sha256 ?? null, values: {} },
        }),
      });
      const data = await response.json() as { error?: string; message?: string };
      if (!response.ok || data.error) throw new Error(data.error ?? `HTTP ${response.status}`);
      setFeedback({ [rowId]: { message: data.message ?? t("polyforge.settings.configSaved"), tone: "default" } });
      setChainEdits((current) => { const next = { ...current }; delete next[rowId]; return next; });
      setChainAdds((current) => { const next = { ...current }; delete next[rowId]; return next; });
      runRead();
      onWritten();
    } catch (error) {
      setFeedback({ [rowId]: { message: error instanceof Error ? error.message : String(error), tone: "error" } });
    } finally {
      setWriting(null);
    }
  }, [onWritten, owner, profile, project, runRead, t]);

  const chainOf = (row: WorkflowRow) => chainEdits[row.id] ?? row.declaredModels ?? [];
  const editChain = (row: WorkflowRow, next: string[]) => setChainEdits((current) => ({ ...current, [row.id]: next }));
  const disabled = !writeCapable || writing !== null;

  const renderChips = (models: string[]) => models.length > 0 ? (
    <span className="polyforge-config-chips">
      {models.map((model, index) => <span key={`${index}:${model}`} className="polyforge-config-chip">{model}</span>)}
    </span>
  ) : <span className="polyforge-config-empty">{t("polyforge.settings.configUnset")}</span>;

  const renderEditor = (row: WorkflowRow) => {
    const chain = chainOf(row);
    const key = stepWriteKey(project ?? "", row.id);
    const save = () => {
      const pending = (chainAdds[row.id] ?? "").trim();
      const value = pending ? addCandidate(chain, pending) : chain;
      const invalid = stepValueError(key, value);
      if (invalid) { setFeedback({ [row.id]: { message: invalid, tone: "error" } }); return; }
      void write(row.id, key, "set", value);
    };
    return (
      <div className="polyforge-workflow-editor" hidden={!foldOpen}>
        <span className="polyforge-config-chips">
          {chain.map((model, index) => (
            <span key={`${index}:${model}`} className="polyforge-config-chip">
              <span className="polyforge-config-chip-label"><BreakableToken text={model} /></span>
              <button type="button" className="polyforge-config-chip-remove" disabled={disabled || index === 0}
                aria-label={`↑ ${model}`} onClick={() => editChain(row, moveCandidate(chain, index, -1))}>↑</button>
              <button type="button" className="polyforge-config-chip-remove" disabled={disabled || index === chain.length - 1}
                aria-label={`↓ ${model}`} onClick={() => editChain(row, moveCandidate(chain, index, 1))}>↓</button>
              <button type="button" className="polyforge-config-chip-remove" disabled={disabled}
                aria-label={`${t("polyforge.settings.configRemoveModel")} ${model}`} onClick={() => editChain(row, removeCandidate(chain, index))}>×</button>
            </span>
          ))}
        </span>
        <span className="polyforge-config-write">
          <input
            type="text"
            aria-label={`${t("polyforge.settings.workflowChainAdd")} · ${row.id}`}
            placeholder={t("polyforge.settings.workflowChainAdd")}
            disabled={disabled}
            value={chainAdds[row.id] ?? ""}
            onChange={(event) => setChainAdds((current) => ({ ...current, [row.id]: event.target.value }))}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                editChain(row, addCandidate(chain, chainAdds[row.id] ?? ""));
                setChainAdds((current) => ({ ...current, [row.id]: "" }));
              }
            }}
          />
          <ConfigButton size="small" disabled={disabled} onClick={save}>{t("polyforge.settings.configSave")}</ConfigButton>
          <ConfigButton size="small" disabled={disabled || !row.declared} onClick={() => void write(row.id, key, "unset")}>
            {t("polyforge.settings.workflowChainClear")}
          </ConfigButton>
        </span>
      </div>
    );
  };

  return (
    <section className="polyforge-config-card polyforge-workflow-card" aria-label={t("polyforge.settings.workflowTitle")}>
      <div className="polyforge-workflow-head">
        <p className="polyforge-config-card-title">{t("polyforge.settings.workflowTitle")}</p>
        {/* The ↻: one fresh read per click; disabled, aria-busy
            and spinning while the newest read is in flight. */}
        <button
          type="button"
          className={`polyforge-workflow-refresh${refreshing ? " animate-spin" : ""}`}
          onClick={() => { runRead(); }}
          disabled={refreshing}
          aria-busy={refreshing}
          title={refreshing ? t("polyforge.refreshing") : t("polyforge.refresh")}
          aria-label={refreshing ? t("polyforge.refreshing") : t("polyforge.refresh")}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M21 12a9 9 0 1 1-2.64-6.36L21 8" />
            <path d="M21 3v5h-5" />
          </svg>
        </button>
      </div>
      <p className="polyforge-config-card-hint">{t("polyforge.settings.workflowHint")}</p>
      {/* the next-dispatch fact and its live reload class are
          the card's, stated once; no row repeats either. */}
      {workflows && (
        <p className="polyforge-workflow-live">
          <span className="polyforge-config-badge" title="live">{reloadClassLabel("live")}</span>
          <span>{t("polyforge.settings.classLiveNote")}</span>
        </p>
      )}
      {readError && <p role="alert" className="config-add-source-error">{readError}</p>}
      {workflows && !workflows.write_capable && (
        <p className="config-scope-switch-reason">
          {t("polyforge.settings.workflowCapabilityHint")}{workflows.write_reason ? ` (${workflows.write_reason})` : ""}
        </p>
      )}
      {/* A workspace whose read failed: exactly one line with its reason,
          and none of its projects (so no write) in the union. */}
      {union.failures.map((entry) => (
        <p key={entry.workspace} className="config-scope-switch-reason is-error">
          {t("polyforge.settings.workflowWorkspaceFailed")}: {entry.yamlPath} — {entry.reason}
        </p>
      ))}
      {union.sources.length > 0 && (
        <ul className="polyforge-workflow-sources">
          {union.sources.map((entry) => (
            <li key={entry.workspace} className="polyforge-workflow-source">
              <span className="polyforge-workflow-label">{t("polyforge.settings.workflowProjectSource")}</span>
              <span className="polyforge-workflow-yaml"><BreakableToken text={entry.yamlPath} /></span>
              {renderChips(entry.workflows?.projects ?? [])}
            </li>
          ))}
        </ul>
      )}
      {workflows && (
        <>
          <label className="polyforge-workflow-project">
            <span>{t("polyforge.settings.workflowProject")}</span>
            <select value={project ?? ""} onChange={(event) => setSelectedProject(event.target.value)} disabled={projects.length === 0}>
              {projects.map((name) => <option key={name} value={name}>{name}</option>)}
            </select>
          </label>
          {/* this is a full sentence, not a short scope-switch
              reason, so it keeps its own class without the shared 320px cap. */}
          <p className="polyforge-workflow-submit-note">{t("polyforge.settings.workflowSubmitNote")}</p>
          {workflows.bundle_errors.map((entry) => (
            <p key={entry.bundle} className="config-scope-switch-reason is-error">
              {t("polyforge.settings.workflowBundleError")}: {entry.bundle} — {entry.reason}
            </p>
          ))}
          <DisclosureToggle
            open={foldOpen}
            controls={editorId}
            onToggle={() => { if (marker === null) setRegion("workflow", !disclosure.workflow); }}
            marker={marker}
          >
            {t("polyforge.settings.workflowEditToggle")}
          </DisclosureToggle>
          <ul id={editorId} className="polyforge-workflow-list">
            {rows.map((row) => (
              <li key={row.id} className="polyforge-workflow-row">
                <p className={row.diverges ? "polyforge-workflow-summary is-divergent" : "polyforge-workflow-summary"}>
                  {/* the row wraps by segment — this lead (id, kind,
                      declared chain), then the effective chain, then the suffix. */}
                  <span className="polyforge-workflow-lead">
                    <span className="polyforge-workflow-id">{row.id}</span>
                    {row.kind === "default" && <span className="polyforge-workflow-kind">{t("polyforge.settings.workflowDefaultRow")}</span>}
                    {row.kind === "task" && <span className="polyforge-workflow-kind">{t("polyforge.settings.workflowTaskRow")}</span>}
                    {row.declared ? (
                      <>
                        <span className="polyforge-workflow-label">{t("polyforge.settings.workflowDeclared")}</span>
                        {renderChips(row.declaredModels ?? [])}
                      </>
                    ) : (
                      <span className="polyforge-workflow-inherit">{t("polyforge.settings.workflowInherit")}</span>
                    )}
                  </span>
                  <span className="polyforge-workflow-effective">
                    <span className="polyforge-workflow-label">{t("polyforge.settings.workflowEffective")}</span>
                    {row.effective.length === 0 && <span className="polyforge-config-empty">{t("polyforge.settings.configUnset")}</span>}
                    {row.effective.map((view) => (
                      <span key={view.bundle ?? ""} className="polyforge-workflow-chain">
                        {view.bundle && <span className="polyforge-workflow-bundle-name">{view.bundle}</span>}
                        {renderChips(view.models)}
                        <span className="polyforge-workflow-from" title={`${t("polyforge.settings.workflowSourceLabel")}: ${view.from}`}>
                          {t("polyforge.settings.workflowSourceLabel")}: <BreakableToken text={view.from} />
                        </span>
                      </span>
                    ))}
                  </span>
                  {row.bundles.length > 0 && (
                    <span className="polyforge-workflow-bundle-suffix">
                      {row.bundles.map((ref, index) => (
                        <Fragment key={ref.bundle}>
                          {index > 0 && " · "}
                          <span
                            className="polyforge-workflow-bundle-name"
                            title={ref.candidates && ref.candidates.length > 0 ? `${t("polyforge.tasks.candidates")}: ${ref.candidates.join(", ")}` : undefined}
                          >
                            {ref.bundle}
                          </span>
                        </Fragment>
                      ))}
                    </span>
                  )}
                </p>
                {/* the project's skip switch of a step some lane
                    declares skippable — outside the fold, on whenever the card
                    can write; checked writes `true`, unchecked unsets the key. */}
                {row.skippable && (
                  <label className="polyforge-workflow-skip" title={t("polyforge.settings.workflowSkipHint")}>
                    <input
                      type="checkbox"
                      aria-label={`${t("polyforge.settings.workflowSkip")} · ${row.id}`}
                      checked={row.skip}
                      disabled={!writeCapable}
                      onChange={(event) => {
                        const key = skipWriteKey(project ?? "", row.id);
                        return event.target.checked ? void write(row.id, key, "set", true) : void write(row.id, key, "unset");
                      }}
                    />
                    <span>{t("polyforge.settings.workflowSkip")}</span>
                  </label>
                )}
                {renderEditor(row)}
                {feedback[row.id] && (
                  <p className={`polyforge-config-row-feedback${feedback[row.id].tone === "error" ? " is-error" : ""}`} role={feedback[row.id].tone === "error" ? "alert" : undefined}>
                    {feedback[row.id].message}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
