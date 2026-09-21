/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
const GLYPHS = ['{', '}', '[', ']', '/', ':', ';', '+', '=', '<', '>', '*', '_']
const WORDS = ['const', 'return', 'if', 'await', '=>', '()', '[]', '</>', '...']

/** Stable, decorative tokens only. Never copy prompts or credentials here. */
export function createL0Tokens(width: number) {
  const count = width < 560 ? 92 : 174
  let seed = 731
  const random = () => {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
    return seed / 4294967296
  }
  const clusters = [
    { x: 0.28, y: 0.55, rx: 0.19, ry: 0.31 },
    { x: 0.52, y: 0.39, rx: 0.18, ry: 0.30 },
    { x: 0.73, y: 0.57, rx: 0.16, ry: 0.28 },
  ]
  return Array.from({ length: count }, (_, index) => {
    const cluster = clusters[index % clusters.length]
    const angle = random() * Math.PI * 2
    const radius = Math.sqrt(random())
    const depth = random()
    return {
      x: cluster.x + Math.cos(angle) * radius * cluster.rx,
      y: cluster.y + Math.sin(angle) * radius * cluster.ry,
      depth,
      phase: random() * Math.PI * 2,
      glyph:
        index % 11 === 0
          ? WORDS[Math.floor(random() * WORDS.length)]
          : GLYPHS[Math.floor(random() * GLYPHS.length)],
    }
  })
}

