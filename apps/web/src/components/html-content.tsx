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
/* oxlint-disable react/no-danger -- SanitizedHtml is branded only after DOMPurify and post-mutation sanitization. */
import DOMPurify, { type Config } from 'dompurify'
import { useEffect, useMemo, useRef } from 'react'

import { cn } from '@/lib/utils'

export type HtmlContentVariant = 'inline' | 'isolated'

interface HtmlContentProps {
  content: string
  className?: string
  variant?: HtmlContentVariant
}

const isolatedContentSandbox =
  'allow-forms allow-popups allow-popups-to-escape-sandbox allow-presentation'

const isolatedContentBaseStyles = `
  :host {
    display: block;
    width: 100%;
    color: inherit;
    font: inherit;
  }

  *,
  *::before,
  *::after {
    box-sizing: border-box;
  }

  img,
  video,
  iframe {
    max-width: 100%;
  }

  iframe {
    border: 0;
  }
`

declare const sanitizedHtmlBrand: unique symbol

type SanitizedHtml = string & {
  readonly [sanitizedHtmlBrand]: true
}

const isolatedSanitizeOptions = {
  ADD_ATTR: [
    'allowfullscreen',
    'autoplay',
    'class',
    'controls',
    'default',
    'id',
    'kind',
    'label',
    'loading',
    'loop',
    'muted',
    'playsinline',
    'poster',
    'preload',
    'referrerpolicy',
    'rel',
    'sandbox',
    'srclang',
    'style',
    'target',
  ],
  ADD_TAGS: ['audio', 'iframe', 'picture', 'source', 'style', 'track', 'video'],
  FORBID_ATTR: ['srcdoc'],
  FORBID_TAGS: ['base', 'embed', 'link', 'meta', 'object', 'script'],
  FORCE_BODY: true,
} satisfies Config

function sanitizeToFragment(html: string, config: Config): DocumentFragment {
  return DOMPurify.sanitize(html, {
    ...config,
    RETURN_DOM_FRAGMENT: true,
  })
}

function sanitizeToHtml(html: string | Node, config?: Config): SanitizedHtml {
  return DOMPurify.sanitize(html, config) as SanitizedHtml
}

function hardenIsolatedHtml(fragment: DocumentFragment): void {
  fragment.querySelectorAll('a[target="_blank"]').forEach((link) => {
    const rel = new Set(
      link.getAttribute('rel')?.split(/\s+/).filter(Boolean) ?? []
    )

    rel.add('noopener')
    rel.add('noreferrer')
    link.setAttribute('rel', [...rel].join(' '))
  })

  fragment.querySelectorAll('iframe').forEach((frame) => {
    frame.removeAttribute('srcdoc')
    frame.setAttribute('sandbox', isolatedContentSandbox)
    frame.setAttribute('referrerpolicy', 'no-referrer')

    if (!frame.hasAttribute('loading')) {
      frame.setAttribute('loading', 'lazy')
    }
  })
}

function sanitizeHtmlContent(
  content: string,
  variant: HtmlContentVariant
): SanitizedHtml {
  if (variant === 'isolated') {
    if (typeof document === 'undefined') {
      return sanitizeToHtml(content, isolatedSanitizeOptions)
    }

    const fragment = sanitizeToFragment(content, isolatedSanitizeOptions)
    hardenIsolatedHtml(fragment)

    // Sanitize again after DOM mutation so mutation-XSS cannot bypass the
    // policy between hardening and the eventual rendering sink.
    return sanitizeToHtml(fragment, isolatedSanitizeOptions)
  }

  return sanitizeToHtml(content)
}

function syncDarkClass(wrapper: HTMLElement): void {
  const isDark = document.documentElement.classList.contains('dark')
  wrapper.classList.toggle('dark', isDark)
}

function SanitizedHtmlContent(props: {
  className?: string
  html: SanitizedHtml
}): React.ReactElement {
  return (
    <div
      className={props.className}
      // biome-ignore lint/security/noDangerouslySetInnerHtml: SanitizedHtml values can only be created by DOMPurify.
      dangerouslySetInnerHTML={{ __html: props.html }}
    />
  )
}

function IsolatedHtmlContent(props: {
  className?: string
  html: SanitizedHtml
}): React.ReactElement {
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const container = containerRef.current
    if (!container) {
      return
    }

    const shadowRoot =
      container.shadowRoot ?? container.attachShadow({ mode: 'open' })
    const applicationStyleNodes = [
      ...document.head.querySelectorAll<HTMLLinkElement | HTMLStyleElement>(
        'style, link[rel="stylesheet"]'
      ),
    ].map((node) => node.cloneNode(true))

    const wrapper = document.createElement('div')
    syncDarkClass(wrapper)
    wrapper.replaceChildren(
      sanitizeToFragment(props.html, isolatedSanitizeOptions)
    )

    const contentStyle = document.createElement('style')
    contentStyle.textContent = isolatedContentBaseStyles

    shadowRoot.replaceChildren(...applicationStyleNodes, contentStyle, wrapper)

    const observer = new MutationObserver(() => syncDarkClass(wrapper))
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class'],
    })

    return () => observer.disconnect()
  }, [props.html])

  return (
    <div ref={containerRef} className={cn('block w-full', props.className)} />
  )
}

export function HtmlContent(props: HtmlContentProps) {
  const variant = props.variant ?? 'inline'
  const html = useMemo(
    () => sanitizeHtmlContent(props.content, variant),
    [props.content, variant]
  )

  if (variant === 'isolated') {
    return <IsolatedHtmlContent className={props.className} html={html} />
  }

  return (
    <SanitizedHtmlContent
      className={cn(
        'prose prose-neutral dark:prose-invert max-w-none',
        props.className
      )}
      html={html}
    />
  )
}
