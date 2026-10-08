/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Shape, TAU, hash, palette as C, rotate, tint, type Sculpture, type Vec3 } from './geometry'

export function pelicanBicycle(): Sculpture {
  const s = new Shape(),
    rear: Vec3 = [-0.72, -0.76, 0],
    front: Vec3 = [0.75, -0.76, 0]
  for (const [index, center] of [rear, front].entries()) {
    s.ring(center, 0.38, 0.037, C.teal, index + 1)
    s.ring(center, 0.32, 0.011, C.gold, index + 1)
    for (let spoke = 0; spoke < 12; spoke++) {
      const a = spoke / 12 * TAU
      s.line(
        center,
        [center[0] + Math.cos(a) * 0.32, center[1] + Math.sin(a) * 0.32, 0],
        0.008,
        C.cream,
        index + 1,
        20
      )
    }
  }
  const crank: Vec3 = [-0.02, -0.65, 0.08],
    seat: Vec3 = [-0.34, -0.18, 0],
    stem: Vec3 = [0.52, -0.14, 0]
  for (const [a, b] of [
    [rear, seat],
    [seat, crank],
    [crank, rear],
    [seat, stem],
    [stem, crank],
    [stem, front],
  ] as [Vec3, Vec3][]) s.line(a, b, 0.025, C.rust)
  s.line(stem, [0.45, 0.05, 0], 0.021, C.gold)
  s.line([0.36, 0.05, 0], [0.57, 0.05, 0], 0.025, C.ink)
  s.box([-0.33, -0.12, 0], [0.31, 0.065, 0.16], C.ink)
  s.ring(crank, 0.105, 0.018, C.gold, 3)
  s.line([-0.15, -0.65, 0.1], [0.11, -0.65, 0.1], 0.014, C.cream, 3)
  s.ellipsoid([-0.18, 0.28, 0.06], [0.42, 0.39, 0.26], C.cream, 4, 4700)
  for (let feather = 0; feather < 7; feather++) s.ellipsoid(
    [-0.4 + feather * 0.067, 0.3 - feather * 0.037, 0.26],
    [0.09, 0.21, 0.065],
    tint(C.teal, 0.7 + feather * 0.035),
    4,
    340
  )
  s.tube(
    t => [0.14 + Math.sin(t * 3) * 0.06, 0.42 + t * 0.49, 0.025],
    0.091,
    C.cream,
    4,
    65,
    16
  )
  s.ellipsoid([0.21, 0.93, 0.035], [0.17, 0.17, 0.14], C.cream, 4, 1600)
  s.surface(
    90,
    23,
    (u, v) => [
      0.29 + u * 0.78,
      0.93 - u * 0.085 - Math.sin(v * Math.PI) * (1 - u) * 0.17,
      0.036 + (v - 0.5) * (1 - u) * 0.17,
    ],
    C.gold,
    4
  )
  s.line([0.3, 0.94, 0.08], [1.07, 0.845, 0.037], 0.012, C.rust, 4, 60)
  s.ellipsoid([0.24, 0.98, 0.175], [0.035, 0.037, 0.024], C.ink, 4, 200)
  s.line([-0.12, 0.01, 0.21], [-0.22, -0.28, 0.21], 0.022, C.gold, 5, 30)
  s.line([-0.22, -0.28, 0.21], [-0.02, -0.65, 0.2], 0.021, C.gold, 5, 32)
  s.line([-0.19, 0.01, -0.14], [0.04, -0.29, -0.14], 0.021, C.gold, 6, 30)
  s.line([0.04, -0.29, -0.14], [-0.02, -0.65, -0.14], 0.021, C.gold, 6, 32)
  s.ellipsoid([-0.02, -0.65, -0.14], [0.11, 0.027, 0.045], C.gold, 6, 260)
  s.ellipsoid([-0.02, -0.65, 0.21], [0.11, 0.027, 0.045], C.gold, 5, 260)
  return s.model(
    t => {
      const wheels = [rotate(2, -t * 2.5, rear), rotate(2, -t * 2.5, front)],
        pedal = rotate(2, -t * 3, crank)
      return (p, v) => {
        if (p.part === 1 || p.part === 2) wheels[p.part - 1](p, v)
        if (p.part === 3) pedal(p, v)
        if (p.part === 4) v[1] += Math.sin(t * 6) * 0.018
        if (p.part === 5 || p.part === 6) {
          const weight = Math.max(0, (0.01 - p.y) / 0.66)
          const phase = -t * 3 + (p.part === 6 ? Math.PI : 0)
          v[0] += Math.sin(phase) * 0.13 * weight
          v[1] += Math.cos(phase) * 0.13 * weight
        }
      }
    },
    [-0.06, 0.04]
  )
}

