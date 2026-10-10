/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
declare global {
  interface Window {
    __lmmExtoreCallback?: string
    __lmmExtoreCallbackPage?: boolean
  }
}

export function extoreCallbackURL(): string | null {
  const raw = window.__lmmExtoreCallback ?? window.location.href
  const url = new URL(raw)
  return url.pathname === '/store/manage' &&
    ['code', 'state', 'iss', 'error', 'error_description'].some((key) =>
      url.searchParams.has(key)
    )
    ? raw
    : null
}

export function clearExtoreCallbackURL(): void {
  delete window.__lmmExtoreCallback
  window.history.replaceState(window.history.state, '', '/store/manage')
}
