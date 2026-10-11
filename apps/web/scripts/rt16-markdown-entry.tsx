/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// A component-only browser entry. Do not add it to the application entry points.
// Real Markdown and DOMPurify are used; neither is replaced with a test double.
import DOMPurify from 'dompurify'
import { flushSync } from 'react-dom'
import { createRoot } from 'react-dom/client'

import { Markdown } from '../src/components/ui/markdown'

const mount = document.getElementById('rt16-mount')
if (!mount) throw new Error('Missing isolated RT16 mount')
const host: HTMLElement = mount
const root = createRoot(host)
const originalDocument = window.document
const originalLocation = window.location
const state = Object.freeze({ purpose: 'RT16 synthetic state only' })
const originalSanitize = DOMPurify.sanitize
let stages: { input: string; output: string }[] = []
let input = ''

function serialize(value: unknown): string {
  if (typeof value === 'string') return value
  if (value instanceof Node) {
    const container = document.createElement('div')
    container.appendChild(value.cloneNode(true))
    return container.innerHTML
  }
  return String(value)
}

DOMPurify.sanitize = function (...args: Parameters<typeof originalSanitize>) {
  const before = serialize(args[0])
  const result = Reflect.apply(originalSanitize, DOMPurify, args)
  stages.push({ input: before, output: serialize(result) })
  return result
} as typeof originalSanitize

Object.defineProperty(window, '__rt16SecurityState', { value: state })
Object.defineProperty(window, '__rt16DummySecret', {
  value: 'RT16_SYNTHETIC_NOT_A_CREDENTIAL',
})
const flags = window as Window & { __rt16Executed?: number }
flags.__rt16Executed = 0

Object.defineProperty(window, '__rt16Render', {
  value(markdown: string) {
    if (typeof markdown !== 'string' || markdown.length > 50000) {
      throw new Error('RT16 input must be a string of at most 50000 characters')
    }
    flushSync(() => root.render(null))
    stages = []
    input = markdown
    flags.__rt16Executed = 0
    flushSync(() => root.render(<Markdown>{markdown}</Markdown>))
  },
})
Object.defineProperty(window, '__rt16Inspect', {
  value() {
    const unsafeAttributes: string[] = []
    const links = [...host.querySelectorAll('a')].map((link) => ({
      href: link.getAttribute('href'),
      target: link.getAttribute('target'),
      rel: link.getAttribute('rel'),
    }))
    for (const element of host.querySelectorAll('*')) {
      for (const attribute of element.attributes) {
        if (/^on/i.test(attribute.name)) {
          unsafeAttributes.push(`${element.tagName}.${attribute.name}`)
        }
        if (['href', 'src', 'xlink:href', 'action', 'formaction'].includes(attribute.name)) {
          try {
            const protocol = new URL(attribute.value, location.href).protocol
            if (protocol === 'javascript:' || protocol === 'vbscript:') {
              unsafeAttributes.push(`${element.tagName}.${attribute.name}:${protocol}`)
            }
          } catch {
            // An invalid URL is recorded in finalDOM, not treated as execution.
          }
        }
      }
    }
    return {
      scope: 'component-only; no application save/read or database check',
      input,
      stages,
      finalDOM: host.innerHTML,
      text: host.textContent,
      executed: flags.__rt16Executed,
      stateUnchanged:
        window.document === originalDocument &&
        window.location === originalLocation &&
        Reflect.get(window, '__rt16SecurityState') === state,
      unsafeAttributes,
      activeElements: host.querySelectorAll('script,iframe,object,embed').length,
      links,
      // Named DOM access is observable, not by itself proof of a real app exploit.
      namedDomAccess: Reflect.get(window, '__rt16UnusedConfig') instanceof Element,
      duplicateIds: [...host.querySelectorAll('[id]')].map((element) => element.id)
        .filter((id, index, all) => all.indexOf(id) !== index),
      purifierVersion: DOMPurify.version,
    }
  },
})
// The runner checks this to ensure that trusted JavaScript really executed.
Object.defineProperty(window, '__rt16Ready', { value: true })
