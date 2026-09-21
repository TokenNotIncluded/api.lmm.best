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

import {
  type BountyDraftValidationInput,
  calculateBountyCharge,
  parseBountyNumericInput,
  validateBountyDraft,
  validateBountySubmissionLinks,
} from '../validation'

const VALID_DRAFT: BountyDraftValidationInput = {
  repositoryUrl: 'https://github.com/TokenNotIncluded/api.lmm.best',
  title: 'Fix missing translations in the bounty workflow',
  description:
    'Find and correct reproducible untranslated or incorrectly translated interface text.',
  rules:
    'Link a valid Issue and focused pull request, update every supported locale, and include passing verification.',
  rewardAmount: 50,
  rewardSlots: 1,
}

describe('bounty draft validation', () => {
  test('accepts a complete draft with a canonical GitHub repository URL', () => {
    assert.deepEqual(validateBountyDraft(VALID_DRAFT), {})
  })

  test('accepts manually entered numeric text while keeping empty inputs invalid', () => {
    assert.deepEqual(
      validateBountyDraft({
        ...VALID_DRAFT,
        rewardAmount: '0.25',
        rewardSlots: '2',
      }),
      {}
    )

    const emptyNumericErrors = validateBountyDraft({
      ...VALID_DRAFT,
      rewardAmount: '',
      rewardSlots: '',
    })
    assert.equal(
      emptyNumericErrors.rewardAmount,
      'Reward per fix must be greater than zero.'
    )
    assert.equal(
      emptyNumericErrors.rewardSlots,
      'Reward slots must be a whole number between 1 and 100.'
    )

    assert.deepEqual(
      validateBountyDraft({
        ...VALID_DRAFT,
        rewardAmount: ' 0.25 ',
        rewardSlots: ' 2 ',
      }),
      {}
    )

    for (const value of ['   ', 'not-a-number']) {
      const malformedNumericErrors = validateBountyDraft({
        ...VALID_DRAFT,
        rewardAmount: value,
        rewardSlots: value,
      })
      assert.equal(
        malformedNumericErrors.rewardAmount,
        'Reward per fix must be greater than zero.'
      )
      assert.equal(
        malformedNumericErrors.rewardSlots,
        'Reward slots must be a whole number between 1 and 100.'
      )
    }
  })

  test('reports every invalid field with a specific message', () => {
    const errors = validateBountyDraft({
      repositoryUrl: 'http://example.com/not-github',
      title: 'bug',
      description: 'too short',
      rules: 'too short',
      rewardAmount: 0,
      rewardSlots: 1.5,
    })

    assert.deepEqual(Object.keys(errors), [
      'repositoryUrl',
      'title',
      'description',
      'rules',
      'rewardAmount',
      'rewardSlots',
    ])
    for (const message of Object.values(errors)) {
      assert.ok(message)
      assert.equal(message.endsWith('.'), true)
    }
  })

  test('rejects repository pages and reward slot values outside the server limits', () => {
    assert.equal(
      validateBountyDraft({
        ...VALID_DRAFT,
        repositoryUrl:
          'https://github.com/TokenNotIncluded/api.lmm.best/issues/15',
      }).repositoryUrl,
      'Enter a GitHub repository URL in the format https://github.com/owner/repository.'
    )
    assert.equal(
      validateBountyDraft({ ...VALID_DRAFT, rewardSlots: 101 }).rewardSlots,
      'Reward slots must be a whole number between 1 and 100.'
    )
  })
})

describe('bounty publication charge', () => {
  test('keeps invalid in-progress numeric inputs out of the charge preview', () => {
    const reward = parseBountyNumericInput(' ')
    const slots = parseBountyNumericInput('not-a-number')

    assert.deepEqual(calculateBountyCharge(reward, slots, 250), {
      gross: 0,
      netReward: 0,
      escrow: 0,
      platformFee: 0,
      feeRatePercent: 2.5,
      total: 0,
    })
  })

  test('deducts the public fee from each listed reward instead of adding it', () => {
    assert.deepEqual(calculateBountyCharge(5_000_000, 20, 100), {
      gross: 100_000_000,
      netReward: 4_950_000,
      escrow: 99_000_000,
      platformFee: 1_000_000,
      feeRatePercent: 1,
      total: 100_000_000,
    })
  })

  test('rounds each slot fee up to the smallest quota unit', () => {
    assert.deepEqual(calculateBountyCharge(333, 3, 250), {
      gross: 999,
      netReward: 324,
      escrow: 972,
      platformFee: 27,
      feeRatePercent: 2.5,
      total: 999,
    })
  })
})

describe('bounty completion evidence validation', () => {
  test('accepts either an Issue link, a pull request link, or both', () => {
    assert.equal(
      validateBountySubmissionLinks({
        issueUrl: 'https://github.com/example/project/issues/1',
        pullRequestUrl: '',
      }),
      undefined
    )
    assert.equal(
      validateBountySubmissionLinks({
        issueUrl: '',
        pullRequestUrl: 'https://github.com/example/project/pull/2',
      }),
      undefined
    )
    assert.equal(
      validateBountySubmissionLinks({
        issueUrl: 'https://github.com/example/project/issues/1',
        pullRequestUrl: 'https://github.com/example/project/pull/2',
      }),
      undefined
    )
  })

  test('requires at least one completion link', () => {
    assert.equal(
      validateBountySubmissionLinks({ issueUrl: '  ', pullRequestUrl: '' }),
      'Provide at least one GitHub Issue or pull request URL.'
    )
  })
})
