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
  // A broad volume, not thousands of points compressed onto a thin torus.
  return Array.from({ length: width < 560 ? 320 : 780 }, (_, index) => ({
    angle: index * 2.39996323 + random() * 0.35,
    cross: Math.acos(random() * 2 - 1),
    radius: (0.24 + Math.cbrt(random()) * 0.76) * (index % 11 === 0 ? 1.18 : 1),
    phase: random() * Math.PI * 2,
    group: index % 3,
    softness: 0.55 + random() * 0.45,
    glyph: index % 8 === 0 ? GLYPHS[Math.floor(random() * GLYPHS.length)] : '',
  }))
}

type Token = ReturnType<typeof createL0Tokens>[number]
type Point = { x: number; y: number }
const anchors = new WeakMap<HTMLElement, Point[]>()

/** Normalized positions from the last painted cloud, shared with text flights. */
export function getL0CloudAnchor(root: HTMLElement, sequence: number): Point {
  const points = anchors.get(root)
  return points?.[sequence % points.length] ?? { x: 0.5, y: 0.6 }
}

/** Soft overlapping lobes retain depth while morphing into separate scenes. */
export function projectL0Token(
  token: Token,
  time: number,
  tilt = 0,
  scene = 0
) {
  const u = token.angle + time * 0.000018
  const v = token.cross
  const breath = 1 + Math.sin(time * 0.00025 + token.phase) * 0.055
  const radius = token.radius * breath
  const sx = Math.cos(u) * Math.sin(v) * radius
  const sy = Math.cos(v) * radius
  const sz = Math.sin(u) * Math.sin(v) * radius
  const group = token.group
  const progress = Math.max(0, Math.min(2, scene))
  const explore = 1 - Math.abs(progress - 1)
  const access = Math.max(0, progress - 1)
  const chat = 1 - explore - access
  const x =
    ((group - 1) * 0.7 + sx * 0.64) * chat +
    ((group - 1) * 0.96 + sx * 0.38) * explore +
    ((group === 0 ? -0.44 : 0.44) + Math.cos(u) * 0.52 + sx * 0.12) * access
  const y =
    ((group === 1 ? -0.12 : 0.12) + sy * 0.54) * chat +
    ((group === 1 ? -0.3 : 0.18) + sy * 0.38) * explore +
    (Math.sin(u) * 0.57 + sy * 0.16) * access
  const z = sz * (0.46 * chat + 0.35 * explore + 0.27 * access)
  const a = 0.2 + tilt * 0.12
  const y1 = y * Math.cos(a) - z * Math.sin(a)
  const z1 = y * Math.sin(a) + z * Math.cos(a)
  const perspective = 4.8 / (4.8 - z1)
  return {
    x: (x + z1 * 0.12) * perspective,
    y: y1 * perspective,
    depth: Math.max(0, Math.min(1, (z1 + 0.7) / 1.4)),
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
  const opacityScale = root.dataset.cloudContrast === 'strong' ? 1.8 : 1
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
    const scale = Math.min(width * 0.3, height * 0.67)
    const landingPoints: Point[] = []
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
      const isGlyph = token.glyph !== ''
      // Glyphs read as the foreground "content" layer; plain dots recede
      // as ambient dust so depth carries typographic weight, not just size.
      ctx.globalAlpha = Math.min(
        1,
        (0.12 + p.depth * p.depth * 0.72) *
          token.softness *
          opacityScale *
          (isGlyph ? 1 : 0.6)
      )
      if (isGlyph && p.y > -0.05 && landingPoints.length < 24) {
        landingPoints.push({ x: x / width, y: y / height })
      }
      if (isGlyph) {
        ctx.font = `${10 + Math.round(p.depth * 4)}px ui-monospace, monospace`
        ctx.fillText(token.glyph, x, y)
      } else {
        const size = 0.7 + p.depth * 0.95
        ctx.fillRect(x, y, size, size)
      }
    }
    anchors.set(root, landingPoints)
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
    anchors.delete(root)
    delete root.dataset.cloudReady
  }
}
