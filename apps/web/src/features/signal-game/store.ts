/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { create } from 'zustand'

import {
  BOARD_SIZES,
  createCircuit,
  hintedTile,
  maxMoves,
  rotateTile,
  traceCircuit,
  type Circuit,
} from '@/features/auth/components/signal-game'
import { useAuthStore } from '@/stores/auth-store'

import {
  beginAttempt,
  finishAttempt,
  HUMAN,
  saveRecord,
  type GameRecord,
  type Participant,
} from './api'
import { putLocalRecord, readLocalRecords } from './storage'

type Phase = 'playing' | 'loading' | 'countdown' | 'won'
type State = {
  roundId: string
  circuit: Circuit
  tiles: number[]
  actions: number[]
  participant: Participant
  mode: 'practice' | 'challenge'
  day: string
  token?: string
  phase: Phase
  readyAt: number
  startedAt: number
  elapsedMs: number | null
  hint: number | null
  records: GameRecord[]
  storageError: boolean
  serviceError: boolean
  currentRecord: string | null
  rounds: number
}
const circuit = createCircuit()
export const useSignalGame = create<State>(() => ({
  roundId: 'initial',
  circuit,
  tiles: circuit.tiles,
  actions: [],
  participant: HUMAN,
  mode: 'practice',
  day: '',
  phase: 'playing',
  readyAt: 0,
  startedAt: 0,
  elapsedMs: null,
  hint: null,
  records: [],
  storageError: false,
  serviceError: false,
  currentRecord: null,
  rounds: 0,
}))
let generation = 0
let countdownTimer: ReturnType<typeof setTimeout> | undefined
let startController: AbortController | undefined
const clock = () =>
  typeof performance === 'undefined' ? Date.now() : performance.now()
