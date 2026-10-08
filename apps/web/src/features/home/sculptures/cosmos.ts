/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  Shape,
  TAU,
  hash,
  palette as C,
  rotate,
  tint,
  type Sculpture,
  type Vec3,
} from './geometry'

// Deliberately low-resolution silhouettes, not a map or a geography data set.
const continents: number[][][] = [
  [
    [-17, 37],
    [10, 37],
    [34, 30],
    [43, 12],
    [51, 10],
    [40, -13],
    [31, -30],
    [19, -35],
    [10, -18],
    [8, 4],
    [-16, 15],
  ],
  [
    [-10, 36],
    [-10, 58],
    [8, 70],
    [48, 70],
    [85, 76],
    [135, 69],
    [175, 64],
    [146, 45],
    [129, 34],
    [117, 21],
    [106, -5],
    [95, 6],
    [79, 7],
    [67, 25],
    [45, 12],
    [35, 30],
    [22, 39],
  ],
  [
    [-168, 67],
    [-144, 72],
    [-107, 74],
    [-63, 56],
    [-53, 48],
    [-82, 25],
    [-89, 16],
    [-104, 22],
    [-117, 32],
    [-128, 52],
    [-163, 59],
  ],
  [
    [-81, 12],
    [-61, 11],
    [-35, -6],
    [-43, -23],
    [-66, -55],
    [-75, -46],
    [-70, -20],
    [-81, -4],
  ],
  [
    [113, -21],
    [130, -12],
    [143, -11],
    [153, -27],
    [146, -39],
    [132, -34],
    [115, -35],
  ],
  [
    [-54, 59],
    [-23, 71],
    [-19, 81],
    [-45, 84],
    [-63, 76],
  ],
  [
    [47, -13],
    [50, -16],
    [46, -25],
    [43, -24],
  ],
]
function inside(x: number, y: number, polygon: number[][]) {
  let hit = false
  for (let i = 0, j = polygon.length - 1; i < polygon.length; j = i++) {
    const [ax, ay] = polygon[i],
      [bx, by] = polygon[j]
    if (ay > y !== by > y && x < ((bx - ax) * (y - ay)) / (by - ay) + ax)
      hit = !hit
  }
  return hit
}
function sphereSample(i: number, count: number): Vec3 {
  const y = 1 - (2 * (i + 0.5)) / count,
    r = Math.sqrt(1 - y * y),
    a = i * 2.3999632297
  return [Math.cos(a) * r, y, Math.sin(a) * r]
}
export function earth(): Sculpture {
  const s = new Shape()
  for (let i = 0; i < 24000; i++) {
    const p = sphereSample(i, 24000),
      lat = (Math.asin(p[1]) * 180) / Math.PI,
      lon = (Math.atan2(p[0], p[2]) * 180) / Math.PI
    const land = continents.some((polygon) => inside(lon, lat, polygon))
    const color = Math.abs(lat) > 75 ? C.cream : land ? C.teal : C.blue
    s.add(p, tint(color, 0.66 + hash(i) * 0.18 + (land ? 0.05 : 0)), 0)
    const cloud =
      Math.sin(lon * 0.12 + Math.sin(lat * 0.1) * 3) +
      Math.cos(lat * 0.16 - lon * 0.045)
    if (cloud > 1.42 && Math.abs(lat) < 71)
      s.add(p.map((x) => x * 1.023) as Vec3, tint(C.cream, 0.91), 1)
  }
  return s.model(
    (t) => {
      const ground = rotate(1, t * 0.16),
        sky = rotate(1, t * 0.18)
      return (p, v) => (p.part ? sky : ground)(p, v)
    },
    [0, 0.13]
  )
}
function rocky(mars: boolean): Sculpture {
  const s = new Shape()
  const craters = Array.from({ length: 34 }, (_, i) => ({
    center: sphereSample(i, 34),
    radius: 0.05 + hash(i + 10) * 0.18,
  }))
  for (let i = 0; i < 23500; i++) {
    const p = sphereSample(i, 23500)
    let relief = 0,
      shade = 0.8 + hash(i) * 0.16
    for (const crater of craters) {
      const dot =
        p[0] * crater.center[0] +
        p[1] * crater.center[1] +
        p[2] * crater.center[2]
      const distance = Math.sqrt(Math.max(0, 2 - 2 * dot)) / crater.radius
      if (distance < 1.2) {
        relief +=
          distance < 0.8
            ? -0.02 * (1 - distance)
            : Math.sin(((distance - 0.8) / 0.4) * Math.PI) * 0.009
        shade *= distance < 0.8 ? 0.65 + distance * 0.24 : 1.05
      }
    }
    if (mars)
      shade *= 0.82 + 0.16 * Math.sin(p[0] * 8 + p[2] * 5) * Math.cos(p[1] * 11)
    const color: Vec3 = mars
      ? p[1] > 0.93
        ? C.cream
        : C.rust
      : [203, 200, 192]
    s.add(p.map((x) => x * (1 + relief)) as Vec3, tint(color, shade))
  }
  return s.model((t) => rotate(1, t * (mars ? 0.16 : 0.11)), [0, 0.1])
}
export const moon = () => rocky(false)
export const mars = () => rocky(true)
export function sun(): Sculpture {
  const s = new Shape()
  for (let i = 0; i < 21000; i++) {
    const p = sphereSample(i, 21000),
      grain = hash(i)
    s.add(
      p.map((x) => x * 0.84) as Vec3,
      tint(grain > 0.5 ? C.gold : C.rust, 0.72 + grain * 0.28)
    )
  }
  for (let flare = 0; flare < 18; flare++) {
    const a = (flare / 18) * TAU
    s.tube(
      (t) => {
        const angle = a + (t - 0.5) * 0.5,
          r = 0.79 + Math.sin(t * Math.PI) * (0.17 + hash(flare) * 0.25)
        return [
          Math.cos(angle) * r,
          Math.sin(angle) * r,
          Math.sin(t * TAU) * 0.09,
        ]
      },
      0.012,
      flare % 3 ? C.rust : C.gold,
      1,
      65,
      6
    )
  }
  return s.model((t) => {
    const turn = rotate(1, t * 0.12),
      corona = rotate(2, -t * 0.05)
    return (p, v) => {
      if (p.part) {
        const pulse = 1 + Math.sin(t * 2 + p.x * 5) * 0.035
        v[0] *= pulse
        v[1] *= pulse
        corona(p, v)
      } else turn(p, v)
    }
  })
}
export function solarSystem(): Sculpture {
  const s = new Shape()
  s.ellipsoid([0, 0, 0], [0.21, 0.21, 0.21], C.gold, 0, 3000)
  const colors = [
    C.cream,
    C.gold,
    C.blue,
    C.rust,
    C.gold,
    C.cream,
    C.teal,
    C.blue,
  ]
  for (let orbit = 0; orbit < 8; orbit++) {
    const radius = 0.38 + orbit * 0.14,
      a = orbit * 2.1
    s.ring([0, 0, 0], radius, 0.005, tint(C.violet, 0.52), 0, 1)
    const center: Vec3 = [Math.cos(a) * radius, 0, Math.sin(a) * radius],
      size = orbit < 4 ? 0.035 + orbit * 0.007 : 0.075 - (orbit - 4) * 0.009
    s.ellipsoid(center, [size, size, size], colors[orbit], orbit + 1, 450)
    if (orbit === 5) s.ring(center, size * 1.7, 0.009, C.gold, orbit + 1, 1)
  }
  return s.model(
    (t) => {
      const turns = colors.map((_, i) => rotate(1, t * (0.55 / (1 + i * 0.42))))
      return (p, v) => {
        if (p.part) turns[p.part - 1](p, v)
      }
    },
    [0.05, 0.64]
  )
}
export function galaxy(): Sculpture {
  const s = new Shape()
  for (let i = 0; i < 23000; i++) {
    const r = Math.sqrt(hash(i + 1)) * 1.31,
      arm = i % 4,
      a = (arm / 4) * TAU + r * 4.9 + (hash(i + 900) - 0.5) * (0.28 + r * 0.35)
    s.add(
      [
        Math.cos(a) * r,
        (hash(i + 500) - 0.5) * (0.04 + (1.3 - r) * 0.07),
        Math.sin(a) * r,
      ],
      tint(
        i % 7 === 0 ? C.rose : i % 5 === 0 ? C.gold : C.violet,
        0.44 + hash(i + 60) * 0.56
      )
    )
  }
  s.ellipsoid([0, 0, 0], [0.29, 0.11, 0.29], C.cream, 0, 4000)
  return s.model((t) => rotate(1, t * 0.07), [0.05, 0.65])
}
export function blackHole(): Sculpture {
  const s = new Shape()
  for (let i = 0; i < 18000; i++) {
    const r = 0.43 + hash(i) * 0.88,
      a = hash(i + 31) * TAU
    s.add(
      [Math.cos(a) * r, 0, Math.sin(a) * r],
      tint(i % 5 ? C.gold : C.rust, (1.28 - r) * 0.72 + 0.19),
      1
    )
  }
  s.ellipsoid([0, 0, 0], [0.38, 0.38, 0.38], [3, 4, 7], 0, 7000)
  s.ring([0, 0, 0.02], 0.403, 0.018, C.gold)
  s.tube(
    (t) => [Math.cos(t * Math.PI) * 0.52, Math.sin(t * Math.PI) * 0.5, -0.18],
    0.028,
    C.cream,
    0,
    170,
    8
  )
  return s.model((t) => {
    const turn = rotate(1, t * 0.38)
    return (p, v) => {
      if (p.part) {
        turn(p, v)
        v[1] = v[2] * 0.24
        v[2] *= 0.95
      }
    }
  })
}
