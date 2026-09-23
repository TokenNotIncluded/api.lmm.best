/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */

/** A small decorative field, drawn by the homepage's existing motion loop. */
const TOKENS = [
  ['</>', 0.07, 0.13],
  ['token', 0.27, 0.075],
  ['{}', 0.64, 0.1],
  ['01', 0.94, 0.22],
  ['[]', 0.045, 0.46],
  ['λ', 0.95, 0.69],
  ['context', 0.16, 0.9],
  ['↗', 0.48, 0.93],
  ['()', 0.76, 0.91],
  ['+', 0.09, 0.72],
  ['<>', 0.45, 0.13],
  ['attention', 0.8, 0.12],
  ['∑', 0.97, 0.43],
  ['{ }', 0.4, 0.83],
  ['next', 0.59, 0.8],
  ['::', 0.19, 0.2],
  ['embed', 0.04, 0.29],
  ['[ ]', 0.89, 0.85],
  ['·', 0.55, 0.25],
  ['01', 0.28, 0.8],
  ['()', 0.035, 0.88],
  ['*', 0.6, 0.04],
] as const

type Cursor = { x: number; y: number } | null
type Box = { left: number; top: number; right: number; bottom: number }

export function tokenRepulsion(
  x: number,
  y: number,
  cursor: Cursor,
  phase = 0
) {
  if (!cursor || ![x, y, cursor.x, cursor.y].every(Number.isFinite)) {
    return { x: 0, y: 0, strength: 0 }
  }
  const dx = x - cursor.x
  const dy = y - cursor.y
  const distance = Math.hypot(dx, dy)
  const strength = Math.max(0, 1 - distance / 145) ** 2
  const angle = distance < 1 ? phase : Math.atan2(dy, dx)
  return {
    x: Math.cos(angle) * strength * 24,
    y: Math.sin(angle) * strength * 24,
    strength,
  }
}

export function createTokenCloud(layer: HTMLElement | null) {
  if (!layer) return null
  const particles = TOKENS.map(([text, x, y], index) => {
    const node = document.createElement('span')
    node.textContent = text
    node.dataset.tokenParticle = ''
    node.dataset.tint =
      index % 5 === 0
        ? 'pink'
        : index % 3 === 0
          ? 'cyan'
          : index % 4 === 1
            ? 'blue'
            : 'ink'
    const depth = 0.6 + ((index * 7) % 9) / 15
    node.style.fontSize = `${11 + depth * 5}px`
    layer.append(node)
    return {
      node,
      text,
      x,
      y,
      depth,
      phase: index * 2.39996,
      dx: 0,
      dy: 0,
      spin: 0,
    }
  })
  let width = 1
  let height = 1
  let exclusions: Box[] = []
  let active = true

  return {
    measure(frame: DOMRect, protectedElements: HTMLElement[]) {
      width = Math.max(1, frame.width)
      height = Math.max(1, frame.height)
      exclusions = protectedElements
        .filter((element) => element.getAttribute('aria-hidden') !== 'true')
        .map((element) => {
          const box = element.getBoundingClientRect()
          return {
            left: box.left - frame.left - 22,
            top: box.top - frame.top - 18,
            right: box.right - frame.left + 22,
            bottom: box.bottom - frame.top + 18,
          }
        })
    },
    draw(time: number, cursor: Cursor, moving: boolean) {
      if (!active) return
      const count = width < 680 ? 10 : particles.length
      for (const [index, particle] of particles.entries()) {
        if (index >= count) {
          particle.node.style.opacity = '0'
          continue
        }
        const clock = moving ? time : 0
        const x =
          particle.x * width + Math.sin(clock * 0.24 + particle.phase) * 6
        const y =
          particle.y * height + Math.cos(clock * 0.2 + particle.phase) * 5
        const force = tokenRepulsion(
          x,
          y,
          moving ? cursor : null,
          particle.phase
        )
        if (moving) {
          particle.dx += (force.x * particle.depth - particle.dx) * 0.13
          particle.dy += (force.y * particle.depth - particle.dy) * 0.13
          particle.spin +=
            (force.strength * 12 * (index % 2 ? 1 : -1) - particle.spin) * 0.1
        } else {
          particle.dx = particle.dy = particle.spin = 0
        }
        const px = x + particle.dx
        const py = y + particle.dy
        const padding = particle.text.length * 5 + 8
        const obscuresText = exclusions.some(
          (box) =>
            px + padding > box.left &&
            px - padding < box.right &&
            py + 12 > box.top &&
            py - 12 < box.bottom
        )
        particle.node.style.opacity = obscuresText
          ? '0'
          : String(0.13 + particle.depth * 0.1 + force.strength * 0.12)
        particle.node.style.transform = `translate3d(${px.toFixed(2)}px,${py.toFixed(2)}px,0) translate(-50%,-50%) rotate(${(Math.sin(particle.phase) * 9 + particle.spin).toFixed(2)}deg)`
      }
    },
    dispose() {
      active = false
      for (const particle of particles) particle.node.remove()
    },
  }
}
