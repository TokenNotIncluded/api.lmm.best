/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Shape, TAU, hash, palette as C, rotate, smooth, tint, type Sculpture, type Vec3 } from './geometry'

export function spacexRocket(): Sculpture {
  const s = new Shape()
  const hull: Vec3 = [223, 231, 239]
  s.surface(95, 50, (u, v) => [Math.cos(v * TAU) * 0.12, -0.58 + u * 0.87, Math.sin(v * TAU) * 0.12], (u, v) => tint(u > 0.31 && u < 0.43 ? C.ink : hull, 0.62 + Math.max(0, Math.sin(v * TAU)) * 0.38), 1)
  s.surface(42, 44, (u, v) => {
    const r = 0.14 * Math.cos(u * Math.PI / 2)
    return [Math.cos(v * TAU) * r, 0.29 + u * 0.3, Math.sin(v * TAU) * r]
  }, (_u, v) => tint(hull, 0.64 + Math.max(0, Math.sin(v * TAU)) * 0.36), 1)
  for (const y of [-0.53, -0.29, 0.26]) s.ring([0, y, 0], 0.122, 0.009, C.ink, 1, 1)
  for (const side of [-1, 1]) {
    s.surface(22, 15, (u, v) => [side * (0.105 + u * 0.095), -0.5 + u * 0.16 + v * (1 - u) * 0.11, (v - 0.5) * 0.028], C.ink, 1)
  }
  // A compact SpaceX wordmark drawn as strokes along the fuselage.
  const letters = [
    [[1,0],[0,0],[0,1],[1,1],[1,2],[0,2]],
    [[0,2],[0,0],[1,0],[1,1],[0,1]],
    [[0,2],[0.5,0],[1,2],[0.75,1],[0.25,1]],
    [[1,0],[0,0],[0,2],[1,2]],
    [[1,0],[0,0],[0,1],[0.8,1],[0,1],[0,2],[1,2]],
    [[0,0],[1,2],[0.5,1],[1,0],[0,2]],
  ]
  for (const [index, letter] of letters.entries()) {
    for (let i = 1; i < letter.length; i++) {
      const point = ([x, y]: number[]): Vec3 => [(y - 1) * 0.025, 0.16 - (index * 1.5 + x) * 0.048, 0.124]
      s.line(point(letter[i - 1]), point(letter[i]), 0.003, C.ink, 1, 6)
    }
  }
  s.surface(45, 32, (u, v) => {
    const r = (1 - u) * (0.065 + u * 0.07)
    return [Math.cos(v * TAU) * r, -0.59 - u * 0.44, Math.sin(v * TAU) * r]
  }, u => u < 0.22 ? hull : u < 0.62 ? C.gold : C.rust, 2)
  s.box([0, -0.94, 0], [0.77, 0.07, 0.56], [90, 108, 125], 0)
  s.line([-0.38, -0.91, -0.15], [-0.38, 0.28, -0.15], 0.038, [109, 131, 151])
  for (let i = 0; i < 11; i++) {
    const side = i % 2 ? 1 : -1, r = 0.09 + hash(i + 10) * 0.1
    s.ellipsoid([side * (0.13 + hash(i) * 0.39), -0.88 + hash(i + 3) * 0.11, hash(i + 7) * 0.42], [r * 1.4, r, r], [177, 191, 204], 10 + i, 350)
  }
  return s.model(t => {
    const phase = ((t % 12) + 12) % 12
    const lift = smooth(Math.min(1, phase / 6)) * 0.5
    return (p, v) => {
      if (p.part === 1 || p.part === 2) {
        v[1] += lift
        if (p.part === 2) {
          const length = (-0.59 - p.y) / 0.44
          v[0] += Math.sin(t * 23 + p.y * 24) * length * 0.012
          v[1] -= length * (0.045 + Math.sin(t * 19) * 0.028)
        }
      } else if (p.part >= 10) {
        const expansion = 1 + Math.sin(t * 1.2 + p.part) * 0.15
        v[0] *= expansion
        v[1] += Math.sin(t * 1.6 + p.part) * 0.018
      }
    }
  }, [0.16, -0.08])
}

