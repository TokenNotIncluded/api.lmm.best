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
import { after, afterEach, test } from 'node:test'

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
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'KeyboardEvent',
  'CustomEvent',
  'StorageEvent',
  'SVGElement',
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
const { useSmsPurchaseBalance } =
  await import('@/features/email-activations/sms-use-purchase-balance')
const { HeroSmsSmsActivationPanel } =
  await import('@/features/email-activations/sms-activation-panel')
const { clearSmsPurchaseRecovery, saveSmsPurchaseRecovery } =
  await import('./sms-purchase-recovery')
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
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((done, fail) => {
    resolve = done
    reject = fail
  })
  return { promise, resolve, reject }
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

async function settle(
  predicate: () => boolean,
  message = 'panel state did not settle'
) {
  for (let attempt = 0; attempt < 200 && !predicate(); attempt += 1) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
    })
  }
  assert.ok(predicate(), message)
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
  const probe = {
    queryClient,
    get balance() {
      return balance
    },
    async close() {
      mountedProbes.delete(probe)
      await act(async () => root.unmount())
      queryClient.clear()
    },
  }
  mountedProbes.add(probe)
  return probe
}

const mountedProbes = new Set<{ close: () => Promise<void> }>()

afterEach(async () => {
  for (const probe of mountedProbes) await probe.close()
  api.get = originalGet
  api.post = originalPost
  useAuthStore.getState().auth.reset()
  document.body.replaceChildren()
  localStorage.clear()
})
after(() => domWindow.close())

function findButton(text: string) {
  const button = Array.from(document.querySelectorAll('button')).find((item) =>
    text === 'Buy phone activation'
      ? /^Buy(?: \d+)? phone activations?$/.test(item.textContent ?? '')
      : item.textContent?.includes(text)
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
        { code: 'go', name: 'Google' },
      ]
    }
    if (url.endsWith('/countries')) {
      data = [
        { id: 6, name: 'Russia', english_name: 'Russia' },
        { id: 36, name: 'Canada', english_name: 'Canada' },
      ]
    }
    if (url.endsWith('/operators')) data = ['fixture-operator']
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

const recoveryKey = (userId: number) =>
  `lmm-hero-sms-purchase-recovery:v1:${userId}`

function pendingPurchase(userId = 1, item = 1, requested = 1) {
  return {
    userId,
    offerId: 'original-quote',
    idempotencyKey: `original-key-${item}`,
    item,
    requested,
  }
}

function transportError(status?: number) {
  return Object.assign(new Error('fixture uncertain response'), {
    isAxiosError: true,
    response: status
      ? { status, data: { message: 'fixture uncertain response' } }
      : undefined,
  })
}

function createdOrder(quota = 4_500_000) {
  return {
    data: {
      success: true,
      data: {
        order: { id: 'fixture-order', status: 'active' },
        quota,
      },
    },
  }
}

async function chooseFavorite(
  probe: Awaited<ReturnType<typeof mount>>,
  quantity = 1
) {
  await settle(() => probe.balance.canPurchase)
  await act(async () => findButton('Favorites').click())
  await settle(() =>
    Array.from(document.querySelectorAll('button')).some(
      (item) => item.textContent?.includes('Telegram') && !item.disabled
    )
  )
  await act(async () => findButton('Telegram').click())
  await settle(() => !findButton('Buy phone activation').disabled)
  for (let count = 1; count < quantity; count += 1) {
    await act(async () => {
      const button = document.querySelector<HTMLButtonElement>(
        '[aria-label="Increase quantity"]'
      )
      assert.ok(button)
      button.click()
    })
  }
}

async function confirmPurchase() {
  await act(async () => findButton('Buy phone activation').click())
  await settle(
    () => document.body.textContent?.includes('Confirm purchase') === true
  )
  await act(async () => findButton('Confirm purchase').click())
}

async function reconcilePurchase() {
  await settle(() => !findButton('Resolve purchase and continue').disabled)
  await act(async () => findButton('Resolve purchase and continue').click())
}

