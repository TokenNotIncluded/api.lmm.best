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

import type { ApiRequestConfig } from '@/lib/api'

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

test('confirmation is immediately available before scrolling and failure can be retried', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let confirmations = 0
  const geometry = ['clientHeight', 'scrollHeight'] as const
  const originalGeometry = geometry.map((key) =>
    Object.getOwnPropertyDescriptor(HTMLElement.prototype, key)
  )
  geometry.forEach((key) => {
    Object.defineProperty(HTMLElement.prototype, key, {
      configurable: true,
      get() {
        return this.getAttribute('role') === 'region'
          ? key === 'clientHeight'
            ? 200
            : 1000
          : 0
      },
    })
  })
  try {
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
  } finally {
    geometry.forEach((key, index) => {
      const original = originalGeometry[index]
      if (original) Object.defineProperty(HTMLElement.prototype, key, original)
      else Reflect.deleteProperty(HTMLElement.prototype, key)
    })
  }
  const viewport = container.querySelector('[role="region"]') as HTMLDivElement
  const button = container.querySelector('button')
  assert.ok(button)
  assert.equal(button.disabled, false)
  assert.equal(confirmations, 0)
  assert.equal(viewport.scrollTop, 0)
  assert.ok(container.textContent?.includes('0%'))
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
  get: (url: string, config?: ApiRequestConfig) => Promise<unknown>,
  run: (
    container: HTMLDivElement,
    client: InstanceType<typeof QueryClient>,
    rerender: () => Promise<void>
  ) => Promise<void>,
  signedIn = true,
  post?: (
    url: string,
    body: { id: number; revision: string },
    config?: ApiRequestConfig
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

async function confirmAnnouncement(container: HTMLDivElement) {
  const viewport = container.querySelector('[role="region"]') as HTMLDivElement
  assert.ok(viewport)
  const button = container.querySelector('footer button') as HTMLButtonElement
  assert.ok(button)
  assert.equal(button.disabled, false)
  await act(async () => button.click())
  await flushQuery()
}

function notice(content: string, revision = 'r1', read_at = 0) {
  return { id: 1, content, publishDate: '2026-09-20', revision, read_at }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

test('announcement GET and POST bind the account scope and GET carries its query abort signal', async () => {
  let getURL: string | undefined
  let postURL: string | undefined
  let getConfig: ApiRequestConfig | undefined
  let postConfig: ApiRequestConfig | undefined
  let posted: { id: number; revision: string } | undefined
  await gateFixture(
    async (url, config) => {
      getURL = url
      getConfig = config
      return { data: { success: true, data: [notice('Scoped notice')] } }
    },
    async (container) => {
      assert.equal(getURL, '/api/user/self/announcements')
      assert.deepEqual(getConfig?.authScope, {
        userId: 7,
        sessionId: undefined,
      })
      assert.ok(getConfig?.signal instanceof AbortSignal)
      assert.equal(getConfig.signal.aborted, false)
      await confirmAnnouncement(container)
      assert.equal(postURL, '/api/user/self/announcements/read')
      assert.deepEqual(posted, { id: 1, revision: 'r1' })
      assert.deepEqual(postConfig?.authScope, {
        userId: 7,
        sessionId: undefined,
      })
      assert.ok(container.querySelector('[data-testid="workspace"]'))
    },
    true,
    async (url, body, config) => {
      postURL = url
      posted = body
      postConfig = config
      return {
        data: { success: true, data: [notice('Scoped notice', 'r1', 42)] },
      }
    }
  )
})

test('two clicks in the same act send one acknowledgement and disable the pending button', async () => {
  const pending = deferred<unknown>()
  const acknowledged: { id: number; revision: string }[] = []
  await gateFixture(
    async () => ({ data: { success: true, data: [notice('Confirm once')] } }),
    async (container) => {
      const button = container.querySelector(
        'footer button'
      ) as HTMLButtonElement
      assert.ok(button)
      assert.equal(button.disabled, false)
      await act(async () => {
        button.click()
        button.click()
      })
      assert.deepEqual(acknowledged, [{ id: 1, revision: 'r1' }])
      assert.equal(button.disabled, true)
      assert.equal(button.textContent, 'Saving')
      assert.equal(container.querySelector('[data-testid="workspace"]'), null)
      await act(async () =>
        pending.resolve({
          data: { success: true, data: [notice('Confirm once', 'r1', 42)] },
        })
      )
      await flushQuery()
      assert.ok(container.querySelector('[data-testid="workspace"]'))
    },
    true,
    async (url, body) => {
      assert.equal(url, '/api/user/self/announcements/read')
      acknowledged.push(body)
      return pending.promise
    }
  )
})

for (const nextGet of ['fails', 'is stale'] as const) {
  test(`successful acknowledgement uses its authoritative list without a second GET that ${nextGet}`, async () => {
    let gets = 0
    const unread = [notice('Authoritative notice')]
    const acknowledged = [notice('Authoritative notice', 'r1', 42)]
    await gateFixture(
      async () => {
        if (++gets > 1 && nextGet === 'fails') {
          throw new Error('GET unavailable after ACK')
        }
        return { data: { success: true, data: unread } }
      },
      async (container, client) => {
        await confirmAnnouncement(container)
        assert.equal(gets, 1)
        assert.ok(container.querySelector('[data-testid="workspace"]'))
        assert.equal(container.querySelector('[role="alert"]'), null)
        assert.deepEqual(client.getQueryData(['mandatory-announcements', 7]), {
          supported: true,
          items: acknowledged,
        })
      },
      true,
      async () => ({ data: { success: true, data: acknowledged } })
    )
  })
}

test('an acknowledgement replaces the complete list including a new server-side unread notice', async () => {
  let gets = 0
  const updated = [
    notice('Confirmed notice', 'r1', 42),
    { ...notice('New server notice', 'r2'), id: 2 },
  ]
  await gateFixture(
    async () => {
      if (++gets > 1) throw new Error('Unexpected follow-up GET')
      return { data: { success: true, data: [notice('Confirmed notice')] } }
    },
    async (container, client) => {
      await confirmAnnouncement(container)
      assert.equal(gets, 1)
      assert.deepEqual(client.getQueryData(['mandatory-announcements', 7]), {
        supported: true,
        items: updated,
      })
      assert.ok(container.textContent?.includes('Announcement 2 of 2'))
      assert.equal(container.querySelector('[data-testid="workspace"]'), null)
    },
    true,
    async () => ({ data: { success: true, data: updated } })
  )
})

test('an in-flight background GET cannot overwrite a successful acknowledgement with stale unread data', async () => {
  const background = deferred<unknown>()
  const unread = [notice('Background refresh notice')]
  const acknowledged = [notice('Background refresh notice', 'r1', 42)]
  let gets = 0
  await gateFixture(
    async () =>
      ++gets === 1
        ? { data: { success: true, data: unread } }
        : background.promise,
    async (container, client) => {
      let refreshing!: Promise<void>
      await act(async () => {
        refreshing = client.refetchQueries({
          queryKey: ['mandatory-announcements', 7],
          exact: true,
        })
      })
      assert.equal(gets, 2)
      await confirmAnnouncement(container)
      assert.ok(container.querySelector('[data-testid="workspace"]'))
      await act(async () => {
        background.resolve({ data: { success: true, data: unread } })
        await refreshing
      })
      await flushQuery()
      assert.equal(gets, 2)
      assert.ok(container.querySelector('[data-testid="workspace"]'))
      assert.deepEqual(client.getQueryData(['mandatory-announcements', 7]), {
        supported: true,
        items: acknowledged,
      })
    },
    true,
    async () => ({ data: { success: true, data: acknowledged } })
  )
})

test('a failed acknowledgement keeps the reader and workspace blocked until a successful retry', async () => {
  let gets = 0
  let posts = 0
  const unread = [notice('Retry this notice')]
  await gateFixture(
    async () => ({ data: { success: true, data: ++gets === 1 ? unread : [] } }),
    async (container, client) => {
      await confirmAnnouncement(container)
      assert.equal(posts, 1)
      assert.equal(gets, 1)
      assert.equal(container.querySelector('[data-testid="workspace"]'), null)
      assert.ok(container.querySelector('[role="alert"]'))
      assert.ok(container.querySelector('[role="region"]'))
      assert.deepEqual(client.getQueryData(['mandatory-announcements', 7]), {
        supported: true,
        items: unread,
      })
      await confirmAnnouncement(container)
      assert.equal(posts, 2)
      assert.equal(gets, 1)
      assert.ok(container.querySelector('[data-testid="workspace"]'))
    },
    true,
    async () => {
      if (++posts === 1) throw new Error('ACK offline')
      return {
        data: { success: true, data: [notice('Retry this notice', 'r1', 42)] },
      }
    }
  )
})

for (const [description, response] of [
  ['success-only', { success: true }],
  ['non-array', { success: true, data: null }],
  [
    'business failure',
    { success: false, data: [notice('Unread notice', 'r1', 42)] },
  ],
] as const) {
  test(`a ${description} acknowledgement response keeps the unread notice blocked`, async () => {
    let gets = 0
    await gateFixture(
      async () => {
        gets++
        return { data: { success: true, data: [notice('Unread notice')] } }
      },
      async (container) => {
        await confirmAnnouncement(container)
        assert.equal(gets, 1)
        assert.equal(container.querySelector('[data-testid="workspace"]'), null)
        assert.ok(container.querySelector('[role="alert"]'))
        assert.equal(
          container.querySelector('footer button')?.hasAttribute('disabled'),
          false
        )
      },
      true,
      async () => ({ data: response })
    )
  })
}

test('a revision conflict refreshes the new notice and acknowledges only its new revision', async () => {
  let gets = 0
  let rotated = false
  const acknowledged: { id: number; revision: string }[] = []
  await gateFixture(
    async () => {
      gets++
      return {
        data: {
          success: true,
          data: [
            rotated
              ? notice('Updated notice', 'r2')
              : notice('Original notice'),
          ],
        },
      }
    },
    async (container, client) => {
      await confirmAnnouncement(container)
      assert.equal(gets, 2)
      assert.deepEqual(acknowledged, [{ id: 1, revision: 'r1' }])
      assert.ok(container.querySelector('[role="region"]'))
      assert.equal(container.querySelector('[data-testid="workspace"]'), null)
      assert.deepEqual(client.getQueryData(['mandatory-announcements', 7]), {
        supported: true,
        items: [notice('Updated notice', 'r2')],
      })
      await confirmAnnouncement(container)
      assert.deepEqual(acknowledged, [
        { id: 1, revision: 'r1' },
        { id: 1, revision: 'r2' },
      ])
      assert.equal(gets, 2)
      assert.ok(container.querySelector('[data-testid="workspace"]'))
    },
    true,
    async (_url, body) => {
      acknowledged.push(body)
      if (!rotated) {
        rotated = true
        throw { isAxiosError: true, response: { status: 409 } }
      }
      return {
        data: { success: true, data: [notice('Updated notice', 'r2', 42)] },
      }
    }
  )
})

test('an in-flight acknowledgement from one user cannot acknowledge the next user notices', async () => {
  const pending = deferred<unknown>()
  const gets: number[] = []
  const posts: number[] = []
  await gateFixture(
    async () => {
      const user = useAuthStore.getState().auth.user
      assert.ok(user)
      const userID = user.id
      gets.push(userID)
      return {
        data: { success: true, data: [notice(`User ${userID} notice`)] },
      }
    },
    async (container, client) => {
      const firstButton = container.querySelector(
        'footer button'
      ) as HTMLButtonElement
      await act(async () => firstButton.click())
      assert.equal(firstButton.disabled, true)
      await act(async () =>
        useAuthStore
          .getState()
          .auth.setUser({ id: 8, username: 'next-user', role: 1 })
      )
      await flushQuery()
      assert.ok(container.querySelector('[role="region"]'))
      assert.equal(
        container.querySelector('footer button')?.hasAttribute('disabled'),
        false
      )
      assert.equal(container.querySelector('[data-testid="workspace"]'), null)
      await act(async () =>
        client.removeQueries({
          queryKey: ['mandatory-announcements', 7],
          exact: true,
        })
      )
      await act(async () =>
        pending.resolve({
          data: { success: true, data: [notice('User 7 notice', 'r1', 42)] },
        })
      )
      await flushQuery()
      assert.ok(container.querySelector('[role="region"]'))
      assert.equal(container.querySelector('[data-testid="workspace"]'), null)
      assert.equal(
        client.getQueryData(['mandatory-announcements', 7]),
        undefined
      )
      assert.deepEqual(client.getQueryData(['mandatory-announcements', 8]), {
        supported: true,
        items: [notice('User 8 notice')],
      })
      await confirmAnnouncement(container)
      assert.deepEqual(gets, [7, 8])
      assert.deepEqual(posts, [7, 8])
      assert.ok(container.querySelector('[data-testid="workspace"]'))
    },
    true,
    async (_url, body) => {
      const user = useAuthStore.getState().auth.user
      assert.ok(user)
      const userID = user.id
      posts.push(userID)
      assert.equal(body.revision, 'r1')
      return userID === 7
        ? pending.promise
        : { data: { success: true, data: [notice('User 8 notice', 'r1', 42)] } }
    }
  )
})

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
      await confirmAnnouncement(container)
      assert.deepEqual(acknowledged, [1])
      assert.ok(container.textContent?.includes('Announcement 2 of 2'))
      await confirmAnnouncement(container)
      assert.deepEqual(acknowledged, [1, 2])
      assert.ok(container.querySelector('[data-testid="workspace"]'))
    },
    true,
    async (_url, body) => {
      acknowledged.push(body.id)
      read.add(body.id)
      return { data: { success: true, data: items() } }
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
