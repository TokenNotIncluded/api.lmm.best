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
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { useHeroSmsActivationDetail } = await import('./hooks')

const originalGet = api.get
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((nextResolve) => {
    resolve = nextResolve
  })
  return { promise, resolve }
}

function detailResponse({
  id,
  email,
  code,
}: {
  id: string
  email: string
  code: string
}) {
  return {
    data: {
      success: true,
      data: {
        id,
        order_id: `order-${id}`,
        domain_id: `domain-${id}`,
        email,
        code,
        status: 'completed',
        charge_quota: 10,
        cancel_reason: '',
        created_at: '2026-08-30T00:00:00Z',
        updated_at: '2026-08-30T00:01:00Z',
      },
    },
  }
}

async function waitForText(container: HTMLElement, text: string) {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    if (container.textContent === text) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
    })
  }
  assert.equal(container.textContent, text)
}

afterEach(() => {
  api.get = originalGet
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('email activation detail query', () => {
  test('does not expose the previous activation while a new detail is loading', async () => {
    const secondResponse = deferred<ReturnType<typeof detailResponse>>()
    let secondRequested = false

    api.get = (async (url: string) => {
      if (url.endsWith('/A')) {
        return detailResponse({
          id: 'A',
          email: 'alpha@example.test',
          code: '111111',
        })
      }
      if (url.endsWith('/B')) {
        secondRequested = true
        return secondResponse.promise
      }
      throw new Error(`Unexpected request: ${url}`)
    }) as typeof api.get

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    function DetailProbe({ activationId }: { activationId: string }) {
      const query = useHeroSmsActivationDetail(activationId)
      const activation = query.data?.activation
      return (
        <div>
          {activation
            ? `${activation.id}|${activation.email}|${activation.code}`
            : 'loading'}
        </div>
      )
    }

    const render = async (activationId: string) => {
      await act(async () => {
        root.render(
          <QueryClientProvider client={queryClient}>
            <DetailProbe activationId={activationId} />
          </QueryClientProvider>
        )
      })
    }

    try {
      await render('A')
      await waitForText(container, 'A|alpha@example.test|111111')

      await render('B')
      assert.equal(secondRequested, true)
      assert.equal(container.textContent, 'loading')
      assert.doesNotMatch(container.textContent ?? '', /alpha|111111/)

      secondResponse.resolve(
        detailResponse({
          id: 'B',
          email: 'beta@example.test',
          code: '222222',
        })
      )
      await waitForText(container, 'B|beta@example.test|222222')
      assert.doesNotMatch(container.textContent ?? '', /alpha|111111/)
    } finally {
      await act(async () => root.unmount())
      queryClient.clear()
    }
  })
})
