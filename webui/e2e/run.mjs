// run.mjs — the console's browser acceptance runner.
//
// Drives the six browser-rendered scenarios of
// artifacts/e2e-scenarios.md (17–22: hostile corpus inert, layout and
// visual tokens, search disabled until a project is selected, machine
// heartbeat bands without self-refresh, the #103 memory visual spec,
// and escaped links resolving to the exact object) against the REAL
// aihub binary over loopback, in a real chromium.
//
// Every scenario's THEN parts are asserted here, including the #103
// DOM behaviours the ported Go suite cannot see (S12's tag chips,
// S13's zero-hit empty state, S15's h1 title and monospace id footer)
// and S18's full layout set (the collapse toggle, icon+text entries,
// the top-bar search box, the #103 type colour tokens, the compact
// work-item table with status pills).
//
// Contract with the Go driver (test/w105_web_browser_acceptance_test.go):
//   PF_WEB_URL       the aihub base URL (http://127.0.0.1:PORT)
//   PF_WEB_KEY       an admin api key (the Basic password; the username
//                    is ignored by the face)
//   PF_WEB_FIXTURES  JSON: the ids/urls the driver seeded
//
// Fails LOUDLY, never silently skips: a missing prerequisite or a
// failed scenario exits non-zero with its name and reason. assert is
// SYNCHRONOUS and every call sits inside an awaited scenario function,
// so a failing check fails its scenario here and now — never an
// unhandled rejection that still prints a PASS line.

import { chromium } from 'playwright'

const baseURL = process.env.PF_WEB_URL ?? ''
const key = process.env.PF_WEB_KEY ?? ''
const fixtures = JSON.parse(process.env.PF_WEB_FIXTURES ?? '{}')

if (!baseURL || !key) {
  fail(`PF_WEB_URL and PF_WEB_KEY must be set (the Go driver passes them); got url=${baseURL ? 'set' : 'empty'} key=${key ? 'set' : 'empty'}`)
}

function fail(message) {
  console.error(`browser suite: ${message}`)
  process.exit(1)
}

// assert is synchronous on purpose: a failed check throws inside the
// scenario's own awaited frame, so the runner below cannot print PASS
// for a scenario whose checks failed.
function assert(cond, message) {
  if (!cond) {
    throw new Error(message)
  }
}

// scenarios: [name, fn(page)] — run in order; the first failure names
// its scenario and fails the run.
const scenarios = [
  ['S17 hostile corpus renders inert', scenarioHostile],
  ['S18 layout, navigation and visual tokens', scenarioLayout],
  ['S19 search disabled until a project is selected', scenarioSearch],
  ['S20 machine bands follow the API threshold, no refresh', scenarioMachines],
  ['S21 memory module carries the #103 visual spec', scenarioMemoryVisual],
  ['S22 links from escaping names resolve to the exact object', scenarioEscapedLinks],
]

const browser = await chromium.launch()
try {
  const context = await browser.newContext({
    baseURL,
    httpCredentials: { username: 'anyone', password: key },
  })
  const page = await context.newPage()
  for (const [name, fn] of scenarios) {
    try {
      await fn(page)
      console.log(`PASS ${name}`)
    } catch (err) {
      fail(`${name}: ${err?.stack ?? err}\nCurrent URL: ${page.url()}`)
    }
  }
} finally {
  await browser.close()
}

