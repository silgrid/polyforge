"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import type { PolyforgeJob, PolyforgeStep } from "@/lib/polyforge-status";
import type {
  PolyforgeIntegrationBatch,
  PolyforgeIntegrationLanded,
  PolyforgeIntegrationRead,
} from "@/lib/polyforge-integration";
import { conversationState } from "@/lib/polyforge-conversation-pure";
import { PolyforgeJobComposer } from "./PolyforgeJobComposer";
import { PolyforgeRefreshButton } from "./PolyforgeSearchBar";

/**
 * Work › 待集成: per mapped project, the integrate batches
 * (steps in declared order, done/declared progress, the current step; a
 * pending question answered through the job card's own composer —
 * `PolyforgeJobComposer` → `POST /api/polyforge/answer`) and the landed items
 * waiting for a green batch (slug, PR links, short merge commits, the waived
 * mark; a blamed item's batch, fix tickets and red rounds), or the project's
 * read error — all from `GET /api/polyforge/integration`.
 *
 * Reads: one when the sub-tab opens (this component mounts), one per ↻, one
 * after an accepted answer, and one when the status snapshot's job row of a
 * listed batch changes status (the `jobs` the dock's existing status poll
 * already holds — no timer here, and the poll itself never reads this route).
 * A failed read shows its error in this view only; the rest of the panel is
 * untouched.
 */

type IntegrationResponse = Partial<PolyforgeIntegrationRead>;

const RED = "#dc2626";
const AMBER = "#d97706";
const sectionHead = { padding: "4px 12px", fontSize: 11, color: "var(--text-dim)" } as const;
const dim = { fontSize: 11, color: "var(--text-dim)" } as const;
const mono = { fontFamily: "var(--font-mono)" } as const;

/** A step's glyph by its status. */
function stepGlyph(status: string): string {
  if (status === "done") return "✓";
  if (status === "running" || status === "in_progress") return "▶";
  if (status === "failed" || status === "blocked") return "✗";
  return "○";
}

/** The batch's steps in declared order (an undeclared started step after
 *  them; a declared step the watch has not reached is pending), and its
 *  progress: done steps over the declared count. Pure. */
export function integrateBatchSteps(batch: PolyforgeIntegrationBatch): { steps: PolyforgeStep[]; done: number; total: number } {
  const watched = batch.steps ?? [];
  const declared = batch.declared_steps ?? [];
  const byName = new Map(watched.map((step) => [step.name, step]));
  const steps = [
    ...declared.map((entry) => byName.get(entry.id) ?? { name: entry.id, status: "pending" }),
    ...watched.filter((step) => !declared.some((entry) => entry.id === step.name)),
  ];
  return { steps, done: steps.filter((step) => step.status === "done").length, total: declared.length || steps.length };
}

/** The job rows' status per listed batch id, as one comparable string (the
 *  re-read key); null without a status snapshot. Pure. */
export function integrateBatchJobKey(batchIds: readonly string[], jobs: readonly PolyforgeJob[] | null): string | null {
  if (!jobs) return null;
  return batchIds.map((id) => `${id}=${jobs.find((job) => job.wi === id)?.status ?? "-"}`).join(",");
}

