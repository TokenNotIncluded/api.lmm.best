/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { bicycle } from './bicycle'
import {
  Shape,
  TAU,
  hash,
  palette as C,
  rotate,
  tint,
  type Sculpture,
} from './geometry'

export function pelicanBicycle(): Sculpture {
  const s = new Shape(),
    ride = bicycle(s, C.gold, C.gold, 0.023)
  s.ellipsoid([-0.3, 0.2, 0], [0.37, 0.34, 0.24], C.cream, 20, 4200)
  for (let feather = 0; feather < 6; feather++) {
    s.tube(
      (t) => [
        -0.48 - t * (0.23 + feather * 0.017),
        0.19 + t * (0.17 - feather * 0.035),
        -0.06 + feather * 0.021,
      ],
      0.027,
      tint(C.cream, 0.7 + feather * 0.04),
      20,
      28,
      6
    )
  }
  // Neck, throat pouch and a long, slim upper bill give a clear pelican profile.
  s.tube(
    (t) => [-0.035 + Math.sin(t * Math.PI * 0.8) * 0.11, 0.35 + t * 0.48, 0],
    0.077,
    C.cream,
    21,
    65,
    14
  )
  s.ellipsoid([0.1, 0.88, 0.01], [0.163, 0.151, 0.125], C.cream, 21, 1400)
  s.surface(
    75,
    27,
    (u, v) => [
      0.19 + u * 0.86,
      0.865 -
        u * 0.072 -
        Math.sin(v * Math.PI) *
          Math.sin((u * 0.8 + 0.17) * Math.PI) *
          (1 - u) *
          0.28,
      (v - 0.5) * (1 - u) * 0.19,
    ],
    (_u, v) => tint(C.gold, 0.7 + Math.sin(v * Math.PI) * 0.27),
    21
  )
  s.tube(
    (t) => [0.18 + t * 0.88, 0.883 - t * 0.086, 0],
    0.014,
    C.gold,
    21,
    80,
    6
  )
  s.line([0.23, 0.872, 0.085], [1.05, 0.795, 0.003], 0.007, C.rust, 21, 55)
  s.ellipsoid([0.125, 0.925, 0.123], [0.032, 0.034, 0.018], C.ink, 21, 180)
  s.ellipsoid([0.133, 0.936, 0.138], [0.01, 0.011, 0.006], C.cream, 21, 65)
  for (let feather = 0; feather < 8; feather++) {
    s.tube(
      (t) => [
        -0.49 + t * 0.43 + feather * 0.022,
        0.32 - t * 0.2 - Math.sin(t * Math.PI) * feather * 0.014,
        0.195 + Math.sin(t * Math.PI) * 0.049,
      ],
      0.022,
      tint(C.cream, 0.67 + feather * 0.036),
      20,
      38,
      6
    )
  }
  s.tube(
    (t) => [
      -0.08 + t * 0.56,
      0.15 - Math.sin((t * Math.PI) / 2) * 0.16,
      0.16 - t * 0.04,
    ],
    0.034,
    C.cream,
    20,
    48,
    8
  )
  return s.model(
    (t) => {
      const motion = ride(t),
        nod = rotate(2, Math.sin(t * 1.1) * 0.025, [-0.035, 0.35, 0])
      return (p, v) => {
        if (p.part === 21) nod(p, v)
        motion(p, v)
      }
    },
    [-0.08, 0.06]
  )
}