// ---------------------------------------------------------------------------
// S17: a hostile work item renders inert — no dialog, no script, no
// external request; the injected markup (including the unicode
// tricks and the hostile event payload) is visible as text.
async function scenarioHostile(page) {
  let dialog = null
  page.on('dialog', (d) => {
    dialog = d
    d.dismiss().catch(() => {})
  })
  const requests = []
  page.on('request', (r) => requests.push(r.url()))
  await page.goto(`/web/${enc(fixtures.project)}/work-items/${enc(fixtures.hostile_wi)}`)
  const detail = page.getByTestId('work-item-detail')
  await detail.waitFor({ timeout: 10_000 }).catch(async (err) => {
    const body = await page.textContent('body').catch(() => '<unreadable>')
    fail(`S17: the work-item detail never rendered: ${err?.message}\nURL: ${page.url()}\nBody: ${JSON.stringify(body.slice(0, 600))}`)
  })
  const body = await detail.textContent()
  // The hostile goal markup, the fullwidth lookalike tag and the
  // hostile event payload are all visible as TEXT.
  assert(body.includes('alert(1)'), 'the hostile goal markup is not shown as text')
  assert(body.includes('＜script＞'), 'the fullwidth lookalike tag is not shown as text')
  assert(body.includes('alert(9)'), 'the hostile event payload is not shown as text')
  // The injected script tag stays escaped text, never an element.
  const scripts = await page.evaluate(() => [...document.querySelectorAll('script')].filter((s) => !s.src).length)
  assert(scripts === 0, 'an inline script element exists in the rendered document')
  assert(!dialog, `a dialog fired: ${dialog?.message}`)
  for (const url of requests) {
    assert(url.startsWith(baseURL), `the page fired an external request: ${url}`)
  }
}

// ---------------------------------------------------------------------------
// S18: the collapsible sidebar with icon+text entries, the top bar
// with the project switcher and search box, the token layer (CSS
// variables, #103 type colour tokens, the 72ch prose limit), the
// compact work-item table with status pills — and no inline
// script/style element or style attribute anywhere.
async function scenarioLayout(page) {
  await page.goto('/web/')
  const sidebar = page.getByTestId('sidebar')
  await sidebar.waitFor()
  for (const label of ['Work items', 'Memories', 'Machines']) {
    const entry = sidebar.getByTitle(label)
    assert(await entry.count() === 1, `the sidebar misses the ${label} entry`)
    assert(await entry.locator('svg').count() === 1, `the ${label} entry carries no inline SVG icon`)
    assert((await entry.textContent()) === label, `the ${label} entry carries no text label next to its icon`)
  }
  // The collapse toggle: collapsing hides the text labels but keeps
  // the icons; expanding brings the labels back.
  const toggle = page.getByTestId('sidebar-toggle')
  assert(await toggle.count() === 1, 'the sidebar carries no collapse toggle')
  const workItems = sidebar.getByTitle('Work items')
  await toggle.click()
  assert((await workItems.textContent()) === '', 'collapsing the sidebar did not hide the entry text')
  assert(await workItems.locator('svg').count() === 1, 'collapsing the sidebar dropped the icons')
  await toggle.click()
  assert((await workItems.textContent()) === 'Work items', 'expanding the sidebar did not restore the entry text')

  await page.getByTestId('topbar').waitFor()
  assert(await page.getByTestId('project-switcher').count() === 1, 'no project switcher in the top bar')
  assert(await page.getByTestId('search-box').count() === 1, 'no search box in the top bar')

  const violations = await page.evaluate(() => {
    const inline = [...document.querySelectorAll('script')].filter((s) => !s.src).length +
      [...document.querySelectorAll('style')].length
    const styled = [...document.querySelectorAll('*')].filter((el) => el.getAttribute('style') !== null).length
    return { inline, styled }
  })
  assert(violations.inline === 0, 'the document carries an inline script or style element')
  assert(violations.styled === 0, 'an element carries an inline style attribute')
  // The token layer shipped: the stylesheet defines the CSS variables,
  // the #103 type colour tokens and the 72ch prose limit.
  const cssText = await page.evaluate(() => {
    let text = ''
    for (const sheet of document.styleSheets) {
      for (const rule of sheet.cssRules) text += rule.cssText
    }
    return text
  })
  assert(cssText.includes('--bg') && cssText.includes('prefers-color-scheme'), 'the CSS-variable dark-theme tokens are absent')
  assert(cssText.includes('72ch'), 'the 72ch prose limit is absent')
  assert(cssText.includes('.t-fact') && cssText.includes('.t-experience'), 'the #103 type colour token rules are absent')

  // The work-item list is a compact table with status pills.
  await page.goto(`/web/${enc(fixtures.project)}/work-items`)
  const list = page.getByTestId('work-items')
  await list.waitFor()
  assert(await list.locator('table').count() === 1, 'the work-item list is not a table')
  assert(await list.getByTestId('work-item-row').count() > 0, 'the work-item table has no rows')
  const pills = await list.locator('[data-testid^="status-"]').count()
  assert(pills > 0, 'the work-item table carries no status pills')
}

