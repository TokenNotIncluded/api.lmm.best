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
import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  Box,
  Bug,
  Compass,
  CreditCard,
  FileText,
  Image as ImageIcon,
  Key,
  LayoutDashboard,
  LifeBuoy,
  ListChecks,
  ListTodo,
  Medal,
  MessageSquare,
  PhoneCall,
  Radio,
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
            {
              title: t('Challenges'),
              url: '/challenges',
              icon: Trophy,
            },
            {
              title: t('Model Square'),
              url: '/pricing',
              icon: Box,
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
        id: 'forge',
        title: t('Open-source bounties'),
        items: [
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
          {
            title: t('Challenges'),
            url: '/challenges',
            icon: Trophy,
          },
          {
            title: t('Rankings'),
            url: '/rankings',
            icon: Medal,
          },
        ],
      },
      {
        id: 'chat',
        title: t('Conversations'),
        items: [
          {
            title: t('Conversation records'),
            url: '/chat-management',
            icon: MessageSquare,
          },
        ],
      },
      {
        id: 'general',
        title: t('General'),
        items: [
          {
            title: t('Overview'),
            url: '/dashboard/overview',
            icon: Activity,
          },
          {
            title: t('Dashboard'),
            url: '/dashboard/models',
            icon: LayoutDashboard,
          },
          {
            title: t('Model Square'),
            url: '/pricing',
            icon: Box,
          },
          {
            title: t('Status detection'),
            url: '/status',
            icon: Server,
          },
          {
            title: t('API Keys'),
            url: '/keys',
            icon: Key,
          },
          {
            title: t('Drawing studio'),
            url: '/drawing',
            icon: ImageIcon,
          },
          {
            title: t('Usage Logs'),
            url: '/usage-logs/common',
            icon: FileText,
          },
          {
            title: t('Task Logs'),
            url: '/usage-logs/task',
            activeUrls: ['/usage-logs/drawing'],
            configUrls: ['/usage-logs/drawing', '/usage-logs/task'],
            icon: ListTodo,
          },
        ],
      },
      {
        id: 'personal',
        title: t('Personal'),
        items: [
          {
            title: t('Wallet'),
            url: '/wallet',
            icon: Wallet,
          },
          {
            title: t('Temporary activations'),
            url: '/temporary-activations',
            icon: PhoneCall,
          },
          {
            title: t('Profile'),
            url: '/profile',
            icon: User,
          },
          {
            title: t('Submit a ticket'),
            url: '/support',
            icon: LifeBuoy,
          },
          {
            title: t('To-dos'),
            url: '/todos',
            icon: ListChecks,
            badge: todoBadge,
          },
        ],
      },
      {
        id: 'admin',
        title: t('Admin'),
        items: [
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
            title: t('Subscriptions'),
            url: '/subscriptions',
            icon: CreditCard,
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
          },
        ],
      },
    ],
  }
}
