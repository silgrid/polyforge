// icons.tsx is the console's one icon module: hand-drawn inline SVG on a
// 24-unit grid, stroked in currentColor so every icon takes its colour
// from the token it sits in. No icon font, no sprite, no <use>, no
// external reference, no package — the CSP allows same-origin only.
import type { ReactNode } from 'react'

function Svg({ size = 16, children }: { size?: number; children: ReactNode }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      className="shrink-0"
    >
      {children}
    </svg>
  )
}

export function BrandIcon() {
  return (
    <Svg size={20}>
      <rect x="3" y="3" width="18" height="18" rx="5" />
      <path d="M8 15.5 12 7l4 8.5M9.5 12.5h5" />
    </Svg>
  )
}

export function WorkItemsIcon() {
  return (
    <Svg>
      <path d="M9 6h11M9 12h11M9 18h11" />
      <path d="m3.5 6 1 1 2-2M3.5 12l1 1 2-2M3.5 18l1 1 2-2" />
    </Svg>
  )
}

export function MemoriesIcon() {
  return (
    <Svg>
      <path d="M4 5.5A1.5 1.5 0 0 1 5.5 4H11v16H5.5A1.5 1.5 0 0 1 4 18.5z" />
      <path d="M13 4h5.5A1.5 1.5 0 0 1 20 5.5v13a1.5 1.5 0 0 1-1.5 1.5H13z" />
      <path d="M15.5 8.5h2M15.5 12h2" />
    </Svg>
  )
}

export function MachinesIcon() {
  return (
    <Svg>
      <rect x="3" y="4" width="18" height="7" rx="2" />
      <rect x="3" y="13" width="18" height="7" rx="2" />
      <path d="M7 7.5h.01M7 16.5h.01M11 7.5h6M11 16.5h6" />
    </Svg>
  )
}

export function ChevronLeftIcon() {
  return (
    <Svg>
      <path d="m14.5 6-6 6 6 6" />
    </Svg>
  )
}

export function ChevronRightIcon() {
  return (
    <Svg>
      <path d="m9.5 6 6 6-6 6" />
    </Svg>
  )
}

export function SearchIcon() {
  return (
    <Svg>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m20 20-4.2-4.2" />
    </Svg>
  )
}

export function ArrowRightIcon() {
  return (
    <Svg size={14}>
      <path d="M5 12h14M13 6l6 6-6 6" />
    </Svg>
  )
}

export function FolderIcon() {
  return (
    <Svg>
      <path d="M3.5 7A1.5 1.5 0 0 1 5 5.5h4l2 2.5h8A1.5 1.5 0 0 1 20.5 9.5v8A1.5 1.5 0 0 1 19 19H5a1.5 1.5 0 0 1-1.5-1.5z" />
    </Svg>
  )
}

export function ClockIcon() {
  return (
    <Svg size={14}>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 2" />
    </Svg>
  )
}

// BandIcon is the heartbeat band mark: a filled dot in the band's own
// colour (the band class on its container sets currentColor).
export function BandIcon() {
  return (
    <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true" focusable="false" className="shrink-0">
      <circle cx="5" cy="5" r="4" fill="currentColor" />
    </svg>
  )
}
