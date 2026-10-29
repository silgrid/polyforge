import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import type { WorkItemDetail } from '../types'
import NotFound from '../components/NotFound'
import StatusPill from '../components/StatusPill'
import SanitizedHtml from '../components/SanitizedHtml'
import DepList from '../components/DepList'
import RelativeTime from '../components/RelativeTime'

// WorkItemDetailPage renders one work item: the goal as the page's one
// h1, the server-sanitized content, the metadata line, both dependency
// directions and the aihub event timeline (labels and payloads render
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
  if (detail === null) return <p className="text-muted">Loading…</p>

  return (
    <article className="max-w-prose" data-testid="work-item-detail">
      <p className="text-muted text-sm mb-1 font-mono">{detail.slug}</p>
      <h1 className="text-2xl font-semibold mb-2">{detail.goal}</h1>
      <p className="mb-4">
        <StatusPill status={detail.status} />
        <span className="text-muted text-sm ml-2">{detail.priority} priority</span>
      </p>
      <dl className="text-sm mb-6">
        <Meta label="Reporter" value={detail.reporter} />
        {detail.assignee ? <Meta label="Assignee" value={detail.assignee} /> : null}
        {detail.milestone ? <Meta label="Milestone" value={detail.milestone} /> : null}
        {detail.bundle ? <Meta label="Bundle" value={detail.bundle} /> : null}
        {detail.labels.length > 0 ? (
          <div className="mb-1">
            <dt className="inline text-muted mr-2">Labels:</dt>
            <dd className="inline">
              {detail.labels.map((label) => (
                <span key={label} className="inline-block border border-line rounded-full px-2 text-xs mr-1 bg-tint">
                  {label}
                </span>
              ))}
            </dd>
          </div>
        ) : null}
        <div className="mb-1">
          <dt className="inline text-muted mr-2">Filed:</dt>
          <dd className="inline text-muted">
            <RelativeTime at={detail.created_at} />
          </dd>
        </div>
        <div className="mb-1">
          <dt className="inline text-muted mr-2">Updated:</dt>
          <dd className="inline text-muted">
            <RelativeTime at={detail.updated_at} />
          </dd>
        </div>
        {detail.closed_at ? (
          <div className="mb-1">
            <dt className="inline text-muted mr-2">Closed:</dt>
            <dd className="inline text-muted">
              <RelativeTime at={detail.closed_at} />
            </dd>
          </div>
        ) : null}
      </dl>

      {detail.html ? <SanitizedHtml html={detail.html} /> : null}

      <DepList title="Blocked by" edges={detail.blocked_by} />
      <DepList title="Blocks" edges={detail.blocking} />

      <section className="mt-6">
        <h2 className="text-base font-semibold mb-2">Timeline</h2>
        <ol className="list-none p-0 m-0">
          {detail.events.map((event, i) => (
            <li key={i} className="py-2 border-b border-line text-sm" data-testid="timeline-event">
              <span className="text-muted font-mono text-xs">
                <RelativeTime at={event.created_at} />
              </span>
              <span className="mx-2 text-muted">·</span>
              <span className="font-medium">{event.type}</span>
              {event.actor ? (
                <>
                  <span className="mx-2 text-muted">·</span>
                  <span className="text-muted">{event.actor}</span>
                </>
              ) : null}
              <pre className="whitespace-pre-wrap font-mono text-xs text-muted mt-1 mb-0" data-testid="event-payload">
                {JSON.stringify(event.payload)}
              </pre>
            </li>
          ))}
        </ol>
      </section>
    </article>
  )
}

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div className="mb-1">
      <dt className="inline text-muted mr-2">{label}:</dt>
      <dd className="inline">{value}</dd>
    </div>
  )
}
