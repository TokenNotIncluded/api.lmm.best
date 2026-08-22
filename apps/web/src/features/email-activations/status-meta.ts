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
    'pending',
    'processing',
    'paid',
    'purchased',
    'ready',
    'waiting',
    'waiting_code',
    'awaiting_code',
    'reconciling',
    'cancel_pending',
    'refund_pending',
  ].includes(normalized)
}

export function canCancelHeroSmsActivation(status: string | null | undefined) {
  const normalized = normalizeStatus(status)
  return [
    'pending',
    'processing',
    'paid',
    'purchased',
    'ready',
    'waiting',
    'waiting_code',
    'awaiting_code',
    'reconciling',
  ].includes(normalized)
}

export function canReorderHeroSmsActivation(status: string | null | undefined) {
  const normalized = normalizeStatus(status)
  return [
    'paid',
    'completed',
    'expired',
    'cancelled',
    'refunded',
    'refund_pending',
  ].includes(normalized)
}

export function getHeroSmsStatusPresentation(
  status: string | null | undefined,
  t: TFunction
): HeroSmsStatusPresentation {
  switch (normalizeStatus(status)) {
    case 'pending':
    case 'processing':
      return {
        label: t('Pending purchase'),
        tone: 'warning',
        icon: Loading03Icon,
      }
    case 'paid':
      return {
        label: t('Paid'),
        tone: 'default',
        icon: CheckmarkCircle02Icon,
      }
    case 'ready':
    case 'waiting':
    case 'waiting_code':
    case 'awaiting_code':
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
    case 'refund_pending':
      return {
        label: t('Refund pending'),
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
    { label: t('Pending purchase'), value: 'pending' },
    { label: t('Paid'), value: 'paid' },
    { label: t('Awaiting code'), value: 'waiting_code' },
    { label: t('Code received'), value: 'completed' },
    { label: t('Reconciling'), value: 'reconciling' },
    { label: t('Cancel pending'), value: 'cancel_pending' },
    { label: t('Refund pending'), value: 'refund_pending' },
    { label: t('Cancelled'), value: 'cancelled' },
    { label: t('Refunded'), value: 'refunded' },
    { label: t('Expired'), value: 'expired' },
    { label: t('Failed'), value: 'failed' },
  ]
}
