/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useRef,
} from 'react'

import { ForgeHome } from '@/features/forge/forge-home'

import './framer-home-motion.css'

type PointerPosition = {
  x: number
  y: number
}

const CENTER_POINTER: PointerPosition = { x: 0.5, y: 0.5 }

function clampUnit(value: number) {
  return Math.min(1, Math.max(0, value))
}

export function Home() {
  const rootRef = useRef<HTMLDivElement>(null)
  const pointerRef = useRef<PointerPosition>(CENTER_POINTER)
  const scrollProgressRef = useRef(0)
  const animationFrameRef = useRef<number | null>(null)

  const writeVisualState = useCallback(() => {
    const root = rootRef.current
    if (!root) return

    const { x, y } = pointerRef.current
    const scrollProgress = scrollProgressRef.current
    const normalizedX = x - 0.5
    const normalizedY = y - 0.5

    root.style.setProperty('--home-pointer-x', `${(x * 100).toFixed(2)}%`)
    root.style.setProperty('--home-pointer-y', `${(y * 100).toFixed(2)}%`)
    root.style.setProperty('--home-tilt-x', `${(-normalizedY * 6).toFixed(2)}deg`)
    root.style.setProperty('--home-tilt-y', `${(normalizedX * 8).toFixed(2)}deg`)
    root.style.setProperty('--home-shift-x', `${(normalizedX * 26).toFixed(2)}px`)
    root.style.setProperty('--home-shift-y', `${(normalizedY * 18).toFixed(2)}px`)
    root.style.setProperty(
      '--home-scroll-y',
      `${(-scrollProgress * 52).toFixed(2)}px`
    )
    root.style.setProperty(
      '--home-scroll-scale',
      (1 + scrollProgress * 0.055).toFixed(4)
    )

    animationFrameRef.current = null
  }, [])

  const scheduleVisualUpdate = useCallback(() => {
    if (animationFrameRef.current !== null) return
    animationFrameRef.current = window.requestAnimationFrame(writeVisualState)
  }, [writeVisualState])

  useEffect(() => {
    const updateScrollProgress = () => {
      const heroRange = Math.max(window.innerHeight * 1.15, 1)
      scrollProgressRef.current = clampUnit(window.scrollY / heroRange)
      scheduleVisualUpdate()
    }

    updateScrollProgress()
    window.addEventListener('scroll', updateScrollProgress, { passive: true })
    window.addEventListener('resize', updateScrollProgress, { passive: true })

    return () => {
      window.removeEventListener('scroll', updateScrollProgress)
      window.removeEventListener('resize', updateScrollProgress)
      if (animationFrameRef.current !== null) {
        window.cancelAnimationFrame(animationFrameRef.current)
      }
    }
  }, [scheduleVisualUpdate])

  const handlePointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.pointerType === 'touch') return

    pointerRef.current = {
      x: clampUnit(event.clientX / Math.max(window.innerWidth, 1)),
      y: clampUnit(event.clientY / Math.max(window.innerHeight, 1)),
    }
    scheduleVisualUpdate()
  }

  const handlePointerLeave = () => {
    pointerRef.current = CENTER_POINTER
    scheduleVisualUpdate()
  }

  return (
    <div
      ref={rootRef}
      className='forge-home-motion-root'
      onPointerMove={handlePointerMove}
      onPointerLeave={handlePointerLeave}
    >
      <ForgeHome />
    </div>
  )
}
