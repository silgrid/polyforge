import { useEffect, useState } from 'react'
import { apiGet, ApiError } from '../api'
import { href } from '../router'
import type { SearchResults } from '../types'
import NotFound from '../components/NotFound'
import StatusPill from '../components/StatusPill'
import TypeDot from '../components/TypeDot'

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
    <div data-testid="search" className="max-w-prose">
      <h1 className="text-xl font-semibold mb-4">
        Search “{q}” · {project}
      </h1>
      {res === null ? (
        <p className="text-muted">Searching…</p>
      ) : (
        <>
          <section className="mb-6">
            <h2 className="text-base font-semibold mb-2 flex items-center gap-2">
              Work items
              <a
                data-testid="see-all-work-items"
                className="text-accent text-sm font-normal"
                href={`${href(project, 'work-items')}?q=${encodeURIComponent(q)}`}
              >
                see all
              </a>
            </h2>
            {res.work_items.length === 0 ? (
              <p className="text-muted">No matching work items.</p>
            ) : (
              <ul className="list-none p-0">
                {res.work_items.map((it) => (
                  <li key={it.id} className="py-1 border-b border-line text-sm">
                    <a className="text-accent" href={href(project, 'work-items', it.id)}>
                      {it.goal}
                    </a>
                    <span className="ml-2">
                      <StatusPill status={it.status} />
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
          <section>
            <h2 className="text-base font-semibold mb-2 flex items-center gap-2">
              Memories
              <a
                data-testid="see-all-memories"
                className="text-accent text-sm font-normal"
                href={`${href(project, 'memories')}?q=${encodeURIComponent(q)}`}
              >
                see all
              </a>
            </h2>
            {res.memories.length === 0 ? (
              <p className="text-muted">No matching memories.</p>
            ) : (
              <ul className="list-none p-0">
                {res.memories.map((m) => (
                  <li key={m.id} className="py-1 border-b border-line text-sm">
                    <TypeDot type={m.type} />
                    <a className="text-accent" href={href(project, 'memories', m.id)}>
                      {m.title}
                    </a>
                    <span className="text-muted ml-2">{m.type}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </>
      )}
    </div>
  )
}
