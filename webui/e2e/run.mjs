// run.mjs — the console's browser acceptance runner,
// rewritten against the console's visual contract.
//
// Drives the browser-rendered scenarios of artifacts/e2e-scenarios.md
// (hostile corpus inert, the one visual language and its four-step type
// hierarchy, the disabled search box, computed-style contrast and
// heartbeat band colours in both themes, the #103 memory visual spec,
// system typography and tabular figures, two-width layout without
// horizontal overflow, and escaped links) against the REAL aihub
// binary over loopback, in a real chromium.
//
// The authored new-behaviour scenarios are S18 (visual hierarchy),
// S20a (WCAG contrast and band colours), S21a (system typography and
// tabular figures) and S22a (no horizontal overflow, narrow icon rail).
// S17/S19/S20b/S21b/S22b keep the v1 semantic coverage. Every
// data-derived assertion is preceded by the bounded waitForContent on
// the same element; there is no fixed sleep anywhere in this file.
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

import { readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

// The built asset tree, resolved from this file (never the cwd).
const distAssets = fileURLToPath(new URL('../dist/assets/', import.meta.url))

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

// waitForContent waits, with a bound, for the data-derived content a
// scenario is about to assert. Work-items, search and memories render
// their container testid while a Loading…/Searching… placeholder is
// still showing, so waiting only for the container loses the race
// against a late API answer. A timeout throws the caller's own
// assertion message, so the scenario still fails naming the check it
// could not observe.
async function waitForContent(locator, message) {
  try {
    await locator.first().waitFor({ timeout: 10_000 })
  } catch {
    throw new Error(message)
  }
}

// typeLevels reads the computed typography facts for the named
// selectors on the current page; a missing element answers null so the
// caller names the check it could not observe.
async function typeLevels(page, selectors) {
  return page.evaluate((sels) => {
    const out = {}
    for (const [name, sel] of Object.entries(sels)) {
      const el = document.querySelector(sel)
      if (!el) {
        out[name] = null
        continue
      }
      const cs = getComputedStyle(el)
      out[name] = {
        size: parseFloat(cs.fontSize),
        family: cs.fontFamily,
        variant: cs.fontVariantNumeric,
        maxWidth: cs.maxWidth,
        width: el.getBoundingClientRect().width,
      }
    }
    return out
  }, selectors)
}

// colourAudit measures computed colours, never source tokens (the
// spec's external oracle). With mode 'text' it answers each selector's
// text colour and its WCAG contrast ratio against the effective
// background. With mode 'semantic' it answers the highest-chroma colour
// the element (and its descendants) renders — a dot, bar, border or
// glyph, the presentation form is free — plus its hue and its ratio
// against the surface behind it; an optional labelPattern maps each
// element to the machine named in its nearest machine card.
async function colourAudit(page, selectors, mode, labelPattern) {
  return page.evaluate(({ sels, mode, labelPattern }) => {
    const parse = (s) => {
      const m = s.match(/rgba?\(([^)]+)\)/)
      if (!m) return null
      const p = m[1].split(',').map(Number)
      return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 }
    }
    const luminance = (c) => {
      const f = (v) => {
        v /= 255
        return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4)
      }
      return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b)
    }
    const ratio = (a, b) => {
      const x = luminance(a)
      const y = luminance(b)
      return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)
    }
    const chroma = (c) => (Math.max(c.r, c.g, c.b) - Math.min(c.r, c.g, c.b)) / 255
    const hue = (c) => {
      const max = Math.max(c.r, c.g, c.b)
      const min = Math.min(c.r, c.g, c.b)
      const d = max - min
      if (d === 0) return null
      let h
      if (max === c.r) h = ((c.g - c.b) / d) % 6
      else if (max === c.g) h = (c.b - c.r) / d + 2
      else h = (c.r - c.g) / d + 4
      h *= 60
      return h < 0 ? h + 360 : h
    }
    const background = (node) => {
      let cur = node
      while (cur) {
        const c = parse(getComputedStyle(cur).backgroundColor)
        if (c && c.a > 0.5) return c
        cur = cur.parentElement
      }
      return { r: 255, g: 255, b: 255, a: 1 }
    }
    const re = labelPattern ? new RegExp(labelPattern) : null
    const out = []
    for (const [name, sel] of sels) {
      const els = mode === 'text' ? [document.querySelector(sel)].filter(Boolean) : [...document.querySelectorAll(sel)]
      if (els.length === 0) {
        out.push({ name, element: false })
        continue
      }
      for (const el of els) {
        const label = re
          ? ((el.closest('[data-testid="machine-card"]')?.textContent ?? '').match(re) ?? ['unknown'])[0]
          : name
        if (mode === 'text') {
          const cs = getComputedStyle(el)
          out.push({ name, label, element: true, colour: cs.color, ratio: ratio(parse(cs.color), background(el)) })
          continue
        }
        let best = null
        for (const node of [el, ...el.querySelectorAll('*')]) {
          const cs = getComputedStyle(node)
          for (const value of [cs.color, cs.backgroundColor, cs.borderTopColor, cs.borderRightColor, cs.borderBottomColor, cs.borderLeftColor]) {
            const c = parse(value)
            if (!c || c.a < 0.5) continue
            const ch = chroma(c)
            if (best === null || ch > best.chroma) best = { rgb: c, chroma: ch }
          }
        }
        const bg = background(el.parentElement ?? el)
        out.push(best === null
          ? { name, label, element: true, chroma: 0, hue: null, ratio: 0 }
          : { name, label, element: true, chroma: best.chroma, hue: hue(best.rgb), ratio: ratio(best.rgb, bg) })
      }
    }
    return out
  }, { sels: selectors, mode, labelPattern: labelPattern ?? null })
}



