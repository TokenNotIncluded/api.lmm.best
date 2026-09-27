/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'

import { readPageOutline } from './page-outline'

test('page outlines omit secrets, form values, hidden content and URL parameters', () => {
  const dom = new Window({
    url: 'https://lmm.test/keys?token=private-query#private-fragment',
  })
  const previous = ['window', 'document'].map(
    (key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)] as const
  )
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: dom,
  })
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: dom.document,
  })
  dom.document.body.innerHTML = `
    <main>
      <h1>API keys</h1>
      <input value="private-password" />
      <h2 hidden>private-hidden</h2>
      <div style="display:none"><button>private-hidden-button</button></div>
      <button><code>private-key-in-code</code>Copy</button>
      <button data-sensitive>private-redemption-code</button>
      <button aria-label="Copy key">sk-abcdefghijk987654321</button>
      <a href="/wallet?token=private-link#private-fragment">Top up</a>
      <a href="https://outside.test/?key=private-external">External page</a>
      <a href="/keys/private-path-secret">Details</a>
      <button disabled>Unavailable</button>
    </main>`
  try {
    const result = readPageOutline({ '/keys': 'Keys', '/wallet': 'Wallet' })
    const serialized = JSON.stringify(result)
    assert.doesNotMatch(serialized, /private-|sk-abcdefgh/)
    assert.deepEqual(result.headings, [{ level: 1, text: 'API keys' }])
    assert.equal(
      result.actions.find((action) => action.text === 'Top up')?.href,
      '/wallet'
    )
    assert.equal(
      result.actions.find((action) => action.text === 'External page')?.href,
      null
    )
    assert.equal(
      result.actions.find((action) => action.text === 'Unavailable')?.disabled,
      true
    )
    assert.equal(
      result.actions.find((action) => action.text === 'Copy')?.kind,
      'button'
    )
  } finally {
    for (const [key, descriptor] of previous) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor)
      else Reflect.deleteProperty(globalThis, key)
    }
    dom.happyDOM.abort()
  }
})
