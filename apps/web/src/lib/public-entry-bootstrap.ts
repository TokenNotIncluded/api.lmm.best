/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */

// These pages expose public content without setup/session APIs. Creating a key
// still requires the page's account checks and the server's authorization.
export function bootstrapPublicEntry(
  pathname: string,
  authenticate: () => Promise<unknown>
): boolean {
  // Pickup alone validates its existing cookie for this order. Starting the
  // standard bootstrap here would mint a broader session or call an IP-blocked
  // refresh endpoint before a buyer can collect from a cold-open private link.
  if (/^\/store\/claim\/[A-Za-z0-9_-]{43}$/.test(pathname)) return true

  const publicEntry =
    pathname === '/scripts' ||
    pathname.startsWith('/scripts/') ||
    pathname === '/test-key' ||
    pathname === '/test-key/'
  if (!publicEntry) return false

  void authenticate().catch(() => undefined)
  return true
}
