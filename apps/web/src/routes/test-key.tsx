/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { createFileRoute } from '@tanstack/react-router'

import { TestKeyPage } from '@/features/test-key/test-key-page'

export const Route = createFileRoute('/test-key')({ component: TestKeyPage })
