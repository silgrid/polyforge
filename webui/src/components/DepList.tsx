import { href } from '../router'
import type { DepEdge } from '../types'
import { Card, SectionHeading } from './ui'

// DepList renders one dependency direction of a work-item detail as a
// section heading over a hairline row list: readable targets as links
// into their detail page, an unreadable one as its bare monospace id
// (the server already stripped everything else — this view adds
// nothing).
export default function DepList({ title, edges }: { title: string; edges: DepEdge[] }) {
  if (edges.length === 0) return null
  return (
    <section>
      <SectionHeading className="mb-2">{title}</SectionHeading>
      <Card>
        <ul className="m-0 list-none p-0">
          {edges.map((e) => (
            <li key={e.id + e.kind} className="border-b border-line px-4 py-2 last:border-b-0 [overflow-wrap:anywhere]">
              {e.slug ? (
                <a href={href(slugProject(e.slug), 'work-items', e.id)}>{e.slug}</a>
              ) : (
                <span className="break-all font-mono text-muted">{e.id}</span>
              )}
              <span className="text-muted"> · {e.kind}</span>
              {e.status ? <span className="text-muted"> · {e.status}</span> : null}
            </li>
          ))}
        </ul>
      </Card>
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
