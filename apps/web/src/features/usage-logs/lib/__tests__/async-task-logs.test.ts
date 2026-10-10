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
import { test } from 'node:test'

import {
  buildAsyncLogParams,
  canKeepPreviousLogData,
  getAsyncLogRefreshInterval,
  getDrawingVideoUrls,
  getTaskResultUrl,
  getTaskAudioClips,
  parseTaskRecords,
  safeMediaUrl,
  summarizeAsyncLogs,
} from '../async-task-logs'

const base = {
  logCategory: 'task' as const,
  isAdmin: false,
  page: 1,
  pageSize: 20,
}

test('async history and ID searches have no hidden today-only filter', () => {
  assert.deepEqual(buildAsyncLogParams({ ...base, searchParams: {} }), {
    p: 1,
    page_size: 20,
  })
  assert.deepEqual(
    buildAsyncLogParams({ ...base, searchParams: { filter: ' old-job ' } }),
    {
      p: 1,
      page_size: 20,
      task_id: 'old-job',
    }
  )
})

test('task filters reach the API without changing camelCase actions', () => {
  assert.deepEqual(
    buildAsyncLogParams({
      ...base,
      isAdmin: true,
      searchParams: {
        status: ' success ',
        action: 'textGenerate',
        platform: 'sora',
        channel: '8',
      },
    }),
    {
      p: 1,
      page_size: 20,
      status: 'SUCCESS',
      action: 'textGenerate',
      platform: 'sora',
      channel_id: '8',
    }
  )
})

test('self queries never include administrator channel filters', () => {
  for (const logCategory of ['task', 'drawing'] as const) {
    const params = buildAsyncLogParams({
      ...base,
      logCategory,
      searchParams: { channel: '8' },
    })
    assert.equal(params.channel_id, undefined)
  }
})

test('drawing filters use mj_id and ignore a stale task platform', () => {
  const params = buildAsyncLogParams({
    ...base,
    logCategory: 'drawing',
    searchParams: {
      filter: 'mj-1',
      status: 'IN_PROGRESS',
      action: 'VIDEO',
      platform: 'suno',
    },
  })
  assert.deepEqual(params, {
    p: 1,
    page_size: 20,
    mj_id: 'mj-1',
    status: 'IN_PROGRESS',
    action: 'VIDEO',
  })
})

test('drawing timestamps are milliseconds and task timestamps are seconds', () => {
  const searchParams = { startTime: 1791590400123, endTime: 1791676800999 }
  const task = buildAsyncLogParams({ ...base, searchParams })
  const drawing = buildAsyncLogParams({
    ...base,
    logCategory: 'drawing',
    searchParams,
  })
  assert.equal(task.start_timestamp, 1791590400)
  assert.equal(task.end_timestamp, 1791676800)
  assert.equal(drawing.start_timestamp, 1791590400123)
  assert.equal(drawing.end_timestamp, 1791676800999)
})

test('zero and open time boundaries are preserved', () => {
  const params = buildAsyncLogParams({
    ...base,
    searchParams: { startTime: 0 },
  })
  assert.equal(params.start_timestamp, 0)
  assert.equal(params.end_timestamp, undefined)
})

test('invalid timestamps are omitted and reversed ranges are rejected', () => {
  assert.deepEqual(
    buildAsyncLogParams({
      ...base,
      searchParams: {
        startTime: Number.NaN,
        endTime: Number.POSITIVE_INFINITY,
      },
    }),
    { p: 1, page_size: 20 }
  )
  assert.throws(
    () =>
      buildAsyncLogParams({
        ...base,
        searchParams: {
          startTime: 2000,
          endTime: 1000,
        },
      }),
    RangeError
  )
})

test('summary counts only the supplied page and does not infer success from 100%', () => {
  assert.deepEqual(
    summarizeAsyncLogs([
      { status: 'QUEUED' },
      { status: 'SUBMITTED' },
      { status: 'NOT_START' },
      { status: 'IN_PROGRESS' },
      { status: 'SUCCESS' },
      { status: 'FAILURE', progress: '100%' },
      { status: 'MODAL' },
      { status: 'UNKNOWN' },
    ]),
    { total: 8, queued: 3, running: 1, success: 1, failed: 1, other: 2 }
  )
})

test('polling follows active states rather than a progress string', () => {
  for (const status of ['QUEUED', 'SUBMITTED', 'NOT_START', 'IN_PROGRESS']) {
    assert.equal(
      getAsyncLogRefreshInterval('task', [{ status, progress: '100%' }], true),
      5000
    )
  }
  for (const status of ['SUCCESS', 'FAILURE', 'MODAL', 'UNKNOWN', '']) {
    assert.equal(
      getAsyncLogRefreshInterval(
        'drawing',
        [{ status, progress: '20%' }],
        true
      ),
      false
    )
  }
})

