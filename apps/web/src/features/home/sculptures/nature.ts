/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  Shape,
  TAU,
  hash,
  mix,
  palette as C,
  tint,
  type Sculpture,
  type Vec3,
} from './geometry'

/** Used only until the existing, packaged lotus image has been sampled. */
export function lotus(): Sculpture {
  const s = new Shape()
  s.tube((t) => [Math.sin(t * 3) * 0.05, -1.35 + t * 1.45, 0], 0.035, C.teal)
  for (const side of [-1, 1]) {
    s.surface(
      34,
      22,
      (u, v) => [
        side * u * 0.77,
        -0.82 + Math.sin(u * Math.PI) * 0.28 + v * 0.04,
        (v - 0.5) * Math.sin(u * Math.PI) * 0.65,
      ],
      (u) => tint(C.teal, 0.6 + u * 0.3)
    )
  }
  for (let layer = 0; layer < 4; layer++) {
    const petals = 12 - layer,
      length = 0.9 - layer * 0.16
    for (let petal = 0; petal < petals; petal++) {
      const angle = (petal / petals) * TAU + layer * 0.4
      const begin = s.points.length
      s.surface(
        32,
        17,
        (u, v) => {
          const r = 0.09 + u * length,
            width = (v - 0.5) * Math.sin(Math.PI * u) * 0.55
          return [
            Math.cos(angle) * r - Math.sin(angle) * width,
            0.02 +
              layer * 0.05 +
              u * u * (0.25 + layer * 0.17) -
              Math.sin(u * Math.PI) * 0.1,
            Math.sin(angle) * r + Math.cos(angle) * width,
          ]
        },
        (u, v) => tint(C.rose, 0.65 + u * 0.3 + Math.sin(v * Math.PI) * 0.1),
        1
      )
      for (let i = begin; i < s.points.length; i++) {
        const p = s.points[i]
        p.closed = [
          p.x * 0.35,
          p.y * 0.9 + Math.hypot(p.x, p.z) * 0.55,
          p.z * 0.35,
        ]
      }
    }
  }
  s.ellipsoid([0, 0.12, 0], [0.15, 0.1, 0.15], C.gold, 0, 650)
  return s.model(
    (t) => {
      const open = 0.67 + Math.sin((t * TAU) / 5.6) * 0.31
      return (p, v) => {
        if (p.closed)
          for (let i = 0; i < 3; i++) v[i] = mix(p.closed[i], v[i], open)
      }
    },
    [0, 0.3]
  )
}

export function fish(): Sculpture {
  const s = new Shape()
  s.ellipsoid([0.05, 0, 0], [0.84, 0.39, 0.28], C.cream, 0, 11000)
  for (const p of s.points) {
    if (Math.sin(p.x * 8 + p.y * 10) + Math.cos(p.z * 19 - p.x * 4) > 0.35)
      p.color = tint(C.rust, 0.72 + hash(p.x + p.y) * 0.25)
  }
  s.surface(
    70,
    40,
    (u, v) => [
      -0.67 - u * 0.79,
      (v - 0.5) * (0.13 + u * 1.32),
      Math.sin(v * Math.PI * 10) * u * 0.03,
    ],
    (u, v) => tint(C.rose, 0.62 + 0.3 * Math.sin(v * Math.PI) + u * 0.06),
    1
  )
  s.surface(
    55,
    22,
    (u, v) => [
      -0.45 + u * 0.92,
      0.27 + Math.sin(u * Math.PI) * v * 0.43,
      v * 0.02,
    ],
    C.gold,
    2
  )
  for (const side of [-1, 1]) {
    s.surface(
      32,
      24,
      (u, v) => [
        0.12 - u * 0.48,
        -0.16 - Math.sin(u * Math.PI) * v * 0.45,
        side * (0.21 + u * 0.25),
      ],
      C.rose,
      2
    )
    s.ellipsoid([0.65, 0.09, side * 0.19], [0.058, 0.06, 0.035], C.ink, 0, 260)
    s.ellipsoid([0.66, 0.11, side * 0.22], [0.017, 0.02, 0.01], C.cream, 0, 60)
    s.tube(
      (t) => [
        0.53 + Math.sin(t * Math.PI) * 0.03,
        -0.2 + t * 0.36,
        side * 0.24,
      ],
      0.012,
      C.gold
    )
  }
  return s.model(
    (t) => (p, v) => {
      const tail = Math.max(0, 0.65 - p.x)
      v[2] += Math.sin(t * 4.4 + p.x * 3.5) * tail * tail * 0.11
      v[1] +=
        Math.sin(t * 1.2) * 0.08 +
        (p.part === 2 ? Math.sin(t * 5 + p.x * 4) * 0.05 : 0)
      v[0] += Math.sin(t * 0.7) * 0.1
    },
    [0.06, 0.12]
  )
}

