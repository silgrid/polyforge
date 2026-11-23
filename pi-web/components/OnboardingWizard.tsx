"use client";

// The first-open onboarding wizard (polyforge#167, 91-onboarding): four
// steps — AIHub connection, account, workspace, first ticket — each
// skippable, each reading the state the page it reuses already owns. The
// wizard copies no configuration logic: the AIHub step shows #162's
// first-boot connection result (and, in the team mode, adds a Cell through
// the existing `POST /api/polyforge/cells`, the key on stdin), the account
// step deep-links to Settings › 账号 and reads the login status through
// #142's `GET /api/settings/account`, the workspace step goes through the
// existing `/api/cwd/validate` selection, and the first ticket reuses
// `PolyforgeFileTicketDialog` with `lane="develop"` (Q5).
//
// Server state and every write come from the parent (AppShell): a step
// completes through `onComplete`, skips through `onSkip` (the step stays
// 「未完成」), closing dismisses through `onClose`. The rendered view is
// therefore a pure function of `view` + `cwd`, which is what the unit test
// observes.
import { useEffect, useId, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import {
  ONBOARDING_STEPS,
  followingOnboardingStep,
  type OnboardingConnection,
  type OnboardingStep,
  type OnboardingView,
} from "@/lib/onboarding-flow";
import { PolyforgeFileTicketDialog } from "./PolyforgeFileTicketDialog";

export interface OnboardingWizardProps {
  view: OnboardingView;
  /** The workspace the page already has selected, when there is one. */
  cwd: string | null;
  onComplete: (step: OnboardingStep) => void;
  /** A skip records nothing: the step stays 「未完成」. */
  onSkip: (step: OnboardingStep) => void;
  onClose: () => void;
  /** Settings › 账号's deep link (the account step's read is the page's). */
  onOpenAccount: () => void;
  onWorkspaceChange: (cwd: string) => void;
}

const STEP_LABEL_KEYS: Record<OnboardingStep, string> = {
  aihub: "onboarding.stepAihub",
  account: "onboarding.stepAccount",
  workspace: "onboarding.stepWorkspace",
  ticket: "onboarding.stepTicket",
};

interface ProjectsRead {
  projects?: { project?: unknown }[];
}

interface AccountRead {
  claude?: { loggedIn?: boolean | null } | null;
}

interface CellAddResponse {
  ok?: boolean;
  error?: string;
}

function connectionModeKey(connection: OnboardingConnection | null): string {
  return connection?.mode === "external" ? "onboarding.aihubTeam" : "onboarding.aihubLocal";
}

function readyLabel(t: (key: string) => string, ready: boolean): string {
  return ready ? t("onboarding.aihubReady") : t("onboarding.aihubNotReadyState");
}

/**
 * The AIHub step: #162's first-boot connection result, plus the team mode's
 * `pf cell add` form (the key is a password input, posted to the existing
 * route — never an argv).
 */
function AihubStep({
  connection,
  onComplete,
  onSkip,
}: {
  connection: OnboardingConnection | null;
  onComplete: () => void;
  onSkip: () => void;
}) {
  const { t } = useI18n();
  const team = connection?.mode === "external";
  const [profile, setProfile] = useState("");
  const [server, setServer] = useState(connection?.server ?? "");
  const [apiKey, setApiKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const ready = connection !== null && (connection.userReady || connection.keyReady);
  const field = { background: "var(--bg)", color: "var(--text)", border: "1px solid var(--border)", borderRadius: 4, padding: "4px 6px", fontSize: 12, font: "inherit" } as const;

  const addCell = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const response = await fetch("/api/polyforge/cells", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ profile, server, apiKey }),
      });
      const body = await response.json() as CellAddResponse;
      if (!response.ok || body.ok !== true) {
        setError(body.error ?? t("onboarding.aihubAddFailed"));
        return;
      }
      setApiKey("");
      onComplete();
    } catch {
      setError(t("onboarding.aihubAddFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div data-onboarding-step-panel="aihub" style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
      <div data-onboarding-connection={connection?.mode ?? "absent"} style={{ color: "var(--text-muted)" }}>
        {t(connectionModeKey(connection))}
      </div>
      {connection === null ? (
        <p role="status" style={{ margin: 0, color: "var(--text-muted)" }}>{t("onboarding.aihubMissing")}</p>
      ) : (
        <dl data-onboarding-connection-facts style={{ margin: 0, display: "grid", gridTemplateColumns: "auto 1fr", gap: "2px 12px" }}>
          <dt>{t("onboarding.aihubPhase")}</dt><dd data-fact="phase">{connection.phase ?? "—"}</dd>
          <dt>{t("onboarding.aihubDefaultCell")}</dt><dd data-fact="defaultCell">{connection.defaultCell ?? "—"}</dd>
          {team && <><dt>{t("onboarding.aihubServer")}</dt><dd data-fact="server">{connection.server ?? "—"}</dd></>}
          <dt>{t("onboarding.aihubUserReady")}</dt><dd data-fact="userReady">{readyLabel(t, connection.userReady)}</dd>
          <dt>{t("onboarding.aihubKeyReady")}</dt><dd data-fact="keyReady">{readyLabel(t, connection.keyReady)}</dd>
        </dl>
      )}
      {team && (
        <fieldset data-onboarding-team-form style={{ border: "1px solid var(--border)", borderRadius: 6, display: "flex", flexDirection: "column", gap: 6, margin: 0, padding: 8 }}>
          <legend style={{ fontSize: 11, color: "var(--text-dim)" }}>{t("onboarding.aihubAddCell")}</legend>
          <label style={{ display: "flex", flexDirection: "column", gap: 2 }}>
            {t("onboarding.aihubProfile")}
            <input value={profile} onChange={(event) => setProfile(event.target.value)} data-onboarding-cell-profile style={field} />
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 2 }}>
            {t("onboarding.aihubServer")}
            <input value={server} onChange={(event) => setServer(event.target.value)} data-onboarding-cell-server style={field} />
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 2 }}>
            {t("onboarding.aihubApiKey")}
            <input type="password" autoComplete="off" value={apiKey} onChange={(event) => setApiKey(event.target.value)} data-onboarding-cell-key style={field} />
          </label>
          {error && <div role="alert" style={{ color: "#dc2626", overflowWrap: "anywhere" }}>{error}</div>}
          <button type="button" onClick={() => void addCell()} data-onboarding-cell-add disabled={busy || profile === "" || server === "" || apiKey === ""}
            style={{ alignSelf: "flex-start", background: "var(--accent)", color: "var(--bg-panel)", border: "none", borderRadius: 4, padding: "4px 12px", fontSize: 12 }}>
            {t("onboarding.aihubAdd")}
          </button>
        </fieldset>
      )}
      {!team && (
        <button type="button" onClick={onComplete} disabled={!ready} data-onboarding-primary="aihub"
          style={{ alignSelf: "flex-start", background: "var(--accent)", color: "var(--bg-panel)", border: "none", borderRadius: 4, padding: "4px 12px", fontSize: 12, opacity: ready ? 1 : 0.5 }}>
          {t("onboarding.continue")}
        </button>
      )}
      <button type="button" onClick={onSkip} style={{ alignSelf: "flex-start", background: "none", color: "var(--text-muted)", border: "none", fontSize: 11, cursor: "pointer" }}>
        {t("onboarding.skip")}
      </button>
    </div>
  );
}

