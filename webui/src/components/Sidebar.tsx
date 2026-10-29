import { useState } from 'react'
import { href, Route } from '../router'

// Sidebar is the collapsible left navigation: Work items, Memories and
// (admins only) Machines — inline SVG icon + text, no icon package.
// The collapse state is plain view state: nothing is persisted.
export default function Sidebar({ route, admin }: { route: Route; admin: boolean }) {
  const [open, setOpen] = useState(true)

  const active = (view: Route['view']) =>
    route.view === view ? 'bg-tint text-fg' : 'text-muted hover:text-fg'

  return (
    <nav
      data-testid="sidebar"
      className={`${open ? 'w-52' : 'w-14'} shrink-0 border-r border-line bg-tint transition-[width]`}
    >
      <div className="flex items-center justify-between px-2 py-3">
        <a href={href()} className="font-semibold text-fg no-underline truncate">
          {open ? 'aihub' : 'a'}
        </a>
        <button
          type="button"
          data-testid="sidebar-toggle"
          aria-label={open ? 'Collapse navigation' : 'Expand navigation'}
          aria-expanded={open}
          onClick={() => setOpen(!open)}
          className="text-muted hover:text-fg border border-line rounded px-1"
        >
          {open ? '‹' : '›'}
        </button>
      </div>
      <ul className="list-none p-0 m-0">
        <NavItem icon={listIcon} label="Work items" to={routeProjectHref(route, 'work-items')} open={open} className={active('work-items') + (route.view === 'work-item' ? ' bg-tint text-fg' : '')} />
        <NavItem icon={bookIcon} label="Memories" to={routeProjectHref(route, 'memories')} open={open} className={active('memories') + (route.view === 'memory' ? ' bg-tint text-fg' : '')} />
        {admin ? (
          <NavItem icon={chipIcon} label="Machines" to={href('machines')} open={open} className={active('machines')} />
        ) : null}
      </ul>
    </nav>
  )
}

// routeProjectHref answers the module link target, keeping the
// selected project when one is in the route.
function routeProjectHref(route: Route, module: 'work-items' | 'memories'): string {
  if (route.view === 'picker' || route.view === 'machines') return href()
  const project = 'project' in route ? route.project : ''
  if (!project) return href()
  return href(project, module)
}

function NavItem({
  icon,
  label,
  to,
  open,
  className,
}: {
  icon: JSX.Element
  label: string
  to: string
  open: boolean
  className: string
}) {
  return (
    <li>
      <a
        href={to}
        title={label}
        className={`${className} flex items-center gap-2 no-underline px-3 py-2`}
      >
        {icon}
        {open ? <span className="truncate">{label}</span> : null}
      </a>
    </li>
  )
}

// The three module icons are inline SVG — no icon package, no external
// resource (the CSP permits same-origin only).
const listIcon = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true">
    <path d="M2 3h12M2 8h12M2 13h12" />
  </svg>
)
const bookIcon = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true">
    <path d="M2 2h5v12H2zM9 2h5v12H9zM9 5.5h5" />
  </svg>
)
const chipIcon = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true">
    <rect x="2" y="2" width="12" height="12" rx="2" />
    <path d="M5 5h2v2H5zM9 9h2v2H9z" />
  </svg>
)
