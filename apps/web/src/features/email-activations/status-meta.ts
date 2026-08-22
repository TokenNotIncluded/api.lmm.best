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
  Alert02Icon,
  CancelCircleIcon,
  CheckmarkCircle02Icon,
  Clock01Icon,
  InformationCircleIcon,
  Loading03Icon,
} from '@hugeicons/core-free-icons'
import type { TFunction } from 'i18next'

export type HeroSmsStatusTone =
  | 'default'
  | 'secondary'
  | 'warning'
  | 'destructive'
  | 'outline'

export type HeroSmsStatusPresentation = {
  label: string
  tone: HeroSmsStatusTone
  icon: typeof Clock01Icon
}

function normalizeStatus(status: string | null | undefined) {
  return String(status ?? 'unknown').trim().toLowerCase().replaceAll(' ', '_')
}

export function isHeroSmsActiveStatus(status: string | null | undefined) {
  const normalized = normalizeStatus(status)
  return [
    'pending_provider',
    'active',
    'reconciling',
    'cancel_pending',
  ].includes(normalized)
}

export function canCancelHeroSmsActivation(status: string | null | undefined) {
  const normalized = normalizeStatus(status)
  return ['pending_provider', 'active', 'reconciling'].includes(normalized)
}

export function canReorderHeroSmsActivation(status: string | null | undefined) {
  const normalized = normalizeStatus(status)
  return ['completed', 'cancelled', 'expired', 'refunded'].includes(normalized)
}

export function getHeroSmsStatusPresentation(
  status: string | null | undefined,
  t: TFunction
): HeroSmsStatusPresentation {
  switch (normalizeStatus(status)) {
    case 'pending_provider':
      return {
        label: t('Pending purchase'),
        tone: 'warning',
        icon: Loading03Icon,
      }
    case 'active':
      return {
        label: t('Awaiting code'),
        tone: 'secondary',
        icon: Clock01Icon,
      }
    case 'completed':
      return {
        label: t('Code received'),
        tone: 'default',
        icon: CheckmarkCircle02Icon,
      }
    case 'cancelled':
      return {
        label: t('Cancelled'),
        tone: 'outline',
        icon: CancelCircleIcon,
      }
    case 'expired':
      return {
        label: t('Expired'),
        tone: 'outline',
        icon: Clock01Icon,
      }
    case 'reconciling':
      return {
        label: t('Reconciling'),
        tone: 'warning',
        icon: InformationCircleIcon,
      }
    case 'cancel_pending':
      return {
        label: t('Cancel pending'),
        tone: 'warning',
        icon: Loading03Icon,
      }
    case 'refunded':
      return {
        label: t('Refunded'),
        tone: 'outline',
        icon: InformationCircleIcon,
      }
    case 'failed':
      return {
        label: t('Failed'),
        tone: 'destructive',
        icon: Alert02Icon,
      }
    default:
      return {
        label: t('Unknown status'),
        tone: 'outline',
        icon: InformationCircleIcon,
      }
  }
}

export function getHeroSmsStatusOptions(t: TFunction) {
  return [
    { label: t('All statuses'), value: 'all' },
    { label: t('Pending purchase'), value: 'pending_provider' },
    { label: t('Awaiting code'), value: 'active' },
    { label: t('Code received'), value: 'completed' },
    { label: t('Reconciling'), value: 'reconciling' },
    { label: t('Cancel pending'), value: 'cancel_pending' },
    { label: t('Cancelled'), value: 'cancelled' },
    { label: t('Expired'), value: 'expired' },
    { label: t('Refunded'), value: 'refunded' },
    { label: t('Failed'), value: 'failed' },
  ]
}
