"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import {
  POLYFORGE_INSTALL_REPO,
  polyforgeInstallRelevant,
  polyforgeVersionFlags,
  type PolyforgeInstallRun,
  type PolyforgeInstallVersions,
} from "@/lib/polyforge-install-pure";
import { ConfigButton, ConfigDetailGrid, ConfigDetailGridRow } from "./SettingsUi";

/**
 * Settings › Polyforge, Host pane: install or update pf from the release
 * channel. It shows the installed, latest and running
 * daemon versions, the running jobs and Pi Web's own version, naming every
 * disagreement in words; one button runs the channel's tools/install.sh
 * through `POST /api/polyforge/install`, and the output follows line by
 * line from `GET /api/polyforge/install/events` until a conclusion. The run
 * belongs to the server: closing the page closes only the stream, and the
 * page reopened replays the run's output and result. While no cli.toml
 * carries an api_key (`needsConfig`: install.sh's has_config rule, the one
 * the route enforces) the run writes cli.toml, so the card asks for the
 * aihub host and the api key — on a first install and on an update of an
 * installed pf alike. The key input is write-only — cleared
 * on every submit, never read back — and the route hands it to install.sh
 * on stdin only. When install.sh refuses an update because jobs run, the
 * refusal and its jobs are shown, and a forced update (PF_INSTALL_FORCE=1)
 * takes a second, separate confirmation that states what it ends. The line
 * naming tools/install.sh shows only while an install or update is relevant
 * (polyforgeInstallRelevant): a healthy, current pf shows none of it.
 */
