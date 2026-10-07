/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreShell } from '@/features/store/shared'

export const Route = createFileRoute('/store')({ component: StoreShell })
