/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Shape, TAU, hash, rotate, type Point, type Sculpture, type Vec3 } from './geometry'
import { PORTRAIT_COLORS, PORTRAIT_GRID, PORTRAIT_MATERIALS } from './portrait-materials'

export const PORTRAIT_PERIOD = 8.4
const ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'
const clamp = (n: number) => Math.max(0, Math.min(1, n))
const bell = (x: number, y: number) => Math.exp(-x * x - y * y)
const RIGHT = [0.988, 0.156] as const
const UP = [0.156, -0.988] as const
const SCALE = 500
const EYES = [[659, 534], [843, 568]] as const

type PortraitPoint = Point & {
  relax?: Vec3
  eyeOffset?: number
  headWeight?: number
}

/** Pure, bounded and periodic. No timers or changes to the shared scene clock. */
export function portraitExpressionAt(seconds: number) {
  const t = Math.max(0, Number.isFinite(seconds) ? seconds : 0)
  const phase = (t % PORTRAIT_PERIOD) / PORTRAIT_PERIOD
  const blinkAt = (at: number, width: number) => {
    const distance = Math.abs(phase * PORTRAIT_PERIOD - at) / width
    return distance < 1 ? 0.5 + 0.5 * Math.cos(distance * Math.PI) : 0
  }
  return {
    angle: phase * TAU,
    smile: 0.3 + 0.7 * (0.5 - 0.5 * Math.cos(phase * TAU)),
    blink: Math.max(blinkAt(2.8, 0.16), blinkAt(6.55, 0.14)),
  }
}

/** A single-view reconstruction: sculpted depth, not a plane carrying a photo. */
const depthAt = (x: number, y: number) => {
  const hx = (x - 737) * RIGHT[0] + (y - 596) * RIGHT[1]
  const hy = (x - 737) * UP[0] + (y - 596) * UP[1]
  const shell = Math.sqrt(Math.max(0, 1 - (hx / 265) ** 2)) * Math.sqrt(Math.max(0, 1 - (hy / 440) ** 2))
  const nose = 0.11 * bell(hx / 39, (hy + 5) / 115) + 0.10 * bell(hx / 53, (hy + 55) / 43)
  const cheeks = 0.035 * bell((Math.abs(hx) - 120) / 68, (hy + 75) / 94)
  const head = 0.015 + shell * 0.33 + nose + cheeks
  const chest = 0.035 + 0.17 * Math.sqrt(Math.max(0, 1 - ((x - 704) / 575) ** 2))
  const weight = clamp((960 - y) / 95)
  return chest + (head - chest) * weight
}

export function smilingPortrait(): Sculpture {
  const s = new Shape()
  const add = (x: number, y: number, color: Vec3, depth = 0, part = 0) => {
    s.add([(x - 730) / SCALE, (750 - y) / SCALE, depthAt(x, y) + depth], color, part)
    const p = s.points[s.points.length - 1] as PortraitPoint
    p.headWeight = clamp((963 - y) / 100)
    const mx = (x - 712) * RIGHT[0] + (y - 742) * RIGHT[1]
    const my = (x - 712) * UP[0] + (y - 742) * UP[1]
    const mouth = bell(mx / 118, my / 63)
    const cheek = bell((Math.abs((x - 739) * RIGHT[0] + (y - 661) * RIGHT[1]) - 112) / 72, (y - 666) / 88)
    const dx = -mx / SCALE * 0.13 * mouth
    const dy = (-my / SCALE * 0.31 - 0.032 * clamp(Math.abs(mx) / 85)) * mouth - cheek * 0.015
    p.relax = [dx * RIGHT[0] + dy * UP[0], -dx * RIGHT[1] - dy * UP[1], -cheek * 0.008]
    if (part === 0) {
      for (const [ex, ey] of EYES) {
        const localX = (x - ex) * RIGHT[0] + (y - ey) * RIGHT[1]
        const localY = (x - ex) * UP[0] + (y - ey) * UP[1]
        if ((localX / 59) ** 2 + (localY / 24) ** 2 < 1) p.eyeOffset = localY / SCALE
      }
    }
  }

  // Decode material indices directly into sampled 3D geometry. Index zero is air.
  let cell = 0
  const { width, height } = PORTRAIT_GRID
  for (let i = 0; i < PORTRAIT_MATERIALS.length;) {
    const symbol = PORTRAIT_MATERIALS[i++]
    const count = symbol === '~' ? ALPHABET.indexOf(PORTRAIT_MATERIALS[i++]) + 1 : 1
    const value = ALPHABET.indexOf(symbol === '~' ? PORTRAIT_MATERIALS[i++] : symbol)
    if (value < 0 || value > PORTRAIT_COLORS.length || count < 1 || cell + count > width * height) throw new Error('Invalid portrait materials')
    for (let run = 0; run < count; run++, cell++) {
      if (value === 0) continue
      const x = 160 + ((cell % width) + 0.2 + hash(cell + 11) * 0.6) / width * 1088
      const y = 200 + (Math.floor(cell / width) + 0.2 + hash(cell + 73) * 0.6) / height * 1048
      const color = PORTRAIT_COLORS[value - 1]
      // Raise only dark material values; a black shirt still has a visible contour.
      const light = color[0] * 0.2126 + color[1] * 0.7152 + color[2] * 0.0722
      const lift = Math.max(0, 36 - light) * 0.85
      add(x, y, [color[0] + lift, color[1] + lift, color[2] + lift])
    }
  }
  if (cell !== width * height) throw new Error('Incomplete portrait materials')

  // Skin under the eyelids prevents a blink from exposing the canvas background.
  for (const [ex, ey] of EYES) {
    for (let row = -4; row <= 4; row++) {
      for (let col = -10; col <= 10; col++) {
        const x = col * 5.5, y = row * 5
        if ((x / 57) ** 2 + (y / 22) ** 2 >= 1) continue
        add(ex + x * RIGHT[0] + y * UP[0], ey + x * RIGHT[1] + y * UP[1], [205, 166, 138], -0.018, 1)
      }
    }
  }

  // A neutral rear cap gives the head thickness. Its unseen shape is approximate.
  const back = new Shape()
  back.ellipsoid([0.024, 0.328, -0.075], [0.40, 0.59, 0.23], [49, 42, 43], 2, 800)
  s.points.push(...back.points.filter((p) => p.z < -0.025))

  return s.model((seconds) => {
    const { angle, smile, blink } = portraitExpressionAt(seconds)
    const turn = rotate(1, Math.sin(angle) * 0.045, [0, -0.25, 0])
    const nod = rotate(2, Math.sin(angle * 2) * 0.014, [0, -0.25, 0])
    const breath = Math.sin(angle * 2) * 0.007
    return (point, v) => {
      const p = point as PortraitPoint
      if (p.relax) {
        v[0] += p.relax[0] * (1 - smile)
        v[1] += p.relax[1] * (1 - smile)
        v[2] += p.relax[2] * (1 - smile)
      }
      if (p.eyeOffset !== undefined) {
        v[0] -= p.eyeOffset * UP[0] * blink * 0.97
        v[1] += p.eyeOffset * UP[1] * blink * 0.97
        v[2] += blink * 0.008
      }
      const x = v[0], y = v[1], z = v[2]
      turn(p, v)
      nod(p, v)
      const head = p.headWeight ?? 1
      v[0] = x + (v[0] - x) * head
      v[1] = y + (v[1] - y) * head + breath * clamp((point.y + 1) / 0.6)
      v[2] = z + (v[2] - z) * head
    }
  }, [0, -0.025])
}
