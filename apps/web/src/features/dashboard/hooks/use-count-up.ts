/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useEffect, useRef, useState } from 'react'

const COUNT_UP_DURATION_MS = 320

function prefersReducedMotion(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return false
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function easeOutCubic(progress: number): number {
  return 1 - (1 - progress) ** 3
}

/**
 * Counts a number up once, the first time a real value arrives.
 *
 * Money and quota figures must never animate on every refresh — this hook
 * latches to the first finite value it sees and stays put afterwards, so a
 * background refetch or a status change repaints instantly. Reduced motion
 * skips the animation entirely and renders the final value on the first frame.
 */
export function useCountUp(target: number | null | undefined, active = true) {
  const startedRef = useRef(false)
  const frameRef = useRef<number | null>(null)
  const [value, setValue] = useState<number | null>(null)

  useEffect(() => {
    if (frameRef.current !== null) {
      cancelAnimationFrame(frameRef.current)
      frameRef.current = null
    }

    const settled =
      typeof target === 'number' && Number.isFinite(target) ? target : null

    if (!active || settled === null) {
      setValue(settled)
      return
    }

    if (startedRef.current || prefersReducedMotion()) {
      startedRef.current = true
      setValue(settled)
      return
    }

    startedRef.current = true
    const startedAt = performance.now()

    const step = (now: number) => {
      const progress = Math.min(1, (now - startedAt) / COUNT_UP_DURATION_MS)
      if (progress >= 1) {
        frameRef.current = null
        setValue(settled)
        return
      }
      setValue(settled * easeOutCubic(progress))
      frameRef.current = requestAnimationFrame(step)
    }

    frameRef.current = requestAnimationFrame(step)

    return () => {
      if (frameRef.current !== null) {
        cancelAnimationFrame(frameRef.current)
        frameRef.current = null
      }
    }
  }, [active, target])

  return value
}