test('polling stops when disabled, empty, in error, or viewing common logs', () => {
  const rows = [{ status: 'IN_PROGRESS' }]
  assert.equal(getAsyncLogRefreshInterval('task', rows, false), false)
  assert.equal(getAsyncLogRefreshInterval('task', rows, true, true), false)
  assert.equal(getAsyncLogRefreshInterval('common', rows, true), false)
  assert.equal(getAsyncLogRefreshInterval('task', [], true), false)
})

test('pagination placeholders cannot cross category or administrator scope', () => {
  const previous = ['logs', 'task', true, 1]
  assert.equal(canKeepPreviousLogData(previous, 'task', true), true)
  assert.equal(canKeepPreviousLogData(previous, 'task', false), false)
  assert.equal(canKeepPreviousLogData(previous, 'drawing', true), false)
  assert.equal(canKeepPreviousLogData(undefined, 'task', false), false)
})

test('result_url is used first and legacy successful URLs remain usable', () => {
  assert.equal(
    getTaskResultUrl({
      status: 'SUCCESS',
      result_url: 'https://media.example/result.mp4',
      fail_reason: 'https://media.example/old.mp4',
    }),
    'https://media.example/result.mp4'
  )
  assert.equal(
    getTaskResultUrl({
      status: 'SUCCESS',
      fail_reason: 'https://media.example/old.mp4',
    }),
    'https://media.example/old.mp4'
  )
  assert.equal(
    getTaskResultUrl({
      status: 'FAILURE',
      result_url: 'https://media.example/result.mp4',
    }),
    undefined
  )
  assert.equal(
    getTaskResultUrl({ status: 'SUCCESS', fail_reason: 'Provider error' }),
    undefined
  )
})

test('media links reject executable schemes, credentials and ambiguous paths', () => {
  for (const url of [
    'javascript:alert(1)',
    'data:text/html,hello',
    'file:///tmp/a',
    '//other.example/a',
    '/\\other.example/a',
    'https://user:pass@example.com/a',
    'https://exam\nple.com/a',
    'not a url',
  ]) {
    assert.equal(safeMediaUrl(url), undefined, url)
  }
  assert.equal(safeMediaUrl('/mj/image/user/task'), '/mj/image/user/task')
  assert.equal(
    safeMediaUrl('https://media.example/a?token=signed'),
    'https://media.example/a?token=signed'
  )
})

test('drawing video results accept stored JSON arrays and remove duplicates', () => {
  assert.deepEqual(
    getDrawingVideoUrls({
      video_url: 'https://media.example/a.mp4',
      video_urls:
        '["https://media.example/a.mp4","https://media.example/b.mp4","javascript:alert(1)"]',
    }),
    ['https://media.example/a.mp4', 'https://media.example/b.mp4']
  )
  assert.deepEqual(getDrawingVideoUrls({ video_urls: 'broken JSON' }), [])
  assert.deepEqual(
    getDrawingVideoUrls({ video_urls: { url: 'https://media.example/a.mp4' } }),
    []
  )
})

test('task data accepts current JSON objects, arrays and legacy JSON strings', () => {
  const clip = { audio_url: 'https://media.example/audio.mp3' }
  for (const data of [
    clip,
    [clip],
    JSON.stringify(clip),
    JSON.stringify([clip]),
  ]) {
    assert.deepEqual(parseTaskRecords(data), [clip])
  }
  for (const data of [null, 3, 'invalid', '[null,1,"x"]']) {
    assert.deepEqual(parseTaskRecords(data), [])
  }
})

test('audio results accept JSON objects and discard unsafe or malformed fields', () => {
  assert.deepEqual(
    getTaskAudioClips({
      id: 'clip',
      audio_url: 'https://media.example/audio.mp3',
      title: { invalid: true },
      image_url: 'javascript:alert(1)',
      metadata: { duration: 42, tags: 'instrumental' },
    }),
    [
      {
        id: 'clip',
        clip_id: undefined,
        title: undefined,
        tags: 'instrumental',
        duration: 42,
        audio_url: 'https://media.example/audio.mp3',
        image_url: undefined,
        image_large_url: undefined,
      },
    ]
  )
  assert.equal(
    getTaskAudioClips(
      JSON.stringify([{ audio_url: 'https://media.example/clip.mp3' }])
    ).length,
    1
  )
  assert.deepEqual(
    getTaskAudioClips([
      { audio_url: 'javascript:alert(1)' },
      { audio_url: 'https://user:password@media.example/clip.mp3' },
      { audio_url: 'http://media.example/clip.mp3' },
    ]),
    []
  )
})
