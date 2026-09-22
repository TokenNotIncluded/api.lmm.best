/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
const GLYPHS = ['{', '}', '/', '+', '>', ':', '[]', '01', '()', '*']

export function createL0Tokens(width: number) {
  let seed = 731
  const random = () => {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
    return seed / 4294967296
  }
  return Array.from({ length: width < 560 ? 1000 : 2600 }, (_, index) => ({
    angle: random() * Math.PI * 2,
    cross: random() * Math.PI * 2,
    radius: 0.35 + Math.sqrt(random()) * (index % 9 === 0 ? 1.05 : 0.65),
    phase: random() * Math.PI * 2,
    glyph: index % 10 === 0 ? GLYPHS[Math.floor(random() * GLYPHS.length)] : '',
  }))
}

type Token = ReturnType<typeof createL0Tokens>[number]

/** Three breathing lobes share one continuous, perspective-projected field. */
export function projectL0Token(
  token: Token,
  time: number,
  tilt = 0,
  scene = 0
) {
  const u = token.angle + time * 0.000045
  const v = token.cross + Math.sin(time * 0.0002 + token.phase) * 0.12
  const tube = (0.32 + Math.sin(u * 3 + time * 0.00012) * 0.065) * token.radius
  const ring = 0.87 + Math.cos(u * 3) * 0.065 + Math.cos(v) * tube
  // One particle field reshapes between conversation, discovery and access.
  const progress = Math.max(0, Math.min(2, scene))
  const explore = 1 - Math.abs(progress - 1)
  const access = Math.max(0, progress - 1)
  const chat = 1 - explore - access
  const group = Math.floor((token.phase / (Math.PI * 2)) * 3)
  const spreadX = (group - 1) * 0.69 + Math.cos(u) * Math.cos(v) * 0.35
  const spreadY = (group === 1 ? -0.36 : 0.2) + Math.sin(v) * 0.36
  const linkedX = (token.phase < Math.PI ? -0.36 : 0.36) + Math.cos(u) * 0.48
  const x = Math.cos(u) * ring * chat + spreadX * explore + linkedX * access
  const y =
    Math.sin(u) * ring * chat + spreadY * explore + Math.sin(u) * 0.7 * access
  const z =
    Math.sin(v) * tube * chat +
    Math.sin(u) * 0.3 * explore +
    Math.sin(v) * 0.15 * access
  const a = 0.62 + tilt * 0.15
  const y1 = y * Math.cos(a) - z * Math.sin(a)
  const z1 = y * Math.sin(a) + z * Math.cos(a)
  const x1 = x * 0.97 + z1 * 0.24
  const z2 = z1 * 0.97 - x * 0.24
  const perspective = 3.8 / (3.8 - z2)
  return {
    x: (x1 * 0.97 + y1 * 0.24) * perspective,
    y: (y1 * 0.97 - x1 * 0.24) * perspective,
    depth: Math.max(0, Math.min(1, (z2 + 1) / 2)),
  }
}

/** No media, WebGL or network dependencies. Every resource has one owner. */
export function mountL0TokenCloud(root: HTMLElement): () => void {
  const canvas = root.querySelector<HTMLCanvasElement>('canvas')
  const toggle = root.querySelector<HTMLButtonElement>('[data-cloud-pause]')
  const win = root.ownerDocument.defaultView
  if (!canvas || !toggle || !win) return () => {}
  let context: CanvasRenderingContext2D | null = null
  try {
    context = canvas.getContext('2d')
  } catch {
    // Keep the local SVG fallback visible when canvas is unavailable.
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
  let scene = 0
  let targetScene = 0
  let width = 1
  let height = 1
  let color = ''
  let bounds = canvas.getBoundingClientRect()
  let tokens = createL0Tokens(bounds.width)
  const pointer = { x: 0, y: 0, strength: 0, active: false, pressed: false }
  const canAnimate = () =>
    !disposed && visible && !doc.hidden && !paused && !reduced.matches

  const paint = (delta = 0) => {
    scene += (targetScene - scene) * Math.min(1, delta / 150)
    pointer.strength +=
      ((pointer.active ? 1 : 0) - pointer.strength) * Math.min(1, delta / 180)
    ctx.clearRect(0, 0, width, height)
    ctx.fillStyle = color
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    const scale = Math.min(width * 0.35, height * 0.43)
    for (const token of tokens) {
      const p = projectL0Token(
        token,
        elapsed,
        (pointer.x / width - 0.5) * pointer.strength,
        scene
      )
      let x = width / 2 + p.x * scale
      let y = height / 2 + p.y * scale
      const dx = pointer.x - x
      const dy = pointer.y - y
      const influence = Math.max(0, 1 - Math.hypot(dx, dy) / 105)
      const pull = (pointer.pressed ? -0.75 : 0.18) * pointer.strength
      x += dx * influence * pull
      y += dy * influence * pull
      ctx.globalAlpha = 0.16 + p.depth * p.depth * 0.84
      if (token.glyph) {
        ctx.font = `${8 + p.depth * 3}px ui-monospace, monospace`
        ctx.fillText(token.glyph, x, y)
      } else {
        const size = 0.6 + p.depth * 1.1
        ctx.fillRect(x, y, size, size)
      }
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
    } else if (frame !== null) {
      win.cancelAnimationFrame(frame)
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
    targetScene =
      root.dataset.cloudScene === 'explore'
        ? 1
        : root.dataset.cloudScene === 'access'
          ? 2
          : 0
    if (paused || reduced.matches || doc.hidden) scene = targetScene
    bounds = canvas.getBoundingClientRect()
    width = Math.max(1, bounds.width)
    height = Math.max(1, bounds.height)
    const ratio = Math.min(win.devicePixelRatio || 1, 2)
    canvas.width = Math.round(width * ratio)
    canvas.height = Math.round(height * ratio)
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0)
    color = win.getComputedStyle(canvas).color
    tokens = createL0Tokens(width)
    visible = bounds.bottom > 0 && bounds.top < win.innerHeight
    paint()
    sync()
  }
  const scroll = () => {
    bounds = canvas.getBoundingClientRect()
    visible = bounds.bottom > 0 && bounds.top < win.innerHeight
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
  const resize = win.ResizeObserver ? new win.ResizeObserver(measure) : null
  const intersection = win.IntersectionObserver
    ? new win.IntersectionObserver(([entry]) => {
        visible = entry.isIntersecting
        sync()
      })
    : null
  const theme = win.MutationObserver ? new win.MutationObserver(measure) : null
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
  resize?.observe(root)
  intersection?.observe(canvas)
  theme?.observe(doc.documentElement, {
    attributes: true,
    attributeFilter: ['class', 'style', 'data-theme'],
  })
  theme?.observe(root, {
    attributes: true,
    attributeFilter: ['data-cloud-scene'],
  })
  root.dataset.cloudReady = 'true'
  measure()
  return () => {
    disposed = true
    if (frame !== null) win.cancelAnimationFrame(frame)
    resize?.disconnect()
    intersection?.disconnect()
    theme?.disconnect()
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
