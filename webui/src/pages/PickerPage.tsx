import { useCallback, useEffect, useState } from 'react'
import { apiGet, apiPost } from '../api'
import { href } from '../router'
import type { Context, ProjectRef } from '../types'
import { ChevronRightIcon, FolderIcon } from '../icons'
import { Card, EmptyState, Muted, PageHeader } from '../components/ui'

// PickerPage is the console home: the caller's readable projects, one row
// each linking into the work-item module. No default project, no
// redirect. Deactivated projects are hidden by default (the server's
// context answer filters them, polyforge#30); the「Show deactivated」
// entry beside the list reveals them with their marker, and the restore
// door sits in the same row — a project a caller may not restore carries
// no button, only the marker.
export default function PickerPage() {
  const [projects, setProjects] = useState<ProjectRef[] | null>(null)
  const [showDeactivated, setShowDeactivated] = useState(false)
  const [error, setError] = useState('')

  const read = useCallback(() => {
    const params = showDeactivated ? { include_deactivated: '1' } : undefined
    apiGet<Context>('/web/api/context', params)
      .then((c) => {
        setProjects(c.projects)
        setError('')
      })
      .catch(() => {
        setProjects([])
        setError('the project list could not be read')
      })
  }, [showDeactivated])

  useEffect(() => {
    read()
  }, [read])

  const restore = async (project: ProjectRef) => {
    try {
      await apiPost(`/web/api/projects/${encodeURIComponent(project.name)}/restore`)
      read()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'the restore failed')
    }
  }

  const dormant = (projects ?? []).filter((p) => p.status === 'deactivated').length
  return (
    <div data-testid="picker" className="max-w-prose">
      <PageHeader title="Projects" />
      <div className="mb-2 flex items-center gap-3">
        <button
          type="button"
          data-testid="show-deactivated"
          className="rounded border border-line px-2 py-1 text-xs text-fg hover:bg-tint disabled:opacity-50"
          aria-pressed={showDeactivated}
          onClick={() => setShowDeactivated(!showDeactivated)}
        >
          {showDeactivated ? 'Hide deactivated' : 'Show deactivated'}
        </button>
        {dormant > 0 && showDeactivated && (
          <Muted>{dormant} deactivated</Muted>
        )}
      </div>
      {error && <Muted>{error}</Muted>}
      {projects === null ? (
        <Muted>Loading…</Muted>
      ) : projects.length === 0 ? (
        <EmptyState>No readable projects for this key.</EmptyState>
      ) : (
        <Card>
          <ul className="m-0 list-none p-0">
            {projects.map((p) => (
              <li
                key={p.id}
                data-testid="picker-row"
                className="flex items-center gap-3 border-b border-line px-4 py-2.5 last:border-b-0"
              >
                <a
                  className="flex min-w-0 flex-1 items-center gap-3 text-fg no-underline hover:bg-tint hover:no-underline"
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
                {p.status === 'deactivated' && (
                  <span
                    data-testid="deactivated-mark"
                    className="rounded bg-tint px-2 py-0.5 text-xs text-muted"
                    title="deactivated: the project is read-only and hidden from the default lists"
                  >
                    deactivated
                  </span>
                )}
                {p.status === 'deactivated' && p.can_restore && (
                  <button
                    type="button"
                    data-testid="restore-project"
                    className="rounded border border-line px-2 py-1 text-xs text-fg hover:bg-tint"
                    title={`restore ${p.name} (the project becomes writable again; nothing was deleted)`}
                    onClick={() => restore(p)}
                  >
                    Restore
                  </button>
                )}
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  )
}