/** One bounded canvas; all listeners and animation frames belong to this mount. */
export function mountL0TokenCloud(root: HTMLElement): () => void {
  const canvas = root.querySelector<HTMLCanvasElement>('canvas')
  const toggle = root.querySelector<HTMLButtonElement>('[data-cloud-pause]')
  const win = root.ownerDocument.defaultView
  if (!canvas || !toggle || !win) return () => {}
  let context: CanvasRenderingContext2D | null = null
  try {
    context = canvas.getContext('2d')
  } catch {
    // The static ASCII fallback remains visible when canvas is unavailable.
  }
  if (!context) return () => {}
  const ctx = context
  const doc = root.ownerDocument
  const reduced = win.matchMedia('(prefers-reduced-motion: reduce)')
  let disposed = false
  let paused = false
  let visible = false
  let frame: number | null = null
  let lastFrame = 0
  let elapsed = 0
  let width = 1
  let height = 1
  let color = ''
  let bounds = canvas.getBoundingClientRect()
  let tokens = createL0Tokens(bounds.width)
  const pointer = { x: 0, y: 0, strength: 0, active: false, pressed: false }

  const canAnimate = () =>
    !disposed && visible && !doc.hidden && !paused && !reduced.matches
  const paint = (delta = 0) => {
    pointer.strength +=
      ((pointer.active ? 1 : 0) - pointer.strength) * Math.min(1, delta / 130)
    ctx.clearRect(0, 0, width, height)
    ctx.fillStyle = color
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    for (const token of tokens) {
      const drift = elapsed * 0.00012
      let x = token.x * width + Math.sin(drift + token.phase) * 9
      let y = token.y * height + Math.cos(drift * 0.8 + token.phase) * 6
      const dx = pointer.x - x
      const dy = pointer.y - y
      const distance = Math.hypot(dx, dy)
      const influence =
        Math.max(0, 1 - distance / 150) * pointer.strength
      const pull = pointer.pressed ? -0.6 : 0.24
      x += dx * influence * pull
      y += dy * influence * pull
      ctx.globalAlpha = Math.min(0.88, 0.16 + token.depth * 0.52 + influence * 0.2)
      ctx.font = `${10 + Math.round(token.depth * 4)}px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`
      ctx.fillText(token.glyph, x, y)
    }
    ctx.globalAlpha = 1
  }
  const tick = (now: number) => {
    frame = null
    if (!canAnimate()) return
    const delta = lastFrame ? now - lastFrame : 34
    if (delta >= 1000 / 30) {
      elapsed += Math.min(delta, 64)
      paint(Math.min(delta, 64))
      lastFrame = now
    }
    frame = win.requestAnimationFrame(tick)
  }
  const sync = () => {
    if (disposed) return
    toggle.disabled = reduced.matches
    toggle.setAttribute('aria-pressed', String(paused || reduced.matches))
    if (canAnimate()) {
      if (frame === null) {
        lastFrame = 0
        frame = win.requestAnimationFrame(tick)
      }
    } else {
      if (frame !== null) win.cancelAnimationFrame(frame)
      frame = null
      lastFrame = 0
      pointer.active = false
      pointer.pressed = false
      pointer.strength = 0
      paint()
    }
  }
  const measure = () => {
    if (disposed) return
    bounds = canvas.getBoundingClientRect()
    width = Math.max(1, bounds.width)
    height = Math.max(1, bounds.height)
    const ratio = Math.min(win.devicePixelRatio || 1, 2)
    canvas.width = Math.round(width * ratio)
    canvas.height = Math.round(height * ratio)
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0)
    color = win.getComputedStyle(canvas).color
    tokens = createL0Tokens(width)
    visible = bounds.bottom > 0 && bounds.top < win.innerHeight && bounds.width > 0
    paint()
    sync()
  }
  const scroll = () => {
    bounds = canvas.getBoundingClientRect()
    visible = bounds.bottom > 0 && bounds.top < win.innerHeight && bounds.width > 0
    sync()
  }
  const move = (event: PointerEvent) => {
    if (event.pointerType === 'touch' || !canAnimate()) return
    pointer.x = event.clientX - bounds.left
    pointer.y = event.clientY - bounds.top
    pointer.active = true
  }
  const enter = (event: PointerEvent) => {
    bounds = canvas.getBoundingClientRect()
    move(event)
  }
  const leave = () => {
    pointer.active = false
    pointer.pressed = false
  }
  const down = (event: PointerEvent) => {
    enter(event)
    if (event.pointerType !== 'touch' && canAnimate()) pointer.pressed = true
  }
  const up = () => {
    pointer.pressed = false
  }
  const pause = () => {
    paused = !paused
    sync()
  }
  const resizeObserver = win.ResizeObserver ? new win.ResizeObserver(measure) : null
  const intersection = win.IntersectionObserver
    ? new win.IntersectionObserver(([entry]) => {
        visible = entry.isIntersecting
        sync()
      })
    : null
  const themeObserver = win.MutationObserver ? new win.MutationObserver(measure) : null

  toggle.addEventListener('click', pause)
  canvas.addEventListener('pointerenter', enter)
  canvas.addEventListener('pointermove', move)
  canvas.addEventListener('pointerdown', down)
  canvas.addEventListener('pointerleave', leave)
  canvas.addEventListener('pointercancel', leave)
  win.addEventListener('pointerup', up)
  win.addEventListener('blur', leave)
  win.addEventListener('resize', measure, { passive: true })
  win.addEventListener('scroll', scroll, { passive: true, capture: true })
  doc.addEventListener('visibilitychange', sync)
  reduced.addEventListener('change', sync)
  resizeObserver?.observe(root)
  intersection?.observe(canvas)
  themeObserver?.observe(doc.documentElement, {
    attributes: true,
    attributeFilter: ['class', 'style', 'data-theme'],
  })
  root.dataset.cloudReady = 'true'
  measure()
  return () => {
    disposed = true
    if (frame !== null) win.cancelAnimationFrame(frame)
    resizeObserver?.disconnect()
    intersection?.disconnect()
    themeObserver?.disconnect()
    toggle.removeEventListener('click', pause)
    canvas.removeEventListener('pointerenter', enter)
    canvas.removeEventListener('pointermove', move)
    canvas.removeEventListener('pointerdown', down)
    canvas.removeEventListener('pointerleave', leave)
    canvas.removeEventListener('pointercancel', leave)
    win.removeEventListener('pointerup', up)
    win.removeEventListener('blur', leave)
    win.removeEventListener('resize', measure)
    win.removeEventListener('scroll', scroll, true)
    doc.removeEventListener('visibilitychange', sync)
    reduced.removeEventListener('change', sync)
    delete root.dataset.cloudReady
  }
}