export function emperorBicycle(): Sculpture {
  const s = new Shape(),
    robe: [number, number, number] = [79, 88, 104]
  const ride = bicycle(s, robe, C.ink, 0.059)
  s.surface(
    75,
    42,
    (u, v) => {
      const a = u * TAU,
        r = 0.23 - v * 0.065
      return [
        -0.31 + v * 0.14 + Math.cos(a) * r,
        -0.17 + v * 0.67,
        Math.sin(a) * r * 0.72,
      ]
    },
    (u, v) =>
      tint(v < 0.055 ? C.gold : robe, 0.7 + Math.cos(u * TAU) ** 2 * 0.29),
    20
  )
  s.line([-0.31, 0.48, 0.14], [-0.07, 0.21, 0.15], 0.018, C.gold, 20)
  s.line([-0.08, 0.46, 0.14], [-0.31, 0.14, 0.17], 0.018, C.gold, 20)
  s.tube(
    (t) => [
      -0.31 + Math.cos(t * TAU) * 0.224,
      -0.035,
      Math.sin(t * TAU) * 0.16,
    ],
    0.025,
    C.rust,
    20,
    80,
    6
  )
  s.ellipsoid([-0.16, 0.69, 0], [0.139, 0.175, 0.13], C.cream, 20, 1700)
  s.ellipsoid([-0.215, 0.735, -0.042], [0.136, 0.16, 0.1], C.ink, 20, 700)
  s.ellipsoid([-0.017, 0.675, 0.051], [0.045, 0.034, 0.05], C.cream, 20, 200)
  s.line([-0.142, 0.756, 0.116], [-0.062, 0.75, 0.116], 0.011, C.ink, 20, 16)
  s.ellipsoid([-0.085, 0.724, 0.124], [0.02, 0.013, 0.009], C.ink, 20, 110)
  s.tube(
    (t) => [-0.135 + t * 0.115, 0.628 + Math.sin(t * Math.PI) * 0.018, 0.133],
    0.013,
    C.ink,
    20,
    24,
    5
  )
  s.surface(
    36,
    22,
    (u, v) => {
      const a = u * TAU,
        r = (1 - v) * 0.055
      return [
        -0.09 + Math.cos(a) * r + v * 0.026,
        0.613 - v * 0.19,
        0.09 + Math.sin(a) * r,
      ]
    },
    C.ink,
    20
  )
  s.box([-0.19, 0.88, 0], [0.24, 0.12, 0.2], C.ink, 20)
  s.box([-0.17, 0.957, 0], [0.47, 0.035, 0.34], C.gold, 20)
  s.box([-0.17, 0.98, 0], [0.5, 0.025, 0.36], C.ink, 20)
  for (const end of [-1, 1])
    for (let strand = 0; strand < 7; strand++) {
      const x = -0.17 + end * 0.23,
        z = -0.14 + strand * 0.047
      s.line([x, 0.953, z], [x, 0.8, z], 0.0045, C.gold, 21, 18)
      for (let bead = 0; bead < 3; bead++)
        s.ellipsoid(
          [x, 0.82 + bead * 0.044, z],
          [0.011, 0.012, 0.011],
          C.gold,
          21,
          42
        )
    }
  for (const side of [-1, 1]) {
    const z = side * 0.125
    s.line([-0.12, 0.39, z], [0.09, 0.13, z], 0.072, robe, 20, 34)
    s.line([0.09, 0.13, z], [0.43, 0.015, z], 0.059, robe, 20, 36)
    s.line(
      [0.36, 0.035, z + 0.045],
      [0.415, 0.016, z + 0.045],
      0.013,
      C.gold,
      20,
      12
    )
    s.ellipsoid([0.475, -0.007, z], [0.058, 0.035, 0.039], C.cream, 20, 220)
  }
  return s.model(
    (t) => {
      const motion = ride(t)
      return (p, v) => {
        if (p.part === 21)
          v[0] += Math.sin(t * 3 + p.z * 3) * (0.96 - p.y) * 0.08
        motion(p, v)
      }
    },
    [-0.1, 0.055]
  )
}

