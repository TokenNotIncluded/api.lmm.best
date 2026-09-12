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
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { useState, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import type { NotificationTab } from '@/components/notification-popover'
import {
  listBountyNotifications,
  markBountyNotificationsRead,
  thankBountyTip,
} from '@/features/open-source-bounties/api'
import type { BountyNotification } from '@/features/open-source-bounties/types'
import {
  listRatioNotifications,
  ratioAnnouncement,
} from '@/features/ratio-notifications/api'
import { useStatus } from '@/hooks/use-status'
import { getNotice } from '@/lib/api'
import { getBackendCapabilities } from '@/lib/backend-capabilities'
import { isConsoleActivated } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'
import { useNotificationStore } from '@/stores/notification-store'

function hashString(input: string): string {
  let hash = 0
  if (!input) return '0'

  for (let i = 0; i < input.length; i += 1) {
    const chr = input.charCodeAt(i)
    hash = (hash << 5) - hash + chr
    hash |= 0
  }

  return hash.toString(36)
}

/**
 * Generate a unique key for an announcement
 * Prefer backend id, fall back to a content hash so edits register
 */
function getAnnouncementKey(item: Record<string, unknown>): string {
  if (!item) return ''

  if (item.id !== undefined && item.id !== null) {
    return `id:${item.id}`
  }

  const fingerprint = JSON.stringify({
    publishDate: (item?.publishDate as string) || '',
    content: ((item?.content as string) || '').trim(),
    extra: ((item?.extra as string) || '').trim(),
    type: (item?.type as string) || '',
    title: ((item?.title as string) || '').trim(),
    link: ((item?.link as string) || '').trim(),
  })
  return `hash:${hashString(fingerprint)}`
}

/**
 * Hook to manage notifications (Notice + Announcements)
 * Provides unread counts and read status management
 */
export function useNotifications() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const authUser = useAuthStore((state) => state.auth.user)
  const userId = authUser?.id ?? 0
  const [popoverOpen, setPopoverOpen] = useState(false)
  const [activeTab, setActiveTab] = useState<NotificationTab>('notice')
  const [thankingTipId, setThankingTipId] = useState(0)

  // Fetch Notice from API
  const {
    data: noticeResponse,
    isLoading: noticeLoading,
    refetch: refetchNotice,
  } = useQuery({
    queryKey: ['notice'],
    queryFn: getNotice,
    staleTime: 1000 * 60 * 5, // 5 minutes
  })

  // Fetch Announcements from status
  const { status, loading: statusLoading, capabilitiesReady } = useStatus()
  const backendCapabilities = getBackendCapabilities(status)
  const announcementsEnabled = status?.announcements_enabled ?? false
  const statusAnnouncements = status?.announcements
  const ratioFeed = useInfiniteQuery({
    queryKey: ['ratio-notifications', userId, authUser?.group],
    queryFn: ({ pageParam }) => listRatioNotifications(pageParam),
    initialPageParam: '',
    getNextPageParam: (page) => page.next,
    enabled: userId > 0,
    staleTime: 60_000,
    refetchInterval: userId > 0 ? 60_000 : false,
    retry: false,
  })
  const announcements = useMemo<Record<string, unknown>[]>(
    () => [
      ...(userId > 0
        ? (ratioFeed.data?.pages ?? []).flatMap((page) =>
            page.events.map((event) => ratioAnnouncement(event, userId, t))
          )
        : []),
      ...(announcementsEnabled
        ? ((statusAnnouncements || []) as Record<string, unknown>[]).slice(
            0,
            20
          )
        : []),
    ],
    [announcementsEnabled, statusAnnouncements, ratioFeed.data, userId, t]
  )
  const bountyNotificationsEnabled =
    userId > 0 &&
    isConsoleActivated(authUser) &&
    capabilitiesReady &&
    backendCapabilities.bounty_notifications
  const bountyNotificationsQueryKey = [
    'open-source-bounties',
    'notifications',
    'unified',
    userId,
  ] as const
  const { data: bountyNotifications = [], isLoading: bountyLoading } = useQuery(
    {
      queryKey: bountyNotificationsQueryKey,
      queryFn: listBountyNotifications,
      enabled: bountyNotificationsEnabled,
      staleTime: 30_000,
      refetchInterval: bountyNotificationsEnabled ? 30_000 : false,
    }
  )

  // Notification store
  const {
    lastReadNotice,
    markNoticeRead,
    markAnnouncementsRead,
    readAnnouncementKeys,
  } = useNotificationStore()

  // Extract notice content
  const noticeContent = noticeResponse?.success
    ? (noticeResponse.data || '').trim()
    : ''

  // Calculate unread counts
  const unreadCounts = useMemo(() => {
    const noticeUnread =
      noticeContent && noticeContent !== lastReadNotice ? 1 : 0

    const announcementsUnread = announcements.filter(
      (item: Record<string, unknown>) => {
        const key = getAnnouncementKey(item)
        return !readAnnouncementKeys.includes(key)
      }
    ).length
    const bountyUnread = bountyNotifications.filter(
      (item) => item.recipient_read_at === 0
    ).length

    return {
      notice: noticeUnread,
      announcements: announcementsUnread,
      bountyNotifications: bountyUnread,
      total: noticeUnread + announcementsUnread + bountyUnread,
    }
  }, [
    noticeContent,
    lastReadNotice,
    announcements,
    readAnnouncementKeys,
    bountyNotifications,
  ])

  const markAnnouncementsAsRead = () => {
    if (announcements.length > 0) {
      const allKeys = announcements.map((item: Record<string, unknown>) =>
        getAnnouncementKey(item)
      )
      markAnnouncementsRead(allKeys)
    }
  }

  // Handle popover open
  const markBountyNotificationsAsRead = () => {
    if (!bountyNotificationsEnabled || unreadCounts.bountyNotifications === 0) {
      return
    }
    const readAt = Math.floor(Date.now() / 1000)
    queryClient.setQueryData<BountyNotification[]>(
      bountyNotificationsQueryKey,
      (items = []) =>
        items.map((item) =>
          item.recipient_read_at > 0
            ? item
            : { ...item, recipient_read_at: readAt }
        )
    )
    void markBountyNotificationsRead().catch(() => {
      void queryClient.invalidateQueries({
        queryKey: bountyNotificationsQueryKey,
      })
    })
  }

  const handleOpenPopover = (tab?: NotificationTab) => {
    const nextTab = tab || activeTab

    // Mark currently visible content as read when opening the notification center
    if (noticeContent) {
      markNoticeRead(noticeContent)
    }
    if (nextTab === 'announcements') {
      markAnnouncementsAsRead()
    }
    if (nextTab === 'bounty-tips') {
      markBountyNotificationsAsRead()
    }

    setActiveTab(nextTab)
    setPopoverOpen(true)
  }

  const handlePopoverOpenChange = (open: boolean) => {
    if (open) {
      handleOpenPopover(activeTab)
      return
    }

    setPopoverOpen(false)
  }

  // Handle tab change - mark announcements as read when switching to that tab
  const handleTabChange = (tab: NotificationTab) => {
    setActiveTab(tab)

    if (tab === 'announcements') {
      markAnnouncementsAsRead()
    }
    if (tab === 'bounty-tips') {
      markBountyNotificationsAsRead()
    }
  }

  const handleThankTip = async (tipId: number) => {
    if (!bountyNotificationsEnabled) return

    setThankingTipId(tipId)
    try {
      const updated = await thankBountyTip(tipId)
      queryClient.setQueryData<BountyNotification[]>(
        bountyNotificationsQueryKey,
        (items = []) =>
          items.map((item) =>
            item.id === tipId ? { ...item, ...updated } : item
          )
      )
      await queryClient.invalidateQueries({
        queryKey: ['open-source-bounties'],
      })
      toast.success(t('Thanks sent'))
    } catch {
      toast.error(t('Unable to send thanks'))
    } finally {
      setThankingTipId(0)
    }
  }

  return {
    // Data
    notice: noticeContent,
    announcements,
    bountyTips: bountyNotifications,
    ratioFeed:
      userId > 0
        ? {
            loading: ratioFeed.isFetching,
            error: ratioFeed.isError,
            hasMore: ratioFeed.hasNextPage,
            loadMore: () => {
              void ratioFeed.fetchNextPage()
            },
            retry: () => {
              void ratioFeed.refetch()
            },
          }
        : undefined,
    loading:
      noticeLoading ||
      statusLoading ||
      (bountyNotificationsEnabled && bountyLoading),

    // Unread counts
    unreadCount: unreadCounts.total,
    unreadNoticeCount: unreadCounts.notice,
    unreadAnnouncementsCount: unreadCounts.announcements,

    // Popover state
    popoverOpen,
    setPopoverOpen: handlePopoverOpenChange,
    activeTab,
    setActiveTab: handleTabChange,
    thankingTipId,

    // Actions
    openPopover: handleOpenPopover,
    closePopover: () => setPopoverOpen(false),
    refetchNotice,
    thankTip: handleThankTip,
  }
}
