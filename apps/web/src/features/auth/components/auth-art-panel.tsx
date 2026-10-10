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
  ArrowUpRight,
  Lightbulb,
  RotateCcw,
  Shuffle,
  Volume2,
  VolumeX,
} from 'lucide-react'
import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { HUMAN } from '@/features/signal-game/api'
import { SignalBoard } from '@/features/signal-game/board'
import { SignalGameFeedback } from '@/features/signal-game/feedback'
import { formatGameTime } from '@/features/signal-game/format'
import {
  playCue,
  setSoundEnabled,
  soundEnabled,
} from '@/features/signal-game/sound'
import {
  rotateSignalTile,
  startSignalGame,
  useSignalGame,
} from '@/features/signal-game/store'

import { BOARD_SIZES, traceCircuit } from './signal-game'

import './signal-game.css'

export function GameClock({
  phase,
  startedAt,
  readyAt,
  elapsedMs,
  displayCountdown = false,
}: {
  phase: string
  startedAt: number
  readyAt: number
  elapsedMs: number | null
  displayCountdown?: boolean
}) {
  const [now, setNow] = useState(() => performance.now())
  useEffect(() => {
    if (phase !== 'playing' && phase !== 'countdown') return
    const timer = setInterval(() => setNow(performance.now()), 100)
    return () => clearInterval(timer)
  }, [phase, startedAt, readyAt])
  if (phase === 'countdown' && displayCountdown) {
    return (
      <strong className='text-6xl'>
        {Math.max(1, Math.ceil((readyAt - now) / 1000))}
      </strong>
    )
  }
  return (
    <span className='tabular-nums'>
      {formatGameTime(
        elapsedMs ?? (phase === 'playing' ? Math.max(0, now - startedAt) : 0)
      )}
    </span>
  )
}
export function AuthArtPanel() {
  const { t } = useTranslation(),
    titleId = useId(),
    state = useSignalGame(),
    [size, setSize] = useState(state.circuit.size),
    [advanced, setAdvanced] = useState(state.circuit.size > 12),
    [error, setError] = useState(false),
    [sound, setSound] = useState(soundEnabled),
    [usedHint, setUsedHint] = useState(false)
  useEffect(() => {
    setSize(state.circuit.size)
    setAdvanced(state.circuit.size > 12)
  }, [state.circuit.size])
  const trace = useMemo(
      () => traceCircuit(state.tiles, state.circuit.size),
      [state.tiles, state.circuit.size]
    ),
    powered = useMemo(() => new Set(trace.path), [trace.path]),
    poweredCount = trace.path.length,
    previousPower = useRef(poweredCount),
    previousWon = useRef(false)
  useEffect(() => {
    if (poweredCount > previousPower.current && !trace.won) playCue('power')
    previousPower.current = poweredCount
  }, [poweredCount, trace.won])
  useEffect(() => {
    if (trace.won && !previousWon.current) playCue('win')
    previousWon.current = trace.won
  }, [trace.won])
  useEffect(() => {
    setUsedHint(false)
    previousPower.current = 0
  }, [state.roundId])
  const turn = (index: number) => {
    if (index === -1) setUsedHint(true)
    try {
      rotateSignalTile(index)
      setError(false)
      playCue('turn')
    } catch {
      setError(true)
    }
  }
  const start = (mode: 'practice' | 'challenge', repeat = false) => {
    setError(false)
    void startSignalGame(
      mode,
      repeat ? state.circuit.size : size,
      HUMAN,
      repeat ? state.circuit.seed : undefined
    ).catch(() => setError(true))
  }
  const blocked = state.phase !== 'playing'
  return (
    <aside
      className='signal-game bg-card text-card-foreground'
      aria-labelledby={titleId}
      data-mode={state.mode}
    >
      <div className='signal-game-heading'>
        <h2 id={titleId}>{t('Signal path')}</h2>
        <p className='text-muted-foreground'>
          {t('Rotate the tiles to connect input to output.')}
        </p>
      </div>
      <details className='signal-game-settings'>
        <summary>
          {t('Board size')}
          <span className='signal-game-settings-value'>
            {size} × {size}
          </span>
        </summary>
        <div className='signal-game-settings-body'>
          <div className='signal-game-options'>
            <label>
              {t('Board size')}
              <select
                value={size}
                onChange={(e) => setSize(Number(e.target.value))}
              >
                {BOARD_SIZES.filter((n) => advanced || n <= 12).map((n) => (
                  <option key={n} value={n}>
                    {n} × {n}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <input
                type='checkbox'
                checked={advanced}
                onChange={(e) => {
                  setAdvanced(e.target.checked)
                  if (!e.target.checked && size > 12) setSize(5)
                }}
              />
              {t('Advanced mode')}
            </label>
          </div>
          <p>
            {t(
              'Challenges: 3-second countdown, no hints, ranked by moves then time.'
            )}
          </p>
          <p>{t('Use arrow keys to move and Enter to rotate.')}</p>
        </div>
      </details>
      <div className='signal-game-modes'>
        <Button
          type='button'
          variant='ghost'
          aria-pressed={state.mode === 'practice'}
          disabled={state.phase === 'loading'}
          onClick={() => start('practice')}
        >
          {t('Practice')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          aria-pressed={state.mode === 'challenge'}
          disabled={state.phase === 'loading'}
          onClick={() => start('challenge')}
        >
          {t('Start challenge')}
          <ArrowUpRight className='size-4' aria-hidden='true' />
        </Button>
      </div>
      <div className='signal-game-score text-muted-foreground'>
        <span>
          {state.mode === 'challenge' ? t('Challenge') : t('Practice')} ·{' '}
          {state.circuit.size} × {state.circuit.size}
        </span>
        <span>
          {t('Moves')}{' '}
          <strong className='text-foreground'>{state.actions.length}</strong>
        </span>
        {state.mode === 'challenge' && (
          <span>
            {t('Time')}{' '}
            <GameClock
              key={state.roundId}
              {...state}
              elapsedMs={state.elapsedMs}
            />
          </span>
        )}
      </div>
      {state.participant.actor === 'ai' && (
        <p className='mb-3 text-xs break-all'>
          AI · {state.participant.agent_name} · {state.participant.harness} ·{' '}
          {state.participant.model_id}
        </p>
      )}
      <div className='signal-game-stage' data-won={trace.won}>
        <SignalBoard
          size={state.circuit.size}
          tiles={state.tiles}
          powered={powered}
          hint={state.hint}
          blocked={blocked}
          turn={turn}
        />
        {(state.phase === 'countdown' || state.phase === 'loading') && (
          <div className='signal-game-countdown' role='status'>
            {state.phase === 'loading' ? (
              t('Loading...')
            ) : (
              <GameClock
                key={state.roundId}
                {...state}
                elapsedMs={state.elapsedMs}
                displayCountdown
              />
            )}
          </div>
        )}
      </div>
      <div
        className={`signal-game-status ${trace.won ? 'text-primary' : 'text-muted-foreground'}`}
        role='status'
        aria-live='polite'
        aria-atomic='true'
      >
        {trace.won
          ? t('Connected in {{count}} moves!', { count: state.actions.length })
          : t('Signal reached {{count}} tiles', { count: trace.path.length })}
      </div>
      <div className='signal-game-actions'>
        <Button
          type='button'
          variant={trace.won ? 'default' : 'outline'}
          disabled={state.phase === 'loading'}
          onClick={() => start(state.mode)}
        >
          <Shuffle className='size-4' aria-hidden='true' />
          {trace.won ? t('Play again') : t('New circuit')}
        </Button>
        {state.mode === 'practice' && (
          <Button
            type='button'
            variant='ghost'
            onClick={() => turn(-1)}
            disabled={blocked}
          >
            <Lightbulb className='size-4' aria-hidden='true' />
            {t('Hint')}
          </Button>
        )}
        <Button
          type='button'
          variant='ghost'
          onClick={() => start(state.mode, true)}
          disabled={state.phase === 'loading' || state.actions.length === 0}
          aria-label={t('Restart circuit')}
          title={t('Restart circuit')}
        >
          <RotateCcw className='size-4' aria-hidden='true' />
        </Button>
        <Button
          type='button'
          variant='ghost'
          aria-pressed={sound}
          aria-label={sound ? t('Sound on') : t('Sound off')}
          title={sound ? t('Sound on') : t('Sound off')}
          onClick={() => {
            const next = !sound
            setSoundEnabled(next)
            setSound(next)
            if (next) playCue('combo')
          }}
        >
          {sound ? (
            <Volume2 className='size-4' aria-hidden='true' />
          ) : (
            <VolumeX className='size-4' aria-hidden='true' />
          )}
        </Button>
      </div>
      <SignalGameFeedback
        powered={poweredCount}
        moves={state.actions.length}
        won={trace.won}
        rounds={state.rounds}
        usedHint={usedHint}
      />
      {(error || state.serviceError) && (
        <p role='alert' className='text-destructive mt-3 text-sm'>
          {t('Game service unavailable. Practice is still available.')}
        </p>
      )}
      {state.storageError && (
        <p role='alert' className='text-destructive mt-3 text-sm'>
          {t(
            'Local storage failed. Keep this page open until your record is saved.'
          )}
        </p>
      )}
      <a href='/games/signal' className='signal-game-link'>
        {t('Leaderboard, records and AI guide')}
        <ArrowUpRight className='size-4' aria-hidden='true' />
      </a>
      <p className='signal-game-note text-muted-foreground'>
        {t('Just for fun. You can sign in or register at any time.')}
      </p>
    </aside>
  )
}
