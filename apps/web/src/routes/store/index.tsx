/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StorePage } from '@/features/store/store-page'

export const Route = createFileRoute('/store/')({ component: StorePage })
