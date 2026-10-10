/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'

import {
  MAX_BALANCE_PARTICLES,
  MAX_PRESET_PARTICLES,
  createWalletTokenLayout,
  createWalletTokenSeed,
  walletCloudExtent,
  walletCloudParticleCount,
} from '../lib/wallet-token-cloud'

import './wallet-token-cloud.css'

export type WalletCloudSuccess = {
  orderId: number
  beforeCredits: number
  creditedCredits: number
}

function tokenPoint(
  tokens: ReturnType<typeof createWalletTokenLayout>,
  index: number,
  variant: 'balance' | 'preset',
  amount: number
) {
  const point = tokens[index]
  const mini = variant === 'preset'
  const extent = walletCloudExtent(amount, variant)
  // Longer generated fragments are scaled down a touch so they occupy
  // roughly the same visual weight as short symbol glyphs, keeping the
  // cloud's rhythm even instead of a few wide fragments dominating it.
  const glyphTrim = point.glyph.length > 2 ? 0.82 : 1
  return {
    x: (mini ? 45 : 210) + point.x * (mini ? 35 : 170) * extent,
    y: (mini ? 25 : 80) + point.y * (mini ? 19 : 67) * extent,
    fontSize:
      ((mini ? 7 : 9) + point.depth ** 1.15 * (mini ? 1.9 : 2.7)) * glyphTrim,
    opacity: 0.3 + point.depth ** 1.3 * 0.66,
    glyph: point.glyph,
  }
}

