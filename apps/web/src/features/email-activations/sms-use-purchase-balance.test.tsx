/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'ResizeObserver',
  'localStorage',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'getComputedStyle', {
  configurable: true,
  value: domWindow.getComputedStyle.bind(domWindow),
})
const { act, useEffect } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSmsPurchaseBalance } = await import('./sms-use-purchase-balance')
const { HeroSmsSmsActivationPanel } = await import('./sms-activation-panel')
const { createInstance } = await import('i18next')
const { I18nextProvider } = await import('react-i18next')
const i18n = createInstance()
await i18n.init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
  keySeparator: false,
})
const originalGet = api.get
const originalPost = api.post
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

function response(id: number, quota: number) {
  return {
    data: {
      success: true,
      data: { id, username: `user-${id}`, role: 1, quota },
    },
  }
}

function login(id: number, sid = `session-${id}`) {
  useAuthStore.getState().auth.setBundle({
    access_token: 'local-test',
    token_type: 'Bearer',
    access_expires_at: 0,
    user: { id, username: `user-${id}`, role: 1, quota: 5_000_000 },
    session: {
      sid,
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: 0,
      last_active_at: 0,
      expires_at: 0,
    },
  })
}

async function settle(predicate: () => boolean) {
  for (let attempt = 0; attempt < 50 && !predicate(); attempt += 1) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
    })
  }
  assert.ok(predicate(), 'balance state did not settle')
}

async function mount(panel = false) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let balance!: ReturnType<typeof useSmsPurchaseBalance>
  function Probe() {
    const current = useSmsPurchaseBalance()
    useEffect(() => {
      balance = current
    }, [current])
    return (
      <>
        <button type='button' disabled={!current.canPurchase}>
          {current.status}
        </button>
        {panel ? <HeroSmsSmsActivationPanel /> : null}
      </>
    )
  }
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={queryClient}>
          <Probe />
        </QueryClientProvider>
      </I18nextProvider>
    )
  )
  return {
    queryClient,
    get balance() {
      return balance
    },
    async close() {
      await act(async () => root.unmount())
      queryClient.clear()
    },
  }
}

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  useAuthStore.getState().auth.reset()
  document.body.replaceChildren()
  localStorage.clear()
})
after(() => domWindow.close())

function findButton(text: string) {
  const button = Array.from(document.querySelectorAll('button')).find((item) =>
    item.textContent?.includes(text)
  )
  assert.ok(button, `missing button: ${text}`)
  return button
}

function mockPanelApi(self: () => Promise<ReturnType<typeof response>>) {
  api.get = (async (url: string) => {
    if (url === '/api/user/self') return self()
    let data: unknown = []
    if (url.endsWith('/services')) {
      data = [
        { code: 'tg', name: 'Telegram' },
        { code: 'wa', name: 'WhatsApp' },
      ]
    }
    if (url.endsWith('/countries')) {
      data = [{ id: 6, name: 'Russia', english_name: 'Russia' }]
    }
    if (url.endsWith('/offer')) {
      data = {
        id: 'quote',
        country_id: 6,
        service: 'tg',
        operator: '',
        inventory: 3,
        customer_price_usd: '1',
        charge_quota: 500_000,
      }
    }
    if (url.endsWith('/orders')) {
      data = { items: [], total: 0, page: 1, size: 50 }
    }
    if (url.endsWith('/orders/current-list')) data = { items: [] }
    return { data: { success: true, data } }
  }) as typeof api.get
  api.post = (async () => {
    throw new Error('No provider purchases are permitted by this test')
  }) as typeof api.post
  localStorage.setItem(
    'lmm-hero-sms-favorites:v1',
    JSON.stringify([{ serviceCode: 'tg', countryId: 6 }])
  )
}

describe('SMS purchase controls', () => {
  test('both entry and confirmation disable after a balance drop or failed read, and exact USD 10 restores them', async () => {
    login(1)
    mockPanelApi(async () => response(1, 5_000_000))
    const probe = await mount(true)
    try {
      await settle(() => probe.balance.canPurchase)
      await act(async () => findButton('Favorites').click())
      await settle(() =>
        Array.from(document.querySelectorAll('button')).some(
          (item) => item.textContent?.includes('Telegram') && !item.disabled
        )
      )
      await act(async () => findButton('Telegram').click())
      await settle(() => !findButton('Buy phone activation').disabled)
      await act(async () => findButton('Buy phone activation').click())
      await settle(
        () => document.body.textContent?.includes('Confirm purchase') === true
      )
      assert.equal(findButton('Confirm purchase').disabled, false)
      await act(async () => probe.balance.recordQuota(4_999_999))
      await settle(() => findButton('Confirm purchase').disabled)
      assert.equal(findButton('Buy phone activation').disabled, true)
      assert.match(
        document.getElementById('sms-confirm-balance-notice')?.textContent ??
          '',
        /Current balance: USD 9\.999998/
      )
      mockPanelApi(async () => {
        throw new Error('offline')
      })
      await act(async () => {
        await probe.balance.refresh()
      })
      await settle(() => probe.balance.status === 'unknown')
      assert.equal(findButton('Buy phone activation').disabled, true)
      assert.equal(findButton('Confirm purchase').disabled, true)
      assert.match(
        document.getElementById('sms-confirm-balance-notice')?.textContent ??
          '',
        /balance could not be verified/
      )
      mockPanelApi(async () => response(1, 5_000_000))
      await act(async () => {
        await probe.balance.refresh()
      })
      await settle(() => !findButton('Confirm purchase').disabled)
      assert.equal(findButton('Buy phone activation').disabled, false)
    } finally {
      await probe.close()
    }
  })

  test('switching WhatsApp back to SMS retains the last SMS service selection', async () => {
    login(1)
    mockPanelApi(async () => response(1, 5_000_000))
    const probe = await mount(true)
    try {
      await settle(() => probe.balance.canPurchase)
      await act(async () => findButton('Favorites').click())
      await settle(() =>
        Array.from(document.querySelectorAll('button')).some(
          (item) => item.textContent?.includes('Telegram') && !item.disabled
        )
      )
      await act(async () => findButton('Telegram').click())
      assert.match(
        document.getElementById('hero-sms-service')?.textContent ?? '',
        /Telegram/
      )
      await act(async () =>
        document.getElementById('hero-sms-channel-whatsapp')?.click()
      )
      assert.match(
        document.getElementById('hero-sms-service')?.textContent ?? '',
        /WhatsApp/
      )
      await act(async () =>
        document.getElementById('hero-sms-channel-sms')?.click()
      )
      assert.match(
        document.getElementById('hero-sms-service')?.textContent ?? '',
        /Telegram/
      )
    } finally {
      await probe.close()
    }
  })
})

