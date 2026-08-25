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
import { useQuery } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { getUserGroups } from '@/lib/api'

import {
  AssistantRequestError,
  confirmAssistantDefaultKey,
  prepareAssistantDefaultKey,
  type AssistantCreateKeyAction,
} from './api'
import {
  isAuthoritativePreparedKeyAction,
  selectableAssistantKeyGroups,
} from './assistant-key-contract'
import { assistantKeyCreationMachine } from './assistant-key-creation-machine'

type PreparedSource = 'external' | 'local'

const INVALIDATING_CONFIRMATION_CODES = new Set([
  'ASSISTANT_KEY_CONFIRMATION_INVALID',
  'ASSISTANT_INVALID_GROUP',
  'ASSISTANT_GROUP_WARNING_CHANGED',
])

export function useAssistantKeyCreation(options: {
  developerAccessGranted: boolean
  confirmationAction?: AssistantCreateKeyAction | null
  autoConfirm?: boolean
  onKeyCreated?: () => void
  onKeyPreparationInvalid?: () => void
}) {
  const { t } = useTranslation()
  const {
    autoConfirm,
    confirmationAction,
    developerAccessGranted,
    onKeyCreated,
    onKeyPreparationInvalid,
  } = options
  const [state, dispatch] = useReducer(
    assistantKeyCreationMachine.reducer,
    assistantKeyCreationMachine.initialState(
      t('AI assistant key'),
      confirmationAction ?? undefined
    )
  )
  const operationRef = useRef<'prepare' | 'confirm' | null>(null)
  const externalTokenRef = useRef(
    confirmationAction?.confirmation_token ?? null
  )
  const autoAttemptedTokenRef = useRef<string | null>(null)
  const groupsQuery = useQuery({
    queryKey: ['assistant-user-groups'],
    queryFn: getUserGroups,
    enabled: developerAccessGranted,
    staleTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: true,
    retry: false,
  })
  const groups = useMemo(
    () => selectableAssistantKeyGroups(groupsQuery.data),
    [groupsQuery.data]
  )
  const selectedGroup = assistantKeyCreationMachine.selectedGroup(state, groups)
  const refetchGroups = groupsQuery.refetch

  useEffect(() => {
    const nextToken = confirmationAction?.confirmation_token ?? null
    if (nextToken === externalTokenRef.current) return
    externalTokenRef.current = nextToken
    autoAttemptedTokenRef.current = null
    if (confirmationAction) {
      dispatch({ type: 'load-external', action: confirmationAction })
    } else {
      dispatch({ type: 'clear-external' })
    }
  }, [confirmationAction])

  const loadLiveGroups = useCallback(async () => {
    const result = await refetchGroups()
    const liveGroups = selectableAssistantKeyGroups(result.data)
    if (result.isError) {
      throw new Error(t('Unable to load selectable key groups. Try again.'))
    }
    if (liveGroups.length === 0) {
      throw new Error(
        t('No selectable key groups are available for this account.')
      )
    }
    return liveGroups
  }, [refetchGroups, t])

  const invalidateAction = useCallback(
    (message: string) => {
      dispatch({ type: 'reset' })
      onKeyPreparationInvalid?.()
      toast.error(message)
    },
    [onKeyPreparationInvalid]
  )

  const prepareDraft = useCallback(
    async (warningConfirmations: number) => {
      if (operationRef.current) return
      operationRef.current = 'prepare'
      try {
        const liveGroups = await loadLiveGroups()
        const requested = { name: state.name.trim(), group: selectedGroup }
        const selected = liveGroups.find(
          (option) => option.id === requested.group
        )
        if (!selected) {
          toast.error(
            t(
              'The selected key group is no longer available. Choose a current group and prepare again.'
            )
          )
          return
        }
        if (
          selected.warning?.enabled &&
          warningConfirmations !== selected.warning.confirmations
        ) {
          dispatch({
            type: 'show-warning',
            warning: selected.warning,
            count: 0,
          })
          return
        }
        dispatch({ type: 'start-preparing' })
        const action = await prepareAssistantDefaultKey(
          requested.name,
          requested.group,
          warningConfirmations
        )
        if (!isAuthoritativePreparedKeyAction(action, liveGroups, requested)) {
          invalidateAction(
            t(
              'The server returned an invalid key preparation. Refresh the page and try again.'
            )
          )
          return
        }
        dispatch({ type: 'prepared', action })
      } catch (error) {
        dispatch({ type: 'reset' })
        toast.error(
          error instanceof Error
            ? error.message
            : t('Unable to prepare API key')
        )
      } finally {
        operationRef.current = null
      }
    },
    [invalidateAction, loadLiveGroups, selectedGroup, state.name, t]
  )

  const review = useCallback(async () => {
    if (state.phase.kind === 'draft') {
      await prepareDraft(0)
      return
    }
    if (state.phase.kind !== 'external' || operationRef.current) return
    operationRef.current = 'prepare'
    try {
      const liveGroups = await loadLiveGroups()
      if (!isAuthoritativePreparedKeyAction(state.phase.action, liveGroups)) {
        invalidateAction(
          t(
            'The server returned an invalid key preparation. Refresh the page and try again.'
          )
        )
        return
      }
      dispatch({ type: 'review-external', action: state.phase.action })
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Unable to prepare API key')
      )
    } finally {
      operationRef.current = null
    }
  }, [invalidateAction, loadLiveGroups, prepareDraft, state.phase, t])

  const confirmAction = useCallback(
    async (
      action: AssistantCreateKeyAction,
      source: PreparedSource,
      twoFactorCode: string
    ) => {
      if (operationRef.current) return
      operationRef.current = 'confirm'
      try {
        const liveGroups = await loadLiveGroups()
        if (!isAuthoritativePreparedKeyAction(action, liveGroups)) {
          invalidateAction(
            t(
              'The selected key group is no longer available. Choose a current group and prepare again.'
            )
          )
          return
        }
        dispatch({
          type: 'start-confirming',
          action,
          source,
          twoFactorCode,
        })
        const key = await confirmAssistantDefaultKey(
          action.confirmation_token,
          twoFactorCode
        )
        dispatch({ type: 'created', key })
        onKeyCreated?.()
        toast.success(t('API key created'))
      } catch (error) {
        if (
          error instanceof AssistantRequestError &&
          INVALIDATING_CONFIRMATION_CODES.has(error.code ?? '')
        ) {
          invalidateAction(error.message)
        } else {
          dispatch({ type: 'confirmation-failed' })
          toast.error(
            error instanceof Error
              ? error.message
              : t('Unable to create API key')
          )
        }
      } finally {
        operationRef.current = null
      }
    },
    [invalidateAction, loadLiveGroups, onKeyCreated, t]
  )

  const confirm = useCallback(async () => {
    if (state.phase.kind !== 'reviewing') return
    await confirmAction(
      state.phase.action,
      state.phase.source,
      state.phase.twoFactorCode
    )
  }, [confirmAction, state.phase])

  const acknowledgeWarning = useCallback(() => {
    if (state.phase.kind !== 'warning') return
    const next = state.phase.confirmations + 1
    if (next < state.phase.warning.confirmations) {
      dispatch({
        type: 'show-warning',
        warning: state.phase.warning,
        count: next,
      })
      return
    }
    void prepareDraft(next)
  }, [prepareDraft, state.phase])

  const dismiss = useCallback(() => {
    const action = assistantKeyCreationMachine.phaseAction(state.phase)
    dispatch({ type: 'reset' })
    if (action) {
      onKeyPreparationInvalid?.()
    }
  }, [onKeyPreparationInvalid, state.phase])

  useEffect(() => {
    if (
      !autoConfirm ||
      state.phase.kind !== 'external' ||
      autoAttemptedTokenRef.current === state.phase.action.confirmation_token
    ) {
      return
    }
    autoAttemptedTokenRef.current = state.phase.action.confirmation_token
    void confirmAction(state.phase.action, 'external', '')
  }, [autoConfirm, confirmAction, state.phase])

  return {
    state,
    groups,
    selectedGroup,
    groupsLoading: groupsQuery.isLoading,
    groupsError: groupsQuery.isError,
    setName: (name: string) => dispatch({ type: 'set-name', name }),
    setGroup: (group: string) => dispatch({ type: 'set-group', group }),
    setTwoFactorCode: (code: string) =>
      dispatch({ type: 'set-two-factor', code }),
    review,
    confirm,
    acknowledgeWarning,
    dismiss,
  }
}
