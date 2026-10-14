/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { RefreshOutcome } from '@/lib/auth-session'

export const AUTH_BOOTSTRAP_NAVIGATION_MS = 30_000

export class AuthBootstrapPendingError extends Error {
  constructor(
    readonly task: Promise<RefreshOutcome>,
    readonly href: string
  ) {
    super('Authentication is still being checked')
    this.name = 'AuthBootstrapPendingError'
  }
}

/** Bounds only navigation waiting, never the shared Cookie transport. */
export async function waitForProtectedAuthentication(
  task: Promise<RefreshOutcome>,
  href: string,
  signal?: AbortSignal,
  budgetMs = AUTH_BOOTSTRAP_NAVIGATION_MS
): Promise<void> {
  if (signal?.aborted) {
    throw new DOMException('Navigation cancelled', 'AbortError')
  }
  let timer: ReturnType<typeof setTimeout> | undefined
  let abort: (() => void) | undefined
  try {
    await Promise.race([
      task,
      new Promise<never>((_resolve, reject) => {
        timer = setTimeout(
          () => reject(new AuthBootstrapPendingError(task, href)),
          budgetMs
        )
        abort = () =>
          reject(new DOMException('Navigation cancelled', 'AbortError'))
        signal?.addEventListener('abort', abort, { once: true })
      }),
    ])
  } finally {
    if (timer !== undefined) clearTimeout(timer)
    if (abort) signal?.removeEventListener('abort', abort)
  }
}
