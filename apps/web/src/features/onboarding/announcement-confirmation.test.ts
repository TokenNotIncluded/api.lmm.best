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

import { confirmVisibleAnnouncements } from './announcement-confirmation'

const notices = () =>
  Array.from({ length: 8 }, (_, index) => ({
    id: index + 1,
    revision: `r${index + 1}`,
    read_at: 0,
  }))

test('one confirmation saves all eight visible notices in server order', async () => {
  const snapshot = notices()
  const posted: number[] = []
  await confirmVisibleAnnouncements(snapshot, async (item) => {
    posted.push(item.id)
    return snapshot.map((notice) => ({
      ...notice,
      read_at: posted.includes(notice.id) ? 1 : 0,
    }))
  })
  assert.deepEqual(posted, [1, 2, 3, 4, 5, 6, 7, 8])
})

test('a failed acknowledgement stops the batch without posting later notices', async () => {
  const snapshot = notices()
  const posted: number[] = []
  await assert.rejects(
    confirmVisibleAnnouncements(snapshot, async (item) => {
      posted.push(item.id)
      if (item.id === 2) throw new Error('offline')
      return snapshot.map((notice) => ({
        ...notice,
        read_at: notice.id === 1 ? 1 : 0,
      }))
    }),
    /offline/
  )
  assert.deepEqual(posted, [1, 2])
})

test('new or edited announcements require another explicit confirmation', async () => {
  for (const unseen of [
    { id: 9, revision: 'new', read_at: 0 },
    { id: 2, revision: 'edited', read_at: 0 },
  ]) {
    const posted: number[] = []
    await confirmVisibleAnnouncements(notices(), async (item) => {
      posted.push(item.id)
      return [unseen, ...notices().slice(2)]
    })
    assert.deepEqual(posted, [1])
  }
})

test('already confirmed notices are not posted again', async () => {
  const snapshot = notices().map((item) => ({ ...item, read_at: 1 }))
  snapshot[7].read_at = 0
  const posted: number[] = []
  await confirmVisibleAnnouncements(snapshot, async (item) => {
    posted.push(item.id)
    return snapshot.map((notice) => ({ ...notice, read_at: 1 }))
  })
  assert.deepEqual(posted, [8])
})

test('a success response without acknowledgement cannot cause an endless loop', async () => {
  const snapshot = notices()
  let posts = 0
  await assert.rejects(
    confirmVisibleAnnouncements(snapshot, async () => {
      posts++
      return snapshot
    }),
    /Unable to confirm reading/
  )
  assert.equal(posts, 1)
})

test('an empty queue does not make a request', async () => {
  await confirmVisibleAnnouncements([], async () => {
    assert.fail('unexpected request')
  })
})

test('a retry resumes from the committed snapshot after a partial failure', async () => {
  let snapshot = notices()
  const posted: number[] = []
  let failOnce = true
  const acknowledge = async (item: (typeof snapshot)[number]) => {
    posted.push(item.id)
    if (item.id === 2 && failOnce) {
      failOnce = false
      throw new Error('offline')
    }
    snapshot = snapshot.map((notice) =>
      notice.id === item.id ? { ...notice, read_at: 1 } : notice
    )
    return snapshot
  }
  await assert.rejects(
    confirmVisibleAnnouncements(snapshot, acknowledge),
    /offline/
  )
  await confirmVisibleAnnouncements(snapshot, acknowledge)
  assert.deepEqual(posted, [1, 2, 2, 3, 4, 5, 6, 7, 8])
  assert.ok(snapshot.every((notice) => notice.read_at))
})

test('the next request follows the order in the committed server response', async () => {
  let snapshot = notices()
  const posted: number[] = []
  await confirmVisibleAnnouncements(snapshot, async (item) => {
    posted.push(item.id)
    snapshot = snapshot.map((notice) =>
      notice.id === item.id ? { ...notice, read_at: 1 } : notice
    )
    if (posted.length === 1) snapshot.reverse()
    return snapshot
  })
  assert.deepEqual(posted, [1, 8, 7, 6, 5, 4, 3, 2])
})
