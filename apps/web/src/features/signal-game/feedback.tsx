/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

export interface GameFeedback {
  /** Powered tiles in the current circuit; drives the badges. */
  powered: number
  /** Turns taken this round; used to spot a clean, hint-free run. */
  moves: number
  /** True on the frame the circuit became connected. */
  won: boolean
  /** Rounds completed since the page opened, for a light streak readout. */
  rounds: number
  /** Practice rounds only — a hint forfeits the clean-run badge. */
  usedHint: boolean
}

/**
 * Rewards the player with short, dismissible praise instead of a permanent
 * wall of chrome. It is purely additive: nothing here reads or writes game
 * state, so the WebMCP surface and the leaderboard stay untouched.
 */
export function SignalGameFeedback(props: GameFeedback) {
  const { t } = useTranslation()
  const reduced = useReducedMotion() ?? false
  const [badge, setBadge] = useState<string | null>(null)
  const wasWon = useRef(false)

  useEffect(() => {
    if (props.won && !wasWon.current) setBadge(t('Circuit connected'))
    wasWon.current = props.won
  }, [props.won, t])

  useEffect(() => {
    if (!badge) return
    const timer = setTimeout(() => setBadge(null), reduced ? 1600 : 2400)
    return () => clearTimeout(timer)
  }, [badge, reduced])

  const cleanRun =
    props.won && props.moves > 0 && props.moves <= 8 && !props.usedHint

  return (
    <div className='signal-game-feedback'>
      <div className='signal-game-badges'>
        <span className='signal-game-badge tabular-nums'>
          {t('{{count}} tiles powered', { count: props.powered })}
        </span>
        <span className='signal-game-badge'>
          {t('{{count}} rounds finished', { count: props.rounds })}
        </span>
        {cleanRun && (
          <span className='signal-game-badge is-earned'>
            {t('Clean run — {{count}} moves', { count: props.moves })}
          </span>
        )}
      </div>
      <div className='signal-game-badge-slot' aria-live='polite'>
        <AnimatePresence>
          {badge && (
            <motion.p
              key={badge}
              className='signal-game-toast'
              initial={reduced ? { opacity: 0 } : { opacity: 0, y: 6 }}
              animate={reduced ? { opacity: 1 } : { opacity: 1, y: 0 }}
              exit={{ opacity: 0 }}
              transition={{ duration: reduced ? 0 : 0.22 }}
            >
              {badge}
            </motion.p>
          )}
        </AnimatePresence>
      </div>
    </div>
  )
}
