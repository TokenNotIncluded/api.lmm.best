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
import type { AssistantCreateKeyAction, AssistantCreatedKey } from './api'
import type {
  AssistantGroupWarning,
  AssistantSelectableGroup,
} from './assistant-key-contract'

type PreparedSource = 'external' | 'local'

export type AssistantKeyCreationPhase =
  | { kind: 'draft' }
  | {
      kind: 'warning'
      warning: AssistantGroupWarning
      confirmations: number
    }
  | { kind: 'preparing' }
  | { kind: 'external'; action: AssistantCreateKeyAction }
  | {
      kind: 'reviewing'
      action: AssistantCreateKeyAction
      source: PreparedSource
      twoFactorCode: string
    }
  | {
      kind: 'confirming'
      action: AssistantCreateKeyAction
      source: PreparedSource
      twoFactorCode: string
    }
  | { kind: 'created'; key: AssistantCreatedKey }

export type AssistantKeyCreationState = {
  name: string
  group: string
  phase: AssistantKeyCreationPhase
}

type CreationEvent =
  | { type: 'set-name'; name: string }
  | { type: 'set-group'; group: string }
  | { type: 'load-external'; action: AssistantCreateKeyAction }
  | { type: 'clear-external' }
  | { type: 'show-warning'; warning: AssistantGroupWarning; count: number }
  | { type: 'start-preparing' }
  | { type: 'prepared'; action: AssistantCreateKeyAction }
  | { type: 'review-external'; action: AssistantCreateKeyAction }
  | {
      type: 'start-confirming'
      action: AssistantCreateKeyAction
      source: PreparedSource
      twoFactorCode: string
    }
  | { type: 'set-two-factor'; code: string }
  | { type: 'confirmation-failed' }
  | { type: 'created'; key: AssistantCreatedKey }
  | { type: 'reset' }

function reduceDraftEvent(
  state: AssistantKeyCreationState,
  event: CreationEvent
): AssistantKeyCreationState | undefined {
  switch (event.type) {
    case 'set-name':
      return state.phase.kind === 'draft'
        ? { ...state, name: event.name }
        : state
    case 'set-group':
      return state.phase.kind === 'draft'
        ? { ...state, group: event.group }
        : state
    case 'load-external':
      return {
        name: event.action.name,
        group: event.action.group,
        phase: { kind: 'external', action: event.action },
      }
    case 'clear-external':
      return state.phase.kind === 'external' ||
        (state.phase.kind === 'reviewing' && state.phase.source === 'external')
        ? { ...state, phase: { kind: 'draft' } }
        : state
    case 'reset':
      return { ...state, phase: { kind: 'draft' } }
    default:
      return undefined
  }
}

function reducePreparationEvent(
  state: AssistantKeyCreationState,
  event: CreationEvent
): AssistantKeyCreationState | undefined {
  switch (event.type) {
    case 'show-warning':
      return {
        ...state,
        phase: {
          kind: 'warning',
          warning: event.warning,
          confirmations: event.count,
        },
      }
    case 'start-preparing':
      return { ...state, phase: { kind: 'preparing' } }
    case 'prepared':
    case 'review-external':
      return {
        name: event.action.name,
        group: event.action.group,
        phase: {
          kind: 'reviewing',
          action: event.action,
          source: event.type === 'prepared' ? 'local' : 'external',
          twoFactorCode: '',
        },
      }
    default:
      return undefined
  }
}

function reduceConfirmationEvent(
  state: AssistantKeyCreationState,
  event: CreationEvent
): AssistantKeyCreationState | undefined {
  switch (event.type) {
    case 'set-two-factor':
      return state.phase.kind === 'reviewing'
        ? {
            ...state,
            phase: { ...state.phase, twoFactorCode: event.code },
          }
        : state
    case 'start-confirming':
      return {
        name: event.action.name,
        group: event.action.group,
        phase: {
          kind: 'confirming',
          action: event.action,
          source: event.source,
          twoFactorCode: event.twoFactorCode,
        },
      }
    case 'confirmation-failed':
      return state.phase.kind === 'confirming'
        ? {
            ...state,
            phase: { ...state.phase, kind: 'reviewing' },
          }
        : state
    case 'created':
      return { ...state, phase: { kind: 'created', key: event.key } }
    default:
      return undefined
  }
}

function reducer(
  state: AssistantKeyCreationState,
  event: CreationEvent
): AssistantKeyCreationState {
  return (
    reduceDraftEvent(state, event) ??
    reducePreparationEvent(state, event) ??
    reduceConfirmationEvent(state, event) ??
    state
  )
}

function initialState(
  name: string,
  action?: AssistantCreateKeyAction
): AssistantKeyCreationState {
  return action
    ? {
        name: action.name,
        group: action.group,
        phase: { kind: 'external', action },
      }
    : { name, group: '', phase: { kind: 'draft' } }
}

function phaseAction(phase: AssistantKeyCreationPhase) {
  return 'action' in phase ? phase.action : undefined
}

function selectedGroup(
  state: AssistantKeyCreationState,
  groups: AssistantSelectableGroup[]
) {
  if (phaseAction(state.phase)) return state.group
  if (groups.some((option) => option.id === state.group)) return state.group
  return groups[0]?.id ?? ''
}

export const assistantKeyCreationMachine = {
  initialState,
  phaseAction,
  reducer,
  selectedGroup,
}