// KIND_HOOKS are the eight memory kind hooks the driver seeds one memory
// for: the four kinds, the three named doc.* types and a plain doc.
const KIND_HOOKS = ['experience', 'fact', 'rule', 'methodology', 'decision', 'design', 'review', 'doc']

// KIND_MIN_DISTANCE is how far apart (RGB euclidean distance, 0–441)
// two kind colours must sit to count as distinct, not just unequal.
const KIND_MIN_DISTANCE = 32

function rgbDistance(a, b) {
  return Math.hypot(a.r - b.r, a.g - b.g, a.b - b.b)
}

// kindAudit answers, for every element carrying the .t-<kind> hook in
// the memory list, the colour that hook puts on screen: the computed
// colours of the element and its descendants are read with the hook
// class present and again with it removed (then restored at once), and
// the first opaque colour that changes is the hook's. rgb is null when
// the hook changes nothing; ratio is that colour against the surface.
async function kindAudit(page, kind) {
  return page.evaluate((k) => {
    const parse = (s) => {
      const m = s.match(/rgba?\(([^)]+)\)/)
      if (!m) return null
      const p = m[1].split(',').map(Number)
      return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 }
    }
    const luminance = (c) => {
      const f = (v) => {
        v /= 255
        return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4)
      }
      return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b)
    }
    const background = (node) => {
      let cur = node
      while (cur) {
        const c = parse(getComputedStyle(cur).backgroundColor)
        if (c && c.a > 0.5) return c
        cur = cur.parentElement
      }
      return { r: 255, g: 255, b: 255, a: 1 }
    }
    const props = ['color', 'backgroundColor', 'borderTopColor', 'borderRightColor', 'borderBottomColor', 'borderLeftColor', 'fill', 'stroke']
    const snapshot = (el) => [el, ...el.querySelectorAll('*')].flatMap((n) => {
      const cs = getComputedStyle(n)
      return props.map((p) => cs[p])
    })
    const out = []
    for (const el of document.querySelectorAll(`[data-testid="memories"] .t-${k}`)) {
      const withHook = snapshot(el)
      el.classList.remove(`t-${k}`)
      const without = snapshot(el)
      el.classList.add(`t-${k}`)
      let rgb = null
      for (let i = 0; i < withHook.length; i++) {
        const c = parse(withHook[i])
        if (withHook[i] !== without[i] && c && c.a >= 0.5) {
          rgb = c
          break
        }
      }
      if (rgb === null) {
        out.push({ rgb: null, ratio: 0 })
        continue
      }
      const bg = background(el.parentElement ?? el)
      const x = luminance(rgb)
      const y = luminance(bg)
      out.push({ rgb, ratio: (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05) })
    }
    return out
  }, kind)
}