test('retains and replays an uncertain purchase after selecting a favorite', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  let postAttempts = 0
  const attemptKeys: string[] = []
  api.post = (async (
    _url: string,
    _body: unknown,
    config: { headers?: { 'Idempotency-Key'?: string } }
  ) => {
    attemptKeys.push(config.headers?.['Idempotency-Key'] ?? '')
    postAttempts += 1
    throw Object.assign(new Error('fixture transport timeout'), {
      isAxiosError: true,
    })
  }) as typeof api.post
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
    await act(async () => findButton('Confirm purchase').click())
    await settle(
      () =>
        document.body.textContent?.includes('Resolve purchase and continue') ===
        true
    )
    assert.equal(postAttempts, 2, 'same item retried exactly once')
    assert.equal(findButton('Buy phone activation').disabled, true)
    await act(async () => findButton('Favorites').click())
    assert.equal(
      document.body.textContent?.includes('Resolve purchase and continue'),
      true,
      'recovery remains available while browsing favorites'
    )
    await act(async () => findButton('Telegram').click())
    assert.equal(findButton('Buy phone activation').disabled, true)
    assert.equal(
      document.body.textContent?.includes('Resolve purchase and continue'),
      true
    )
    assert.equal(postAttempts, 2, 'no actual or additional purchase invoked')
    assert.equal(attemptKeys[0], attemptKeys[1])
    api.post = (async (
      _url: string,
      _body: unknown,
      config: { headers?: { 'Idempotency-Key'?: string } }
    ) => {
      attemptKeys.push(config.headers?.['Idempotency-Key'] ?? '')
      postAttempts += 1
      return {
        data: {
          success: true,
          data: {
            order: { id: 'fixture-new-purchase', status: 'active' },
            quota: 5_000_000,
          },
        },
      }
    }) as typeof api.post
    await act(async () => findButton('Favorites').click())
    await reconcilePurchase()
    await settle(() => postAttempts === 3)
    assert.equal(
      attemptKeys[0],
      attemptKeys[2],
      'recovery replays the uncertain item with its original key'
    )
    assert.ok(attemptKeys.every(Boolean))
  } finally {
    await probe.close()
  }
})

test('loads the original persisted item after a page reload and preserves partial batch counts', async () => {
  login(7)
  mockPanelApi(async () =>
    response(7, useAuthStore.getState().auth.user?.quota ?? 5_000_000)
  )
  const saved = pendingPurchase(7, 2, 3)
  localStorage.setItem(recoveryKey(7), JSON.stringify(saved))
  const attempts: unknown[] = []
  api.post = (async (
    _url: string,
    body: unknown,
    config: { headers?: Record<string, string> }
  ) => {
    attempts.push({ body, key: config.headers?.['Idempotency-Key'] })
    assert.deepEqual(
      JSON.parse(localStorage.getItem(recoveryKey(7)) ?? ''),
      saved
    )
    return createdOrder()
  }) as typeof api.post
  const probe = await mount(true)
  try {
    await settle(() => probe.balance.canPurchase)
    await settle(
      () =>
        document.body.textContent?.includes('Resolve purchase and continue') ===
        true
    )
    assert.ok(
      document.body.textContent?.includes(
        '1 of 3 phone activations were purchased'
      )
    )
    assert.equal(findButton('Buy phone activation').disabled, true)
    await reconcilePurchase()
    await settle(() => localStorage.getItem(recoveryKey(7)) === null)
    assert.deepEqual(attempts, [
      { body: { offer_id: saved.offerId }, key: saved.idempotencyKey },
    ])
    assert.equal(useAuthStore.getState().auth.user?.quota, 4_500_000)
  } finally {
    await probe.close()
  }
})

test('records an in-flight item before POST and stops remaining batch items after unmount', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  const post = deferred<ReturnType<typeof createdOrder>>()
  let attempts = 0
  let originalKey = ''
  api.post = (async (
    _url: string,
    body: { offer_id: string },
    config: { headers?: Record<string, string> }
  ) => {
    attempts += 1
    originalKey = config.headers?.['Idempotency-Key'] ?? ''
    const saved = JSON.parse(localStorage.getItem(recoveryKey(1)) ?? '')
    assert.equal(saved.offerId, body.offer_id)
    assert.equal(saved.idempotencyKey, originalKey)
    assert.equal(saved.requested, 3)
    assert.equal(saved.item, 1)
    return post.promise
  }) as typeof api.post
  const first = await mount(true)
  await chooseFavorite(first, 3)
  await confirmPurchase()
  await settle(() => attempts === 1)
  await first.close()
  const second = await mount(true)
  try {
    await settle(
      () =>
        document.body.textContent?.includes('Resolve purchase and continue') ===
        true
    )
    assert.equal(findButton('Buy phone activation').disabled, true)
    await act(async () => post.resolve(createdOrder()))
    await settle(() => localStorage.getItem(recoveryKey(1)) === null)
    assert.equal(attempts, 1, 'unmounted batch must not buy subsequent items')
    assert.ok(originalKey.endsWith('-1'))
  } finally {
    await second.close()
  }
})

