/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
export const TEST_KEY_PATH = '/test-key'
export const TEST_KEY_BOOKMARK_NAME = 'LMM Test Key'
export const TEST_KEY_WINDOW_FEATURES =
  'popup,width=460,height=680,resizable=yes,scrollbars=yes,noopener,noreferrer'

/** Only the site's address belongs in a bookmark. Never include account data. */
export function testKeyUrl(origin: string): string {
  const url = new URL(origin)
  if (
    !['https:', 'http:'].includes(url.protocol) ||
    url.username ||
    url.password
  ) {
    throw new Error('Invalid site origin')
  }
  return new URL(TEST_KEY_PATH, url.origin).href
}

export function buildTestKeyBookmarklet(origin: string): string {
  return `javascript:void(window.open(${JSON.stringify(testKeyUrl(origin))},"_blank",${JSON.stringify(TEST_KEY_WINDOW_FEATURES)}))`
}

export function openTestKeyWindow(): void {
  // noopener deliberately returns null, even when a window opens successfully.
  // Do not interpret that as a blocked popup and navigate the original page.
  window.open(
    testKeyUrl(window.location.origin),
    '_blank',
    TEST_KEY_WINDOW_FEATURES
  )
}
