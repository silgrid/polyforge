import { useState } from 'react'
import type { ReactNode } from 'react'
import { href, Route } from '../router'
import { BrandIcon, ChevronLeftIcon, ChevronRightIcon, MachinesIcon, MemoriesIcon, WorkItemsIcon } from '../icons'

// Sidebar is the collapsible left navigation: Work items, Memories and
// (admins only) Machines — inline SVG icon + text label. Collapsed, and
// always at the narrow width (a CSS media query in styles.css), it is
// an icon rail whose entries keep their names as title/aria-label. The
// collapse state is plain view state: nothing is persisted.
export default function Sidebar({ route, admin }: { route: Route; admin: boolean }) {
  const [open, setOpen] = useState(true)

  const isActive = (...views: Route['view'][]) => views.includes(route.view)

  return (
    <nav
      data-testid="sidebar"
      className={`console-nav ${open ? '' : 'is-collapsed'} shrink-0 border-r border-line bg-surface`}
    >
      <div className="sticky top-0 flex h-screen flex-col py-3">
        <a
          href={href()}
          title="aihub"
          className="mx-2 mb-4 flex items-center gap-2.5 rounded-md px-2.5 py-1 font-semibold text-fg no-underline hover:no-underline"
        >
          <span className="text-accent">
            <BrandIcon />
          </span>
          {open ? <span className="nav-label truncate">aihub</span> : null}
        </a>
        <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
          <NavItem icon={<WorkItemsIcon />} label="Work items" to={routeProjectHref(route, 'work-items')} open={open} active={isActive('work-items', 'work-item')} />
          <NavItem icon={<MemoriesIcon />} label="Memories" to={routeProjectHref(route, 'memories')} open={open} active={isActive('memories', 'memory')} />
          {admin ? (
            <NavItem icon={<MachinesIcon />} label="Machines" to={href('machines')} open={open} active={isActive('machines')} />
          ) : null}
        </ul>
        <button
          type="button"
          data-testid="sidebar-toggle"
          aria-label={open ? 'Collapse navigation' : 'Expand navigation'}
          aria-expanded={open}
          onClick={() => setOpen(!open)}
          className="mx-2 mt-auto flex items-center justify-center rounded-md border border-line px-2.5 py-1.5 text-muted hover:bg-tint hover:text-fg"
        >
          {open ? <ChevronLeftIcon /> : <ChevronRightIcon />}
        </button>
      </div>
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

// NavItem is one module entry: the active one sits on the accent-soft
// fill with an accent left mark.
function NavItem({
  icon,
  label,
  to,
  open,
  active,
}: {
  icon: ReactNode
  label: string
  to: string
  open: boolean
  active: boolean
}) {
  return (
    <li className="mx-2">
      <a
        href={to}
        title={label}
        aria-label={label}
        aria-current={active ? 'page' : undefined}
        className={`relative flex items-center gap-2.5 rounded-md px-3 py-1.5 no-underline hover:no-underline ${
          active
            ? 'bg-accent-soft text-accent-fg before:absolute before:bottom-1.5 before:left-0 before:top-1.5 before:w-0.5 before:rounded-full before:bg-accent'
            : 'text-muted hover:bg-tint hover:text-fg'
        }`}
      >
        {icon}
        {open ? <span className="nav-label truncate">{label}</span> : null}
      </a>
    </li>
  )
}
