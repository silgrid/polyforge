import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import { href } from '../router'
import type { SearchResults } from '../types'
import NotFound from '../components/NotFound'
import StatusPill from '../components/StatusPill'
import TypeDot from '../components/TypeDot'
import { ArrowRightIcon } from '../icons'
import { Card, EmptyState, Muted, PageHeader, SectionHeading } from '../components/ui'

// SearchPage renders the global search of ONE project: two labelled
// groups — Work items and Memories — each capped at 20 by the face,
// each row linking into its module, and a "see all" link per group
// carrying the same query into that module's list/search view.
export default function SearchPage({ project }: { project: string }) {
  const q = new URLSearchParams(window.location.search).get('q') ?? ''
  const [res, setRes] = useState<SearchResults | null>(null)
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    setRes(null)
    setMissing(false)
    if (q === '') return
    apiGet<SearchResults>('/web/api/search', { project, q })
      .then(setRes)
      .catch((err) => {
        if (err instanceof ApiError && err.status === 404) setMissing(true)
      })
  }, [project, q])

  if (missing) return <NotFound />
  if (q === '') return <NotFound />

  return (
    <div data-testid="search" className="max-w-3xl">
      <PageHeader title={`Search “${q}” · ${project}`} />
      {res === null ? (
        <Muted>Searching…</Muted>
      ) : (
        <div className="flex flex-col gap-6">
          <section>
            <GroupHeading title="Work items" testid="see-all-work-items" to={`${href(project, 'work-items')}?q=${encodeURIComponent(q)}`} />
            {res.work_items.length === 0 ? (
              <EmptyState>No matching work items.</EmptyState>
            ) : (
              <Card>
                <ul className="m-0 list-none p-0">
                  {res.work_items.map((it) => (
                    <li key={it.id} className="flex items-center gap-3 border-b border-line px-4 py-2 last:border-b-0">
                      <a className="min-w-0 flex-1 font-medium text-fg [overflow-wrap:anywhere] hover:text-accent-fg" href={href(project, 'work-items', it.id)}>
                        {it.goal}
                      </a>
                      <StatusPill status={it.status} />
                    </li>
                  ))}
                </ul>
              </Card>
            )}
          </section>
          <section>
            <GroupHeading title="Memories" testid="see-all-memories" to={`${href(project, 'memories')}?q=${encodeURIComponent(q)}`} />
            {res.memories.length === 0 ? (
              <EmptyState>No matching memories.</EmptyState>
            ) : (
              <Card>
                <ul className="m-0 list-none p-0">
                  {res.memories.map((m) => (
                    <li key={m.id} className="flex items-center gap-3 border-b border-line px-4 py-2 last:border-b-0">
                      <TypeDot type={m.type} />
                      <a className="min-w-0 flex-1 font-medium text-fg [overflow-wrap:anywhere] hover:text-accent-fg" href={href(project, 'memories', m.id)}>
                        {m.title}
                      </a>
                      <span className="text-caption text-muted">{m.type}</span>
                    </li>
                  ))}
                </ul>
              </Card>
            )}
          </section>
        </div>
      )}
    </div>
  )
}

// GroupHeading is one result group's section heading with its accent
// "see all" link carrying the query into the module.
function GroupHeading({ title, testid, to }: { title: string; testid: string; to: string }) {
  return (
    <div className="mb-2 flex items-baseline justify-between gap-3">
      <SectionHeading>{title}</SectionHeading>
      <a data-testid={testid} className="inline-flex items-center gap-1 text-caption" href={to}>
        see all
        <ArrowRightIcon />
      </a>
    </div>
  )
}
