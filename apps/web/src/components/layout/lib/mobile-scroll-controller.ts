/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
export const MOBILE_SCROLL_MEDIA = '(max-width: 767px)'

export type ScrollSample = {
  top: number
  max: number
  viewport: number
}

type ScrollTrack = ScrollSample & { travel: number }

/** Track net travel in one direction, not individual noisy scroll events. */
export function trackMobileScroll(
  previous: ScrollTrack | undefined,
  sample: ScrollSample
): { track: ScrollTrack; hidden?: boolean } {
  const max = Math.max(0, sample.max)
  const top = Math.max(0, Math.min(sample.top, max))
  const track = { ...sample, top, max, travel: 0 }

  // Collapsing the bars changes the scroll range. Its resulting scroll event
  // is layout movement, not an upward gesture. Also reset after content resizes.
  if (
    previous &&
    (previous.max !== max || previous.viewport !== sample.viewport)
  ) {
    return { track }
  }
  if (top <= 8 || max === 0) return { track, hidden: false }
  if (!previous) return { track }

  const delta = top - previous.top
  track.travel =
    delta === 0 || Math.sign(delta) === Math.sign(previous.travel)
      ? previous.travel + delta
      : delta
  if (track.travel >= 24) {
    return { track: { ...track, travel: 0 }, hidden: true }
  }
  if (track.travel <= -12) {
    return { track: { ...track, travel: 0 }, hidden: false }
  }
  return { track }
}

const EDITABLE =
  'textarea, input:not([type]), input[type="text"], input[type="search"], input[type="email"], input[type="password"], input[type="tel"], input[type="url"], input[type="number"], [contenteditable]:not([contenteditable="false"])'
const OVERLAY =
  '[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"]'

function interactionNeedsControls(root: HTMLElement) {
  const active = document.activeElement
  if (
    active instanceof HTMLElement &&
    (active.closest(EDITABLE) ||
      (active.closest('[data-mobile-scroll-chrome]') &&
        active.matches(':focus-visible')))
  ) {
    return true
  }
  return Boolean(
    root.querySelector(
      '[data-mobile-scroll-chrome] [aria-expanded="true"], [data-mobile-scroll-chrome] details[open]'
    ) ||
    document.querySelector(
      '[aria-modal="true"]:not([hidden]):not([data-closed]), [role="menu"][data-open], [role="listbox"][data-open], [role="menu"][data-state="open"], [role="listbox"][data-state="open"]'
    )
  )
}

function readSample(target: HTMLElement): ScrollSample {
  return {
    top: target.scrollTop,
    max: target.scrollHeight - target.clientHeight,
    viewport: target.clientHeight,
  }
}

/** One passive, capture-phase listener covers both page and nested list scrolls. */
export function observeMobileScroll(
  root: HTMLElement,
  onHiddenChange: (hidden: boolean) => void,
  documentScroll = false
) {
  const media = window.matchMedia(MOBILE_SCROLL_MEDIA)
  let tracks = new WeakMap<HTMLElement, ScrollTrack>()
  let hidden = false
  let width = window.innerWidth

  const update = (next: boolean) => {
    if (hidden === next) return
    hidden = next
    onHiddenChange(next)
  }
  const reveal = () => {
    tracks = new WeakMap()
    update(false)
  }
  const seed = (target: HTMLElement, afterScroll = false) => {
    const sample = readSample(target)
    const previous = tracks.get(target)
    if (
      sample.max > 0 &&
      (!previous ||
        previous.max !== sample.max ||
        previous.viewport !== sample.viewport)
    ) {
      const track = trackMobileScroll(undefined, sample).track
      // Passive wheel events can arrive after the compositor has scrolled.
      // Keep the old position, clamped to the new range, when rebasing them.
      if (afterScroll && previous) {
        track.top = Math.max(0, Math.min(previous.top, sample.max))
      }
      tracks.set(target, track)
    }
  }
  const onIntent = (event: Event) => {
    if (!media.matches) return
    let target = event.target instanceof HTMLElement ? event.target : null
    while (target && root.contains(target)) {
      seed(target, event.type === 'wheel')
      target = target.parentElement
    }
    if (documentScroll) seed(document.documentElement)
  }
  const onScroll = (event: Event) => {
    if (!media.matches) return
    const target =
      event.target === document && documentScroll
        ? document.scrollingElement || document.documentElement
        : event.target
    if (!(target instanceof HTMLElement)) return
    const isDocument =
      target === document.scrollingElement ||
      target === document.documentElement
    if (isDocument) {
      if (!documentScroll) return
    } else if (
      !root.contains(target) ||
      !target.closest('main, [data-mobile-scroll-root]') ||
      target.closest(`${OVERLAY}, ${EDITABLE}, [data-mobile-scroll-ignore]`)
    ) {
      return
    }
    // Horizontal-only widgets must not change the page controls.
    if (target.scrollHeight <= target.clientHeight) {
      if (tracks.has(target)) reveal()
      return
    }
    if (interactionNeedsControls(root)) {
      reveal()
      return
    }

    const sample = readSample(target)
    const result = trackMobileScroll(tracks.get(target), sample)
    tracks.set(target, result.track)
    if (result.hidden === undefined) return
    if (result.hidden && !hidden) {
      // A short page must remain scrollable after the bars give back their
      // height, otherwise there is no upward gesture available to restore them.
      const releasedHeight = Array.from(
        root.querySelectorAll<HTMLElement>(
          '[data-mobile-scroll-chrome][data-mode="flow"]'
        )
      ).reduce(
        (height, region) => height + region.getBoundingClientRect().height,
        0
      )
      if (sample.max <= releasedHeight + 32) return
    }
    update(result.hidden)
  }
  const onFocus = (event: FocusEvent) => {
    if (event.target instanceof Node && root.contains(event.target)) reveal()
  }
  const onKey = (event: KeyboardEvent) => {
    if (event.key === 'Tab' || event.key === 'Escape' || event.key === 'Home') {
      reveal()
    }
  }
  const onResize = () => {
    // Mobile browser bars change height while scrolling. Only a width change
    // resets the controls; keyboard editing is handled by focus instead.
    if (width !== window.innerWidth) {
      width = window.innerWidth
      reveal()
    }
  }

  onHiddenChange(false)
  root
    .querySelectorAll<HTMLElement>('main, [data-mobile-scroll-root]')
    .forEach((target) => seed(target))
  if (documentScroll) seed(document.documentElement)
  root.addEventListener('pointerdown', onIntent, {
    capture: true,
    passive: true,
  })
  root.addEventListener('wheel', onIntent, { capture: true, passive: true })
  document.addEventListener('scroll', onScroll, {
    capture: true,
    passive: true,
  })
  document.addEventListener('focusin', onFocus)
  document.addEventListener('keydown', onKey)
  window.addEventListener('resize', onResize)
  media.addEventListener('change', reveal)
  return () => {
    root.removeEventListener('pointerdown', onIntent, true)
    root.removeEventListener('wheel', onIntent, true)
    document.removeEventListener('scroll', onScroll, true)
    document.removeEventListener('focusin', onFocus)
    document.removeEventListener('keydown', onKey)
    window.removeEventListener('resize', onResize)
    media.removeEventListener('change', reveal)
  }
}