test('persists only the unknown partial batch item and replays it after remount', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  const attempts: Array<{ offer: unknown; key: string | undefined }> = []
  api.post = (async (
    _url: string,
    body: unknown,
    config: { headers?: Record<string, string> }
  ) => {
    attempts.push({ offer: body, key: config.headers?.['Idempotency-Key'] })
    if (attempts.length === 1) return createdOrder()
    throw transportError()
  }) as typeof api.post
  const first = await mount(true)
  await chooseFavorite(first, 3)
  await confirmPurchase()
  await settle(
    () =>
      attempts.length === 3 &&
      document.body.textContent?.includes('Resolve purchase and continue') ===
        true
  )
  const saved = JSON.parse(localStorage.getItem(recoveryKey(1)) ?? '')
  assert.equal(saved.item, 2)
  assert.equal(saved.requested, 3)
  assert.ok(saved.idempotencyKey.endsWith('-2'))
  assert.equal(attempts[1]?.key, attempts[2]?.key)
  assert.equal('phone_number' in saved, false)
  assert.equal('code' in saved, false)
  await first.close()
  const second = await mount(true)
  try {
    await settle(
      () =>
        document.body.textContent?.includes(
          '1 of 3 phone activations were purchased'
        ) === true
    )
    api.post = (async (
      _url: string,
      body: unknown,
      config: { headers?: Record<string, string> }
    ) => {
      attempts.push({ offer: body, key: config.headers?.['Idempotency-Key'] })
      return createdOrder()
    }) as typeof api.post
    await reconcilePurchase()
    await settle(() => localStorage.getItem(recoveryKey(1)) === null)
    assert.equal(
      attempts.length,
      4,
      'recovery must not resume the unpurchased third item'
    )
    assert.deepEqual(attempts[3], attempts[1])
  } finally {
    await second.close()
  }
})

test('treats a first HTTP 503 as uncertain and keeps the same recovery pair', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  const keys: string[] = []
  api.post = (async (
    _url: string,
    _body: unknown,
    config: { headers?: Record<string, string> }
  ) => {
    keys.push(config.headers?.['Idempotency-Key'] ?? '')
    throw transportError(503)
  }) as typeof api.post
  const probe = await mount(true)
  try {
    await chooseFavorite(probe)
    await confirmPurchase()
    await settle(
      () =>
        document.body.textContent?.includes('Resolve purchase and continue') ===
        true
    )
    assert.equal(keys.length, 2)
    assert.equal(keys[0], keys[1])
    assert.equal(
      JSON.parse(localStorage.getItem(recoveryKey(1)) ?? '').idempotencyKey,
      keys[0]
    )
    assert.equal(findButton('Buy phone activation').disabled, true)
  } finally {
    await probe.close()
  }
})

test('retains recovery when changing operator, country, and service', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  api.post = (async () => {
    throw transportError()
  }) as typeof api.post
  const probe = await mount(true)
  const assertRetained = (saved: unknown) => {
    assert.deepEqual(
      JSON.parse(localStorage.getItem(recoveryKey(1)) ?? ''),
      saved
    )
    assert.ok(
      document.body.textContent?.includes('Resolve purchase and continue')
    )
    const buy = Array.from(document.querySelectorAll('button')).find((button) =>
      /^Buy .*activations?$/.test(button.textContent ?? '')
    )
    assert.ok(buy)
    assert.equal(buy.disabled, true)
  }
  const selectOption = async (id: string, label: string) => {
    await act(async () => {
      const control = document.getElementById(id)
      assert.ok(control)
      control.click()
    })
    await settle(
      () =>
        Array.from(document.querySelectorAll('[role="option"]')).some(
          (option) => option.textContent?.includes(label)
        ),
      `Missing ${id} option ${label}`
    )
    await act(async () => {
      const option = Array.from(
        document.querySelectorAll<HTMLElement>('[role="option"]')
      ).find((candidate) => candidate.textContent?.includes(label))
      assert.ok(option)
      option.click()
    })
  }
  try {
    await chooseFavorite(probe)
    await confirmPurchase()
    await settle(
      () =>
        document.body.textContent?.includes('Resolve purchase and continue') ===
        true
    )
    const saved = JSON.parse(localStorage.getItem(recoveryKey(1)) ?? '')
    await selectOption('hero-sms-operator', 'fixture-operator')
    assertRetained(saved)
    await selectOption('hero-sms-country', 'Canada')
    assertRetained(saved)
    await selectOption('hero-sms-service', 'Google')
    assertRetained(saved)
  } finally {
    await probe.close()
  }
})

