/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  createFileRoute,
  Outlet,
  redirect,
  useRouterState,
} from '@tanstack/react-router'

import { AuthenticatedLayout } from '@/components/layout'
import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import {
  isConsoleActivated,
  isContributorRoute,
} from '@/lib/console-activation'
import { isPublicDirectoryPath } from '@/lib/public-directory-route'
import { useAuthStore } from '@/stores/auth-store'

function DirectoryAwareLayout() {
  const pathname = useRouterState({
    select: (state) => state.location.pathname,
  })

  if (isPublicDirectoryPath(pathname)) {
    return (
      <ForgePublicShell>
        <div className='mx-auto w-full max-w-7xl py-6 sm:py-10'>
          <Outlet />
        </div>
      </ForgePublicShell>
    )
  }

  return <AuthenticatedLayout />
}

export const Route = createFileRoute('/_authenticated')({
  beforeLoad: ({ location }) => {
    // Keep the existing route ID, but expose only the directory itself.
    // All other routes and directory descendants retain the original guards.
    if (isPublicDirectoryPath(location.pathname)) return

    const { auth } = useAuthStore.getState()

    if (!auth.user || !auth.accessToken) {
      throw redirect({
        to: '/sign-in',
        search: { redirect: location.href },
      })
    }

    if (
      !isConsoleActivated(auth.user) &&
      !isContributorRoute(location.pathname)
    ) {
      throw redirect({ to: '/getting-started' })
    }
  },
  component: DirectoryAwareLayout,
})
