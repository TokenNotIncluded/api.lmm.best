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
import { useEffect, useRef } from 'react'

declare global {
  interface Window {
    turnstile?: {
      render: (
        element: HTMLElement,
        options: Record<string, unknown>
      ) => unknown
      remove?: (widgetId: unknown) => void
    }
  }
}

interface TurnstileProps {
  siteKey: string
  onVerify: (token: string) => void
  onExpire?: () => void
  className?: string
}

export function Turnstile({
  siteKey,
  onVerify,
  onExpire,
  className,
}: TurnstileProps) {
  const ref = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    let disposed = false
    let rendered = false
    let poller: number | undefined
    let retryTimer: number | undefined
    let widgetId: unknown
    let renderAttempts = 0

    const stopTimers = () => {
      if (poller !== undefined) {
        window.clearInterval(poller)
        poller = undefined
      }
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer)
        retryTimer = undefined
      }
    }

    const renderWidget = () => {
      if (disposed || rendered || !ref.current || !window.turnstile) return
      try {
        widgetId = window.turnstile.render(ref.current, {
          sitekey: siteKey,
          callback: (token: string) => onVerify(token),
          'error-callback': () => onExpire?.(),
          'expired-callback': () => onExpire?.(),
        })
        rendered = true
        stopTimers()
      } catch {
        // The async Turnstile script can expose its API just before the
        // widget runtime is ready. Retry briefly without calling turnstile.ready:
        // Cloudflare rejects ready() when api.js is loaded async/defer.
        renderAttempts += 1
        if (!disposed && renderAttempts < 20 && retryTimer === undefined) {
          retryTimer = window.setTimeout(() => {
            retryTimer = undefined
            renderWidget()
          }, 100)
        }
      }
    }

    const cleanup = () => {
      disposed = true
      stopTimers()
      if (widgetId !== undefined) {
        try {
          window.turnstile?.remove?.(widgetId)
        } catch {
          // The widget may already have been removed by Turnstile.
        }
      }
    }

    const scriptId = 'cf-turnstile'
    const existingScript = document.querySelector(`#${scriptId}`)

    // A different login form may have inserted the shared script already.
    // Wait for its runtime instead of returning before this widget is rendered.
    if (existingScript && !window.turnstile) {
      poller = window.setInterval(renderWidget, 100)
      existingScript.addEventListener('load', renderWidget, { once: true })
      return cleanup
    }

    const render = () => {
      renderWidget()
    }

    if (window.turnstile) {
      render()
      return cleanup
    }
    const s = document.createElement('script')
    s.id = scriptId
    s.src =
      'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
    s.async = true
    s.defer = true
    s.onload = () => render()
    document.head.appendChild(s)

    return cleanup
  }, [siteKey, onVerify, onExpire])

  return <div ref={ref} className={className} />
}
