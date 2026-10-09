/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { buildTestKeyBookmarklet, TEST_KEY_BOOKMARK_NAME } from './bookmarklet'

// Rasterized from public/lmm-cut-mark.svg at 32px. The light background keeps
// the existing black mark visible in both light and dark bookmark bars.
// Bookmark HTML importers accept PNG data URLs; a dragged SVG does not set
// the browser-owned bookmark icon.
export const TEST_KEY_BOOKMARK_ICON =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAIAAAD8GO2jAAAABmJLR0QA/wD/AP+gvaeTAAAA40lEQVRIie2WMQ6DMAxF7aqCAbFF3CNDjgAzG+IInIGbcBQuwWGQV9LBFYraSrVLo0KFJ8d6yZfzo8hIRBAzLlFP/wsBIKIHGygIrlRVxcuyLLUMMpFlWSiw5lxHxGmaENFauyyLirlKuvTeD8PgveedKkbUAQDkeQ4A8zxrGVEH4TYts49nyr2HiZwRCbRtm6ZpkiRN02gZkQfGmLquEbEoCi0jNbnrOgAYx1HLSE12zjnnPmD28YpOgajx5rPbHse/ougCL76K0I8twV4e9or6vr9nz2PLV05fhxo8Z9OfC9wAlz+nfCWlkWQAAAAASUVORK5CYII='

function escapeHtml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
}

/** Importing this file saves the script and icon, never an account credential. */
export function buildTestKeyBookmarkFile(
  origin: string,
  name = TEST_KEY_BOOKMARK_NAME
): string {
  const title = escapeHtml(name)
  const href = escapeHtml(buildTestKeyBookmarklet(origin))
  return [
    '<!DOCTYPE NETSCAPE-Bookmark-file-1>',
    '<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">',
    `<TITLE>${title}</TITLE>`,
    `<H1>${title}</H1>`,
    '<DL><p>',
    `    <DT><A HREF="${href}" ICON="${TEST_KEY_BOOKMARK_ICON}">${title}</A>`,
    '</DL><p>',
    '',
  ].join('\n')
}

export function testKeyBookmarkDownloadUrl(origin: string, name: string): string {
  return `data:text/html;charset=utf-8,${encodeURIComponent(buildTestKeyBookmarkFile(origin, name))}`
}
