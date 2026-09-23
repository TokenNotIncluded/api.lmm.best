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
import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  Box,
  Building2,
  Bug,
  Compass,
  CreditCard,
  FileText,
  Globe2,
  Gift as GiftIcon,
  Image as ImageIcon,
  Key,
  LayoutDashboard,
  LifeBuoy,
  ListChecks,
  ListTodo,
  Medal,
  MessageSquare,
  MonitorCog,
  PhoneCall,
  Radio,
  RotateCcw,
  Server,
  ServerCog,
  Settings,
  Ticket,
  Trophy,
  User,
  Users,
  Wallet,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import type { SidebarData } from '@/components/layout/types'
import { getTodos } from '@/features/todos/api'
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
import { hasPermission } from '@/lib/admin-permissions'
import { isConsoleActivated } from '@/lib/console-activation'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

/**
 * Root navigation groups for the application sidebar.
 *
 * These are shown when the URL does not match any nested sidebar view
 * registered in `layout/lib/sidebar-view-registry.ts`.
 */
export function useSidebarData(): SidebarData {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const consoleActivated = isConsoleActivated(user)
  const todosQuery = useQuery({
    queryKey: ['todos', 'all'],
    queryFn: () => getTodos('all'),
    enabled: Boolean(user) && consoleActivated,
    staleTime: 30_000,
    retry: false,
  })
  const todoBadge =
    (todosQuery.data?.total_unread_count ?? 0) > 0
      ? String(todosQuery.data?.total_unread_count)
      : undefined

  if (!consoleActivated) {
    return {
      navGroups: [
        {
          id: 'onboarding',
          title: t('Getting started'),
          items: [
            {
              title: t('Getting started'),
              url: '/getting-started',
              icon: Compass,
            },
            { title: t('Tool market'), url: '/tool-market', icon: Box },
            {
              title: t('Challenges'),
              url: '/challenges',
              icon: Trophy,
            },
            {
              title: t('Models and pricing'),
              url: '/pricing',
              icon: Box,
              interaction: 'model-panel',
            },
            {
              title: t('Status detection'),
              url: '/status',
              icon: Server,
            },
          ],
        },
      ],
    }
  }

  return {
    navGroups: [
      {
        id: 'general',
        title: t('Use AI'),
        items: [
          {
            title: t('Getting started'),
            url: '/getting-started',
            icon: Compass,
          },
          {
            title: t('Models and pricing'),
            url: '/pricing',
            icon: Box,
            interaction: 'model-panel',
          },
          {
            title: t('Conversation records'),
            url: '/chat-management',
            icon: MessageSquare,
          },
          { title: t('Drawing studio'), url: '/drawing', icon: ImageIcon },
          { title: t('Overview'), url: '/dashboard/overview', icon: Activity },
          {
            title: t('Dashboard'),
            url: '/dashboard/models',
            icon: LayoutDashboard,
          },
        ],
      },
      {
        id: 'developer',
        title: t('Developers'),
        items: [
          { title: t('Integration guide'), url: '/developers', icon: FileText },
          { title: t('API Keys'), url: '/keys', icon: Key },
          { title: t('Client setup'), url: '/guide', icon: Compass },
          { title: t('Usage Logs'), url: '/usage-logs/common', icon: FileText },
          {
            title: t('Task Logs'),
            url: '/usage-logs/task',
            activeUrls: ['/usage-logs/drawing'],
            configUrls: ['/usage-logs/drawing', '/usage-logs/task'],
            icon: ListTodo,
          },
          { title: t('Status detection'), url: '/status', icon: Server },
          {
            title: t('Remote control'),
            url: '/remote-control',
            icon: MonitorCog,
          },
        ],
      },
      {
        id: 'forge',
        title: t('Ecosystem'),
        items: [
          { title: t('AI directory'), url: '/ai-directory', icon: Globe2 },
          {
            title: t('Open-source bounties'),
            url: '/open-source-bounties',
            icon: Bug,
          },
          {
            title: t('Channel marketplace'),
            url: '/public-relay',
            icon: Radio,
          },
          { title: t('Tool market'), url: '/tool-market', icon: Box },
          { title: t('Scripts'), url: '/scripts', icon: FileText },
          { title: t('Challenges'), url: '/challenges', icon: Trophy },
          { title: t('Rankings'), url: '/rankings', icon: Medal },
        ],
      },
      {
        id: 'personal',
        title: t('Account'),
        items: [
          { title: t('Wallet'), url: '/wallet', icon: Wallet },
          { title: t('Company'), url: '/company', icon: Building2 },
          { title: t('Profile'), url: '/profile', icon: User },
          { title: t('Submit a ticket'), url: '/support', icon: LifeBuoy },
          {
            title: t('To-dos'),
            url: '/todos',
            icon: ListChecks,
            badge: todoBadge,
          },
        ],
      },
      {
        id: 'services',
        title: t('Other services'),
        items: [
          {
            title: t('Temporary activations'),
            url: '/temporary-activations',
            icon: PhoneCall,
          },
        ],
      },
      {
        id: 'admin',
        title: t('Admin'),
        items: [
          ...(user &&
          user.role >= ROLE.ADMIN &&
          hasPermission(user, 'acquisition', 'read')
            ? [
                {
                  title: t('Operations analytics'),
                  icon: Users,
                  items: [
                    {
                      title: t('User acquisition'),
                      url: '/operations/sources',
                    },
                  ],
                },
              ]
            : []),
          {
            title: t('Channels'),
            url: '/channels',
            icon: Radio,
          },
          {
            title: t('Models'),
            url: '/models/metadata',
            icon: Box,
          },
          {
            title: t('Users'),
            url: '/users',
            icon: Users,
          },
          {
            title: t('Redemption Codes'),
            url: '/redemption-codes',
            icon: Ticket,
          },
          {
            title: t('Discount Codes'),
            url: '/discount-codes',
            icon: Ticket,
          },
          {
            title: t('Red Packets'),
            url: '/red-packets',
            icon: GiftIcon,
          },
          {
            title: t('Subscriptions'),
            url: '/subscriptions',
            icon: CreditCard,
          },
          {
            title: t('Subscription reset'),
            url: '/subscriptions/reset',
            icon: RotateCcw,
            requiredRole: ROLE.SUPER_ADMIN,
          },
          {
            title: t('System Info'),
            url: '/system-info',
            icon: ServerCog,
            requiredRole: ROLE.SUPER_ADMIN,
          },
          {
            title: t('System Settings'),
            url: '/system-settings/site',
            activeUrls: ['/system-settings'],
            icon: Settings,
            requiredRole: ROLE.SUPER_ADMIN,
          },
        ],
      },
    ],
  }
}