export function PolyforgeIntegrate({
  jobs,
  workflowStatusLabel,
}: {
  /** The dock's status snapshot jobs (null: no snapshot). */
  jobs: PolyforgeJob[] | null;
  workflowStatusLabel: (status: string) => string;
}) {
  const { t } = useI18n();
  const [data, setData] = useState<IntegrationResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  // Only the newest read writes the view.
  const seqRef = useRef(0);
  const read = useCallback(() => {
    const seq = ++seqRef.current;
    setRefreshing(true);
    fetch("/api/polyforge/integration", { cache: "no-store" })
      .then(async (response) => {
        const body = await response.json().catch(() => ({})) as IntegrationResponse;
        if (seq !== seqRef.current) return;
        setRefreshing(false);
        if (!response.ok) {
          setError(body.error ?? t("polyforge.integrate.error"));
          return;
        }
        setData(body);
        setError(body.error ?? null);
      })
      .catch(() => {
        if (seq !== seqRef.current) return;
        setRefreshing(false);
        setError(t("polyforge.integrate.error"));
      });
  }, [t]);
  // One read per opening (the ref survives StrictMode's dev double invoke).
  const openedRef = useRef(false);
  useEffect(() => {
    if (openedRef.current) return;
    openedRef.current = true;
    read();
  }, [read]);
  // A listed batch's job row changed status: read once. A change of the
  // listed batches themselves (a read's answer) or a missing snapshot on
  // either side is no change.
  const batchIds = (data?.projects ?? []).flatMap((project) => project.batches.map((batch) => batch.id));
  const idsKey = batchIds.join(",");
  const jobKey = integrateBatchJobKey(batchIds, jobs);
  const seenRef = useRef<{ ids: string; jobs: string | null } | null>(null);
  useEffect(() => {
    const previous = seenRef.current;
    seenRef.current = { ids: idsKey, jobs: jobKey };
    if (!previous || previous.ids !== idsKey || previous.jobs === null || jobKey === null || previous.jobs === jobKey) return;
    read();
  }, [idsKey, jobKey, read]);

  const projects = data?.projects ?? [];
  return (
    <div role="region" aria-label={t("polyforge.work.integrateTab")} style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: "var(--text)" }}>{t("polyforge.work.integrateTab")}</span>
        <span style={{ marginLeft: "auto" }}>
          <PolyforgeRefreshButton refreshing={refreshing} onClick={read} data-pf-integrate-refresh="" />
        </span>
      </div>
      <div style={{ flex: 1, minHeight: 0, overflowY: "auto", display: "flex", flexDirection: "column" }}>
        {error && (
          <div data-pf-integrate-error="" style={{ padding: "8px 12px", display: "flex", flexDirection: "column", gap: 4, fontSize: 12 }}>
            <span style={{ color: RED }}>{t("polyforge.integrate.error")}</span>
            <span style={{ color: "var(--text-dim)", whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{error}</span>
          </div>
        )}
        {!data && !error && <div style={{ padding: 16, ...dim, fontSize: 12 }}>{t("polyforge.integrate.loading")}</div>}
        {data?.pfPresent === false && <div style={{ padding: 16, ...dim, fontSize: 12 }}>{t("polyforge.integrate.notInstalled")}</div>}
        {data?.pfPresent && !data.error && projects.length === 0 && <div style={{ padding: 16, ...dim, fontSize: 12 }}>{t("polyforge.integrate.noProjects")}</div>}
        {projects.map((project) => (
          <section key={project.project} data-pf-integrate-project={project.project} style={{ display: "flex", flexDirection: "column", borderBottom: "1px solid var(--border)" }}>
            <div style={{ padding: "6px 12px", fontSize: 12, fontWeight: 600, color: "var(--text)", ...mono }}>{project.project}</div>
            {project.error && (
              <div data-pf-integrate-project-error="" style={{ padding: "0 12px 6px", fontSize: 11, color: RED, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{project.error}</div>
            )}
            {project.batches.length > 0 && (
              <>
                <div style={sectionHead}>{t("polyforge.integrate.batches", { count: project.batches.length })}</div>
                {project.batches.map((batch) => (
                  <IntegrateBatchRow key={batch.id} batch={batch} job={jobs?.find((job) => job.wi === batch.id)} daemonPresent={jobs !== null}
                    workflowStatusLabel={workflowStatusLabel} onAnswered={read} />
                ))}
              </>
            )}
            {!project.error && (
              <>
                <div style={sectionHead}>{t("polyforge.integrate.landed", { count: project.landed.length })}</div>
                {project.landed.length === 0 && <div style={{ padding: "0 12px 6px", ...dim }}>{t("polyforge.integrate.none")}</div>}
              </>
            )}
            {project.landed.map((item) => <IntegrateLandedRow key={item.id} item={item} />)}
          </section>
        ))}
      </div>
    </div>
  );
}

function IntegrateLandedRow({ item }: { item: PolyforgeIntegrationLanded }) {
  const { t } = useI18n();
  return (
    <div data-pf-integrate-landed={item.id} style={{ display: "flex", flexDirection: "column", gap: 2, padding: "6px 12px", borderTop: "1px solid var(--border)", fontSize: 12 }}>
      <span style={{ display: "flex", flexWrap: "wrap", alignItems: "baseline", gap: 6, minWidth: 0 }}>
        <span style={{ ...mono, color: "var(--text)" }}>{item.slug}</span>
        {item.waived && (
          <span data-pf-integrate-waived="" title={t("polyforge.integrate.waivedTitle")}
            style={{ fontSize: 10, padding: "0 4px", borderRadius: 3, border: "1px solid currentColor", color: AMBER }}>
            {t("polyforge.integrate.waived")}
          </span>
        )}
      </span>
      {item.repos.map((repo) => (
        <span key={repo.repo} style={{ display: "flex", flexWrap: "wrap", gap: 8, fontSize: 11, color: "var(--text-muted)" }}>
          <span>{repo.repo}</span>
          {repo.pr && (
            <a href={repo.pr} target="_blank" rel="noreferrer" title={repo.pr} style={{ color: "var(--accent)", overflowWrap: "anywhere" }}>
              {repo.pr.replace(/^https?:\/\/[^/]+\//i, "")}
            </a>
          )}
          {repo.merge_commit && <span data-pf-integrate-merge="" title={repo.merge_commit} style={mono}>{repo.merge_commit.slice(0, 7)}</span>}
        </span>
      ))}
      {(item.batch || item.fix_tickets || item.red_rounds !== undefined) && (
        <span style={{ display: "flex", flexWrap: "wrap", gap: 8, ...dim }}>
          {item.batch && <span>{t("polyforge.integrate.batch", { id: item.batch })}</span>}
          {item.fix_tickets && item.fix_tickets.length > 0 && <span data-pf-integrate-fix="">{t("polyforge.integrate.fixTickets", { list: item.fix_tickets.join(", ") })}</span>}
          {item.red_rounds !== undefined && <span data-pf-integrate-red-rounds="" style={{ color: RED }}>{t("polyforge.integrate.redRounds", { count: item.red_rounds })}</span>}
        </span>
      )}
    </div>
  );
}

function IntegrateBatchRow({
  batch,
  job,
  daemonPresent,
  workflowStatusLabel,
  onAnswered,
}: {
  batch: PolyforgeIntegrationBatch;
  /** The batch's job row in the status snapshot, if any. */
  job: PolyforgeJob | undefined;
  daemonPresent: boolean;
  workflowStatusLabel: (status: string) => string;
  onAnswered: () => void;
}) {
  const { t } = useI18n();
  const status = batch.workflowStatus ?? batch.status;
  const { steps, done, total } = integrateBatchSteps(batch);
  return (
    <div data-pf-integrate-batch={batch.id} style={{ display: "flex", flexDirection: "column", gap: 4, padding: "6px 12px", borderTop: "1px solid var(--border)", fontSize: 12 }}>
      <span style={{ display: "flex", flexWrap: "wrap", alignItems: "baseline", gap: 6 }}>
        <span style={{ ...mono, color: "var(--text)" }}>{batch.slug}</span>
        {status && <span style={{ fontSize: 11, color: status === "running" ? "var(--accent)" : "var(--text-muted)" }}>{workflowStatusLabel(status)}</span>}
        {total > 0 && (
          <span data-pf-integrate-progress="" title={t("polyforge.integrate.progressTitle")} style={{ marginLeft: "auto", fontWeight: 600, color: "var(--text)" }}>{`${done}/${total}`}</span>
        )}
      </span>
      {steps.length > 0 && (
        <span style={{ display: "flex", flexWrap: "wrap", gap: 8, fontSize: 11 }}>
          {steps.map((step) => (
            <span key={step.name} data-pf-integrate-step={step.name} data-status={step.status} title={step.status}
              style={{ color: step.name === batch.currentStep ? "var(--text)" : "var(--text-muted)", fontWeight: step.name === batch.currentStep ? 600 : 400, whiteSpace: "nowrap" }}>
              {stepGlyph(step.status)} {step.name}
            </span>
          ))}
        </span>
      )}
      {batch.currentStep && (
        <span style={dim}>
          {t("polyforge.integrate.currentStep")}{" "}
          <span data-pf-integrate-current="" style={{ ...mono, color: "var(--text)" }}>{batch.currentStep}</span>
        </span>
      )}
      {batch.error && <span style={{ fontSize: 11, color: RED, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{batch.error}</span>}
      {batch.pendingQuestion && (
        // The job card's own composer: the question and its one input box,
        // `POST /api/polyforge/answer`; an accepted answer reads the view again.
        <PolyforgeJobComposer
          wi={batch.id}
          profile={job?.profile}
          state={conversationState({ jobStatus: job?.status, workflowStatus: batch.workflowStatus, currentStep: batch.currentStep, blocked: batch.blocked,
            steps: batch.steps, daemonPresent })}
          question={batch.pendingQuestion}
          blocked={batch.blocked}
          onSent={onAnswered}
        />
      )}
    </div>
  );
}
