// README screenshots and a browser smoke test, against a demo instance (DEMO_DATA=true seeds three months of memos):
//
//   DATA_DIR=/tmp/voice-demo DEMO_DATA=true ADMIN_EMAIL=demo@example.com ADMIN_PASSWORD=demodemo1 ADMIN_NAME=Maya PORT=18307 ./pocket-voice
//   CDP=http://127.0.0.1:9333 BASE=http://127.0.0.1:18307 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                # checks only
// playwright-core comes from web/node_modules, or set PW_FROM to a package.json next to an install of it.
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(process.env.PW_FROM || new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18307'
const shots = process.env.SHOTS !== '0'
const dir = new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'demo@example.com', password: 'demodemo1' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
async function go(page, path, wait = 1500, frozen = true) {
  await page.goto(base + path, { waitUntil: 'networkidle' })
  // headless Chrome freezes CSS fade-ins: show everything at once
  if (frozen) await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; }' })
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(wait)
}
async function shot(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }
const memoId = async (page, q) => page.evaluate(t => fetch('/api/v1/search?q=' + encodeURIComponent(t)).then(r => r.json()).then(j => j.items[0].id), q)

// ---- desktop ----
{
  const { ctx, page } = await session()
  await go(page, '/', 2000)
  assert.ok((await page.content()).includes('Dentist, dry cleaning'), 'the inbox lists today’s memos')
  await shot(page, 'desktop-inbox')
  const school = await memoId(page, 'Jahorina')
  await go(page, `/memos/${school}`, 2000)
  assert.ok((await page.content()).includes('Found in this memo'), 'a memo shows what the AI found')
  await shot(page, 'desktop-memo')
  await go(page, '/memos', 1500)
  await shot(page, 'desktop-all')
  await go(page, '/settings/speech', 1500)
  await shot(page, 'desktop-speech')
  await go(page, '/dictation', 1500)
  const dictation = await page.content()
  assert.ok(dictation.includes('Time saved') && dictation.includes('Pocket Projects') && dictation.includes('dict-row'), 'the dictation page shows its numbers and the history')
  for (const p of ['/starred', '/dictation?app=Slack', '/kind/journal', '/tag/work', '/memos?q=pita', '/settings/apps', '/settings/notifications', '/settings/general', '/settings/data', '/settings/ai', '/settings/tokens', '/settings/about']) await go(page, p, 500)
  await ctx.close()
}

// ---- a meeting, dark ----
{
  const { ctx, page } = await session({ dark: true })
  const call = await memoId(page, 'launch week')
  await go(page, `/memos/${call}`, 2000)
  const meeting = await page.content()
  assert.ok(meeting.includes('Who\'s who') && meeting.includes('Is this <b>Nina</b>?') && meeting.includes('talk-chip'), 'a meeting shows its voices and who they are')
  await shot(page, 'desktop-meeting')
  await go(page, '/people', 800)
  assert.ok((await page.content()).includes('Lejla'), 'People lists the named voices')
  await ctx.close()
}

// ---- phone ----
{
  const { ctx, page } = await session({ phone: true })
  await go(page, '/', 2000)
  await shot(page, 'phone-inbox')
  const today = await memoId(page, 'dentist')
  await go(page, `/memos/${today}`, 2000)
  await shot(page, 'phone-memo')
  await ctx.close()
}
{
  const { ctx, page } = await session({ phone: true, dark: true })
  const walk = await memoId(page, 'Vrela Bosne')
  await go(page, `/memos/${walk}`, 2000)
  await page.evaluate(() => document.querySelectorAll('.segmented button')[1]?.click())
  await page.waitForTimeout(400)
  await shot(page, 'phone-memo-dark')
  await go(page, '/memos', 1500)
  await shot(page, 'phone-all-dark')
  await ctx.close()
}

await browser.close()
if (errors.length) {
  console.error('browser errors:\n' + errors.join('\n'))
  process.exit(1)
}
console.log('ok')
