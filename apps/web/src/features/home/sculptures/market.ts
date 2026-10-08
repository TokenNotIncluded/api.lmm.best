/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  Shape,
  TAU,
  palette as C,
  rotate,
  tint,
  type Sculpture,
  type Vec3,
} from './geometry'

function awning(s: Shape, y: number, depth: number) {
  for (let stripe = 0; stripe < 16; stripe++) {
    const color = stripe % 2 ? C.cream : C.teal
    s.surface(
      9,
      28,
      (u, v) => [
        -1.04 + (stripe + u) * 0.13,
        y + Math.sin(v * Math.PI) * 0.14 - v * 0.16,
        -depth / 2 + v * depth,
      ],
      color,
      1
    )
    s.surface(
      9,
      9,
      (u, v) => [
        -1.04 + (stripe + u) * 0.13,
        y - 0.16 - v * (0.11 + Math.sin(u * Math.PI) * 0.05),
        depth / 2,
      ],
      tint(color, 0.82),
      1
    )
  }
}
function bottle(s: Shape, center: Vec3, color: Vec3, part = 0) {
  s.ellipsoid(center, [0.065, 0.12, 0.065], color, part, 260)
  s.box(
    [center[0], center[1] + 0.13, center[2]],
    [0.045, 0.035, 0.045],
    C.gold,
    part
  )
}
export function shop(): Sculpture {
  const s = new Shape()
  s.box([0, -0.22, -0.12], [1.88, 1.43, 0.82], C.teal)
  s.box([0, -0.98, 0], [2.25, 0.1, 1.14], C.ink)
  for (const side of [-1, 1]) {
    s.box([side * 0.6, -0.24, 0.31], [0.46, 0.87, 0.03], C.ink)
    for (let row = 0; row < 3; row++) {
      s.box([side * 0.6, -0.55 + row * 0.27, 0.4], [0.45, 0.026, 0.13], C.gold)
      for (let item = 0; item < 3; item++) {
        bottle(
          s,
          [side * 0.6 - 0.14 + item * 0.14, -0.43 + row * 0.27, 0.37],
          [C.rose, C.gold, C.teal][item]
        )
      }
    }
  }
  s.box([0, -0.31, 0.34], [0.33, 1.06, 0.065], C.gold, 2)
  s.box([0, -0.23, 0.38], [0.25, 0.75, 0.018], C.ink, 2)
  s.ellipsoid([0.1, -0.36, 0.41], [0.024, 0.024, 0.024], C.cream, 2, 100)
  awning(s, 0.66, 1.16)
  s.box([0, 0.77, -0.01], [1.14, 0.25, 0.11], C.ink)
  for (let i = 0; i < 5; i++) {
    s.box(
      [-0.35 + i * 0.17, 0.77, 0.065],
      [0.075, 0.065 + (i % 2) * 0.055, 0.01],
      C.gold
    )
  }
  for (const side of [-1, 1]) {
    s.line([side * 0.92, 0.2, 0.5], [side * 1.09, 0.09, 0.5], 0.018, C.gold)
    s.ellipsoid([side * 1.1, 0.04, 0.5], [0.08, 0.09, 0.08], C.gold, 3, 450)
    s.box([side * 1.03, -0.79, 0.5], [0.18, 0.28, 0.18], C.rust)
    s.ellipsoid([side * 1.03, -0.57, 0.5], [0.17, 0.19, 0.16], C.teal, 4, 700)
  }
  return s.model(
    (t) => {
      const door = rotate(1, Math.sin(t * 0.65) * 0.22, [-0.16, 0, 0.34])
      return (p, v) => {
        if (p.part === 1) {
          v[1] += Math.sin(t * 2 + p.x * 3) * 0.025 * Math.max(0, p.z)
        }
        if (p.part === 2) door(p, v)
        if (p.part === 3) v[0] += Math.sin(t * 1.5) * 0.028
        if (p.part === 4) v[0] += Math.sin(t * 1.8 + p.y * 4) * 0.025
      }
    },
    [-0.24, 0.16]
  )
}
export function vending(): Sculpture {
  const s = new Shape()
  s.box([0, -0.02, 0], [1.4, 2.03, 0.8], C.rust)
  s.box([-0.17, 0.22, 0.413], [0.88, 1.13, 0.045], C.ink)
  s.box([0, 0.89, 0.43], [1.12, 0.13, 0.04], C.cream)
  for (let row = 0; row < 3; row++) {
    const y = 0.58 - row * 0.32
    s.box([-0.17, y - 0.13, 0.48], [0.83, 0.024, 0.12], C.gold)
    for (let col = 0; col < 4; col++) {
      bottle(
        s,
        [-0.48 + col * 0.205, y, 0.49],
        [C.teal, C.gold, C.rose, C.blue][col],
        row === 2 && col === 2 ? 1 : 0
      )
    }
  }
  s.box([0.49, 0.52, 0.44], [0.22, 0.25, 0.03], C.ink)
  s.box([0.49, 0.55, 0.46], [0.16, 0.025, 0.015], C.teal, 2)
  for (let i = 0; i < 5; i++) {
    s.ellipsoid(
      [0.49, 0.17 - i * 0.12, 0.47],
      [0.045, 0.037, 0.025],
      i === 2 ? C.gold : C.cream,
      0,
      160
    )
  }
  s.box([-0.14, -0.71, 0.43], [0.88, 0.28, 0.04], C.ink)
  s.box([-0.14, -0.84, 0.49], [0.9, 0.035, 0.17], C.gold)
  for (const x of [-0.5, 0.5]) s.box([x, -1.1, 0], [0.17, 0.16, 0.6], C.ink)
  return s.model(
    (t) => (p, v) => {
      if (p.part === 1) {
        const drop = Math.pow((1 - Math.cos(t * 1.2)) / 2, 4)
        v[1] -= drop * 0.51
        v[2] += drop * 0.04
      }
      if (p.part === 2) v[0] += Math.sin(t * 3) * 0.025
    },
    [-0.3, 0.1]
  )
}
export function stall(): Sculpture {
  const s = new Shape()
  awning(s, 0.96, 1.13)
  for (const x of [-0.91, 0.91]) {
    for (const z of [-0.43, 0.43]) {
      s.line([x, -0.93, z], [x, 0.93, z], 0.033, C.gold)
    }
  }
  s.box([0, -0.49, 0], [1.91, 0.63, 0.8], C.rust)
  s.box([0, -0.14, 0], [2.1, 0.1, 1.02], C.cream)
  for (let i = 0; i < 12; i++) {
    s.box([-0.88 + i * 0.16, -0.47, 0.425], [0.045, 0.57, 0.025], C.gold)
  }
  for (let tray = 0; tray < 3; tray++) {
    const x = -0.66 + tray * 0.66
    s.box([x, -0.045, 0.09], [0.54, 0.09, 0.69], C.ink)
    for (let fruit = 0; fruit < 9; fruit++) {
      s.ellipsoid(
        [
          x - 0.16 + (fruit % 3) * 0.16,
          0.06 + (fruit % 2) * 0.035,
          -0.12 + Math.floor(fruit / 3) * 0.17,
        ],
        [0.088, 0.085, 0.085],
        [C.gold, C.rose, C.teal][tray],
        0,
        240
      )
    }
  }
  s.line([0.64, 0.79, 0.51], [0.64, 0.49, 0.51], 0.009, C.gold)
  s.box([0.64, 0.35, 0.51], [0.31, 0.29, 0.04], C.ink, 2)
  for (let i = 0; i < 3; i++) {
    s.line(
      [0.54, 0.42 - i * 0.07, 0.54],
      [0.73 - i * 0.035, 0.42 - i * 0.07, 0.54],
      0.009,
      C.cream,
      2,
      12
    )
  }
  for (const side of [-1, 1]) {
    s.ring([side * 0.72, -0.97, 0.43], 0.14, 0.033, C.ink)
  }
  return s.model(
    (t) => {
      const sign = rotate(2, Math.sin(t * 1.7) * 0.13, [0.64, 0.79, 0.51])
      return (p, v) => {
        if (p.part === 1) v[1] += Math.sin(p.x * TAU + t * 2) * 0.025
        if (p.part === 2) sign(p, v)
      }
    },
    [-0.27, 0.15]
  )
}
