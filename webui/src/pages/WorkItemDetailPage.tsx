import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { apiGet, ApiError } from '../api'
import type { WorkItemDetail } from '../types'
import NotFound from '../components/NotFound'
import StatusPill from '../components/StatusPill'
import SanitizedHtml from '../components/SanitizedHtml'
import DepList from '../components/DepList'
import RelativeTime from '../components/RelativeTime'
import { Card, Muted, SectionHeading } from '../components/ui'

// WorkItemDetailPage renders one work item: the slug over the goal as
// the page's one h1, the metadata card, the server-sanitized content
// card, both dependency directions and the aihub event timeline card (labels and payloads render
// as text — React escapes them).
export default function WorkItemDetailPage({ project, id }: { project: string; id: string }) {
  const [detail, setDetail] = useState<WorkItemDetail | null>(null)
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    setDetail(null)
    setMissing(false)
    apiGet<WorkItemDetail>('/web/api/work-items/' + encodeURIComponent(id), { project })
      .then(setDetail)
      .catch((err) => {
        if (err instanceof ApiError && err.status === 404) setMissing(true)
      })
  }, [project, id])

  if (missing) return <NotFound />
  if (detail === null) return <Muted>Loading…</Muted>

  return (
    <article className="flex max-w-3xl flex-col gap-5" data-testid="work-item-detail">
      <div>
        <p className="mb-1 font-mono text-caption text-muted">{detail.slug}</p>
        <h1 className="mb-2 text-title font-semibold tracking-tight [overflow-wrap:anywhere]">{detail.goal}</h1>
        <p className="flex flex-wrap items-center gap-2">
          <StatusPill status={detail.status} />
          <span className="text-caption text-muted">{detail.priority} priority</span>
        </p>
      </div>

      <Card className="px-5 py-4">
        <dl className="m-0 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-6 gap-y-1.5">
          <Meta label="Reporter">{detail.reporter}</Meta>
          {detail.assignee ? <Meta label="Assignee">{detail.assignee}</Meta> : null}
          {detail.milestone ? <Meta label="Milestone">{detail.milestone}</Meta> : null}
          {detail.bundle ? <Meta label="Bundle">{detail.bundle}</Meta> : null}
          {detail.labels.length > 0 ? (
            <Meta label="Labels">
              {detail.labels.map((label) => (
                <span key={label} className="tag-chip">
                  {label}
                </span>
              ))}
            </Meta>
          ) : null}
          <Meta label="Filed" muted>
            <RelativeTime at={detail.created_at} />
          </Meta>
          <Meta label="Updated" muted>
            <RelativeTime at={detail.updated_at} />
          </Meta>
          {detail.closed_at ? (
            <Meta label="Closed" muted>
              <RelativeTime at={detail.closed_at} />
            </Meta>
          ) : null}
        </dl>
      </Card>

      {detail.html ? (
        <Card className="px-5 py-4">
          <SanitizedHtml html={detail.html} />
        </Card>
      ) : null}

      <DepList title="Blocked by" edges={detail.blocked_by} />
      <DepList title="Blocks" edges={detail.blocking} />

      <section>
        <SectionHeading className="mb-2">Timeline</SectionHeading>
        <Card>
          <ol className="m-0 list-none p-0">
            {detail.events.map((event, i) => (
              <li key={i} className="border-b border-line px-4 py-2.5 last:border-b-0" data-testid="timeline-event">
                <div className="flex flex-wrap items-baseline gap-x-2">
                  <span className="tabular font-mono text-caption text-muted">
                    <RelativeTime at={event.created_at} />
                  </span>
                  <span className="font-medium">{event.type}</span>
                  {event.actor ? <span className="text-muted">· {event.actor}</span> : null}
                </div>
                <pre className="mb-0 mt-1 whitespace-pre-wrap break-all font-mono text-caption text-muted" data-testid="event-payload">
                  {JSON.stringify(event.payload)}
                </pre>
              </li>
            ))}
          </ol>
        </Card>
      </section>
    </article>
  )
}

// Meta is one metadata row: the muted label column and its value.
function Meta({ label, muted = false, children }: { label: string; muted?: boolean; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted">{label}</dt>
      <dd className={`m-0 min-w-0 [overflow-wrap:anywhere] ${muted ? 'text-muted' : ''}`}>{children}</dd>
    </>
  )
}
