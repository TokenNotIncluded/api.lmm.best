/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
export const HOME_WORLD_NAMES = [
  'home',
  'store',
  'tools',
  'ecosystem',
  'future',
] as const

/** Matches the native-scroll panels, including the exact end of the runway. */
export function homeWorldChapter(progress: number) {
  const finite = Number.isFinite(progress) ? progress : 0
  return Math.min(4, Math.max(0, Math.floor(finite * 5)))
}

/** A bounded overlap around each native-scroll boundary, identical in reverse. */
export function homeWorldFrame(progress: number, count = 5) {
  const p = Math.min(1, Math.max(0, Number.isFinite(progress) ? progress : 0))
  const position = p * count
  const boundary = Math.ceil(position)
  const windowSize = 0.45
  if (
    boundary > 0 &&
    boundary < count &&
    position > boundary - windowSize &&
    position < boundary
  ) {
    const fraction = (position - boundary + windowSize) / windowSize
    const mix = fraction * fraction * (3 - 2 * fraction)
    return {
      from: boundary - 1,
      to: boundary,
      mix,
      chapter: mix < 0.5 ? boundary - 1 : boundary,
    }
  }
  const chapter = Math.min(count - 1, Math.floor(position))
  return { from: chapter, to: chapter, mix: 0, chapter }
}

/** Time-based damping converges in about 600ms, without queued wheel handlers. */
export function smoothWorldProgress(
  current: number,
  target: number,
  elapsedMs: number
) {
  const value =
    current +
    (target - current) *
      (1 -
        Math.exp(
          -(Number.isFinite(elapsedMs) ? Math.max(0, elapsedMs) : 0) / 170
        ))
  return Math.abs(value - target) < 0.0001 ? target : value
}