export function jellyfish(): Sculpture {
  const s = new Shape()
  s.surface(
    120,
    72,
    (u, v) => {
      const a = u * TAU,
        r = Math.sin(v * Math.PI * 0.5) * 0.8
      return [
        Math.cos(a) * r,
        0.26 + Math.cos(v * Math.PI * 0.5) * 0.66,
        Math.sin(a) * r,
      ]
    },
    (u, v) =>
      tint(
        v > 0.88 ? C.rose : C.violet,
        0.55 + 0.4 * Math.pow(Math.cos(u * TAU * 8), 8) + v * 0.08
      )
  )
  s.ring([0, 0.26, 0], 0.8, 0.023, C.rose, 0, 1)
  for (let arm = 0; arm < 20; arm++) {
    const a = (arm / 20) * TAU,
      radius = arm % 3 === 0 ? 0.24 : 0.61
    s.tube(
      (t) => [
        Math.cos(a) * radius + Math.sin(t * 8 + a) * 0.055,
        0.27 - t * (1.22 + hash(arm) * 0.4),
        Math.sin(a) * radius + Math.cos(t * 9 + a) * 0.06,
      ],
      arm % 3 === 0 ? 0.026 : 0.012,
      arm % 3 === 0 ? C.rose : C.teal,
      1,
      95,
      6
    )
  }
  return s.model(
    (t) => {
      const pulse = Math.sin(t * 2.5),
        size = 0.93 + pulse * 0.08
      return (p, v) => {
        v[0] *= size
        v[2] *= size
        v[1] += Math.sin(t * 1.25) * 0.1
        if (p.part) {
          const length = 0.27 - p.y
          v[0] += Math.sin(t * 2.4 + length * 4 + p.z * 3) * length * 0.09
          v[2] += Math.cos(t * 2 + length * 3) * length * 0.055
        } else v[1] += (p.y - 0.26) * pulse * 0.14
      }
    },
    [0, 0.12]
  )
}

export function dandelion(): Sculpture {
  const s = new Shape(),
    center: Vec3 = [0, 0.4, 0]
  s.tube((t) => [Math.sin(t * 2) * 0.08, -1.32 + t * 1.72, 0], 0.024, C.teal)
  s.ellipsoid(center, [0.11, 0.11, 0.11], C.gold, 0, 500)
  for (let seed = 0; seed < 150; seed++) {
    const y = 1 - (2 * (seed + 0.5)) / 150,
      r = Math.sqrt(1 - y * y),
      a = seed * 2.399963
    const direction: Vec3 = [Math.cos(a) * r, y, Math.sin(a) * r]
    const tip: Vec3 = [
      direction[0] * 0.66,
      0.4 + direction[1] * 0.66,
      direction[2] * 0.66,
    ]
    for (let i = 0; i < 16; i++)
      s.add(
        [
          (direction[0] * i) / 24,
          0.4 + (direction[1] * i) / 24,
          (direction[2] * i) / 24,
        ],
        tint(C.cream, 0.6),
        seed + 1
      )
    const tangent: Vec3 =
      Math.abs(y) < 0.9
        ? [-direction[2], 0, direction[0]]
        : [0, direction[2], -y]
    const length = Math.hypot(...tangent)
    const [ax, ay, az] = tangent.map((v) => v / length)
    const bx = direction[1] * az - direction[2] * ay
    const by = direction[2] * ax - direction[0] * az
    const bz = direction[0] * ay - direction[1] * ax
    for (let ray = 0; ray < 7; ray++) {
      const angle = (ray / 7) * TAU
      for (let j = 0; j < 10; j++) {
        const spread = (j / 9) * 0.11
        const c = Math.cos(angle) * spread,
          d = Math.sin(angle) * spread
        s.add(
          [
            tip[0] + ax * c + bx * d + direction[0] * spread * 0.35,
            tip[1] + ay * c + by * d + direction[1] * spread * 0.35,
            tip[2] + az * c + bz * d + direction[2] * spread * 0.35,
          ],
          C.cream,
          seed + 1
        )
      }
    }
  }
  return s.model(
    (t) => {
      const sway = Math.sin(t * 1.1) * 0.026
      const wind = Math.pow((1 - Math.cos(t * 0.75)) / 2, 3)
      return (p, v) => {
        v[0] += sway * (p.y + 1.32)
        if (!p.part) return
        const loosen = Math.max(0, wind - hash(p.part) * 0.55)
        v[0] += loosen * (0.7 + hash(p.part + 100) * 0.9)
        v[1] += loosen * (0.2 + hash(p.part + 200) * 0.6)
        v[2] += loosen * Math.sin(p.part * 2.4 + t) * 0.4
      }
    },
    [0, 0.03]
  )
}

