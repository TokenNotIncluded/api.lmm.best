/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { getBountyKind } from './types'
import {
  formatBountyDeadline, isBountyDeliveryUrl, isBountyRecruitmentOpen,
  parseBountyDeadline, validateBountyDraft, validateBountySubmissionLinks,
} from './validation'

const draft = {
  kind: 'general' as const,
  publisherType: 'company' as const,
  deadlineAt: '', repositoryUrl: '', title: 'Create a product guide',
  description: 'Write an accessible product guide with working examples.',
  rules: 'Deliver an editable document covering every agreed section.',
  rewardAmount: 100, rewardSlots: 2,
}
const submission = { issueUrl: '', pullRequestUrl: '', deliveryUrl: '', submissionNote: '' }

test('general drafts do not need a GitHub repository; legacy drafts still do', () => {
  assert.deepEqual(validateBountyDraft(draft, { rawCredits: true }), {})
  assert.ok(validateBountyDraft({ ...draft, kind: 'open_source' }).repositoryUrl)
  assert.ok(validateBountyDraft({ ...draft, repositoryUrl: 'https://github.com/a/b' }).repositoryUrl)
  assert.equal(getBountyKind(undefined), 'open_source')
  assert.equal(getBountyKind({ kind: 'general' }), 'general')
})

test('Unicode text limits count complete characters, not UTF-16 units', () => {
  assert.ok(validateBountyDraft({ ...draft, title: '🐟🐟' }).title)
  assert.deepEqual(validateBountyDraft({ ...draft, title: '🐟'.repeat(120), description: '字'.repeat(2000), rules: '字'.repeat(5000) }), {})
  assert.ok(validateBountyDraft({ ...draft, title: '🐟'.repeat(121) }).title)
})

test('deadline uses local time, rejects impossible dates, and permits no cutoff', () => {
  assert.equal(parseBountyDeadline(''), 0)
  assert.ok(Number.isNaN(parseBountyDeadline('2099-02-31T12:00')))
  assert.ok(Number.isNaN(parseBountyDeadline('not a date')))
  const seconds = parseBountyDeadline('2099-12-01T12:30')
  assert.equal(formatBountyDeadline(seconds), '2099-12-01T12:30')
  assert.equal(isBountyRecruitmentOpen({}), true)
  assert.equal(isBountyRecruitmentOpen({ deadline_at: 100 }, 100), false)
  assert.equal(isBountyRecruitmentOpen({ deadline_at: 101 }, 100), true)
  for (const deadline of [-1, NaN, Infinity, 253402300800, 100.5]) {
    assert.equal(isBountyRecruitmentOpen({ deadline_at: deadline }, 100), false)
  }
})

test('expired drafts cannot publish, but published text edits stay available', () => {
  const expired = { ...draft, deadlineAt: '2000-01-01T00:00' }
  assert.ok(validateBountyDraft(expired).deadlineAt)
  assert.equal(validateBountyDraft(expired, { contentOnly: true }).deadlineAt, undefined)
})

test('general delivery accepts an HTTPS link or a substantive completion note', () => {
  assert.equal(validateBountySubmissionLinks({ ...submission, deliveryUrl: 'https://example.com/artifact' }, { kind: 'general' }), undefined)
  assert.equal(validateBountySubmissionLinks({ ...submission, submissionNote: '字'.repeat(20) }, { kind: 'general' }), undefined)
  assert.ok(validateBountySubmissionLinks(submission, { kind: 'general' }))
  assert.ok(validateBountySubmissionLinks({ ...submission, submissionNote: '字'.repeat(19) }, { kind: 'general' }))
  assert.ok(validateBountySubmissionLinks({ ...submission, submissionNote: '字'.repeat(2001) }, { kind: 'general' }))
  assert.ok(validateBountySubmissionLinks({ ...submission, deliveryUrl: 'https://example.com', issueUrl: 'https://github.com/a/b/issues/1' }, { kind: 'general' }))
  assert.ok(validateBountySubmissionLinks({ ...submission, submissionNote: '字'.repeat(20) }, { kind: 'open_source' }))
})

test('delivery links reject active protocols, credentials and oversized encoded data', () => {
  for (const url of ['javascript:alert(1)', 'data:text/html,test', 'http://example.com',
    'https://user:password@example.com', 'https://example.com\\@evil.test',
    'https://example.com/\nfile', 'https://example.com:99999/',
    'https://example.com/' + '字'.repeat(400)]) {
    assert.equal(isBountyDeliveryUrl(url), false, url)
    assert.ok(validateBountySubmissionLinks({ ...submission, deliveryUrl: url, submissionNote: '字'.repeat(20) }, { kind: 'general' }))
  }
  assert.equal(isBountyDeliveryUrl('https://example.com/a%20b'), true)
})