for (const status of [401, 403, 404, 409, 202]) {
  test(`retains the first unproven HTTP ${status} purchase failure`, async () => {
    login(1)
    mockPanelApi(async () => response(1, 5_000_000))
    const keys: string[] = []
    api.post = (async (
      _url: string,
      _body: unknown,
      config: { headers?: Record<string, string> }
    ) => {
      keys.push(config.headers?.['Idempotency-Key'] ?? '')
      throw transportError(status)
    }) as typeof api.post
    const probe = await mount(true)
    try {
      await chooseFavorite(probe)
      await confirmPurchase()
      await settle(
        () =>
          document.body.textContent?.includes(
            'Resolve purchase and continue'
          ) === true
      )
      assert.equal(keys.length, 2)
      assert.equal(keys[0], keys[1])
      assert.equal(
        JSON.parse(localStorage.getItem(recoveryKey(1)) ?? '').idempotencyKey,
        keys[0]
      )
      assert.equal(findButton('Buy phone activation').disabled, true)
    } finally {
      await probe.close()
    }
  })
}

test('handles a definitive first minimum-balance refusal without retry or unknown recovery', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  let attempts = 0
  api.post = (async () => {
    attempts += 1
    throw Object.assign(new Error('fixture minimum balance'), {
      isAxiosError: true,
      response: {
        status: 402,
        data: { code: 'TEMPORARY_SMS_MINIMUM_BALANCE' },
      },
    })
  }) as typeof api.post
  const probe = await mount(true)
  try {
    await chooseFavorite(probe)
    await confirmPurchase()
    await settle(
      () =>
        document.body.textContent?.includes('Purchase not completed') === true
    )
    assert.equal(attempts, 1)
    assert.equal(localStorage.getItem(recoveryKey(1)), null)
    assert.equal(
      document.body.textContent?.includes('Resolve purchase and continue'),
      false
    )
  } finally {
    await probe.close()
  }
})

test('keeps the recovery pair through repeated timeouts, auth failures, and unproven business errors', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  const saved = pendingPurchase()
  localStorage.setItem(recoveryKey(1), JSON.stringify(saved))
  const responses = [undefined, 401, 403, 429, 409, 402, 500]
  const pairs: Array<{ offer: unknown; key: string | undefined }> = []
  api.post = (async (
    _url: string,
    body: unknown,
    config: { headers?: Record<string, string> }
  ) => {
    const index = pairs.length
    pairs.push({ offer: body, key: config.headers?.['Idempotency-Key'] })
    throw transportError(responses[index])
  }) as typeof api.post
  const probe = await mount(true)
  try {
    await settle(() => !findButton('Resolve purchase and continue').disabled)
    for (let index = 0; index < responses.length; index += 1) {
      await reconcilePurchase()
      await settle(
        () =>
          pairs.length === index + 1 &&
          !findButton('Resolve purchase and continue').disabled
      )
      assert.deepEqual(
        JSON.parse(localStorage.getItem(recoveryKey(1)) ?? ''),
        saved
      )
      assert.equal(findButton('Buy phone activation').disabled, true)
    }
    assert.ok(pairs.every((pair) => pair.key === saved.idempotencyKey))
    assert.ok(
      pairs.every(
        (pair) =>
          JSON.stringify(pair.offer) ===
          JSON.stringify({ offer_id: saved.offerId })
      )
    )
  } finally {
    await probe.close()
  }
})

test('isolates account recovery and does not retry an old purchase using a new account', async () => {
  login(1)
  mockPanelApi(async () =>
    response(useAuthStore.getState().auth.user?.id ?? 0, 5_000_000)
  )
  const post = deferred<ReturnType<typeof createdOrder>>()
  let attempts = 0
  api.post = (async () => {
    attempts += 1
    return post.promise
  }) as typeof api.post
  const probe = await mount(true)
  try {
    await chooseFavorite(probe)
    await confirmPurchase()
    await settle(() => attempts === 1)
    const saved = JSON.parse(localStorage.getItem(recoveryKey(1)) ?? '')
    await act(async () => login(2))
    await settle(() => probe.balance.canPurchase)
    await act(async () => post.reject(transportError()))
    await settle(() => useAuthStore.getState().auth.user?.id === 2)
    assert.deepEqual(
      JSON.parse(localStorage.getItem(recoveryKey(1)) ?? ''),
      saved
    )
    assert.equal(localStorage.getItem(recoveryKey(2)), null)
    assert.equal(useAuthStore.getState().auth.user?.quota, 5_000_000)
    assert.equal(
      document.body.textContent?.includes('Resolve purchase and continue'),
      false
    )
    assert.equal(attempts, 1)
    await act(async () => login(1, 'new-session-1'))
    await settle(
      () =>
        document.body.textContent?.includes('Resolve purchase and continue') ===
        true
    )
    api.post = (async (
      _url: string,
      _body: unknown,
      config: { authScope?: { userId: number; sessionId?: string } }
    ) => {
      assert.deepEqual(config.authScope, {
        userId: 1,
        sessionId: 'new-session-1',
      })
      attempts += 1
      return createdOrder()
    }) as typeof api.post
    await settle(() => !findButton('Resolve purchase and continue').disabled)
    await reconcilePurchase()
    await settle(() => localStorage.getItem(recoveryKey(1)) === null)
    assert.equal(attempts, 2)
  } finally {
    await probe.close()
  }
})

