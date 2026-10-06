/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { isAxiosError } from 'axios'

import { isRateLimitedError } from './request-rate-limit'

/** Rate limiting is server backpressure, not a transient failure to amplify. */
export function createQueryRetry(production: boolean, maximumRetries = 4) {
  return (failureCount: number, error: unknown): boolean =>
    production &&
    failureCount < maximumRetries &&
    !isRateLimitedError(error) &&
    !(
      isAxiosError(error) &&
      [401, 402, 403, 429].includes(error.response?.status ?? 0)
    )
}
