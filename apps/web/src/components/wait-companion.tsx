/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { BulbIcon, Undo03Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  createLightPuzzle,
  LIGHT_PUZZLE_SEEDS,
  solveLightPuzzle,
  toggleLight,
} from '@/lib/light-puzzle'
import { cn } from '@/lib/utils'

interface WaitCompanionProps {
  pending: boolean
  taskKey?: string | number
  delayMs?: number
  finishedLabel?: string
  onReturnToTask?: () => void
  className?: string
}

/** Optional activity, with no network, timers during play, rewards, or task side effects. */
export function WaitCompanion({
  taskKey = 'wait',
  ...props
}: WaitCompanionProps) {
  return <WaitRound key={taskKey} {...props} />
}

function WaitRound({
  pending,
  delayMs = 4000,
  finishedLabel,
  onReturnToTask,
  className,
}: Omit<WaitCompanionProps, 'taskKey'>) {
  const { t } = useTranslation()
  const [offered, setOffered] = useState(false)
  const [playing, setPlaying] = useState(false)
  const [round, setRound] = useState(() => randomRound())
  const [board, setBoard] = useState(() => createLightPuzzle(round))
  const [target, setTarget] = useState(() => solveLightPuzzle(board).length)
  const [moves, setMoves] = useState(0)
  const [history, setHistory] = useState<boolean[][]>([])
  const [hint, setHint] = useState<number | null>(null)
  const [cleared, setCleared] = useState(0)
  const [perfectStreak, setPerfectStreak] = useState(0)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const restartRef = useRef<HTMLButtonElement>(null)
  const previousFocusRef = useRef<HTMLElement | null>(null)
  const gameHadFocusRef = useRef(false)
  const solved = board.every((on) => !on)
  const lights = board.filter(Boolean).length

  useEffect(() => {
    setOffered(false)
    if (!pending) return
    const nextRound = randomRound()
    const nextBoard = createLightPuzzle(nextRound)
    setPlaying(false)
    setRound(nextRound)
    setBoard(nextBoard)
    setTarget(solveLightPuzzle(nextBoard).length)
    setMoves(0)
    setHistory([])
    setHint(null)
    setCleared(0)
    setPerfectStreak(0)
    const timer = window.setTimeout(
      () => setOffered(true),
      Math.max(0, delayMs)
    )
    return () => window.clearTimeout(timer)
  }, [pending, delayMs])

  useLayoutEffect(() => {
    const root = rootRef.current
    return () => {
      const target = previousFocusRef.current
      if (root?.contains(document.activeElement) && target?.isConnected) {
        target.focus({ preventScroll: true })
      }
    }
  }, [playing])

  useLayoutEffect(() => {
    if (
      !pending &&
      playing &&
      gameHadFocusRef.current &&
      document.activeElement === document.body
    ) {
      triggerRef.current?.focus({ preventScroll: true })
    }
  }, [pending, playing])

  useLayoutEffect(() => {
    if (solved && playing && pending) {
      restartRef.current?.focus({ preventScroll: true })
    }
  }, [solved, playing, pending])

  if (!playing && (!pending || !offered)) return null

  const rememberFocus = (target: EventTarget | null) => {
    if (target instanceof HTMLElement && !rootRef.current?.contains(target)) {
      previousFocusRef.current = target
    }
  }
  const close = () => {
    setPlaying(false)
    if (!pending) {
      onReturnToTask?.()
      if (!onReturnToTask && previousFocusRef.current?.isConnected) {
        previousFocusRef.current.focus({ preventScroll: true })
      }
    } else {
      triggerRef.current?.focus({ preventScroll: true })
    }
  }
  const startPuzzle = (nextRound: number) => {
    const nextBoard = createLightPuzzle(nextRound)
    if (!solved && moves > 0) setPerfectStreak(0)
    setRound(nextRound)
    setBoard(nextBoard)
    setTarget(solveLightPuzzle(nextBoard).length)
    setMoves(0)
    setHistory([])
    setHint(null)
  }
  const play = (index: number) => {
    const nextBoard = toggleLight(board, index)
    const nextMoves = moves + 1
    setHistory((current) => [...current, board])
    setBoard(nextBoard)
    setMoves(nextMoves)
    setHint(null)
    if (nextBoard.every((on) => !on)) {
      setCleared((current) => current + 1)
      setPerfectStreak((current) => (nextMoves === target ? current + 1 : 0))
    }
  }
  const undo = () => {
    const previous = history.at(-1)
    if (!previous) return
    setBoard(previous)
    setHistory((current) => current.slice(0, -1))
    setMoves((current) => Math.max(0, current - 1))
    setHint(null)
  }
  const showHint = () => {
    setHint(solveLightPuzzle(board)[0] ?? null)
  }

  return (
    <div
      ref={rootRef}
      className={cn(
        'flex max-w-full flex-col items-start gap-3 py-2',
        className
      )}
      data-testid='wait-companion'
      onFocusCapture={() => {
        gameHadFocusRef.current = true
      }}
      onBlurCapture={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) {
          gameHadFocusRef.current = false
        }
      }}
    >
      {!pending && playing ? (
        <p role='status' className='text-sm font-medium'>
          {finishedLabel ?? t('The wait is over.')}
        </p>
      ) : null}
      <Button
        ref={triggerRef}
        className='min-h-11'
        type='button'
        variant='ghost'
        size='sm'
        onFocus={(event) => rememberFocus(event.relatedTarget)}
        onPointerDown={() => rememberFocus(document.activeElement)}
        onClick={() => (playing ? close() : setPlaying(true))}
        aria-expanded={pending ? playing : undefined}
      >
        {playing
          ? pending
            ? t('Close game')
            : t('Back to task')
          : t('Play while you wait')}
      </Button>
      {playing && pending ? (
        <div className='border-border bg-muted/25 flex max-w-full flex-col gap-3 rounded-lg border p-3'>
          <div className='flex min-w-64 items-start justify-between gap-4'>
            <div>
              <p className='text-sm font-semibold'>
                {t('Puzzle {{number}}', { number: round + 1 })}
              </p>
              <p className='text-muted-foreground text-xs tabular-nums'>
                {t('Target: {{count}}', { count: target })}
                {' · '}
                {t('Lights: {{count}}', { count: lights })}
              </p>
            </div>
            <div className='text-muted-foreground text-right text-xs tabular-nums'>
              <p>{t('Cleared: {{count}}', { count: cleared })}</p>
              <p>{t('Perfect streak: {{count}}', { count: perfectStreak })}</p>
            </div>
          </div>
          <p className='text-muted-foreground max-w-72 text-xs leading-5'>
            {t(
              'Clear all lights in as few moves as possible. Each tap flips its neighbours.'
            )}
          </p>
          <div
            role='group'
            aria-label={t('Lights out')}
            className='grid w-fit grid-cols-3 gap-1'
          >
            {board.map((on, index) => (
              <button
                key={index}
                type='button'
                className={cn(
                  'hover:bg-muted focus-visible:ring-ring flex size-12 items-center justify-center rounded-md transition-[background-color,transform] active:scale-95 focus-visible:ring-2 focus-visible:outline-none motion-reduce:transition-none',
                  hint === index && 'bg-primary/10 ring-primary ring-2'
                )}
                aria-label={t('Dot {{number}}', { number: index + 1 })}
                aria-pressed={on}
                disabled={solved}
                onClick={() => play(index)}
              >
                <span
                  aria-hidden='true'
                  className={cn(
                    'size-5 rounded-full transition-[transform,background-color] motion-reduce:transition-none',
                    on
                      ? 'bg-primary scale-100'
                      : 'border-muted-foreground/70 scale-75 border bg-transparent'
                  )}
                />
              </button>
            ))}
          </div>
          <div className='flex min-h-5 items-center gap-4 text-xs'>
            <span className='text-muted-foreground tabular-nums'>
              {t('Moves: {{count}}', { count: moves })}
            </span>
            {solved ? (
              <span role='status' className='font-medium'>
                {moves === target ? t('Perfect!') : t('All clear.')}
              </span>
            ) : hint != null ? (
              <span role='status'>
                {t('Try dot {{number}}', { number: hint + 1 })}
              </span>
            ) : null}
          </div>
          <div className='flex flex-wrap items-center gap-1'>
            <Button
              type='button'
              size='sm'
              variant='ghost'
              className='min-h-11'
              disabled={solved || history.length === 0}
              onClick={undo}
            >
              <HugeiconsIcon icon={Undo03Icon} data-icon='inline-start' />
              {t('Undo')}
            </Button>
            <Button
              type='button'
              size='sm'
              variant='ghost'
              className='min-h-11'
              disabled={solved}
              onClick={showHint}
            >
              <HugeiconsIcon icon={BulbIcon} data-icon='inline-start' />
              {t('Hint')}
            </Button>
            <Button
              ref={restartRef}
              type='button'
              size='sm'
              variant={solved ? 'default' : 'ghost'}
              className='min-h-11'
              onClick={() => startPuzzle(round + 1)}
            >
              {t('Another puzzle')}
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  )
}

function randomRound() {
  return Math.floor(Math.random() * LIGHT_PUZZLE_SEEDS.length)
}