// ---------------------------------------------------------------------------
// S19: the search box is disabled on the picker and runs nothing; with
// a project, the grouped page answers and the see-all links carry the
// query into the module.
async function scenarioSearch(page) {
  await page.goto('/web/')
  const box = page.getByTestId('search-box')
  await box.waitFor()
  assert(await box.isDisabled(), 'the search box is enabled before a project is selected')
  // A disabled box can take no input at all, so nothing can run; the
  // picker URL stands.
  assert(page.url() === `${baseURL}/web/`, 'the picker navigated without a selected project')

  await page.goto(`/web/${enc(fixtures.project)}/search?q=needle`)
  const wiGroup = page.getByTestId('search')
  await wiGroup.waitFor()
  assert(await page.getByTestId('see-all-work-items').count() === 1, 'no work-items see-all link')
  assert(await page.getByTestId('see-all-memories').count() === 1, 'no memories see-all link')
  const wiHref = await page.getByTestId('see-all-work-items').getAttribute('href')
  assert(wiHref === `/web/${enc(fixtures.project)}/work-items?q=needle`, `the see-all link lost the query: ${wiHref}`)
  await page.getByTestId('see-all-work-items').click()
  await page.waitForURL(`**/work-items?q=needle`)
  assert(await page.getByTestId('work-items').count() === 1, 'the see-all link does not open the module list')
}

// ---------------------------------------------------------------------------
// S20: machine cards render the heartbeat bands (green / yellow /
// red, stale marks, superseded mark) and never refresh themselves —
// proven deterministically: the page runs under a fake clock advanced
// five minutes (well past any polling interval), and neither a
// /web/api request nor a DOM change may happen.
async function scenarioMachines(page) {
  await page.clock.install()
  await page.goto('/web/machines')
  const cards = page.getByTestId('machine-card')
  await cards.first().waitFor()
  const bands = await cards.evaluateAll((els) =>
    els.map((el) => ({
      id: el.getAttribute('data-machine-id'),
      band: el.querySelector('[data-testid="machine-band"]').className,
      text: el.textContent,
    })),
  )
  assert(bands.length === 3, `expected 3 machine cards, got ${bands.length}`)
  const byID = Object.fromEntries(bands.map((b) => [b.id, b]))
  assert(byID['web-green'] && byID['web-green'].band.includes('green'), `the 1-minute machine is not green: ${JSON.stringify(byID)}`)
  assert(byID['web-yellow'] && byID['web-yellow'].band.includes('amber'), `the 20-minute machine is not yellow: ${JSON.stringify(byID)}`)
  assert(byID['web-red'] && byID['web-red'].band.includes('red'), `the 2-hour machine is not red: ${JSON.stringify(byID)}`)
  assert(byID['web-yellow'].text.includes('stale'), 'the yellow machine carries no stale mark')
  assert(byID['web-red'].text.includes('stale'), 'the red machine carries no stale mark')
  assert(byID['web-red'].text.includes('superseded'), 'the superseded machine is not marked')
  // Snapshot: after the load, five minutes of fake time pass. A page
  // with any polling timer would fire it here (and re-fetch); a page
  // with none changes nothing.
  const apiCalls = []
  const onRequest = (r) => {
    if (new URL(r.url()).pathname.startsWith('/web/api/')) apiCalls.push(r.url())
  }
  page.on('request', onRequest)
  const before = await page.textContent('[data-testid="machines"]')
  await page.clock.fastForward(5 * 60_000)
  const after = await page.textContent('[data-testid="machines"]')
  page.off('request', onRequest)
  assert(apiCalls.length === 0, `the machines page polled on its own: ${JSON.stringify(apiCalls)}`)
  assert(before === after, 'the machines page refreshed itself without a reload')
}

