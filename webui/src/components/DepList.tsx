import { href } from '../router'
import type { DepEdge } from '../types'

// DepList renders one dependency direction of a work-item detail:
// readable targets as links into their detail page, an unreadable one
// as its bare id (the server already stripped everything else — this
// view adds nothing).
export default function DepList({ title, edges }: { title: string; edges: DepEdge[] }) {
  if (edges.length === 0) return null
  return (
    <section className="mb-6">
      <h2 className="text-base font-semibold mb-1">{title}</h2>
      <ul className="list-none p-0 m-0">
        {edges.map((e) => (
          <li key={e.id + e.kind} className="py-1 border-b border-line text-sm">
            {e.slug ? (
              <a className="text-accent" href={href(slugProject(e.slug), 'work-items', e.id)}>
                {e.slug}
              </a>
            ) : (
              <span className="text-muted font-mono">{e.id}</span>
            )}
            <span className="text-muted"> · {e.kind}</span>
            {e.status ? <span className="text-muted"> · {e.status}</span> : null}
          </li>
        ))}
      </ul>
    </section>
  )
}

// slugProject answers the project name part of a "<project>#<seq>"
// slug — a cross-project readable edge links into ITS project's module,
// not the current one (a wrong-project detail would 404).
function slugProject(slug: string): string {
  const cut = slug.lastIndexOf('#')
  return cut > 0 ? slug.slice(0, cut) : ''
}
