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

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { UserAvatar } = await import('./user-avatar')
const { getGravatarUrl } = await import('@/lib/avatar')
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
).IS_REACT_ACT_ENVIRONMENT = true

after(() => domWindow.close())

test('account changes discard old requests and release each mounted blob URL', async () => {
  const originals = {
    caches: Object.getOwnPropertyDescriptor(globalThis, 'caches'),
    createObjectURL: URL.createObjectURL,
    revokeObjectURL: URL.revokeObjectURL,
  }
  let resolveOld: (response: Response) => void = () => {}
  let resolveNew: (response: Response) => void = () => {}
  const oldResponse = new Promise<Response>((resolve) => {
    resolveOld = resolve
  })
  const newResponse = new Promise<Response>((resolve) => {
    resolveNew = resolve
  })
  const oldUrl = await getGravatarUrl('old@example.com')
  const newUrl = await getGravatarUrl('new@example.com')
  const requested: string[] = []
  Object.defineProperty(globalThis, 'caches', {
    configurable: true,
    value: {
      open: async () => ({
        match: (url: string) => {
          requested.push(url)
          return url === oldUrl ? oldResponse : newResponse
        },
      }),
    },
  })
  const created: string[] = []
  const revoked: string[] = []
  URL.createObjectURL = () => {
    const url = `blob:test-${created.length}`
    created.push(url)
    return url
  }
  URL.revokeObjectURL = (url) => {
    revoked.push(url)
  }
  const container = document.createElement('div')
  const root = createRoot(container)
  const response = () =>
    new Response(new Blob(['avatar'], { type: 'image/png' }), {
      headers: { 'x-lmm-avatar-expires': String(Date.now() + 60_000) },
    })
  async function flush() {
    await new Promise((resolve) => setTimeout(resolve, 0))
  }
  try {
    await act(async () => {
      root.render(<UserAvatar name='Old' email='old@example.com' />)
      await flush()
    })
    await act(async () => {
      await flush()
    })
    assert.deepEqual(requested, [oldUrl])
    await act(async () => {
      root.render(<UserAvatar name='New' email='new@example.com' />)
    })
    await act(async () => {
      await flush()
    })
    assert.deepEqual(requested, [oldUrl, newUrl])
    await act(async () => {
      resolveNew(response())
      await flush()
    })
    assert.equal(
      container.querySelector('img')?.getAttribute('src'),
      'blob:test-0'
    )
    await act(async () => {
      resolveOld(response())
      await flush()
    })
    assert.equal(container.querySelector('img')?.getAttribute('alt'), 'New')
    assert.deepEqual(created, ['blob:test-0'])
    const image = container.querySelector('img')
    assert.ok(image)
    await act(async () => image.dispatchEvent(new Event('load')))
    assert.equal(image.classList.contains('invisible'), false)
    await act(async () => root.render(<UserAvatar name='Empty' email='' />))
    assert.equal(container.querySelector('img'), null)
    assert.equal(
      container.querySelector('[data-slot=avatar-fallback]')?.textContent,
      'E'
    )
    assert.deepEqual(revoked, ['blob:test-0'])
  } finally {
    await act(async () => root.unmount())
    URL.createObjectURL = originals.createObjectURL
    URL.revokeObjectURL = originals.revokeObjectURL
    if (originals.caches) {
      Object.defineProperty(globalThis, 'caches', originals.caches)
    } else {
      Reflect.deleteProperty(globalThis, 'caches')
    }
  }
})