// overflowOf answers how far the document and body scroll past the
// viewport (0 means no horizontal scroll).
async function overflowOf(page) {
  return page.evaluate(() => ({
    doc: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    body: document.body.scrollWidth - document.body.clientWidth,
  }))
}

// waitForPageData waits, with a bound, for the data-derived content of a
// page before its layout is measured, so a late API answer never makes
// the measurement meaningless.
async function waitForPageData(page, testid, label) {
  const waits = {
    'work-items': () => page.getByTestId('work-item-row').first(),
    memories: () => page.getByTestId('memory-row').first(),
    'work-item-detail': () => page.getByTestId('work-item-detail').locator('h1'),
    'memory-detail': () => page.getByTestId('memory-detail').locator('h1'),
    machines: () => page.getByTestId('machine-card').first(),
    search: () => page.getByTestId('see-all-work-items').first(),
    picker: () => page.getByTestId('picker').locator('a').first(),
  }
  const wait = waits[testid]
  if (wait) await waitForContent(wait(), `${label} never rendered its data-derived content`)
}

// scenarios: [name, fn(page)] — run in order. Every scenario runs even
// after an earlier one fails, so one run names every red bar; the run
// exits non-zero with each failing scenario and its reason.
const scenarios = [
  ['S17 hostile corpus renders inert', scenarioHostile],
  ['S18 one visual language with a four-step type hierarchy', scenarioVisualHierarchy],
  ['S19 search disabled until a project is selected', scenarioSearch],
  ['S20a computed contrast and heartbeat band colours in both themes', scenarioContrast],
  ['S20b machine bands follow the API threshold, no refresh', scenarioMachines],
  ['S21a system typography, tabular figures and no font asset', scenarioTypography],
  ['S21b memory module carries the #103 visual spec', scenarioMemoryVisual],
  ['S22a no horizontal overflow and a functional narrow rail', scenarioOverflow],
  ['S22b links from escaping names resolve to the exact object', scenarioEscapedLinks],
]