/** The account step: the deep link and #142's read-back login status (Q4 —
 *  only a signed-in answer completes the step). */
function AccountStep({ onComplete, onSkip, onOpenAccount }: { onComplete: () => void; onSkip: () => void; onOpenAccount: () => void }) {
  const { t } = useI18n();
  const [state, setState] = useState<"idle" | "loading" | "logged-in" | "logged-out" | "failed">("idle");

  const check = async () => {
    if (state === "loading") return;
    setState("loading");
    try {
      const response = await fetch("/api/settings/account");
      const body = await response.json() as AccountRead;
      if (!response.ok) {
        setState("failed");
        return;
      }
      if (body.claude?.loggedIn === true) {
        setState("logged-in");
        onComplete();
        return;
      }
      setState("logged-out");
    } catch {
      setState("failed");
    }
  };

  const status = state === "logged-in"
    ? t("onboarding.accountLoggedIn")
    : state === "logged-out"
      ? t("onboarding.accountLoggedOut")
      : state === "failed"
        ? t("onboarding.accountCheckFailed")
        : "";
  return (
    <div data-onboarding-step-panel="account" style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
      <p style={{ margin: 0, color: "var(--text-muted)" }}>{t("onboarding.accountHint")}</p>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <button type="button" onClick={onOpenAccount} data-onboarding-account-link
          style={{ background: "none", color: "var(--accent)", border: "1px solid var(--border)", borderRadius: 4, padding: "4px 12px", fontSize: 12 }}>
          {t("onboarding.accountOpen")}
        </button>
        <button type="button" onClick={() => void check()} disabled={state === "loading"} data-onboarding-account-check
          style={{ background: "var(--accent)", color: "var(--bg-panel)", border: "none", borderRadius: 4, padding: "4px 12px", fontSize: 12 }}>
          {t("onboarding.accountCheck")}
        </button>
      </div>
      {status && <div role="status" data-onboarding-account-status={state}>{status}</div>}
      <button type="button" onClick={onSkip} style={{ alignSelf: "flex-start", background: "none", color: "var(--text-muted)", border: "none", fontSize: 11, cursor: "pointer" }}>
        {t("onboarding.skip")}
      </button>
    </div>
  );
}

