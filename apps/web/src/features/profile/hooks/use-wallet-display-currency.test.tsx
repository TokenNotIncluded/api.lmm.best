/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { UserProfile } from '../types'

const domWindow = new Window({ url: 'https://console.example.test/profile' })
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
    value: domWindow[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { useWalletDisplayCurrency } =
  await import('./use-wallet-display-currency')

const originalAuth = useAuthStore.getState().auth
const originalPut = api.put
type Hook = ReturnType<typeof useWalletDisplayCurrency>
type Props = Parameters<typeof useWalletDisplayCurrency>[0]
type RequestSignal = NonNullable<Parameters<typeof api.put>[2]>['signal']

function profile(id = 1, setting: Record<string, unknown> = {}): UserProfile {
  return {
    id,
    username: `test-user-${id}`,
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
    setting: JSON.stringify(setting),
  }
}

function setOwner(
  id = 1,
  sid = 'test-session',
  token = 'test-token',
  setting: Record<string, unknown> = {}
) {
  useAuthStore.setState({
    auth: {
      ...originalAuth,
      user: { id, username: `test-user-${id}`, role: 1, setting },
      accessToken: token,
      session: {
        sid,
        current: true,
        login_method: 'test',
        ip: '',
        user_agent: '',
        created_at: 1,
        last_active_at: 1,
        expires_at: 2,
      },
    },
  })
}

function deferred<T>() {
  let complete: ((value: T) => void) | undefined
  const promise = new Promise<T>((resolve) => {
    complete = resolve
  })
  return {
    promise,
    resolve(value: T) {
      assert.ok(complete)
      complete(value)
    },
  }
}

async function renderHook(props: Props) {
  let value: Hook | undefined
  function Harness(current: Props) {
    value = useWalletDisplayCurrency(current)
    return null
  }
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const render = async (next: Props) => {
    await act(async () => {
      root.render(<Harness {...next} />)
    })
  }
  await render(props)
  return {
    root,
    render,
    state() {
      assert.ok(value)
      return value
    },
  }
}

beforeEach(() => setOwner())
afterEach(() => {
  api.put = originalPut
  useAuthStore.setState({ auth: originalAuth })
  document.body.replaceChildren()
})
after(() => domWindow.close())

describe('saved wallet display preference', () => {
  test('writes one key and merges it into the latest same-owner settings after ACK', async () => {
    const receipt = deferred<{ data: { success: boolean } }>()
    const requests: { url: string; body: unknown; signal?: RequestSignal }[] =
      []
    api.put = (async (url, body, config) => {
      requests.push({ url, body, signal: config?.signal })
      assert.equal(config?.skipBusinessError, true)
      assert.equal(config?.skipErrorHandler, true)
      return receipt.promise
    }) as typeof api.put
    let refreshes = 0
    const props = {
      profile: profile(1, { settlement_currency: 'CNY' }),
      loading: false,
      onProfileUpdate: () => {
        refreshes += 1
      },
    }
    const rendered = await renderHook(props)
    try {
      let saving: Promise<void> | undefined
      await act(async () => {
        saving = rendered.state().save('USD')
      })
      assert.equal(rendered.state().saving, true)
      assert.equal(rendered.state().value, 'USD')
      const auth = useAuthStore.getState().auth
      const currentUser = auth.user
      assert.ok(currentUser)
      await act(async () =>
        auth.setUser({
          ...currentUser,
          setting: {
            language: 'ja',
            settlement_currency: 'USD',
            record_ip_log: true,
          },
        })
      )
      await act(async () => {
        receipt.resolve({ data: { success: true } })
        await saving
      })
      assert.equal(rendered.state().saved, true)
      assert.equal(rendered.state().saving, false)
      assert.equal(refreshes, 1)
      assert.deepEqual(
        requests.map(({ url, body }) => ({ url, body })),
        [{ url: '/api/user/self', body: { wallet_display_currency: 'USD' } }]
      )
      assert.ok(requests[0]?.signal)
      assert.deepEqual(
        JSON.parse(String(useAuthStore.getState().auth.user?.setting)),
        {
          language: 'ja',
          settlement_currency: 'USD',
          record_ip_log: true,
          wallet_display_currency: 'USD',
        }
      )
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })

  test('a failed profile refresh and stale language-only profile cannot undo a successful save', async () => {
    api.put = (async () => ({ data: { success: true } })) as typeof api.put
    const props = {
      profile: profile(1, { wallet_display_currency: 'CNY', language: 'zh' }),
      loading: false,
      onProfileUpdate: () => {
        throw new Error('Profile read failed')
      },
    }
    const rendered = await renderHook(props)
    try {
      await act(async () => {
        await rendered.state().save('USD')
      })
      await rendered.render({
        ...props,
        profile: profile(1, { wallet_display_currency: 'CNY', language: 'ja' }),
      })
      assert.equal(rendered.state().value, 'USD')
      assert.equal(rendered.state().saved, true)
      assert.equal(rendered.state().failedValue, null)
      assert.equal(
        JSON.parse(String(useAuthStore.getState().auth.user?.setting))
          .wallet_display_currency,
        'USD'
      )
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })

  test('retries a failed selection without changing the saved wallet or settlement preference', async () => {
    setOwner(1, 'test-session', 'test-token', { settlement_currency: 'CNY' })
    let calls = 0
    api.put = (async () => ({
      data: { success: ++calls > 1 },
    })) as typeof api.put
    const rendered = await renderHook({
      profile: profile(),
      loading: false,
      onProfileUpdate: () => undefined,
    })
    try {
      await act(async () => {
        await rendered.state().save('CREDIT')
      })
      assert.equal(rendered.state().value, '')
      assert.equal(rendered.state().failedValue, 'CREDIT')
      assert.equal(rendered.state().saved, false)
      assert.deepEqual(useAuthStore.getState().auth.user?.setting, {
        settlement_currency: 'CNY',
      })
      await act(async () => {
        await rendered.state().save('CREDIT')
      })
      assert.equal(rendered.state().saved, true)
      assert.equal(rendered.state().value, 'CREDIT')
      assert.deepEqual(
        JSON.parse(String(useAuthStore.getState().auth.user?.setting)),
        { settlement_currency: 'CNY', wallet_display_currency: 'CREDIT' }
      )
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })

  test('deduplicates pending changes and does not write before the owner profile is loaded', async () => {
    const receipt = deferred<{ data: { success: boolean } }>()
    let calls = 0
    api.put = (async () => {
      calls += 1
      return receipt.promise
    }) as typeof api.put
    const props = {
      profile: profile(),
      loading: true,
      onProfileUpdate: () => undefined,
    }
    const rendered = await renderHook(props)
    try {
      await act(async () => {
        await rendered.state().save('USD')
      })
      assert.equal(calls, 0)
      await rendered.render({ ...props, loading: false, profile: profile(2) })
      assert.equal(rendered.state().available, false)
      await act(async () => {
        await rendered.state().save('USD')
      })
      assert.equal(calls, 0)
      await rendered.render({ ...props, loading: false })
      let saving: Promise<void> | undefined
      await act(async () => {
        saving = rendered.state().save('USD')
        await rendered.state().save('CNY')
      })
      assert.equal(calls, 1)
      await act(async () => {
        receipt.resolve({ data: { success: true } })
        await saving
      })
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })

  for (const changed of ['user', 'session', 'token'] as const) {
    test(`discards pending writes and confirmations after a ${changed} change`, async () => {
      const receipt = deferred<{ data: { success: boolean } }>()
      let signal: RequestSignal
      api.put = (async (_url, _body, config) => {
        signal = config?.signal
        return receipt.promise
      }) as typeof api.put
      let refreshes = 0
      const props = {
        profile: profile(),
        loading: false,
        onProfileUpdate: () => {
          refreshes += 1
        },
      }
      const rendered = await renderHook(props)
      try {
        let saving: Promise<void> | undefined
        await act(async () => {
          saving = rendered.state().save('CREDIT')
        })
        const id = changed === 'user' ? 2 : 1
        await act(async () =>
          setOwner(
            id,
            changed === 'session' ? 'new-session' : 'test-session',
            changed === 'token' ? 'new-token' : 'test-token',
            { wallet_display_currency: 'USD' }
          )
        )
        await rendered.render({
          ...props,
          profile: profile(id, { wallet_display_currency: 'USD' }),
        })
        assert.equal(signal?.aborted, true)
        assert.equal(rendered.state().value, 'USD')
        assert.equal(rendered.state().saving, false)
        assert.equal(rendered.state().failedValue, null)
        assert.equal(rendered.state().saved, false)
        await act(async () => {
          receipt.resolve({ data: { success: true } })
          await saving
        })
        assert.equal(refreshes, 0)
        assert.deepEqual(useAuthStore.getState().auth.user?.setting, {
          wallet_display_currency: 'USD',
        })
      } finally {
        await act(async () => rendered.root.unmount())
      }
    })
  }

  test('a batched switch away and back cannot accept an old ACK or block the next save', async () => {
    const staleReceipt = deferred<{ data: { success: boolean } }>()
    let calls = 0
    api.put = (async () =>
      ++calls === 1
        ? staleReceipt.promise
        : { data: { success: true } }) as typeof api.put
    const rendered = await renderHook({
      profile: profile(),
      loading: false,
      onProfileUpdate: () => undefined,
    })
    try {
      let oldSaving: Promise<void> | undefined
      await act(async () => {
        oldSaving = rendered.state().save('CREDIT')
      })
      const firstOwner = useAuthStore.getState().auth
      await act(async () => {
        setOwner(2)
        useAuthStore.setState({ auth: firstOwner })
      })
      assert.equal(rendered.state().saving, false)
      assert.equal(rendered.state().value, '')
      await act(async () => {
        await rendered.state().save('CNY')
      })
      assert.equal(rendered.state().value, 'CNY')
      await act(async () => {
        staleReceipt.resolve({ data: { success: true } })
        await oldSaving
      })
      assert.equal(rendered.state().value, 'CNY')
      assert.equal(
        JSON.parse(String(useAuthStore.getState().auth.user?.setting))
          .wallet_display_currency,
        'CNY'
      )
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })
})