const browser = await chromium.launch()
const failures = []
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
      console.log(`FAIL ${name}`)
      failures.push(`${name}: ${err?.stack ?? err}\nCurrent URL: ${page.url()}`)
    }
  }
} finally {
  await browser.close()
}
for (const message of failures) {
  console.error(`browser suite: ${message}`)
}
if (failures.length > 0) {
  process.exitCode = 1
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
// S18 (new behaviour): one visual language across the
// page set. The retained v1 chrome coverage stays here (the collapsible
// sidebar with icon+text entries, the top bar, zero inline styling); the
// new contract adds a four-step type hierarchy (page title > section
// heading > body > muted/caption), a table for every list and a card
// surface for the detail/machine views. The type ladder is the
// red-at-baseline check: v1 renders section headings at the body size,
// so the fourth step does not exist.
async function scenarioVisualHierarchy(page) {
  // Retained chrome: the collapsible sidebar with icon + text entries,
  // the top bar with the project switcher and search box, and no inline
  // script/style element or style attribute anywhere.
  await page.goto('/web/')
  const sidebar = page.getByTestId('sidebar')
  await sidebar.waitFor()
  for (const label of ['Work items', 'Memories', 'Machines']) {
    const entry = sidebar.getByTitle(label)
    assert(await entry.count() === 1, `the sidebar misses the ${label} entry`)
    assert(await entry.locator('svg').count() === 1, `the ${label} entry carries no inline SVG icon`)
    assert((await entry.textContent()) === label, `the ${label} entry carries no text label next to its icon`)
  }
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
  const violations = await page.evaluate(() => ({
    inline: [...document.querySelectorAll('script')].filter((s) => !s.src).length + document.querySelectorAll('style').length,
    styled: [...document.querySelectorAll('*')].filter((el) => el.getAttribute('style') !== null).length,
  }))
  assert(violations.inline === 0, 'the document carries an inline script or style element')
  assert(violations.styled === 0, 'an element carries an inline style attribute')

  // The four-step type hierarchy on the memory list: page title, section
  // (date-group) heading, body text and the muted/caption chip level.
  await page.goto(`/web/${enc(fixtures.project)}/memories`)
  const list = page.getByTestId('memories')
  await list.waitFor()
  await waitForContent(list.getByTestId('memory-row'), 'the memory list has no rows')
  await waitForContent(list.locator('.tag-chip'), 'the memory list rows carry no tag chips')
  const levels = await typeLevels(page, {
    title: '[data-testid="memories"] h1',
    section: '[data-testid="memories"] h2, [data-testid="memories"] h3',
    body: '[data-testid="memories"] td',
    caption: '[data-testid="memories"] .tag-chip',
  })
  for (const name of ['title', 'section', 'body', 'caption']) {
    assert(levels[name] !== null, `the memory list has no ${name} text level`)
  }
  const size = { title: levels.title.size, section: levels.section.size, body: levels.body.size, caption: levels.caption.size }
  assert(size.title > size.section, `the page title (${size.title}px) is not larger than the section heading (${size.section}px)`)
  assert(size.section > size.body, `the section heading (${size.section}px) is not larger than body text (${size.body}px)`)
  assert(size.body > size.caption, `body text (${size.body}px) is not larger than the muted/caption level (${size.caption}px)`)

  // Retained list shape: every list is one table with rows.
  await page.goto(`/web/${enc(fixtures.project)}/work-items`)
  const items = page.getByTestId('work-items')
  await items.waitFor()
  await waitForContent(items.locator('table'), 'the work-item list is not a table')
  await waitForContent(items.getByTestId('work-item-row'), 'the work-item table has no rows')
  assert(await items.locator('table').count() === 1, 'the work-item list is not exactly one table')
  assert(await items.getByTestId('work-item-row').count() > 0, 'the work-item table has no rows')

  // Detail and machine views are bounded card surfaces.
  await page.goto(`/web/${enc(fixtures.project)}/memories`)
  await waitForContent(page.getByTestId('memory-row'), 'the memory list has no rows to open')
  await page.getByTestId('memory-row').first().locator('a').first().click()
  const detail = page.getByTestId('memory-detail')
  await waitForContent(detail.locator('header'), 'the memory detail carries no header card')
  assert(await detail.locator('header').count() === 1, 'the memory detail carries no header card')
  await page.goto('/web/machines')
  const cards = page.getByTestId('machine-card')
  await waitForContent(cards, 'the machines page has no machine cards')
  const surfaces = await page.evaluate(() => {
    const pageBg = getComputedStyle(document.body).backgroundColor
    return [...document.querySelectorAll('[data-testid="machine-card"]')].map((el) => {
      const cs = getComputedStyle(el)
      return (parseFloat(cs.borderTopWidth) > 0 && cs.borderTopStyle !== 'none') || parseFloat(cs.borderTopLeftRadius) > 0 || cs.backgroundColor !== pageBg
    })
  })
  assert(surfaces.length > 0 && surfaces.every(Boolean), 'a machine card has no card surface (border, radius or distinct background)')
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
  await waitForContent(page.getByTestId('see-all-work-items'), 'no work-items see-all link')
  assert(await page.getByTestId('see-all-work-items').count() === 1, 'no work-items see-all link')
  await waitForContent(page.getByTestId('see-all-memories'), 'no memories see-all link')
  assert(await page.getByTestId('see-all-memories').count() === 1, 'no memories see-all link')
  const wiHref = await page.getByTestId('see-all-work-items').getAttribute('href')
  assert(wiHref === `/web/${enc(fixtures.project)}/work-items?q=needle`, `the see-all link lost the query: ${wiHref}`)
  await page.getByTestId('see-all-work-items').click()
  await page.waitForURL(`**/work-items?q=needle`)
  await waitForContent(page.getByTestId('work-items'), 'the see-all link does not open the module list')
  assert(await page.getByTestId('work-items').count() === 1, 'the see-all link does not open the module list')
}

// ---------------------------------------------------------------------------
// S20a (new behaviour): every foreground colour meets the
// WCAG ratio in BOTH themes, measured from computed styles — text at
// least 4.5:1, every memory kind colour at least 3:1 and distinct from
// every other kind — and the three
// heartbeat bands resolve to distinguishable green / amber / red hues
// that each clear the non-text 3:1 floor. The band check is the
// red-at-baseline one: v1 renders all three bands in the same
// foreground colour.
async function scenarioContrast(page) {
  const theme = {}
  for (const scheme of ['light', 'dark']) {
    await page.emulateMedia({ colorScheme: scheme })

    await page.goto(`/web/${enc(fixtures.project)}/memories`)
    const list = page.getByTestId('memories')
    await list.waitFor()
    theme[scheme] = await page.evaluate(() => ({
      bg: getComputedStyle(document.body).backgroundColor,
      fg: getComputedStyle(document.body).color,
    }))
    await waitForContent(list.getByTestId('memory-row'), `the ${scheme} memory list has no rows`)
    await waitForContent(list.locator('.tag-chip'), `the ${scheme} memory list rows carry no tag chips`)
    await waitForContent(list.locator('.t-fact'), `the ${scheme} memory list carries no .t-<kind> hook`)
    const text = await colourAudit(page, [
      ['body', '[data-testid="memories"] td'],
      ['muted', '[data-testid="memories"] .tag-chip'],
    ], 'text')
    for (const m of text) {
      assert(m.element, `the ${scheme} memory list has no ${m.name} text to measure`)
      assert(m.ratio >= 4.5, `the ${scheme} ${m.name} text contrast is ${m.ratio.toFixed(2)}:1 (want >= 4.5:1)`)
    }

    // Every kind hook renders its own colour: each of the eight hooks
    // clears 3:1 against its surface, all marks of one kind agree, and
    // two kinds never resolve to the same colour.
    const kinds = {}
    for (const k of KIND_HOOKS) {
      await waitForContent(list.locator(`.t-${k}`), `the ${scheme} memory list carries no .t-${k} hook`)
      const marks = await kindAudit(page, k)
      for (const m of marks) {
        assert(m.rgb, `the ${scheme} .t-${k} hook changes no rendered colour (the colour does not come from the hook)`)
        assert(m.ratio >= 3, `the ${scheme} .t-${k} kind colour contrast is ${m.ratio.toFixed(2)}:1 (want >= 3:1)`)
        assert(rgbDistance(m.rgb, marks[0].rgb) === 0, `the ${scheme} .t-${k} marks resolve different colours: ${JSON.stringify(marks)}`)
      }
      kinds[k] = marks[0].rgb
    }
    for (const [i, a] of KIND_HOOKS.entries()) {
      for (const b of KIND_HOOKS.slice(i + 1)) {
        const d = rgbDistance(kinds[a], kinds[b])
        assert(d >= KIND_MIN_DISTANCE,
          `the ${scheme} kinds ${a} and ${b} resolve the same colour (${JSON.stringify(kinds[a])} vs ${JSON.stringify(kinds[b])}, distance ${d.toFixed(1)})`)
      }
    }

    await page.goto(`/web/${enc(fixtures.project)}/work-items`)
    const items = page.getByTestId('work-items')
    await items.waitFor()
    await waitForContent(items.locator('[data-testid^="status-"]'), `the ${scheme} work-item list carries no status pills`)
    const [pill] = await colourAudit(page, [['pill', '[data-testid^="status-"]']], 'text')
    assert(pill.element, `the ${scheme} work-item list has no status pill text to measure`)
    assert(pill.ratio >= 4.5, `the ${scheme} status pill text contrast is ${pill.ratio.toFixed(2)}:1 (want >= 4.5:1)`)

    await page.goto('/web/machines')
    await waitForContent(page.getByTestId('machine-card'), `the ${scheme} machines page has no machine cards`)
    const bands = await colourAudit(page, [['band', '[data-testid="machine-band"]']], 'semantic', 'web-(green|yellow|red)')
    assert(bands.length === 3, `expected 3 heartbeat bands in ${scheme}, got ${bands.length}`)
    for (const band of bands) {
      assert(band.chroma > 0.05, `the ${scheme} band for ${band.label} carries no colour (chroma ${band.chroma.toFixed(3)})`)
      assert(band.ratio >= 3, `the ${scheme} band for ${band.label} is ${band.ratio.toFixed(2)}:1 against its surface (want >= 3:1)`)
    }
    const green = bands.find((b) => b.label === 'web-green')
    const amber = bands.find((b) => b.label === 'web-yellow')
    const red = bands.find((b) => b.label === 'web-red')
    assert(green && green.hue >= 70 && green.hue <= 180, `the 1-minute machine's band is not green-hued: ${JSON.stringify(green)}`)
    assert(amber && amber.hue > 20 && amber.hue < 70, `the 20-minute machine's band is not amber-hued: ${JSON.stringify(amber)}`)
    assert(red && (red.hue >= 340 || red.hue <= 20), `the 2-hour machine's band is not red-hued: ${JSON.stringify(red)}`)
  }
  // Both themes resolve their own token set from the system preference.
  assert(theme.light.bg !== theme.dark.bg, `light and dark resolve the same page background: ${theme.light.bg}`)
  assert(theme.light.fg !== theme.dark.fg, `light and dark resolve the same foreground: ${theme.light.fg}`)
  await page.emulateMedia({ colorScheme: null })
}

// ---------------------------------------------------------------------------
// S20b: machine cards carry a heartbeat band element, the stale and
// superseded marks follow the API threshold, and the page never
// refreshes itself —
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
      band: el.querySelector('[data-testid="machine-band"]') !== null,
      text: el.textContent,
    })),
  )
  assert(bands.length === 3, `expected 3 machine cards, got ${bands.length}`)
  assert(bands.every((b) => b.band), 'a machine card carries no heartbeat band element')
  const cardOf = (id) => bands.find((b) => (b.text ?? '').includes(id))
  const yellow = cardOf('web-yellow')
  const red = cardOf('web-red')
  assert(yellow && yellow.text.includes('stale'), 'the yellow machine carries no stale mark')
  assert(red && red.text.includes('stale'), 'the red machine carries no stale mark')
  assert(red && red.text.includes('superseded'), 'the superseded machine is not marked')
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
// S21a (new behaviour): typography uses the system font
// stack and ships no font file; numeric columns use tabular figures; the
// id footer uses the monospace stack. Tabular figures are the
// red-at-baseline check: v1 leaves font-variant-numeric at `normal`.
async function scenarioTypography(page) {
  const fontRequests = []
  const onRequest = (r) => {
    if (/\.(woff2?|ttf|otf|eot)$/i.test(new URL(r.url()).pathname)) fontRequests.push(r.url())
  }
  page.on('request', onRequest)
  await page.goto(`/web/${enc(fixtures.project)}/work-items`)
  const items = page.getByTestId('work-items')
  await items.waitFor()
  await waitForContent(items.getByTestId('work-item-row'), 'the work-item table has no rows')
  const type = await typeLevels(page, { body: 'body' })
  assert(type.body !== null && /system-ui|-apple-system|PingFang|Noto Sans CJK|Segoe UI|sans-serif/.test(type.body.family),
    `body text does not resolve from the system font stack: ${type.body?.family}`)
  const numerals = await page.evaluate(() => {
    const cells = [...document.querySelectorAll('[data-testid="work-items"] table th, [data-testid="work-items"] table td')]
    return cells
      .filter((el) => /^[\d\s.,:%-]+$/.test((el.textContent ?? '').trim()) && /\d/.test(el.textContent ?? ''))
      .map((el) => ({ text: (el.textContent ?? '').trim().slice(0, 24), variant: getComputedStyle(el).fontVariantNumeric }))
  })
  assert(numerals.length > 0, 'the work-item table shows no numeric cells to measure tabular figures')
  for (const cell of numerals) {
    assert(cell.variant.includes('tabular-nums'), `the numeric cell "${cell.text}" is not set in tabular figures (font-variant-numeric: ${cell.variant})`)
  }

  await page.goto(`/web/${enc(fixtures.project)}/memories`)
  await waitForContent(page.getByTestId('memory-row'), 'the memory list has no rows to open')
  await page.getByTestId('memory-row').first().locator('a').first().click()
  await waitForContent(page.getByTestId('memory-detail'), 'the memory detail never rendered')
  const footer = await typeLevels(page, { id: '[data-testid="memory-id-footer"]' })
  assert(footer.id !== null, 'the memory detail carries no id footer')
  assert(/mono/i.test(footer.id.family), `the id footer is not in the monospace stack: ${footer.id.family}`)
  page.off('request', onRequest)

  assert(fontRequests.length === 0, `the console requested a font file: ${JSON.stringify(fontRequests)}`)
  let assets = []
  try {
    assets = readdirSync(distAssets)
  } catch {
    throw new Error(`the built asset tree is missing at ${distAssets} — build webui before running the suite`)
  }
  const shipped = assets.filter((name) => /\.(woff2?|ttf|otf|eot)$/i.test(name))
  assert(shipped.length === 0, `the built assets ship a font file: ${JSON.stringify(shipped)}`)
}

