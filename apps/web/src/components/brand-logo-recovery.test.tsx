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
import { readFileSync } from 'node:fs'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/' })
const previousGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLImageElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
] as const) {
  previousGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
previousGlobals.set(
  'IS_REACT_ACT_ENVIRONMENT',
  Object.getOwnPropertyDescriptor(globalThis, 'IS_REACT_ACT_ENVIRONMENT')
)
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
  writable: true,
})

const { act, createElement } = await import('react')
const { createRoot } = await import('react-dom/client')
const { BrandLogo } = await import('./brand-logo')

after(() => {
  domWindow.close()
  for (const [key, descriptor] of previousGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

async function withLogo(
  props: Parameters<typeof BrandLogo>[0],
  check: (
    container: HTMLDivElement,
    update: (next: Parameters<typeof BrandLogo>[0]) => Promise<void>
  ) => Promise<void> | void
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const update = async (next: Parameters<typeof BrandLogo>[0]) => {
    await act(async () => root.render(createElement(BrandLogo, next)))
  }
  try {
    await update(props)
    await check(container, update)
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
}

for (const src of [
  undefined,
  '',
  '   ',
  '/lmm-best-mark.svg',
  '/lmm-forge-mark.svg',
  '/logo.png',
  '/favicon.ico?v=1',
]) {
  test(`built-in logo does not create an image request: ${String(src)}`, async () => {
    await withLogo({ src }, (container) => {
      assert.ok(container.querySelector('svg'))
      assert.equal(container.querySelector('img'), null)
      assert.equal(container.querySelector('svg')?.getAttribute('aria-hidden'), 'true')
    })
  })
}

for (const src of [
  'javascript:alert(1)',
  'data:image/svg+xml,<svg/>',
  '//untrusted.example/logo.svg',
  'https://user:password@example.test/logo.svg',
]) {
  test(`unsafe logo source is not rendered: ${src}`, async () => {
    await withLogo({ src }, (container) => {
      assert.equal(container.querySelector('img'), null)
      assert.ok(container.querySelector('svg'))
    })
  })
}

test('custom logo retains dimensions and its accessible name after failure', async () => {
  await withLogo(
    { src: '/tenant.svg', alt: 'Tenant', width: 32, height: 32, className: 'brand-test' },
    async (container) => {
      const img = container.querySelector('img')
      assert.ok(img)
      assert.equal(img.getAttribute('src'), '/tenant.svg')
      await act(async () => {
        img.dispatchEvent(new Event('error'))
      })
      const mark = container.querySelector('svg')
      assert.ok(mark)
      assert.equal(container.querySelector('img'), null)
      assert.equal(mark.getAttribute('aria-label'), 'Tenant')
      assert.equal(mark.getAttribute('role'), 'img')
      assert.equal(mark.getAttribute('width'), '32')
      assert.equal(mark.getAttribute('height'), '32')
      assert.ok(mark.classList.contains('brand-test'))
    }
  )
})

test('failure is stable on rerender but resets for a new URL', async () => {
  await withLogo({ src: '/missing.svg' }, async (container, update) => {
    const oldImage = container.querySelector('img')
    assert.ok(oldImage)
    await act(async () => {
      oldImage.dispatchEvent(new Event('error'))
    })
    await update({ src: '/missing.svg', alt: 'Updated label' })
    assert.equal(container.querySelector('img'), null)
    assert.equal(container.querySelector('svg')?.getAttribute('aria-label'), 'Updated label')
    await update({ src: '/replacement.svg', alt: 'Updated label' })
    assert.equal(container.querySelector('img')?.getAttribute('src'), '/replacement.svg')
    await act(async () => {
      oldImage.dispatchEvent(new Event('error'))
    })
    assert.equal(container.querySelector('img')?.getAttribute('src'), '/replacement.svg')
    await update({ src: '/logo.png' })
    assert.equal(container.querySelector('img'), null)
    await update({ src: '/replacement.svg' })
    assert.equal(container.querySelector('img')?.getAttribute('src'), '/replacement.svg')
  })
})

test('external artwork is preserved even when named logo.png', async () => {
  await withLogo({ src: ' https://tenant.example/logo.png ', decoding: 'async' }, (container) => {
    const image = container.querySelector('img')
    assert.ok(image)
    assert.equal(image.getAttribute('src'), 'https://tenant.example/logo.png')
    assert.equal(image.getAttribute('alt'), '')
    assert.equal(image.getAttribute('decoding'), 'async')
  })
})

test('mark has visible color fallbacks outside the Forge theme', async () => {
  await withLogo({}, (container) => {
    assert.equal(container.querySelector('path')?.getAttribute('stroke'), 'var(--forge-brand-mark-ink, currentColor)')
  })
})

test('public shell uses the same configured BrandLogo as the console', () => {
  const shell = readFileSync(new URL('../features/forge/forge-public-shell.tsx', import.meta.url), 'utf8')
  assert.match(shell, /<BrandLogo\s+src=\{logo\}/)
  assert.doesNotMatch(shell, /<LmmBrandMark/)
})
