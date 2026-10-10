#!/usr/bin/env node
/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { lstat, readFile, realpath, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import path from 'node:path'

const require = createRequire(import.meta.url)
const playwrightEntry = (() => {
  try {
    return require.resolve('playwright')
  } catch {
    const nodePrefix = path.resolve(path.dirname(process.execPath), '../lib')
    return require.resolve('playwright', { paths: [nodePrefix] })
  }
})()
const { chromium } = (await import(playwrightEntry)).default

const baseUrl = process.env.PERSONA_REVIEW_URL ?? 'http://127.0.0.1:4174'
const configuredWorkspace = process.env.PERSONA_DEPLOY_WORKSPACE
const configuredOutputDirectory = process.env.PERSONA_OUTPUT_DIR
const parsedBaseUrl = new URL(baseUrl)

if (
  parsedBaseUrl.protocol !== 'http:' ||
  parsedBaseUrl.hostname !== '127.0.0.1' ||
  parsedBaseUrl.port !== '4174'
) {
  throw new Error('PERSONA_REVIEW_URL must be exactly http://127.0.0.1:4174')
}
if (!configuredWorkspace || !configuredOutputDirectory) {
  throw new Error(
    'PERSONA_DEPLOY_WORKSPACE and PERSONA_OUTPUT_DIR are required'
  )
}

async function validateArtifactWorkspace(workspaceInput, outputInput) {
  if (!path.isAbsolute(workspaceInput) || !path.isAbsolute(outputInput)) {
    throw new Error('workspace and output directory must be absolute paths')
  }
  const workspace = await realpath(workspaceInput)
  const output = await realpath(outputInput)
  if (
    workspaceInput !== workspace ||
    outputInput !== output ||
    workspace === '/' ||
    workspace.startsWith('/tmp/') ||
    workspace.startsWith('/var/tmp/') ||
    output === workspace ||
    !output.startsWith(`${workspace}${path.sep}`)
  ) {
    throw new Error('workspace or output directory is unsafe')
  }
  for (const candidate of [workspace, output]) {
    const info = await lstat(candidate)
    if (!info.isDirectory() || info.isSymbolicLink()) {
      throw new Error('workspace paths must be real directories')
    }
  }
  const markerPath = path.join(workspace, '.lmm-deploy-workspace')
  const marker = await readFile(markerPath, 'utf8')
  if (
    !marker.includes('format=1\n') ||
    !marker.includes('role=controller\n') ||
    !marker.includes(`workspace=${workspace}\n`)
  ) {
    throw new Error('deployment workspace marker is missing or invalid')
  }
  return { workspace, output }
}

const artifactWorkspace = await validateArtifactWorkspace(
  configuredWorkspace,
  configuredOutputDirectory
)
const runId = `debug-personas-${Date.now()}-${process.pid}`
const reportPath = path.join(artifactWorkspace.output, `${runId}-report.json`)
const screenshotPath = (name) =>
  path.join(artifactWorkspace.output, `${runId}-${name}.png`)
const rawEmail = 'alice.debug@example.invalid'
const rawKey = 'sk-debugSecret987654321'
const homepageMessage = `Please help configure the SDK; my email is ${rawEmail} and api key: ${rawKey}`
const safeHomepageMessage =
  'Please help configure the SDK; my email is [REDACTED_EMAIL] and [REDACTED_CREDENTIAL]'

function createEvidence(persona, viewport) {
  return {
    persona,
    viewport,
    assertions: [],
    failedRequests: [],
    pageErrors: [],
    externalRequests: [],
  }
}

function record(evidence, name, pass, detail = '') {
  evidence.assertions.push({ name, pass, detail })
  if (!pass) throw new Error(`${name}${detail ? `: ${detail}` : ''}`)
}

async function createContext(browser, persona, viewport) {
  const context = await browser.newContext({
    serviceWorkers: 'block',
    viewport,
  })
  const evidence = createEvidence(persona, viewport)
  await context.route('**/*', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.origin !== parsedBaseUrl.origin) {
      evidence.externalRequests.push({
        method: request.method(),
        origin: url.origin,
        path: url.pathname,
      })
      await route.abort('blockedbyclient')
      return
    }
    if (url.pathname === '/api/status') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: {
            system_name: 'LMM Persona Lab',
            logo: '/logo.png',
            assistant: { enabled: true },
            announcements_enabled: false,
          },
        }),
      })
      return
    }
    if (url.pathname.startsWith('/api/')) {
      evidence.failedRequests.push({
        method: request.method(),
        path: url.pathname,
        reason: 'unexpected network API request',
      })
      await route.abort('blockedbyclient')
      return
    }
    await route.continue()
  })
  const page = await context.newPage()
  page.on('pageerror', (error) => {
    evidence.pageErrors.push(String(error).slice(0, 500))
  })
  page.on('requestfailed', (request) => {
    const url = new URL(request.url())
    if (
      url.origin === parsedBaseUrl.origin &&
      url.pathname.startsWith('/api/')
    ) {
      evidence.failedRequests.push({
        method: request.method(),
        path: url.pathname,
        reason: request.failure()?.errorText ?? 'request failed',
      })
    }
  })
  return { context, page, evidence }
}

