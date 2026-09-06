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

// Keep enough copy to exercise the card's collapsed preview and accessible
// expand control without relying on browser line measurements.
const longDescription = Array.from(
  { length: 8 },
  (_, index) =>
    `Scope detail ${index + 1}: provide a focused change with verification evidence and a clear handoff for the next contributor.`
).join(' ')

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
  test('offers an accessible expand control for long descriptions', () => {
    const markup = renderCard()

    assert.ok(markup.includes(longDescription))
    assert.match(markup, /line-clamp-4/)
    assert.match(markup, /aria-expanded="false"/)
    assert.match(markup, /aria-controls="bounty-description-1"/)
    assert.match(markup, /Expand description/)
  })

  test('keeps short descriptions uncluttered', () => {
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <BountyCard
          project={{ ...project, id: 2, description: 'Short summary.' }}
          rank={1}
          viewerUserId={999}
          pending=''
          onAccept={() => {}}
          onSubmit={() => {}}
        />
      </I18nextProvider>
    )

    assert.ok(markup.includes('Short summary.'))
    assert.doesNotMatch(markup, /Expand description/)
    assert.doesNotMatch(markup, /line-clamp-4/)
  })
})
