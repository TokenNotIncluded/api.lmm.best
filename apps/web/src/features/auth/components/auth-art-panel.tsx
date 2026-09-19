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
import { ArrowRight, Check, Lightbulb, RotateCcw, Shuffle } from 'lucide-react'
import { useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import {
  createCircuit,
  GRID_SIZE,
  hintedTile,
  neighbor,
  PORTS,
  rotateTile,
  traceCircuit,
} from './signal-game'

import './signal-game.css'

const PORT_POINTS: Record<number, [number, number]> = {
  1: [32, 0],
  2: [64, 32],
  4: [32, 64],
  8: [0, 32],
}
const DIRECTION_KEYS = {
  ArrowUp: 1,
  ArrowRight: 2,
  ArrowDown: 4,
  ArrowLeft: 8,
} as const

export function AuthArtPanel() {
  const { t } = useTranslation()
  const titleId = useId()
  const [circuit, setCircuit] = useState(() => createCircuit())
  const [tiles, setTiles] = useState(circuit.tiles)
  const [moves, setMoves] = useState(0)
  const [rounds, setRounds] = useState(0)
  const [focus, setFocus] = useState(10)
  const [hint, setHint] = useState<number | null>(null)
  const buttons = useRef<(HTMLButtonElement | null)[]>([])
  const trace = useMemo(() => traceCircuit(tiles), [tiles])
  const powered = new Set(trace.path)
  const directionNames = {
    1: t('North'),
    2: t('East'),
    4: t('South'),
    8: t('West'),
  }

  const turn = (index: number, withHint = false) => {
    if (trace.won) return
    const next = [...tiles]
    next[index] = rotateTile(next[index])
    setTiles(next)
    setMoves((value) => value + 1)
    setHint(withHint ? index : null)
    if (traceCircuit(next).won) setRounds((value) => value + 1)
  }
  const start = (fresh: boolean) => {
    const next = fresh ? createCircuit() : circuit
    setCircuit(next)
    setTiles([...next.tiles])
    setMoves(0)
    setHint(null)
  }
  const requestHint = () => {
    const index = hintedTile(circuit, tiles)
    if (index !== null) {
      turn(index, true)
      setFocus(index)
      buttons.current[index]?.focus()
    }
  }

  return (
    <aside
      className='signal-game bg-card text-card-foreground'
      aria-labelledby={titleId}
    >
      <div className='signal-game-heading'>
        <h2 id={titleId}>{t('Signal path')}</h2>
        <p className='text-muted-foreground'>
          {t('Rotate the tiles to connect input to output.')}
        </p>
      </div>
      <div className='signal-game-score text-muted-foreground'>
        <span>
          {t('Moves')}: <strong className='text-foreground'>{moves}</strong>
        </span>
        <span>
          {t('Circuits solved')}:{' '}
          <strong className='text-foreground'>{rounds}</strong>
        </span>
      </div>
      <div className='signal-game-board-wrap'>
        <span
          className='signal-game-port signal-game-input text-primary'
          aria-label={t('Input')}
        >
          <ArrowRight aria-hidden='true' />
        </span>
        <div
          className='signal-game-board'
          role='grid'
          aria-label={t('Signal path')}
          aria-rowcount={GRID_SIZE}
          aria-colcount={GRID_SIZE}
        >
          {Array.from({ length: GRID_SIZE }, (_, row) => (
            <div role='row' className='signal-game-row' key={row}>
              {tiles
                .slice(row * GRID_SIZE, (row + 1) * GRID_SIZE)
                .map((mask, col) => {
                  const index = row * GRID_SIZE + col
                  const ports = PORTS.filter((port) => mask & port)
                  const from = PORT_POINTS[ports[0]],
                    to = PORT_POINTS[ports[1]]
                  const lit = powered.has(index)
                  return (
                    <div role='gridcell' key={index}>
                      <button
                        type='button'
                        ref={(element) => {
                          buttons.current[index] = element
                        }}
                        className={`signal-game-tile ${lit ? 'is-powered' : ''} ${hint === index ? 'is-hinted' : ''}`}
                        tabIndex={index === focus ? 0 : -1}
                        aria-disabled={trace.won}
                        aria-label={`${t('Rotate tile at row {{row}}, column {{column}}', { row: row + 1, column: col + 1 })}. ${ports.map((port) => directionNames[port]).join(', ')}${lit ? `. ${t('Connected to input')}` : ''}`}
                        onFocus={() => setFocus(index)}
                        onClick={() => turn(index)}
                        onKeyDown={(event) => {
                          const port =
                            DIRECTION_KEYS[
                              event.key as keyof typeof DIRECTION_KEYS
                            ]
                          if (!port) return
                          event.preventDefault()
                          const target = neighbor(index, port)
                          if (target !== null) {
                            setFocus(target)
                            buttons.current[target]?.focus()
                          }
                        }}
                      >
                        <svg viewBox='0 0 64 64' fill='none' aria-hidden='true'>
                          <path
                            className='signal-game-track'
                            d={`M${from.join(' ')} L32 32 L${to.join(' ')}`}
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
        <span
          className={`signal-game-port signal-game-output ${trace.won ? 'text-primary' : 'text-muted-foreground'}`}
          aria-label={t('Output')}
        >
          {trace.won ? (
            <Check aria-hidden='true' />
          ) : (
            <ArrowRight aria-hidden='true' />
          )}
        </span>
      </div>
      <div
        className={`signal-game-status ${trace.won ? 'text-primary' : 'text-muted-foreground'}`}
        role='status'
        aria-live='polite'
        aria-atomic='true'
      >
        {trace.won
          ? t('Connected in {{count}} moves!', { count: moves })
          : t('Signal reached {{count}} tiles', { count: trace.path.length })}
      </div>
      <div className='signal-game-actions'>
        <Button
          type='button'
          variant={trace.won ? 'default' : 'outline'}
          onClick={() => start(true)}
        >
          <Shuffle className='size-4' aria-hidden='true' />
          {trace.won ? t('Play again') : t('New circuit')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          onClick={requestHint}
          disabled={trace.won}
        >
          <Lightbulb className='size-4' aria-hidden='true' />
          {t('Hint')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          onClick={() => start(false)}
          disabled={moves === 0}
          aria-label={t('Restart circuit')}
          title={t('Restart circuit')}
        >
          <RotateCcw className='size-4' aria-hidden='true' />
        </Button>
      </div>
      <p className='signal-game-help text-muted-foreground'>
        {t('Use arrow keys to move and Enter to rotate.')}
      </p>
      <p className='signal-game-note text-muted-foreground'>
        {t('Just for fun. You can sign in or register at any time.')}
      </p>
    </aside>
  )
}
