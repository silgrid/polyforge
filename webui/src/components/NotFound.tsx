import { href } from '../router'

// NotFound is the ONE not-found view: an unknown client route, and any
// page whose JSON face answered 404 (unknown or unreadable project or
// object) — the same words for both, so the console never tells a
// visitor whether a thing exists.
export default function NotFound() {
  return (
    <div data-testid="not-found" className="card max-w-prose px-6 py-5">
      <h1 className="mb-2 text-title font-semibold tracking-tight">Not found</h1>
      <p className="text-muted">
        No such page — or nothing you can read here. Pick a project on the <a href={href()}>console home</a>.
      </p>
    </div>
  )
}
