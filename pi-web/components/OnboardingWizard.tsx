"use client";

// First-open setup: configuration writes stay on the existing account,
// workspace, Cell and ticket paths; AppShell owns persisted step state.
import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useI18n } from "@/hooks/useI18n";
import { focusModalPanel } from "@/lib/stacked-dialog";
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
  cwd: string | null;
  onComplete: (step: OnboardingStep) => void;
  onSkip: (step: OnboardingStep) => void;
  onClose: () => void;
  onOpenAccount: () => void;
  onOpenModels: () => void;
  onWorkspaceChange: (cwd: string) => void;
}

const STEP_LABEL_KEYS: Record<OnboardingStep, string> = {
  aihub: "onboarding.stepAihub",
  account: "onboarding.stepAccount",
  workspace: "onboarding.stepWorkspace",
  ticket: "onboarding.stepTicket",
};
const STEP_HINT_KEYS: Record<OnboardingStep, string> = {
  aihub: "onboarding.aihubHint",
  account: "onboarding.accountHint",
  workspace: "onboarding.workspaceHint",
  ticket: "onboarding.ticketHint",
};

function StepFrame({ step, children, actions, onSkip, busy = false }: {
  step: OnboardingStep; children: ReactNode; actions: ReactNode; onSkip: () => void; busy?: boolean;
}) {
  const { t } = useI18n();
  return <section className="onboarding-step-panel" data-onboarding-step-panel={step}>
    <div className="onboarding-content">
      <header className="onboarding-step-heading">
        <h2 tabIndex={-1}>{t(STEP_LABEL_KEYS[step])}</h2>
        <p>{t(STEP_HINT_KEYS[step])}</p>
      </header>
      {children}
    </div>
    <footer className="onboarding-footer">
      <button type="button" className="onboarding-skip" onClick={onSkip} disabled={busy}>{t("onboarding.skip")}</button>
      <div className="onboarding-actions">{actions}</div>
    </footer>
  </section>;
}

function AihubStep({ connection, onComplete, onSkip }: {
  connection: OnboardingConnection | null; onComplete: () => void; onSkip: () => void;
}) {
  const { t } = useI18n();
  const team = connection?.mode === "external";
  const [profile, setProfile] = useState("");
  const [server, setServer] = useState(connection?.server ?? "");
  const [apiKey, setApiKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The record is setup evidence, not a live health/authentication probe.
  const ready = connection?.mode === "local" && connection.phase === "done"
    && connection.userReady && connection.keyReady && Boolean(connection.defaultCell) && Boolean(connection.server);
  const failed = connection?.phase?.endsWith("/failed") === true;
  const status = !connection || !connection.mode ? "missing" : failed ? "failed" : team ? "external" : ready ? "ready" : "pending";
  const titleKeys = {
    missing: "onboarding.connectionMissing", failed: "onboarding.connectionFailed",
    external: "onboarding.aihubTeam", ready: "onboarding.connectionReady", pending: "onboarding.connectionPending",
  };
  const hintKeys = {
    missing: "onboarding.aihubMissing", failed: "onboarding.connectionFailedHint",
    external: "onboarding.connectionExternalHint", ready: "onboarding.connectionReadyHint", pending: "onboarding.connectionPendingHint",
  };
  const formId = useId();
  const addCell = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const response = await fetch("/api/polyforge/cells", {
        method: "POST", headers: { "content-type": "application/json" },
        body: JSON.stringify({ profile, server, apiKey }),
      });
      const body = await response.json() as { ok?: boolean; error?: string };
      if (!response.ok || body.ok !== true) { setError(body.error ?? t("onboarding.aihubAddFailed")); return; }
      setApiKey("");
      onComplete();
    } catch { setError(t("onboarding.aihubAddFailed")); }
    finally { setBusy(false); }
  };
  return <StepFrame step="aihub" onSkip={onSkip} busy={busy} actions={team ? (
    <button type="submit" form={formId} className="onboarding-primary" data-onboarding-cell-add
      disabled={busy || !profile.trim() || !server.trim() || !apiKey} aria-busy={busy}>{t(busy ? "onboarding.working" : "onboarding.aihubAdd")}</button>
  ) : (
    <button type="button" className="onboarding-primary" onClick={onComplete} disabled={!ready} data-onboarding-primary="aihub">{t("onboarding.continue")} <span aria-hidden="true">→</span></button>
  )}>
    <div className="onboarding-connection" data-onboarding-connection={connection?.mode ?? "absent"} data-connection-status={status}>
      <div className="onboarding-connection-heading">
        <span className="onboarding-status-icon" aria-hidden="true">{ready ? "✓" : failed ? "!" : "◎"}</span>
        <div role="status"><h3>{t(titleKeys[status])}</h3><p>{t(hintKeys[status])}</p></div>
      </div>
      {connection && <dl className="onboarding-summary">
        <div><dt>{t("onboarding.project")}</dt><dd data-fact="project">{connection.project ?? t("onboarding.notProvided")}</dd></div>
        <div><dt>{t("onboarding.aihubServer")}</dt><dd data-fact="server">{connection.server ?? t(connection.mode === "local" ? "onboarding.aihubLocal" : "onboarding.notProvided")}</dd></div>
      </dl>}
    </div>
    {team && <form id={formId} onSubmit={(event) => { event.preventDefault(); void addCell(); }} className="onboarding-form" data-onboarding-team-form>
      <h3>{t("onboarding.aihubAddCell")}</h3>
      <label>{t("onboarding.aihubProfile")}<input value={profile} onChange={(event) => setProfile(event.target.value)} data-onboarding-cell-profile disabled={busy} /></label>
      <label>{t("onboarding.aihubServer")}<input value={server} onChange={(event) => setServer(event.target.value)} data-onboarding-cell-server disabled={busy} /></label>
      <label>{t("onboarding.aihubApiKey")}<input type="password" autoComplete="off" value={apiKey} onChange={(event) => setApiKey(event.target.value)} data-onboarding-cell-key disabled={busy} /></label>
      {error && <p role="alert" className="onboarding-error">{error}</p>}
    </form>}
    {connection && <details className="onboarding-details">
      <summary>{t("onboarding.technicalDetails")}</summary>
      <dl data-onboarding-connection-facts>
        <dt>{t("onboarding.aihubPhase")}</dt><dd data-fact="phase">{connection.phase ?? "—"}</dd>
        <dt>{t("onboarding.aihubDefaultCell")}</dt><dd data-fact="defaultCell">{connection.defaultCell ?? "—"}</dd>
        <dt>{t("onboarding.aihubUserReady")}</dt><dd data-fact="userReady">{t(connection.userReady ? "onboarding.aihubReady" : "onboarding.aihubNotReadyState")}</dd>
        <dt>{t("onboarding.aihubKeyReady")}</dt><dd data-fact="keyReady">{t(connection.keyReady ? "onboarding.aihubReady" : "onboarding.aihubNotReadyState")}</dd>
      </dl>
    </details>}
  </StepFrame>;
}

