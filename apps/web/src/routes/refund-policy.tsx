/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'
import { RefundPolicy } from '@/features/legal/refund-policy'

export const Route = createFileRoute('/refund-policy')({ component: RefundPolicy })