test('ignores a late successful purchase callback after the same user signs in with a new session', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  const post = deferred<ReturnType<typeof createdOrder>>()
  let attempts = 0
  api.post = (async () => {
    attempts += 1
    return post.promise
  }) as typeof api.post
  const probe = await mount(true)
  try {
    await chooseFavorite(probe)
    await confirmPurchase()
    await settle(() => attempts === 1)
    const saved = JSON.parse(localStorage.getItem(recoveryKey(1)) ?? '')
    await act(async () => login(1, 'replacement-session'))
    await settle(() => probe.balance.canPurchase)
    await act(async () => post.resolve(createdOrder()))
    await settle(() => !findButton('Resolve purchase and continue').disabled)
    assert.deepEqual(
      JSON.parse(localStorage.getItem(recoveryKey(1)) ?? ''),
      saved
    )
    assert.equal(useAuthStore.getState().auth.user?.quota, 5_000_000)
    assert.equal(findButton('Buy phone activation').disabled, true)
    assert.equal(attempts, 1)
  } finally {
    await probe.close()
  }
})

test('fails closed if an existing recovery cannot be read even when storage writes succeed', () => {
  const original = pendingPurchase()
  saveSmsPurchaseRecovery(original)
  const descriptor = Object.getOwnPropertyDescriptor(window, 'localStorage')
  const stored = window.localStorage
  let writes = 0
  let deletes = 0
  try {
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        getItem: () => {
          throw new Error('fixture read failure')
        },
        setItem: () => {
          writes += 1
        },
        removeItem: () => {
          deletes += 1
        },
      },
    })
    assert.throws(() =>
      saveSmsPurchaseRecovery({ ...original, idempotencyKey: 'new-key' })
    )
    clearSmsPurchaseRecovery(original)
    assert.equal(writes, 0)
    assert.equal(deletes, 0)
  } finally {
    if (descriptor) Object.defineProperty(window, 'localStorage', descriptor)
    else {
      Object.defineProperty(window, 'localStorage', {
        configurable: true,
        value: stored,
      })
    }
  }
  assert.deepEqual(JSON.parse(stored.getItem(recoveryKey(1)) ?? ''), original)
})

test('does not clear a newer durable item when an old completion arrives', () => {
  const original = pendingPurchase()
  saveSmsPurchaseRecovery(original)
  const newer = { ...original, offerId: 'new-quote', idempotencyKey: 'new-key' }
  localStorage.setItem(recoveryKey(1), JSON.stringify(newer))
  clearSmsPurchaseRecovery(original)
  assert.deepEqual(
    JSON.parse(localStorage.getItem(recoveryKey(1)) ?? ''),
    newer
  )
})

test('does not leave an orphan recovery after another resolver settled the durable item', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  const retry = deferred<ReturnType<typeof createdOrder>>()
  let attempts = 0
  api.post = (async () => {
    attempts += 1
    if (attempts === 1) throw transportError()
    return retry.promise
  }) as typeof api.post
  const probe = await mount(true)
  try {
    await chooseFavorite(probe)
    await confirmPurchase()
    await settle(() => attempts === 2)
    const saved = JSON.parse(localStorage.getItem(recoveryKey(1)) ?? '')
    await act(async () => clearSmsPurchaseRecovery(saved))
    await act(async () => retry.reject(transportError()))
    await settle(() => !findButton('Buy phone activation').disabled)
    assert.equal(localStorage.getItem(recoveryKey(1)), null)
    assert.equal(
      document.body.textContent?.includes('Resolve purchase and continue'),
      false
    )
    assert.equal(attempts, 2)
  } finally {
    await probe.close()
  }
})

test('does not send a purchase when durable browser storage is unavailable', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  let attempts = 0
  api.post = (async () => {
    attempts += 1
    return createdOrder()
  }) as typeof api.post
  const probe = await mount(true)
  const storageDescriptor = Object.getOwnPropertyDescriptor(
    window,
    'localStorage'
  )
  try {
    await chooseFavorite(probe)
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get: () => {
        throw new Error('fixture storage blocked')
      },
    })
    await confirmPurchase()
    await settle(
      () =>
        document.body.textContent?.includes('Purchase not completed') === true
    )
    assert.equal(attempts, 0)
  } finally {
    if (storageDescriptor) {
      Object.defineProperty(window, 'localStorage', storageDescriptor)
    } else delete (window as { localStorage?: Storage }).localStorage
    await probe.close()
  }
})