async function openDebugPanel(page) {
  await page.getByTestId('persona-debug-trigger').click()
  await page.getByTestId('persona-debug-panel').waitFor({ state: 'visible' })
}

async function selectPersona(page, persona) {
  await openDebugPanel(page)
  await page.getByTestId(`persona-debug-option-${persona}`).click()
  await page.waitForFunction(
    (expected) =>
      document
        .querySelector('[data-testid="persona-debug-trigger"]')
        ?.textContent?.toLowerCase()
        .includes(expected),
    persona
  )
}

async function assertNoHorizontalOverflow(
  page,
  evidence,
  name = 'no horizontal overflow'
) {
  const dimensions = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }))
  record(
    evidence,
    name,
    dimensions.scrollWidth <= dimensions.clientWidth + 1,
    JSON.stringify(dimensions)
  )
}

async function assertNoDebugErrors(page, evidence) {
  const visibleText = await page.locator('body').innerText()
  record(
    evidence,
    'no unmocked request errors are visible',
    !visibleText.includes('PERSONA_DEBUG_'),
    visibleText.match(/PERSONA_DEBUG_[^\n]*/g)?.join(' | ') ?? ''
  )
}

async function openAssistant(page) {
  const panel = page.locator('#ai-assistant-panel')
  const launcher = page.getByTestId('assistant-launcher')
  if ((await launcher.getAttribute('aria-expanded')) !== 'true') {
    await launcher.click()
  }
  await panel.waitFor({ state: 'visible' })
  return panel
}

async function assertHomepageInputFocusStyle(page, evidence) {
  const input = page.locator('#forge-home-message')
  await input.waitFor({ state: 'visible' })
  await input.focus()
  const focusStyle = await input.evaluate((element) => {
    const group = element.closest('[data-slot="input-group"]')
    const groupStyle = group ? getComputedStyle(group) : null
    const controlStyle = getComputedStyle(element)
    return {
      focused: document.activeElement === element,
      groupBoxShadow: groupStyle?.boxShadow ?? 'missing',
      groupOutlineStyle: groupStyle?.outlineStyle ?? 'missing',
      groupOutlineWidth: groupStyle?.outlineWidth ?? 'missing',
      controlBoxShadow: controlStyle.boxShadow,
      controlOutlineStyle: controlStyle.outlineStyle,
      controlOutlineWidth: controlStyle.outlineWidth,
      groupFound: Boolean(group),
    }
  })
  const noPaintedShadow = (value) =>
    value === 'none' ||
    (value.includes('0px 0px 0px 0px') && value.includes('rgba'))
  const noOutline = (style, width) => style === 'none' || width === '0px'
  record(
    evidence,
    'homepage InputGroup focus has no extra box-shadow/ring',
    focusStyle.focused &&
      focusStyle.groupFound &&
      noPaintedShadow(focusStyle.groupBoxShadow) &&
      noPaintedShadow(focusStyle.controlBoxShadow) &&
      noOutline(focusStyle.groupOutlineStyle, focusStyle.groupOutlineWidth) &&
      noOutline(focusStyle.controlOutlineStyle, focusStyle.controlOutlineWidth),
    JSON.stringify(focusStyle)
  )
}

async function submitHomepageMessage(page, evidence) {
  const input = page.locator('#forge-home-message')
  await input.waitFor({ state: 'visible' })
  await assertHomepageInputFocusStyle(page, evidence)
  await input.fill(homepageMessage)
  await assertNoHorizontalOverflow(
    page,
    evidence,
    'homepage has no horizontal overflow'
  )
  const form = input.locator('xpath=ancestor::form')
  const submit = form.locator('button[type="submit"]')
  await submit.waitFor({ state: 'visible' })
  await Promise.all([
    page.waitForURL(/\/(?:wallet|dashboard)(?:\/|$)/),
    submit.click(),
  ])
}

