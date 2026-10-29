import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import { href } from '../router'
import type { MemoryList, MemoryRow } from '../types'
import NotFound from '../components/NotFound'
import TypeDot from '../components/TypeDot'
import RelativeTime from '../components/RelativeTime'

// MemoriesPage is the project's memory module — the #103 read face at
// the console prefix: an empty query browses (date-grouped zebra rows
// with tag chips, a forward Next while the face returns a cursor); a
// keyword renders the one top-50 page with the refine hint when it came
// back full, and a zero-hit keyword answers a guiding empty state that
// suggests refining or clearing the query. Type and tag filters ride
// the URL.
export default function MemoriesPage({ project }: { project: string }) {
  const params = new URLSearchParams(window.location.search)
  const q = params.get('q') ?? ''
  const type = params.get('type') ?? ''
  const tags = params.get('tags') ?? ''
  const after = params.get('after') ?? ''
  const [list, setList] = useState<MemoryList | null>(null)
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    setList(null)
    setMissing(false)
    apiGet<MemoryList>('/web/api/memories', { project, q, type, tags, after })
      .then(setList)
      .catch((err) => {
        if (err instanceof ApiError && err.status === 404) setMissing(true)
      })
  }, [project, q, type, tags, after])

  if (missing) return <NotFound />

  const listUrl = (extra: Record<string, string>) => {
    const p = new URLSearchParams({ ...(q ? { q } : {}), ...(type ? { type } : {}), ...(tags ? { tags } : {}) })
    for (const [k, v] of Object.entries(extra)) p.set(k, v)
    const qs = p.toString()
    return href(project, 'memories') + (qs ? `?${qs}` : '')
  }

  const kinds = new Set<string>()
  for (const row of list?.items ?? []) kinds.add(row.type.split('.')[0])

  return (
    <div data-testid="memories" className="max-w-prose">
      <div className="flex flex-wrap items-center gap-2 mb-4">
        <h1 className="text-xl font-semibold mr-2">Memories · {project}</h1>
        <a
          href={listUrl({ type: '' })}
          className={`no-underline text-xs border rounded-full px-2 py-0.5 ${
            type === '' ? 'bg-tint text-fg border-fg' : 'text-muted border-line hover:text-fg'
          }`}
        >
          all
        </a>
        {[...kinds].map((kind) => (
          <a
            key={kind}
            href={listUrl({ type: kind + '.*' })}
            className={`no-underline text-xs border rounded-full px-2 py-0.5 ${
              type === kind + '.*' ? 'bg-tint text-fg border-fg' : 'text-muted border-line hover:text-fg'
            }`}
          >
            {kind}
          </a>
        ))}
        <form action={href(project, 'memories')} method="get" className="flex gap-2 ml-2">
          <input type="hidden" name="type" value={type} />
          <input
            data-testid="memories-search"
            type="search"
            name="q"
            defaultValue={q}
            placeholder="Search memories…"
            className="bg-bg text-fg border border-line rounded px-2 py-1 w-64"
          />
          <button type="submit" className="border border-line rounded px-2 py-1 text-sm bg-tint">
            Search
          </button>
        </form>
      </div>

      {q && list?.refine_hint ? (
        <p data-testid="refine-hint" className="text-muted text-sm mb-2">
          Many matches — showing the top 50. Refine the query to narrow the results.
        </p>
      ) : null}

      {list === null ? (
        <p className="text-muted">Loading…</p>
      ) : list.items.length === 0 ? (
        q ? (
          <p data-testid="empty-search" className="text-muted">
            No memories match this query — refine it, or clear it to browse the newest memories.
          </p>
        ) : (
          <p className="text-muted">No memories.</p>
        )
      ) : (
        <DateGroupedRows project={project} rows={list.items} />
      )}

      {list?.next_cursor ? (
        <p className="mt-4">
          <a data-testid="memories-next" className="text-accent" href={listUrl({ after: list.next_cursor })}>
            Next page
          </a>
        </p>
      ) : null}
    </div>
  )
}

// DateGroupedRows renders the rows date-grouped (Today / Yesterday /
// Earlier) over the zebra table.
function DateGroupedRows({ project, rows }: { project: string; rows: MemoryRow[] }) {
  const groups: { label: string; rows: MemoryRow[] }[] = []
  for (const row of rows) {
    const label = dayLabel(row.updated_at)
    const last = groups[groups.length - 1]
    if (last && last.label === label) last.rows.push(row)
    else groups.push({ label, rows: [row] })
  }
  return (
    <div>
      {groups.map((g) => (
        <section key={g.label}>
          <h2 className="text-sm font-semibold text-muted mt-4 mb-1">{g.label}</h2>
          <table className="zebra w-full text-sm border-collapse">
            <tbody>
              {g.rows.map((row) => (
                <tr key={row.id} data-testid="memory-row">
                  <td className="py-1 pr-2">
                    <TypeDot type={row.type} />
                  </td>
                  <td className="py-1 pr-2">
                    <a className="text-accent" href={href(project, 'memories', row.id)}>
                      {row.title}
                    </a>
                    <span className="text-muted ml-2">{row.type}</span>
                  </td>
                  <td className="py-1 pr-2">
                    {row.tags.map((tag) => (
                      <span key={tag} className="tag-chip">
                        {tag}
                      </span>
                    ))}
                  </td>
                  <td className="py-1 pr-2 text-muted">{row.visibility}</td>
                  <td className="py-1 text-muted text-right">
                    <RelativeTime at={row.updated_at} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ))}
    </div>
  )
}

// dayLabel answers the row's date-group label (an unparseable timestamp
// groups as Earlier).
function dayLabel(at: string): string {
  const t = new Date(at)
  if (Number.isNaN(t.getTime())) return 'Earlier'
  const day = new Date()
  const one = 86_400_000
  if (t.toDateString() === day.toDateString()) return 'Today'
  if (t.toDateString() === new Date(day.getTime() - one).toDateString()) return 'Yesterday'
  return 'Earlier'
}
