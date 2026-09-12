/*
Copyright (C) 2026 LIghtJUNction
*/
import {
  ArrowDown01Icon,
  ArrowLeft01Icon,
  ArrowRight01Icon,
  ArrowUp01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

type Point = { x: number; y: number }
type Direction = 'UP' | 'DOWN' | 'LEFT' | 'RIGHT'

const GRID_SIZE = 20
const INITIAL_SNAKE: Point[] = [
  { x: 10, y: 10 },
  { x: 10, y: 11 },
  { x: 10, y: 12 },
]
const INITIAL_DIRECTION: Direction = 'UP'
const TICK_MS = 140

function getRandomFood(snake: Point[]): Point {
  while (true) {
    const x = Math.floor(Math.random() * GRID_SIZE)
    const y = Math.floor(Math.random() * GRID_SIZE)
    const collision = snake.some((seg) => seg.x === x && seg.y === y)
    if (!collision) return { x, y }
  }
}

export function DrawingSnakeGame() {
  const { t } = useTranslation()
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const [score, setScore] = useState(0)
  const [highScore, setHighScore] = useState(() => {
    if (typeof window === 'undefined' || !window.localStorage) return 0
    try {
      return Number(window.localStorage.getItem('lmm_snake_high_score') || 0)
    } catch {
      return 0
    }
  })

  const stateRef = useRef({
    snake: [...INITIAL_SNAKE],
    direction: INITIAL_DIRECTION,
    nextDirection: INITIAL_DIRECTION,
    food: getRandomFood(INITIAL_SNAKE),
    score: 0,
    alive: true,
  })

  const changeDirection = useCallback((newDir: Direction) => {
    const current = stateRef.current.direction
    if (newDir === 'UP' && current !== 'DOWN') {
      stateRef.current.nextDirection = 'UP'
    } else if (newDir === 'DOWN' && current !== 'UP') {
      stateRef.current.nextDirection = 'DOWN'
    } else if (newDir === 'LEFT' && current !== 'RIGHT') {
      stateRef.current.nextDirection = 'LEFT'
    } else if (newDir === 'RIGHT' && current !== 'LEFT') {
      stateRef.current.nextDirection = 'RIGHT'
    }
  }, [])

  // Touch swipe handling
  const touchStartRef = useRef<{ x: number; y: number } | null>(null)

  const handleTouchStart = (e: React.TouchEvent) => {
    const touch = e.touches[0]
    if (touch) {
      touchStartRef.current = { x: touch.clientX, y: touch.clientY }
    }
  }

  const handleTouchEnd = (e: React.TouchEvent) => {
    if (!touchStartRef.current) return
    const touch = e.changedTouches[0]
    if (!touch) return
    const dx = touch.clientX - touchStartRef.current.x
    const dy = touch.clientY - touchStartRef.current.y
    touchStartRef.current = null

    const minSwipe = 18
    if (Math.abs(dx) > Math.abs(dy)) {
      if (Math.abs(dx) > minSwipe) {
        changeDirection(dx > 0 ? 'RIGHT' : 'LEFT')
      }
    } else {
      if (Math.abs(dy) > minSwipe) {
        changeDirection(dy > 0 ? 'DOWN' : 'UP')
      }
    }
  }

  // Keyboard navigation
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      // Don't capture typing in inputs or textareas
      const target = event.target as HTMLElement | null
      if (
        target?.tagName === 'INPUT' ||
        target?.tagName === 'TEXTAREA' ||
        target?.isContentEditable
      ) {
        return
      }

      let handled = false
      switch (event.key) {
        case 'ArrowUp':
        case 'w':
        case 'W':
          changeDirection('UP')
          handled = true
          break
        case 'ArrowDown':
        case 's':
        case 'S':
          changeDirection('DOWN')
          handled = true
          break
        case 'ArrowLeft':
        case 'a':
        case 'A':
          changeDirection('LEFT')
          handled = true
          break
        case 'ArrowRight':
        case 'd':
        case 'D':
          changeDirection('RIGHT')
          handled = true
          break
      }
      if (handled && event.key.startsWith('Arrow')) {
        event.preventDefault()
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => {
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [changeDirection])

  // Game loop and rendering
  useEffect(() => {
    let animationId: number
    let isPaused = false

    const handleVisibilityChange = () => {
      isPaused = document.visibilityState === 'hidden'
    }
    document.addEventListener('visibilitychange', handleVisibilityChange)

    let resetTimeoutId: ReturnType<typeof setTimeout> | null = null

    const tick = () => {
      if (isPaused) return
      const s = stateRef.current
      if (!s.alive) {
        return
      }

      s.direction = s.nextDirection
      const head = { ...s.snake[0] }
      if (s.direction === 'UP') head.y -= 1
      if (s.direction === 'DOWN') head.y += 1
      if (s.direction === 'LEFT') head.x -= 1
      if (s.direction === 'RIGHT') head.x += 1

      // Wrap around walls for smooth casual play
      if (head.x < 0) head.x = GRID_SIZE - 1
      if (head.x >= GRID_SIZE) head.x = 0
      if (head.y < 0) head.y = GRID_SIZE - 1
      if (head.y >= GRID_SIZE) head.y = 0

      // Self collision
      const hitsBody = s.snake.some(
        (seg) => seg.x === head.x && seg.y === head.y
      )
      if (hitsBody) {
        s.alive = false
        if (resetTimeoutId) {
          clearTimeout(resetTimeoutId)
        }
        resetTimeoutId = setTimeout(() => {
          s.snake = [...INITIAL_SNAKE]
          s.direction = INITIAL_DIRECTION
          s.nextDirection = INITIAL_DIRECTION
          s.food = getRandomFood(INITIAL_SNAKE)
          s.score = 0
          s.alive = true
          setScore(0)
          resetTimeoutId = null
        }, 800)
        return
      }

      const eatsFood = head.x === s.food.x && head.y === s.food.y
      s.snake.unshift(head)
      if (eatsFood) {
        s.score += 1
        setScore(s.score)
        setHighScore((prev) => {
          const next = Math.max(prev, s.score)
          try {
            window.localStorage?.setItem('lmm_snake_high_score', String(next))
          } catch {
            // Ignore
          }
          return next
        })
        s.food = getRandomFood(s.snake)
      } else {
        s.snake.pop()
      }
    }

    const timerId = setInterval(tick, TICK_MS)

    const render = () => {
      const canvas = canvasRef.current
      if (canvas) {
        const ctx = canvas.getContext('2d')
        if (ctx) {
          const size = canvas.width
          const cellSize = size / GRID_SIZE
          ctx.clearRect(0, 0, size, size)

          // Background matrix dots
          ctx.fillStyle = 'rgba(255, 255, 255, 0.08)'
          for (let gx = 0; gx < GRID_SIZE; gx++) {
            for (let gy = 0; gy < GRID_SIZE; gy++) {
              ctx.beginPath()
              ctx.arc(
                gx * cellSize + cellSize / 2,
                gy * cellSize + cellSize / 2,
                cellSize * 0.16,
                0,
                Math.PI * 2
              )
              ctx.fill()
            }
          }

          const s = stateRef.current

          // Draw food (amber glowing dot)
          ctx.fillStyle = '#f59e0b'
          ctx.beginPath()
          ctx.arc(
            s.food.x * cellSize + cellSize / 2,
            s.food.y * cellSize + cellSize / 2,
            cellSize * 0.38,
            0,
            Math.PI * 2
          )
          ctx.fill()

          // Draw snake body & head
          s.snake.forEach((seg, idx) => {
            if (idx === 0) {
              // Head: vibrant emerald dot
              ctx.fillStyle = '#4ade80'
              ctx.beginPath()
              ctx.arc(
                seg.x * cellSize + cellSize / 2,
                seg.y * cellSize + cellSize / 2,
                cellSize * 0.44,
                0,
                Math.PI * 2
              )
              ctx.fill()
            } else {
              // Body: retro matrix green dot
              ctx.fillStyle = s.alive
                ? 'rgba(34, 197, 94, 0.85)'
                : 'rgba(239, 68, 68, 0.8)'
              ctx.beginPath()
              ctx.arc(
                seg.x * cellSize + cellSize / 2,
                seg.y * cellSize + cellSize / 2,
                cellSize * 0.36,
                0,
                Math.PI * 2
              )
              ctx.fill()
            }
          })
        }
      }
      animationId = requestAnimationFrame(render)
    }

    animationId = requestAnimationFrame(render)

    return () => {
      clearInterval(timerId)
      if (resetTimeoutId) {
        clearTimeout(resetTimeoutId)
      }
      cancelAnimationFrame(animationId)
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [])

  return (
    <div className='flex flex-col items-center justify-center gap-3 select-none'>
      <div className='flex w-full max-w-[240px] items-center justify-between px-1 text-xs text-white/70'>
        <span className='font-mono font-medium'>
          {t('Score: {{score}}', { score })}
        </span>
        <span className='font-mono text-[11px] text-white/50'>
          {t('High score: {{score}}', { score: highScore })}
        </span>
      </div>

      <div
        className='relative touch-none rounded-xl border border-white/15 bg-black/60 p-2 shadow-inner'
        onTouchStart={handleTouchStart}
        onTouchEnd={handleTouchEnd}
        style={{ touchAction: 'none' }}
      >
        <canvas
          ref={canvasRef}
          width={240}
          height={240}
          className='block rounded-lg'
          style={{ width: 240, height: 240 }}
        />
      </div>

      <p className='max-w-[240px] text-center text-[11px] leading-normal text-white/50'>
        {t('Swipe or use arrow keys to control')}
      </p>

      {/* Mini mobile D-pad */}
      <div className='grid w-28 grid-cols-3 gap-1 pt-1 sm:hidden'>
        <div />
        <Button
          type='button'
          variant='secondary'
          size='icon-xs'
          className='size-8 bg-white/10 text-white hover:bg-white/20'
          onClick={() => changeDirection('UP')}
          aria-label='Up'
        >
          <HugeiconsIcon
            icon={ArrowUp01Icon}
            className='size-4'
            strokeWidth={2}
          />
        </Button>
        <div />
        <Button
          type='button'
          variant='secondary'
          size='icon-xs'
          className='size-8 bg-white/10 text-white hover:bg-white/20'
          onClick={() => changeDirection('LEFT')}
          aria-label='Left'
        >
          <HugeiconsIcon
            icon={ArrowLeft01Icon}
            className='size-4'
            strokeWidth={2}
          />
        </Button>
        <Button
          type='button'
          variant='secondary'
          size='icon-xs'
          className='size-8 bg-white/10 text-white hover:bg-white/20'
          onClick={() => changeDirection('DOWN')}
          aria-label='Down'
        >
          <HugeiconsIcon
            icon={ArrowDown01Icon}
            className='size-4'
            strokeWidth={2}
          />
        </Button>
        <Button
          type='button'
          variant='secondary'
          size='icon-xs'
          className='size-8 bg-white/10 text-white hover:bg-white/20'
          onClick={() => changeDirection('RIGHT')}
          aria-label='Right'
        >
          <HugeiconsIcon
            icon={ArrowRight01Icon}
            className='size-4'
            strokeWidth={2}
          />
        </Button>
      </div>
    </div>
  )
}
