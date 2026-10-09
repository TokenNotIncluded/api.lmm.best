import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE)
const origin = 'http://127.0.0.1:4174'
const output = process.env.L0_BROWSER_OUTPUT
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const report = []
try {
  for (const [width, colorScheme] of [[1440, 'dark'], [390, 'light']]) {
    const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 1000 }, colorScheme, locale: 'en-US', serviceWorkers: 'block' })
    await context.addInitScript(() => {
      localStorage.setItem('i18nextLng', 'en')
      localStorage.setItem('lmm:source-consent:v2', 'no')
    })
    await context.route('**/*', async route => {
      const url = new URL(route.request().url())
      if (url.origin !== origin) return route.abort('blockedbyclient')
      if (url.pathname === '/api/status') return route.fulfill({ json: { success: true, data: { system_name: 'LMM Review', assistant: { enabled: false }, announcements_enabled: false } } })
      if (url.pathname.startsWith('/api/')) return route.abort('blockedbyclient')
      return route.continue()
    })
    const page = await context.newPage()
    page.setDefaultTimeout(15000)
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    try {
      await page.goto(`${origin}/getting-started?debug_persona=l0&console_review=1`, { waitUntil: 'domcontentloaded', timeout: 60000 })
      await page.getByTestId('persona-debug-trigger').waitFor({ timeout: 60000 })
      const reject = page.getByRole('button', { name: /^(Do not collect|不收集|拒绝收集)$/ })
      if (await reject.isVisible()) await reject.click()
      await page.evaluate(async () => {
        const { api } = await import('/src/lib/http-client.ts')
        const { useAuthStore } = await import('/src/stores/auth-store.ts')
        const user = useAuthStore.getState().auth.user
        if (user?.developer_access_granted !== false) throw new Error('Review requires an actual L0 persona')
        const original = api.defaults.adapter
        const state = window.l0Review = { creates: 0, chatCalls: 0, messages: [], request: null }
        api.defaults.adapter = async config => {
          const url = new URL(config.url, location.origin)
          const method = (config.method || 'get').toUpperCase()
          const ok = data => ({ data: { success: true, data }, status: 200, statusText: 'OK', headers: {}, config })
          if (url.pathname === '/api/assistant/registration-check') return ok({ state: 'ready' })
          if (url.pathname === '/api/assistant/chat') { state.chatCalls++; throw new Error('Support must not invoke AI') }
          if (url.pathname === '/api/assistant/support/eligibility') return ok({ eligible: false })
          if (url.pathname === '/api/assistant/support/self') return ok({ request: state.request })
          if (url.pathname === '/api/assistant/support' && method === 'POST') {
            const body = typeof config.data === 'string' ? JSON.parse(config.data) : config.data
            if (body.kind !== 'handoff' || body.conversation_id !== 0) throw new Error('Unexpected support request')
            state.creates++
            if (state.creates === 1) return { ...ok(null), data: { success: false, message: 'Synthetic temporary support failure' } }
            state.request = { id: 8, user_id: user.id, conversation_id: 91, kind: 'handoff', status: 'pending', topic: '', preferred_time: '', scheduled_at: 0, assigned_admin_id: 0, assigned_admin_name: '', created_at: 1, updated_at: 1, accepted_at: 0, closed_at: 0 }
            return ok({ request: state.request, created: true })
          }
          if (url.pathname === '/api/assistant/support/8') return ok({ request: state.request, messages: state.messages })
          if (url.pathname === '/api/assistant/support/8/messages') {
            const body = typeof config.data === 'string' ? JSON.parse(config.data) : config.data
            if (!body.client_turn_id) throw new Error('Support message needs an idempotency key')
            const message = { id: 1, role: 'user', content: body.content, created_at: 1 }
            state.messages.push(message)
            return ok({ message })
          }
          return original(config)
        }
      })
      await page.getByTestId('l0-contact-support').click()
      await page.waitForURL('**/support*')
      const support = page.getByTestId('support-conversation')
      await support.waitFor()
      assert.equal(await page.evaluate(() => window.l0Review.creates), 0)
      await page.screenshot({ path: path.join(output, `support-l0-${width}.png`), fullPage: true, animations: 'disabled' })
      await support.getByRole('button', { name: 'Transfer to human', exact: true }).click()
      await support.getByRole('alert').waitFor()
      await support.getByRole('button', { name: 'Transfer to human', exact: true }).click()
      await support.getByRole('textbox', { name: 'Message', exact: true }).fill('I use Linux and need help enabling L1.')
      await support.getByRole('button', { name: 'Send', exact: true }).click()
      await page.waitForFunction(() => window.l0Review.messages.length === 1)
      assert.equal(await support.getByRole('textbox', { name: 'Message', exact: true }).inputValue(), '')
      const state = await page.evaluate(() => ({ ...window.l0Review, width: innerWidth, scrollWidth: document.documentElement.scrollWidth }))
      assert.equal(state.creates, 2)
      assert.equal(state.chatCalls, 0)
      assert.ok(state.scrollWidth <= state.width, 'No horizontal overflow')
      assert.deepEqual(errors, [])
      await page.screenshot({ path: path.join(output, `support-pending-${width}.png`), fullPage: true, animations: 'disabled' })
      for (const destination of ['/getting-started', '/todos', '/pricing']) {
        await page.evaluate(to => { history.pushState({}, '', to); window.dispatchEvent(new PopStateEvent('popstate')) }, `${destination}?debug_persona=l0&console_review=1`)
        await page.waitForTimeout(1200)
        assert.equal(new URL(page.url()).pathname.replace(/\/$/, ''), destination)
        const text = await page.locator('body').innerText()
        assert.doesNotMatch(text, /Internal Server Error|Something went wrong|Access request rejected|Awaiting review|PERSONA_DEBUG_UNMOCKED_REQUEST/)
        assert.equal(await page.locator('[data-sonner-toast][data-type="error"]:visible').count(), 0)
      }
      report.push({ width, colorScheme, ok: true, errors, supportRequests: state.creates, aiCalls: state.chatCalls })
    } catch (error) {
      await page.screenshot({ path: path.join(output, `failure-${width}.png`), fullPage: true }).catch(() => {})
      report.push({ width, colorScheme, ok: false, errors, error: String(error.stack || error) })
    } finally { await context.close() }
  }
} finally {
  await browser.close()
  await writeFile(path.join(output, 'report.json'), JSON.stringify(report, null, 2))
}
console.log(JSON.stringify(report, null, 2))
assert.ok(report.length === 2 && report.every(item => item.ok), 'L0 browser checks failed; inspect the report')
