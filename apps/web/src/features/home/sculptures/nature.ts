/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  Shape, TAU, hash, mix, palette as C, rotate, tint,
  type Sculpture, type Vec3,
} from './geometry'
/** Petal membranes and their veins share the same opening transform. */

function petal(
  s: Shape,
  sample: (u: number, v: number) => Vec3,
  color: (u: number, v: number) => Vec3,
  closed: (p: Vec3) => Vec3
) {
  const start = s.points.length
  s.surface(
    52,
    31,
    sample,
    (u, v) => {
      const a = sample(Math.max(0, u - 0.002), v),
        b = sample(Math.min(1, u + 0.002), v),
        c = sample(u, Math.max(0, v - 0.002)),
        d = sample(u, Math.min(1, v + 0.002))
      const ux = b[0] - a[0],
        uy = b[1] - a[1],
        uz = b[2] - a[2],
        vx = d[0] - c[0],
        vy = d[1] - c[1],
        vz = d[2] - c[2]
      const nx = uy * vz - uz * vy,
        ny = uz * vx - ux * vz,
        nz = ux * vy - uy * vx,
        length = Math.hypot(nx, ny, nz)
      const light = length ? Math.abs((-nx * 0.32 + ny * 0.42 + nz * 0.84) / length) : 0.7
      return tint(color(u, v), 0.65 + light * 0.35)
    },
    1
  )
  for (const v of [0.18, 0.5, 0.82]) {
    s.tube(
      (u) => sample(0.08 + u * 0.88, v),
      0.0025,
      tint(color(0.65, v), 0.83),
      1,
      35,
      3
    )
  }
  s.tube((v) => sample(0.985, v), 0.006, tint(color(0.98, 0.3), 1.16), 1, 44, 4)
  s.tube(
    (u) => sample(0.18 + u * 0.8, 0.025),
    0.0035,
    tint(color(0.6, 0), 0.55),
    1,
    38,
    3
  )
  for (let i = start; i < s.points.length; i++) {
    const p = s.points[i]
    p.closed = closed([p.x, p.y, p.z])
  }
}

export function lotus(): Sculpture {
  const s = new Shape()
  s.tube((t) => [Math.sin(t * 2.8) * 0.06, -1.26 + t * 1.44, -0.04], 0.027, C.teal)
  // A folded lily pad with a real notch and radial veins, behind the bloom.
  const leaf = (u: number, v: number): Vec3 => {
    const a = 0.22 + v * (TAU - 0.44), r = u * 0.57
    return [
      -0.45 + Math.cos(a) * r,
      -0.64 + Math.sin(a) * r * 0.27 + u * u * 0.08,
      -0.08 + Math.sin(a) * r * 0.64
    ]
  }
  s.surface(
    36,
    80,
    leaf,
    (u, v) => tint(C.teal, 0.6 + 0.22 * u + 0.12 * Math.cos(v * TAU))
  )
  for (let vein = 0; vein < 11; vein++) {
    s.tube((u) => leaf(u, vein / 10), 0.006, tint(C.gold, 0.76), 0, 30, 4)
  }
  for (let layer = 0; layer < 3; layer++) {
    const petals = 9 - layer * 2,
      length = [1.02, 0.73, 0.43][layer],
      lift = [0.14, 0.41, 0.69][layer]
    for (let index = 0; index < petals; index++) {
      const angle = index / petals * TAU + layer * 0.45
      petal(
        s,
        (u, v) => {
          const q = v * 2 - 1,
            r = 0.04 + u * length,
            width = q * Math.sin(Math.PI * u) ** 0.85 * (0.4 - layer * 0.065)
          return [
            Math.cos(angle) * r - Math.sin(angle) * width,
            0.15 + layer * 0.045 + u ** 1.8 * lift - Math.sin(Math.PI * u) * (0.13 + q * q * 0.06),
            Math.sin(angle) * r + Math.cos(angle) * width,
          ]
        },
        (u, v) => {
          const rim = Math.abs(v * 2 - 1) ** 2,
            light = 0.82 + 0.12 * Math.cos(angle) + 0.06 * Math.sin(v * Math.PI)
          return tint([249 - u * 36, 193 - u * 96 - rim * 24, 211 - u * 64 - rim * 13], light)
        },
        ([x, y, z]) => [x * 0.32, y * 0.91 + Math.hypot(x, z) * 0.57, z * 0.32]
      )
    }
  }
  s.ellipsoid([0, 0.26, 0], [0.15, 0.09, 0.15], C.gold, 0, 600)
  for (let i = 0; i < 28; i++) {
    const a = i * 2.399963, r = 0.08 + hash(i) * 0.09
    s.line(
      [Math.cos(a) * r, 0.24, Math.sin(a) * r],
      [Math.cos(a) * r * 1.1, 0.35 + hash(i + 3) * 0.08, Math.sin(a) * r * 1.1],
      0.008,
      C.gold,
      0,
      12
    )
  }
  return s.model((t) => {
    const open = 0.78 + Math.sin(t * TAU / 6.4) * 0.2
    return (p, v) => {
      if (p.closed) for (let i = 0; i < 3; i++) v[i] = mix(p.closed[i], v[i], open)
      v[0] += Math.sin(t * 0.9) * (p.y + 1.26) * 0.016
    }
  }, [0.12, -0.56])
}