test('updates an already open panel when another browser tab saves or settles recovery', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  const probe = await mount(true)
  try {
    await chooseFavorite(probe)
    const saved = pendingPurchase()
    await act(async () => {
      localStorage.setItem(recoveryKey(1), JSON.stringify(saved))
      window.dispatchEvent(new StorageEvent('storage', { key: recoveryKey(1) }))
    })
    assert.equal(findButton('Buy phone activation').disabled, true)
    assert.ok(
      document.body.textContent?.includes('Resolve purchase and continue')
    )
    await act(async () => {
      localStorage.removeItem(recoveryKey(1))
      window.dispatchEvent(new StorageEvent('storage', { key: recoveryKey(1) }))
    })
    assert.equal(findButton('Buy phone activation').disabled, false)
    assert.equal(
      document.body.textContent?.includes('Resolve purchase and continue'),
      false
    )
  } finally {
    await probe.close()
  }
})

test('does not send a purchase if durable storage reads work but writes fail', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  let attempts = 0
  api.post = (async () => {
    attempts += 1
    return createdOrder()
  }) as typeof api.post
  const probe = await mount(true)
  const storageDescriptor = Object.getOwnPropertyDescriptor(
    window,
    'localStorage'
  )
  const stored = window.localStorage
  try {
    await chooseFavorite(probe)
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        getItem: stored.getItem.bind(stored),
        removeItem: stored.removeItem.bind(stored),
        setItem: () => {
          throw new Error('fixture storage write failure')
        },
      },
    })
    await confirmPurchase()
    await settle(
      () =>
        document.body.textContent?.includes('Purchase not completed') === true
    )
    assert.equal(attempts, 0)
    assert.equal(stored.getItem(recoveryKey(1)), null)
    assert.equal(
      document.body.textContent?.includes('Resolve purchase and continue'),
      false
    )
  } finally {
    if (storageDescriptor) {
      Object.defineProperty(window, 'localStorage', storageDescriptor)
    } else {
      Object.defineProperty(window, 'localStorage', {
        configurable: true,
        value: stored,
      })
    }
    await probe.close()
  }
})

test('unblocks a new purchase only after the server proves the original item was not created', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  const probe = await mount(true)
  const saved = pendingPurchase()
  const attempts: Array<{ body: unknown; key: string | undefined }> = []
  api.post = (async (
    _url: string,
    body: unknown,
    config: { headers?: Record<string, string> }
  ) => {
    attempts.push({ body, key: config.headers?.['Idempotency-Key'] })
    if (attempts.length === 1) {
      throw Object.assign(
        new Error('HeroSMS SMS purchase was not created; refresh the quote'),
        {
          isAxiosError: true,
          response: { status: 409, data: { code: 'PURCHASE_NOT_CREATED' } },
        }
      )
    }
    return createdOrder()
  }) as typeof api.post
  try {
    await chooseFavorite(probe)
    await act(async () => saveSmsPurchaseRecovery(saved))
    assert.equal(findButton('Buy phone activation').disabled, true)
    await reconcilePurchase()
    await settle(() => !findButton('Buy phone activation').disabled)
    assert.equal(localStorage.getItem(recoveryKey(1)), null)
    assert.equal(
      document.body.textContent?.includes('Resolve purchase and continue'),
      false
    )
    assert.ok(
      document.body.textContent?.includes(
        'The price changed before item 1. Review the new quote.'
      )
    )
    assert.equal(
      document.body.textContent?.includes(
        'HeroSMS SMS purchase was not created'
      ),
      false
    )
    assert.deepEqual(attempts, [
      { body: { offer_id: saved.offerId }, key: saved.idempotencyKey },
    ])
    await confirmPurchase()
    await settle(() => attempts.length === 2)
    assert.notEqual(attempts[1]?.key, saved.idempotencyKey)
    assert.deepEqual(attempts[1]?.body, { offer_id: 'quote' })
  } finally {
    await probe.close()
  }
})

test('maps a definitive first no-purchase proof to the existing quote-changed feedback', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  let attempts = 0
  api.post = (async () => {
    attempts += 1
    throw Object.assign(
      new Error('HeroSMS SMS purchase was not created; refresh the quote'),
      {
        isAxiosError: true,
        response: { status: 409, data: { code: 'PURCHASE_NOT_CREATED' } },
      }
    )
  }) as typeof api.post
  const probe = await mount(true)
  try {
    await chooseFavorite(probe)
    await confirmPurchase()
    await settle(
      () =>
        document.body.textContent?.includes(
          'The price changed before item 1. Review the new quote.'
        ) === true
    )
    assert.equal(attempts, 1)
    assert.equal(localStorage.getItem(recoveryKey(1)), null)
    assert.equal(
      document.body.textContent?.includes('Resolve purchase and continue'),
      false
    )
    assert.equal(
      document.body.textContent?.includes(
        'HeroSMS SMS purchase was not created'
      ),
      false
    )
  } finally {
    await probe.close()
  }
})

