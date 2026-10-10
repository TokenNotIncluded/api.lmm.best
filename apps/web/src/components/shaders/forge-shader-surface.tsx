/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useEffect, useRef, useState } from 'react'

import { cn } from '@/lib/utils'

import {
  canAnimateShader,
  requestShaderSlot,
  shaderChapterVariant,
  shaderCapability,
  shaderRenderSize,
  type ForgeShaderVariant,
} from './shader-policy'
import type { ForgeShaderHandle, ShaderPalette } from './shader-runtime'

import './forge-shader.css'

type ShaderState = 'static' | 'loading' | 'ready' | 'unavailable'
let colorCanvas: HTMLCanvasElement | undefined

/** Resolve semantic CSS colors through the browser's own color parser. The
 * one-pixel sRGB canvas also handles the theme's OKLCH values correctly. */
function readPalette(element: HTMLElement): ShaderPalette {
  colorCanvas ??= document.createElement('canvas')
  colorCanvas.width = colorCanvas.height = 1
  const context = colorCanvas.getContext('2d', { willReadFrequently: true })
  const probe = document.createElement('span')
  probe.hidden = true
  element.append(probe)
  const color = (role: string) => {
    probe.style.color = `var(${role})`
    const value = window.getComputedStyle(probe).color
    if (!context) return value
    context.clearRect(0, 0, 1, 1)
    context.fillStyle = value
    context.fillRect(0, 0, 1, 1)
    const bytes = context.getImageData(0, 0, 1, 1).data
    return `#${[...bytes]
      .slice(0, 3)
      .map((byte) => byte.toString(16).padStart(2, '0'))
      .join('')}`
  }
  const palette = {
    ground: color('--background'),
    primary: color('--primary'),
    muted: color('--muted'),
    ink: color('--foreground'),
  }
  probe.remove()
  return palette
}

