/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  Shape,
  TAU,
  hash,
  rotate,
  tint,
  type Sculpture,
  type Vec3,
} from './geometry'

/** Crater-rich far-side interpretation, not a surveyed lunar elevation map. */
export function moonFarSide(): Sculpture {
  const s = new Shape()
  const craters = Array.from({ length: 72 }, (_, i) => {
    const y = 1 - 2 * hash(i + 71),
      a = hash(i + 503) * TAU
    const r = Math.sqrt(1 - y * y)
    return {
      x: Math.cos(a) * r,
      y,
      z: Math.sin(a) * r,
      radius: 0.035 + hash(i + 912) ** 2 * 0.2,
    }
  })
  for (let i = 0; i < 28000; i++) {
    const y = 1 - (2 * (i + 0.5)) / 28000
    const r = Math.sqrt(1 - y * y),
      a = i * 2.3999632297
    const x = Math.cos(a) * r,
      z = Math.sin(a) * r
    let relief = 0,
      shade = 1
    for (const crater of craters) {
      const d2 = 2 - 2 * (x * crater.x + y * crater.y + z * crater.z)
      if (d2 > crater.radius ** 2 * 1.5) continue
      const d = Math.sqrt(Math.max(0, d2)) / crater.radius
      const bowl = Math.max(0, 1 - d * d)
      const rim = Math.exp(-(((d - 1) / 0.13) ** 2))
      relief += crater.radius * (rim * 0.16 - bowl * 0.24)
      shade *= 1 - bowl * 0.32 + rim * 0.16
    }
    // A subdued southern basin; the far side stays predominantly cratered highlands.
    const basin =
      Math.max(0, 1 - ((x + 0.18) ** 2 + (y + 0.49) ** 2) / 0.27) *
      Math.max(0, z)
    const light =
      0.31 + 0.65 * Math.max(0, -x * 0.44 + y * 0.35 + z * 0.76)
    const albedo: Vec3 = [209, 207, 200]
    s.add(
      [x * (1 + relief), y * (1 + relief), z * (1 + relief)],
      tint(
        albedo,
        Math.min(
          1.15,
          light * shade * (0.94 + hash(i) * 0.08) * (1 - basin * 0.24)
        )
      )
    )
  }
  return s.model((t) => {
    const spin = rotate(2, t * 0.16)
    const turn = rotate(1, Math.sin(t * 0.27) * 0.22)
    return (p, v) => {
      spin(p, v)
      turn(p, v)
    }
  })
}
