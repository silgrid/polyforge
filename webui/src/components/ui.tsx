// ui.tsx holds the few shared presentation primitives every view uses,
// so the page header, section heading, card, dense table, filter chip
// and empty state look the same everywhere. Class-only styling: no
// inline style attribute exists anywhere in the console.
import type { ReactNode } from 'react'

// PageHeader renders the page's one h1 (the title step of the type
// scale) with an optional caption line and trailing controls.
export function PageHeader({
  title,
  caption,
  children,
}: {
  title: ReactNode
  caption?: ReactNode
  children?: ReactNode
}) {
  return (
    <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-title font-semibold tracking-tight [overflow-wrap:anywhere]">{title}</h1>
        {caption ? <p className="mt-1 text-caption text-muted">{caption}</p> : null}
      </div>
      {children ? <div className="flex min-w-0 flex-wrap items-center gap-2">{children}</div> : null}
    </div>
  )
}

// SectionHeading is the section step of the type scale.
export function SectionHeading({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <h2 className={`text-section font-semibold ${className}`}>{children}</h2>
}

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <div className={`card ${className}`}>{children}</div>
}

// DataTable is the dense list: one table with hairline rows inside a
// card that scrolls on its own, so the document never scrolls
// horizontally.
export function DataTable({ head, children }: { head?: ReactNode; children: ReactNode }) {
  return (
    <div className="card min-w-0 overflow-x-auto">
      <table className="data-table">
        {head ? <thead>{head}</thead> : null}
        <tbody>{children}</tbody>
      </table>
    </div>
  )
}

// Chip is one filter choice; the active one carries the accent.
export function Chip({ to, active, children }: { to: string; active: boolean; children: ReactNode }) {
  return (
    <a
      href={to}
      aria-current={active ? 'true' : undefined}
      className={`rounded-full border px-2.5 py-0.5 text-caption no-underline hover:no-underline ${
        active ? 'border-accent bg-accent-soft text-accent-fg' : 'border-line text-muted hover:text-fg'
      }`}
    >
      {children}
    </a>
  )
}

export function EmptyState({ children, testid }: { children: ReactNode; testid?: string }) {
  return (
    <p data-testid={testid} className="card px-4 py-6 text-center text-muted">
      {children}
    </p>
  )
}

// Muted is the placeholder line a view shows while its data loads.
export function Muted({ children }: { children: ReactNode }) {
  return <p className="text-muted">{children}</p>
}

// The shared form-control classes: hairline border, surface fill, the
// accent focus ring.
export const inputClass =
  'min-w-0 rounded-md border border-line bg-surface px-2.5 py-1.5 text-fg placeholder:text-muted focus:border-accent disabled:opacity-60'
export const buttonClass =
  'rounded-md border border-line bg-surface px-3 py-1.5 text-fg hover:bg-tint disabled:opacity-60'
