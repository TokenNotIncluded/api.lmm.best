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
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  changedGroupRatioOptions,
  type GroupRatioOptionValues,
} from '../group-ratio-option-values'
import {
  formatGroupRatioValues,
  mergeGroupRatioDraft,
  normalizeGroupRatioValues,
} from '../group-ratio-save-state'

const baseline: GroupRatioOptionValues = {
  GroupRatio: '{"default":1}',
  TopupGroupRatio: '{}',
  UserUsableGroups: '{"default":"Default"}',
  GroupGroupRatio: '{}',
  AutoGroups: '["default"]',
  MaxTokenAutoGroups: 4,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
  GroupWarnings: '{}',
}

test('equal props and JSON formatting are not pricing changes', () => {
  assert.deepEqual(
    changedGroupRatioOptions(
      normalizeGroupRatioValues(formatGroupRatioValues({ ...baseline })),
      normalizeGroupRatioValues(baseline)
    ),
    {}
  )
})

test('a settings refresh preserves edits and updates untouched fields', () => {
  const draft = { ...baseline, GroupRatio: '{"default":2}' }
  const incoming = { ...baseline, TopupGroupRatio: '{"default":0.148}' }
  assert.deepEqual(
    normalizeGroupRatioValues(mergeGroupRatioDraft(draft, baseline, incoming)),
    { ...incoming, GroupRatio: draft.GroupRatio }
  )
})

test('save acknowledgement preserves newer edits', () => {
  const submitted = { ...baseline, GroupRatio: '{"default":2}' }
  const draft = { ...submitted, GroupRatio: '{"default":3}' }
  const merged = normalizeGroupRatioValues(
    mergeGroupRatioDraft(draft, submitted, submitted)
  )
  assert.deepEqual(changedGroupRatioOptions(merged, submitted), {
    GroupRatio: '{"default":3}',
  })
  assert.deepEqual(
    changedGroupRatioOptions(
      normalizeGroupRatioValues(
        mergeGroupRatioDraft(submitted, submitted, submitted)
      ),
      submitted
    ),
    {}
  )
})

test('reverting a field while saving remains a new unsaved edit', () => {
  const submitted = { ...baseline, GroupRatio: '{"default":2}' }
  const merged = normalizeGroupRatioValues(
    mergeGroupRatioDraft(baseline, submitted, submitted)
  )
  assert.equal(merged.GroupRatio, baseline.GroupRatio)
  assert.deepEqual(changedGroupRatioOptions(merged, submitted), {
    GroupRatio: baseline.GroupRatio,
  })
})

test('invalid intermediate JSON is preserved', () => {
  const draft = { ...baseline, GroupWarnings: '{"free":' }
  const incoming = {
    ...baseline,
    GroupWarnings: '{"default":{"enabled":false}}',
  }
  const merged = mergeGroupRatioDraft(draft, baseline, incoming)
  assert.equal(merged.GroupWarnings, draft.GroupWarnings)
})

test('numeric and boolean edits remain correctly typed and dirty', () => {
  const draft = {
    ...baseline,
    MaxTokenAutoGroups: 8,
    DefaultUseAutoGroup: true,
  }
  const merged = normalizeGroupRatioValues(
    mergeGroupRatioDraft(draft, baseline, {
      ...baseline,
      GroupRatio: '{"default":2}',
    })
  )
  assert.equal(merged.MaxTokenAutoGroups, 8)
  assert.equal(merged.DefaultUseAutoGroup, true)
  assert.equal(merged.GroupRatio, '{"default":2}')
})

test('reconciliation does not mutate draft or server snapshots', () => {
  const draft = Object.freeze({ ...baseline, AutoGroups: '["default","fast"]' })
  const incoming = Object.freeze({ ...baseline, MaxTokenAutoGroups: 6 })
  const original = Object.freeze({ ...baseline })
  const merged = mergeGroupRatioDraft(draft, original, incoming)
  assert.notEqual(merged, draft)
  assert.notEqual(merged, incoming)
  assert.equal(merged.AutoGroups, draft.AutoGroups)
  assert.equal(merged.MaxTokenAutoGroups, incoming.MaxTokenAutoGroups)
})
