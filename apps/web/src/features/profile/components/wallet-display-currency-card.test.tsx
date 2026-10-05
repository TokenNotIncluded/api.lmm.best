/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { UserProfile } from '../types'

const domWindow = new Window({ url: 'https://console.example.test/profile' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { WalletDisplayCurrencyCard } =
  await import('./wallet-display-currency-card')

const originalAuth = useAuthStore.getState().auth
const originalPut = api.put
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const profile: UserProfile = {
  id: 1,
  username: 'test-user',
  display_name: 'Test user',
  role: 1,
  group: 'default',
  quota: 10,
  used_quota: 0,
  request_count: 0,
  status: 1,
  aff_count: 0,
  aff_quota: 0,
  aff_history_quota: 0,
  created_time: 1,
  setting: '{}',
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 15))
}

async function renderCard(loading = false) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <WalletDisplayCurrencyCard
          profile={profile}
          loading={loading}
          onProfileUpdate={() => undefined}
        />
      </I18nextProvider>
    )
    await flush()
  })
  return { container, root }
}

async function select(container: HTMLElement, label: string) {
  const trigger = container.querySelector<HTMLButtonElement>(
    '[data-slot="select-trigger"]'
  )
  assert.ok(trigger)
  await act(async () => {
    trigger.click()
    await flush()
  })
  const option = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((candidate) => candidate.textContent === label)
  assert.ok(option, `Missing ${label} choice`)
  await act(async () => {
    option.click()
    await flush()
  })
}

beforeEach(() => {
  useAuthStore.setState({
    auth: {
      ...originalAuth,
      user: { id: 1, username: 'test-user', role: 1, setting: {} },
      accessToken: 'test-only-token',
    },
  })
})
afterEach(() => {
  api.put = originalPut
  useAuthStore.setState({ auth: originalAuth })
  document.body.replaceChildren()
})
after(() => domWindow.close())

describe('wallet display setting card', () => {
  test('explains independent display units and saves the selection directly', async () => {
    const bodies: unknown[] = []
    api.put = (async (_url, body) => {
      bodies.push(body)
      return { data: { success: true } }
    }) as typeof api.put
    const rendered = await renderCard()
    try {
      assert.match(
        rendered.container.textContent ?? '',
        /Following language: USD/
      )
      assert.match(
        rendered.container.textContent ?? '',
        /Payment settlement currency is configured separately/
      )
      await select(rendered.container, 'Credits')
      assert.deepEqual(bodies, [{ wallet_display_currency: 'CREDIT' }])
      assert.match(
        rendered.container.textContent ?? '',
        /Wallet display preference saved/
      )
      assert.match(
        rendered.container.textContent ?? '',
        /Wallet display unit: Credits/
      )
      assert.equal(document.querySelector('[role="dialog"]'), null)
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })

  test('shows a local save failure and retries the same display selection', async () => {
    const bodies: unknown[] = []
    api.put = (async (_url, body) => {
      bodies.push(body)
      return {
        data: { success: bodies.length > 1, message: 'private backend detail' },
      }
    }) as typeof api.put
    const rendered = await renderCard()
    try {
      await select(rendered.container, 'CNY')
      assert.match(
        rendered.container.textContent ?? '',
        /Could not save wallet display preference/
      )
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /private backend detail/
      )
      const retry = [
        ...rendered.container.querySelectorAll<HTMLButtonElement>('button'),
      ].find((button) => button.textContent === 'Retry')
      assert.ok(retry)
      await act(async () => {
        retry.click()
        await flush()
      })
      assert.deepEqual(bodies, [
        { wallet_display_currency: 'CNY' },
        { wallet_display_currency: 'CNY' },
      ])
      assert.match(
        rendered.container.textContent ?? '',
        /Wallet display preference saved/
      )
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /Could not save wallet display preference/
      )
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })

  test('disables the selection while loading instead of publishing a default preference', async () => {
    const rendered = await renderCard(true)
    try {
      const trigger = rendered.container.querySelector<HTMLButtonElement>(
        '[data-slot="select-trigger"]'
      )
      assert.ok(trigger?.disabled)
      assert.match(rendered.container.textContent ?? '', /Loading/)
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /Following language: USD/
      )
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })
})
