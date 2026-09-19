import { RULES_VERSION } from '@/features/auth/components/signal-game'
/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { api } from '@/lib/api'

export type Participant = {
  actor: 'human' | 'ai'
  model_id: string
  harness: string
  agent_name: string
}
export const HUMAN: Participant = {
  actor: 'human',
  model_id: '',
  harness: '',
  agent_name: '',
}
export type GameRecord = Participant & {
  id: string
  mode: 'practice' | 'challenge'
  day: string
  size: number
  seed: number
  actions: number[]
  token?: string
  elapsed_ms: number | null
  created_at: number
  verified: boolean
  uploaded_account?: number
  server_id?: number
  public?: boolean
}
export type Rank = Participant & {
  player: number
  name: string
  note: string
  moves: number
  elapsed_ms: number
  finished_at: number
}
export type SavedRecord = Participant & {
  id: number
  mode: 'practice' | 'challenge'
  size: number
  day: string
  moves: number
  elapsed_ms: number
  hints: number
  note: string
  public: boolean
  created_at: number
}
async function body<T>(
  request: Promise<{ data: { success: boolean; data: T; message?: string } }>
) {
  const result = (await request).data
  if (!result.success) throw new Error('Game service rejected the request')
  return result.data
}
export async function beginAttempt(
  size: number,
  participant: Participant,
  signal?: AbortSignal
) {
  const result = await body<{
    token: string
    seed: number
    size: number
    day: string
    rules_version: number
    countdown_seconds: number
  }>(
    api.post(
      '/api/games/signal/attempts',
      { size, ...participant },
      { timeout: 12000, signal }
    )
  )
  if (
    result.rules_version !== RULES_VERSION ||
    result.size !== size ||
    result.countdown_seconds !== 3 ||
    typeof result.token !== 'string' ||
    result.token.length !== 43 ||
    !Number.isInteger(result.seed)
  ) {
    throw new Error('Game rules mismatch')
  }
  return result
}
export const finishAttempt = (record: GameRecord, signal?: AbortSignal) =>
  body<{ moves: number; elapsed_ms: number; finished_at: number }>(
    api.post(
      '/api/games/signal/finish',
      { token: record.token, actions: record.actions },
      { timeout: 20000, signal }
    )
  )
export const saveRecord = (
  record: GameRecord,
  publish: boolean,
  email: string,
  note: string,
  signal?: AbortSignal
) =>
  body<SavedRecord>(
    api.post(
      '/api/games/signal/records',
      {
        mode: record.mode,
        size: record.size,
        seed: record.seed,
        rules_version: RULES_VERSION,
        actions: record.mode === 'practice' ? record.actions : undefined,
        token: record.token,
        publish,
        email,
        note,
        actor: record.actor,
        model_id: record.model_id,
        harness: record.harness,
        agent_name: record.agent_name,
      },
      { timeout: 20000, signal }
    )
  )
export const getLeaderboard = (
  size: number,
  day?: string,
  signal?: AbortSignal
) =>
  body<{ day: string; size: number; entries: Rank[] }>(
    api.get('/api/games/signal/leaderboard', {
      params: { size, ...(day ? { day } : {}) },
      timeout: 12000,
      signal,
    })
  )
export const getMyRecords = (signal?: AbortSignal) =>
  body<SavedRecord[]>(
    api.get('/api/games/signal/records', { timeout: 12000, signal })
  )
