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

const domWindow = new Window({
  url: 'https://console.example.test/public-relay',
})
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
const originalGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'customElements',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  originalGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
originalGlobals.set(
  'matchMedia',
  Object.getOwnPropertyDescriptor(globalThis, 'matchMedia')
)
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: (media: string) => ({
    matches: false,
    media,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent() {
      return false
    },
  }),
})
originalGlobals.set(
  'IS_REACT_ACT_ENVIRONMENT',
  Object.getOwnPropertyDescriptor(globalThis, 'IS_REACT_ACT_ENVIRONMENT')
)
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { PublicRelay } = await import('./index')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

after(() => {
  domWindow.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('channel market does not request administrator data for contributors or ordinary admins', async () => {
  const originalAdapter = api.defaults.adapter
  const originalAuth = useAuthStore.getState().auth
  const requests: string[] = []
  api.defaults.adapter = async (config) => {
    const url = config.url ?? ''
    requests.push(url)
    const data =
      url === '/api/public-relays/config'
        ? { group: 'FREE', minimum_withdrawal_usd: 10 }
        : url.startsWith('/api/public-relays?')
          ? { group: 'FREE', items: [] }
          : []
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, data },
    }
  }

  const renderForRole = async (role: number) => {
    useAuthStore.getState().auth.setUser({
      id: role,
      username: `role-${role}`,
      role,
      developer_access_granted: true,
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <PublicRelay />
          </I18nextProvider>
        </QueryClientProvider>
      )
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    return { container, root, client }
  }

  const dispose = async (view: Awaited<ReturnType<typeof renderForRole>>) => {
    await act(async () => view.root.unmount())
    view.client.clear()
    view.container.remove()
  }

  try {
    const contributor = await renderForRole(1)
    try {
      const shareButton = [
        ...contributor.container.querySelectorAll('button'),
      ].find((button) => button.textContent?.includes('Share a channel'))
      assert.ok(shareButton, 'contributors can open the submission form')
      await act(async () => shareButton.click())
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 20))
      })
      assert.ok(
        requests.some((url) => url.startsWith('/api/public-relays?')),
        'the public catalog still loads'
      )
      assert.ok(
        requests.every(
          (url) =>
            ![
              '/api/channel/models',
              '/api/group/',
              '/api/prefill_group',
              '/api/option/',
            ].some((adminUrl) => url.startsWith(adminUrl))
        ),
        `contributor requests must stay within the public API: ${requests.join(', ')}`
      )
    } finally {
      await dispose(contributor)
    }

    requests.length = 0
    const admin = await renderForRole(10)
    try {
      assert.ok(
        !requests.includes('/api/option/'),
        'ordinary administrators cannot read root-only settings'
      )
    } finally {
      await dispose(admin)
    }
  } finally {
    api.defaults.adapter = originalAdapter
    useAuthStore.setState({ auth: originalAuth })
  }
})