export function rose(): Sculpture {
  const s = new Shape()
  s.tube(
    (t) => [Math.sin(t * 2.6) * 0.09, -1.26 + t * 1.69, -0.12],
    0.024,
    tint(C.teal, 0.8)
  )
  for (const side of [-1, 1]) {
    const leaf = (u: number, v: number): Vec3 => {
      const q = v * 2 - 1
      return [
        0.07 + side * u * 0.56,
        -0.64 + (side > 0 ? 0.2 : 0) + u * 0.3 + q * Math.sin(Math.PI * u) * 0.15,
        -0.09 + Math.sin(Math.PI * u) * 0.07 - q * q * 0.035
      ]
    }
    s.surface(38, 22, leaf, (u, v) => tint(C.teal, 0.6 + Math.sin(v * Math.PI) * 0.3))
    s.tube((u) => leaf(u, 0.5), 0.009, tint(C.gold, 0.77), 0, 45, 5)
    for (let rib = 1; rib < 7; rib++) for (const edge of [0, 1]) {
      s.tube(
        (t) => leaf(rib / 8 + t * 0.12, mix(0.5, edge, t)),
        0.004,
        tint(C.teal, 1.08),
        0,
        12,
        3
      )
    }
  }
  // Offset whorls curl around the centre rather than forming a flat red disk.
  for (let layer = 0; layer < 4; layer++) {
    const petals = 7 - layer,
      radius = [0.73, 0.56, 0.37, 0.2][layer]
    for (let index = 0; index < petals; index++) {
      const angle = index / petals * TAU + layer * 0.73
      petal(
        s,
        (u, v) => {
          const q = v * 2 - 1,
            a = angle + q * (0.64 + layer * 0.035) * Math.sin(u * Math.PI * 0.68) ** 0.6 + u * 0.34,
            r = radius * (0.18 + u * 0.82) * (1 - q * q * 0.22)
          return [
            Math.cos(a) * r,
            0.48 + Math.sin(a) * r * 0.9,
            layer * 0.085 + Math.sin(u * Math.PI) * (0.28 - layer * 0.04) - u ** 5 * 0.12 + q * q * u * 0.065,
          ]
        },
        (u, v) => {
          const light = 0.8 + Math.sin(v * Math.PI) * 0.15 + Math.cos(angle) * 0.05
          return tint(
            [201 + u * 47, 39 + u ** 3 * 97 + Math.abs(v - 0.5) * 20, 76 + u ** 3 * 100],
            light
          )
        },
        ([x, y, z]) => [x * 0.54, 0.48 + (y - 0.48) * 0.62, z + Math.hypot(x, y - 0.48) * 0.25]
      )
    }
  }
  s.tube((t) => {
    const a = t * TAU * 1.8, r = 0.013 + t * 0.08
    return [Math.cos(a) * r, 0.48 + Math.sin(a) * r, 0.49 - t * 0.045]
  }, 0.018, tint(C.rose, 0.88), 0, 100, 8)
  return s.model((t) => {
    const open = 0.83 + Math.sin(t * TAU / 7.2) * 0.15
    return (p, v) => {
      if (p.closed) for (let i = 0; i < 3; i++) v[i] = mix(p.closed[i], v[i], open)
      v[0] += Math.sin(t * 0.85) * (p.y + 1.26) * 0.02
    }
  }, [0.22, 0.15])
}

