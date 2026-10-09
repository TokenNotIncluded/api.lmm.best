/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

const view = new Window({ url: 'https://fixture.invalid/guide' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: view[key],
  })
}
mock.module('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
mock.module('@/components/ui/markdown', () => ({
  Markdown: ({
    children,
    className,
  }: {
    children: ReactNode
    className?: string
  }) => <div className={className}>{children}</div>,
}))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { DocumentReader } = await import('./document-reader')
const host = document.createElement('div')
document.body.append(host)
let root = createRoot(host)
const content =
  '# First\nFirst body.\n\n## Second\nSecond body.\n\n## Third\nThird body.\n\n## Last\nLast body.'
const sections = () => [
  ...host.querySelectorAll<HTMLDetailsElement>('[data-reading-section]'),
]
afterEach(async () => {
  await act(async () => root.unmount())
  host.replaceChildren()
  root = createRoot(host)
  window.location.hash = ''
})
after(async () => {
  await act(async () => root.unmount())
  view.close()
})

test('short documents remain full text without an unnecessary directory', async () => {
  await act(async () =>
    root.render(<DocumentReader content='# Brief\nAll content.' />)
  )
  assert.equal(sections().length, 0)
  assert.ok(host.textContent?.includes('All content.'))
})
test('folds preserve every section and the controls expand or collapse all', async () => {
  await act(async () => root.render(<DocumentReader content={content} />))
  assert.equal(sections().length, 4)
  assert.equal(sections().filter((section) => section.open).length, 2)
  for (const text of [
    'First body.',
    'Second body.',
    'Third body.',
    'Last body.',
  ]) {
    assert.ok(host.textContent?.includes(text))
  }
  const click = async (text: string) => {
    const button = [...host.querySelectorAll('button')].find(
      (node) => node.textContent === text
    )
    assert.ok(button)
    await act(async () => button.click())
  }
  await click('Expand all')
  assert.ok(sections().every((section) => section.open))
  await click('Collapse all')
  assert.ok(sections().every((section) => !section.open))
})
test('printing expands the complete document then restores the reader choices', async () => {
  await act(async () => root.render(<DocumentReader content={content} />))
  sections()[0].open = false
  const before = sections().map((section) => section.open)
  window.dispatchEvent(new window.Event('beforeprint'))
  assert.ok(sections().every((section) => section.open))
  window.dispatchEvent(new window.Event('afterprint'))
  assert.deepEqual(
    sections().map((section) => section.open),
    before
  )
})
test('a direct section link opens the target without opening all other sections', async () => {
  await act(async () => root.render(<DocumentReader content={content} />))
  const target = sections()[3]
  let scrolled = false
  target.scrollIntoView = () => {
    scrolled = true
  }
  window.location.hash = `#${target.id}`
  window.dispatchEvent(new window.Event('hashchange'))
  assert.ok(target.open)
  assert.ok(scrolled)
  assert.equal(sections()[2].open, false)
})

test('document layout classes are applied once, not to each chapter body', async () => {
  await act(async () =>
    root.render(<DocumentReader content={content} className='reader-layout' />)
  )
  assert.equal(host.querySelectorAll('.reader-layout').length, 1)
  assert.ok(host.querySelector('.document-reader.reader-layout'))
  assert.equal(
    host.querySelector('.document-section-body .reader-layout'),
    null
  )
})
