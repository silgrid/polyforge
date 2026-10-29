// clean-dist.mjs removes the generated build output before each build.
// Nothing under dist is tracked, so on a clean checkout the directory
// does not exist yet — it is created here (the go:embed all:dist
// pattern needs the tree once the build has run; `make build` always
// runs this first).
import { existsSync, mkdirSync, readdirSync, rmSync, statSync } from 'node:fs'
import { join } from 'node:path'

const dist = new URL('../dist/', import.meta.url).pathname
if (!existsSync(dist)) {
  mkdirSync(dist, { recursive: true })
}
for (const name of readdirSync(dist)) {
  const path = join(dist, name)
  if (statSync(path).isDirectory()) rmSync(path, { recursive: true })
  else rmSync(path)
}