export function fish(): Sculpture {
  const s = new Shape()
  const body = (u: number, a: number, lift = 0): Vec3 => {
    const r = Math.sin(Math.PI * u) ** 0.64 * (0.88 + u * 0.2)
    return [
      -0.82 + u * 1.69,
      Math.sin(a) * (r * 0.37 + lift),
      Math.cos(a) * (r * 0.28 + lift)
    ]
  }
  s.surface(
    130,
    80,
    (u, v) => body(u, v * TAU),
    (u, v) => {
      const patch = Math.sin(u * 19 + v * 13) + Math.cos(v * 24 - u * 11)
      return tint(
        patch > 0.48 ? C.rust : C.cream,
        0.69 + 0.28 * Math.max(0, Math.cos(v * TAU - 0.5))
      )
    }
  )
  // Crescent scales sit just above the body surface, not a painted noise mask.
  for (let row = 0; row < 12; row++) for (let col = 0; col < 14; col++) {
    const u = 0.16 + row * 0.047,
      a = (col + (row % 2) * 0.5) / 14 * TAU
    s.tube(
      (t) => body(u + Math.sin(t * Math.PI) * 0.025, a + (t - 0.5) * 0.38, 0.005),
      0.0035,
      tint(C.gold, 0.8),
      0,
      13,
      3
    )
  }
  const tail = (u: number, v: number): Vec3 => {
    const q = v * 2 - 1
    return [
      -0.76 - u * (0.44 + Math.abs(q) ** 0.7 * 0.35),
      q * (0.055 + u * 0.59),
      Math.sin(u * Math.PI) * q * 0.05
    ]
  }
  s.surface(
    70,
    45,
    tail,
    (u, v) => tint(C.rose, 0.66 + 0.28 * Math.sin(v * Math.PI) + u * 0.06),
    1
  )
  for (let ray = 0; ray < 13; ray++) {
    s.tube((u) => tail(u, ray / 12), 0.006, ray % 3 ? C.rust : C.cream, 1, 55, 4)
  }
  const dorsal = (u: number, v: number): Vec3 => [
    -0.51 + u * 0.97,
    0.24 + Math.sin(u * Math.PI) * (0.11 + v * 0.31),
    v * 0.015
  ]
  s.surface(55, 23, dorsal, C.gold, 2)
  for (let ray = 1; ray < 12; ray++) s.tube((v) => dorsal(ray / 12, v), 0.005, C.rust, 2, 22, 3)
  for (const side of [-1, 1]) {
    const fin = (u: number, v: number): Vec3 => [
      0.19 - u * 0.48,
      -0.17 - Math.sin(u * Math.PI) * v * 0.41,
      side * (0.19 + u * 0.28)
    ]
    s.surface(36, 23, fin, (u) => tint(C.rose, 0.72 + u * 0.25), 2)
    for (let ray = 1; ray < 7; ray++) s.tube((u) => fin(u, ray / 6), 0.004, C.cream, 2, 24, 3)
    s.ellipsoid([0.66, 0.095, side * 0.18], [0.061, 0.062, 0.039], C.gold, 0, 340)
    s.ellipsoid([0.667, 0.098, side * 0.211], [0.034, 0.038, 0.018], C.ink, 0, 180)
    s.ellipsoid([0.676, 0.113, side * 0.225], [0.012, 0.015, 0.009], C.cream, 0, 60)
    s.tube(
      (t) => [
        0.46 + Math.sin(t * Math.PI) * 0.067,
        0.23 - t * 0.43,
        side * (0.19 + Math.sin(t * Math.PI) * 0.08)
      ],
      0.011,
      C.rust,
      0,
      46,
      5
    )
    s.tube(
      (t) => [
        0.825 + t * 0.075,
        -0.05 - Math.sin(t * Math.PI / 2) * 0.12,
        side * (0.035 + t * 0.11)
      ],
      0.009,
      C.gold,
      0,
      25,
      5
    )
  }
  s.ellipsoid([0.845, -0.018, 0], [0.034, 0.047, 0.064], C.rust, 0, 220)
  return s.model(
    (t) => (p, v) => {
      const tailWeight = Math.max(0, 0.65 - p.x)
      v[2] += Math.sin(t * 4.4 + p.x * 3.5) * tailWeight * tailWeight * 0.11
      v[1] += Math.sin(t * 1.2) * 0.08 + (p.part === 2 ? Math.sin(t * 5 + p.x * 4) * 0.05 : 0)
      v[0] += 0.24 + Math.sin(t * 0.7) * 0.1
    // Keep the long tail inside the narrow canvas throughout its stroke.
    for (let axis = 0; axis < 3; axis++) v[axis] *= 0.92
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
      const a = u * TAU, r = Math.sin(v * Math.PI * 0.5) * 0.8
      return [
        Math.cos(a) * r,
        0.26 + Math.cos(v * Math.PI * 0.5) * 0.66,
        Math.sin(a) * r
      ]
    },
    (u, v) => tint(
      v > 0.88 ? C.rose : C.violet,
      0.55 + 0.4 * Math.pow(Math.cos(u * TAU * 8), 8) + v * 0.08
    )
  )
  s.ring([0, 0.26, 0], 0.8, 0.023, C.rose, 0, 1)
  for (let arm = 0; arm < 20; arm++) {
    const a = arm / 20 * TAU, radius = arm % 3 === 0 ? 0.24 : 0.61
    s.tube(
      (t) => [
        Math.cos(a) * radius + Math.sin(t * 8 + a) * 0.055,
        0.27 - t * (1.22 + hash(arm) * 0.4),
        Math.sin(a) * radius + Math.cos(t * 9 + a) * 0.06
      ],
      arm % 3 === 0 ? 0.026 : 0.012,
      arm % 3 === 0 ? C.rose : C.teal,
      1,
      95,
      6
    )
  }
  return s.model((t) => {
    const pulse = Math.sin(t * 2.5), size = 0.93 + pulse * 0.08
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
  }, [0, 0.12])
}