test('retains recovery when a no-purchase code has the wrong HTTP status or a normal refusal is returned', async () => {
  login(1)
  mockPanelApi(async () => response(1, 5_000_000))
  const saved = pendingPurchase()
  localStorage.setItem(recoveryKey(1), JSON.stringify(saved))
  const replies = [
    { status: 500, code: 'PURCHASE_NOT_CREATED' },
    { status: 200, code: 'PURCHASE_NOT_CREATED' },
    { status: 401, code: 'PURCHASE_NOT_CREATED' },
    { status: 409, code: 'PRICE_CHANGED' },
    { status: 402, code: 'TEMPORARY_SMS_MINIMUM_BALANCE' },
    { status: 402, code: 'INSUFFICIENT_BALANCE' },
  ]
  let attempts = 0
  api.post = (async (
    _url: string,
    body: unknown,
    config: { headers?: Record<string, string> }
  ) => {
    assert.deepEqual(body, { offer_id: saved.offerId })
    assert.equal(config.headers?.['Idempotency-Key'], saved.idempotencyKey)
    const reply = replies[attempts++]
    assert.ok(reply)
    if (reply.status === 200) {
      return {
        data: {
          success: false,
          code: reply.code,
          message: 'fixture unproven response',
        },
      }
    }
    throw Object.assign(new Error('fixture unproven response'), {
      isAxiosError: true,
      response: { status: reply.status, data: { code: reply.code } },
    })
  }) as typeof api.post
  const probe = await mount(true)
  try {
    for (let index = 0; index < replies.length; index += 1) {
      await reconcilePurchase()
      await settle(
        () =>
          attempts === index + 1 &&
          !findButton('Resolve purchase and continue').disabled
      )
      assert.deepEqual(
        JSON.parse(localStorage.getItem(recoveryKey(1)) ?? ''),
        saved
      )
      assert.equal(findButton('Buy phone activation').disabled, true)
    }
  } finally {
    await probe.close()
  }
})

test('does not overwrite a newer balance when an unmounted original purchase returns after recovery', async () => {
  login(1)
  let serverQuota = 10_000_000
  mockPanelApi(async () => response(1, serverQuota))
  const original = deferred<ReturnType<typeof createdOrder>>()
  const keys: string[] = []
  api.post = (async (
    _url: string,
    _body: unknown,
    config: { headers?: Record<string, string> }
  ) => {
    keys.push(config.headers?.['Idempotency-Key'] ?? '')
    if (keys.length === 1) return original.promise
    serverQuota = keys.length === 2 ? 9_500_000 : 9_000_000
    return createdOrder(serverQuota)
  }) as typeof api.post
  const first = await mount(true)
  await chooseFavorite(first)
  await confirmPurchase()
  await settle(() => keys.length === 1)
  await first.close()
  const second = await mount(true)
  try {
    await reconcilePurchase()
    await settle(
      () =>
        localStorage.getItem(recoveryKey(1)) === null &&
        useAuthStore.getState().auth.user?.quota === 9_500_000
    )
    await chooseFavorite(second)
    await confirmPurchase()
    await settle(
      () =>
        keys.length === 3 &&
        useAuthStore.getState().auth.user?.quota === 9_000_000 &&
        !findButton('Buy phone activation').disabled
    )
    assert.equal(keys[0], keys[1])
    assert.notEqual(keys[0], keys[2])
    await act(async () => original.resolve(createdOrder(9_500_000)))
    assert.equal(
      useAuthStore.getState().auth.user?.quota,
      9_000_000,
      'a delayed response must not restore the earlier balance'
    )
    assert.equal(
      second.queryClient.getQueryData([
        'user',
        'sms-purchase-balance',
        1,
        'session-1',
      ]),
      9_000_000
    )
  } finally {
    await second.close()
  }
})

