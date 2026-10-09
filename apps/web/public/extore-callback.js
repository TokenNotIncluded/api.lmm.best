/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
;(() => {
  if (window.location.pathname !== '/store/manage') return
  const query = new URLSearchParams(window.location.search)
  if (
    !['code', 'state', 'iss', 'error', 'error_description'].some((key) =>
      query.has(key)
    )
  ) {
    return
  }
  window.__lmmExtoreCallbackPage = true
  Object.defineProperty(window, '__lmmExtoreCallback', {
    value: window.location.href,
    configurable: true,
  })
  const referrer = document.createElement('meta')
  referrer.name = 'referrer'
  referrer.content = 'no-referrer'
  document.head.appendChild(referrer)
  const csp = document.createElement('meta')
  csp.httpEquiv = 'Content-Security-Policy'
  csp.content =
    "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-src 'none'"
  document.head.appendChild(csp)
  window.history.replaceState(window.history.state, '', '/store/manage')
})()
