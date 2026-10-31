import { useEffect, useState } from 'react'
import { apiGet, ApiError } from './api'
import { parseRoute, href, Route } from './router'
import type { Context } from './types'
import Sidebar from './components/Sidebar'
import TopBar from './components/TopBar'
import NotFound from './components/NotFound'
import PickerPage from './pages/PickerPage'
import WorkItemsPage from './pages/WorkItemsPage'
import WorkItemDetailPage from './pages/WorkItemDetailPage'
import MemoriesPage from './pages/MemoriesPage'
import MemoryDetailPage from './pages/MemoryDetailPage'
import MachinesPage from './pages/MachinesPage'
import SearchPage from './pages/SearchPage'

// App is the console root: the shell (sidebar + top bar) around
// the routed view. Navigation is plain anchors — the server serves the
// one shell for every document path, the browser reloads it, and each
// view fetches its own data from the private JSON face. No history
// library, no client-side routing state.
export default function App() {
  const route = parseRoute(window.location.pathname)
  const [ctx, setCtx] = useState<Context | null>(null)
  const [authRequired, setAuthRequired] = useState(false)

  useEffect(() => {
    apiGet<Context>('/web/api/context')
      .then(setCtx)
      .catch((err) => {
        if (err instanceof ApiError && err.status === 401) setAuthRequired(true)
      })
  }, [])

  if (authRequired) {
    return (
      <main className="mx-auto max-w-prose p-8">
        <div className="card p-6">
          <h1 className="mb-2 text-title font-semibold">aihub console</h1>
          <p className="text-muted">Authentication required — reload the page to sign in with your api key.</p>
        </div>
      </main>
    )
  }
  if (ctx === null) {
    return (
      <main className="mx-auto max-w-prose p-8">
        <p className="text-muted">Loading…</p>
      </main>
    )
  }

  return (
    <div className="flex min-h-screen">
      <Sidebar route={route} admin={ctx.admin} />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar ctx={ctx} route={route} />
        <main className="w-full min-w-0 max-w-6xl flex-1 px-4 py-6 sm:px-8">
          <View route={route} />
        </main>
      </div>
    </div>
  )
}

// View renders the routed page. Unknown client routes — and pages whose
// JSON face answered 404 — share the one not-found view.
function View({ route }: { route: Route }) {
  switch (route.view) {
    case 'picker':
      return <PickerPage />
    case 'work-items':
      return <WorkItemsPage project={route.project} />
    case 'work-item':
      return <WorkItemDetailPage project={route.project} id={route.id} />
    case 'memories':
      return <MemoriesPage project={route.project} />
    case 'memory':
      return <MemoryDetailPage project={route.project} id={route.id} />
    case 'search':
      return <SearchPage project={route.project} />
    case 'machines':
      return <MachinesPage />
    default:
      return <NotFound />
  }
}

export { href }
