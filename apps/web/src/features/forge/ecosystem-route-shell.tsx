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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { AuthenticatedLayout } from '@/components/layout/components/authenticated-layout'
import { isConsoleActivated } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

import { ForgePublicShell } from './forge-public-shell'

type EcosystemRouteShellProps = {
  children?: React.ReactNode
  console?: React.ReactNode
  public?: React.ReactNode
}

export function EcosystemRouteShell(props: EcosystemRouteShellProps) {
  const user = useAuthStore((state) => state.auth.user)

  if (isConsoleActivated(user)) {
    return (
      <AuthenticatedLayout>
        <div className='flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden'>
          {props.console ?? props.children}
        </div>
      </AuthenticatedLayout>
    )
  }

  return <ForgePublicShell>{props.public ?? props.children}</ForgePublicShell>
}