async function assertAutoAssistantSend(page, evidence) {
  const panel = page.locator('#ai-assistant-panel')
  const launcher = page.getByTestId('assistant-launcher')
  await panel.waitFor({ state: 'visible', timeout: 30_000 })
  const launcherState = {
    count: await launcher.count(),
    expanded: await launcher.getAttribute('aria-expanded'),
    mobileLauncherCount: await page
      .getByTestId('assistant-mobile-launcher')
      .count(),
  }
  record(
    evidence,
    'assistant opens automatically after homepage submit',
    launcherState.count === 1 &&
      launcherState.expanded === 'true' &&
      launcherState.mobileLauncherCount === 1,
    JSON.stringify(launcherState)
  )

  await panel
    .getByText('Debug assistant received:', { exact: false })
    .first()
    .waitFor({ timeout: 30_000 })
  const panelText = await panel.innerText()
  const userMessages = panel.locator('.is-user')
  const debugReplies = panel
    .locator('.is-assistant')
    .filter({ hasText: 'Debug assistant received:' })
  const userTexts = await userMessages.allInnerTexts()
  const userMessageCount = await userMessages.count()
  const debugReplyCount = await debugReplies.count()
  record(
    evidence,
    'exactly one user message and one assistant debug reply',
    userMessageCount === 1 &&
      debugReplyCount === 1 &&
      userTexts.length === 1 &&
      userTexts[0].includes(safeHomepageMessage),
    JSON.stringify({
      userMessageCount,
      assistantDebugReplyCount: debugReplyCount,
      userTexts,
    })
  )

  const sensitiveState = await page.evaluate(
    ({ email, key }) => {
      const inputValues = Array.from(
        document.querySelectorAll('input, textarea'),
        (element) =>
          element instanceof HTMLInputElement ||
          element instanceof HTMLTextAreaElement
            ? element.value
            : ''
      )
      const pageContent = `${document.documentElement.innerHTML}\n${inputValues.join('\n')}`
      const sessionEntries = Object.entries(sessionStorage)
      return {
        pageHasEmail: pageContent.includes(email),
        pageHasKey: pageContent.includes(key),
        sessionHasEmail: sessionEntries.some(([name, value]) =>
          `${name}=${value}`.includes(email)
        ),
        sessionHasKey: sessionEntries.some(([name, value]) =>
          `${name}=${value}`.includes(key)
        ),
        pendingQueueKeys: sessionEntries
          .map(([name]) => name)
          .filter(
            (name) =>
              name === 'lmm_assistant_request:v1' ||
              name === 'lmm_assistant_auto_send:v1' ||
              name.startsWith('lmm_assistant_queued_')
          ),
      }
    },
    { email: rawEmail, key: rawKey }
  )
  record(
    evidence,
    'raw email and API key absent from page and sessionStorage',
    !sensitiveState.pageHasEmail &&
      !sensitiveState.pageHasKey &&
      !sensitiveState.sessionHasEmail &&
      !sensitiveState.sessionHasKey,
    JSON.stringify(sensitiveState)
  )
  record(
    evidence,
    'email redacted before display and persistence',
    !sensitiveState.pageHasEmail && !sensitiveState.sessionHasEmail
  )
  record(
    evidence,
    'API key redacted before display and persistence',
    !sensitiveState.pageHasKey && !sensitiveState.sessionHasKey
  )
  record(
    evidence,
    'one-shot assistant request queue consumed',
    sensitiveState.pendingQueueKeys.length === 0,
    JSON.stringify({ pendingQueueKeys: sensitiveState.pendingQueueKeys })
  )
  record(
    evidence,
    'stable redaction markers shown',
    panelText.includes('[REDACTED_EMAIL]') &&
      panelText.includes('[REDACTED_CREDENTIAL]'),
    JSON.stringify({
      emailMarker: panelText.includes('[REDACTED_EMAIL]'),
      credentialMarker: panelText.includes('[REDACTED_CREDENTIAL]'),
    })
  )
  return panel
}

async function runL0(browser) {
  const { context, page, evidence } = await createContext(browser, 'l0', {
    width: 698,
    height: 900,
  })
  try {
    await page.goto(baseUrl, {
      waitUntil: 'domcontentloaded',
    })
    await page.getByTestId('persona-debug-trigger').waitFor()
    record(
      evidence,
      'debug runtime marker',
      (await page.locator('html').getAttribute('data-persona-debug')) === 'true'
    )
    const isolationProbe = await page.evaluate(async () => {
      const capture = async (url) => {
        try {
          await fetch(url)
          return 'resolved'
        } catch (error) {
          return String(error)
        }
      }
      return {
        backend: await capture('/api/persona-debug-network-probe'),
        external: await capture(
          'https://persona-debug-probe.invalid/credential-leak'
        ),
      }
    })
    record(
      evidence,
      'unmocked backend fetch is blocked before network',
      isolationProbe.backend.includes('PERSONA_DEBUG_UNMOCKED_REQUEST'),
      isolationProbe.backend
    )
    record(
      evidence,
      'external fetch is blocked before network',
      isolationProbe.external.includes('PERSONA_DEBUG_EXTERNAL_REQUEST'),
      isolationProbe.external
    )
    await submitHomepageMessage(page, evidence)
    await page.waitForURL(/\/wallet(?:\/|$)/)
    await assertAutoAssistantSend(page, evidence)
    await assertNoHorizontalOverflow(page, evidence)
    await assertNoDebugErrors(page, evidence)
    await page.screenshot({ path: screenshotPath('l0-698'), fullPage: true })

    await page.goto(new URL('/keys', baseUrl).toString(), {
      waitUntil: 'domcontentloaded',
    })
    await page.waitForURL(/\/wallet(?:\/|$)/)
    record(evidence, 'L0 key route denied', true)
  } finally {
    await context.close()
  }
  return evidence
}

