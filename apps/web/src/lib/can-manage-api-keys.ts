/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import type { AuthUser } from '@/stores/auth-store'

import { isConsoleActivated } from './console-activation'

/** A key-management capability does not grant other developer operations. */
export function canManageApiKeys(user: AuthUser | null | undefined): boolean {
  const capability = user?.permissions?.api_key_management
  if (typeof capability === 'boolean') return capability === true
  // Older servers keep their existing explicit developer-access gate.
  return isConsoleActivated(user)
}
