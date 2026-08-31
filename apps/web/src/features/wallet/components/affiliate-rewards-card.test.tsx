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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import type { UserWalletData } from '../types'
import { AffiliateRewardsCard } from './affiliate-rewards-card'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const user: UserWalletData = {
  id: 1,
  username: 'tester',
  quota: 0,
  used_quota: 0,
  request_count: 0,
  aff_quota: 0,
  aff_history_quota: 0,
  aff_count: 0,
  group: 'default',
}

function renderCard() {
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <AffiliateRewardsCard
        user={user}
        affiliateLink='https://api.lmm.best/sign-up?ref=abc'
        onTransfer={() => {}}
      />
    </I18nextProvider>
  )
}

describe('AffiliateRewardsCard referral description', () => {
  test('renders the referral explanation without single-line truncation', () => {
    const markup = renderCard()

    assert.match(
      markup,
      /Earn rewards when users join through your referral link/
    )
    assert.doesNotMatch(markup, /line-clamp-1/)
  })
})
