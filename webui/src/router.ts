// The browser-side route table. The server serves one shell for every
// authenticated document path; the client parses the path and renders
// the view (or the one not-found view). No router dependency, no
// history library — plain anchors and popstate.

export type Route =
  | { view: 'picker' }
  | { view: 'work-items'; project: string }
  | { view: 'work-item'; project: string; id: string }
  | { view: 'memories'; project: string }
  | { view: 'memory'; project: string; id: string }
  | { view: 'search'; project: string }
  | { view: 'machines' }
  | { view: 'not-found' }

// parseRoute maps a console path (the /web-rooted browser path) to
// its view. Unknown shapes answer the one not-found view — never a
// guess.
export function parseRoute(path: string): Route {
  // The server mounts the console under /web/; the router works on
  // the segments BELOW that prefix (an exact /web is the picker).
  if (path === '/web') {
    path = '/'
  } else if (path.startsWith('/web/')) {
    path = path.slice('/web'.length)
  }
  const segments = path
    .split('/')
    .filter((s) => s.length > 0)
    .map((s) => {
      try {
        return decodeURIComponent(s)
      } catch {
        // A malformed escape (a stray %) must never throw during render
        // and leave a blank page: it is simply not a route this console
        // knows — the one not-found view answers it.
        return ''
      }
    })
  if (segments.some((s) => s === '')) return { view: 'not-found' }
  if (segments.length === 0) return { view: 'picker' }
  if (segments[0] === 'machines' && segments.length === 1) return { view: 'machines' }
  if (segments.length === 1) return { view: 'not-found' }
  const project = segments[0]
  switch (segments[1]) {
    case 'work-items':
      if (segments.length === 2) return { view: 'work-items', project }
      if (segments.length === 3) return { view: 'work-item', project, id: segments[2] }
      break
    case 'memories':
      if (segments.length === 2) return { view: 'memories', project }
      if (segments.length === 3) return { view: 'memory', project, id: segments[2] }
      break
    case 'search':
      if (segments.length === 2) return { view: 'search', project }
      break
  }
  return { view: 'not-found' }
}

// href builds a console href with every path segment escaped
// (encodeURIComponent per segment): project names and object ids with
// ?, #, /, %, space or non-ASCII resolve to the exact object (the
// client half of the link-escaping fix; the server decodes each
// escaped segment of r.URL.EscapedPath()).
export function href(...segments: (string | number)[]): string {
  return '/web/' + segments.map((s) => encodeURIComponent(String(s))).join('/')
}