/** The workspace step: the page's current workspace, selectable through the
 *  existing `/api/cwd/validate` selection. */
function WorkspaceStep({ cwd, onComplete, onSkip, onWorkspaceChange }: { cwd: string | null; onComplete: () => void; onSkip: () => void; onWorkspaceChange: (cwd: string) => void }) {
  const { t } = useI18n();
  const [path, setPath] = useState(cwd ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const field = { background: "var(--bg)", color: "var(--text)", border: "1px solid var(--border)", borderRadius: 4, padding: "4px 6px", fontSize: 12, font: "inherit" } as const;

  const use = async () => {
    if (busy || path.trim() === "") return;
    setBusy(true);
    setError(null);
    try {
      const response = await fetch("/api/cwd/validate", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ cwd: path.trim() }),
      });
      const body = await response.json() as { cwd?: string; error?: string };
      if (!response.ok || !body.cwd) {
        setError(body.error ?? t("onboarding.workspaceFailed"));
        return;
      }
      onWorkspaceChange(body.cwd);
      onComplete();
    } catch {
      setError(t("onboarding.workspaceFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div data-onboarding-step-panel="workspace" style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
      <p style={{ margin: 0, color: "var(--text-muted)" }}>{t("onboarding.workspaceCurrent")}: <code>{cwd ?? "—"}</code></p>
      <label style={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {t("onboarding.workspacePath")}
        <input value={path} onChange={(event) => setPath(event.target.value)} data-onboarding-workspace-path style={field} />
      </label>
      {error && <div role="alert" style={{ color: "#dc2626", overflowWrap: "anywhere" }}>{error}</div>}
      <button type="button" onClick={() => void use()} disabled={busy || path.trim() === ""} data-onboarding-workspace-use
        style={{ alignSelf: "flex-start", background: "var(--accent)", color: "var(--bg-panel)", border: "none", borderRadius: 4, padding: "4px 12px", fontSize: 12 }}>
        {t("onboarding.workspaceUse")}
      </button>
      <button type="button" onClick={onSkip} style={{ alignSelf: "flex-start", background: "none", color: "var(--text-muted)", border: "none", fontSize: 11, cursor: "pointer" }}>
        {t("onboarding.skip")}
      </button>
    </div>
  );
}

/** The first-ticket step: the existing dialog in its message-less,
 *  `develop`-lane form (Q5). */
function TicketStep({ onComplete, onSkip, onClose }: { onComplete: () => void; onSkip: () => void; onClose: () => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [projects, setProjects] = useState<string[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    fetch("/api/polyforge/projects", { signal: controller.signal })
      .then(async (response) => {
        const body = await response.json() as ProjectsRead;
        const names = (body.projects ?? [])
          .map((row) => row.project)
          .filter((name): name is string => typeof name === "string" && name !== "");
        setProjects(names);
      })
      .catch((error) => { if (!(error instanceof DOMException && error.name === "AbortError")) setProjects([]); });
    return () => controller.abort();
  }, []);
  return (
    <div data-onboarding-step-panel="ticket" style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
      <p style={{ margin: 0, color: "var(--text-muted)" }}>{t("onboarding.ticketHint")}</p>
      <button type="button" onClick={() => setOpen(true)} data-onboarding-ticket-open
        style={{ alignSelf: "flex-start", background: "var(--accent)", color: "var(--bg-panel)", border: "none", borderRadius: 4, padding: "4px 12px", fontSize: 12 }}>
        {t("onboarding.ticketOpen")}
      </button>
      <button type="button" onClick={onSkip} style={{ alignSelf: "flex-start", background: "none", color: "var(--text-muted)", border: "none", fontSize: 11, cursor: "pointer" }}>
        {t("onboarding.skip")}
      </button>
      {open && (
        <PolyforgeFileTicketDialog
          projects={projects}
          lane="develop"
          onClose={() => setOpen(false)}
          onCreated={() => { setOpen(false); onComplete(); onClose(); }}
        />
      )}
    </div>
  );
}

export function OnboardingWizard({
  view,
  cwd,
  onComplete,
  onSkip,
  onClose,
  onOpenAccount,
  onWorkspaceChange,
}: OnboardingWizardProps) {
  const { t } = useI18n();
  const titleId = useId();
  const [panel, setPanel] = useState<OnboardingStep>(view.nextStep);
  const currentIndex = ONBOARDING_STEPS.indexOf(view.nextStep);

  // The parent's view is the one source: a completed step moves the panel to
  // the new first unfinished one, never backwards.
  useEffect(() => {
    setPanel(view.nextStep);
  }, [view.nextStep]);

  const advance = (step: OnboardingStep) => {
    const next = followingOnboardingStep(step);
    if (next === null) onClose();
    else setPanel(next);
  };
  const complete = (step: OnboardingStep) => {
    onComplete(step);
    advance(step);
  };
  const skip = (step: OnboardingStep) => {
    onSkip(step);
    advance(step);
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      data-onboarding-wizard
      data-onboarding-current={view.nextStep}
      style={{ position: "fixed", inset: 0, zIndex: 1000, background: "rgba(0, 0, 0, 0.35)", display: "flex", alignItems: "center", justifyContent: "center", padding: 16 }}
    >
      <div style={{ width: "min(560px, 100%)", maxHeight: "100%", overflowY: "auto", background: "var(--bg-panel)", color: "var(--text)", border: "1px solid var(--border)", borderRadius: 8, padding: 14, display: "flex", flexDirection: "column", gap: 10 }}>
        <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", gap: 8 }}>
          <div>
            <div id={titleId} style={{ fontSize: 15, fontWeight: 600 }}>{t("onboarding.title")}</div>
            <div style={{ fontSize: 11, color: "var(--text-dim)" }}>{t("onboarding.description")}</div>
          </div>
          <div style={{ fontSize: 11, color: "var(--text-dim)", whiteSpace: "nowrap" }}>
            {t("onboarding.stepOf", { current: String(currentIndex + 1), total: String(ONBOARDING_STEPS.length) })}
          </div>
          <button type="button" onClick={onClose} aria-label={t("onboarding.close")} title={t("onboarding.close")} data-onboarding-close
            style={{ background: "none", border: "none", color: "var(--text-dim)", fontSize: 18, cursor: "pointer", lineHeight: 1 }}>×</button>
        </div>

        <ol data-onboarding-steps style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: 2 }}>
          {ONBOARDING_STEPS.map((step, index) => {
            const completeStep = view.steps[step] === "complete";
            const active = panel === step;
            return (
              <li key={step} data-onboarding-step={step} data-onboarding-status={view.steps[step]}>
                <button type="button" onClick={() => setPanel(step)} disabled={!completeStep && step !== view.nextStep}
                  aria-current={active ? "step" : undefined}
                  style={{ width: "100%", display: "flex", alignItems: "center", gap: 8, background: active ? "var(--bg-hover)" : "none", border: "none", borderRadius: 4, padding: "4px 6px", color: "var(--text)", textAlign: "left", cursor: "pointer", fontSize: 12 }}>
                  <span aria-hidden="true" style={{ width: 16, color: completeStep ? "var(--accent)" : "var(--text-dim)" }}>{completeStep ? "✓" : String(index + 1)}</span>
                  <span style={{ flex: 1 }}>{t(STEP_LABEL_KEYS[step])}</span>
                  <span data-onboarding-status-text={view.steps[step]} style={{ fontSize: 10, color: completeStep ? "var(--accent)" : "var(--text-dim)" }}>
                    {completeStep ? t("onboarding.statusComplete") : t("onboarding.statusIncomplete")}
                  </span>
                </button>
              </li>
            );
          })}
        </ol>

        {panel === "aihub" && <AihubStep connection={view.connection} onComplete={() => complete("aihub")} onSkip={() => skip("aihub")} />}
        {panel === "account" && <AccountStep onComplete={() => complete("account")} onSkip={() => skip("account")} onOpenAccount={onOpenAccount} />}
        {panel === "workspace" && <WorkspaceStep cwd={cwd} onComplete={() => complete("workspace")} onSkip={() => skip("workspace")} onWorkspaceChange={onWorkspaceChange} />}
        {panel === "ticket" && <TicketStep onComplete={() => complete("ticket")} onSkip={() => skip("ticket")} onClose={onClose} />}
      </div>
    </div>
  );
}
