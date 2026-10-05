/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const window = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: key === 'window' ? window : window[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { default: i18n } = await import('@/i18n/config')
const { api } = await import('@/lib/http-client')
const { useAuthStore } = await import('@/stores/auth-store')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { useWalletCurrency } = await import('./use-wallet-currency')
const initialConfig = useSystemConfigStore.getState().config
const initialAuth = useAuthStore.getState().auth
const initialPut = api.put
after(() => {
  api.put = initialPut
  useAuthStore.setState({ auth: initialAuth })
  useSystemConfigStore.setState({ config: initialConfig })
  window.happyDOM.abort()
})

async function render() {
  let value: ReturnType<typeof useWalletCurrency> | undefined
  function Harness() {
    value = useWalletCurrency()
    return <p>{value.formatQuota(10)}</p>
  }
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<Harness />)
  })
  return {
    current: () => {
      assert.ok(value)
      return value
    },
    container,
    close: async () => {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

function configure() {
  useAuthStore.getState().auth.reset('idle')
  useWalletCurrencyPreferenceStore.getState().setPreference('')
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 10,
      creditsPerUsdExact: '10',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
      quotaPerUnit: 2,
    },
  })
}

test('reacts to language, anonymous preference and FX while prior closures retain their denomination', async () => {
  configure()
  await i18n.changeLanguage('en')
  const rendered = await render()
  try {
    assert.equal(rendered.container.textContent, '1 USD')
    const first = rendered.current()
    await act(async () => {
      await i18n.changeLanguage('zhCN')
    })
    assert.equal(rendered.current().currency, 'CNY')
    assert.equal(rendered.container.textContent, '7 CNY')
    assert.equal(first.formatQuota(10), '1 USD')
    const cny = rendered.current()
    await act(async () => {
      useSystemConfigStore.getState().setConfig({
        currency: {
          ...useSystemConfigStore.getState().config.currency,
          cnyPerUsd: 8,
          cnyPerUsdExact: '8',
        },
      })
    })
    assert.equal(rendered.container.textContent, '8 CNY')
    assert.equal(cny.amountToQuota('7'), 10)
    assert.equal(cny.formatQuota(10), '7 CNY')
    await act(async () => {
      await rendered.current().setPreference('CREDIT')
    })
    assert.equal(rendered.current().currency, 'CREDIT')
    assert.equal(rendered.current().quotaToInput(1), '1')
    assert.equal(rendered.current().step, 1)
  } finally {
    await rendered.close()
  }
})

test('acknowledged account preference merges only its key and cannot leak through a late previous-account reply', async () => {
  configure()
  await i18n.changeLanguage('en')
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'a',
    role: 1,
    setting: { language: 'en', settlement_currency: 'USD' },
  })
  let resolveOld:
    | ((value: Awaited<ReturnType<typeof api.put>>) => void)
    | undefined
  const calls: unknown[] = []
  api.put = (async (_url, payload) => {
    calls.push(payload)
    if (calls.length === 1) {
      return await new Promise<Awaited<ReturnType<typeof api.put>>>(
        (resolve) => {
          resolveOld = resolve
        }
      )
    }
    return { data: { success: true } }
  }) as typeof api.put
  const rendered = await render()
  try {
    let oldWrite: Promise<boolean> | undefined
    await act(async () => {
      oldWrite = rendered.current().setPreference('CNY')
      await Promise.resolve()
    })
    await act(async () => {
      useAuthStore.getState().auth.setUser({
        id: 2,
        username: 'b',
        role: 1,
        setting: { language: 'en', settlement_currency: 'CNY' },
      })
    })
    assert.equal(rendered.current().saving, false)
    await act(async () => {
      assert.equal(await rendered.current().setPreference('CREDIT'), true)
    })
    assert.equal(rendered.current().currency, 'CREDIT')
    await act(async () => {
      assert.ok(resolveOld)
      resolveOld({ data: { success: true } } as Awaited<
        ReturnType<typeof api.put>
      >)
      await oldWrite
    })
    assert.equal(rendered.current().currency, 'CREDIT')
    assert.equal(useAuthStore.getState().auth.user?.id, 2)
    const setting = JSON.parse(
      String(useAuthStore.getState().auth.user?.setting)
    )
    assert.equal(setting.settlement_currency, 'CNY')
    assert.deepEqual(calls, [
      { wallet_display_currency: 'CNY' },
      { wallet_display_currency: 'CREDIT' },
    ])
  } finally {
    await rendered.close()
    api.put = initialPut
  }
})

test('failed account saves retain the previous unit and permit a successful retry', async () => {
  configure()
  await i18n.changeLanguage('en')
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'a',
    role: 1,
    setting: { wallet_display_currency: 'USD' },
  })
  let calls = 0
  api.put = (async () => ({ data: { success: ++calls > 1 } })) as typeof api.put
  const rendered = await render()
  try {
    await act(async () => {
      assert.equal(await rendered.current().setPreference('CNY'), false)
    })
    assert.equal(rendered.current().currency, 'USD')
    assert.equal(rendered.current().saving, false)
    assert.equal(
      rendered.current().error,
      'Could not save balance display currency. Try again.'
    )
    await act(async () => {
      assert.equal(await rendered.current().setPreference('CNY'), true)
    })
    assert.equal(rendered.current().currency, 'CNY')
    assert.equal(rendered.current().error, null)
  } finally {
    await rendered.close()
    api.put = initialPut
  }
})
