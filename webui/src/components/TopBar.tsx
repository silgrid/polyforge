import { useState } from 'react'
import type { Context } from '../types'
import { href, Route } from '../router'

// TopBar is the fixed top strip: the project switcher and the global
// search box. The search box is disabled until a project is selected —
// search is scoped to one project, so on the picker it runs nothing.
// The selected project lives in the URL, never in storage.
export default function TopBar({ ctx, route }: { ctx: Context; route: Route }) {
  const project = 'project' in route ? route.project : ''
  const [q, setQ] = useState('')

  const switchProject = (name: string) => {
    window.location.href = href(name, 'work-items')
  }
  const runSearch = (event: React.FormEvent) => {
    event.preventDefault()
    if (!project || q.trim() === '') return
    window.location.href = `${href(project, 'search')}?q=${encodeURIComponent(q.trim())}`
  }

  return (
    <header
      data-testid="topbar"
      className="flex items-center gap-4 border-b border-line px-4 py-2 bg-tint"
    >
      <label className="text-muted text-sm">
        Project
        <select
          data-testid="project-switcher"
          value={project}
          onChange={(e) => switchProject(e.target.value)}
          className="ml-2 bg-bg text-fg border border-line rounded px-2 py-1"
        >
          <option value="">—</option>
          {ctx.projects.map((p) => (
            <option key={p.id} value={p.name}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <form onSubmit={runSearch} className="flex-1 flex justify-end gap-2">
        <input
          data-testid="search-box"
          type="search"
          placeholder="Search this project…"
          disabled={!project}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          className="w-72 max-w-full bg-bg text-fg border border-line rounded px-2 py-1 disabled:opacity-50"
        />
        <button
          type="submit"
          disabled={!project}
          className="border border-line rounded px-3 py-1 text-fg bg-tint disabled:opacity-50"
        >
          Search
        </button>
      </form>
    </header>
  )
}