// ---------------------------------------------------------------------------
// S21b (retained v1 semantics, rewritten to the new contract's selector
// vocabulary): date groups, tag chips and status pills; the zero-hit
// empty state; the detail h1 and monospace id footer; and a prose
// measure no wider than 72ch. The freed presentation hooks (.zebra,
// .type-dot) are no longer asserted; the retained .t-<kind> hook is.
async function scenarioMemoryVisual(page) {
  await page.goto(`/web/${enc(fixtures.project)}/memories`)
  const list = page.getByTestId('memories')
  await list.waitFor()
  await waitForContent(list.getByTestId('memory-row'), 'the memory list shows no rows')
  const groupText = await list.textContent()
  assert(/Today|Yesterday|Earlier/.test(groupText), 'the memory list shows no date group label')
  await waitForContent(list.locator('.tag-chip'), 'the list rows show no tag chips')
  assert(await list.locator('.tag-chip').count() > 0, 'the list rows show no tag chips')
  await waitForContent(list.locator('.t-fact'), 'the list rows carry no .t-<kind> hook')
  const [kind] = await colourAudit(page, [['kind', '[data-testid="memories"] .t-fact']], 'semantic')
  assert(kind.element && kind.chroma > 0.05, `the memory kind hook resolves to no colour: ${JSON.stringify(kind)}`)

  // Work items render their status as a pill with its status-* testid.
  await page.goto(`/web/${enc(fixtures.project)}/work-items`)
  const items = page.getByTestId('work-items')
  await items.waitFor()
  await waitForContent(items.locator('[data-testid^="status-"]'), 'the work-item list carries no status pills')
  assert(await items.locator('[data-testid^="status-"]').count() > 0, 'the work-item list carries no status pills')

  // A zero-hit keyword answers a guiding empty state, not a blank.
  await page.goto(`/web/${enc(fixtures.project)}/memories?q=w105nothing-matches-this`)
  const empty = page.getByTestId('empty-search')
  await empty.waitFor()
  const emptyText = (await empty.textContent()).toLowerCase()
  assert(emptyText.includes('refine') && emptyText.includes('clear'),
    `the zero-hit empty state does not suggest refining or clearing the query: ${emptyText}`)

  // The detail: the h1 title, the monospace id footer with the exact id,
  // and a prose measure no wider than 72ch.
  await page.goto(`/web/${enc(fixtures.project)}/memories`)
  const row = page.getByTestId('memory-row').first()
  await waitForContent(row, 'the memory list has no rows to open')
  const link = row.locator('a').first()
  const href = await link.getAttribute('href')
  const wantID = decodeURIComponent(href.split('/').pop())
  const wantTitle = await link.textContent()
  await link.click()
  const detail = page.getByTestId('memory-detail')
  await waitForContent(detail, 'the memory detail never rendered')
  assert(await detail.locator('header').count() === 1, 'the memory detail carries no header card')
  assert((await detail.locator('h1').textContent()) === wantTitle,
    'the detail page title is not the memory title in its h1')
  const footer = detail.getByTestId('memory-id-footer')
  assert(await footer.count() === 1, 'the memory detail carries no id footer')
  const footerClass = await footer.getAttribute('class')
  assert((footerClass ?? '').includes('id-footer'), `the id footer lost its .id-footer hook: ${footerClass}`)
  assert((await footer.textContent()) === wantID, 'the id footer does not show the exact memory id')
  const prose = await page.evaluate(() => {
    const el = document.querySelector('[data-testid="sanitized-body"]')
    if (!el) return null
    const cs = getComputedStyle(el)
    const canvas = document.createElement('canvas')
    const ctx = canvas.getContext('2d')
    ctx.font = `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`
    const ch = ctx.measureText('0').width
    return { maxWidth: cs.maxWidth, width: el.getBoundingClientRect().width, ch }
  })
  assert(prose !== null, 'the memory detail carries no sanitized prose body')
  const limit = 72 * prose.ch
  if (prose.maxWidth !== 'none') {
    assert(parseFloat(prose.maxWidth) <= limit * 1.02, `the prose measure max-width is ${prose.maxWidth} (want <= 72ch)`)
  }
  assert(prose.width <= limit * 1.02, `the prose body renders ${prose.width}px wide (want <= 72ch = ${limit.toFixed(1)}px)`)
}

