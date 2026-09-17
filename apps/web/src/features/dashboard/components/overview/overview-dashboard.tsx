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
import { ROLE } from '@/lib/roles'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { useDashboardContentVisibility } from '../../hooks/use-status-data'
import { AnnouncementsPanel } from './announcements-panel'
import { ApiInfoPanel } from './api-info-panel'
import { FAQPanel } from './faq-panel'
import { PerformanceHealthPanel } from './performance-health-panel'
import { SummaryCards } from './summary-cards'
import { UptimePanel } from './uptime-panel'
import { WorkspaceStart } from './workspace-start'

export function OverviewDashboard() {
  const user = useAuthStore((state) => state.auth.user)
  const {
    apiInfo: showApiInfoPanel,
    announcements: showAnnouncementsPanel,
    faq: showFAQPanel,
    uptimeKuma: showUptimePanel,
  } = useDashboardContentVisibility()
  const isAdmin = Boolean(user?.role && user.role >= ROLE.ADMIN)
  const showLeftContentPanels =
    isAdmin || showApiInfoPanel || showAnnouncementsPanel || showFAQPanel
  const showContentPanels = showLeftContentPanels || showUptimePanel

  return (
    <div className='dashboard-editorial workspace-overview'>
      <WorkspaceStart />
      <div className='workspace-metrics'>
        <SummaryCards />
      </div>
      {showContentPanels && (
        <div
          className={cn(
            'workspace-resource-grid',
            showLeftContentPanels &&
              showUptimePanel &&
              'workspace-resource-grid-split'
          )}
        >
          {showLeftContentPanels && (
            <div className='workspace-main-panels'>
              {isAdmin && (
                <div className='workspace-panel workspace-panel-wide'>
                  <PerformanceHealthPanel />
                </div>
              )}
              {showApiInfoPanel && (
                <div className='workspace-panel'>
                  <ApiInfoPanel />
                </div>
              )}
              {showAnnouncementsPanel && (
                <div className='workspace-panel'>
                  <AnnouncementsPanel />
                </div>
              )}
              {showFAQPanel && (
                <div className='workspace-panel'>
                  <FAQPanel />
                </div>
              )}
            </div>
          )}
          {showUptimePanel && (
            <div className='workspace-panel'>
              <UptimePanel />
            </div>
          )}
        </div>
      )}
    </div>
  )
}
