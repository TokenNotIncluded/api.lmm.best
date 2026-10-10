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
import type { AssistantIntent } from './api'
import type { AssistantPresetId } from './assistant-events'

export type AssistantNavigationPath =
  | '/pricing'
  | '/wallet'
  | '/usage-logs'
  | '/keys'
  | '/drawing'
  | '/models'
  | '/open-source-bounties'
  | '/support'

const INTENT_PRESET: Partial<Record<AssistantIntent, AssistantPresetId>> = {
  onboarding: 'onboarding',
  plan_purchase: 'plan',
  api_key: 'api-key',
  client_setup: 'client-setup',
  cost: 'cost',
  bounty: 'bounty',
  usage: 'usage',
  models: 'models',
  invitation: 'invitation',
  human_support: 'human',
}

export function getAssistantPresetForIntent(
  intent: AssistantIntent | undefined
): AssistantPresetId | undefined {
  return intent ? INTENT_PRESET[intent] : undefined
}

const NAVIGATION_WORDS =
  /(?:打开|进入|跳转|前往|查看|go to|open|take me to|show me)/i

/**
 * Navigation is opt-in: an intent alone only suggests a tool card. We only
 * leave the assistant surface when the user explicitly asks to open a page.
 */
export function getExplicitAssistantNavigation(
  message: string,
  intent: AssistantIntent | undefined
): AssistantNavigationPath | undefined {
  if (!intent || !NAVIGATION_WORDS.test(message)) return undefined
  switch (intent) {
    case 'onboarding':
      return '/wallet'
    case 'plan_purchase':
      return '/pricing'
    case 'api_key':
      return '/keys'
    case 'usage':
      return '/usage-logs'
    case 'models':
      return '/models'
    case 'bounty':
      return '/open-source-bounties'
    case 'invitation':
      return '/wallet'
    case 'human_support':
      return '/support'
    default:
      return undefined
  }
}

export function isExplicitAssistantL1Request(message: string): boolean {
  const normalized = message.toLocaleLowerCase().trim()
  return [
    /(?:申请|开通|解锁|升级|审核|需要|想要).{0,12}l1/i,
    /l1.{0,12}(?:申请|开通|解锁|升级|审核|需要|想要)/i,
    /(?:request|apply|unlock|upgrade|review|need|want).{0,12}l1/i,
    /(?:developer|api) access.{0,20}(?:request|apply|unlock|upgrade|need|want)/i,
    /(?:request|apply|unlock|upgrade|need|want).{0,20}(?:l1|developer|api) access?/i,
  ].some((pattern) => pattern.test(normalized))
}
