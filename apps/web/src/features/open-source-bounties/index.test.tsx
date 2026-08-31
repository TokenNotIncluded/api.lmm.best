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

import { BountyCard } from './index'
import type { BountyProject } from './types'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

// A description long enough that a three-line clamp would truncate it.
const longDescription = [
  'Line one of a deliberately long bounty description.',
  'Line two keeps going to force wrapping.',
  'Line three would still not be the end, so clamping at three lines would hide content.',
  'Line four confirms the full text stays visible when clamping is removed.',
].join(' ')

const project: BountyProject = {
  id: 1,
  owner_user_id: 10,
  owner_username: 'publisher',
  repository_url: 'https://github.com/LIghtJUNction/api.lmm.best',
  title: 'Fix favicon regressions',
  description: longDescription,
  rules: 'Acceptance rules',
  reward_quota: 1000,
  net_reward_quota: 900,
  reward_slots: 1,
  escrow_quota: 1000,
  platform_fee_rate_bps: 100,
  platform_fee_quota: 100,
  status: 'published',
  created_at: 0,
  updated_at: 0,
  published_at: 0,
  closed_at: 0,
  archived_at: 0,
  active_challenge_count: 0,
  approved_challenge_count: 0,
  owner_rating_average: 0,
  owner_rating_count: 0,
  owner_thank_heart_count: 0,
}

function renderCard() {
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <BountyCard
        project={project}
        rank={1}
        viewerUserId={999}
        pending=''
        onAccept={() => {}}
        onSubmit={() => {}}
      />
    </I18nextProvider>
  )
}

describe('bounty project card description', () => {
  test('renders the full description without three-line truncation', () => {
    const markup = renderCard()

    assert.ok(markup.includes(longDescription))
    assert.doesNotMatch(markup, /line-clamp-3/)
  })
})