function AccountStep({ onComplete, onSkip, onOpenAccount, onOpenModels }: { onComplete: () => void; onSkip: () => void; onOpenAccount: () => void; onOpenModels: () => void }) {
  const { t } = useI18n();
  const [state, setState] = useState<"idle" | "loading" | "logged-in" | "logged-out" | "failed">("idle");
  const check = async () => {
    if (state === "loading") return;
    setState("loading");
    try {
      // Both existing reads return status/model metadata only, never keys.
      // getAvailable() behind models/enabled includes custom models.json sources.
      const read = async (url: string) => {
        const response = await fetch(url);
        if (!response.ok) throw new Error("account-read");
        return response.json();
      };
      const [account, models] = await Promise.allSettled([read("/api/settings/account"), read("/api/models/enabled")]);
      const signedIn = account.status === "fulfilled" && account.value.claude?.loggedIn === true;
      const piAvailable = models.status === "fulfilled" && !models.value.error && !models.value.modelError
        && Array.isArray(models.value.providers) && models.value.providers.some((provider: { models?: { ref?: unknown }[] }) =>
          Array.isArray(provider.models) && provider.models.some((model) => typeof model.ref === "string" && model.ref.length > 0));
      if (signedIn || piAvailable) { setState("logged-in"); onComplete(); return; }
      setState(account.status === "rejected" || models.status === "rejected" || (models.status === "fulfilled" && (models.value.error || models.value.modelError)) ? "failed" : "logged-out");
    } catch { setState("failed"); }
  };
  return <StepFrame step="account" onSkip={onSkip} busy={state === "loading"} actions={
    <button type="button" className="onboarding-primary" onClick={() => void check()} disabled={state === "loading"} aria-busy={state === "loading"} data-onboarding-account-check>{t(state === "loading" ? "onboarding.working" : "onboarding.accountCheck")}</button>
  }>
    <div className="onboarding-card">
      <h3>{t("onboarding.accountTitle")}</h3>
      <p>{t("onboarding.accountReturn")}</p>
      <button type="button" className="onboarding-secondary" onClick={onOpenAccount} data-onboarding-account-link>{t("onboarding.accountOpen")} <span aria-hidden="true">↗</span></button>
    </div>
    <div className="onboarding-card">
      <h3>{t("onboarding.customTitle")}</h3><p>{t("providers.customSubtitle")}</p>
      <button type="button" className="onboarding-secondary" onClick={onOpenModels} data-onboarding-models-link>{t("onboarding.customOpen")} <span aria-hidden="true">↗</span></button>
    </div>
    {state !== "idle" && state !== "loading" && <p role="status" data-onboarding-account-status={state} className="onboarding-notice">
      {t(state === "logged-in" ? "onboarding.accountLoggedIn" : state === "failed" ? "onboarding.accountCheckFailed" : "onboarding.accountLoggedOut")}
    </p>}
  </StepFrame>;
}

