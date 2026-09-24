/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */

/** The first ten background tokens can be picked up and tried in the input. */
const TOKENS = [
  ['</>', 0.07, 0.13],
  ['token', 0.27, 0.075],
  ['{}', 0.64, 0.1],
  ['42', 0.94, 0.22],
  ['ai', 0.045, 0.46],
  ['λ', 0.95, 0.69],
  ['context', 0.16, 0.9],
  ['gpt', 0.48, 0.93],
  ['api', 0.76, 0.91],
  ['∑', 0.09, 0.72],
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

export function createTokenCloud(
  layer: HTMLElement | null,
  onSelect?: (token: string) => void
) {
  if (!layer) return null
  const particles = TOKENS.map(([text, x, y], index) => {
    const interactive = index < 10
    const node = document.createElement(interactive ? 'button' : 'span')
    node.textContent = text
    node.dataset.tokenParticle = ''
    if (interactive) {
      const button = node as HTMLButtonElement
      button.type = 'button'
      button.draggable = true
      button.dataset.tokenOption = text
      button.addEventListener('click', () => onSelect?.(text))
      button.addEventListener('dragstart', (event) => {
        event.dataTransfer?.setData('text/plain', text)
        if (event.dataTransfer) event.dataTransfer.effectAllowed = 'copy'
      })
    } else node.setAttribute('aria-hidden', 'true')
    node.dataset.tint =
      index % 5 === 0
        ? 'feedback'
        : index % 3 === 0
          ? 'highlight'
          : index % 4 === 1
            ? 'forward'
            : 'ink'
    const depth = 0.6 + ((index * 7) % 9) / 15
    node.style.fontSize = `${11 + depth * 5}px`
    layer.append(node)
    return {
      node,
      text,
      interactive,
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
      const local = layer.getBoundingClientRect()
      const bounds = local.width > 0 && local.height > 0 ? local : frame
      width = Math.max(1, bounds.width)
      height = Math.max(1, bounds.height)
      exclusions = protectedElements
        .filter((element) => element.getAttribute('aria-hidden') !== 'true')
        .map((element) => {
          const box = element.getBoundingClientRect()
          return {
            left: box.left - bounds.left - 22,
            top: box.top - bounds.top - 18,
            right: box.right - bounds.left + 22,
            bottom: box.bottom - bounds.top + 18,
          }
        })
    },
    draw(time: number, cursor: Cursor, moving: boolean) {
      if (!active) return
      const count = width < 680 ? 10 : particles.length
      for (const [index, particle] of particles.entries()) {
        if (index >= count) {
          particle.node.style.opacity = '0'
          particle.node.inert = true
          continue
        }
        const x = particle.x * width
        const y = particle.y * height
        const force = particle.interactive
          ? { x: 0, y: 0, strength: 0 }
          : tokenRepulsion(x, y, moving ? cursor : null, particle.phase)
        if (moving) {
          particle.dx += (force.x * particle.depth - particle.dx) * 0.13
          particle.dy += (force.y * particle.depth - particle.dy) * 0.13
          particle.spin +=
            (force.strength * 12 * (index % 2 ? 1 : -1) - particle.spin) * 0.1
        } else {
          particle.dx = particle.dy = particle.spin = 0
        }
        const driftX = moving
          ? Math.sin(time * 0.52 + particle.phase) * (2.5 + particle.depth * 4.5)
          : 0
        const driftY = moving
          ? Math.cos(time * 0.39 + particle.phase * 1.17) *
            (1.8 + particle.depth * 3.2)
          : 0
        const px = x + particle.dx + driftX
        const py = y + particle.dy + driftY
        const padding = particle.text.length * 5 + 8
        const obscuresText =
          exclusions.some(
            (box) =>
              px + padding > box.left &&
              px - padding < box.right &&
              py + 12 > box.top &&
              py - 12 < box.bottom
          ) && document.activeElement !== particle.node
        particle.node.inert = obscuresText
        particle.node.style.opacity = obscuresText
          ? '0'
          : particle.interactive
            ? '0.88'
            : String(0.18 + particle.depth * 0.15 + force.strength * 0.16)
        particle.node.style.transform = `translate3d(${px.toFixed(2)}px,${py.toFixed(2)}px,0) translate(-50%,-50%) rotate(${particle.spin.toFixed(2)}deg)`
      }
    },
    dispose() {
      active = false
      for (const particle of particles) particle.node.remove()
    },
  }
}