export function validateParticipant(participant: Participant) {
  if (participant.actor === 'human') return HUMAN
  if (
    participant.actor !== 'ai' ||
    !participant.model_id?.trim() ||
    participant.model_id.length > 100 ||
    !participant.harness?.trim() ||
    participant.harness.length > 60 ||
    !participant.agent_name?.trim() ||
    participant.agent_name.length > 80
  ) {
    throw new Error(
      'Model ID, harness and agent name are required before AI play'
    )
  }
  return {
    actor: 'ai' as const,
    model_id: participant.model_id.trim(),
    harness: participant.harness.trim(),
    agent_name: participant.agent_name.trim(),
  }
}
export async function startSignalGame(
  mode: 'practice' | 'challenge',
  size: number,
  participant: Participant = HUMAN,
  seed?: number
) {
  if (!(BOARD_SIZES as readonly number[]).includes(size)) {
    throw new Error('Unsupported board size')
  }
  const actor = validateParticipant(participant),
    epoch = ++generation,
    roundId = `${Date.now()}-${epoch}`
  clearTimeout(countdownTimer)
  startController?.abort()
  startController = new AbortController()
  if (mode === 'practice') {
    const next = createCircuit(seed, size)
    useSignalGame.setState({
      roundId,
      circuit: next,
      tiles: next.tiles,
      actions: [],
      participant: actor,
      mode,
      day: '',
      token: undefined,
      phase: 'playing',
      readyAt: 0,
      startedAt: 0,
      elapsedMs: null,
      hint: null,
      currentRecord: null,
      serviceError: false,
    })
    return
  }
  const previous = useSignalGame.getState()
  useSignalGame.setState({ phase: 'loading', serviceError: false })
  try {
    const attempt = await beginAttempt(size, actor, startController.signal)
    if (epoch !== generation) return
    const next = createCircuit(attempt.seed, size),
      readyAt = clock() + 3000
    useSignalGame.setState({
      roundId,
      circuit: next,
      tiles: next.tiles,
      actions: [],
      participant: actor,
      mode,
      day: attempt.day,
      token: attempt.token,
      phase: 'countdown',
      readyAt,
      startedAt: readyAt,
      elapsedMs: null,
      hint: null,
      currentRecord: null,
    })
    countdownTimer = setTimeout(() => {
      if (epoch === generation) useSignalGame.setState({ phase: 'playing' })
    }, 3000)
  } catch (error) {
    if (epoch === generation) {
      useSignalGame.setState((current) => ({
        ...previous,
        records: current.records,
        rounds: current.rounds,
        storageError: current.storageError,
        serviceError: true,
      }))
    }
    throw error
  }
}
let writes = Promise.resolve()
async function remember(record: GameRecord) {
  const old = useSignalGame.getState().records.find((r) => r.id === record.id)
  record = {
    ...record,
    uploaded_account: record.uploaded_account ?? old?.uploaded_account,
    server_id: record.server_id ?? old?.server_id,
    public: record.public ?? old?.public,
  }
  useSignalGame.setState((s) => ({
    records: [record, ...s.records.filter((r) => r.id !== record.id)],
  }))
  try {
    writes = writes
      .catch(() => undefined)
      .then(() =>
        putLocalRecord(
          useSignalGame.getState().records.find((r) => r.id === record.id) ??
            record
        )
      )
    await writes
    useSignalGame.setState({ storageError: false })
  } catch {
    useSignalGame.setState({ storageError: true })
  }
}
export async function loadSignalRecords() {
  try {
    const records = await readLocalRecords()
    useSignalGame.setState((s) => ({
      records: [
        ...new Map([...records, ...s.records].map((r) => [r.id, r])).values(),
      ].sort((a, b) => b.created_at - a.created_at),
      storageError: false,
    }))
  } catch {
    useSignalGame.setState({ storageError: true })
  }
}
export function rotateSignalTile(
  index: number,
  source: 'human' | 'ai' = 'human'
) {
  const state = useSignalGame.getState()
  if (state.phase !== 'playing') {
    throw new Error(
      state.phase === 'won'
        ? 'Round complete. Stop playing.'
        : 'Wait for the countdown to finish'
    )
  }
  if (source === 'ai' && state.participant.actor !== 'ai') {
    throw new Error(
      'Start an AI round with model ID, harness and agent name first'
    )
  }
  if (state.actions.length >= maxMoves(state.circuit.size)) {
    throw new Error('Move limit reached. Start another round.')
  }
  let tile = index
  if (index === -1) {
    if (state.mode === 'challenge') {
      throw new Error('Hints are forbidden in challenge mode')
    }
    tile = hintedTile(state.circuit, state.tiles) ?? -1
  }
  if (!Number.isInteger(tile) || tile < 0 || tile >= state.tiles.length) {
    throw new Error('Invalid tile index')
  }
  const tiles = [...state.tiles]
  tiles[tile] = rotateTile(tiles[tile])
  const actions = [...state.actions, index],
    won = traceCircuit(tiles, state.circuit.size).won
  useSignalGame.setState({
    tiles,
    actions,
    hint: index === -1 ? tile : null,
    ...(won ? { phase: 'won', rounds: state.rounds + 1 } : {}),
  })
  if (won) {
    const id =
      typeof crypto !== 'undefined' && crypto.randomUUID
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random()}`
    const record: GameRecord = {
      id,
      mode: state.mode,
      day: state.day,
      size: state.circuit.size,
      seed: state.circuit.seed,
      actions,
      token: state.token,
      ...state.participant,
      elapsed_ms:
        state.mode === 'challenge'
          ? Math.max(0, Math.round(clock() - state.startedAt))
          : null,
      created_at: Date.now(),
      verified: state.mode === 'practice',
    }
    useSignalGame.setState({ currentRecord: id, elapsedMs: record.elapsed_ms })
    void remember(record).then(() => {
      if (record.mode === 'challenge') {
        void verifySignalRecord(id).catch(() => undefined)
      }
    })
  }
}
export async function verifySignalRecord(id: string) {
  const record = useSignalGame.getState().records.find((r) => r.id === id)
  if (!record || record.mode !== 'challenge') {
    throw new Error('Challenge record not found')
  }
  try {
    const result = await finishAttempt(record)
    const next = { ...record, elapsed_ms: result.elapsed_ms, verified: true }
    await remember(next)
    useSignalGame.setState((s) => ({
      serviceError: false,
      ...(s.currentRecord === id ? { elapsedMs: result.elapsed_ms } : {}),
    }))
    return next
  } catch (error) {
    useSignalGame.setState({ serviceError: true })
    throw error
  }
}
export async function uploadSignalRecord(
  id: string,
  publish = false,
  email = '',
  note = ''
) {
  const user = useAuthStore.getState().auth.user
  if (!user) {
    throw new Error(
      'Sign in before submitting a score. Do not register automatically.'
    )
  }
  let record = useSignalGame.getState().records.find((r) => r.id === id)
  if (!record) throw new Error('Local record not found')
  if (record.uploaded_account && record.uploaded_account !== user.id) {
    throw new Error('This record belongs to another account')
  }
  if (record.mode === 'practice' && publish) {
    throw new Error('Practice mode is not ranked')
  }
  if (record.mode === 'challenge' && !record.verified) {
    record = await verifySignalRecord(id)
  }
  if (useAuthStore.getState().auth.user?.id !== user.id) {
    throw new Error('Account changed before submission')
  }
  const saved = await saveRecord(record, publish, email, note, user.id)
  await remember({
    ...record,
    uploaded_account: user.id,
    server_id: saved.id,
    public: saved.public,
  })
  return saved
}
export function signalSnapshot() {
  const s = useSignalGame.getState()
  return {
    round_id: s.roundId,
    rules_version: 2,
    size: s.circuit.size,
    mode: s.mode,
    day: s.day,
    phase: s.phase,
    countdown_remaining_ms:
      s.phase === 'countdown' ? Math.max(0, Math.ceil(s.readyAt - clock())) : 0,
    moves: s.actions.length,
    elapsed_ms:
      s.mode === 'challenge'
        ? (s.elapsedMs ??
          (s.phase === 'playing'
            ? Math.max(0, Math.round(clock() - s.startedAt))
            : 0))
        : null,
    participant: s.participant,
    input: {
      tile_index: Math.floor(s.circuit.size / 2) * s.circuit.size,
      port: 'west',
    },
    output: {
      tile_index:
        Math.floor(s.circuit.size / 2) * s.circuit.size + s.circuit.size - 1,
      port: 'east',
    },
    port_bits: { north: 1, east: 2, south: 4, west: 8 },
    tiles: s.tiles,
    connected_path: traceCircuit(s.tiles, s.circuit.size).path,
    can_hint: s.mode === 'practice' && s.phase === 'playing',
    record_id: s.currentRecord,
    max_moves: maxMoves(s.circuit.size),
  }
}