export function emperorBear(): Sculpture {
  const s = new Shape()
  s.ellipsoid([-0.11, -0.41, 0], [0.8, 0.38, 0.33], C.cream, 0, 6000)
  for (let leg = 0; leg < 4; leg++) {
    const x = leg < 2 ? -0.56 : 0.47,
      z = leg % 2 ? 0.23 : -0.23
    s.ellipsoid([x, -0.77, z], [0.13, 0.3, 0.13], C.cream, leg + 1, 850)
    s.ellipsoid([x + 0.07, -1, z], [0.2, 0.09, 0.15], C.cream, leg + 1, 650)
  }
  s.ellipsoid([0.76, -0.25, 0], [0.34, 0.28, 0.27], C.cream, 0, 2600)
  s.ellipsoid([1.04, -0.32, 0.05], [0.25, 0.15, 0.2], C.cream, 0, 1600)
  s.ellipsoid([1.24, -0.28, 0.065], [0.065, 0.064, 0.105], C.ink, 0, 400)
  for (const z of [-0.18, 0.18]) {
    s.ellipsoid([0.65, 0.01, z], [0.09, 0.1, 0.065], C.cream, 0, 420)
    s.ellipsoid([0.87, -0.15, z * 1.35], [0.026, 0.028, 0.018], C.ink, 0, 130)
  }
  s.ellipsoid([-0.91, -0.39, 0], [0.12, 0.11, 0.11], C.cream, 0, 400)
  s.surface(
    85,
    48,
    (u, v) => {
      const a = u * TAU,
        r = 0.32 - v * 0.18
      return [-0.22 + Math.cos(a) * r, -0.05 + v * 0.72, Math.sin(a) * r * 0.8]
    },
    (u, v) => v < 0.07 || Math.abs(u - 0.25) < 0.025 ? C.gold : C.ink,
    5
  )
  s.line([-0.24, 0.6, 0.16], [-0.05, 0.31, 0.25], 0.019, C.gold, 5)
  s.line([-0.05, 0.31, 0.25], [-0.3, 0.18, 0.25], 0.019, C.gold, 5)
  s.ellipsoid([-0.22, 0.82, 0], [0.15, 0.18, 0.14], C.cream, 5, 1500)
  s.ellipsoid([-0.22, 0.86, -0.08], [0.16, 0.19, 0.1], C.ink, 5, 800)
  s.ellipsoid([-0.22, 0.65, 0.115], [0.064, 0.1, 0.045], C.ink, 5, 400)
  for (const x of [-0.27, -0.17]) s.ellipsoid([x, 0.85, 0.127], [0.018, 0.013, 0.012], C.ink, 5, 80)
  s.box([-0.22, 1.02, 0], [0.33, 0.09, 0.22], C.gold, 5)
  s.box([-0.22, 1.085, 0], [0.53, 0.055, 0.38], C.ink, 5)
  for (let tassel = 0; tassel < 9; tassel++) {
    const x = -0.44 + tassel * 0.055
    s.line([x, 1.075, 0.195], [x, 0.91 - tassel % 2 * 0.07, 0.195], 0.005, C.gold, 6, 16)
    s.ellipsoid([x, 0.91 - tassel % 2 * 0.07, 0.195], [0.013, 0.018, 0.012], C.gold, 6, 65)
  }
  s.line([0.01, 0.45, 0.1], [0.34, 0.16, 0.16], 0.065, C.ink, 5)
  s.line([0.34, 0.16, 0.16], [0.93, -0.18, 0.2], 0.01, C.gold)
  // Bent knees straddle the bear; the rider sits instead of standing on its back.
  for (const side of [-1, 1]) {
    s.line([-0.2, 0.05, side * 0.18], [0.05, -0.16, side * 0.38], 0.09, C.ink, 5)
    s.line([0.05, -0.16, side * 0.38], [-0.04, -0.47, side * 0.4], 0.065, C.ink, 5)
    s.line([0.09, -0.15, side * 0.45], [0, -0.46, side * 0.46], 0.012, C.gold, 5)
    s.ellipsoid([0.04, -0.49, side * 0.4], [0.13, 0.058, 0.09], C.ink, 5, 450)
  }
  for (const point of s.points) {
    if ((point.part === 5 || point.part === 6) && point.y > 0.15) point.y = 0.15 + (point.y - 0.15) * 0.77
  }
  return s.model(
    t => {
      const gait = Math.sin(t * 2.8) * 0.24
      const legs = Array.from(
        { length: 4 },
        (_, i) => rotate(2, gait * (i % 3 ? 1 : -1), [i < 2 ? -0.56 : 0.47, -0.46, i % 2 ? 0.23 : -0.23])
      )
      return (p, v) => {
        if (p.part >= 1 && p.part <= 4) legs[p.part - 1](p, v)
        else v[1] += Math.sin(t * 5.6) * 0.018
        if (p.part === 6) v[0] += Math.sin(t * 3 + p.x * 4) * (1.1 - p.y) * 0.13
      }
    },
    [-0.11, 0.06]
  )
}

