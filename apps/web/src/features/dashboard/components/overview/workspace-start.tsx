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
import { useTranslation } from 'react-i18next'

import { getWorkspaceCopy } from '@/components/layout/lib/workspace-copy'
import { selectWorkspaceShortcuts } from '@/components/layout/lib/workspace-navigation'
import { useModelPlaza } from '@/context/model-plaza-provider'
import { useSearch } from '@/context/search-provider'
import { useSidebarView } from '@/hooks/use-sidebar-view'
import { useAuthStore } from '@/stores/auth-store'

import { WorkspaceLaunchpad } from './workspace-launchpad'

export function WorkspaceStart() {
  const { i18n } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const { navGroups } = useSidebarView()
  const { setOpen } = useSearch()
  const { openPanel } = useModelPlaza()
  return (
    <WorkspaceLaunchpad
      name={user?.display_name || user?.username}
      copy={getWorkspaceCopy(i18n.resolvedLanguage || i18n.language)}
      shortcuts={selectWorkspaceShortcuts(navGroups, user?.role)}
      onSearch={() => setOpen(true)}
      onModelPanel={openPanel}
    />
  )
}
