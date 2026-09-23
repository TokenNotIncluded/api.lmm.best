/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import './signal-tuner.css'

const COLUMNS = 5
const ROWS = 4
const ROUND_TARGET = 5

type Cell = { row: number; column: number }

const randomCell = (avoid?: Cell | null): Cell => {
  let next: Cell
  do {
    next = {
      row: Math.floor(Math.random() * ROWS),
      column: Math.floor(Math.random() * COLUMNS),
    }
  } while (avoid && next.row === avoid.row && next.column === avoid.column)
  return next
}

/**
 * A five-second distraction for a dead end: tune the receiver by clicking the
 * lit cell. Keyboard players get arrow keys plus Enter. Respecting
 * prefers-reduced-motion simply means the cell changes without a pulse.
 */
export function SignalTuner() {
  const { t } = useTranslation()
  const [target, setTarget] = useState<Cell | null>(null)
  const [hits, setHits] = useState(0)
  const [best, setBest] = useState(0)
  const [playing, setPlaying] = useState(false)
  const gridRef = useRef<HTMLDivElement>(null)

  const start = useCallback(() => {
    setHits(0)
    setTarget(randomCell())
    setPlaying(true)
  }, [])

  useEffect(() => {
    if (!playing) return
    if (hits >= ROUND_TARGET) {
      setPlaying(false)
      setTarget(null)
      setBest((previous) => Math.max(previous, hits))
    }
  }, [hits, playing])

  const tune = (cell: Cell) => {
    if (!target || !playing) return
    if (cell.row === target.row && cell.column === target.column) {
      const next = hits + 1
      setHits(next)
      if (next >= ROUND_TARGET) {
        setBest((previous) => Math.max(previous, next))
        setPlaying(false)
        setTarget(null)
      } else {
        setTarget(randomCell(cell))
      }
    } else {
      // A miss costs nothing but the streak; dead ends should not punish.
      setHits(0)
    }
  }

  // Keep the focus on the grid so arrow keys keep working after a click.
  const handleKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const offset = {
      ArrowRight: 1,
      ArrowLeft: -1,
      ArrowDown: COLUMNS,
      ArrowUp: -COLUMNS,
    }[event.key]
    if (offset === undefined || !playing) return
    const buttons = Array.from(
      gridRef.current?.querySelectorAll('button') ?? []
    )
    const index = buttons.indexOf(event.target as HTMLButtonElement)
    if (index < 0) return
    event.preventDefault()
    buttons[(index + offset + buttons.length) % buttons.length]?.focus()
  }

  const completed = !playing && hits >= ROUND_TARGET

  return (
    <div className='signal-tuner'>
      <div className='signal-tuner-head'>
        <p className='signal-tuner-label'>{t('Signal tuner')}</p>
        <p className='signal-tuner-score' aria-live='polite'>
          {t('Locked')}: {hits} / {ROUND_TARGET}
          {best > 0 && ` · ${t('Best')} ${best}`}
        </p>
      </div>
      <div
        ref={gridRef}
        className='signal-tuner-grid'
        data-playing={playing || undefined}
        onKeyDown={handleKeyDown}
        role='group'
        aria-label={t('Tune the receiver by selecting the lit cell')}
      >
        {Array.from({ length: ROWS * COLUMNS }, (_, index) => {
          const row = Math.floor(index / COLUMNS)
          const column = index % COLUMNS
          const lit =
            playing &&
            target !== null &&
            target.row === row &&
            target.column === column
          return (
            <button
              key={index}
              type='button'
              className='signal-tuner-cell'
              data-lit={lit || undefined}
              aria-label={lit ? t('Signal found') : t('Empty frequency')}
              tabIndex={playing ? 0 : -1}
              onClick={() => tune({ row, column })}
            />
          )
        })}
      </div>
      <div className='signal-tuner-foot'>
        {completed ? (
          <p className='signal-tuner-done' role='status'>
            {t('Signal locked. Nice.')}
          </p>
        ) : (
          <p className='signal-tuner-hint'>
            {playing
              ? t('Tap the lit cell. Five in a row.')
              : t('Five correct picks lock the signal.')}
          </p>
        )}
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='min-h-11'
          onClick={start}
        >
          {playing ? t('Restart') : completed ? t('Play again') : t('Play')}
        </Button>
      </div>
    </div>
  )
}
