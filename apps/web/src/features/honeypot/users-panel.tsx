/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { getHoneypotUsers, type HoneypotScope } from './api'
import { HoneypotBulkActionButton } from './bulk-mark-dialog'
import { HoneypotFeedback } from './feedback'
import { legacyUserIDsToStrings } from './legacy-user-ids'

export function HoneypotUsersPanel(props: { scope: HoneypotScope }) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<{
    query: string
    state: string
    after: string | null
  }>({ query: '', state: 'all', after: null })
  const [selected, setSelected] = useState<string[]>([])
  const actorID = legacyUserIDsToStrings([props.scope.userId])?.[0]
  const users = useQuery({
    queryKey: [
      'honeypot-users-v2',
      props.scope.userId,
      props.scope.sessionId,
      filter,
    ],
    queryFn: () => getHoneypotUsers(props.scope, { ...filter, limit: 50 }),
    retry: false,
    refetchOnWindowFocus: false,
  })
  const rows = users.data?.users ?? []
  const nextAfter = users.data?.next_after
  function select(id: string, checked: boolean) {
    setSelected((value) =>
      checked
        ? [...new Set([...value, id])]
        : value.filter((item) => item !== id)
    )
  }
  return (
    <div className='space-y-4'>
      <form
        className='flex flex-wrap items-center gap-2'
        onSubmit={(event) => {
          event.preventDefault()
          setSelected([])
          setFilter((value) => ({ ...value, query, after: null }))
        }}
      >
        <Input
          className='min-w-0 flex-1 basis-56'
          aria-label={t('Username or email fragment')}
          placeholder={t('Username or email fragment')}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <NativeSelect
          value={filter.state}
          aria-label={t('Status')}
          onChange={(event) => {
            setSelected([])
            setFilter({ query, state: event.target.value, after: null })
          }}
        >
          {(['all', 'normal', 'marked', 'disabled'] as const).map((state) => (
            <NativeSelectOption key={state} value={state}>
              {t(
                {
                  all: 'All',
                  normal: 'Normal',
                  marked: 'Marked',
                  disabled: 'Disabled',
                }[state]
              )}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <Button type='submit'>{t('Search')}</Button>
        <Button
          type='button'
          variant='outline'
          disabled={users.isFetching}
          onClick={() => {
            setSelected([])
            void users.refetch()
          }}
        >
          {t('Reload')}
        </Button>
      </form>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Only selected accounts use these policies. No users are selected automatically.'
        )}
      </p>
      <HoneypotFeedback error={users.error} />
      {users.isPending && <p role='status'>{t('Loading...')}</p>}
      <div className='flex flex-wrap items-center gap-2'>
        <span className='text-sm'>
          {t('Selected {{count}} users', { count: selected.length })}
        </span>
        <HoneypotBulkActionButton
          ids={selected}
          onApplied={() => {
            setSelected([])
            void users.refetch()
          }}
        />
        {selected.length > 0 && (
          <Button type='button' variant='ghost' onClick={() => setSelected([])}>
            {t('Clear selection')}
          </Button>
        )}
      </div>
      <ul className='divide-y'>
        {rows.map((user) => (
          <li
            key={user.user_id}
            className='flex min-w-0 items-start gap-3 py-3'
          >
            <Checkbox
              aria-label={t('Select user {{id}}', { id: user.user_id })}
              checked={selected.includes(user.user_id)}
              disabled={
                user.platform_role !== 'user' ||
                !actorID ||
                user.user_id === actorID ||
                users.isFetching
              }
              onCheckedChange={(checked) =>
                select(user.user_id, checked === true)
              }
            />
            <div className='min-w-0 flex-1 space-y-1 text-sm'>
              <p className='font-medium break-all'>
                #{user.user_id} · {user.username}
              </p>
              <p className='text-muted-foreground break-all'>{user.email}</p>
              <p className='flex flex-wrap gap-x-3 gap-y-1'>
                <span>{t(user.active ? 'Enabled' : 'Disabled')}</span>
                <span>{t(user.marked ? 'Marked' : 'Normal')}</span>
                {user.policy_id && (
                  <span className='font-mono break-all'>{user.policy_id}</span>
                )}
                <span>
                  {t('Revision {{revision}}', { revision: user.revision })}
                </span>
              </p>
            </div>
          </li>
        ))}
      </ul>
      {!users.isPending && !users.error && rows.length === 0 && (
        <p>{t('No Users Found')}</p>
      )}
      <div className='flex gap-2'>
        {filter.after !== null && (
          <Button
            type='button'
            variant='outline'
            disabled={users.isFetching}
            onClick={() => {
              setSelected([])
              setFilter((value) => ({ ...value, after: null }))
            }}
          >
            {t('First page')}
          </Button>
        )}
        {nextAfter && (
          <Button
            type='button'
            variant='outline'
            disabled={users.isFetching}
            onClick={() => {
              setSelected([])
              setFilter((value) => ({
                ...value,
                after: nextAfter,
              }))
            }}
          >
            {t('Next page')}
          </Button>
        )}
      </div>
    </div>
  )
}