export function dandelion(): Sculpture {
  const s = new Shape(), center: Vec3 = [0, 0.4, 0]
  s.tube((t) => [Math.sin(t * 2) * 0.08, -1.32 + t * 1.72, 0], 0.024, C.teal)
  s.ellipsoid(center, [0.11, 0.11, 0.11], C.gold, 0, 500)
  for (let seed = 0; seed < 150; seed++) {
    const y = 1 - 2 * (seed + 0.5) / 150,
      r = Math.sqrt(1 - y * y),
      a = seed * 2.399963
    const direction: Vec3 = [Math.cos(a) * r, y, Math.sin(a) * r]
    const tip: Vec3 = [direction[0] * 0.66, 0.4 + direction[1] * 0.66, direction[2] * 0.66]
    for (let i = 0; i < 16; i++) {
      s.add(
        [direction[0] * i / 24, 0.4 + direction[1] * i / 24, direction[2] * i / 24],
        tint(C.cream, 0.6),
        seed + 1
      )
    }
    const tangent: Vec3 = Math.abs(y) < 0.9 ? [-direction[2], 0, direction[0]] : [0, direction[2], -y]
    const length = Math.hypot(...tangent)
    const [ax, ay, az] = tangent.map((v) => v / length)
    const bx = direction[1] * az - direction[2] * ay
    const by = direction[2] * ax - direction[0] * az
    const bz = direction[0] * ay - direction[1] * ax
    for (let ray = 0; ray < 7; ray++) {
      const angle = ray / 7 * TAU
      for (let j = 0; j < 10; j++) {
        const spread = j / 9 * 0.11,
          c = Math.cos(angle) * spread,
          d = Math.sin(angle) * spread
        s.add(
          [
            tip[0] + ax * c + bx * d + direction[0] * spread * 0.35,
            tip[1] + ay * c + by * d + direction[1] * spread * 0.35,
            tip[2] + az * c + bz * d + direction[2] * spread * 0.35
          ],
          C.cream,
          seed + 1
        )
      }
    }
  }
  return s.model(
    (t) => {
      const sway = Math.sin(t * 1.1) * 0.026,
        wind = Math.pow((1 - Math.cos(t * 0.75)) / 2, 3)
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
    [[-0.86, -0.08, 0], [0.43, 0.34, 0.36]],
    [[-0.49, 0.18, 0], [0.48, 0.47, 0.46]],
    [[0.02, 0.33, 0], [0.57, 0.63, 0.48]],
    [[0.52, 0.11, 0], [0.52, 0.42, 0.4]],
    [[0.94, -0.08, 0], [0.35, 0.28, 0.31]],
    [[-0.14, -0.2, 0.17], [0.95, 0.26, 0.4]],
  ]
  lobes.forEach(([c, r], i) => s.ellipsoid(c, r, i % 2 ? C.cream : [203, 214, 228], i, 2600))
  return s.model((t) => (p, v) => {
    v[0] += Math.sin(t * 0.65) * 0.18
    v[1] += Math.sin(t + p.part * 0.6) * 0.035
    v[2] += Math.sin(t * 0.8 + p.x * 2) * 0.05
  }, [0.03, 0.06])
}

export function dragonfly(): Sculpture {
  const s = new Shape()
  for (let segment = 0; segment < 10; segment++) {
    const y = 0.06 - segment * 0.108, r = 0.057 - segment * 0.0032
    s.ellipsoid(
      [0, y, 0],
      [r, 0.074, r],
      segment % 2 ? C.teal : tint(C.teal, 0.69),
      0,
      240
    )
    s.tube(
      (t) => [Math.cos(t * TAU) * r * 0.85, y + 0.045, Math.sin(t * TAU) * r * 0.85],
      0.005,
      C.gold,
      0,
      28,
      4
    )
  }
  s.ellipsoid([0, 0.24, 0], [0.125, 0.2, 0.105], C.teal, 0, 1600)
  s.line([0, 0.12, 0.107], [0, 0.37, 0.082], 0.014, C.gold)
  s.ellipsoid([0, 0.47, 0.012], [0.13, 0.11, 0.09], C.gold, 0, 650)
  for (const side of [-1, 1]) {
    s.ellipsoid(
      [side * 0.097, 0.515, 0.056],
      [0.082, 0.078, 0.071],
      tint(C.teal, 0.75),
      0,
      500
    )
    s.ellipsoid([side * 0.117, 0.542, 0.108], [0.022, 0.019, 0.014], C.cream, 0, 100)
    for (let pair = 0; pair < 2; pair++) {
      const part = 1 + pair + (side > 0 ? 2 : 0), root = 0.29 - pair * 0.19
      const wing = (u: number, v: number): Vec3 => {
        const q = v * 2 - 1,
          width = Math.sin(Math.PI * u) ** 0.7 * (pair ? 0.19 : 0.14) * (1 - u * 0.28)
        return [
          side * (0.075 + u * 1.12),
          root + u * (pair ? -0.39 : 0.27) + q * width,
          Math.sin(u * Math.PI) * (1 - q * q) * 0.04
        ]
      }
      s.surface(
        72,
        24,
        wing,
        (u, v) => tint([188, 220, 215], 0.72 + Math.sin(v * Math.PI) * 0.2),
        part
      )
      for (const v of [0, 0.35, 0.65, 1]) s.tube((u) => wing(u, v), 0.007, C.teal, part, 72, 4)
      for (let rib = 1; rib < 15; rib++) {
        const u = rib / 16
        s.tube(
          (v) => wing(u + Math.sin(v * Math.PI) * 0.027, v),
          0.0045,
          tint(C.teal, 0.8),
          part,
          18,
          3
        )
      }
      s.tube((t) => wing(0.79 + t * 0.085, 0.08), 0.014, C.gold, part, 15, 5)
    }
    for (let leg = 0; leg < 3; leg++) {
      const root: Vec3 = [side * 0.07, 0.31 - leg * 0.09, 0.07],
        knee: Vec3 = [side * (0.21 + leg * 0.025), 0.13 - leg * 0.11, 0.13],
        foot: Vec3 = [side * (0.29 + leg * 0.02), 0.22 - leg * 0.16, 0.18]
      s.line(root, knee, 0.01, tint(C.gold, 0.8), 0, 18)
      s.line(knee, foot, 0.008, C.teal, 0, 18)
    }
  }
  return s.model(
    (t) => {
      const wings = Array.from(
        { length: 4 },
        (_, i) => {
          const side = i < 2 ? -1 : 1, pair = i % 2
          return rotate(
            1,
            side * Math.sin(t * 18 + pair * 0.7) * 0.52,
            [side * 0.075, 0.29 - pair * 0.19, 0]
          )
        }
      )
      const bank = rotate(2, -0.22 + Math.sin(t * 0.7) * 0.05)
      return (p, v) => {
        if (p.part) wings[p.part - 1](p, v)
        bank(p, v)
        v[1] += Math.sin(t * 1.5) * 0.06
        v[0] += Math.sin(t * 0.9) * 0.05
      }
    },
    [0.05, 0.34]
  )
}
