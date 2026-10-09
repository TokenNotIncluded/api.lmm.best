/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import { describe, expect, it } from 'bun:test'
import { readFileSync } from 'node:fs'

import { renderToStaticMarkup } from 'react-dom/server'

import {
  assistantOpenUILibrary,
  assistantOpenUIPrompt,
  OpenUIDataTable,
} from './library'
// Synthetic fixture values, not production data.
const rows = [
  ['Model A', 12],
  ['Model B', 2],
]
describe('assistant OpenUI component contract', () => {
  it('has a small allowlist with no forms, HTML or server tools', () => {
    expect(Object.keys(assistantOpenUILibrary.components).sort()).toEqual([
      'BarChart',
      'ConsoleLink',
      'DataTable',
      'Metric',
      'Stack',
    ])
  })
  it('keeps the server prompt identical to the frontend component contract', () => {
    const file = new URL(
      '../../../../../api-go/controller/assistant_openui_prompt.txt',
      import.meta.url
    )
    expect(readFileSync(file, 'utf8')).toBe(assistantOpenUIPrompt())
  })
  it('keeps the account, billing and confirmation boundaries in the prompt', () => {
    const prompt = assistantOpenUIPrompt()
    expect(prompt).toContain('live tool results')
    expect(prompt).toContain('confirmation cards')
    expect(prompt).toContain('display-only')
    expect(prompt).toContain('ordinary questions in Markdown')
  })
  it('renders a semantic table with keyboard-accessible sort controls', () => {
    const html = renderToStaticMarkup(
      <OpenUIDataTable
        title='Requests'
        columns={['Model', 'Requests']}
        rows={rows}
      />
    )
    expect(html).toContain('<caption')
    expect(html).toContain('aria-sort="none"')
    expect(html).toContain('type="button"')
    expect(html).toContain('Model A')
    expect(rows).toEqual([
      ['Model A', 12],
      ['Model B', 2],
    ])
  })
  it('escapes data instead of treating model strings as HTML', () => {
    const html = renderToStaticMarkup(
      <OpenUIDataTable
        title='<script>bad()</script>'
        columns={['Name']}
        rows={[['<img src=x onerror=bad()>']]}
      />
    )
    expect(html).not.toContain('<script>')
    expect(html).not.toContain('<img ')
    expect(html).toContain('&lt;script&gt;')
  })
})
