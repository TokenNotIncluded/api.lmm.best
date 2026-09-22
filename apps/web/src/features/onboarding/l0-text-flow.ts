/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
// Display graphemes, not model tokenizer IDs. Stream deltas carry text only.
const segmenter =
  typeof Intl.Segmenter === 'function'
    ? new Intl.Segmenter(undefined, { granularity: 'grapheme' })
    : null

export function visualTokens(text: string) {
  if (segmenter) {
    return Array.from(segmenter.segment(text), ({ segment, index }) => ({
      text: segment,
      index,
    }))
  }
  // Older browsers display a whole chunk rather than break an emoji or accent.
  return text ? [{ text, index: 0 }] : []
}

/** Bound segmentation work to the animated suffix, not the whole growing answer. */
export function visualTokenTail(text: string, limit = 192) {
  if (!text || limit <= 0) return []
  if (!segmenter) return visualTokens(text)
  const segments = segmenter.segment(text)
  const start =
    segments.containing(Math.max(0, text.length - limit * 8))?.index ?? 0
  return visualTokens(text.slice(start))
    .map((token) => ({
      ...token,
      index: token.index + start,
    }))
    .slice(-limit)
}

export function insertedTokens(before: string, after: string) {
  const old = visualTokens(before)
  const next = visualTokens(after)
  let start = 0
  let end = next.length
  let oldEnd = old.length
  while (
    start < oldEnd &&
    start < end &&
    old[start].text === next[start].text
  ) {
    start++
  }
  while (
    end > start &&
    oldEnd > start &&
    next[end - 1].text === old[oldEnd - 1].text
  ) {
    end--
    oldEnd--
  }
  return next.slice(start, end)
}

const MAX_FLIGHTS = 80
const MAX_ARRIVAL_DELAY = 340

type Point = { x: number; y: number }
type Flight = {
  animation: Animation
  settle: () => void
  direction: 'up' | 'down'
}

