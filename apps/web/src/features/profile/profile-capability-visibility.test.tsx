/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { ProfilePasskeyCapability } = await import('./index')

const originalGet = api.get
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

afterEach(() => {
  api.get = originalGet
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('profile passkey capability visibility', () => {
  test('mounts Passkey only after live status explicitly enables it', async () => {
    const gets: string[] = []
    api.get = (async (url) => {
      gets.push(url)
      return {
        data: {
          success: true,
          data: {
            enabled: false,
            backup_eligible: false,
            backup_state: false,
            last_used_at: null,
          },
        },
      }
    }) as typeof api.get

    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    const renderCapability = async (
      capabilitiesReady: boolean,
      passkeyLogin: boolean
    ) => {
      await act(async () => {
        root.render(
          <I18nextProvider i18n={i18n}>
            <ProfilePasskeyCapability
              capabilitiesReady={capabilitiesReady}
              passkeyLogin={passkeyLogin}
              loading={false}
            />
          </I18nextProvider>
        )
        await flushEffects()
      })
    }

    await renderCapability(false, true)
    assert.equal(container.textContent?.includes('Passkey Login'), false)
    assert.deepEqual(
      gets.filter((url) => url === '/api/user/passkey'),
      []
    )

    await renderCapability(true, false)
    assert.equal(container.textContent?.includes('Passkey Login'), false)
    assert.deepEqual(
      gets.filter((url) => url === '/api/user/passkey'),
      []
    )

    await renderCapability(true, true)
    assert.equal(container.textContent?.includes('Passkey Login'), true)
    assert.equal(
      gets.filter((url) => url === '/api/user/passkey').length > 0,
      true
    )

    await act(async () => root.unmount())
    container.remove()
  })
})
