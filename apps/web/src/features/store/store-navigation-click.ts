/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { MouseEvent } from 'react'

export function handleStoreNavigationClick(
  event: MouseEvent<HTMLElement>,
  navigate: (options: { href: string }) => Promise<unknown>
) {
  if (
    event.defaultPrevented ||
    event.button !== 0 ||
    event.metaKey ||
    event.ctrlKey ||
    event.shiftKey ||
    event.altKey ||
    !(event.target instanceof Element)
  ) {
    return
  }
  const anchor = event.target.closest('a[href]')
  if (
    !anchor ||
    !event.currentTarget.contains(anchor) ||
    anchor.hasAttribute('download') ||
    (anchor.getAttribute('target') && anchor.getAttribute('target') !== '_self')
  ) {
    return
  }
  const href = anchor.getAttribute('href')
  if (href === null) return
  let url: URL
  try {
    url = new URL(href, window.location.href)
  } catch {
    return
  }
  if (
    url.origin !== window.location.origin ||
    (url.pathname !== '/store' && !url.pathname.startsWith('/store/')) ||
    url.pathname.startsWith('/store/claim/')
  ) {
    return
  }
  // Keep ordinary shop navigation in the current app and authenticated session.
  // Private pickup links retain their separate document/bootstrap boundary.
  event.preventDefault()
  void navigate({ href: `${url.pathname}${url.search}${url.hash}` })
}
