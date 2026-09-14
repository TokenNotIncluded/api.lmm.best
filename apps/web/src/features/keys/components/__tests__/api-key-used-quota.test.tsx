/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
for (const key of ['window', 'document', 'HTMLElement', 'Node'] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { ApiKeyUsedQuota } = await import('../api-keys-cells')

after(() => domWindow.close())

test('renders API key usage directly, including zero', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)

  await act(async () => root.render(<ApiKeyUsedQuota used={0} />))
  const usage = container.querySelector('[data-api-key-used-quota]')
  assert.ok(usage)
  assert.notEqual(usage.textContent, '-')

  await act(async () => root.render(<ApiKeyUsedQuota used={500_000} />))
  assert.ok(container.textContent?.trim())
  assert.notEqual(container.textContent?.trim(), '-')

  await act(async () => root.unmount())
})
