/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useReducedMotion } from 'motion/react'
import { useEffect, useMemo, type CSSProperties } from 'react'

import { cn } from '@/lib/utils'

/**
 * Decoration for the one-time key reveal: a brief ring of sparks behind the
 * "copy" action so the single unrepeatable moment in the key lifecycle reads
 * as an event rather than another form result.
 *
 * Purely decorative and dependency-free (CSS transforms over a few dozen
 * spans). When the user prefers reduced motion nothing is rendered at all —
 * the caller keeps its final state and only loses the confetti.
 */
const PARTICLE_COUNT = 28
const LIFETIME_MS = 1100

/** Semantic tokens only, so the burst tracks light/dark like everything else. */
const COLORS = [
  'var(--primary)',
  'var(--success)',
  'var(--info)',
  'var(--warning)',
  'var(--chart-1)',
  'var(--chart-2)',
]

type Particle = {
  id: number
  angle: number
  distance: number
  rotation: number
  size: number
  delay: number
  duration: number
  color: string
}

function createParticles(): Particle[] {
  return Array.from({ length: PARTICLE_COUNT }, (_, id) => {
    const spread = (id / PARTICLE_COUNT) * Math.PI * 2
    return {
      id,
      // Even ring plus jitter so it never reads as a mechanical starburst.
      angle: spread + (Math.random() - 0.5) * 0.5,
      distance: 30 + Math.random() * 64,
      rotation: (Math.random() - 0.5) * 540,
      size: 3 + Math.random() * 4,
      delay: Math.random() * 70,
      duration: 480 + Math.random() * 340,
      color: COLORS[id % COLORS.length],
    }
  })
}

/**
 * @param burstKey A new value replays the burst; `null` renders nothing.
 */
export function KeyCreatedBurst({
  burstKey,
  onDone,
  className,
}: {
  burstKey: number | null
  onDone?: () => void
  className?: string
}) {
  const shouldReduceMotion = useReducedMotion()
  const active = burstKey !== null && !shouldReduceMotion
  const particles = useMemo(
    // Re-roll the layout for every burst.
    () => (active ? createParticles() : []),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- burstKey is a replay nonce: each new value must re-roll the particles.
    [active, burstKey]
  )

  useEffect(() => {
    if (!active) return
    const timer = window.setTimeout(() => onDone?.(), LIFETIME_MS)
    return () => window.clearTimeout(timer)
  }, [active, burstKey, onDone])

  // Reduced motion: finish immediately, render no decoration.
  useEffect(() => {
    if (burstKey !== null && shouldReduceMotion) onDone?.()
  }, [burstKey, onDone, shouldReduceMotion])

  if (!particles.length) return null

  return (
    <span
      aria-hidden='true'
      data-slot='key-created-burst'
      className={cn(
        'pointer-events-none absolute inset-0 z-10 flex items-center justify-center',
        className
      )}
    >
      {particles.map((particle) => {
        const x = Math.cos(particle.angle) * particle.distance
        const y = Math.sin(particle.angle) * particle.distance
        return (
          <span
            key={`${burstKey}-${particle.id}`}
            className='absolute block rounded-[1px]'
            style={
              {
                width: `${particle.size}px`,
                height: `${particle.size}px`,
                backgroundColor: particle.color,
                animation: `key-created-burst-fly ${particle.duration}ms cubic-bezier(0.2, 0.7, 0.3, 1) ${particle.delay}ms both`,
                '--burst-x': `${x}px`,
                '--burst-y': `${y}px`,
                '--burst-rotate': `${particle.rotation}deg`,
              } as CSSProperties
            }
          />
        )
      })}
    </span>
  )
}

/** Mounted once with the sheet so the keyframes live beside their particles. */
export function KeyCreatedBurstStyles() {
  return (
    <style>{`
@keyframes key-created-burst-fly {
  0% {
    opacity: 1;
    transform: translate(0, 0) scale(1) rotate(0deg);
  }
  100% {
    opacity: 0;
    transform: translate(var(--burst-x), var(--burst-y)) scale(0.35) rotate(var(--burst-rotate));
  }
}
`}</style>
  )
}
