/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
type Point = { x: number; y: number }
export const L0_MAX_FLIGHTS = 28
export const L0_ARRIVAL_DURATION = 320

/** Sample a continuous curve instead of changing direction at one hard corner. */
export function l0FlightFrames(
  from: Point,
  to: Point,
  up: boolean,
  lane: number
) {
  const dy = to.y - from.y
  const bend = ((lane % 3) - 1) * Math.min(30, Math.abs(dy) * 0.1)
  const p1 = { x: from.x + bend, y: from.y + dy * 0.3 }
  const p2 = { x: to.x - bend, y: to.y - dy * 0.3 }
  return Array.from({ length: 13 }, (_, index) => {
    const t = index / 12
    const s = 1 - t
    const x =
      s ** 3 * from.x +
      3 * s * s * t * p1.x +
      3 * s * t * t * p2.x +
      t ** 3 * to.x
    const y =
      s ** 3 * from.y +
      3 * s * s * t * p1.y +
      3 * s * t * t * p2.y +
      t ** 3 * to.y
    const scale = up ? 1 - t * 0.45 : 0.82 + t * 0.18
    const opacity = up ? Math.min(1, (1 - t) * 2.8) : Math.min(1, t * 5)
    return {
      transform: `translate3d(${x}px,${y}px,0) scale(${scale})`,
      opacity,
      offset: t,
    }
  })
}