export function PolyforgeInstallCard({ active, refreshKey, onFinished }: {
  active: boolean;
  /** Bumped by the page's 「重新检测」: a fresh read past the version caches. */
  refreshKey: number;
  onFinished: () => void;
}) {
  const { t } = useI18n();
  const [versions, setVersions] = useState<PolyforgeInstallVersions | null>(null);
  const [readError, setReadError] = useState<string | null>(null);
  const [run, setRun] = useState<PolyforgeInstallRun | null>(null);
  const [lines, setLines] = useState<string[]>([]);
  const [host, setHost] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [problem, setProblem] = useState<string | null>(null);
  const [posting, setPosting] = useState(false);
  const [confirmingForce, setConfirmingForce] = useState(false);
  // Why this page stopped following a run it showed as running (the stream
  // refused: signed out, or the server no longer knows the run).
  const [streamError, setStreamError] = useState<string | null>(null);
  const sourceRef = useRef<EventSource | null>(null);
  const cursorRef = useRef(0);
  const shownRunRef = useRef<string | null>(null);
  const onFinishedRef = useRef(onFinished);
  useEffect(() => {
    onFinishedRef.current = onFinished;
  }, [onFinished]);

  const read = useCallback(async (force: boolean): Promise<PolyforgeInstallRun | null> => {
    try {
      const response = await fetch(`/api/polyforge/install${force ? "?force=1" : ""}`);
      const body = await response.json() as { versions?: PolyforgeInstallVersions | null; run?: PolyforgeInstallRun | null; error?: string };
      if (body.versions) {
        setVersions(body.versions);
        setReadError(null);
      } else {
        setReadError(body.error ?? t("polyforge.install.readFailed"));
      }
      return body.run ?? null;
    } catch {
      setReadError(t("polyforge.install.readFailed"));
      return null;
    }
  }, [t]);

  // Follow the run's output from the cursor; EventSource re-sends the last
  // line id on its own reconnects, and the cursor drops a replayed line.
  const follow = useCallback(() => {
    sourceRef.current?.close();
    setStreamError(null);
    const source = new EventSource(`/api/polyforge/install/events?after=${cursorRef.current}`);
    sourceRef.current = source;
    // A transient drop reconnects by itself (CONNECTING); a refused stream
    // (401, 404) is CLOSED for good: stop showing the run as running, say why.
    source.onerror = () => {
      if (source.readyState !== EventSource.CLOSED) return;
      if (sourceRef.current === source) sourceRef.current = null;
      setStreamError(t("polyforge.install.streamLost"));
      void read(false);
    };
    source.onmessage = (message) => {
      let event: { type?: string; n?: number; text?: string; run?: PolyforgeInstallRun };
      try { event = JSON.parse(message.data); } catch { return; }
      if (event.type === "line" && typeof event.n === "number" && typeof event.text === "string") {
        if (event.n <= cursorRef.current) return;
        cursorRef.current = event.n;
        const text = event.text;
        setLines((current) => [...current, text]);
      } else if (event.type === "end" && event.run) {
        source.close();
        if (sourceRef.current === source) sourceRef.current = null;
        setRun(event.run);
        void read(true);
        onFinishedRef.current();
      }
    };
  }, [read, t]);

  /** Show this run: a new one restarts the output from its first line. */
  const attach = useCallback((current: PolyforgeInstallRun) => {
    setRun(current);
    if (shownRunRef.current !== current.id) {
      shownRunRef.current = current.id;
      cursorRef.current = 0;
      setLines([]);
      follow();
    } else if (current.state === "running" && !sourceRef.current) {
      follow();
    }
  }, [follow]);

  const sync = useCallback(async (force: boolean) => {
    const current = await read(force);
    if (current) attach(current);
  }, [read, attach]);

  // Every open of the section reads once (no timer; guarded against
  // StrictMode's simulated remount like the detection); the stream does the rest.
  const ranForActive = useRef(false);
  useEffect(() => {
    if (!active) {
      ranForActive.current = false;
      return;
    }
    if (ranForActive.current) return;
    ranForActive.current = true;
    void sync(false);
  }, [active, sync]);
  const seenRefresh = useRef(refreshKey);
  useEffect(() => {
    if (seenRefresh.current === refreshKey) return;
    seenRefresh.current = refreshKey;
    void sync(true);
  }, [refreshKey, sync]);
  useEffect(() => () => { sourceRef.current?.close(); }, []);

  const start = async (force: boolean) => {
    setProblem(null);
    // the key never stays in the form, whatever the outcome — a refused
    // check included
    const typedKey = apiKey;
    setApiKey("");
    const body: { force?: boolean; refusedRun?: string; host?: string; apiKey?: string } = {};
    if (force) {
      // a force answers this refusal only (the route checks it is the last
      // run and still fresh)
      body.force = true;
      body.refusedRun = run?.id;
    } else if (versions?.needsConfig) {
      const trimmed = host.trim();
      if (!trimmed) {
        setProblem(t("polyforge.install.needHost"));
        return;
      }
      if (typedKey.length < 8) {
        setProblem(t("polyforge.install.needKey"));
        return;
      }
      body.host = trimmed;
      body.apiKey = typedKey;
    }
    setConfirmingForce(false);
    setPosting(true);
    try {
      const response = await fetch("/api/polyforge/install", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const answer = await response.json() as { run?: PolyforgeInstallRun; error?: string };
      if (!response.ok || !answer.run) {
        setProblem(answer.error ?? t("polyforge.install.startFailed"));
        // another page's run, or a state this page has not seen: show it
        if (response.status === 409) void sync(false);
        return;
      }
      attach(answer.run);
    } catch {
      setProblem(t("polyforge.install.startFailed"));
    } finally {
      setPosting(false);
    }
  };

  // The confirmation shows the jobs running NOW, read afresh — never only the
  // ones the refusal named.
  const askForce = async () => {
    setProblem(null);
    await read(true);
    setConfirmingForce(true);
  };

  return (
    <PolyforgeInstallView
      versions={versions}
      readError={readError}
      streamError={streamError}
      run={run}
      lines={lines}
      host={host}
      apiKey={apiKey}
      problem={problem}
      posting={posting}
      confirmingForce={confirmingForce}
      onHost={setHost}
      onApiKey={setApiKey}
      onStart={() => void start(false)}
      onAskForce={() => void askForce()}
      onForce={() => void start(true)}
      onCancelForce={() => setConfirmingForce(false)}
    />
  );
}

export interface PolyforgeInstallViewProps {
  versions: PolyforgeInstallVersions | null;
  readError: string | null;
  streamError: string | null;
  run: PolyforgeInstallRun | null;
  lines: string[];
  host: string;
  apiKey: string;
  problem: string | null;
  posting: boolean;
  confirmingForce: boolean;
  onHost: (value: string) => void;
  onApiKey: (value: string) => void;
  onStart: () => void;
  onAskForce: () => void;
  onForce: () => void;
  onCancelForce: () => void;
}

/** The card's face, from its state alone (no effect, no fetch). */
export function PolyforgeInstallView({
  versions, readError, streamError, run, lines, host, apiKey, problem, posting, confirmingForce,
  onHost, onApiKey, onStart, onAskForce, onForce, onCancelForce,
}: PolyforgeInstallViewProps) {
  const { t } = useI18n();
  const unknown = t("polyforge.install.unknown");
  const running = run?.state === "running" && !streamError;
  const flags = versions ? polyforgeVersionFlags(versions) : null;
  const installed = versions?.installed ?? unknown;
  // A run this page lost the stream of is no longer shown as running.
  const shownState = run?.state === "running" && streamError ? "lost" : run?.state;
  const conclusion = !run
    ? null
    : shownState === "lost"
      ? streamError
      : run.state === "running"
        ? t("polyforge.install.stateRunning")
        : run.state === "succeeded"
          ? t("polyforge.install.stateSucceeded")
          : run.state === "refused"
            ? t("polyforge.install.stateRefused", { reason: run.refusal?.reason || unknown })
            : t("polyforge.install.stateFailed", { error: run.error || unknown });

  return (
    <section className="polyforge-config-card polyforge-install-card" aria-label={t("polyforge.install.title")} data-pf-install>
      <p className="polyforge-config-card-title">{t("polyforge.install.title")}</p>
      {polyforgeInstallRelevant(versions, run) && (
        <p className="polyforge-config-card-hint" data-pf-install-hint>{t("polyforge.install.hint", { repo: versions?.repo ?? POLYFORGE_INSTALL_REPO })}</p>
      )}
      {readError && <p className="config-add-source-error" role="alert">{readError}</p>}

      {versions && (
        <ConfigDetailGrid>
          <ConfigDetailGridRow label={t("polyforge.install.installed")} tone="plain" mono>
            <span data-pf-install-version="installed">{versions.pfPresent ? installed : t("polyforge.install.none")}</span>
          </ConfigDetailGridRow>
          <ConfigDetailGridRow label={t("polyforge.install.latest", { repo: versions.repo })} tone="plain" mono>
            <span data-pf-install-version="latest">{versions.latest ?? `${unknown}${versions.latestError ? `: ${versions.latestError}` : ""}`}</span>
          </ConfigDetailGridRow>
          <ConfigDetailGridRow label={t("polyforge.install.running")} tone="plain" mono>
            <span data-pf-install-version="running">
              {versions.daemonReachable === null
                ? t("polyforge.install.notApplicable")
                : versions.daemonReachable
                  ? versions.running ?? unknown
                  : `${t("polyforge.install.daemonDown")}${versions.daemonError ? `: ${versions.daemonError}` : ""}`}
            </span>
          </ConfigDetailGridRow>
          <ConfigDetailGridRow label={t("polyforge.install.jobs")} tone="plain">
            <span data-pf-install-jobs={versions.daemonReachable ? versions.jobs.length : undefined}>
              {versions.daemonReachable
                ? `${versions.jobs.length}${versions.jobs.length ? ` · ${versions.jobs.map((job) => `${job.slug} (${job.status})`).join(", ")}` : ""}`
                : t("polyforge.install.notApplicable")}
            </span>
          </ConfigDetailGridRow>
          <ConfigDetailGridRow label={t("polyforge.install.piWeb")} tone="plain" mono>
            <span data-pf-install-version="pi-web">{versions.piWeb || unknown}</span>
          </ConfigDetailGridRow>
        </ConfigDetailGrid>
      )}
      {versions && flags?.updateAvailable && (
        <p className="polyforge-install-flag" data-pf-install-flag="update">
          {t("polyforge.install.flagUpdate", { latest: versions.latest ?? "", installed })}
        </p>
      )}
      {versions && flags?.daemonStale && (
        <p className="polyforge-install-flag is-warning" data-pf-install-flag="daemon-stale">
          {t("polyforge.install.flagDaemonStale", { running: versions.running ?? "", installed })}
        </p>
      )}
      {versions && flags?.piWebOutOfSync && (
        <p className="polyforge-install-flag is-warning" data-pf-install-flag="pi-web">
          {t("polyforge.install.flagPiWeb", { piWeb: versions.piWeb, installed })}
        </p>
      )}
      {versions?.pfPresent && versions.jobs.length > 0 && (
        <p className="polyforge-install-flag is-warning" data-pf-install-flag="jobs">
          {t("polyforge.install.flagJobs", { count: versions.jobs.length })}
        </p>
      )}

      {versions?.needsConfig && !running && (
        <div className="polyforge-install-credentials" data-pf-install-credentials>
          <p className="polyforge-config-card-hint">{t("polyforge.install.firstInstallHint")}</p>
          <input aria-label={t("polyforge.install.host")} placeholder={t("polyforge.install.host")} value={host} onChange={(event) => onHost(event.target.value)} />
          <input type="password" autoComplete="off" aria-label={t("polyforge.install.apiKey")} placeholder={t("polyforge.install.apiKey")} value={apiKey} onChange={(event) => onApiKey(event.target.value)} />
        </div>
      )}
      <div className="polyforge-install-actions">
        <ConfigButton variant="primary" disabled={!versions || running || posting} onClick={onStart} data-pf-install-start>
          {versions?.pfPresent ? t("polyforge.install.update") : t("polyforge.install.install")}
        </ConfigButton>
        {running && <span className="config-scope-switch-reason">{t("polyforge.install.busyReason")}</span>}
      </div>
      {problem && <p className="config-add-source-error" role="status">{problem}</p>}

      {run && (
        <div className="polyforge-install-run" data-pf-install-run={run.state}>
          <p role="status" className={`polyforge-install-conclusion is-${shownState}`} data-pf-install-conclusion={shownState}>{conclusion}</p>
          {run.state === "refused" && (
            <div className="polyforge-install-refusal" data-pf-install-refusal>
              {run.refusal && run.refusal.jobs.length > 0 && (
                <>
                  <p className="polyforge-config-card-hint">{t("polyforge.install.refusedJobs")}</p>
                  <ul className="polyforge-install-jobs">
                    {run.refusal.jobs.map((job) => <li key={job}>{job}</li>)}
                  </ul>
                </>
              )}
              {confirmingForce ? (
                <div className="polyforge-install-force" role="alertdialog" aria-label={t("polyforge.install.forceTitle")} data-pf-install-force-confirm>
                  <p className="polyforge-install-force-title">{t("polyforge.install.forceTitle")}</p>
                  <p className="polyforge-config-card-hint">{t("polyforge.install.forceConsequence")}</p>
                  {/* the jobs running NOW (re-read when the confirmation opened) */}
                  <div data-pf-install-force-jobs={versions?.daemonReachable ? versions.jobs.length : "unknown"}>
                    {!versions?.daemonReachable ? (
                      <p className="polyforge-config-card-hint">{t("polyforge.install.forceJobsUnknown")}</p>
                    ) : versions.jobs.length === 0 ? (
                      <p className="polyforge-config-card-hint">{t("polyforge.install.forceNoJobsNow")}</p>
                    ) : (
                      <>
                        <p className="polyforge-config-card-hint">{t("polyforge.install.forceJobsNow")}</p>
                        <ul className="polyforge-install-jobs">
                          {versions.jobs.map((job) => <li key={`${job.slug} ${job.status}`}>{`${job.slug} (${job.status})`}</li>)}
                        </ul>
                      </>
                    )}
                  </div>
                  <div className="polyforge-install-actions">
                    <ConfigButton size="small" variant="danger" disabled={posting} onClick={onForce} data-pf-install-force-go>
                      {t("polyforge.install.forceConfirm")}
                    </ConfigButton>
                    <ConfigButton size="small" onClick={onCancelForce}>{t("polyforge.install.cancel")}</ConfigButton>
                  </div>
                </div>
              ) : (
                <ConfigButton size="small" variant="danger" disabled={posting} onClick={onAskForce} data-pf-install-force>
                  {t("polyforge.install.force")}
                </ConfigButton>
              )}
            </div>
          )}
          <pre className="polyforge-install-output" role="log" aria-label={t("polyforge.install.output")} aria-live="polite" data-pf-install-output>
            {lines.join("\n")}
          </pre>
        </div>
      )}
    </section>
  );
}
