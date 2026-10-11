/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */

export const PAGE_SIZE_OPTIONS = [10, 20, 30, 40, 50, 100] as const

type PageState = { pageIndex: number; pageSize: number }

export function parsePageSize(value: string | null): number | null {
  const size = PAGE_SIZE_OPTIONS.find((option) => String(option) === value)
  return size ?? null
}

/** Inline mobile pages share the content scroller; desktop footers do not. */
export function resetInlinePaginationScroll(
  control: HTMLElement | null,
  previous: PageState,
  current: PageState
): void {
  if (
    previous.pageIndex === current.pageIndex &&
    previous.pageSize === current.pageSize
  ) {
    return
  }
  control
    ?.closest<HTMLElement>('[data-mobile-scroll-root]')
    ?.scrollTo({ top: 0, behavior: 'instant' })
}
