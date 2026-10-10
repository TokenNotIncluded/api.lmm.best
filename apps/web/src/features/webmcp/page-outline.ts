/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { clip } from './tool-kit'

const PRIVATE_CONTENT =
  'input, textarea, select, code, pre, [hidden], [inert], [aria-hidden="true"], [data-sensitive], [data-private], [data-webmcp-private]'

function safeText(value: string) {
  return clip(
    value
      .replaceAll(/\bsk-[\w-]+/g, '[redacted]')
      .replaceAll(/\b[A-Za-z0-9_-]{24,}(?:\.[A-Za-z0-9_-]+)*\b/g, '[redacted]')
      .replaceAll(/\s+/g, ' ')
      .trim(),
    120
  )
}

function visible(element: Element) {
  if (element.closest(PRIVATE_CONTENT)) return false
  const view = element.ownerDocument.defaultView
  for (
    let current: Element | null = element;
    current;
    current = current.parentElement
  ) {
    const style = view?.getComputedStyle(current)
    if (style?.display === 'none' || style?.visibility === 'hidden') {
      return false
    }
  }
  return true
}

function label(element: Element) {
  const explicit = element.getAttribute('aria-label')
  if (explicit) return safeText(explicit)
  const copy = element.cloneNode(true) as Element
  for (const privateElement of copy.querySelectorAll(PRIVATE_CONTENT)) {
    privateElement.remove()
  }
  return safeText(copy.textContent ?? '')
}

/** Return navigation affordances only. Form values and URL parameters stay private. */
export function readPageOutline(knownPaths: Readonly<Record<string, string>>) {
  const root =
    document.querySelector('main') ??
    document.querySelector('#root') ??
    document.body
  const knownPath = (value: string) => {
    try {
      const url = new URL(value, window.location.origin)
      if (
        url.origin !== window.location.origin ||
        url.username ||
        url.password
      ) {
        return null
      }
      return Object.hasOwn(knownPaths, url.pathname) ? url.pathname : null
    } catch {
      return null
    }
  }
  const headings = [...root.querySelectorAll('h1, h2, h3')]
    .filter(visible)
    .map((element) => ({
      level: Number(element.tagName[1]),
      text: label(element),
    }))
    .filter((heading) => heading.text)
    .slice(0, 40)
  const actions = [...root.querySelectorAll('button, a[href], [role="tab"]')]
    .filter(visible)
    .map((element) => ({
      kind:
        element.getAttribute('role') === 'tab'
          ? 'tab'
          : element.tagName === 'A'
            ? 'link'
            : 'button',
      text: label(element),
      href:
        element.tagName === 'A'
          ? knownPath(element.getAttribute('href') ?? '')
          : null,
      disabled:
        element.hasAttribute('disabled') ||
        element.getAttribute('aria-disabled') === 'true',
    }))
    .filter((action) => action.text)
    .slice(0, 60)
  return {
    path: knownPath(window.location.pathname),
    title: safeText(document.title),
    language: document.documentElement.lang || null,
    headings,
    actions,
  }
}