export function WalletTokenCloud({
  amount,
  variant = 'balance',
  success,
  onSuccessComplete,
}: {
  amount: number
  variant?: 'balance' | 'preset'
  success?: WalletCloudSuccess | null
  onSuccessComplete?: (orderId: number) => void
}) {
  const svgRef = useRef<SVGSVGElement>(null)
  const cloudRef = useRef<HTMLDivElement>(null)
  const [visible, setVisible] = useState(false)
  const playedRef = useRef<number | null>(null)
  const onCompleteRef = useRef(onSuccessComplete)
  useEffect(() => {
    onCompleteRef.current = onSuccessComplete
  }, [onSuccessComplete])
  useEffect(() => {
    if (variant !== 'balance') return
    const element = cloudRef.current
    if (!element) return
    let intersects = false
    const sync = () =>
      setVisible(intersects && document.visibilityState === 'visible')
    const observer =
      typeof IntersectionObserver === 'function'
        ? new IntersectionObserver(
            ([entry]) => {
              intersects =
                entry.isIntersecting && entry.intersectionRatio >= 0.25
              sync()
            },
            { threshold: [0, 0.25] }
          )
        : null
    const fallback = () => {
      const rect = element.getBoundingClientRect()
      intersects =
        rect.bottom > 0 &&
        rect.top < window.innerHeight &&
        rect.right > 0 &&
        rect.left < window.innerWidth
      sync()
    }
    observer?.observe(element)
    if (!observer) {
      fallback()
      window.addEventListener('scroll', fallback, true)
      window.addEventListener('resize', fallback)
    }
    document.addEventListener('visibilitychange', sync)
    return () => {
      observer?.disconnect()
      window.removeEventListener('scroll', fallback, true)
      window.removeEventListener('resize', fallback)
      document.removeEventListener('visibilitychange', sync)
    }
  }, [variant])
  const [compact, setCompact] = useState(false)
  const [activeSuccess, setActiveSuccess] = useState<WalletCloudSuccess | null>(
    null
  )
  const [layoutSeed] = useState(createWalletTokenSeed)
  const tokens = useMemo(
    () =>
      createWalletTokenLayout(
        variant === 'preset' ? MAX_PRESET_PARTICLES : MAX_BALANCE_PARTICLES,
        layoutSeed
      ),
    [layoutSeed, variant]
  )

  useEffect(() => {
    if (variant !== 'balance') return
    const media = window.matchMedia('(max-width: 640px)')
    const sync = () => setCompact(media.matches)
    sync()
    media.addEventListener('change', sync)
    return () => media.removeEventListener('change', sync)
  }, [variant])

  useEffect(() => {
    if (
      variant !== 'balance' ||
      !success ||
      !visible ||
      playedRef.current === success.orderId
    ) {
      setActiveSuccess(null)
      return
    }
    const reduced = window.matchMedia(
      '(prefers-reduced-motion: reduce)'
    ).matches
    setActiveSuccess(reduced ? null : success)
    const timeout = window.setTimeout(
      () => {
        playedRef.current = success.orderId
        setActiveSuccess(null)
        onCompleteRef.current?.(success.orderId)
      },
      reduced ? 0 : 3000
    )
    return () => window.clearTimeout(timeout)
  }, [success, variant, visible])

  const after = Math.max(0, amount)
  const before = activeSuccess
    ? Math.min(after, Math.max(0, activeSuccess.beforeCredits))
    : after
  const densityFactor = compact && variant === 'balance' ? 0.58 : 1
  const baseCount = Math.round(
    walletCloudParticleCount(before, variant) * densityFactor
  )
  const totalCount = Math.round(
    walletCloudParticleCount(after, variant) * densityFactor
  )
  const incomingCount = Math.max(0, totalCount - baseCount)
  const burstCount = activeSuccess
    ? Math.max(12, Math.min(32, incomingCount))
    : 0
  const mini = variant === 'preset'
  const startScale = activeSuccess
    ? Math.min(
        0.82,
        walletCloudExtent(before, variant) / walletCloudExtent(after, variant)
      )
    : 1

  useEffect(() => {
    const svg = svgRef.current
    if (!svg || window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      return
    }
    const target = svg.parentElement
    if (!target) return
    const nodes = Array.from(
      svg.querySelectorAll<SVGTextElement>('.wallet-token-cloud-particle')
    )
    const offsets = nodes.map(() => ({ x: 0, y: 0, vx: 0, vy: 0 }))
    const pointer = { x: 0, y: 0, active: false, pressed: false }
    const reach = mini ? 28 : 88
    const displacement = mini ? 9 : 27
    let frame: number | null = null

    const tick = () => {
      frame = null
      let moving = false
      for (const [index, node] of nodes.entries()) {
        const point = tokenPoint(tokens, index, variant, after)
        const offset = offsets[index]
        const dx = point.x - pointer.x
        const dy = point.y - pointer.y
        const distance = Math.max(1, Math.hypot(dx, dy))
        const force = pointer.active
          ? Math.max(0, 1 - distance / reach) ** 2
          : 0
        const targetX =
          (dx / distance) * force * displacement * (pointer.pressed ? 1.6 : 1)
        const targetY =
          (dy / distance) * force * displacement * (pointer.pressed ? 1.6 : 1)
        offset.vx = (offset.vx + (targetX - offset.x) * 0.16) * 0.76
        offset.vy = (offset.vy + (targetY - offset.y) * 0.16) * 0.76
        offset.x += offset.vx
        offset.y += offset.vy
        node.style.transform = `translate(${offset.x.toFixed(2)}px, ${offset.y.toFixed(2)}px)`
        if (
          Math.abs(offset.vx) +
            Math.abs(offset.vy) +
            Math.abs(offset.x - targetX) +
            Math.abs(offset.y - targetY) >
          0.12
        ) {
          moving = true
        }
      }
      if (moving) frame = window.requestAnimationFrame(tick)
    }
    const schedule = () => {
      if (frame === null) frame = window.requestAnimationFrame(tick)
    }
    const move = (event: PointerEvent) => {
      const bounds = target.getBoundingClientRect()
      pointer.x =
        ((event.clientX - bounds.left) / bounds.width) * (mini ? 90 : 420)
      pointer.y =
        ((event.clientY - bounds.top) / bounds.height) * (mini ? 50 : 160)
      pointer.active = true
      schedule()
    }
    const down = (event: PointerEvent) => {
      pointer.pressed = true
      move(event)
    }
    const up = () => {
      pointer.pressed = false
      schedule()
    }
    const leave = () => {
      pointer.active = false
      pointer.pressed = false
      schedule()
    }
    target.addEventListener('pointermove', move)
    target.addEventListener('pointerdown', down)
    target.addEventListener('pointerup', up)
    target.addEventListener('pointerleave', leave)
    target.addEventListener('pointercancel', leave)
    return () => {
      if (frame !== null) window.cancelAnimationFrame(frame)
      target.removeEventListener('pointermove', move)
      target.removeEventListener('pointerdown', down)
      target.removeEventListener('pointerup', up)
      target.removeEventListener('pointerleave', leave)
      target.removeEventListener('pointercancel', leave)
    }
  }, [after, baseCount, mini, tokens, variant])

  return (
    <div
      ref={cloudRef}
      className={`wallet-token-cloud wallet-token-cloud--${variant}`}
      data-success={activeSuccess ? 'true' : undefined}
      data-testid={`wallet-token-cloud-${variant}`}
      aria-hidden='true'
      style={{ '--cloud-start-scale': startScale } as CSSProperties}
    >
      <svg
        ref={svgRef}
        viewBox={mini ? '0 0 90 50' : '0 0 420 160'}
        preserveAspectRatio='none'
      >
        {tokens.slice(0, baseCount).map((_, index) => {
          const point = tokenPoint(tokens, index, variant, after)
          return (
            <text
              key={`base-${index}`}
              className='wallet-token-cloud-particle'
              x={point.x}
              y={point.y}
              textAnchor='middle'
              dominantBaseline='middle'
              fontSize={point.fontSize}
              opacity={point.opacity}
            >
              {point.glyph}
            </text>
          )
        })}
        {activeSuccess &&
          tokens.slice(baseCount, totalCount).map((_, offset) => {
            const point = tokenPoint(tokens, baseCount + offset, variant, after)
            return (
              <text
                key={`added-${activeSuccess.orderId}-${offset}`}
                className='wallet-token-cloud-added'
                x={point.x}
                y={point.y}
                textAnchor='middle'
                dominantBaseline='middle'
                fontSize={point.fontSize + 0.8}
                style={{ animationDelay: `${(offset % 28) * 22}ms` }}
              >
                {point.glyph}
              </text>
            )
          })}
        {activeSuccess &&
          Array.from({ length: burstCount }, (_, offset) => {
            const point = tokenPoint(
              tokens,
              Math.min(tokens.length - 1, baseCount + offset),
              variant,
              after
            )
            return (
              <text
                key={`burst-${activeSuccess.orderId}-${offset}`}
                className='wallet-token-cloud-burst'
                x={point.x}
                y={point.y}
                textAnchor='middle'
                dominantBaseline='middle'
                fontSize={point.fontSize + 2}
                style={
                  {
                    animationDelay: `${offset * 24}ms`,
                    '--burst-x': `${36 + (offset % 4) * 12}px`,
                    '--burst-y': `${58 + (offset % 5) * 13}px`,
                  } as CSSProperties
                }
              >
                {point.glyph}
              </text>
            )
          })}
      </svg>
    </div>
  )
}
