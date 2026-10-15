/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { AxiosError, type AxiosAdapter, type AxiosResponse } from 'axios'
import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/settings' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { api } = await import('@/lib/api')
const { useTwoFA } = await import('./use-two-fa')
let current: ReturnType<typeof useTwoFA> | undefined
function requireHook() {
  assert.ok(current)
  return current
}
function Probe() {
  current = useTwoFA()
  return null
}
const originalAdapter = api.defaults.adapter
const roots: ReturnType<typeof createRoot>[] = []
function response(
  config: Parameters<AxiosAdapter>[0],
  data: unknown
): AxiosResponse {
  return { config, data, status: 200, statusText: 'OK', headers: {} }
}
afterEach(async () => {
  for (const root of roots.splice(0)) await act(async () => root.unmount())
  api.defaults.adapter = originalAdapter
  current = undefined
})
after(() => dom.close())
async function mount() {
  const root = createRoot(document.createElement('div'))
  roots.push(root)
  await act(async () => root.render(<Probe />))
  assert.ok(current)
  return current
}
test('failed or malformed status stays unknown and only an explicit successful retry establishes state', async () => {
  api.defaults.adapter = async (config) => {
    throw new AxiosError('unavailable', 'ERR_BAD_RESPONSE', config, undefined, {
      ...response(config, { success: false, code: 'MFA_UNAVAILABLE' }),
      status: 503,
    })
  }
  await mount()
  assert.equal(current?.status, null)
  assert.equal(current?.error, true)
  assert.equal(current?.loading, false)
  api.defaults.adapter = async (config) =>
    response(config, {
      success: true,
      data: { enabled: true, locked: false, backup_codes_remaining: 4 },
    })
  await act(async () => {
    await current?.refetch()
  })
  assert.equal(requireHook().status?.enabled, true)
  assert.equal(current?.error, false)
  api.defaults.adapter = async (config) =>
    response(config, {
      success: true,
      data: { enabled: 'false', locked: false, backup_codes_remaining: 4 },
    })
  await act(async () => {
    await current?.refetch()
  })
  assert.equal(current?.status, null)
  assert.equal(current?.error, true)
})
test('an older read cannot overwrite a later successful status read', async () => {
  api.defaults.adapter = async (config) =>
    response(config, {
      success: true,
      data: { enabled: true, locked: false, backup_codes_remaining: 4 },
    })
  await mount()
  let release!: () => void
  api.defaults.adapter = (config) =>
    new Promise((resolve) => {
      release = () =>
        resolve(
          response(config, {
            success: true,
            data: { enabled: false, locked: false, backup_codes_remaining: 0 },
          })
        )
    })
  let older!: Promise<void>
  await act(async () => {
    older = requireHook().refetch()
    await Promise.resolve()
  })
  api.defaults.adapter = async (config) =>
    response(config, {
      success: true,
      data: { enabled: true, locked: false, backup_codes_remaining: 3 },
    })
  await act(async () => {
    await current?.refetch()
  })
  await act(async () => {
    release()
    await older
  })
  assert.equal(current?.status?.enabled, true)
  assert.equal(current?.status?.backup_codes_remaining, 3)
})
