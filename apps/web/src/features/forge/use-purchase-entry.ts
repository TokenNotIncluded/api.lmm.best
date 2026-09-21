/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { canOfferRegistration } from '@/features/auth/lib/registration'
import { useStatus } from '@/hooks/use-status'
import { isConsoleActivated } from '@/lib/console-activation'
import { isLocalPreview } from '@/lib/local-preview'
import { useAuthStore } from '@/stores/auth-store'

/** The entry follows server-granted access; a payment never grants access. */
export function usePurchaseEntry() {
  const user = useAuthStore((state) => state.auth.user)
  const { status, capabilitiesReady } = useStatus()

  if (isConsoleActivated(user)) {
    return { to: '/wallet', label: 'Add Funds' } as const
  }
  if (user) {
    return { to: '/getting-started', label: 'Check access status' } as const
  }
  if (canOfferRegistration(status, capabilitiesReady, isLocalPreview())) {
    return { to: '/sign-up', label: 'Create account' } as const
  }
  return { to: '/sign-in', label: 'Sign in' } as const
}
