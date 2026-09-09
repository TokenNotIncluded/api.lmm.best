/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { act, api, flushEffects } from './assistant-key-tool-test-support'

const { createRoot } = await import('react-dom/client')
const { toast } = await import('sonner')
const { AssistantAdminChangeTool } =
  await import('./assistant-admin-change-tool')

test('locked confirmation is consumed with a warning and no applied claim', async () => {
  const originalPost = api.post
  const originalWarning = toast.warning
  const originalSuccess = toast.success
  const warnings: string[] = []
  let applied = 0
  let successes = 0
  let requests = 0
  toast.warning = ((message: string) => {
    warnings.push(message)
    return 'warning'
  }) as typeof toast.warning
  toast.success = (() => {
    successes += 1
    return 'success'
  }) as typeof toast.success
  api.post = (async () => {
    requests += 1
    return {
      data: {
        success: true,
        data: {
          applied: false,
          kind: 'pricing',
          status: 'ignored_locked',
          warnings: ['The model price is locked and unchanged.'],
        },
      },
    }
  }) as typeof api.post
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () => {
      root.render(
        <AssistantAdminChangeTool
          action={{
            type: 'admin_pricing_change',
            confirmation_token: 'one-time-token',
            requires_confirmation: true,
            expires_in_seconds: 600,
            pricing: {
              model_id: 'model',
              old: { value: 2 },
              next: { value: 9 },
            },
          }}
          onApplied={() => {
            applied += 1
          }}
        />
      )
    })
    await act(async () => {
      container.querySelector<HTMLButtonElement>('button')?.click()
      await flushEffects()
    })
    assert.equal(requests, 1)
    assert.equal(applied, 0)
    assert.equal(successes, 0)
    assert.deepEqual(warnings, ['The model price is locked and unchanged.'])
    assert.match(container.textContent ?? '', /Locked price changes ignored/)
    assert.doesNotMatch(
      container.textContent ?? '',
      /Administrator change applied/
    )
    assert.equal(container.querySelector('button'), null)
  } finally {
    await act(async () => root.unmount())
    container.remove()
    api.post = originalPost
    toast.warning = originalWarning
    toast.success = originalSuccess
  }
})
