/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Shape, TAU, hash, rotate, tint, type Sculpture, type Vec3 } from './geometry'

const BLUE: Vec3 = [62, 155, 225]
const BELLY: Vec3 = [158, 205, 229]

export function blueWhale(): Sculpture {
  const s = new Shape()
  // A broad, blunt head tapers into the narrow tail stock. The head faces right.
  const body = (u: number, a: number, lift = 0): Vec3 => {
    const r = Math.sin(Math.PI * u) ** 0.52 * (0.18 + 0.82 * u)
    return [-0.99 + u * 2.04, 0.06 + Math.sin(a) * (r * 0.45 + lift), Math.cos(a) * (r * 0.36 + lift)]
  }
  s.surface(150, 96, (u, v) => body(u, v * TAU), (u, v) => {
    const a = v * TAU
    const underside = Math.sin(a) < -0.35
    return tint(underside ? BELLY : BLUE, 0.66 + 0.27 * Math.max(0, Math.cos(a - 0.6)) + hash(u * 177 + v * 47) * 0.06)
  })
  // Throat pleats and a gently curved mouth remain part of the body when swimming.
  for (let rib = 0; rib < 10; rib++) {
    const a = Math.PI + 0.2 + rib * 0.27
    s.tube(t => body(0.47 + t * 0.45, a, 0.005), 0.0035, [93, 145, 177], 0, 52, 3)
  }
  for (const side of [-1, 1]) {
    s.tube(t => [0.28 + t * 0.69, -0.115 + t * t * 0.16, side * (0.29 * (1 - t * t) + 0.012)], 0.009, [32, 89, 127], 0, 65, 5)
    s.ellipsoid([0.63, 0.055, side * 0.283], [0.038, 0.029, 0.015], [16, 39, 54], 0, 200)
    s.ellipsoid([0.64, 0.064, side * 0.294], [0.009, 0.009, 0.006], BELLY, 0, 65)
    // Paired long pectoral fins, not fish-like vertical tail fins.
    s.surface(65, 25, (u, v) => [
      0.37 - u * 0.49 + (v - 0.5) * Math.sin(Math.PI * u) * 0.23,
      -0.11 - u * 0.46 + Math.sin(v * Math.PI) * 0.03,
      side * (0.19 + u * 0.51),
    ], (u, v) => tint(BLUE, 0.62 + Math.sin(v * Math.PI) * 0.29 + u * 0.06), side < 0 ? 2 : 3)
    // Two horizontal flukes with a central notch.
    s.surface(65, 32, (u, v) => [
      -0.91 - u * 0.35 + Math.sin(v * Math.PI) * u * 0.18,
      0.055 + Math.sin(u * Math.PI) * 0.045,
      side * (0.045 + u * 0.49) * (0.22 + v * 0.78),
    ], (_u, v) => tint(BLUE, 0.64 + Math.sin(v * Math.PI) * 0.3), 1)
  }
  s.surface(35, 18, (u, v) => [-0.58 + u * 0.31, 0.24 + Math.sin(u * Math.PI) * v * 0.19, (v - 0.5) * 0.032], BLUE)
  // A few rising bubbles reinforce the underwater scene without a water box.
  for (let i = 0; i < 9; i++) {
    const r = 0.014 + hash(i + 7) * 0.018
    s.ellipsoid([-1.16 + hash(i + 1) * 0.32, -0.3 + hash(i + 2) * 1.1, 0.12 + hash(i + 4) * 0.26], [r, r, r], BELLY, 10 + i, 90)
  }
  return s.model((t) => {
    const roll = rotate(0, Math.sin(t * 0.7) * 0.07)
    const left = rotate(0, Math.sin(t * 1.9) * 0.18, [0.37, -0.11, -0.19])
    const right = rotate(0, -Math.sin(t * 1.9) * 0.18, [0.37, -0.11, 0.19])
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
      v[0] += Math.sin(t * 0.6) * 0.075
      v[1] += Math.sin(t * 0.85) * 0.055
    }
  }, [-0.12, -0.24])
}
