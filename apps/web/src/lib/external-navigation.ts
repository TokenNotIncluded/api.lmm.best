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
/*
Copyright (C) 2026 LIghtJUNction
*/

type ExternalUrlResolver = () => string | null | Promise<string | null>

function closePopup(popup: Window | null) {
  if (!popup) return
  try {
    if (popup.closed) return
    popup.close()
  } catch {
    // The browser may expose a restricted WindowProxy after navigation.
  }
}

function navigateCurrentWindow(url: string): boolean {
  let anchor: HTMLAnchorElement | null = null
  try {
    anchor = document.createElement('a')
    anchor.href = url
    anchor.target = '_self'
    anchor.rel = 'noopener noreferrer'
    document.body.appendChild(anchor)
    anchor.click()
    return true
  } catch {
    return false
  } finally {
    anchor?.remove()
  }
}

function isPopupOpen(popup: Window | null): popup is Window {
  if (!popup) return false
  try {
    return !popup.closed
  } catch {
    return false
  }
}

/**
 * Reserve a window inside the click event, then resolve a validated URL.
 * Browsers routinely block window.open calls made after an async credential
 * request. A blocked or prematurely closed popup falls back to the current tab.
 */
export async function openResolvedExternalUrl(
  resolveUrl: ExternalUrlResolver
): Promise<boolean> {
  if (typeof window === 'undefined') return false

  let popup: Window | null = null
  try {
    popup = window.open()
  } catch {
    popup = null
  }
  if (popup) {
    try {
      popup.opener = null
    } catch {
      // Some WindowProxy implementations expose a read-only opener.
    }
  }

  let completed = false
  try {
    const url = await resolveUrl()
    if (!url) return false

    if (isPopupOpen(popup)) {
      try {
        popup.location.href = url
        try {
          popup.focus()
        } catch {
          // Navigation succeeded even when focus is restricted.
        }
        completed = true
        return true
      } catch {
        closePopup(popup)
      }
    }

    completed = navigateCurrentWindow(url)
    return completed
  } finally {
    if (!completed) closePopup(popup)
  }
}

/**
 * Open a validated URL with the same blocked-popup fallback used by async
 * navigation. Callers remain responsible for validating the URL first.
 */
export function openExternalUrl(url: string): Promise<boolean> {
  return openResolvedExternalUrl(() => url)
}
