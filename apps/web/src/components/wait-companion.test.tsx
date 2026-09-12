/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ComponentProps } from 'react'

const dom = new Window({ url: 'http://localhost/' })
for (const name of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Element',
  'Node',
  'Event',
  'MouseEvent',
  'FocusEvent',
  'MutationObserver',
  'getComputedStyle',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, name, {
    configurable: true,
    value: dom[name],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { WaitCompanion } = await import('./wait-companion')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const roots: ReturnType<typeof createRoot>[] = []

async function mount(props: ComponentProps<typeof WaitCompanion>) {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  roots.push(root)
  const update = async (next: ComponentProps<typeof WaitCompanion>) => {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <WaitCompanion {...next} />
        </I18nextProvider>
      )
    })
  }
  await update(props)
  return { host, update }
}
async function settle(ms = 15) {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, ms))
  })
}
async function click(button: HTMLButtonElement | null | undefined) {
  assert.ok(button)
  await act(async () => {
    button.click()
  })
}
function dots(host: HTMLElement) {
  return Array.from(
    host.querySelectorAll<HTMLButtonElement>('button[aria-pressed]')
  )
}

afterEach(async () => {
  for (const root of roots.splice(0)) await act(async () => root.unmount())
  document.body.replaceChildren()
})
after(() => dom.close())

test('short waits never show or leave behind a game offer', async () => {
  const { host, update } = await mount({ pending: true, delayMs: 50 })
  assert.equal(host.textContent, '')
  await update({ pending: false, delayMs: 50 })
  await settle(70)
  assert.equal(host.textContent, '')
})

test('the game is opt-in, does not steal focus, and its initial puzzle can be solved', async () => {
  const input = document.createElement('input')
  document.body.append(input)
  input.focus()
  const { host } = await mount({ pending: true, delayMs: 0 })
  await settle()
  assert.equal(document.activeElement, input)
  assert.equal(dots(host).length, 0)
  await click(host.querySelector('button'))
  assert.equal(dots(host).length, 9)
  for (const cell of [0, 4, 8]) await click(dots(host)[cell])
  assert.ok(host.textContent?.includes('All clear.'))
  assert.equal(document.activeElement?.textContent, 'Another puzzle')
  assert.ok(
    dots(host).every(
      (button) =>
        button.disabled && button.getAttribute('aria-pressed') === 'false'
    )
  )
  assert.ok(host.textContent?.includes('Moves: 3'))
})

test('returning output removes the board and provides a route back to the task', async () => {
  let returned = 0
  const props = {
    pending: true,
    delayMs: 0,
    onReturnToTask: () => {
      returned++
    },
  }
  const { host, update } = await mount(props)
  await settle()
  await click(host.querySelector('button'))
  dots(host)[0]?.focus()
  await update({ ...props, pending: false, finishedLabel: 'Reply ready' })
  assert.equal(dots(host).length, 0)
  assert.equal(document.activeElement, host.querySelector('button'))
  assert.ok(host.textContent?.includes('Reply ready'))
  assert.equal(host.querySelector('button')?.textContent, 'Back to task')
  await click(host.querySelector('button'))
  assert.equal(returned, 1)
  assert.equal(host.textContent, '')
})

test('closing the activity never completes or cancels the pending task', async () => {
  let returned = 0
  const { host } = await mount({
    pending: true,
    delayMs: 0,
    onReturnToTask: () => {
      returned++
    },
  })
  await settle()
  await click(host.querySelector('button'))
  await click(host.querySelector('button'))
  assert.equal(dots(host).length, 0)
  assert.equal(returned, 0)
  assert.equal(host.querySelector('button')?.textContent, 'Play while you wait')
})

test('different tasks never share an open game or move counter', async () => {
  const { host, update } = await mount({
    pending: true,
    delayMs: 0,
    taskKey: 'first',
  })
  await settle()
  await click(host.querySelector('button'))
  await click(dots(host)[0])
  assert.ok(host.textContent?.includes('Moves: 1'))
  await update({ pending: true, delayMs: 0, taskKey: 'second' })
  await settle()
  assert.equal(dots(host).length, 0)
  await click(host.querySelector('button'))
  assert.ok(host.textContent?.includes('Moves: 0'))
})

test('a new wait with the same task key starts a fresh playable puzzle', async () => {
  const { host, update } = await mount({ pending: true, delayMs: 0 })
  await settle()
  await click(host.querySelector('button'))
  for (const cell of [0, 4, 8]) await click(dots(host)[cell])
  assert.ok(host.textContent?.includes('All clear.'))
  assert.equal(document.activeElement?.textContent, 'Another puzzle')
  await update({ pending: false, delayMs: 0 })
  await update({ pending: true, delayMs: 0 })
  await settle()
  await click(host.querySelector('button'))
  assert.ok(
    dots(host).some((button) => button.getAttribute('aria-pressed') === 'true')
  )
  assert.ok(dots(host).every((button) => !button.disabled))
  assert.ok(host.textContent?.includes('Moves: 0'))
})
