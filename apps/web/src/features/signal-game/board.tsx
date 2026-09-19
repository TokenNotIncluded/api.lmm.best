/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  inputTile,
  neighbor,
  outputTile,
  PORTS,
} from '@/features/auth/components/signal-game'

type Props = {
  size: number
  tiles: number[]
  powered: Set<number>
  hint: number | null
  blocked: boolean
  turn: (index: number) => void
}
const positions: Record<number, [number, number]> = {
  1: [32, 0],
  2: [64, 32],
  4: [32, 64],
  8: [0, 32],
}
const arrows: Record<string, number> = {
  ArrowUp: 1,
  ArrowRight: 2,
  ArrowDown: 4,
  ArrowLeft: 8,
}
export function SignalBoard(props: Props) {
  return props.size === 5 ? (
    <SmallBoard {...props} />
  ) : (
    <LargeBoard key={props.size} {...props} />
  )
}
function SmallBoard({ size, tiles, powered, hint, blocked, turn }: Props) {
  const { t } = useTranslation(),
    [focus, setFocus] = useState(inputTile(size)),
    buttons = useRef<(HTMLButtonElement | null)[]>([])
  const names: Record<number, string> = {
    1: t('North'),
    2: t('East'),
    4: t('South'),
    8: t('West'),
  }
  return (
    <div className='signal-game-board-wrap'>
      <span className='signal-game-port signal-game-input'>→</span>
      <div
        className='signal-game-board'
        role='grid'
        aria-label={t('Signal path')}
        aria-rowcount={size}
        aria-colcount={size}
      >
        {Array.from({ length: size }, (_, row) => (
          <div role='row' className='signal-game-row' key={row}>
            {tiles.slice(row * size, (row + 1) * size).map((mask, col) => {
              const index = row * size + col,
                ports = PORTS.filter((p) => mask & p),
                a = positions[ports[0]],
                b = positions[ports[1]],
                lit = powered.has(index)
              return (
                <div role='gridcell' key={index}>
                  <button
                    type='button'
                    ref={(el) => {
                      buttons.current[index] = el
                    }}
                    className={`signal-game-tile ${lit ? 'is-powered' : ''} ${hint === index ? 'is-hinted' : ''}`}
                    tabIndex={index === focus ? 0 : -1}
                    aria-disabled={blocked}
                    aria-label={`${t('Rotate tile at row {{row}}, column {{column}}', { row: row + 1, column: col + 1 })}. ${ports.map((p) => names[p]).join(', ')}${lit ? `. ${t('Connected to input')}` : ''}`}
                    onFocus={() => setFocus(index)}
                    onClick={() => {
                      if (!blocked) turn(index)
                    }}
                    onKeyDown={(e) => {
                      const port = arrows[e.key]
                      if (!port) return
                      e.preventDefault()
                      const next = neighbor(index, port, size)
                      if (next !== null) {
                        setFocus(next)
                        buttons.current[next]?.focus()
                      }
                    }}
                  >
                    <svg viewBox='0 0 64 64' fill='none' aria-hidden='true'>
                      <path
                        className='signal-game-track'
                        d={`M${a.join(' ')} L32 32 L${b.join(' ')}`}
                      />
                      <circle
                        cx='32'
                        cy='32'
                        r='3.5'
                        className='signal-game-node'
                      />
                    </svg>
                  </button>
                </div>
              )
            })}
          </div>
        ))}
      </div>
      <span className='signal-game-port signal-game-output'>→</span>
    </div>
  )
}
function LargeBoard({ size, tiles, powered, hint, blocked, turn }: Props) {
  const { t } = useTranslation(),
    viewport = useRef<HTMLDivElement>(null),
    canvas = useRef<HTMLCanvasElement>(null),
    [cell, setCell] = useState(42),
    [focus, setFocus] = useState(inputTile(size)),
    pointer = useRef<{
      x: number
      y: number
      left: number
      top: number
    } | null>(null)
  const draw = useCallback(() => {
    const view = viewport.current,
      element = canvas.current
    if (!view || !element) return
    const ctx = element.getContext('2d')
    if (!ctx) return
    const w = Math.min(view.clientWidth, size * cell),
      h = Math.min(view.clientHeight, size * cell),
      ratio = Math.min(window.devicePixelRatio || 1, 1.5)
    if (
      element.width !== Math.round(w * ratio) ||
      element.height !== Math.round(h * ratio)
    ) {
      element.width = Math.round(w * ratio)
      element.height = Math.round(h * ratio)
    }
    element.style.width = `${w}px`
    element.style.height = `${h}px`
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0)
    const css = getComputedStyle(view),
      bg = css.getPropertyValue('--background').trim() || '#181a17',
      muted = css.getPropertyValue('--muted-foreground').trim() || '#999',
      signal = css.getPropertyValue('--signal-color').trim() || '#b77d3b'
    ctx.fillStyle = bg
    ctx.fillRect(0, 0, w, h)
    const x0 = Math.floor(view.scrollLeft / cell),
      y0 = Math.floor(view.scrollTop / cell)
    for (
      let row = y0;
      row < Math.min(size, Math.ceil((view.scrollTop + h) / cell));
      row++
    ) {
      for (
        let col = x0;
        col < Math.min(size, Math.ceil((view.scrollLeft + w) / cell));
        col++
      ) {
        const index = row * size + col,
          x = col * cell - view.scrollLeft,
          y = row * cell - view.scrollTop,
          cx = x + cell / 2,
          cy = y + cell / 2,
          lit = powered.has(index)
        ctx.strokeStyle = index === focus ? signal : '#77777744'
        ctx.lineWidth = index === focus ? 2 : 1
        ctx.strokeRect(x + 2, y + 2, cell - 4, cell - 4)
        ctx.strokeStyle = lit ? signal : muted
        ctx.lineWidth = lit ? 4 : 2.5
        ctx.lineCap = 'round'
        ctx.lineJoin = 'round'
        ctx.beginPath()
        const ports = PORTS.filter((p) => tiles[index] & p)
        ports.forEach((p, i) => {
          const [px, py] = positions[p]
          if (i === 0) {
            ctx.moveTo(x + (px / 64) * cell, y + (py / 64) * cell)
            ctx.lineTo(cx, cy)
          } else ctx.lineTo(x + (px / 64) * cell, y + (py / 64) * cell)
        })
        ctx.stroke()
        if (index === hint) {
          ctx.strokeStyle = signal
          ctx.strokeRect(x + 5, y + 5, cell - 10, cell - 10)
        }
        if (index === inputTile(size) || index === outputTile(size)) {
          ctx.font = '9px sans-serif'
          ctx.fillStyle = signal
          ctx.fillText(index === inputTile(size) ? 'IN' : 'OUT', x + 4, y + 12)
        }
      }
    }
  }, [size, tiles, powered, hint, cell, focus])
  useEffect(() => {
    draw()
    const view = viewport.current
    if (!view) return
    view.addEventListener('scroll', draw, { passive: true })
    const observer =
      typeof ResizeObserver === 'function' ? new ResizeObserver(draw) : null
    observer?.observe(view)
    window.addEventListener('resize', draw)
    return () => {
      view.removeEventListener('scroll', draw)
      observer?.disconnect()
      window.removeEventListener('resize', draw)
    }
  }, [draw])
  const reveal = (index: number) => {
    const view = viewport.current
    if (!view) return
    setFocus(index)
    view.scrollTo({
      left: Math.max(0, (index % size) * cell - view.clientWidth / 2),
      top: Math.max(0, Math.floor(index / size) * cell - view.clientHeight / 2),
    })
  }
  useEffect(() => {
    const view = viewport.current
    if (view) {
      view.scrollTop = Math.max(
        0,
        Math.floor(size / 2) * cell - view.clientHeight / 2
      )
    }
  }, [size, cell])
  return (
    <div className='signal-large-board'>
      <div className='mb-2 flex flex-wrap items-center gap-2'>
        <Button
          type='button'
          size='sm'
          variant='outline'
          aria-label={t('Zoom out')}
          onClick={() => setCell((c) => Math.max(16, c - 8))}
        >
          −
        </Button>
        <span className='text-xs'>
          {size} × {size}
        </span>
        <Button
          type='button'
          size='sm'
          variant='outline'
          aria-label={t('Zoom in')}
          onClick={() => setCell((c) => Math.min(64, c + 8))}
        >
          +
        </Button>
        <Button
          type='button'
          size='sm'
          variant='ghost'
          onClick={() => reveal(inputTile(size))}
        >
          {t('Input')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='ghost'
          onClick={() => reveal(outputTile(size))}
        >
          {t('Output')}
        </Button>
      </div>
      <div ref={viewport} className='signal-game-viewport'>
        <div style={{ width: size * cell, height: size * cell }}>
          <canvas
            ref={canvas}
            tabIndex={0}
            role='application'
            aria-label={t('Use arrow keys to move and Enter to rotate.')}
            className='signal-game-canvas'
            onPointerDown={(e) => {
              const view = viewport.current
              if (view) {
                pointer.current = {
                  x: e.clientX,
                  y: e.clientY,
                  left: view.scrollLeft,
                  top: view.scrollTop,
                }
              }
            }}
            onPointerCancel={() => {
              pointer.current = null
            }}
            onPointerUp={(e) => {
              const start = pointer.current,
                view = viewport.current
              pointer.current = null
              if (
                !start ||
                !view ||
                Math.hypot(e.clientX - start.x, e.clientY - start.y) > 6 ||
                view.scrollLeft !== start.left ||
                view.scrollTop !== start.top
              ) {
                return
              }
              const rect = e.currentTarget.getBoundingClientRect(),
                col = Math.floor(
                  (e.clientX - rect.left + view.scrollLeft) / cell
                ),
                row = Math.floor(
                  (e.clientY - rect.top + view.scrollTop) / cell
                ),
                index = row * size + col
              if (col < 0 || row < 0 || col >= size || row >= size) return
              setFocus(index)
              if (!blocked) turn(index)
            }}
            onKeyDown={(e) => {
              const port = arrows[e.key]
              if (port) {
                e.preventDefault()
                const next = neighbor(focus, port, size)
                if (next !== null) reveal(next)
              } else if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                if (!blocked) turn(focus)
              }
            }}
          />
        </div>
      </div>
      <p className='text-muted-foreground mt-2 text-xs' role='status'>
        {t('Rotate tile at row {{row}}, column {{column}}', {
          row: Math.floor(focus / size) + 1,
          column: (focus % size) + 1,
        })}
      </p>
      <Button
        type='button'
        variant='ghost'
        disabled={blocked}
        onClick={() => turn(focus)}
      >
        {t('Rotate selected tile')}
      </Button>
    </div>
  )
}
