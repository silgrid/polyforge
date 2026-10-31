import { useEffect, useState } from 'react'
import { apiGet } from '../api'
import { href } from '../router'
import type { Context, ProjectRef } from '../types'
import { ChevronRightIcon, FolderIcon } from '../icons'
import { Card, EmptyState, Muted, PageHeader } from '../components/ui'

// PickerPage is the console home: exactly the caller's readable
// projects, one row each linking into the work-item module. No default
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
      <PageHeader title="Projects" />
      {projects === null ? (
        <Muted>Loading…</Muted>
      ) : projects.length === 0 ? (
        <EmptyState>No readable projects for this key.</EmptyState>
      ) : (
        <Card>
          <ul className="m-0 list-none p-0">
            {projects.map((p) => (
              <li key={p.id} className="border-b border-line last:border-b-0">
                <a
                  className="flex items-center gap-3 px-4 py-2.5 text-fg no-underline hover:bg-tint hover:no-underline"
                  href={href(p.name, 'work-items')}
                >
                  <span className="text-muted">
                    <FolderIcon />
                  </span>
                  <span className="min-w-0 flex-1 font-medium [overflow-wrap:anywhere]">{p.name}</span>
                  <span className="text-muted">
                    <ChevronRightIcon />
                  </span>
                </a>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  )
}
