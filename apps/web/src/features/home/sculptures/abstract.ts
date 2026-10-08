/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  Shape,
  TAU,
  palette as C,
  rotate,
  tint,
  type Sculpture,
} from './geometry'

export function socket(): Sculpture {
  const s = new Shape()
  s.ring([0, 0, 0], 0.71, 0.23, C.violet)
  s.ring([0, 0, 0.16], 0.41, 0.035, C.gold)
  s.ellipsoid([0, 0, -0.04], [0.29, 0.29, 0.11], C.ink, 0, 2200)
  for (let fin = 0; fin < 12; fin++) {
    const turn = rotate(2, (fin / 12) * TAU),
      start = s.points.length
    s.box([1.04, 0, 0], [0.39, 0.15, 0.18], C.violet)
    s.box([1.19, 0, 0.035], [0.08, 0.16, 0.2], C.gold)
    for (let i = start; i < s.points.length; i++) {
      const p = s.points[i],
        v: [number, number, number] = [p.x, p.y, p.z]
      turn(p, v)
      p.x = v[0]
      p.y = v[1]
      p.z = v[2]
    }
  }
  return s.model((t) => rotate(2, t * 0.19), [0.13, 0.23])
}
export function gyroscope(): Sculpture {
  const s = new Shape()
  for (const axis of [0, 1, 2] as const) {
    s.ring(
      [0, 0, 0],
      1.04 - axis * 0.17,
      0.065,
      [C.teal, C.gold, C.violet][axis],
      axis + 1,
      axis
    )
  }
  s.ellipsoid([0, 0, 0], [0.28, 0.28, 0.28], C.rose, 0, 2200)
  return s.model(
    (t) => {
      const turns = [
        rotate(0, t * 0.6),
        rotate(1, -t * 0.43),
        rotate(2, t * 0.3),
      ]
      return (p, v) => {
        if (p.part) turns[p.part - 1](p, v)
      }
    },
    [0.28, 0.29]
  )
}
export function mobius(): Sculpture {
  const s = new Shape()
  s.surface(
    240,
    55,
    (u, v) => {
      const a = u * TAU,
        w = (v - 0.5) * 0.65,
        r = 0.8 + Math.cos(a / 2) * w
      return [Math.cos(a) * r, Math.sin(a) * r, Math.sin(a / 2) * w]
    },
    (u, v) =>
      tint(
        v < 0.05 || v > 0.95 ? C.gold : C.violet,
        0.6 + 0.35 * Math.sin(u * Math.PI)
      )
  )
  return s.model((t) => rotate(1, t * 0.23), [0, 0.32])
}
export function trefoil(): Sculpture {
  const s = new Shape()
  s.tube(
    (t) => {
      const a = t * TAU,
        r = (2 + Math.cos(3 * a)) * 0.37
      return [Math.cos(2 * a) * r, Math.sin(2 * a) * r, Math.sin(3 * a) * 0.37]
    },
    0.13,
    C.teal,
    0,
    420,
    30
  )
  s.points.forEach((p) => {
    p.color = tint(p.z > 0.15 ? C.gold : C.teal, 0.72 + p.y * 0.15)
  })
  return s.model(
    (t) => {
      const turn = rotate(1, t * 0.24),
        roll = rotate(2, t * 0.08)
      return (p, v) => {
        turn(p, v)
        roll(p, v)
      }
    },
    [0, 0.14]
  )
}
export function helix(): Sculpture {
  const s = new Shape()
  for (const phase of [0, Math.PI]) {
    s.tube(
      (t) => [
        Math.cos(t * TAU * 1.65 + phase) * 0.52,
        (t - 0.5) * 2.25,
        Math.sin(t * TAU * 1.65 + phase) * 0.52,
      ],
      0.09,
      phase ? C.rose : C.teal,
      0,
      210,
      20
    )
  }
  for (let i = 0; i < 19; i++) {
    const a = (i / 18) * TAU * 1.65,
      y = (i / 18 - 0.5) * 2.25
    s.line(
      [-Math.cos(a) * 0.52, y, -Math.sin(a) * 0.52],
      [Math.cos(a) * 0.52, y, Math.sin(a) * 0.52],
      0.024,
      C.gold
    )
  }
  return s.model((t) => rotate(1, t * 0.4), [0.05, 0.12])
}
export function ribbon(): Sculpture {
  const s = new Shape()
  for (let band = 0; band < 3; band++) {
    s.surface(
      130,
      40,
      (u, v) => {
        const a = u * TAU,
          width = (v - 0.5) * 0.28,
          r = 0.81 + width
        return [
          Math.cos(a) * r,
          Math.sin(a * 2) * 0.4 + (band - 1) * 0.37,
          Math.sin(a) * r,
        ]
      },
      (u, v) =>
        tint(
          v < 0.07 || v > 0.93 ? C.gold : [C.violet, C.rose, C.teal][band],
          0.65 + Math.sin(u * Math.PI) * 0.3
        ),
      band
    )
  }
  return s.model(
    (t) => {
      const turn = rotate(1, t * 0.15)
      return (p, v) => {
        v[1] += Math.sin(t * 1.3 + p.x * 3 + p.z * 2) * 0.16
        turn(p, v)
      }
    },
    [0.05, 0.38]
  )
}
