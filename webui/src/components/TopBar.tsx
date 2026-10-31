import { useState } from 'react'
import type { Context } from '../types'
import { href, Route } from '../router'
import { SearchIcon } from '../icons'
import { buttonClass, inputClass } from './ui'

// TopBar is the top strip: the project switcher and the global search
// box. The search box is disabled until a project is selected — search
// is scoped to one project, so on the picker it runs nothing. The
// selected project lives in the URL, never in storage.
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
      className="sticky top-0 z-10 flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line bg-surface px-4 py-2.5 sm:px-8"
    >
      <label className="flex min-w-0 items-center gap-2 text-caption text-muted">
        Project
        <select
          data-testid="project-switcher"
          value={project}
          onChange={(e) => switchProject(e.target.value)}
          className={`${inputClass} max-w-[12rem] py-1 text-body`}
        >
          <option value="">—</option>
          {ctx.projects.map((p) => (
            <option key={p.id} value={p.name}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <form onSubmit={runSearch} className="flex w-full min-w-0 justify-end gap-2 sm:w-auto sm:flex-1">
        <label className="relative flex min-w-0 flex-1 items-center sm:max-w-xs">
          <span className="pointer-events-none absolute left-2.5 text-muted">
            <SearchIcon />
          </span>
          <input
            data-testid="search-box"
            type="search"
            aria-label="Search this project"
            placeholder="Search this project…"
            disabled={!project}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            className={`${inputClass} w-full py-1 pl-8`}
          />
        </label>
        <button type="submit" disabled={!project} className={`${buttonClass} py-1`}>
          Search
        </button>
      </form>
    </header>
  )
}
