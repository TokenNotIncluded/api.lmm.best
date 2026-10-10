/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useReducedMotion } from 'motion/react'
import { useEffect, useMemo, useState, type CSSProperties } from 'react'

/**
 * A short celebratory burst shown when a notification is cleared.
 *
 * Kept dependency-free: a couple of dozen colour blocks animated by one CSS
 * keyframe. It is purely decorative, so a user who prefers reduced motion gets
 * no confetti at all — the caller still renders the final read state.
 */
type Particle = {
  id: number
  x: number
  y: number
  rotation: number
  size: number
  delay: number
  duration: number
  color: string
  shape: 'square' | 'round'
}

/** Semantic tokens only, so the burst tracks light/dark like everything else. */
const COLORS = [
  'var(--primary)',
  'var(--success)',
  'var(--warning)',
  'var(--info)',
  'var(--chart-1)',
  'var(--chart-4)',
]

const COUNT = 26
const LIFETIME_MS = 850

function buildParticles(count: number): Particle[] {
  return Array.from({ length: count }, (_, id) => {
    // An upward fan, so the blocks read as a small pop beside the row rather
    // than an all-directions starburst that would crowd it.
    const angle = -Math.PI / 2 + (Math.random() - 0.5) * 2.2
    const distance = 42 + Math.random() * 74
    return {
      id,
      x: Math.cos(angle) * distance,
      y: Math.sin(angle) * distance * 0.6,
      rotation: (Math.random() - 0.5) * 540,
      size: 3 + Math.random() * 4,
      delay: Math.random() * 70,
      duration: 520 + Math.random() * 300,
      color: COLORS[id % COLORS.length],
      shape: id % 3 === 0 ? 'round' : 'square',
    }
  })
}

export function TodoBurst({ burstKey }: { burstKey: number | null }) {
  const shouldReduceMotion = useReducedMotion()
  // Every increment of the key re-rolls the layout, not just the null/set edge,
  // so a counter that keeps incrementing (repeated "mark as read") still pops.
  const particles = useMemo(
    () =>
      burstKey !== null && !shouldReduceMotion ? buildParticles(COUNT) : [],
    [burstKey, shouldReduceMotion]
  )

  if (!particles.length) return null

  return (
    <span
      aria-hidden='true'
      data-slot='todo-burst'
      className='pointer-events-none absolute top-1/2 left-2 z-10 block size-0'
    >
      {particles.map((particle) => (
        <span
          key={`${burstKey}-${particle.id}`}
          className={
            particle.shape === 'round'
              ? 'todo-burst-piece absolute block rounded-full'
              : 'todo-burst-piece absolute block rounded-[1px]'
          }
          style={
            {
              width: `${particle.size}px`,
              height: `${particle.size}px`,
              backgroundColor: particle.color,
              animation: `todo-burst-fly ${particle.duration}ms cubic-bezier(0.25, 0.8, 0.35, 1) ${particle.delay}ms forwards`,
              '--burst-x': `${particle.x}px`,
              '--burst-y': `${particle.y}px`,
              '--burst-rotate': `${particle.rotation}deg`,
            } as CSSProperties
          }
        />
      ))}
    </span>
  )
}

/** The confetti lifetime, so callers can retire the trigger value in step. */
export const TODO_BURST_LIFETIME_MS = LIFETIME_MS

/**
 * Owns the `todo-burst-fly` keyframes for the whole page. Mounted once by the
 * feed so the particles carry no global stylesheet dependency of their own.
 */
export function TodoBurstStyles() {
  const [mounted, setMounted] = useState(false)
  useEffect(() => {
    setMounted(true)
    return () => setMounted(false)
  }, [])
  if (!mounted) return null
  return (
    <style>{`@keyframes todo-burst-fly{0%{opacity:1;transform:translate(0,0) scale(1) rotate(0deg)}100%{opacity:0;transform:translate(var(--burst-x),var(--burst-y)) scale(0.35) rotate(var(--burst-rotate))}}`}</style>
  )
}