export function ForgeShaderSurface({
  variant,
  className,
  active = true,
  interaction = 'ambient',
}: {
  variant: ForgeShaderVariant
  className?: string
  active?: boolean
  interaction?: 'ambient' | 'intent'
}) {
  const hostRef = useRef<HTMLDivElement>(null)
  const planeRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const handleRef = useRef<ForgeShaderHandle | null>(null)
  const [state, setState] = useState<ShaderState>('static')
  const [failureReason, setFailureReason] = useState<string>()
  const [eligible, setEligible] = useState(false)
  const [composition, setComposition] = useState(variant)
  const unavailable = state === 'unavailable'

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    if (
      !('gpu' in navigator) ||
      typeof IntersectionObserver === 'undefined' ||
      typeof ResizeObserver === 'undefined' ||
      typeof MutationObserver === 'undefined'
    ) {
      return
    }
    const motion = window.matchMedia('(prefers-reduced-motion: reduce)')
    const connection = (
      navigator as Navigator & {
        connection?: {
          saveData?: boolean
          addEventListener?: typeof window.addEventListener
          removeEventListener?: typeof window.removeEventListener
        }
      }
    ).connection
    const home =
      variant === 'home' ? host.closest<HTMLElement>('.lmm-home') : null
    const cinema = home ? cinemaElement(host) : null
    const intentTarget =
      interaction === 'intent' ? host.closest('button, a') : null
    let visible = false
    let intent = interaction !== 'intent'
    const update = () => {
      const homePaused =
        home?.dataset.motion === 'paused' ||
        home?.dataset.motion === 'reduced' ||
        home?.dataset.motion === 'static'
      const rect = host.getBoundingClientRect()
      setEligible(
        'gpu' in navigator &&
          canAnimateShader({
            active,
            visible:
              visible && shaderRenderSize(rect.width, rect.height) !== null,
            documentVisible: document.visibilityState !== 'hidden',
            reducedMotion: motion.matches,
            saveData: connection?.saveData === true,
            homePaused,
            intent,
          })
      )
      setComposition(
        cinema ? shaderChapterVariant(cinema.dataset.chapter) : variant
      )
    }
    const enter = () => {
      intent = true
      update()
    }
    const leave = () => {
      intent = intentTarget?.contains(document.activeElement) === true
      update()
    }
    const blur = (event: FocusEvent) => {
      intent =
        intentTarget?.contains(event.relatedTarget as Node | null) === true
      update()
    }
    const observer = new IntersectionObserver(
      ([entry]) => {
        visible = entry?.isIntersecting === true
        update()
      },
      { threshold: 0.01 }
    )
    observer.observe(host)
    const sizeObserver = new ResizeObserver(update)
    sizeObserver.observe(host)
    const chapterObserver = new MutationObserver(update)
    if (cinema) {
      chapterObserver.observe(cinema, {
        attributes: true,
        attributeFilter: ['data-chapter'],
      })
    }
    if (home) {
      chapterObserver.observe(home, {
        attributes: true,
        attributeFilter: ['data-motion'],
      })
    }
    motion.addEventListener('change', update)
    connection?.addEventListener?.('change', update)
    document.addEventListener('visibilitychange', update)
    intentTarget?.addEventListener('pointerenter', enter)
    intentTarget?.addEventListener('pointerleave', leave)
    intentTarget?.addEventListener('focusin', enter)
    intentTarget?.addEventListener('focusout', blur as EventListener)
    update()
    return () => {
      observer.disconnect()
      sizeObserver.disconnect()
      chapterObserver.disconnect()
      motion.removeEventListener('change', update)
      connection?.removeEventListener?.('change', update)
      document.removeEventListener('visibilitychange', update)
      intentTarget?.removeEventListener('pointerenter', enter)
      intentTarget?.removeEventListener('pointerleave', leave)
      intentTarget?.removeEventListener('focusin', enter)
      intentTarget?.removeEventListener('focusout', blur as EventListener)
    }
  }, [active, interaction, variant])

  useEffect(() => {
    const host = hostRef.current
    const plane = planeRef.current
    const canvas = canvasRef.current
    if (!eligible || unavailable || !host || !plane || !canvas) {
      return
    }
    let cancelled = false
    setState('static')
    let resizeObserver: ResizeObserver | undefined
    let themeObserver: MutationObserver | undefined
    const resize = () => {
      const rect = host.getBoundingClientRect()
      const size = shaderRenderSize(rect.width, rect.height)
      if (!size) return false
      plane.style.width = `${size.width}px`
      plane.style.height = `${size.height}px`
      plane.style.transform = `scale(${size.scale})`
      handleRef.current?.resize(size.width, size.height)
      return true
    }
    const release = requestShaderSlot(() => {
      if (cancelled) return
      if (!resize()) {
        queueMicrotask(() => release?.())
        return
      }
      setState('loading')
      const gpu = (
        navigator as Navigator & {
          gpu?: { requestAdapter(): Promise<unknown | null> }
        }
      ).gpu
      void shaderCapability(gpu)
        .then((capability) => {
          if (cancelled) return null
          if (capability !== 'available') {
            setFailureReason(capability)
            setState('unavailable')
            return null
          }
          return import('./shader-runtime')
        })
        .then((module) => {
          if (cancelled || !module) return
          const { createForgeShader } = module
          handleRef.current = createForgeShader(
            canvas,
            cinemaVariant(host, variant),
            readPalette(host),
            {
              ready: () => {
                if (!cancelled) setState('ready')
              },
              unavailable: (reason) => {
                if (!cancelled) {
                  setFailureReason(reason)
                  setState('unavailable')
                }
              },
            }
          )
          resizeObserver = new ResizeObserver(resize)
          resizeObserver.observe(host)
          themeObserver = new MutationObserver(() =>
            handleRef.current?.update(
              cinemaVariant(host, variant),
              readPalette(host)
            )
          )
          themeObserver.observe(document.documentElement, { attributes: true })
          if (document.body) {
            themeObserver.observe(document.body, { attributes: true })
          }
        })
        .catch(() => {
          if (!cancelled) setState('unavailable')
        })
    })
    return () => {
      cancelled = true
      resizeObserver?.disconnect()
      themeObserver?.disconnect()
      const handle = handleRef.current
      handleRef.current = null
      // Stop RAF immediately. Release the capacity lease only once asynchronous
      // initialization has settled and its late GPU root has been released.
      if (handle) {
        handle.pause()
        void handle.destroy().then(release, release)
      } else release?.()
    }
  }, [eligible, unavailable, variant])

  useEffect(() => {
    const host = hostRef.current
    if (host) handleRef.current?.update(composition, readPalette(host))
  }, [composition])

  return (
    <div
      ref={hostRef}
      className={cn('forge-shader-surface', className)}
      data-forge-shader={composition}
      data-shader-state={eligible ? state : 'static'}
      data-shader-reason={failureReason}
      aria-hidden='true'
    >
      <div className='forge-shader-fallback' />
      <div className='forge-shader-plane' ref={planeRef}>
        <canvas
          ref={canvasRef}
          data-forge-shader-canvas
          style={{ width: '100%', height: '100%' }}
        />
      </div>
    </div>
  )
}

function cinemaVariant(host: HTMLElement, fallback: ForgeShaderVariant) {
  return fallback === 'home'
    ? shaderChapterVariant(cinemaElement(host)?.dataset.chapter)
    : fallback
}

function cinemaElement(host: HTMLElement) {
  return (
    host
      .closest<HTMLElement>('.lmm-home')
      ?.querySelector<HTMLElement>('[data-cinema-inner]') ?? null
  )
}