/** A stylized fireball, rising mushroom cloud and expanding shock ring. */
export function atomicExplosion(): Sculpture {
  const s = new Shape()
  s.ellipsoid([0, -0.68, 0], [0.27, 0.24, 0.27], C.gold, 1, 2500)
  s.surface(85, 45, (u, v) => {
    const a = v * TAU, r = 0.15 + Math.sin(u * Math.PI) * 0.05 + u ** 3 * 0.23
    return [Math.cos(a) * r, -0.73 + u * 1.26, Math.sin(a) * r]
  }, (u, v) => tint(u < 0.5 ? C.rust : C.gold, 0.66 + Math.sin(v * TAU) ** 2 * 0.28), 2)
  s.ellipsoid([0, 0.6, 0], [0.78, 0.32, 0.7], [222, 139, 78], 3, 7000)
  for (let i = 0; i < 18; i++) {
    const a = i / 18 * TAU, r = 0.14 + hash(i + 300) * 0.075
    s.ellipsoid([Math.cos(a) * 0.59, 0.49 + Math.sin(a * 3) * 0.075, Math.sin(a) * 0.53], [r * 1.2, r, r], i % 3 ? [196, 157, 120] : C.gold, 3, 700)
  }
  s.ring([0, -0.8, 0], 0.83, 0.022, C.gold, 4, 1)
  for (let i = 0; i < 160; i++) {
    const a = hash(i + 1) * TAU, r = 0.5 + hash(i + 2) * 0.3
    s.add([Math.cos(a) * r, -0.72 + hash(i + 3) * 0.12, Math.sin(a) * r], C.rust, 5)
  }
  return s.model(t => {
    const phase = ((t % 12) + 12) % 12
    const bloom = smooth(Math.min(1, phase / 3.4))
    const rise = smooth(Math.min(1, phase / 5.2))
    const swirl = rotate(1, t * 0.1)
    return (p, v) => {
      if (p.part === 1) {
        const size = 0.8 + Math.sin(t * 1.7) * 0.12
        v[0] *= size; v[2] *= size
      } else if (p.part === 2 || p.part === 3) {
        v[0] *= 0.22 + bloom * 0.78
        v[2] *= 0.22 + bloom * 0.78
        v[1] = -0.68 + (p.y + 0.68) * (0.12 + rise * 0.88)
        v[0] += Math.sin(p.y * 7 + t * 1.3) * 0.02 * bloom
        swirl(p, v)
      } else {
        const expansion = 0.2 + 0.8 * ((phase % 4) / 4)
        v[0] *= expansion; v[2] *= expansion
      }
    }
  }, [0.13, -0.16])
}

export function rotatingChair(): Sculpture {
  const s = new Shape()
  const wood: Vec3 = [175, 112, 71]
  const fabric: Vec3 = [97, 153, 177]
  s.box([0, -0.13, 0], [0.92, 0.13, 0.79], wood)
  s.ellipsoid([0, -0.02, 0.03], [0.45, 0.12, 0.39], fabric, 0, 4600)
  s.box([0, 0.45, -0.36], [0.98, 0.86, 0.11], wood)
  s.ellipsoid([0, 0.45, -0.26], [0.435, 0.39, 0.12], fabric, 0, 4700)
  for (const x of [-0.38, 0.38]) {
    for (const z of [-0.29, 0.29]) s.line([x, -0.15, z], [x * 1.2, -0.94, z * 1.26], 0.04, wood)
    s.line([x * 1.27, 0.27, -0.32], [x * 1.27, 0.27, 0.36], 0.045, wood)
    s.line([x * 1.27, -0.14, 0.27], [x * 1.27, 0.27, 0.27], 0.025, wood)
  }
  for (const x of [-0.19, 0.19]) s.ellipsoid([x, 0.47, -0.137], [0.022, 0.022, 0.012], tint(fabric, 0.58), 0, 120)
  return s.model(t => rotate(1, t * 0.58), [0.2, -0.2])
}