describe('SMS purchase balance session and settlement isolation', () => {
  test('starts unknown despite stored quota and permits exactly USD 10 after verification', async () => {
    login(1)
    const pending = deferred<ReturnType<typeof response>>()
    api.get = (() => pending.promise) as typeof api.get
    const probe = await mount()
    try {
      const initialBalance = probe.balance
      assert.equal(initialBalance.status, 'unknown')
      assert.equal(initialBalance.canPurchase, false)
      pending.resolve(response(1, 5_000_000))
      await settle(() => probe.balance.canPurchase)
      assert.equal(probe.balance.balanceUSD, 10)
      await act(async () => probe.balance.recordQuota(4_999_999))
      await settle(() => probe.balance.status === 'below-minimum')
      assert.equal(probe.balance.canPurchase, false)
    } finally {
      await probe.close()
    }
  })

  test('late balance reads cannot overwrite newer batch purchase or refund quotas', async () => {
    login(1)
    api.get = (async () => response(1, 6_000_000)) as typeof api.get
    const probe = await mount()
    try {
      await settle(() => probe.balance.canPurchase)
      await act(async () => probe.balance.recordQuota(5_500_000))
      const stale = deferred<ReturnType<typeof response>>()
      api.get = (() => stale.promise) as typeof api.get
      let refresh!: Promise<boolean>
      await act(async () => {
        refresh = probe.balance.refresh()
      })
      await act(async () => probe.balance.recordQuota(4_999_999))
      stale.resolve(response(1, 6_000_000))
      await act(async () => {
        assert.equal(await refresh, false)
      })
      await settle(() => probe.balance.status === 'below-minimum')
      assert.equal(useAuthStore.getState().auth.user?.quota, 4_999_999)
      assert.equal(
        probe.queryClient.getQueryData([
          'user',
          'sms-purchase-balance',
          1,
          'session-1',
        ]),
        4_999_999
      )
      await act(async () => probe.balance.recordQuota(5_000_000))
      await settle(() => probe.balance.canPurchase)
    } finally {
      await probe.close()
    }
  })

  for (const [nextId, nextSession] of [
    [2, 'session-2'],
    [1, 'replacement-session'],
  ] as const) {
    test(`account/session switch to ${nextSession} rejects late reads, refresh permission and old settlements`, async () => {
      login(1)
      api.get = (async () => response(1, 5_000_000)) as typeof api.get
      const probe = await mount()
      try {
        await settle(() => probe.balance.canPurchase)
        const previous = probe.balance
        const oldRead = deferred<ReturnType<typeof response>>()
        const newRead = deferred<ReturnType<typeof response>>()
        let reads = 0
        api.get = (() =>
          ++reads === 1 ? oldRead.promise : newRead.promise) as typeof api.get
        let oldRefresh!: Promise<boolean>
        await act(async () => {
          oldRefresh = previous.refresh()
        })
        await act(async () => login(nextId, nextSession))
        assert.equal(probe.balance.status, 'unknown')
        assert.equal(probe.balance.canPurchase, false)
        await act(async () => {
          previous.recordQuota(99_000_000)
          previous.markDenied()
          oldRead.resolve(response(1, 99_000_000))
        })
        assert.equal(await oldRefresh, false)
        assert.equal(await previous.refresh(), false)
        assert.equal(previous.isCurrentSession(), false)
        assert.equal(useAuthStore.getState().auth.user?.quota, 5_000_000)
        assert.equal(probe.balance.status, 'unknown')
        newRead.resolve(response(nextId, 4_999_999))
        await settle(() => probe.balance.status === 'below-minimum')
        assert.equal(useAuthStore.getState().auth.user?.quota, 4_999_999)
        assert.equal(probe.balance.serverDenied, false)
      } finally {
        await probe.close()
      }
    })
  }

  test('failed revalidation blocks cached allowance and successful retry restores it', async () => {
    login(1)
    api.get = (async () => response(1, 5_000_000)) as typeof api.get
    const probe = await mount()
    try {
      await settle(() => probe.balance.canPurchase)
      api.get = (async () => {
        throw new Error('offline')
      }) as typeof api.get
      await act(async () => {
        assert.equal(await probe.balance.refresh(), false)
      })
      await settle(() => probe.balance.status === 'unknown')
      assert.equal(probe.balance.canPurchase, false)
      api.get = (async () => response(1, 5_000_000)) as typeof api.get
      await act(async () => {
        assert.equal(await probe.balance.refresh(), true)
      })
      await settle(() => probe.balance.canPurchase)
    } finally {
      await probe.close()
    }
  })
})
