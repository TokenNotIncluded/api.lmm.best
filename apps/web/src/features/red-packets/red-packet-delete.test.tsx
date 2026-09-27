/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://example.test/red-packets' })
domWindow.document.write('<!doctype html><html><body></body></html>')
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'matchMedia',
  'customElements',
  'CSSStyleSheet',
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

const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const {
  createRouter,
  createRootRoute,
  createMemoryHistory,
  RouterContextProvider,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { RedPackets } = await import('./index')
const { api } = await import('@/lib/api')
const originalGet = api.get
const originalDelete = api.delete
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
const clients: InstanceType<typeof QueryClient>[] = []
const mounted: {
  root: ReturnType<typeof createRoot>
  element: HTMLDivElement
}[] = []
afterEach(async () => {
  for (const { root, element } of mounted.splice(0)) {
    await act(async () => root.unmount())
    element.remove()
  }
  for (const client of clients.splice(0)) client.clear()
  api.get = originalGet
  api.delete = originalDelete
})
after(() => domWindow.close())

async function renderPackets() {
  const packet = {
    id: 1,
    slug: 'live',
    title: 'Live packet',
    enabled: true,
    start_at: 0,
    end_at: 0,
    total_items: 22,
    remaining_items: 17,
    claim_count: 5,
  }
  let packets = [
    packet,
    {
      ...packet,
      id: 2,
      slug: 'empty',
      title: 'Empty packet',
      total_items: 7,
      remaining_items: 0,
      claim_count: 7,
    },
  ]
  api.get = (async (url: string) => ({
    data: {
      success: true,
      data: url === '/api/red-packet/admin' ? packets : {},
    },
  })) as typeof api.get
  const element = document.createElement('div')
  document.body.appendChild(element)
  const root = createRoot(element)
  mounted.push({ root, element })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const router = createRouter({
    routeTree: createRootRoute(),
    history: createMemoryHistory(),
  })
  await act(async () => {
    root.render(
      <RouterContextProvider router={router}>
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <RedPackets />
          </I18nextProvider>
        </QueryClientProvider>
      </RouterContextProvider>
    )
    await new Promise((resolve) => setTimeout(resolve, 25))
  })
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 25))
  })
  return {
    element,
    remove: () => {
      packets = [packet]
    },
  }
}
function button(text: string, parent: ParentNode = document) {
  const found = [...parent.querySelectorAll<HTMLButtonElement>('button')].find(
    (x) => x.textContent?.trim() === text
  )
  assert.ok(found, `button ${text} exists`)
  return found
}

test('exhausted packets can be deleted with confirmation; active packets have no delete action', async () => {
  const { element, remove } = await renderPackets()
  assert.ok(
    element.querySelector('[data-packet-id="1"]')?.textContent?.includes('Live')
  )
  assert.equal(
    element.querySelector(
      '[data-packet-id="1"] button[aria-label="Delete red packet"]'
    ),
    null
  )
  assert.ok(
    element
      .querySelector('[data-packet-id="2"]')
      ?.textContent?.includes('Fully claimed')
  )
  let calls = 0
  let resolve!: (response: { data: { success: boolean } }) => void
  api.delete = ((url: string) => {
    assert.equal(url, '/api/red-packet/admin/2')
    calls++
    return new Promise<{ data: { success: boolean } }>((accept) => {
      resolve = accept
    })
  }) as typeof api.delete
  await act(async () => button('Delete', element).click())
  assert.ok(document.querySelector('[role="alertdialog"]'))
  await act(async () => button('Cancel').click())
  assert.equal(calls, 0)
  await act(async () => button('Delete', element).click())
  const dialog = document.querySelector('[role="alertdialog"]')!
  await act(async () => button('Delete', dialog).click())
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
  assert.equal(button('Delete', dialog).disabled, true)
  assert.equal(button('Cancel', dialog).disabled, true)
  await act(async () => button('Delete', dialog).click())
  assert.equal(calls, 1)
  await act(async () => {
    remove()
    resolve({ data: { success: true } })
    await new Promise((accept) => setTimeout(accept, 25))
  })
  await act(async () => {
    await new Promise((accept) => setTimeout(accept, 25))
  })
  assert.equal(element.querySelector('[data-packet-id="2"]'), null)
  assert.ok(element.querySelector('[data-packet-id="1"]'))
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
})

for (const failure of ['business', 'network']) {
  test(`${failure} deletion failure retains the card and allows retry`, async () => {
    const { element } = await renderPackets()
    api.delete = (async () => {
      if (failure === 'network') throw new Error('offline')
      return { data: { success: false, message: 'Still active' } }
    }) as typeof api.delete
    await act(async () => button('Delete', element).click())
    const dialog = document.querySelector('[role="alertdialog"]')!
    await act(async () => button('Delete', dialog).click())
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    assert.ok(element.querySelector('[data-packet-id="2"]'))
    assert.ok(document.querySelector('[role="alertdialog"]'))
    assert.equal(button('Delete', dialog).disabled, false)
  })
}
