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

const dom = new Window({ url: 'http://localhost/' })
dom.document.write('<!doctype html><html><body></body></html>')
Object.defineProperty(dom.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Element',
  'Node',
  'Event',
  'MutationObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: key === 'window' ? dom : dom[key],
  })
}
Object.defineProperty(globalThis, 'ResizeObserver', {
  configurable: true,
  value: class {
    observe() {}
    disconnect() {}
  },
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { initReactI18next } = await import('react-i18next')
await createInstance()
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const { AnnouncementReader, MandatoryAnnouncements } =
  await import('./mandatory-announcements')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
after(() => dom.close())

test('reading requires the bottom and failed confirmation can be retried', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let confirmations = 0
  await act(async () => {
    root.render(
      <AnnouncementReader
        item={{
          id: 1,
          content: 'Read this announcement',
          publishDate: '2025-01-01T00:00:00Z',
          revision: 'r1',
          read_at: 0,
        }}
        completed={0}
        total={2}
        onContinue={async () => {
          confirmations++
          if (confirmations === 1) throw new Error('offline')
        }}
      />
    )
  })
  const viewport = container.querySelector('[role="region"]') as HTMLDivElement
  Object.defineProperty(viewport, 'clientHeight', {
    configurable: true,
    value: 200,
  })
  Object.defineProperty(viewport, 'scrollHeight', {
    configurable: true,
    value: 1000,
  })
  const button = container.querySelector('button')
  assert.ok(button)
  await act(async () =>
    viewport.dispatchEvent(new Event('scroll', { bubbles: true }))
  )
  assert.equal(button.disabled, true)
  assert.equal(confirmations, 0)
  await act(async () => {
    viewport.scrollTop = 800
    viewport.dispatchEvent(new Event('scroll', { bubbles: true }))
  })
  assert.equal(button.disabled, false)
  await act(async () => button.click())
  assert.equal(confirmations, 1)
  assert.ok(container.querySelector('[role="alert"]'))
  assert.equal(button.disabled, false)
  await act(async () => button.click())
  assert.equal(confirmations, 2)
  await act(async () => root.unmount())
  container.remove()
})

async function flushQuery() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
}

async function gateFixture(
  get: () => Promise<unknown>,
  run: (
    container: HTMLDivElement,
    client: InstanceType<typeof QueryClient>,
    rerender: () => Promise<void>
  ) => Promise<void>,
  signedIn = true,
  post?: (
    url: string,
    body: { id: number; revision: string }
  ) => Promise<unknown>
) {
  const originalGet = api.get
  const originalPost = api.post
  api.get = get as typeof api.get
  api.post = (post ??
    (async () => {
      throw new Error('Unexpected announcement acknowledgement')
    })) as typeof api.post
  useAuthStore
    .getState()
    .auth.setUser(
      signedIn ? { id: 7, username: 'announcement-fixture', role: 1 } : null
    )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  let root = createRoot(container)
  const render = async () => {
    await act(async () => {
      root.render(
        <QueryClientProvider client={client}>
          <MandatoryAnnouncements>
            <p data-testid='workspace'>Workspace</p>
          </MandatoryAnnouncements>
        </QueryClientProvider>
      )
    })
    await flushQuery()
  }
  try {
    await render()
    await run(container, client, async () => {
      await act(async () => root.unmount())
      root = createRoot(container)
      await render()
    })
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
    api.get = originalGet
    api.post = originalPost
    useAuthStore.getState().auth.setUser(null)
  }
}

// The reader only unlocks its button once the viewport is scrolled to the end,
// so a gate test has to drive a real scroll on whichever reader is mounted.
async function readToBottomAndConfirm(container: HTMLDivElement) {
  const viewport = container.querySelector('[role="region"]') as HTMLDivElement
  assert.ok(viewport)
  Object.defineProperty(viewport, 'clientHeight', {
    configurable: true,
    value: 200,
  })
  Object.defineProperty(viewport, 'scrollHeight', {
    configurable: true,
    value: 1000,
  })
  await act(async () => {
    viewport.scrollTop = 800
    viewport.dispatchEvent(new Event('scroll', { bubbles: true }))
  })
  const button = container.querySelector('footer button') as HTMLButtonElement
  assert.ok(button)
  assert.equal(button.disabled, false)
  await act(async () => button.click())
  await flushQuery()
}