export function catBomb(): Sculpture {
  const s = new Shape()
  s.tube(
    t => [-0.67 - Math.sin(t * 2.4) * 0.38, -0.71 + t * 0.6, -0.06],
    0.085,
    C.rust,
    2,
    85,
    12
  )
  s.ellipsoid([-0.48, -0.44, 0], [0.32, 0.48, 0.25], C.rust, 0, 3700)
  s.ellipsoid([-0.39, 0.17, 0.06], [0.34, 0.31, 0.26], C.gold, 0, 3500)
  for (const x of [-0.65, -0.13]) {
    s.surface(
      28,
      25,
      (u, v) => [x + (v - 0.5) * (1 - u) * 0.23, 0.31 + u * 0.31, 0.04 + (1 - u) * 0.07],
      (u, v) => tint(v > 0.25 && v < 0.75 ? C.rose : C.gold, 0.8 + u * 0.2)
    )
  }
  for (const x of [-0.52, -0.27]) {
    s.ellipsoid([x, 0.22, 0.3], [0.056, 0.062, 0.018], C.teal, 0, 350)
    s.ellipsoid([x, 0.22, 0.321], [0.012, 0.044, 0.008], C.ink, 0, 150)
    s.ellipsoid([x, -0.86, 0.1], [0.13, 0.075, 0.16], C.cream, 0, 550)
  }
  s.ellipsoid([-0.39, 0.08, 0.319], [0.045, 0.027, 0.02], C.rose, 0, 200)
  for (const side of [-1, 1]) for (let w = 0; w < 3; w++) s.line(
    [-0.39 + side * 0.12, 0.01, 0.31],
    [-0.39 + side * 0.45, 0.1 - w * 0.07, 0.32],
    0.006,
    C.cream,
    0,
    20
  )
  s.line([-0.2, -0.18, 0.22], [0.17, 0.1, 0.24], 0.074, C.gold, 1, 60)
  s.ellipsoid([0.17, 0.1, 0.24], [0.1, 0.07, 0.07], C.cream, 1, 550)
  s.line([0.18, 0.13, 0.24], [0.4, 0.3, 0.07], 0.008, C.gold, 1, 25)
  s.ellipsoid([0.4, 0.3, 0.07], [0.018, 0.023, 0.018], C.rust, 1, 120)
  s.ellipsoid([0.66, -0.54, 0], [0.41, 0.41, 0.41], C.ink, 0, 6100)
  s.ellipsoid([0.55, -0.35, 0.32], [0.1, 0.085, 0.016], tint(C.blue, 0.67), 0, 400)
  s.box([0.66, -0.105, 0], [0.16, 0.12, 0.14], C.gold)
  s.tube(
    t => [0.66 - t * 0.24, -0.04 + t * 0.34, Math.sin(t * Math.PI) * 0.04],
    0.018,
    C.gold
  )
  for (let spark = 0; spark < 85; spark++) {
    const a = spark * 2.399963,
      r = 0.04 + hash(spark) * 0.1
    s.add(
      [0.42 + Math.cos(a) * r, 0.3 + Math.sin(a) * r, 0.04 + hash(spark + 99) * 0.06],
      spark % 2 ? C.gold : C.rust,
      3
    )
  }
  return s.model(
    t => {
      const paw = rotate(2, Math.sin(t * 1.5) * 0.12, [-0.2, -0.18, 0.22])
      const spark = 0.65 + Math.sin(t * 3.1) * 0.35
      return (p, v) => {
        if (p.part === 1) paw(p, v)
        if (p.part === 2) v[2] += Math.sin(t * 2 + p.y * 3) * 0.15
        if (p.part === 3) {
          v[0] = 0.42 + (p.x - 0.42) * spark
          v[1] = 0.3 + (p.y - 0.3) * spark
        }
      }
    },
    [0.02, 0.05]
  )
}