test('does not apply a late recovery balance or clear a newer partial batch result', async () => {
  login(1)
  let serverQuota = 10_000_000
  mockPanelApi(async () => response(1, serverQuota))
  const recovery = deferred<ReturnType<typeof createdOrder>>()
  const saved = pendingPurchase()
  let attempts = 0
  api.post = (async () => {
    attempts += 1
    if (attempts === 1) return recovery.promise
    if (attempts === 2) {
      serverQuota = 9_000_000
      return createdOrder(serverQuota)
    }
    throw Object.assign(new Error('fixture price changed'), {
      isAxiosError: true,
      response: { status: 409, data: { code: 'PRICE_CHANGED' } },
    })
  }) as typeof api.post
  const probe = await mount(true)
  let unsubscribe = () => {}
  try {
    await chooseFavorite(probe)
    await act(async () => saveSmsPurchaseRecovery(saved))
    await reconcilePurchase()
    await settle(() => attempts === 1)
    await act(async () => clearSmsPurchaseRecovery(saved))
    await chooseFavorite(probe, 2)
    await confirmPurchase()
    await settle(
      () =>
        attempts === 3 &&
        document.body.textContent?.includes(
          '1 of 2 phone activations were purchased'
        ) === true &&
        !findButton('Buy phone activation').disabled
    )
    const observed: Array<number | undefined> = []
    unsubscribe = useAuthStore.subscribe((state) =>
      observed.push(state.auth.user?.quota)
    )
    await act(async () => recovery.resolve(createdOrder(9_500_000)))
    assert.equal(useAuthStore.getState().auth.user?.quota, 9_000_000)
    assert.ok(
      observed.every((quota) => quota === 9_000_000),
      'the late recovery quota must never be applied, even before the fresh balance read'
    )
    assert.ok(
      document.body.textContent?.includes(
        '1 of 2 phone activations were purchased'
      )
    )
    assert.ok(document.body.textContent?.includes('fixture price changed'))
    assert.equal(localStorage.getItem(recoveryKey(1)), null)
  } finally {
    unsubscribe()
    await probe.close()
  }
})

test('keeps a newer pending item and its balance when an old recovery succeeds', async () => {
  login(1)
  mockPanelApi(async () => response(1, 9_000_000))
  const original = pendingPurchase()
  localStorage.setItem(recoveryKey(1), JSON.stringify(original))
  const recovery = deferred<ReturnType<typeof createdOrder>>()
  api.post = (async () => recovery.promise) as typeof api.post
  const probe = await mount(true)
  let unsubscribe = () => {}
  try {
    await reconcilePurchase()
    const newer = {
      ...original,
      offerId: 'new-quote',
      idempotencyKey: 'new-key',
    }
    await act(async () => {
      clearSmsPurchaseRecovery(original)
      saveSmsPurchaseRecovery(newer)
    })
    const observed: Array<number | undefined> = []
    unsubscribe = useAuthStore.subscribe((state) =>
      observed.push(state.auth.user?.quota)
    )
    await act(async () => recovery.resolve(createdOrder(9_500_000)))
    await settle(() => !findButton('Resolve purchase and continue').disabled)
    assert.deepEqual(
      JSON.parse(localStorage.getItem(recoveryKey(1)) ?? ''),
      newer
    )
    assert.equal(findButton('Buy phone activation').disabled, true)
    assert.equal(useAuthStore.getState().auth.user?.quota, 9_000_000)
    assert.ok(observed.every((quota) => quota === 9_000_000))
  } finally {
    unsubscribe()
    await probe.close()
  }
})

test('keeps recovery blocked without applying the response balance when durable deletion fails', async () => {
  login(1)
  mockPanelApi(async () => response(1, 9_000_000))
  const original = pendingPurchase()
  localStorage.setItem(recoveryKey(1), JSON.stringify(original))
  const recovery = deferred<ReturnType<typeof createdOrder>>()
  api.post = (async () => recovery.promise) as typeof api.post
  const probe = await mount(true)
  const descriptor = Object.getOwnPropertyDescriptor(window, 'localStorage')
  const stored = window.localStorage
  let unsubscribe = () => {}
  try {
    await reconcilePurchase()
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        getItem: stored.getItem.bind(stored),
        setItem: stored.setItem.bind(stored),
        removeItem: () => {
          throw new Error('fixture storage deletion failed')
        },
      },
    })
    const observed: Array<number | undefined> = []
    unsubscribe = useAuthStore.subscribe((state) =>
      observed.push(state.auth.user?.quota)
    )
    await act(async () => recovery.resolve(createdOrder(9_500_000)))
    await settle(() => !findButton('Resolve purchase and continue').disabled)
    assert.deepEqual(JSON.parse(stored.getItem(recoveryKey(1)) ?? ''), original)
    assert.equal(findButton('Buy phone activation').disabled, true)
    assert.equal(useAuthStore.getState().auth.user?.quota, 9_000_000)
    assert.ok(observed.every((quota) => quota === 9_000_000))
  } finally {
    unsubscribe()
    if (descriptor) {
      Object.defineProperty(window, 'localStorage', descriptor)
    } else {
      Object.defineProperty(window, 'localStorage', {
        configurable: true,
        value: stored,
      })
    }
    await probe.close()
  }
})