function WorkspaceStep({ cwd, onComplete, onSkip, onWorkspaceChange }: { cwd: string | null; onComplete: () => void; onSkip: () => void; onWorkspaceChange: (cwd: string) => void }) {
  const { t } = useI18n();
  const [path, setPath] = useState(cwd ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const formId = useId();
  const use = async () => {
    if (busy || !path.trim()) return;
    setBusy(true); setError(null);
    try {
      const response = await fetch("/api/cwd/validate", {
        method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ cwd: path.trim() }),
      });
      const body = await response.json() as { cwd?: string; error?: string };
      if (!response.ok || !body.cwd) { setError(body.error ?? t("onboarding.workspaceFailed")); return; }
      onWorkspaceChange(body.cwd); onComplete();
    } catch { setError(t("onboarding.workspaceFailed")); }
    finally { setBusy(false); }
  };
  return <StepFrame step="workspace" onSkip={onSkip} busy={busy} actions={
    <button type="submit" form={formId} className="onboarding-primary" disabled={busy || !path.trim()} aria-busy={busy} data-onboarding-workspace-use>{t(busy ? "onboarding.working" : "onboarding.workspaceUse")}</button>
  }>
    <div className="onboarding-card"><span className="onboarding-eyebrow">{t("onboarding.workspaceCurrent")}</span><code>{cwd ?? t("onboarding.notProvided")}</code></div>
    <form id={formId} className="onboarding-form" onSubmit={(event) => { event.preventDefault(); void use(); }}>
      <label>{t("onboarding.workspacePath")}<input value={path} onChange={(event) => setPath(event.target.value)} data-onboarding-workspace-path disabled={busy} /></label>
      <p>{t("onboarding.workspaceHelp")}</p>
      {error && <p role="alert" className="onboarding-error">{error}</p>}
    </form>
  </StepFrame>;
}

function TicketStep({ onComplete, onSkip, onBusyChange }: { onComplete: () => void; onSkip: () => void; onBusyChange: (busy: boolean) => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [projects, setProjects] = useState<string[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    fetch("/api/polyforge/projects", { signal: controller.signal }).then(async (response) => {
      const body = await response.json() as { projects?: { project?: unknown }[] };
      setProjects(response.ok ? (body.projects ?? []).map((row) => row.project).filter((name): name is string => typeof name === "string" && name !== "") : []);
    }).catch(() => { if (!controller.signal.aborted) setProjects([]); });
    return () => controller.abort();
  }, []);
  if (open) return <section className="onboarding-step-panel" data-onboarding-step-panel="ticket">
    <PolyforgeFileTicketDialog projects={projects} lane="develop" embedded onBusyChange={onBusyChange} onClose={() => setOpen(false)} onCreated={onComplete} />
  </section>;
  return <StepFrame step="ticket" onSkip={onSkip} actions={
    <button type="button" className="onboarding-primary" onClick={() => setOpen(true)} data-onboarding-ticket-open>{t("onboarding.ticketOpen")} <span aria-hidden="true">→</span></button>
  }>
    <div className="onboarding-card"><span className="onboarding-eyebrow">{t("onboarding.ticketEyebrow")}</span><h3>{t("onboarding.ticketTitle")}</h3><p>{t("onboarding.ticketHelp")}</p></div>
  </StepFrame>;
}

