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
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLTextAreaElement',
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
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { AssistantActivationTool } = await import('./assistant-activation-tool')

const originalGet = api.get
const originalPost = api.post
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})


async function renderState(state: string, failed = false) {
  useAuthStore.setState({ user: { id: 71, username: 'test', role: 1 } })
  api.get = (async (url: string) => {
    assert.equal(url, '/api/assistant/registration-check')
    if (failed) throw new Error('offline')
    return { data: { success: true, data: { state } } }
  }) as typeof api.get
  api.post = (async () => { throw new Error('The status card must never submit a recommendation') }) as typeof api.post
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<QueryClientProvider client={queryClient}><I18nextProvider i18n={i18n}><AssistantActivationTool /></I18nextProvider></QueryClientProvider>)
    await new Promise((resolve) => setTimeout(resolve, 50))
  })
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)) })
  return { container, async cleanup() { await act(async () => root.unmount()); queryClient.clear(); container.remove() } }
}
afterEach(() => { api.get = originalGet; api.post = originalPost; useAuthStore.setState({ user: null }) })
after(() => domWindow.close())
describe('tool-based admission status', () => {
  test('does not show or submit a recommendation letter', async () => {
    const view = await renderState('ready')
    try { assert.match(view.container.textContent ?? '', /No recommendation letter is required/); assert.equal(view.container.querySelector('textarea'), null) } finally { await view.cleanup() }
  })
  test('hold preserves a human support explanation', async () => {
    const view = await renderState('held')
    try { assert.match(view.container.textContent ?? '', /human support/); assert.match(view.container.textContent ?? '', /on hold/) } finally { await view.cleanup() }
  })
  test('request failure does not imply activation', async () => {
    const view = await renderState('active', true)
    try { assert.match(view.container.textContent ?? '', /status is unavailable/); assert.doesNotMatch(view.container.textContent ?? '', /L1 access is active/) } finally { await view.cleanup() }
  })
})
