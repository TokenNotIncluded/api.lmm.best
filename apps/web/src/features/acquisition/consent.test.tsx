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
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/' })
dom.document.write('<!doctype html><html><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Element',
  'Node',
  'Event',
  'MutationObserver',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: key === 'window' ? dom : dom[key],
  })
}
Object.assign(globalThis, {
  IS_REACT_ACT_ENVIRONMENT: true,
  __LMM_PERSONA_DEBUG__: false,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { initReactI18next } = await import('react-i18next')
await createInstance()
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { SourceConsent } = await import('./consent')
after(() => dom.close())

test('withdrawal failures remain visible and retryable instead of pretending server collection stopped', async () => {
  const originalPost = api.post,
    originalDelete = api.delete
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let attempts = 0
  api.post = (async () => ({ status: 204, data: '' })) as typeof api.post
  api.delete = (async () => {
    attempts++
    return { status: 200, data: { success: attempts > 1 } }
  }) as typeof api.delete
  useAuthStore.getState().auth.reset('complete')
  localStorage.setItem('lmm:source-consent:v2', 'yes')
  const button = (text: string) => {
    const found = Array.from(container.querySelectorAll('button')).find(
      (item) => item.textContent === text
    )
    assert.ok(found)
    return found
  }
  try {
    await act(async () => root.render(<SourceConsent />))
    await act(async () => button('Source privacy').click())
    await act(async () => button('Do not collect').click())
    assert.equal(attempts, 1)
    assert.ok(container.querySelector('[role="alert"]'))
    assert.equal(localStorage.getItem('lmm:source-consent:v2'), 'no')
    await act(async () => button('Do not collect').click())
    assert.equal(attempts, 2)
    assert.equal(container.querySelector('[role="alert"]'), null)
    assert.ok(button('Source privacy'))
  } finally {
    await act(async () => root.unmount())
    api.post = originalPost
    api.delete = originalDelete
    container.remove()
  }
})
