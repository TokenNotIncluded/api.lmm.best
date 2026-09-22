/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import {
  l0FlightFrames,
  L0_ARRIVAL_DURATION,
  L0_MAX_FLIGHTS,
} from './l0-flight-path'
import { getL0CloudAnchor } from './l0-token-cloud'

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

type Point = { x: number; y: number }
type Flight = {
  animation: Animation
  settle: () => void
  direction: 'up' | 'down'
}

/** Disposable visual copies only: no interaction, persistence or network work. */
export function mountL0TextFlow(cloud: HTMLElement) {
  const doc = cloud.ownerDocument
  const win = doc.defaultView
  if (!win) {
    const noop = () => {}
    return {
      input: noop,
      sentence: noop,
      receive: noop,
      clear: noop,
      clearResponses: noop,
      dispose: noop,
    }
  }
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
  const arrivals = new Map<HTMLElement, { cancel?: () => void }>()
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
    const point = getL0CloudAnchor(cloud, sequence++)
    return {
      x: rect.left + point.x * rect.width,
      y: rect.top + point.y * rect.height,
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
    if (!enabled() || flights.size >= L0_MAX_FLIGHTS || !text.trim()) {
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
    let animation: Animation
    try {
      animation = node.animate(
        l0FlightFrames(from, to, direction === 'up', sequence),
        {
          duration:
            direction === 'up'
              ? 620 + (sequence % 3) * 30
              : L0_ARRIVAL_DURATION,
          easing: 'cubic-bezier(.22,.7,.25,1)',
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

  // Small adjacent grapheme packets keep the sentence in place without a swarm.
  // Never join separate lines or reorder RTL spans. Text is still laid out by React.
  const animateTokens = (nodes: HTMLElement[], direction: 'up' | 'down') => {
    const groups: Array<
      Array<{ node: HTMLElement; text: string; rect: DOMRect }>
    > = []
    for (const node of nodes) {
      const rect = node.getBoundingClientRect()
      if (!onScreen(rect) || node.getClientRects().length !== 1) continue
      const text = node.textContent ?? ''
      const last = groups.at(-1)
      const previous = last?.at(-1)
      if (
        last &&
        last.length < 3 &&
        previous &&
        Math.abs(previous.rect.top - rect.top) < 1 &&
        Math.abs(previous.rect.height - rect.height) < 1 &&
        Math.abs(previous.rect.right - rect.left) < 1
      ) {
        last.push({ node, text, rect })
      } else {
        groups.push([{ node, text, rect }])
      }
    }
    for (const group of groups) {
      if (flights.size >= L0_MAX_FLIGHTS) break
      const text = group.map((item) => item.text).join('')
      if (!text.trim()) continue
      const rect = group[0].rect
      const original = group.map(({ node }) => node.style.opacity)
      const local = { x: rect.left, y: rect.top }
      const remote = cloudPoint()
      for (const { node } of group) node.style.opacity = '0'
      const handle: { cancel?: () => void } = {}
      handle.cancel = fly(
        text,
        direction === 'up' ? local : remote,
        direction === 'up' ? remote : local,
        win.getComputedStyle(group[0].node),
        direction,
        () => {
          group.forEach(({ node }, index) => {
            node.style.opacity = original[index]
            if (arrivals.get(node) === handle) arrivals.delete(node)
          })
        }
      )
      if (handle.cancel && direction === 'down') {
        for (const { node } of group) arrivals.set(node, handle)
      }
    }
  }

  const sentence = (source: HTMLElement) => {
    if (canFly()) {
      animateTokens(
        Array.from(source.querySelectorAll<HTMLElement>('[data-l0-source]')),
        'up'
      )
    }
  }

  // Native input remains editable. Only committed inserted text is visualized.
  const input = (source: HTMLInputElement, before = '') => {
    if (!canFly()) return
    const tokens = insertedTokens(before, source.value).slice(
      -L0_MAX_FLIGHTS * 3
    )
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
    for (let index = 0; index < tokens.length; index += 3) {
      const packet = tokens.slice(index, index + 3)
      const first = packet[0]
      const last = packet.at(-1)
      if (!last) continue
      const range = doc.createRange()
      range.setStart(text, first.index)
      range.setEnd(text, last.index + last.text.length)
      const start = range.getBoundingClientRect()
      if (
        start.left < rect.left ||
        start.right > rect.right ||
        !onScreen(start)
      ) {
        continue
      }
      fly(
        packet.map((token) => token.text).join(''),
        { x: start.left, y: start.top },
        cloudPoint(),
        style,
        'up'
      )
    }
    mirror.remove()
  }

  // Mark every real delta, even when hidden or over budget; never replay a backlog.
  const receive = (answer: HTMLElement, animate = true) => {
    const pending: HTMLElement[] = []
    for (const token of answer.querySelectorAll<HTMLElement>(
      '[data-l0-arrival]'
    )) {
      const text = token.textContent ?? ''
      if (seen.get(token) === text) continue
      seen.set(token, text)
      arrivals.get(token)?.cancel?.()
      pending.push(token)
    }
    if (animate && pending.length && canFly()) animateTokens(pending, 'down')
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