/** The animation layer is disposable, non-interactive and never stores text. */
export function mountL0TextFlow(cloud: HTMLElement) {
  const doc = cloud.ownerDocument
  const win = doc.defaultView!
  const motion = win.matchMedia('(prefers-reduced-motion: reduce)')
  const layer = doc.createElement('div')
  layer.className = 'l0-flight-layer'
  layer.setAttribute('aria-hidden', 'true')
  Object.assign(layer.style, {
    position: 'fixed',
    inset: '0',
    overflow: 'hidden',
    pointerEvents: 'none',
    zIndex: '49',
  })
  doc.body.append(layer)
  const flights = new Set<Flight>()
  const seen = new WeakMap<HTMLElement, string>()
  const arrivals = new Map<HTMLElement, () => void>()
  let disposed = false
  let sequence = 0

  const enabled = () =>
    !disposed &&
    !doc.hidden &&
    !motion.matches &&
    typeof layer.animate === 'function' &&
    cloud.querySelector('[data-cloud-pause]')?.getAttribute('aria-pressed') !==
      'true'

  const onScreen = (rect: DOMRect) =>
    rect.width > 0 &&
    rect.height > 0 &&
    rect.bottom > 0 &&
    rect.top < win.innerHeight &&
    rect.right > 0 &&
    rect.left < win.innerWidth

  const cloudPoint = (): Point => {
    const rect = cloud.getBoundingClientRect()
    const a = sequence++ * 2.39996323
    const radius = Math.min(rect.width * 0.29, rect.height * 0.34)
    return {
      x: rect.left + rect.width / 2 + Math.cos(a) * radius,
      y: rect.top + rect.height / 2 + Math.sin(a) * radius * 0.65,
    }
  }

  const fly = (
    text: string,
    from: Point,
    to: Point,
    style: CSSStyleDeclaration,
    direction: 'up' | 'down',
    done: () => void = () => {}
  ) => {
    if (!enabled() || flights.size >= MAX_FLIGHTS || !text.trim()) {
      done()
      return
    }
    const node = doc.createElement('span')
    node.dataset.l0Flight = direction
    node.textContent = text
    Object.assign(node.style, {
      position: 'absolute',
      left: '0',
      top: '0',
      margin: '0',
      font: style.font,
      letterSpacing: style.letterSpacing,
      color: style.color,
      whiteSpace: 'pre',
      lineHeight: style.lineHeight,
      transformOrigin: 'center',
      willChange: 'transform,opacity',
    })
    layer.append(node)
    const path = (point: Point, scale = 1) =>
      `translate3d(${point.x}px,${point.y}px,0) scale(${scale})`
    const bend = {
      x: from.x * 0.4 + to.x * 0.6,
      y: from.y * 0.55 + to.y * 0.45,
    }
    let animation: Animation
    try {
      animation = node.animate(
        direction === 'up'
          ? [
              { transform: path(from), opacity: 1, offset: 0 },
              { transform: path(bend, 0.9), opacity: 0.95, offset: 0.48 },
              { transform: path(to, 0.7), opacity: 0.65, offset: 0.78 },
              { transform: path(to, 0.5), opacity: 0, offset: 1 },
            ]
          : [
              { transform: path(from, 0.7), opacity: 0, offset: 0 },
              { transform: path(from, 0.8), opacity: 1, offset: 0.13 },
              { transform: path(bend, 0.95), opacity: 1, offset: 0.52 },
              { transform: path(to), opacity: 1, offset: 1 },
            ],
        {
          duration:
            direction === 'up' ? 540 + (sequence % 5) * 24 : MAX_ARRIVAL_DELAY,
          easing: 'cubic-bezier(.2,.65,.25,1)',
          fill: 'both',
        }
      )
    } catch {
      node.remove()
      done()
      return
    }
    let settled = false
    const flight: Flight = {
      animation,
      direction,
      settle: () => {
        if (settled) return
        settled = true
        flights.delete(flight)
        animation.onfinish = null
        animation.oncancel = null
        animation.cancel()
        node.remove()
        done()
      },
    }
    flights.add(flight)
    animation.onfinish = flight.settle
    animation.oncancel = flight.settle
    return flight.settle
  }

  const clear = () => {
    for (const flight of flights) flight.settle()
  }
  const clearResponses = () => {
    for (const flight of flights) {
      if (flight.direction === 'down') flight.settle()
    }
  }
  const canFly = () => enabled() && onScreen(cloud.getBoundingClientRect())

  // Preset spans occupy the exact original sentence positions, including wraps.
  const sentence = (source: HTMLElement) => {
    if (!canFly()) return
    for (const token of source.querySelectorAll<HTMLElement>(
      '[data-l0-source]'
    )) {
      const rect = token.getBoundingClientRect()
      const text = token.textContent ?? ''
      if (!onScreen(rect) || !text.trim() || flights.size >= MAX_FLIGHTS) {
        continue
      }
      const style = win.getComputedStyle(token)
      const original = token.style.opacity
      const point = cloudPoint()
      token.style.opacity = '0'
      fly(text, { x: rect.left, y: rect.top }, point, style, 'up', () => {
        token.style.opacity = original
      })
    }
  }

  // Native input remains editable. Only committed inserted text is visualized.
  const input = (source: HTMLInputElement, before = '') => {
    if (!canFly()) return
    const tokens = insertedTokens(before, source.value).slice(-MAX_FLIGHTS)
    if (!tokens.length) return
    const rect = source.getBoundingClientRect()
    const style = win.getComputedStyle(source)
    const mirror = doc.createElement('span')
    const lineHeight =
      Number.parseFloat(style.lineHeight) ||
      Number.parseFloat(style.fontSize) * 1.5
    Object.assign(mirror.style, {
      position: 'fixed',
      visibility: 'hidden',
      whiteSpace: 'pre',
      font: style.font,
      letterSpacing: style.letterSpacing,
      lineHeight: `${lineHeight}px`,
      left: `${rect.left - source.scrollLeft}px`,
      top: `${rect.top + (rect.height - lineHeight) / 2}px`,
    })
    const text = doc.createTextNode(source.value)
    mirror.append(text)
    doc.body.append(mirror)
    for (const token of tokens) {
      const range = doc.createRange()
      range.setStart(text, token.index)
      range.setEnd(text, token.index + token.text.length)
      const start = range.getBoundingClientRect()
      if (
        start.left < rect.left ||
        start.right > rect.right ||
        !onScreen(start)
      ) {
        continue
      }
      fly(
        token.text,
        { x: start.left, y: start.top },
        cloudPoint(),
        style,
        'up'
      )
    }
    mirror.remove()
  }

  // Actual stream text is laid out first. Its visual copy lands at that same spot.
  const receive = (answer: HTMLElement, animate = true) => {
    for (const token of answer.querySelectorAll<HTMLElement>(
      '[data-l0-arrival]'
    )) {
      const text = token.textContent ?? ''
      if (seen.get(token) === text) continue
      seen.set(token, text)
      arrivals.get(token)?.()
      // Hidden panels record arrivals without measuring or replaying their backlog.
      if (!animate) continue
      const rect = token.getBoundingClientRect()
      if (
        !canFly() ||
        !onScreen(rect) ||
        !text.trim() ||
        flights.size >= MAX_FLIGHTS
      ) {
        continue
      }
      const style = win.getComputedStyle(token)
      const original = token.style.opacity
      token.style.opacity = '0'
      let cancel: (() => void) | undefined
      cancel = fly(
        text,
        cloudPoint(),
        { x: rect.left, y: rect.top },
        style,
        'down',
        () => {
          token.style.opacity = original
          if (arrivals.get(token) === cancel) arrivals.delete(token)
        }
      )
      if (cancel) arrivals.set(token, cancel)
    }
  }

  const settleOnChange = () => {
    if (!enabled()) clear()
  }
  const pause = new win.MutationObserver(settleOnChange)
  const toggle = cloud.querySelector('[data-cloud-pause]')
  if (toggle) {
    pause.observe(toggle, {
      attributes: true,
      attributeFilter: ['aria-pressed'],
    })
  }
  // Layout changes reveal authoritative text immediately, rather than land at stale coordinates.
  win.addEventListener('resize', clear, { passive: true })
  win.addEventListener('scroll', clear, { passive: true, capture: true })
  doc.addEventListener('visibilitychange', settleOnChange)
  motion.addEventListener('change', settleOnChange)
  return {
    input,
    sentence,
    receive,
    clear,
    clearResponses,
    dispose() {
      disposed = true
      clear()
      pause.disconnect()
      win.removeEventListener('resize', clear)
      win.removeEventListener('scroll', clear, true)
      doc.removeEventListener('visibilitychange', settleOnChange)
      motion.removeEventListener('change', settleOnChange)
      layer.remove()
    },
  }
}

export type L0TextFlow = ReturnType<typeof mountL0TextFlow>