test('two required announcements advance in order and then open the workspace', async () => {
  const read = new Set<number>()
  const items = () => [
    {
      id: 1,
      content: 'First required notice',
      publishDate: '2026-09-18',
      revision: 'r1',
      read_at: read.has(1) ? 1 : 0,
    },
    {
      id: 2,
      content: 'Second required notice',
      publishDate: '2026-09-19',
      revision: 'r2',
      read_at: read.has(2) ? 1 : 0,
    },
  ]
  const acknowledged: number[] = []
  await gateFixture(
    async () => ({ data: { success: true, data: items() } }),
    async (container) => {
      assert.equal(container.querySelector('[data-testid="workspace"]'), null)
      assert.ok(container.textContent?.includes('Announcement 1 of 2'))
      await readToBottomAndConfirm(container)
      assert.deepEqual(acknowledged, [1])
      assert.ok(container.textContent?.includes('Announcement 2 of 2'))
      await readToBottomAndConfirm(container)
      assert.deepEqual(acknowledged, [1, 2])
      assert.ok(container.querySelector('[data-testid="workspace"]'))
    },
    true,
    async (_url, body) => {
      acknowledged.push(body.id)
      read.add(body.id)
      return { data: { success: true } }
    }
  )
})

test('an announcement published before an acknowledged one is numbered by its own position', async () => {
  await gateFixture(
    async () => ({
      data: {
        success: true,
        data: [
          {
            id: 9,
            content: 'Backdated required notice',
            publishDate: '2026-09-10',
            revision: 'r9',
            read_at: 0,
          },
          {
            id: 4,
            content: 'Already acknowledged notice',
            publishDate: '2026-09-15',
            revision: 'r4',
            read_at: 1,
          },
        ],
      },
    }),
    async (container) => {
      // Counting acknowledgements would say "2 of 2" for the first notice.
      assert.ok(container.textContent?.includes('Announcement 1 of 2'))
      assert.equal(
        container.textContent?.includes('Announcement 2 of 2'),
        false
      )
    }
  )
})

test('a legacy 404 opens the workspace and does not retry on remount', async () => {
  let requests = 0
  await gateFixture(
    async () => {
      requests++
      throw { isAxiosError: true, response: { status: 404 } }
    },
    async (container, _client, remount) => {
      assert.ok(container.querySelector('[data-testid="workspace"]'))
      assert.equal(container.textContent?.includes('Retry'), false)
      assert.equal(requests, 1)
      await remount()
      assert.ok(container.querySelector('[data-testid="workspace"]'))
      assert.equal(requests, 1)
    }
  )
})

test('a newly supported backend still requires an unread announcement', async () => {
  let supported = false
  await gateFixture(
    async () => {
      if (!supported) throw { isAxiosError: true, response: { status: 404 } }
      return {
        data: {
          success: true,
          data: [
            {
              id: 1,
              content: 'A real required notice',
              publishDate: '2026-09-20',
              revision: 'r1',
              read_at: 0,
            },
          ],
        },
      }
    },
    async (container, client) => {
      assert.ok(container.querySelector('[data-testid="workspace"]'))
      supported = true
      await act(async () => {
        await client.invalidateQueries({
          queryKey: ['mandatory-announcements', 7],
        })
      })
      await flushQuery()
      assert.equal(container.querySelector('[data-testid="workspace"]'), null)
      assert.ok(container.textContent?.includes('Required announcement'))
    }
  )
})

for (const status of [401, 403, 500, 503]) {
  test(`HTTP ${status} remains a recoverable error, not an announcement bypass`, async () => {
    await gateFixture(
      async () => {
        throw { isAxiosError: true, response: { status } }
      },
      async (container) => {
        assert.equal(container.querySelector('[data-testid="workspace"]'), null)
        assert.ok(
          container.textContent?.includes('Unable to load announcements')
        )
        assert.equal(container.querySelector('button')?.textContent, 'Retry')
      }
    )
  })
}

test('malformed success payload does not bypass required announcements', async () => {
  await gateFixture(
    async () => ({ data: { success: true, data: null } }),
    async (container) => {
      assert.equal(container.querySelector('[data-testid="workspace"]'), null)
      assert.ok(container.textContent?.includes('Unable to load announcements'))
    }
  )
})

test('signed-out state does not wait for a disabled announcement query', async () => {
  let requests = 0
  await gateFixture(
    async () => {
      requests++
      throw new Error('Must not fetch while signed out')
    },
    async (container) => {
      assert.ok(container.querySelector('[data-testid="workspace"]'))
      assert.equal(requests, 0)
    },
    false
  )
})

test('a network error offers a real exit and one successful retry restores the workspace', async () => {
  let requests = 0
  await gateFixture(
    async () => {
      if (++requests === 1) throw new Error('offline')
      return { data: { success: true, data: [] } }
    },
    async (container) => {
      assert.equal(
        container.querySelector('a[href="/"]')?.textContent,
        'Back to home'
      )
      const copyButton = container.querySelector('button')
      assert.ok(copyButton)
      await act(async () => copyButton.click())
      await flushQuery()
      assert.equal(requests, 2)
      assert.ok(container.querySelector('[data-testid="workspace"]'))
    }
  )
})
