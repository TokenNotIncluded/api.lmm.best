/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { createFileRoute, redirect } from '@tanstack/react-router'

import { getAuthenticatedLandingRoute } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

// Old bookmarks remain usable, but the Getting Started page no longer exists.
export const Route = createFileRoute('/_authenticated/getting-started/')({
  beforeLoad: () => {
    throw redirect({
      href: getAuthenticatedLandingRoute(useAuthStore.getState().auth.user),
      replace: true,
    })
  },
})