// ---------------------------------------------------------------------------
// S21: the memory module carries the #103 visual spec — date-grouped
// zebra rows with type-colour dots and tag chips, the refine-free
// zero-hit empty state, and the detail header card with the h1 title
// and the monospace id footer.
async function scenarioMemoryVisual(page) {
  await page.goto(`/web/${enc(fixtures.project)}/memories`)
  const list = page.getByTestId('memories')
  await list.waitFor()
  const groupText = await list.textContent()
  assert(groupText.includes('Today') || groupText.includes('Yesterday') || groupText.includes('Earlier'),
    'the memory list shows no date group label')
  assert(await list.locator('.type-dot').count() > 0, 'the list shows no type-colour dots')
  assert(await list.locator('.zebra').count() > 0, 'the list is not a zebra table')
  assert(await list.locator('.tag-chip').count() > 0, 'the list rows show no tag chips')

  // A zero-hit keyword answers a guiding empty state, not a blank.
  await page.goto(`/web/${enc(fixtures.project)}/memories?q=w105nothing-matches-this`)
  const empty = page.getByTestId('empty-search')
  await empty.waitFor()
  const emptyText = (await empty.textContent()).toLowerCase()
  assert(emptyText.includes('refine') && emptyText.includes('clear'),
    `the zero-hit empty state does not suggest refining or clearing the query: ${emptyText}`)

  // The detail: the h1 title and the monospace id footer carry the
  // exact memory the row linked to.
  await page.goto(`/web/${enc(fixtures.project)}/memories`)
  const row = page.getByTestId('memory-row').first()
  await row.waitFor()
  const link = row.locator('a').first()
  const href = await link.getAttribute('href')
  const wantID = decodeURIComponent(href.split('/').pop())
  const wantTitle = await link.textContent()
  await link.click()
  const detail = page.getByTestId('memory-detail')
  await detail.waitFor()
  assert(await detail.locator('header').count() === 1, 'the memory detail carries no header card')
  assert((await detail.locator('h1').textContent()) === wantTitle,
    'the detail page title is not the memory title in its h1')
  const footer = detail.getByTestId('memory-id-footer')
  assert(await footer.count() === 1, 'the memory detail carries no id footer')
  const footerClass = await footer.getAttribute('class')
  assert((footerClass ?? '').includes('id-footer'), `the id footer is not the monospace id style: ${footerClass}`)
  assert((await footer.textContent()) === wantID, 'the id footer does not show the exact memory id')
}

// ---------------------------------------------------------------------------
// S22: links built from a project name and object ids containing
// ?, #, /, %, space and non-ASCII resolve to the EXACT object.
async function scenarioEscapedLinks(page) {
  const project = fixtures.odd_project
  await page.goto(`/web/${enc(project)}/work-items`)
  const link = page.getByTestId('work-item-row').first().locator('a')
  await link.waitFor()
  const href = await link.getAttribute('href')
  const expected = `/web/${enc(project)}/work-items/${enc(fixtures.odd_wi)}`
  assert(href === expected, `the generated href is ${href}, want ${expected}`)
  await link.click()
  const detail = page.getByTestId('work-item-detail')
  await detail.waitFor()
  const goal = await detail.locator('h1').textContent()
  assert(goal === fixtures.odd_goal, `the escaped link resolved to ${goal}, want ${fixtures.odd_goal}`)
  // The memory link round-trips the same way and opens the EXACT
  // memory: the h1 is its title and the footer its id.
  await page.goto(`/web/${enc(project)}/memories`)
  const memLink = page.getByTestId('memory-row').first().locator('a')
  await memLink.waitFor()
  const memHref = await memLink.getAttribute('href')
  assert(memHref === `/web/${enc(project)}/memories/${enc(fixtures.odd_mem)}`,
    `the odd memory href is ${memHref}, want the exact escaped id`)
  await memLink.click()
  const memDetail = page.getByTestId('memory-detail')
  await memDetail.waitFor()
  assert((await memDetail.locator('h1').textContent()) === fixtures.odd_mem_title,
    'the odd memory link did not open the exact memory (title mismatch)')
  assert((await memDetail.getByTestId('memory-id-footer').textContent()) === fixtures.odd_mem,
    'the odd memory link did not open the exact memory (id mismatch)')
}

function enc(segment) {
  return encodeURIComponent(segment)
}
