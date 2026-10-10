/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const window = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: key === 'window' ? window : window[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { ThemeProvider, useTheme } = await import('./theme-provider')
const { ThemeCustomizationProvider, useThemeCustomization } =
  await import('./theme-customization-provider')
const { getCookie } = await import('@/lib/cookies')
after(() => window.happyDOM.abort())

async function render() {
  let theme: ReturnType<typeof useTheme> | undefined
  let style: ReturnType<typeof useThemeCustomization> | undefined
  function Harness() {
    theme = useTheme()
    style = useThemeCustomization()
    return <span>{theme.resolvedTheme}</span>
  }
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <ThemeProvider defaultTheme='light'>
        <ThemeCustomizationProvider><Harness /></ThemeCustomizationProvider>
      </ThemeProvider>
    )
  })
  return {
    current: () => {
      assert.ok(theme)
      assert.ok(style)
      return { theme, style }
    },
    close: async () => {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

test('appearance preview changes real provider output without saving cookies', async () => {
  // Happy DOM may briefly expose the empty preset cookie marked for deletion.
  // Compare persistent values while retaining all other cookie assertions.
  const persistentCookies = () =>
    document.cookie
      .split('; ')
      .filter((cookie) => cookie !== 'theme_preset=')
      .join('; ')
  const mounted = await render()
  try {
    await act(async () => {
      mounted.current().theme.setTheme('light')
      mounted.current().style.setPreset('default')
    })
    const cookies = persistentCookies()
    let stopMode!: () => void
    let stopPreset!: () => void
    await act(async () => {
      stopMode = mounted.current().theme.previewTheme('dark')
      stopPreset = mounted.current().style.previewPreset('rose-garden')
    })
    assert.equal(document.documentElement.classList.contains('dark'), true)
    assert.equal(document.body.getAttribute('data-theme-preset'), 'rose-garden')
    assert.equal(mounted.current().theme.resolvedTheme, 'dark')
    assert.equal(mounted.current().theme.theme, 'light')
    assert.equal(mounted.current().style.customization.preset, 'default')
    assert.equal(persistentCookies(), cookies)
    await act(async () => { stopMode(); stopPreset() })
    assert.equal(document.documentElement.classList.contains('light'), true)
    assert.equal(document.body.getAttribute('data-theme-preset'), null)
    assert.equal(persistentCookies(), cookies)
  } finally {
    await mounted.close()
  }
})

test('manual choices, including the same saved value, win over old preview cleanup', async () => {
  const mounted = await render()
  try {
    await act(async () => {
      mounted.current().theme.setTheme('light')
      mounted.current().style.setPreset('default')
    })
    let stopMode!: () => void
    let stopPreset!: () => void
    await act(async () => {
      stopMode = mounted.current().theme.previewTheme('dark')
      stopPreset = mounted.current().style.previewPreset('rose-garden')
    })
    await act(async () => {
      mounted.current().theme.setTheme('light')
      mounted.current().style.setPreset('ocean-breeze')
    })
    await act(async () => { stopMode(); stopPreset() })
    assert.equal(mounted.current().theme.resolvedTheme, 'light')
    assert.equal(document.body.getAttribute('data-theme-preset'), 'ocean-breeze')
    assert.equal(getCookie('theme_preset'), 'ocean-breeze')
  } finally {
    await mounted.close()
  }
})

test('a newly selected Anthropic preset survives remount and the legacy migration', async () => {
  const mounted = await render()
  await act(async () => mounted.current().style.setPreset('anthropic'))
  assert.equal(getCookie('theme_preset_default_migrated_v1'), '1')
  await mounted.close()
  const reloaded = await render()
  try {
    assert.equal(reloaded.current().style.customization.preset, 'anthropic')
    assert.equal(document.body.getAttribute('data-theme-preset'), 'anthropic')
  } finally {
    await reloaded.close()
  }
})
