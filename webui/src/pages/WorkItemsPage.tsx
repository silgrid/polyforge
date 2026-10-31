import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import { href } from '../router'
import type { WorkItemList } from '../types'
import NotFound from '../components/NotFound'
import StatusPill from '../components/StatusPill'
import RelativeTime from '../components/RelativeTime'
import { ArrowRightIcon } from '../icons'
import { buttonClass, Chip, DataTable, EmptyState, inputClass, Muted, PageHeader } from '../components/ui'

// The status filter row: All plus each api status.
const STATUSES = ['queued', 'running', 'paused', 'blocked', 'wrapped', 'failed', 'canceled']

// WorkItemsPage is the project's work-item list: the status filter and
// substring search ride the URL query (a filter change is navigation),
// the table is dense (number, title, status, priority, age — the last
// two hide at the narrow width), and Next pages through the opaque
// cursor.
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
      <PageHeader title={`Work items · ${project}`} />
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-1.5">
          {['', ...STATUSES].map((s) => (
            <Chip key={s || 'all'} to={listUrl(s ? { status: s } : { status: '' })} active={status === s}>
              {s || 'all'}
            </Chip>
          ))}
        </div>
        <form action={href(project, 'work-items')} method="get" className="flex w-full min-w-0 gap-2 sm:w-auto">
          <input type="hidden" name="status" value={status} />
          <input
            data-testid="work-items-search"
            type="search"
            name="q"
            defaultValue={q}
            placeholder="Search goal or content…"
            className={`${inputClass} w-full sm:w-64`}
          />
          <button type="submit" className={buttonClass}>
            Filter
          </button>
        </form>
      </div>
      {page === null ? (
        <Muted>Loading…</Muted>
      ) : page.items.length === 0 ? (
        <EmptyState>No work items.</EmptyState>
      ) : (
        <DataTable
          head={
            <tr>
              <th className="w-px">#</th>
              <th>Title</th>
              <th className="w-px">Status</th>
              <th className="narrow-hidden w-px">Priority</th>
              <th className="narrow-hidden w-px">Age</th>
            </tr>
          }
        >
          {page.items.map((it) => (
            <tr key={it.id} data-testid="work-item-row">
              <td className="text-muted">{it.seq}</td>
              <td className="[overflow-wrap:anywhere]">
                <a className="font-medium text-fg hover:text-accent-fg" href={href(project, 'work-items', it.id)}>
                  {it.goal}
                </a>
              </td>
              <td>
                <StatusPill status={it.status} />
              </td>
              <td className="narrow-hidden text-muted">{it.priority}</td>
              <td className="narrow-hidden whitespace-nowrap text-muted">
                <RelativeTime at={it.created_at} />
              </td>
            </tr>
          ))}
        </DataTable>
      )}
      {page?.next_cursor ? (
        <p className="mt-4">
          <a data-testid="next-page" className="inline-flex items-center gap-1" href={listUrl({ after: page.next_cursor })}>
            Next page
            <ArrowRightIcon />
          </a>
        </p>
      ) : null}
    </div>
  )
}
