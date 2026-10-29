import { useEffect, useState } from 'react'
import { apiGet } from '../api'
import { href } from '../router'
import type { Context, ProjectRef } from '../types'

// PickerPage is the console home: exactly the caller's readable
// projects, one link each into the work-item module. No default
// project, no redirect.
export default function PickerPage() {
  const [projects, setProjects] = useState<ProjectRef[] | null>(null)
  useEffect(() => {
    apiGet<Context>('/web/api/context')
      .then((c) => setProjects(c.projects))
      .catch(() => setProjects([]))
  }, [])
  return (
    <div data-testid="picker" className="max-w-prose">
      <h1 className="text-2xl font-semibold mb-4">Projects</h1>
      {projects === null ? (
        <p className="text-muted">Loading…</p>
      ) : projects.length === 0 ? (
        <p className="text-muted">No readable projects for this key.</p>
      ) : (
        <ul className="list-none p-0">
          {projects.map((p) => (
            <li key={p.id} className="border-b border-line">
              <a className="block py-2 text-accent no-underline" href={href(p.name, 'work-items')}>
                {p.name}
              </a>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
