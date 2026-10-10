/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Shape, TAU, hash, rotate, smooth, tint, type Sculpture, type Vec3 } from './geometry'

const BLUE: Vec3 = [76, 151, 200]
const BELLY: Vec3 = [158, 199, 218]

export function blueWhale(): Sculpture {
  const s = new Shape()
  // NPS/NOAA reference: broad, flattened rostrum, small rear dorsal fin,
  // mottled blue-grey skin and horizontal, not fish-like vertical, flukes.
  const body = (u: number, a: number, lift = 0): Vec3 => {
    const radius = u < 0.76
      ? 0.045 + 0.955 * Math.sin(u / 0.76 * Math.PI / 2) ** 0.9
      : Math.sqrt(Math.max(0, 1 - ((u - 0.76) / 0.24) ** 2))
    const head = smooth(Math.max(0, Math.min(1, (u - 0.67) / 0.33)))
    return [
      -0.99 + u * 2.04,
      0.06 + Math.sin(a) * (radius * (0.29 - head * 0.075) + lift),
      Math.cos(a) * (radius * 0.34 + lift),
    ]
  }
  s.surface(128, 80, (u, v) => body(u, v * TAU), (u, v) => {
    const a = v * TAU
    const mottling = Math.sin(u * 48 + Math.sin(a * 5)) * Math.sin(a * 19 - u * 13)
    const shade = 0.66 + 0.27 * Math.max(0, Math.cos(a - 0.6)) + (u < 0.78 ? mottling * 0.065 : 0)
    return tint(Math.sin(a) < -0.35 ? BELLY : BLUE, shade)
  })
  // Representative pleats, sampled on the underside instead of floating lines.
  for (let rib = 0; rib < 20; rib++) {
    const a = Math.PI + 0.3 + rib / 19 * (Math.PI - 0.6)
    s.tube(t => body(0.45 + t * 0.51, a, 0.004), 0.0027, [98, 146, 174], 0, 40, 3)
  }
  for (const side of [-1, 1]) {
    const angle = side > 0 ? -0.18 : Math.PI + 0.18
    s.tube(t => body(0.55 + t * 0.435, angle, 0.006), 0.006, [38, 86, 115], 0, 52, 4)
    const eye = body(0.755, side > 0 ? 0.025 : Math.PI - 0.025, 0.006)
    s.ellipsoid(eye, [0.022, 0.017, 0.009], [20, 44, 57], 0, 140)
    s.ellipsoid([eye[0] + 0.005, eye[1] + 0.006, eye[2] + side * 0.007], [0.006, 0.005, 0.003], BELLY, 0, 35)
    const root = body(0.66, side > 0 ? -0.45 : Math.PI + 0.45)
    s.surface(52, 24, (u, v) => [
      root[0] - u * 0.48 + (v - 0.5) * Math.sin(Math.PI * u) * 0.17,
      root[1] - u * 0.31 + Math.sin(v * Math.PI) * Math.sin(Math.PI * u) * 0.025,
      root[2] + side * u * 0.40,
    ], (_u, v) => tint(BLUE, 0.62 + Math.sin(v * Math.PI) * 0.3), side < 0 ? 2 : 3)
    s.surface(56, 26, (u, v) => {
      const chord = 0.23 * Math.sin(Math.PI * u) ** 0.55 + 0.03 * (1 - u)
      return [
        -0.985 - Math.sin(u * Math.PI / 2) * 0.21 + chord * (v - 0.5),
        0.055 + Math.sin(u * Math.PI) * Math.sin(v * Math.PI) * 0.025,
        side * (0.035 + u * 0.54),
      ]
    }, (_u, v) => tint(BLUE, 0.64 + Math.sin(v * Math.PI) * 0.3), 1)
  }
  // The small dorsal fin grows out of the back, about three-quarters aft.
  s.surface(32, 18, (u, v) => {
    const x = -0.61 + u * 0.25
    const back = body((x + 0.99) / 2.04, Math.PI / 2)
    return [x - v * 0.045, back[1] + Math.sin(u * Math.PI) * v * 0.13, (v - 0.5) * 0.019]
  }, BLUE)
  // Two recessed blowholes and the central ridge stay attached to the head.
  for (const side of [-1, 1]) {
    const top = body(0.78, Math.PI / 2)
    s.ellipsoid([top[0], top[1] + 0.002, side * 0.036], [0.036, 0.008, 0.014], [42, 89, 117], 0, 85)
  }
  s.tube(t => body(0.79 + t * 0.18, Math.PI / 2, 0.004), 0.004, [119, 178, 209], 0, 25, 3)
  for (let i = 0; i < 8; i++) {
    const r = 0.013 + hash(i + 7) * 0.017
    s.ellipsoid([-1.14 + hash(i + 1) * 0.3, -0.3 + hash(i + 2) * 1.1, 0.12 + hash(i + 4) * 0.26], [r, r, r], BELLY, 10 + i, 70)
  }
  return s.model((t) => {
    const roll = rotate(0, Math.sin(t * 0.7) * 0.06)
    const leftRoot = body(0.66, Math.PI + 0.45)
    const rightRoot = body(0.66, -0.45)
    const left = rotate(0, Math.sin(t * 1.9) * 0.14, leftRoot)
    const right = rotate(0, -Math.sin(t * 1.9) * 0.14, rightRoot)
    return (p, v) => {
      if (p.part >= 10) {
        v[1] = -0.45 + ((p.y + 0.45 + t * 0.19) % 1.55)
        v[0] += Math.sin(t + p.part) * 0.025
        return
      }
      if (p.part === 2) left(p, v)
      if (p.part === 3) right(p, v)
      const tail = Math.max(0, (0.58 - p.x) / 1.84)
      v[1] += Math.sin(t * 2.15 + p.x * 2.5) * tail * tail * 0.16
      roll(p, v)
      v[0] += Math.sin(t * 0.6) * 0.065
      v[1] += Math.sin(t * 0.85) * 0.045
    }
  }, [-0.12, -0.24])
}
