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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, RefreshCw } from 'lucide-react'
/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { AuthArtPanel } from '@/features/auth/components/auth-art-panel'
import { BOARD_SIZES, maxMoves } from '@/features/auth/components/signal-game'
import { useAuthStore } from '@/stores/auth-store'

import { getLeaderboard, getMyRecords } from './api'
import { formatGameTime } from './format'
import {
  loadSignalRecords,
  uploadSignalRecord,
  useSignalGame,
  verifySignalRecord,
} from './store'

export function SignalGamePage() {
  const { t } = useTranslation(),
    state = useSignalGame(),
    user = useAuthStore((s) => s.auth.user),
    queryClient = useQueryClient()
  const [selectedRankSize, setRankSize] = useState<number | null>(null),
    [selected, setSelected] = useState(''),
    [email, setEmail] = useState(''),
    [note, setNote] = useState(''),
    [pending, setPending] = useState(false),
    [message, setMessage] = useState(''),
    [copied, setCopied] = useState(false)
  useEffect(() => {
    void loadSignalRecords()
  }, [])
  const rankSize = selectedRankSize ?? state.circuit.size
  const board = useQuery({
    queryKey: ['signal-leaderboard', rankSize],
    queryFn: ({ signal }) => getLeaderboard(rankSize, undefined, signal),
    staleTime: 30000,
    retry: 1,
  })
  const mine = useQuery({
    queryKey: ['signal-records', user?.id],
    queryFn: ({ signal }) => getMyRecords(signal),
    enabled: !!user,
    staleTime: 30000,
    retry: false,
  })
  const record =
    state.records.find((r) => r.id === (selected || state.currentRecord)) ??
    state.records[0]
  const submit = async (publish: boolean) => {
    if (!record) return
    setPending(true)
    setMessage('')
    try {
      await uploadSignalRecord(record.id, publish, email, note)
      setMessage(t('Score saved'))
      void queryClient.invalidateQueries({ queryKey: ['signal-leaderboard'] })
      void queryClient.invalidateQueries({ queryKey: ['signal-records'] })
    } catch {
      setMessage(
        t('Score submission failed. Your local record is still available.')
      )
    } finally {
      setPending(false)
    }
  }
  const prompt = t(
    'Open {{url}} and play one {{size}} × {{size}} Signal path challenge through WebMCP. Before starting, provide your actual model ID, harness name and an agent name in lmm_signal_start. Ask me if an identity field is unknown; do not invent it. Read lmm_signal_state, wait for the three-second countdown, and use lmm_signal_rotate with the returned round_id. Hints are forbidden. Stop after winning or {{limit}} rotations; do not automatically start another round. Report moves and time. Submit with lmm_signal_submit only when I ask you to upload; it uses my signed-in account. If signed out, ask me to sign in manually and keep the local record. Do not register an account or perform payments.',
    {
      url:
        typeof window === 'undefined'
          ? '/games/signal'
          : `${window.location.origin}/games/signal`,
      size: state.circuit.size,
      limit: Math.min(2000, maxMoves(state.circuit.size)),
    }
  )
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(prompt)
      setCopied(true)
    } catch {
      setCopied(false)
      setMessage(t('Copy failed. Select the prompt below to copy it.'))
    }
  }
  return (
    <main className='mx-auto w-full max-w-7xl px-4 py-10 sm:px-8'>
      <div className='grid items-start gap-8 xl:grid-cols-[minmax(0,1.3fr)_minmax(20rem,1fr)]'>
        <AuthArtPanel />
        <div className='space-y-8'>
          <section aria-labelledby='signal-leaderboard-title'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
              <h2
                id='signal-leaderboard-title'
                className='text-2xl font-semibold'
              >
                {t('Leaderboard')}
              </h2>
              <select
                aria-label={t('Board size')}
                value={rankSize}
                onChange={(e) => setRankSize(Number(e.target.value))}
                className='bg-background rounded border p-2 text-sm'
              >
                {BOARD_SIZES.map((n) => (
                  <option value={n} key={n}>
                    {n} × {n}
                  </option>
                ))}
              </select>
              <Button
                type='button'
                variant='ghost'
                size='icon'
                aria-label={t('Refresh')}
                onClick={() => void board.refetch()}
              >
                <RefreshCw className='size-4' />
              </Button>
            </div>
            <p className='text-muted-foreground mt-2 text-sm'>
              {t(
                'Same daily board, ranked by moves, then time. Only submitted challenges are public.'
              )}{' '}
              {board.data?.day} UTC
            </p>
            <p className='text-muted-foreground mt-2 text-xs'>
              {t(
                'Browser and WebMCP indicate the entry used, not verified human or model identity.'
              )}
            </p>
            {board.isPending ? (
              <p className='py-6'>{t('Loading...')}</p>
            ) : board.isError ? (
              <p role='status' className='py-6'>
                {t('Leaderboard unavailable. You can still practice.')}
              </p>
            ) : !board.data.entries.length ? (
              <p className='text-muted-foreground py-6'>
                {t('No submitted scores yet')}
              </p>
            ) : (
              <ol className='mt-4 divide-y border-y'>
                {board.data.entries.map((row, index) => (
                  <li
                    key={`${row.player}-${row.agent_name}-${row.model_id}-${row.harness}`}
                    className='flex items-start gap-3 py-4'
                  >
                    <span className='text-muted-foreground w-6 shrink-0 tabular-nums'>
                      {index + 1}
                    </span>
                    <div className='min-w-0 flex-1'>
                      <p className='font-medium break-words'>
                        {row.name || `#${row.player}`}
                        {row.actor === 'ai' ? ` / ${row.agent_name}` : ''}
                      </p>
                      <p className='text-muted-foreground text-xs break-all'>
                        {row.actor === 'ai'
                          ? `WebMCP · ${row.harness} · ${row.model_id}`
                          : t('Browser player')}
                      </p>
                      {row.note && (
                        <p className='mt-1 text-sm break-words'>{row.note}</p>
                      )}
                    </div>
                    <div className='shrink-0 text-right text-sm tabular-nums'>
                      <p>
                        {row.moves} {t('Moves')}
                      </p>
                      <p className='text-muted-foreground'>
                        {formatGameTime(row.elapsed_ms)}
                      </p>
                    </div>
                  </li>
                ))}
              </ol>
            )}
          </section>
          <section aria-labelledby='signal-records-title'>
            <h2 id='signal-records-title' className='text-xl font-semibold'>
              {t('Local records')}
            </h2>
            <p className='text-muted-foreground mt-2 text-sm'>
              {t(
                'Guest results stay in this browser. Sign in later to upload them; clearing browser data removes local records.'
              )}
            </p>
            {!state.records.length ? (
              <p className='text-muted-foreground py-5'>
                {t('Finish a circuit to record your result')}
              </p>
            ) : (
              <>
                <label className='mt-4 block text-sm'>
                  {t('Select a record')}
                  <select
                    className='bg-background mt-1 block w-full rounded border p-2'
                    value={record?.id ?? ''}
                    onChange={(e) => setSelected(e.target.value)}
                  >
                    {state.records.map((r) => (
                      <option key={r.id} value={r.id}>
                        {r.mode === 'challenge'
                          ? t('Challenge')
                          : t('Practice')}{' '}
                        · {r.size}×{r.size} · {r.actions.length} {t('Moves')} ·{' '}
                        {r.actor === 'ai' ? r.agent_name : t('Browser player')}
                      </option>
                    ))}
                  </select>
                </label>
                {record && (
                  <div className='mt-3 text-sm'>
                    <p>
                      {t('Moves')}: {record.actions.length} · {t('Time')}:{' '}
                      {formatGameTime(record.elapsed_ms)}
                    </p>
                    <p className='text-muted-foreground'>
                      {new Date(record.created_at).toLocaleString()} ·{' '}
                      {record.actor === 'ai'
                        ? `${record.agent_name} / ${record.harness} / ${record.model_id}`
                        : t('Browser player')}
                    </p>
                    {record.mode === 'challenge' && !record.verified && (
                      <Button
                        type='button'
                        variant='outline'
                        className='mt-3'
                        onClick={() =>
                          void verifySignalRecord(record.id).catch(() =>
                            setMessage(
                              t(
                                'Challenge verification failed. Keep the local record and retry.'
                              )
                            )
                          )
                        }
                      >
                        {t('Retry score verification')}
                      </Button>
                    )}
                  </div>
                )}
                {!user ? (
                  <a
                    className='mt-4 inline-block underline underline-offset-4'
                    href='/sign-in?redirect=%2Fgames%2Fsignal'
                  >
                    {t('Sign in to submit your score')}
                  </a>
                ) : (
                  <div className='mt-4 space-y-3'>
                    <p className='text-sm'>
                      {t('Account')}: {user.display_name || user.username}
                      {record?.actor === 'ai' ? ` / ${record.agent_name}` : ''}
                    </p>
                    <label className='block text-sm'>
                      {t('Email (optional, private)')}
                      <Input
                        className='mt-1'
                        type='email'
                        maxLength={254}
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                        autoComplete='email'
                      />
                    </label>
                    <label className='block text-sm'>
                      {t('Public note (optional)')}
                      <Input
                        className='mt-1'
                        value={note}
                        maxLength={160}
                        onChange={(e) => setNote(e.target.value)}
                      />
                    </label>
                    <div className='flex flex-wrap gap-2'>
                      <Button
                        type='button'
                        disabled={pending || !record}
                        onClick={() => void submit(false)}
                      >
                        {t('Save to my records')}
                      </Button>
                      {record?.mode === 'challenge' && (
                        <Button
                          type='button'
                          variant='outline'
                          disabled={pending}
                          onClick={() => void submit(true)}
                        >
                          {t('Submit to leaderboard')}
                        </Button>
                      )}
                    </div>
                  </div>
                )}
              </>
            )}
            {message && (
              <p role='status' className='mt-3 text-sm'>
                {message}
              </p>
            )}
          </section>
          {user && (
            <section>
              <h2 className='text-xl font-semibold'>{t('Account records')}</h2>
              {mine.isError ? (
                <p className='mt-3'>{t('Could not load account records')}</p>
              ) : (
                <ul className='mt-3 divide-y'>
                  {mine.data?.map((r) => (
                    <li key={r.id} className='py-3 text-sm'>
                      {r.mode === 'challenge' ? t('Challenge') : t('Practice')}{' '}
                      · {r.size}×{r.size} · {r.moves} {t('Moves')} ·{' '}
                      {formatGameTime(
                        r.mode === 'challenge' ? r.elapsed_ms : null
                      )}
                      <span className='text-muted-foreground block'>
                        {r.actor === 'ai'
                          ? `${r.agent_name} / ${r.harness} / ${r.model_id}`
                          : t('Browser player')}{' '}
                        · {r.public ? t('Public') : t('Private')}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          )}
        </div>
      </div>
      <section
        className='mt-12 border-t pt-8'
        id='ai-guide'
        aria-labelledby='signal-ai-guide-title'
      >
        <h2 id='signal-ai-guide-title' className='text-2xl font-semibold'>
          {t('Let an AI play through WebMCP')}
        </h2>
        <ol className='text-muted-foreground mt-4 list-decimal space-y-3 pl-5 text-sm leading-relaxed'>
          <li>
            {t(
              'Use a browser agent that supports document.modelContext. Open this game page in that browser.'
            )}
          </li>
          <li>
            {t(
              'The agent must declare its model ID, harness and agent name before a round. Uploaded scores belong to the account signed in at submission.'
            )}
          </li>
          <li>
            {t(
              'Read the board with lmm_signal_state; rotate at most 32 tiles per lmm_signal_rotate call. Challenge hints are disabled, including through tools.'
            )}
          </li>
          <li>
            {t(
              'Guest play is allowed. Only lmm_signal_submit requires sign-in. It never signs in, creates accounts or spends credits for you.'
            )}
          </li>
        </ol>
        <div className='mt-5 flex flex-wrap gap-3'>
          <Button type='button' onClick={() => void copy()}>
            <Copy className='size-4' />
            {copied ? t('Copied') : t('Copy prompt for AI')}
          </Button>
          <a
            className='self-center text-sm underline underline-offset-4'
            href='/webmcp'
          >
            {t('WebMCP documentation')}
          </a>
        </div>
        <pre className='bg-muted mt-4 rounded-lg p-5 text-sm leading-relaxed break-words whitespace-pre-wrap'>
          {prompt}
        </pre>
      </section>
    </main>
  )
}
