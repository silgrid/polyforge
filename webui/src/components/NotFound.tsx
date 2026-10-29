import { href } from '../router'

// NotFound is the ONE not-found view: an unknown client route, and any
// page whose JSON face answered 404 (unknown or unreadable project or
// object) — the same words for both, so the console never tells a
// visitor whether a thing exists.
export default function NotFound() {
  return (
    <div data-testid="not-found" className="max-w-prose">
      <h1 className="text-xl font-semibold mb-2">Not found</h1>
      <p className="text-muted">
        No such page — or nothing you can read here. Pick a project on the{' '}
        <a className="text-accent" href={href()}>
          console home
        </a>
        .
      </p>
    </div>
  )
}
