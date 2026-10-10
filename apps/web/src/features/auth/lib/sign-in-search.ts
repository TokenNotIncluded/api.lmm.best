/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { z } from 'zod'

export const signInSearchSchema = z.object({
  redirect: z.string().optional().catch(undefined),
  // The router parses raw `reauth=1` as a number before validation.
  reauth: z
    .union([z.literal('1'), z.literal(1)])
    .transform(() => '1' as const)
    .optional()
    .catch(undefined),
})
