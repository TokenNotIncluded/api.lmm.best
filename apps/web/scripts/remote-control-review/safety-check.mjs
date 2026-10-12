/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// Actual production page on an encrypted loopback fixture. No live credentials.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdtemp, mkdir, readFile, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const here = dirname(fileURLToPath(import.meta.url))
const base = process.env.REMOTE_SAFETY_OUTPUT
  ? resolve(process.env.REMOTE_SAFETY_OUTPUT)
  : tmpdir()
await mkdir(base, { recursive: true })
const out = await mkdtemp(resolve(base, 'lmm-remote-safety-'))
const log = createWriteStream(resolve(out, 'server.log'))
const child = spawn(
  process.env.BUN_BIN || 'bun',
  [resolve(here, 'safety-server.mjs')],
  {
    env: { ...process.env, REMOTE_SAFETY_OUTPUT: out },
    stdio: ['ignore', 'pipe', 'pipe'],
  }
)
child.stdout.pipe(log, { end: false })
child.stderr.pipe(log, { end: false })
let startupError
child.on('error', (error) => {
  startupError = error
})
let browser
try {
  let origin
  for (let attempt = 0; attempt < 150; attempt++) {
    if (startupError || child.exitCode !== null) {
      throw (
        startupError ||
        new Error(`Fixture failed; inspect ${resolve(out, 'server.log')}`)
      )
    }
    try {
      origin = (await readFile(resolve(out, 'origin'), 'utf8')).trim()
      break
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 100))
    }
  }
  assert.ok(origin, 'Fixture startup timed out')
  browser = await chromium.launch({
    executablePath: process.env.CHROME_PATH,
    headless: true,
  })
  const page = await browser.newPage({
    viewport: { width: 1440, height: 1000 },
  })
  const errors = [],
    wire = []
  page.on('pageerror', (error) => errors.push(String(error)))
  page.on('request', (request) => {
    if (request.method() === 'POST') wire.push(request.postData() || '')
  })
  await page.route('**/*', (route) =>
    new URL(route.request().url()).origin === origin
      ? route.continue()
      : route.abort()
  )
  await page.goto(origin)
  await page.getByText('登录后继续', { exact: true }).waitFor()
  await page.evaluate(() => window.remoteReview.setUser(1))
  const unlock = async () => {
    await page.getByLabel('PIN', { exact: true }).fill('fixture-pin')
    await page.getByRole('button', { name: '解锁', exact: true }).click()
    await page.waitForFunction(() =>
      Array.from(document.querySelectorAll('button')).some(
        (button) => button.textContent.trim() === '图片' && !button.disabled
      )
    )
  }
  await unlock()
  await page.screenshot({
    path: resolve(out, 'desktop-dark.png'),
    fullPage: true,
  })
  const file = await page.evaluate(() => {
    const canvas = document.createElement('canvas')
    canvas.width = 500
    canvas.height = 300
    const context = canvas.getContext('2d')
    context.fillStyle = '#003366'
    context.fillRect(0, 0, 500, 300)
    context.fillStyle = 'white'
    context.font = '30px sans-serif'
    context.fillText('REMOTE IMAGE FIXTURE', 30, 120)
    return canvas.toDataURL('image/png').split(',')[1]
  })
  const image = {
    name: 'image.png',
    mimeType: 'image/png',
    buffer: Buffer.from(file, 'base64'),
  }
  await page.locator('input[type=file]').setInputFiles(image)
  await page.getByRole('img', { name: '图片预览', exact: true }).waitFor()
  await page.getByRole('button', { name: '发送任务', exact: true }).click()
  await page
    .getByRole('status')
    .filter({ hasText: 'Pi 已收到任务' })
    .waitFor({ timeout: 12000 })
  const commands = await (await fetch(`${origin}/fixture/commands`)).json()
  assert.equal(commands.length, 1)
  assert.equal(commands[0].content, '')
  assert.equal(commands[0].images[0].mimeType, 'image/jpeg')
  assert.ok(commands[0].images[0].data.length <= 32768)
  await page.getByLabel('向 Pi 发送任务', { exact: true }).fill('Keep my draft')
  await page.locator('input[type=file]').setInputFiles(image)
  await page.getByRole('img', { name: '图片预览', exact: true }).waitFor()
  await fetch(`${origin}/fixture/refuse`)
  await page.getByRole('button', { name: '发送任务', exact: true }).click()
  await page.getByRole('alert').waitFor()
  assert.equal(
    await page.getByLabel('向 Pi 发送任务', { exact: true }).inputValue(),
    'Keep my draft'
  )
  assert.equal(
    await page.getByRole('img', { name: '图片预览', exact: true }).count(),
    1
  )
  await page.getByRole('button', { name: '移除附件', exact: true }).click()
  assert.equal(
    await page.getByRole('img', { name: '图片预览', exact: true }).count(),
    0
  )
  await page
    .getByLabel('向 Pi 发送任务', { exact: true })
    .evaluate((element, data) => {
      const clipboard = new DataTransfer()
      clipboard.items.add(
        new File(
          [Uint8Array.from(atob(data), (character) => character.charCodeAt(0))],
          'pasted.png',
          { type: 'image/png' }
        )
      )
      element.dispatchEvent(
        new ClipboardEvent('paste', {
          clipboardData: clipboard,
          bubbles: true,
          cancelable: true,
        })
      )
    }, file)
  await page.getByRole('img', { name: '图片预览', exact: true }).waitFor()
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 })
    assert.ok(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth
      ),
      `horizontal overflow at ${width}`
    )
    await page.screenshot({
      path: resolve(out, `mobile-${width}.png`),
      fullPage: true,
    })
  }
  await page.evaluate(() => document.documentElement.classList.remove('dark'))
  await page.screenshot({
    path: resolve(out, 'mobile-light.png'),
    fullPage: true,
  })
  await page.getByRole('button', { name: '锁定', exact: true }).click()
  await page.getByLabel('PIN', { exact: true }).waitFor()
  assert.equal(
    await page.getByLabel('向 Pi 发送任务', { exact: true }).count(),
    0
  )
  await unlock()
  await page.evaluate(() => window.remoteReview.setUser(2))
  await page.getByLabel('PIN', { exact: true }).waitFor()
  assert.equal(await page.getByText('设计工作区', { exact: true }).count(), 0)
  assert.ok(
    await page.evaluate(() =>
      window.remoteReview.cachedOwners().every((key) => key[2] !== '1:browser')
    )
  )
  await unlock()
  await fetch(`${origin}/fixture/generation`)
  await page.waitForTimeout(16000)
  await page.getByLabel('PIN', { exact: true }).waitFor()
  assert.equal(await page.getByText('设计工作区', { exact: true }).count(), 0)
  await unlock()
  await fetch(`${origin}/fixture/freeze`)
  await page.waitForTimeout(1500)
  await page.clock.install()
  await page.clock.fastForward(46000)
  await page
    .getByRole('status')
    .filter({ hasText: '正在等待 Pi 连接' })
    .waitFor()
  assert.equal(
    await page
      .getByRole('button', { name: '发送任务', exact: true })
      .isEnabled(),
    false
  )
  assert.equal(
    await page.getByRole('button', { name: '图片', exact: true }).isEnabled(),
    false
  )
  await page.evaluate(() => window.remoteReview.setUser())
  await page.getByText('登录后继续', { exact: true }).waitFor()
  assert.equal(
    await page.getByLabel('向 Pi 发送任务', { exact: true }).count(),
    0
  )
  assert.ok(
    await page.evaluate(() => window.remoteReview.cachedOwners().length === 0)
  )
  assert.ok(
    wire.every(
      (body) =>
        !body.includes('fixture-pin') &&
        !body.includes('Keep my draft') &&
        !body.includes(commands[0].images[0].data)
    )
  )
  assert.deepEqual(errors, [])
  await writeFile(
    resolve(out, 'result.json'),
    JSON.stringify(
      {
        passed: true,
        source: 'production remote page',
        backend: 'encrypted loopback fixture, not WIP authentication',
        viewports: [1440, 390, 320],
        checks: [
          'image input/clipboard/removal',
          'image-only encrypted command',
          'draft retention',
          'lock',
          'account/logout cache isolation',
          'generation relock',
          'heartbeat expiry',
        ],
        pageErrors: errors,
      },
      null,
      2
    )
  )
  console.log(`PASS: production remote page safety review; artifacts: ${out}`)
} finally {
  await browser?.close()
  child.kill('SIGTERM')
  await new Promise((resolve) => {
    if (child.exitCode !== null) return resolve()
    child.once('exit', resolve)
    setTimeout(() => {
      child.kill('SIGKILL')
      resolve()
    }, 3000).unref()
  })
  log.end()
}
