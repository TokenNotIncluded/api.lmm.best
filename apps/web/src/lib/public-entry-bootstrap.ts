/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */

// These pages expose public content without setup/session APIs. Creating a key
// still requires the page's account checks and the server's authorization.
export function bootstrapPublicEntry(
  pathname: string,
  authenticate: () => Promise<unknown>
): boolean {
  const publicEntry =
    pathname === '/scripts' ||
    pathname.startsWith('/scripts/') ||
    pathname === '/test-key' ||
    pathname === '/test-key/'
  if (!publicEntry) return false

  void authenticate().catch(() => undefined)
  return true
}
