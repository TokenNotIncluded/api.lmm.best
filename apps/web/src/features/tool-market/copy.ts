/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import type { TFunction } from 'i18next'

export function marketStatus(value: string, t: TFunction): string {
  switch (value) {
    case 'reserved':
    case 'held':
      return t('Reserved')
    case 'settled':
      return t('Settled')
    case 'released':
      return t('Released')
    case 'draft':
      return t('Draft')
    case 'pending':
      return t('Pending review')
    case 'rejected':
      return t('Rejected')
    case 'published':
      return t('Published')
    case 'paused':
    case 'suspended':
      return t('Paused')
    case 'cancelled':
      return t('Cancelled')
    case 'succeeded':
      return t('Succeeded')
    case 'running':
      return t('Running')
    case 'failed':
      return t('Failed')
    default:
      return t('Unknown')
  }
}

export function marketPermission(value: string, t: TFunction): string {
  switch (value) {
    case 'read':
      return t('Read')
    case 'write':
      return t('Write')
    case 'delete':
      return t('Delete')
    case 'send':
      return t('Send')
    case 'network':
      return t('Network')
    case 'files':
      return t('Files')
    case 'external_account':
      return t('External account')
    default:
      return t('Unknown')
  }
}

export function marketPermissionList(raw: string, t: TFunction): string {
  try {
    const values: unknown = JSON.parse(raw)
    return Array.isArray(values)
      ? values.map((value) => marketPermission(String(value), t)).join(', ')
      : t('None')
  } catch {
    return t('Unknown')
  }
}
