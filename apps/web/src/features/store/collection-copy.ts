/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StoreCollectionUnavailableReason } from './catalogue-types'

export const STORE_COLLECTION_REASON_LABELS: Record<
  StoreCollectionUnavailableReason,
  string
> = {
  not_visible: 'Product unavailable',
  insufficient_stock: 'Insufficient stock',
  purchase_limit: 'Purchase limit reached',
  unavailable: 'Unavailable',
}
