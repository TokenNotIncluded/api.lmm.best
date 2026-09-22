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

import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useStatus } from '@/hooks/use-status'
import { useAuthStore } from '@/stores/auth-store'

import { CheckinCalendarCard } from './components/checkin-calendar-card'
import { GiftCard } from './components/gift-card'
import { LanguagePreferencesCard } from './components/language-preferences-card'
import { LoginSessionsCard } from './components/login-sessions-card'
import { PasskeyCard } from './components/passkey-card'
import { ProfileHeader } from './components/profile-header'
import { ProfileSecurityCard } from './components/profile-security-card'
import { ProfileSettingsCard } from './components/profile-settings-card'
import { SettlementCurrencyCard } from './components/settlement-currency-card'
import { SidebarModulesCard } from './components/sidebar-modules-card'
import { TwoFACard } from './components/two-fa-card'
import { useProfile } from './hooks'

interface ProfilePasskeyCapabilityProps {
  capabilitiesReady: boolean
  passkeyLogin: boolean
  loading: boolean
}

export function ProfilePasskeyCapability({
  capabilitiesReady,
  passkeyLogin,
  loading,
}: ProfilePasskeyCapabilityProps) {
  if (!capabilitiesReady || !passkeyLogin) return null
  return <PasskeyCard loading={loading} />
}

export function Profile() {
  const { t } = useTranslation()
  const { profile, loading, refreshProfile } = useProfile()
  const { status, capabilitiesReady } = useStatus()
  const permissions = useAuthStore((s) => s.auth.user?.permissions)
  const checkinEnabled = status?.checkin_enabled === true
  const turnstileEnabled = Boolean(
    status?.turnstile_check && status?.turnstile_site_key
  )
  const canConfigureSidebar = permissions?.sidebar_settings !== false

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Profile')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Tabs
          defaultValue='overview'
          className='console-page-tabs mx-auto max-w-6xl'
        >
          <TabsList aria-label={t('Profile')}>
            <TabsTrigger value='overview'>{t('Overview')}</TabsTrigger>
            <TabsTrigger value='account'>{t('Account')}</TabsTrigger>
            <TabsTrigger value='security'>{t('Security')}</TabsTrigger>
            <TabsTrigger value='preferences'>{t('Preferences')}</TabsTrigger>
            <TabsTrigger value='rewards'>{t('Rewards')}</TabsTrigger>
          </TabsList>
          <TabsContent value='overview' keepMounted>
            <ProfileHeader profile={profile} loading={loading} />
          </TabsContent>
          <TabsContent value='account' keepMounted className='space-y-5'>
            <ProfileSettingsCard
              profile={profile}
              loading={loading}
              onProfileUpdate={refreshProfile}
            />
          </TabsContent>
          <TabsContent value='security' keepMounted className='space-y-5'>
            <div className='grid items-start gap-5 lg:grid-cols-2'>
              <ProfileSecurityCard profile={profile} loading={loading} />
              <div className='space-y-5'>
                <TwoFACard loading={loading} />
                <ProfilePasskeyCapability
                  capabilitiesReady={capabilitiesReady}
                  passkeyLogin={status?.passkey_login === true}
                  loading={loading}
                />
              </div>
            </div>
            <LoginSessionsCard />
          </TabsContent>
          <TabsContent value='preferences' keepMounted className='space-y-5'>
            <div className='grid items-start gap-5 lg:grid-cols-2'>
              <LanguagePreferencesCard
                profile={profile}
                onProfileUpdate={refreshProfile}
              />
              <SettlementCurrencyCard
                profile={profile}
                loading={loading}
                onProfileUpdate={refreshProfile}
              />
            </div>
            {canConfigureSidebar && <SidebarModulesCard />}
          </TabsContent>
          <TabsContent
            value='rewards'
            keepMounted
            className='grid items-start gap-5 lg:grid-cols-2'
          >
            <GiftCard />
            {checkinEnabled && (
              <CheckinCalendarCard
                checkinEnabled={checkinEnabled}
                turnstileEnabled={turnstileEnabled}
                turnstileSiteKey={status?.turnstile_site_key || ''}
              />
            )}
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
