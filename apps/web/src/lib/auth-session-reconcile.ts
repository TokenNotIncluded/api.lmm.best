/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  beginAuthCookieReload,
  getAuthCookieBlockedState,
  isAuthCookieIdle,
  waitForAuthCookieIdle,
} from '@/lib/auth-cookie'
import { clearAuthentication } from '@/lib/auth-session'
import type { AuthSessionSyncEvent } from '@/lib/auth-session-sync'
import { useAuthStore } from '@/stores/auth-store'

export function createAuthSessionReconciler(runtime: {
  reload: () => void
  signedOut: () => void
}) {
  let disposed = false
  return {
    dispose() {
      disposed = true
    },
    async handle(event: AuthSessionSyncEvent): Promise<void> {
      const observed = useAuthStore.getState().auth
      // A new writer can enqueue between the idle promise resolving and this
      // continuation. Recheck here, then keep reconciliation synchronous.
      do {
        await waitForAuthCookieIdle()
      } while (!isAuthCookieIdle())
      if (disposed) return
      const current = useAuthStore.getState().auth
      if (event.kind === 'authenticated') {
        const validFlow =
          current.pending2FAFlowToken &&
          (current.pending2FAFlowExpiresAt === null ||
            current.pending2FAFlowExpiresAt > Date.now() / 1000)
        if (getAuthCookieBlockedState() || validFlow) return
        if (event.sid === current.session?.sid) return
        // A queued local login/challenge is an explicit account action. Its
        // completed result supersedes a foreign event received while waiting.
        if (
          current.user?.id !== observed.user?.id ||
          current.session?.sid !== observed.session?.sid ||
          current.accessToken !== observed.accessToken ||
          current.pending2FAFlowToken !== observed.pending2FAFlowToken
        ) {
          return
        }
        if (!beginAuthCookieReload()) return
        if (current.session) clearAuthentication(false)
        runtime.reload()
        return
      }
      if (current.session?.sid === event.sid) {
        // Revoking the older signed-in account must not cancel a separate
        // password-to-MFA ceremony already committed by this document.
        clearAuthentication(false, 'complete', true)
        if (!useAuthStore.getState().auth.pending2FAFlowToken) {
          runtime.signedOut()
        }
      }
    },
  }
}