export function catBomb(): Sculpture {
  const s = new Shape()
  s.tube(
    (t) => [-0.67 - Math.sin(t * 2.6) * 0.31, -0.54 + t * 0.63, -0.12],
    0.058,
    C.gold,
    2,
    75,
    10
  )
  s.ellipsoid([-0.45, -0.24, 0], [0.29, 0.4, 0.235], C.gold, 0, 2700)
  s.ellipsoid([-0.42, -0.2, 0.218], [0.14, 0.24, 0.032], C.cream, 0, 650)
  for (const x of [-0.65, -0.28]) {
    s.ellipsoid([x, -0.55, 0.045], [0.158, 0.17, 0.21], C.gold, 0, 650)
    s.ellipsoid([x + 0.01, -0.67, 0.17], [0.15, 0.066, 0.14], C.cream, 0, 400)
    for (const offset of [-0.045, 0.04])
      s.line(
        [x + offset, -0.659, 0.285],
        [x + offset, -0.622, 0.277],
        0.005,
        C.rust,
        0,
        9
      )
  }
  s.ellipsoid([-0.42, 0.36, 0.015], [0.335, 0.293, 0.247], C.gold, 0, 3200)
  for (const side of [-1, 1]) {
    for (const face of [-1, 1]) {
      s.surface(
        28,
        21,
        (u, v) => [
          -0.42 + side * (0.22 + u * 0.055) + (v - 0.5) * (1 - u) * 0.25,
          0.5 + u * 0.31,
          0.014 + face * (1 - u) * 0.11,
        ],
        (_u, v) =>
          v > 0.24 && v < 0.76 && face > 0 ? tint(C.rose, 0.9) : C.gold
      )
    }
    const x = -0.42 + side * 0.125
    s.ellipsoid(
      [x, 0.401, 0.236],
      [0.067, 0.072, 0.027],
      tint(C.teal, 0.85),
      4,
      350
    )
    s.ellipsoid([x + 0.008, 0.401, 0.26], [0.017, 0.051, 0.012], C.ink, 4, 180)
    s.ellipsoid(
      [x + 0.022, 0.427, 0.271],
      [0.012, 0.015, 0.007],
      C.cream,
      4,
      60
    )
    s.ellipsoid(
      [-0.42 + side * 0.062, 0.283, 0.238],
      [0.092, 0.065, 0.04],
      C.cream,
      0,
      350
    )
    for (let whisker = 0; whisker < 3; whisker++) {
      s.tube(
        (t) => [
          -0.42 + side * (0.12 + t * 0.23),
          0.275 + (whisker - 1) * (0.025 + t * 0.049),
          0.248 + t * 0.014,
        ],
        0.005,
        tint(C.cream, 0.9),
        0,
        28,
        4
      )
    }
  }
  s.surface(
    17,
    17,
    (u, v) => [-0.42 + (v - 0.5) * (1 - u) * 0.076, 0.301 - u * 0.047, 0.288],
    C.rust
  )
  s.line([-0.42, 0.255, 0.274], [-0.42, 0.228, 0.263], 0.006, C.rust, 0, 10)
  for (const offset of [-0.07, 0, 0.07]) {
    s.tube(
      (t) => [
        -0.42 + offset + Math.sin(t * Math.PI) * offset * 0.25,
        0.565 - t * 0.055,
        0.15 + t * 0.048,
      ],
      0.01,
      C.rust,
      0,
      20,
      4
    )
  }
  s.tube(
    (t) => [
      -0.22 + t * 0.44,
      -0.02 + Math.sin(t * Math.PI) * 0.024,
      0.17 + t * 0.03,
    ],
    0.059,
    C.gold,
    1,
    42,
    9
  )
  s.ellipsoid([0.22, -0.02, 0.2], [0.085, 0.057, 0.075], C.cream, 1, 450)
  const shell: [number, number, number] = [77, 95, 121]
  s.ellipsoid([0.59, -0.36, 0], [0.4, 0.4, 0.4], shell, 0, 5000)
  s.tube(
    (t) => [
      0.59 + Math.cos(1.8 + t * 0.84) * 0.275,
      -0.36 + Math.sin(1.8 + t * 0.84) * 0.275,
      0.298,
    ],
    0.01,
    tint(C.blue, 1.03),
    0,
    48,
    5
  )
  s.ellipsoid([0.42, -0.15, 0.302], [0.047, 0.027, 0.014], C.cream, 0, 180)
  s.box([0.57, 0.046, 0], [0.14, 0.08, 0.14], C.gold)
  s.tube(
    (t) => [
      0.57 + Math.sin(t * Math.PI) * 0.09 - t * 0.11,
      0.09 + t * 0.31,
      0.018,
    ],
    0.015,
    C.gold,
    0,
    52,
    7
  )
  for (let ray = 0; ray < 7; ray++) {
    const a = (ray / 7) * TAU
    s.line(
      [0.46 + Math.cos(a) * 0.035, 0.4 + Math.sin(a) * 0.035, 0.018],
      [0.46 + Math.cos(a) * 0.09, 0.4 + Math.sin(a) * 0.09, 0.018],
      0.006,
      ray % 2 ? C.gold : C.rust,
      3,
      10
    )
  }
  for (let spark = 0; spark < 28; spark++) {
    const a = spark * 2.399963,
      r = 0.07 + hash(spark) * 0.075
    s.add([0.46 + Math.cos(a) * r, 0.4 + Math.sin(a) * r, 0.018], C.gold, 3)
  }
  return s.model(
    (t) => {
      const paw = rotate(2, Math.sin(t * 1.5) * 0.035, [-0.22, -0.02, 0.17])
      const tail = rotate(1, Math.sin(t * 1.3) * 0.2, [-0.67, -0.54, -0.12])
      const spark = 0.8 + Math.sin(t * 3.1) * 0.2
      const blink =
        1 - Math.exp(-Math.pow((((t + 0.7) % 5.4) - 0.15) / 0.075, 2)) * 0.9
      return (p, v) => {
        if (p.part === 1) paw(p, v)
        if (p.part === 2) tail(p, v)
        if (p.part === 3) {
          v[0] = 0.46 + (p.x - 0.46) * spark
          v[1] = 0.4 + (p.y - 0.4) * spark
        }
        if (p.part === 4) v[1] = 0.401 + (p.y - 0.401) * blink
      }
    },
    [0.015, 0.03]
  )
}
