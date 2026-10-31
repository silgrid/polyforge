import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import { href } from '../router'
import type { MemoryList, MemoryRow } from '../types'
import NotFound from '../components/NotFound'
import TypeDot from '../components/TypeDot'
import RelativeTime from '../components/RelativeTime'
import { ArrowRightIcon } from '../icons'
import { buttonClass, Chip, DataTable, EmptyState, inputClass, Muted, PageHeader, SectionHeading } from '../components/ui'

// MemoriesPage is the project's memory module — the #103 read face at
// the console prefix: an empty query browses (date-grouped dense rows
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
    <div data-testid="memories" className="max-w-4xl">
      <PageHeader title={`Memories · ${project}`} />
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-1.5">
          <Chip to={listUrl({ type: '' })} active={type === ''}>
            all
          </Chip>
          {[...kinds].map((kind) => (
            <Chip key={kind} to={listUrl({ type: kind + '.*' })} active={type === kind + '.*'}>
              {kind}
            </Chip>
          ))}
        </div>
        <form action={href(project, 'memories')} method="get" className="flex w-full min-w-0 gap-2 sm:w-auto">
          <input type="hidden" name="type" value={type} />
          <input
            data-testid="memories-search"
            type="search"
            name="q"
            defaultValue={q}
            placeholder="Search memories…"
            className={`${inputClass} w-full sm:w-64`}
          />
          <button type="submit" className={buttonClass}>
            Search
          </button>
        </form>
      </div>

      {q && list?.refine_hint ? (
        <p data-testid="refine-hint" className="mb-3 text-caption text-muted">
          Many matches — showing the top 50. Refine the query to narrow the results.
        </p>
      ) : null}

      {list === null ? (
        <Muted>Loading…</Muted>
      ) : list.items.length === 0 ? (
        q ? (
          <EmptyState testid="empty-search">
            No memories match this query — refine it, or clear it to browse the newest memories.
          </EmptyState>
        ) : (
          <EmptyState>No memories.</EmptyState>
        )
      ) : (
        <DateGroupedRows project={project} rows={list.items} />
      )}

      {list?.next_cursor ? (
        <p className="mt-4">
          <a data-testid="memories-next" className="inline-flex items-center gap-1" href={listUrl({ after: list.next_cursor })}>
            Next page
            <ArrowRightIcon />
          </a>
        </p>
      ) : null}
    </div>
  )
}

// DateGroupedRows renders the rows date-grouped (Today / Yesterday /
// Earlier): a section heading over one dense table per group.
function DateGroupedRows({ project, rows }: { project: string; rows: MemoryRow[] }) {
  const groups: { label: string; rows: MemoryRow[] }[] = []
  for (const row of rows) {
    const label = dayLabel(row.updated_at)
    const last = groups[groups.length - 1]
    if (last && last.label === label) last.rows.push(row)
    else groups.push({ label, rows: [row] })
  }
  return (
    <div className="flex flex-col gap-6">
      {groups.map((g) => (
        <section key={g.label}>
          <SectionHeading className="mb-2">{g.label}</SectionHeading>
          <DataTable>
            {g.rows.map((row) => (
              <tr key={row.id} data-testid="memory-row">
                <td className="w-px">
                  <TypeDot type={row.type} />
                </td>
                <td className="[overflow-wrap:anywhere]">
                  <a className="font-medium text-fg hover:text-accent-fg" href={href(project, 'memories', row.id)}>
                    {row.title}
                  </a>
                  <span className="ml-2 text-caption text-muted">{row.type}</span>
                </td>
                <td>
                  {row.tags.map((tag) => (
                    <span key={tag} className="tag-chip">
                      {tag}
                    </span>
                  ))}
                </td>
                <td className="narrow-hidden w-px text-muted">{row.visibility}</td>
                <td className="narrow-hidden w-px whitespace-nowrap text-right text-muted">
                  <RelativeTime at={row.updated_at} />
                </td>
              </tr>
            ))}
          </DataTable>
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
