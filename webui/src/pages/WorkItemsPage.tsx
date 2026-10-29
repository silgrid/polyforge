import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import { href } from '../router'
import type { WorkItemList } from '../types'
import NotFound from '../components/NotFound'
import StatusPill from '../components/StatusPill'
import RelativeTime from '../components/RelativeTime'

// The status filter row: All plus each api status.
const STATUSES = ['queued', 'running', 'paused', 'blocked', 'wrapped', 'failed', 'canceled']

// WorkItemsPage is the project's work-item list: the status filter and
// substring search ride the URL query (a filter change is navigation),
// the table is compact (number, title, status, priority, age), and
// Next pages through the opaque cursor.
export default function WorkItemsPage({ project }: { project: string }) {
  const params = new URLSearchParams(window.location.search)
  const status = params.get('status') ?? ''
  const q = params.get('q') ?? ''
  const after = params.get('after') ?? ''
  const [page, setPage] = useState<WorkItemList | null>(null)
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    setPage(null)
    setMissing(false)
    apiGet<WorkItemList>('/web/api/work-items', { project, status, q, after })
      .then(setPage)
      .catch((err) => {
        if (err instanceof ApiError && err.status === 404) setMissing(true)
      })
  }, [project, status, q, after])

  if (missing) return <NotFound />
  const listUrl = (extra: Record<string, string>) => {
    const p = new URLSearchParams({ ...(q ? { q } : {}), ...(status ? { status } : {}) })
    for (const [k, v] of Object.entries(extra)) p.set(k, v)
    const qs = p.toString()
    return href(project, 'work-items') + (qs ? `?${qs}` : '')
  }

  return (
    <div data-testid="work-items">
      <div className="flex flex-wrap items-center gap-2 mb-4">
        <h1 className="text-xl font-semibold mr-2">Work items · {project}</h1>
        {['', ...STATUSES].map((s) => (
          <a
            key={s || 'all'}
            href={listUrl(s ? { status: s } : { status: '' })}
            className={`no-underline text-xs border rounded-full px-2 py-0.5 ${
              status === s ? 'bg-tint text-fg border-fg' : 'text-muted border-line hover:text-fg'
            }`}
          >
            {s || 'all'}
          </a>
        ))}
        <form action={href(project, 'work-items')} method="get" className="flex gap-2 ml-2">
          <input type="hidden" name="status" value={status} />
          <input
            data-testid="work-items-search"
            type="search"
            name="q"
            defaultValue={q}
            placeholder="Search goal or content…"
            className="bg-bg text-fg border border-line rounded px-2 py-1 w-64"
          />
          <button type="submit" className="border border-line rounded px-2 py-1 text-sm bg-tint">
            Filter
          </button>
        </form>
      </div>
      {page === null ? (
        <p className="text-muted">Loading…</p>
      ) : page.items.length === 0 ? (
        <p className="text-muted">No work items.</p>
      ) : (
        <table className="zebra w-full max-w-prose text-sm border-collapse">
          <thead>
            <tr className="text-left text-muted">
              <th className="py-1 pr-2">#</th>
              <th className="py-1 pr-2">Title</th>
              <th className="py-1 pr-2">Status</th>
              <th className="py-1 pr-2">Priority</th>
              <th className="py-1">Age</th>
            </tr>
          </thead>
          <tbody>
            {page.items.map((it) => (
              <tr key={it.id} data-testid="work-item-row">
                <td className="py-1 pr-2 text-muted">{it.seq}</td>
                <td className="py-1 pr-2">
                  <a className="text-accent" href={href(project, 'work-items', it.id)}>
                    {it.goal}
                  </a>
                </td>
                <td className="py-1 pr-2">
                  <StatusPill status={it.status} />
                </td>
                <td className="py-1 pr-2 text-muted">{it.priority}</td>
                <td className="py-1 text-muted">
                  <RelativeTime at={it.created_at} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {page?.next_cursor ? (
        <p className="mt-4">
          <a
            data-testid="next-page"
            className="text-accent"
            href={listUrl({ after: page.next_cursor })}
          >
            Next page
          </a>
        </p>
      ) : null}
    </div>
  )
}