// ---------------------------------------------------------------------------
// S22a (new behaviour): no console page scrolls
// horizontally at 1280×800 or 375×812, and at the narrow width the
// sidebar is a functional icon rail (icons and accessible names stay,
// the visible text label hides) with the switcher, search and collapse
// control still present. v1's fixed 208px sidebar overflows a 375px
// viewport, so this is red at baseline.
async function scenarioOverflow(page) {
  const pages = [
    ['the picker', '/web/', 'picker'],
    ['the work-items list', `/web/${enc(fixtures.project)}/work-items`, 'work-items'],
    ['the work-item detail', `/web/${enc(fixtures.project)}/work-items/${enc(fixtures.hostile_wi)}`, 'work-item-detail'],
    ['the memories list', `/web/${enc(fixtures.project)}/memories`, 'memories'],
    ['the search results', `/web/${enc(fixtures.project)}/search?q=needle`, 'search'],
    ['the machines page', '/web/machines', 'machines'],
  ]
  for (const [width, height] of [[1280, 800], [375, 812]]) {
    await page.setViewportSize({ width, height })
    for (const [label, path, testid] of pages) {
      await page.goto(path)
      await page.getByTestId(testid).waitFor()
      await waitForPageData(page, testid, label)
      const box = await overflowOf(page)
      assert(box.doc <= 0 && box.body <= 0,
        `${label} scrolls horizontally at ${width}×${height}: document +${box.doc}px, body +${box.body}px`)
    }
    // The memory detail is reached through a list row and measured too.
    await page.goto(`/web/${enc(fixtures.project)}/memories`)
    await waitForContent(page.getByTestId('memory-row'), 'the memory list has no rows to open')
    await page.getByTestId('memory-row').first().locator('a').first().click()
    await waitForContent(page.getByTestId('memory-detail'), 'the memory detail never rendered')
    const box = await overflowOf(page)
    assert(box.doc <= 0 && box.body <= 0,
      `the memory detail scrolls horizontally at ${width}×${height}: document +${box.doc}px, body +${box.body}px`)
  }

  // 375: the sidebar is an icon rail — icons and accessible names stay,
  // the visible text label hides, and the shell's controls remain.
  await page.setViewportSize({ width: 375, height: 812 })
  await page.goto(`/web/${enc(fixtures.project)}/work-items`)
  await waitForContent(page.getByTestId('work-item-row'), 'the work-item list has no rows')
  for (const label of ['Work items', 'Memories', 'Machines']) {
    const entry = page.getByTestId('sidebar').locator(`[title="${label}"], [aria-label="${label}"]`)
    assert(await entry.count() === 1, `the narrow sidebar misses the ${label} entry with its accessible name`)
    assert(await entry.locator('svg').count() === 1, `the narrow ${label} entry carries no inline SVG icon`)
    assert((await entry.innerText()).trim() === '', `the narrow ${label} entry still shows its text label instead of an icon rail`)
  }
  for (const id of ['sidebar-toggle', 'project-switcher', 'search-box']) {
    assert(await page.getByTestId(id).count() === 1, `the narrow shell misses ${id}`)
  }
  await page.setViewportSize({ width: 1280, height: 800 })
}

// ---------------------------------------------------------------------------
// S22b: links built from a project name and object ids containing
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