async function runL1(browser) {
  const { context, page, evidence } = await createContext(browser, 'l1', {
    width: 698,
    height: 900,
  })
  try {
    await page.goto(baseUrl, { waitUntil: 'domcontentloaded' })
    await selectPersona(page, 'l1')
    await page.waitForURL(/\/dashboard(?:\/|$)/)
    await page.getByRole('link', { name: 'Go to home', exact: true }).click()
    await page.waitForURL(/\/$/)
    await submitHomepageMessage(page, evidence)
    await page.waitForURL(/\/dashboard(?:\/|$)/)
    record(evidence, 'L1 lands in the console after homepage submit', true)
    await assertAutoAssistantSend(page, evidence)
    await assertNoHorizontalOverflow(page, evidence)
    await assertNoDebugErrors(page, evidence)

    await page
      .locator('a[href="/keys"]')
      .first()
      .evaluate((anchor) => anchor.click())
    await page.waitForURL(/\/keys(?:\/|$)/)
    record(
      evidence,
      'L1 key route is visible',
      /\/keys(?:\/|$)/.test(page.url())
    )
    await openAssistant(page)
    await assertNoHorizontalOverflow(page, evidence)
    await assertNoDebugErrors(page, evidence)
    await page.screenshot({ path: screenshotPath('l1-698'), fullPage: true })
  } finally {
    await context.close()
  }
  return evidence
}

async function runAdmin(browser) {
  const { context, page, evidence } = await createContext(browser, 'admin', {
    width: 1039,
    height: 900,
  })
  try {
    await page.goto(baseUrl, { waitUntil: 'domcontentloaded' })
    await selectPersona(page, 'admin')
    await page.waitForURL(/\/dashboard(?:\/|$)/)
    await page.getByRole('link', { name: 'Go to home', exact: true }).click()
    await page.waitForURL(/\/$/)
    await submitHomepageMessage(page, evidence)
    await page.waitForURL(/\/dashboard(?:\/|$)/)
    record(evidence, 'admin lands in the console after homepage submit', true)
    await assertAutoAssistantSend(page, evidence)
    const panel = await openAssistant(page)
    await panel
      .getByRole('button', { name: 'Conversation history', exact: true })
      .click()
    await panel.getByRole('button', { name: 'User audit', exact: true }).click()
    await panel.getByLabel('User ID', { exact: true }).fill('1001')
    await panel.getByRole('button', { name: 'View', exact: true }).click()
    await panel
      .getByText('My email is [REDACTED:EMAIL]', { exact: false })
      .waitFor({ timeout: 5_000 })
    const historyText = await panel.innerText()
    record(
      evidence,
      'lower-access audit is visible',
      historyText.includes('Lower-access user conversation')
    )
    record(
      evidence,
      'audited history remains redacted',
      historyText.includes('[REDACTED:EMAIL]') &&
        !historyText.includes(rawEmail)
    )
    record(
      evidence,
      '1039px uses assistant overlay',
      (await page.getByTestId('assistant-mobile-launcher').count()) === 1
    )
    await assertNoHorizontalOverflow(page, evidence)
    await assertNoDebugErrors(page, evidence)
    await page.screenshot({
      path: screenshotPath('admin-1039'),
      fullPage: true,
    })
  } finally {
    await context.close()
  }
  return evidence
}

const browser = await chromium.launch({ headless: true })
const results = []
let error = ''
try {
  results.push(await runL0(browser))
  results.push(await runL1(browser))
  results.push(await runAdmin(browser))
} catch (caught) {
  error = String(caught).slice(0, 1_000)
} finally {
  await browser.close()
}

const unsafeEvidence = results.flatMap((result) => [
  ...result.failedRequests,
  ...result.externalRequests,
  ...result.pageErrors,
])
const status =
  !error &&
  results.length === 3 &&
  results.every((result) => result.assertions.every((item) => item.pass)) &&
  unsafeEvidence.length === 0
    ? 'PASS'
    : 'FAIL'
const report = { runId, status, baseUrl, results, error, unsafeEvidence }
await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`, {
  flag: 'wx',
  mode: 0o600,
})

console.log(JSON.stringify({ status, reportPath, results, error }, null, 2))
if (status !== 'PASS') process.exitCode = 1
