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
import { join } from 'node:path'
import { describe, test } from 'node:test'

import { Window } from 'happy-dom'

import { applyFaviconToDom } from './dom-utils'

function setupDom(iconHrefs: string[]) {
  const domWindow = new Window()
  const links = iconHrefs
    .map((href) => `<link rel="icon" href="${href}" />`)
    .join('')
  domWindow.document.write(
    `<!doctype html><html><head>${links}</head><body></body></html>`
  )
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
  return domWindow
}

describe('applyFaviconToDom', () => {
  test('keeps exactly one icon link when starting from the LMM Forge entry mark', () => {
    const domWindow = setupDom(['/lmm-forge-mark.svg'])

    applyFaviconToDom('https://cdn.example.com/logo.png')

    const icons = [
      ...domWindow.document.querySelectorAll('link[rel~="icon"]'),
    ] as unknown as HTMLLinkElement[]
    assert.equal(icons.length, 1)
    assert.equal(icons[0].href, 'https://cdn.example.com/logo.png')
  })

  test('re-applying the same URL does not duplicate the icon link', () => {
    const domWindow = setupDom(['/lmm-forge-mark.svg'])

    applyFaviconToDom('https://cdn.example.com/logo.png')
    applyFaviconToDom('https://cdn.example.com/logo.png')

    assert.equal(
      [...domWindow.document.querySelectorAll('link[rel~="icon"]')].length,
      1
    )
  })

  test('keeps the entry mark untouched when it is already the active icon', () => {
    const domWindow = setupDom(['/lmm-forge-mark.svg'])

    applyFaviconToDom('/lmm-forge-mark.svg')

    const icons = [
      ...domWindow.document.querySelectorAll('link[rel~="icon"]'),
    ] as unknown as HTMLLinkElement[]
    assert.equal(icons.length, 1)
    assert.match(icons[0].href, /\/lmm-forge-mark\.svg$/)
  })

  test('converges duplicate icon links to a single link', () => {
    const domWindow = setupDom(['/lmm-forge-mark.svg', '/logo.png'])

    applyFaviconToDom('https://cdn.example.com/logo.png')

    assert.equal(
      [...domWindow.document.querySelectorAll('link[rel~="icon"]')].length,
      1
    )
  })
})

describe('entry favicon declaration', () => {
  test('index.html declares the LMM Forge mark as the initial icon', () => {
    const html = readFileSync(
      join(import.meta.dirname, '../../index.html'),
      'utf8'
    )

    assert.match(
      html,
      /<link rel="icon" type="image\/svg\+xml" href="\/lmm-forge-mark\.svg" \/>/
    )
  })
})