export function OnboardingWizard({ view, cwd, onComplete, onSkip, onClose, onOpenAccount, onOpenModels, onWorkspaceChange }: OnboardingWizardProps) {
  const { t } = useI18n();
  const titleId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const [panel, setPanel] = useState<OnboardingStep>(view.nextStep);
  const [ticketBusy, setTicketBusy] = useState(false);
  const ticketBusyRef = useRef(false);
  const updateTicketBusy = (busy: boolean) => { ticketBusyRef.current = busy; setTicketBusy(busy); };
  const currentIndex = ONBOARDING_STEPS.indexOf(panel);
  useEffect(() => { setPanel(view.nextStep); }, [view.nextStep]);
  useLayoutEffect(() => focusModalPanel(document, dialogRef.current), []);
  useEffect(() => {
    // Only the top visible dialog owns keys. Settings can open above this
    // wizard and return focus to its account link without dismissing setup.
    const handleKey = (event: KeyboardEvent) => {
      const dialogs = [...document.querySelectorAll<HTMLElement>('[role="dialog"], [role="alertdialog"]')].filter((element) => element.getClientRects().length > 0);
      if (dialogs.at(-1) !== dialogRef.current) return;
      if (event.key === "Escape") {
        event.stopPropagation();
        if (event.isComposing || event.keyCode === 229) return;
        event.preventDefault();
        if (ticketBusyRef.current) return;
        const cancelTicket = dialogRef.current?.querySelector<HTMLButtonElement>("[data-onboarding-ticket-cancel]");
        if (cancelTicket) cancelTicket.click();
        else closeRef.current();
      }
      if (event.key === "Tab") {
        const controls = [...dialogRef.current.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [tabindex="0"]')].filter((element) => element.getClientRects().length > 0);
        const first = controls[0]; const last = controls.at(-1);
        if (event.shiftKey && (document.activeElement === first || document.activeElement === dialogRef.current)) { event.preventDefault(); last?.focus(); }
        else if (!event.shiftKey && (document.activeElement === last || document.activeElement === dialogRef.current)) { event.preventDefault(); first?.focus(); }
      }
    };
    document.addEventListener("keydown", handleKey, true);
    return () => document.removeEventListener("keydown", handleKey, true);
  }, []);
  useEffect(() => { dialogRef.current?.querySelector<HTMLElement>(".onboarding-content h2, [data-onboarding-ticket-form]")?.focus(); }, [panel]);
  const advance = (step: OnboardingStep) => {
    const next = followingOnboardingStep(step);
    if (next === null) onClose(); else setPanel(next);
  };
  const complete = (step: OnboardingStep) => { onComplete(step); advance(step); };
  const skip = (step: OnboardingStep) => { onSkip(step); advance(step); };
  return <div className="onboarding-backdrop">
    <div ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby={titleId} tabIndex={-1}
      className="onboarding-dialog" data-onboarding-wizard data-onboarding-current={view.nextStep}>
      <header className="onboarding-header">
        <div><span className="onboarding-eyebrow">Pi Web</span><h1 id={titleId}>{t("onboarding.title")}</h1></div>
        <button type="button" onClick={onClose} disabled={ticketBusy} aria-label={t("onboarding.close")} className="onboarding-close" data-onboarding-close>×</button>
      </header>
      <div className="onboarding-layout">
        <nav className="onboarding-nav" aria-label={t("onboarding.progress")}>
          <p className="onboarding-step-count">{t("onboarding.stepOf", { current: String(currentIndex + 1), total: String(ONBOARDING_STEPS.length) })}</p>
          <ol data-onboarding-steps>{ONBOARDING_STEPS.map((step, index) => {
            const completeStep = view.steps[step] === "complete";
            return <li key={step} data-onboarding-step={step} data-onboarding-status={view.steps[step]}>
              <button type="button" onClick={() => setPanel(step)} disabled={ticketBusy || (!completeStep && step !== view.nextStep && step !== panel)} aria-current={panel === step ? "step" : undefined}>
                <span className="onboarding-step-number" aria-hidden="true">{completeStep ? "✓" : index + 1}</span>
                <span className="onboarding-step-label">{t(STEP_LABEL_KEYS[step])}<small data-onboarding-status-text={view.steps[step]}>{t(completeStep ? "onboarding.statusComplete" : "onboarding.statusIncomplete")}</small></span>
              </button>
            </li>;
          })}</ol>
          <p className="onboarding-resume-hint">{t("onboarding.description")}</p>
        </nav>
        {panel === "aihub" && <AihubStep connection={view.connection} onComplete={() => complete("aihub")} onSkip={() => skip("aihub")} />}
        {panel === "account" && <AccountStep onComplete={() => complete("account")} onSkip={() => skip("account")} onOpenAccount={onOpenAccount} onOpenModels={onOpenModels} />}
        {panel === "workspace" && <WorkspaceStep cwd={cwd} onComplete={() => complete("workspace")} onSkip={() => skip("workspace")} onWorkspaceChange={onWorkspaceChange} />}
        {panel === "ticket" && <TicketStep onComplete={() => complete("ticket")} onSkip={() => skip("ticket")} onBusyChange={updateTicketBusy} />}
      </div>
    </div>
  </div>;
}