export function cloud(): Sculpture {
  const s = new Shape()
  const lobes: [Vec3, Vec3][] = [
    [
      [-0.86, -0.08, 0],
      [0.43, 0.34, 0.36],
    ],
    [
      [-0.49, 0.18, 0],
      [0.48, 0.47, 0.46],
    ],
    [
      [0.02, 0.33, 0],
      [0.57, 0.63, 0.48],
    ],
    [
      [0.52, 0.11, 0],
      [0.52, 0.42, 0.4],
    ],
    [
      [0.94, -0.08, 0],
      [0.35, 0.28, 0.31],
    ],
    [
      [-0.14, -0.2, 0.17],
      [0.95, 0.26, 0.4],
    ],
  ]
  lobes.forEach(([c, r], i) =>
    s.ellipsoid(c, r, i % 2 ? C.cream : [203, 214, 228], i, 2600)
  )
  return s.model(
    (t) => (p, v) => {
      v[0] += Math.sin(t * 0.65) * 0.18
      v[1] += Math.sin(t + p.part * 0.6) * 0.035
      v[2] += Math.sin(t * 0.8 + p.x * 2) * 0.05
    },
    [0.03, 0.06]
  )
}

export function dragonfly(): Sculpture {
  const s = new Shape()
  s.ellipsoid([0, -0.15, 0], [0.075, 0.64, 0.075], C.teal, 0, 2200)
  s.ellipsoid([0, 0.37, 0], [0.14, 0.22, 0.13], C.gold, 0, 1000)
  for (const side of [-1, 1]) {
    s.ellipsoid([side * 0.1, 0.57, 0.025], [0.088, 0.085, 0.07], C.teal, 0, 650)
    s.ellipsoid(
      [side * 0.108, 0.58, 0.083],
      [0.029, 0.03, 0.015],
      C.ink,
      0,
      150
    )
    for (let pair = 0; pair < 2; pair++) {
      const part = side < 0 ? 1 + pair : 3 + pair
      s.surface(
        80,
        30,
        (u, v) => [
          side * (0.08 + u * 1.13),
          0.28 -
            pair * 0.25 +
            u * (pair ? -0.34 : 0.2) +
            (v - 0.5) * Math.sin(Math.PI * u) * 0.38,
          Math.sin(v * Math.PI) * Math.sin(u * Math.PI) * 0.035,
        ],
        (u, v) =>
          tint(
            C.cream,
            Math.sin(u * 90 + v * 12) > 0.83 ? 0.98 : 0.52 + 0.22 * v
          ),
        part
      )
    }
    for (let leg = 0; leg < 3; leg++)
      s.tube(
        (t) => [
          side * (0.07 + t * 0.25),
          0.3 - leg * 0.13 - Math.sin(t * Math.PI) * 0.12,
          0.1 + Math.sin(t * Math.PI) * 0.06,
        ],
        0.012,
        C.gold,
        0,
        25,
        6
      )
  }
  return s.model(
    (t) => {
      const flap = Math.sin(t * 24) * 0.6
      return (p, v) => {
        if (p.part) v[2] += Math.abs(p.x) * flap * (p.part % 2 ? 1 : -0.8)
        v[1] += Math.sin(t * 1.5) * 0.1
        v[0] += Math.sin(t * 0.9) * 0.06
      }
    },
    [0.08, 0.27]
  )
}
